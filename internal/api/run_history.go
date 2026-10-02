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

// GetRunHistory lists paginated execution identities for Statistics.
// @Summary List run diagnostics history
// @Tags transcription
// @Produce json
// @Success 200 {object} models.RunHistory
// @Router /api/v1/transcription/statistics/runs [get]
// @Security ApiKeyAuth
// @Security BearerAuth
func (h *Handler) GetRunHistory(c *gin.Context) {
	days, dayErr := strconv.Atoi(c.DefaultQuery("days", "30"))
	page, pageErr := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, limitErr := strconv.Atoi(c.DefaultQuery("limit", "20"))
	status := c.DefaultQuery("status", "all")
	query, executionID := c.Query("q"), c.Query("run_id")
	if dayErr != nil || (days != 0 && days != 7 && days != 30 && days != 90) || pageErr != nil || page < 1 || page > 1000000 || limitErr != nil || limit < 1 || limit > 50 || len(query) > 200 || len(executionID) > 128 || (status != "all" && status != "completed" && status != "failed" && status != "active") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid run history filters"})
		return
	}
	provider, ok := h.jobRepo.(interface{ Database() *gorm.DB })
	if !ok || provider.Database() == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Run history is unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	result, err := repository.ReadRunHistory(ctx, provider.Database(), repository.RunHistoryFilter{Days: days, Page: page, Limit: limit, Query: query, Status: status, ExecutionID: executionID}, time.Now())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not read run history"})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, result)
}
