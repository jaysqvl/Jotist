package api

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jaysqvl/Jotist/internal/repository"
	"gorm.io/gorm"
)

// GetQueueActivity is the authenticated, read-only view of all recording work.
func (h *Handler) GetQueueActivity(c *gin.Context) {
	provider, ok := h.jobRepo.(interface{ Database() *gorm.DB })
	if h.taskQueue == nil || !ok || provider.Database() == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Queue activity is unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	result, err := repository.ReadQueueActivity(ctx, provider.Database(), h.taskQueue.ActivitySnapshot())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not read queue activity"})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, result)
}
