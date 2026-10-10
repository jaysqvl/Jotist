package api

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ReadinessCheck checks service dependencies without changing jobs or loading models.
// @Summary Check whether the service can accept transcription work
// @Description Read-only database and queue startup checks. Busy workers remain ready. Failure responses omit internal errors and private paths.
// @Tags health
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Failure 503 {object} map[string]interface{}
// @Router /ready [get]
func (h *Handler) ReadinessCheck(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	checks := gin.H{"database": "unavailable", "queue": "unavailable"}
	if h.taskQueue != nil && h.taskQueue.Ready() {
		checks["queue"] = "ready"
	}
	if provider, ok := h.jobRepo.(interface{ Database() *gorm.DB }); ok {
		if db := provider.Database(); db != nil {
			if connection, err := db.DB(); err == nil {
				ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
				defer cancel()
				if connection.PingContext(ctx) == nil {
					checks["database"] = "ready"
				}
			}
		}
	}
	status, code := "not_ready", http.StatusServiceUnavailable
	if checks["database"] == "ready" && checks["queue"] == "ready" {
		status, code = "ready", http.StatusOK
	}
	c.JSON(code, gin.H{"status": status, "checks": checks})
}
