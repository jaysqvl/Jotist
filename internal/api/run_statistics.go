package api

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"scriberr/internal/repository"
)

// GetRunStatistics serves retained run telemetry to authenticated clients.
// @Summary Get global run statistics
// @Tags transcription
// @Produce json
// @Param days query int false "Window: 0 (all), 7, 30 or 90 days" default(30)
// @Success 200 {object} models.RunStatistics
// @Router /api/v1/transcription/statistics [get]
// @Security ApiKeyAuth
// @Security BearerAuth
func (h *Handler) GetRunStatistics(c *gin.Context) {
	days, err := strconv.Atoi(c.DefaultQuery("days", "30"))
	if err != nil || (days != 0 && days != 7 && days != 30 && days != 90) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Choose 0, 7, 30 or 90 days"})
		return
	}
	provider, ok := h.jobRepo.(interface{ Database() *gorm.DB })
	if !ok || provider.Database() == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Run statistics are unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	result, err := repository.ReadRunStatistics(ctx, provider.Database(), days, time.Now())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not read run statistics"})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, result)
}
