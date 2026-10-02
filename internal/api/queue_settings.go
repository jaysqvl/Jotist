package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jaysqvl/Jotist/internal/models"
	"github.com/jaysqvl/Jotist/internal/queue"
)

type UpdateQueueSettingsRequest struct {
	Workers *int `json:"workers" binding:"required"`
}

// GetQueueSettings shows the shared server-wide recording concurrency limit.
// @Summary Read recording queue settings
// @Tags admin
// @Produce json
// @Success 200 {object} models.QueueSettingsResponse
// @Router /api/v1/admin/queue/settings [get]
// @Security BearerAuth
func (h *Handler) GetQueueSettings(c *gin.Context) {
	if h.taskQueue == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Queue settings are unavailable"})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, h.taskQueue.QueueSettings())
}

// UpdateQueueSettings persists a recording claim limit across all users.
// @Summary Set recording concurrency
// @Description Save a server-wide limit. Active executions finish normally when the limit is lowered. QUEUE_WORKERS is an explicit deployment override.
// @Tags admin
// @Accept json
// @Produce json
// @Param settings body UpdateQueueSettingsRequest true "Recording concurrency"
// @Success 200 {object} models.QueueSettingsResponse
// @Router /api/v1/admin/queue/settings [put]
// @Security BearerAuth
func (h *Handler) UpdateQueueSettings(c *gin.Context) {
	if h.taskQueue == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Queue settings are unavailable"})
		return
	}
	var req UpdateQueueSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Workers == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "A whole-number workers value is required"})
		return
	}
	if err := models.ValidateQueueWorkers(*req.Workers); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	if err := h.taskQueue.SetWorkers(ctx, *req.Workers); err != nil {
		switch {
		case errors.Is(err, queue.ErrWorkerOverride):
			c.JSON(http.StatusConflict, gin.H{"error": "Recording concurrency is controlled by the server's QUEUE_WORKERS setting"})
		case errors.Is(err, queue.ErrQueueSettingsUnavailable), errors.Is(err, context.Canceled):
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Queue settings are unavailable"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not save recording concurrency"})
		}
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, h.taskQueue.QueueSettings())
}
