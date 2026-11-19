package api

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type readinessResponse struct {
	Status string          `json:"status"`
	Checks readinessChecks `json:"checks"`
}

type readinessChecks struct {
	Database string `json:"database"`
	Queue    string `json:"queue"`
}

// ReadinessCheck checks service dependencies without changing jobs or loading models.
func (h *Handler) ReadinessCheck(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	checks := readinessChecks{Database: "unavailable", Queue: "unavailable"}
	if h.taskQueue != nil && h.taskQueue.Ready() {
		checks.Queue = "ready"
	}
	if provider, ok := h.jobRepo.(interface{ Database() *gorm.DB }); ok {
		if db := provider.Database(); db != nil {
			if connection, err := db.DB(); err == nil {
				ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
				defer cancel()
				if connection.PingContext(ctx) == nil {
					checks.Database = "ready"
				}
			}
		}
	}
	status, code := "not_ready", http.StatusServiceUnavailable
	if checks.Database == "ready" && checks.Queue == "ready" {
		status, code = "ready", http.StatusOK
	}
	c.JSON(code, readinessResponse{Status: status, Checks: checks})
}
