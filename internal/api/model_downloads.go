package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"

	"github.com/gin-gonic/gin"
	"github.com/jaysqvl/Jotist/internal/modeldownloads"
	"github.com/jaysqvl/Jotist/internal/modelstorage"
	"github.com/jaysqvl/Jotist/internal/repository"
	"github.com/jaysqvl/Jotist/internal/serverlock"
	"github.com/jaysqvl/Jotist/internal/transcription/adapters"
	"gorm.io/gorm"
)

func newModelDownloads(h *Handler, db *gorm.DB) *modeldownloads.Service {
	return modeldownloads.New(modeldownloads.Options{
		Store: repository.NewModelDownloadRepository(db), Fetcher: adapters.NewModelDownloader(h.config.WhisperXEnv),
		WorkersStopped: serverlock.PriorWorkersStopped,
		Token: func(ctx context.Context, owner uint) (string, error) {
			if h.userRepo == nil {
				return "", modeldownloads.ErrUnavailable
			}
			user, err := h.userRepo.FindByID(ctx, owner)
			if err != nil {
				return "", err
			}
			return user.HFToken, nil
		},
		Keep: func(ctx context.Context, sources []string) error {
			if h.modelRetention == nil {
				return modeldownloads.ErrUnavailable
			}
			inventory, err := h.modelRetention.Inventory(ctx, false)
			if err != nil {
				return err
			}
			for _, source := range sources {
				found := false
				for _, entry := range inventory.Entries {
					if entry.Source != source {
						continue
					}
					found = true
					if entry.KeepDownloaded {
						break
					}
					if err := h.modelRetention.SetKeepDownloaded(ctx, entry.ID, entry.Version, true); err != nil {
						return err
					}
					break
				}
				if !found {
					return modelstorage.ErrNotFound
				}
			}
			return nil
		},
	})
}

// StartModelDownloads starts only the file-download worker. No model weights or
// Python environment are touched until a signed-in owner queues a download.
func (h *Handler) StartModelDownloads() error {
	if h.modelDownloads == nil {
		return modeldownloads.ErrUnavailable
	}
	return h.modelDownloads.Start()
}
func (h *Handler) StopModelDownloads() {
	if h.modelDownloads != nil {
		h.modelDownloads.Close()
	}
}

type ModelDownloadCatalogResponse struct {
	Models []modeldownloads.Model `json:"models"`
}
type ModelDownloadListResponse struct {
	Downloads []modeldownloads.Job `json:"downloads"`
	Available bool                 `json:"available"`
	Reason    string               `json:"reason,omitempty"`
}
type DownloadModelRequest struct {
	ModelID        string `json:"model_id" binding:"required,len=32,hexadecimal"`
	KeepDownloaded bool   `json:"keep_downloaded"`
}

// GetModelDownloadCatalog lists the server's registered downloadable models.
// @Summary Browse models available for manual download
// @Tags settings
// @Produce json
// @Success 200 {object} ModelDownloadCatalogResponse
// @Router /api/v1/settings/models/catalog [get]
// @Security BearerAuth
func (h *Handler) GetModelDownloadCatalog(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if h.modelDownloads == nil {
		downloadError(c, modeldownloads.ErrUnavailable)
		return
	}
	c.JSON(http.StatusOK, ModelDownloadCatalogResponse{Models: h.modelDownloads.Catalog()})
}

// ListModelDownloads returns bounded, public file-download metadata.
// @Summary List model downloads and recent outcomes
// @Tags settings
// @Produce json
// @Success 200 {object} ModelDownloadListResponse
// @Router /api/v1/settings/models/downloads [get]
// @Security BearerAuth
func (h *Handler) ListModelDownloads(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if h.modelDownloads == nil {
		downloadError(c, modeldownloads.ErrUnavailable)
		return
	}
	jobs, err := h.modelDownloads.List(c.Request.Context())
	if err != nil {
		downloadError(c, err)
		return
	}
	for i := range jobs {
		jobs[i] = downloadForOwner(c, jobs[i])
	}
	result := ModelDownloadListResponse{Downloads: jobs, Available: h.modelDownloads.Available()}
	if !result.Available {
		result.Reason = "Model downloads are unavailable. Unfinished downloads stay protected until the server can safely resume them."
	}
	c.JSON(http.StatusOK, result)
}

// DownloadModel queues a curated weight download without starting a run.
// @Summary Download a model's files
// @Tags settings
// @Accept json
// @Produce json
// @Param download body DownloadModelRequest true "Registered model and keep preference"
// @Success 202 {object} modeldownloads.Job
// @Failure 409 {object} map[string]string
// @Router /api/v1/settings/models/downloads [post]
// @Security BearerAuth
func (h *Handler) DownloadModel(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if h.modelDownloads == nil {
		downloadError(c, modeldownloads.ErrUnavailable)
		return
	}
	owner, ok := c.Get("user_id")
	id, valid := owner.(uint)
	if !ok || !valid || id == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Sign in to download models."})
		return
	}
	var req DownloadModelRequest
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 4096))
	decoder.DisallowUnknownFields()
	err := decoder.Decode(&req)
	var trailing any
	if err != nil || !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(req.ModelID) || decoder.Decode(&trailing) != io.EOF {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Choose a model from the download catalog."})
		return
	}
	job, _, err := h.modelDownloads.Enqueue(c.Request.Context(), id, req.ModelID, req.KeepDownloaded)
	if err != nil {
		downloadError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, downloadForOwner(c, job))
}

// CancelModelDownload cancels only the requesting owner's model file download.
// @Summary Cancel a model download
// @Tags settings
// @Produce json
// @Param downloadID path string true "Download job ID"
// @Success 200 {object} modeldownloads.Job
// @Router /api/v1/settings/models/downloads/{downloadID}/cancel [post]
// @Security BearerAuth
func (h *Handler) CancelModelDownload(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if h.modelDownloads == nil {
		downloadError(c, modeldownloads.ErrUnavailable)
		return
	}
	owner, _ := c.Get("user_id")
	id, valid := owner.(uint)
	if !valid || id == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Sign in to manage model downloads."})
		return
	}
	job, err := h.modelDownloads.Cancel(c.Request.Context(), id, c.Param("downloadID"))
	if err != nil {
		downloadError(c, err)
		return
	}
	c.JSON(http.StatusOK, downloadForOwner(c, job))
}

func downloadForOwner(c *gin.Context, job modeldownloads.Job) modeldownloads.Job {
	owner, _ := c.Get("user_id")
	id, _ := owner.(uint)
	job.CanCancel = id != 0 && id == job.OwnerID && job.Active()
	return job
}

func downloadError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, modeldownloads.ErrUnknown):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, modeldownloads.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, modeldownloads.ErrFull), errors.Is(err, modeldownloads.ErrFinished):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Model downloads are unavailable. Refresh to check the current state."})
	}
}
