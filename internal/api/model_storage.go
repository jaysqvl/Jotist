package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jaysqvl/Jotist/internal/config"
	"github.com/jaysqvl/Jotist/internal/modelstorage"
	"github.com/jaysqvl/Jotist/internal/repository"
	"github.com/jaysqvl/Jotist/internal/transcription/adapters"
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
	result, err := h.modelStorage.Inventory(ctx, c.Query("refresh") == "true")
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Could not inspect model storage. Try refreshing."})
		return
	}
	if c.Query("refresh") == "true" {
		modelstorage.ProcessGate().ActivityChanged()
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
