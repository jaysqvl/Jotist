package api

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jaysqvl/Jotist/internal/config"
	"github.com/jaysqvl/Jotist/internal/modelstorage"
	"github.com/jaysqvl/Jotist/internal/repository"
	"github.com/jaysqvl/Jotist/internal/transcription/adapters"
	"github.com/jaysqvl/Jotist/pkg/logger"
	"gorm.io/gorm"
)

func newModelStorageManager(cfg *config.Config, db *gorm.DB) *modelstorage.Manager {
	catalog := adapters.ModelStorageCatalog()
	options := modelstorage.DefaultOptions(cfg.WhisperXEnv, catalog)
	for _, direct := range options.Direct {
		catalog = append(catalog, direct.Model)
	}
	options.Uses = func(ctx context.Context) ([]modelstorage.Use, error) { return repository.ReadModelStorageUses(ctx, db) }
	options.Details = func(ctx context.Context) (map[string]modelstorage.Details, error) {
		return repository.ReadModelStorageDetails(ctx, db, catalog)
	}
	options.AdditionalHubRoots = func(ctx context.Context) ([]string, error) { return repository.ReadModelStorageDirectories(ctx, db) }
	options.AdditionalDirect = func(ctx context.Context) ([]modelstorage.DirectFiles, error) {
		dirs, err := repository.ReadModelStorageDirectories(ctx, db)
		if err != nil {
			return nil, err
		}
		var result []modelstorage.DirectFiles
		for _, dir := range dirs {
			result = append(result, modelstorage.AlignmentFiles(dir)...)
		}
		return result, nil
	}
	return modelstorage.New(options)
}

// GetModelStorage lists downloaded model files without loading any model.
// @Summary Audit downloaded models and local cache storage
// @Tags settings
// @Produce json
// @Success 200 {object} modelstorage.Inventory
// @Router /api/v1/settings/models [get]
// @Security BearerAuth
func (h *Handler) GetModelStorage(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if h.modelStorage == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Model storage is unavailable."})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	var result modelstorage.Inventory
	var err error
	if h.modelRetention != nil {
		result, err = h.modelRetention.Inventory(ctx, c.Query("refresh") == "true")
	} else {
		result, err = h.modelStorage.Inventory(ctx, c.Query("refresh") == "true")
	}
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Could not inspect model storage. Try refreshing."})
		return
	}
	if c.Query("refresh") == "true" {
		modelstorage.ProcessGate().ActivityChanged()
	}
	c.JSON(http.StatusOK, result)
}

// StartModelCacheMaintenance owns one daily/event-driven background task. The
// server stops it before closing the database; handlers/tests do not spawn it.
func (h *Handler) StartModelCacheMaintenance() func() {
	if h.modelRetention == nil {
		return func() {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		h.modelRetention.Run(ctx, func(result modelstorage.CleanupResult, err error) {
			if ctx.Err() != nil {
				return
			}
			if err != nil || result.FailedModels > 0 {
				logger.Warn("Model cache cleanup could not finish", "removed_models", result.RemovedModels, "failed_models", result.FailedModels)
			} else if result.RemovedModels > 0 {
				logger.Info("Unused model files removed", "models", result.RemovedModels, "bytes", result.RemovedBytes)
			}
		})
	}()
	var once sync.Once
	return func() { once.Do(func() { cancel(); wg.Wait() }) }
}

type UpdateModelRetentionRequest struct {
	Enabled       *bool `json:"enabled" binding:"required"`
	RetentionDays *int  `json:"retention_days" binding:"required" minimum:"1" maximum:"365"`
}

// UpdateModelRetention saves the owner's server-wide model-cache policy.
// @Summary Set automatic model cache retention
// @Tags settings
// @Accept json
// @Produce json
// @Param policy body UpdateModelRetentionRequest true "Automatic cleanup policy"
// @Success 200 {object} modelstorage.CacheRetentionPolicy
// @Router /api/v1/settings/models/retention [put]
// @Security BearerAuth
func (h *Handler) UpdateModelRetention(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if h.modelRetention == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Model cache settings are unavailable."})
		return
	}
	var req UpdateModelRetentionRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil || req.RetentionDays == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Automatic cleanup and a whole-number retention period are required."})
		return
	}
	if err := modelstorage.ValidateRetentionDays(*req.RetentionDays); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	policy, err := h.modelRetention.SavePolicy(ctx, *req.Enabled, *req.RetentionDays)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Could not save the cache retention policy."})
		return
	}
	c.JSON(http.StatusOK, policy)
}

type UpdateModelKeepDownloadedRequest struct {
	KeepDownloaded *bool  `json:"keep_downloaded" binding:"required"`
	Version        string `json:"version" binding:"required,len=64" minLength:"64" maxLength:"64"`
}

type UpdateModelKeepDownloadedResponse struct {
	ID             string `json:"id"`
	KeepDownloaded bool   `json:"keep_downloaded"`
}

// UpdateModelKeepDownloaded keeps a selected model out of all cache removals.
// @Summary Keep a model downloaded or resume its retention policy
// @Tags settings
// @Accept json
// @Produce json
// @Param id path string true "Opaque cache entry ID"
// @Param preference body UpdateModelKeepDownloadedRequest true "Keep downloaded preference and current file version"
// @Success 200 {object} UpdateModelKeepDownloadedResponse
// @Failure 409 {object} map[string]string
// @Router /api/v1/settings/models/{id}/retention [put]
// @Security BearerAuth
func (h *Handler) UpdateModelKeepDownloaded(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if h.modelRetention == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Model cache settings are unavailable."})
		return
	}
	var req UpdateModelKeepDownloadedRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.KeepDownloaded == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Choose whether to keep this model downloaded and include its current file version."})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	err := h.modelRetention.SetKeepDownloaded(ctx, c.Param("id"), req.Version, *req.KeepDownloaded)
	if err != nil {
		status, message := http.StatusServiceUnavailable, "Could not confirm this preference. Refresh to check whether the model is kept."
		switch {
		case errors.Is(err, modelstorage.ErrChanged):
			status, message = http.StatusConflict, "These model files changed. Refresh before changing this preference."
		case errors.Is(err, modelstorage.ErrNotFound):
			status, message = http.StatusNotFound, "These model files are no longer cached. Refresh the inventory."
		case errors.Is(err, modelstorage.ErrReadOnly):
			status, message = http.StatusConflict, "Only managed model files without pending cleanup can be kept downloaded."
		}
		c.JSON(status, gin.H{"error": message})
		return
	}
	c.JSON(http.StatusOK, UpdateModelKeepDownloadedResponse{ID: c.Param("id"), KeepDownloaded: *req.KeepDownloaded})
}

// PreviewModelCleanup returns the exact eligible set to confirm before a clear.
// @Summary Preview model cache cleanup
// @Tags settings
// @Produce json
// @Param mode query string false "Cleanup scope" Enums(all, expired)
// @Success 200 {object} modelstorage.CleanupPlan
// @Router /api/v1/settings/models/cleanup [get]
// @Security BearerAuth
func (h *Handler) PreviewModelCleanup(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if h.modelRetention == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Model cache cleanup is unavailable."})
		return
	}
	mode := c.DefaultQuery("mode", "all")
	if mode != "all" && mode != "expired" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Choose all or expired model files."})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	plan, err := h.modelRetention.Preview(ctx, mode, true)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Could not prepare cache cleanup. Refresh and try again."})
		return
	}
	c.JSON(http.StatusOK, plan)
}

type CleanupModelCacheRequest struct {
	Mode    string `json:"mode" binding:"required,oneof=all expired" enums:"all,expired"`
	Version string `json:"version" binding:"required,len=64" minLength:"64" maxLength:"64"`
}

// CleanupModelCache rechecks the confirmed set and protects newly active models.
// @Summary Clear confirmed downloaded model files
// @Tags settings
// @Accept json
// @Produce json
// @Param confirmation body CleanupModelCacheRequest true "Confirmed cleanup version"
// @Success 200 {object} modelstorage.CleanupResult
// @Failure 409 {object} map[string]string
// @Router /api/v1/settings/models/cleanup [post]
// @Security BearerAuth
func (h *Handler) CleanupModelCache(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if h.modelRetention == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Model cache cleanup is unavailable."})
		return
	}
	var req CleanupModelCacheRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Review and confirm the current cache cleanup preview."})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Minute)
	defer cancel()
	result, err := h.modelRetention.Collect(ctx, req.Mode, req.Version)
	if err != nil {
		if errors.Is(err, modelstorage.ErrChanged) {
			c.JSON(http.StatusConflict, gin.H{"error": "The cache changed. Close this dialog and review a fresh preview."})
			return
		}
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Cleanup did not finish. Refresh to check which model files remain.", "result": result})
		return
	}
	c.JSON(http.StatusOK, result)
}

// ModelStorageEvents notifies the signed-in owner when model/run activity
// changes. Notifications carry no paths, parameters, recording data or secrets.
// @Summary Subscribe to model storage changes
// @Tags settings
// @Produce text/event-stream
// @Success 200 {string} string "Model storage invalidations"
// @Router /api/v1/settings/models/events [get]
// @Security BearerAuth
func (h *Handler) ModelStorageEvents(c *gin.Context) {
	if h.modelStorage == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Model storage is unavailable."})
		return
	}
	changes, unsubscribe := h.modelStorage.SubscribeChanges()
	defer unsubscribe()
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-store")
	c.Header("X-Accel-Buffering", "no")
	write := func(value string) bool {
		if _, err := c.Writer.WriteString(value); err != nil {
			return false
		}
		c.Writer.Flush()
		return true
	}
	if !write("data: {\"type\":\"connected\"}\n\n") {
		return
	}
	keepalive := time.NewTicker(30 * time.Second)
	defer keepalive.Stop()
	var shutdown <-chan struct{}
	if h.broadcaster != nil {
		shutdown = h.broadcaster.Done()
	}
	for {
		select {
		case <-shutdown:
			return
		case <-c.Request.Context().Done():
			return
		case <-changes:
			if !write("data: {\"type\":\"model_storage_changed\"}\n\n") {
				return
			}
		case <-keepalive.C:
			// A transport keepalive only; no database/filesystem work.
			if !write(": keepalive\n\n") {
				return
			}
		}
	}
}

type RemoveModelFilesRequest struct {
	Version string `json:"version" binding:"required,len=64"`
}

// RemoveModelFiles removes only the verified model's files, never profiles or runs.
// @Summary Remove a downloaded model
// @Tags settings
// @Accept json
// @Produce json
// @Param id path string true "Opaque cache entry ID"
// @Param request body RemoveModelFilesRequest true "Confirmed inventory version"
// @Success 200 {object} modelstorage.Removal
// @Router /api/v1/settings/models/{id} [delete]
// @Security BearerAuth
func (h *Handler) RemoveModelFiles(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if h.modelStorage == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Model storage is unavailable."})
		return
	}
	var req RemoveModelFilesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Refresh the inventory and confirm the model to remove."})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	result, err := h.modelStorage.Remove(ctx, c.Param("id"), req.Version)
	if err != nil {
		status, message := http.StatusInternalServerError, "Removal could not finish. Refresh to check for remaining files."
		switch {
		case errors.Is(err, modelstorage.ErrKept):
			status, message = http.StatusConflict, "Keep downloaded is on. Turn it off before removing this model."
		case errors.Is(err, modelstorage.ErrBusy):
			status, message = http.StatusConflict, "This model is now in use or downloading. No files were removed."
		case errors.Is(err, modelstorage.ErrChanged):
			status, message = http.StatusConflict, "These model files changed. Refresh and review the updated size before removing them."
		case errors.Is(err, modelstorage.ErrNotFound):
			status, message = http.StatusNotFound, "These model files are no longer cached."
		case errors.Is(err, modelstorage.ErrReadOnly):
			status, message = http.StatusConflict, "These files cannot be safely removed by Jotist."
		case errors.Is(err, modelstorage.ErrUnverified):
			status, message = http.StatusServiceUnavailable, "Run activity or profile references could not be verified. No files were removed."
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			status, message = http.StatusServiceUnavailable, "The storage request timed out. Refresh to check its current state."
		}
		c.JSON(status, gin.H{"error": message})
		return
	}
	c.JSON(http.StatusOK, result)
}
