package api

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/jaysqvl/Jotist/internal/models"
)

type SummaryTemplateRequest struct {
	Name               string  `json:"name" binding:"required,min=1"`
	Description        *string `json:"description" required:"false"`
	Model              string  `json:"model" binding:"required,min=1"`
	Prompt             string  `json:"prompt" binding:"required,min=1"`
	IncludeSpeakerInfo *bool   `json:"include_speaker_info" required:"false"`
}

type SummarySettingsRequest struct {
	DefaultModel string `json:"default_model" binding:"required,min=1"`
}

type SummarySettingsResponse struct {
	DefaultModel string `json:"default_model"`
}

// ListSummaryTemplates returns all templates
func (h *Handler) ListSummaryTemplates(c *gin.Context) {
	// TODO: Add pagination support
	items, _, err := h.summaryRepo.List(c.Request.Context(), 0, 1000)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch templates"})
		return
	}
	c.JSON(http.StatusOK, items)
}

// CreateSummaryTemplate creates a new template
func (h *Handler) CreateSummaryTemplate(c *gin.Context) {
	var req SummaryTemplateRequest
	if err := bindJSON(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	item := &models.SummaryTemplate{
		Name:        req.Name,
		Description: req.Description,
		Model:       req.Model,
		Prompt:      req.Prompt,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if req.IncludeSpeakerInfo != nil {
		item.IncludeSpeakerInfo = *req.IncludeSpeakerInfo
	}
	if err := h.summaryRepo.Create(c.Request.Context(), item); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create template"})
		return
	}
	c.JSON(http.StatusCreated, item)
}

// GetSummaryTemplate fetches one by id
func (h *Handler) GetSummaryTemplate(c *gin.Context) {
	id := c.Param("id")
	item, err := h.summaryRepo.FindByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Template not found"})
		return
	}
	c.JSON(http.StatusOK, item)
}

// UpdateSummaryTemplate updates an existing template
func (h *Handler) UpdateSummaryTemplate(c *gin.Context) {
	id := c.Param("id")
	var req SummaryTemplateRequest
	if err := bindJSON(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	item, err := h.summaryRepo.FindByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Template not found"})
		return
	}
	item.Name = req.Name
	item.Description = req.Description
	item.Model = req.Model
	item.Prompt = req.Prompt
	if req.IncludeSpeakerInfo != nil {
		item.IncludeSpeakerInfo = *req.IncludeSpeakerInfo
	}
	item.UpdatedAt = time.Now()
	if err := h.summaryRepo.Update(c.Request.Context(), item); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update template"})
		return
	}
	c.JSON(http.StatusOK, item)
}

// DeleteSummaryTemplate deletes a template
func (h *Handler) DeleteSummaryTemplate(c *gin.Context) {
	id := c.Param("id")
	if err := h.summaryRepo.Delete(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete template"})
		return
	}
	c.Status(http.StatusNoContent)
}

// GetSummarySettings returns the global summary settings (default model)
func (h *Handler) GetSummarySettings(c *gin.Context) {
	s, err := h.summaryRepo.GetSettings(c.Request.Context())
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusOK, SummarySettingsResponse{DefaultModel: ""})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch settings"})
		return
	}
	c.JSON(http.StatusOK, SummarySettingsResponse{DefaultModel: s.DefaultModel})
}

// SaveSummarySettings updates default model (creates row if absent)
func (h *Handler) SaveSummarySettings(c *gin.Context) {
	var req SummarySettingsRequest
	if err := bindJSON(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	s, err := h.summaryRepo.GetSettings(c.Request.Context())
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			s = &models.SummarySetting{
				DefaultModel: req.DefaultModel,
				UpdatedAt:    time.Now(),
			}
			if err := h.summaryRepo.SaveSettings(c.Request.Context(), s); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save settings"})
				return
			}
			c.JSON(http.StatusOK, SummarySettingsResponse{DefaultModel: s.DefaultModel})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save settings"})
		return
	}
	s.DefaultModel = req.DefaultModel
	s.UpdatedAt = time.Now()
	if err := h.summaryRepo.SaveSettings(c.Request.Context(), s); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save settings"})
		return
	}
	c.JSON(http.StatusOK, SummarySettingsResponse{DefaultModel: s.DefaultModel})
}
