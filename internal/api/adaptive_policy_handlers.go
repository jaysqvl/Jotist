package api

import (
	"errors"
	"net/http"

	"scriberr/internal/models"
	"scriberr/internal/repository"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func (h *Handler) adaptiveLearningStore(c *gin.Context) *repository.AdaptiveLearningRepository {
	provider, ok := h.profileRepo.(interface{ Database() *gorm.DB })
	if !ok || provider.Database() == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Profile learning storage is unavailable"})
		return nil
	}
	if _, err := h.profileRepo.FindByID(c.Request.Context(), c.Param("id")); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Profile not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not read profile"})
		}
		return nil
	}
	return repository.NewAdaptiveLearningRepository(provider.Database())
}

// GetAdaptivePolicy returns stored measurements and immutable profile history.
// @Summary Get profile learning state
// @Tags profiles
// @Produce json
// @Param id path string true "Profile ID"
// @Param scope_key query string false "Optional exact scope fingerprint"
// @Success 200 {object} repository.AdaptivePolicySnapshot
// @Router /api/v1/profiles/{id}/adaptive-policy [get]
// @Security ApiKeyAuth
// @Security BearerAuth
func (h *Handler) GetAdaptivePolicy(c *gin.Context) {
	store := h.adaptiveLearningStore(c)
	if store == nil {
		return
	}
	result, err := store.Snapshot(c.Request.Context(), c.Param("id"), c.Query("scope_key"))
	if err != nil {
		adaptiveActionError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

type adaptiveProfileAction struct {
	ExpectedRevision   int64  `json:"expected_revision"`
	ExpectedGeneration int64  `json:"expected_generation"`
	PlanID             string `json:"plan_id"`
	Revision           int64  `json:"revision"`
}

func (h *Handler) adaptiveProfileAction(c *gin.Context, action string) {
	var request adaptiveProfileAction
	if err := bindJSON(c, &request); err != nil || request.ExpectedRevision < 1 || request.ExpectedGeneration < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "A positive expected_revision and expected_generation are required"})
		return
	}
	if action == "freeze" && request.PlanID == "" || action == "restore" && request.Revision < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Select a stored plan or profile revision"})
		return
	}
	store := h.adaptiveLearningStore(c)
	if store == nil {
		return
	}
	var profile *models.TranscriptionProfile
	var err error
	switch action {
	case "reset":
		profile, err = store.Reset(c.Request.Context(), c.Param("id"), request.ExpectedRevision, request.ExpectedGeneration)
	case "freeze":
		profile, err = store.Freeze(c.Request.Context(), c.Param("id"), request.ExpectedRevision, request.ExpectedGeneration, request.PlanID)
	case "restore":
		profile, err = store.Restore(c.Request.Context(), c.Param("id"), request.ExpectedRevision, request.ExpectedGeneration, request.Revision)
	}
	if err != nil {
		adaptiveActionError(c, err)
		return
	}
	c.JSON(http.StatusOK, profile)
}

func adaptiveActionError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, repository.ErrAdaptiveConflict), errors.Is(err, repository.ErrAdaptiveEvidence):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "Profile or saved revision not found"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not update profile learning state"})
	}
}

// ResetAdaptivePolicy changes the generation without removing historical evidence.
// @Summary Reset profile learned starting plans
// @Tags profiles
// @Accept json
// @Produce json
// @Param id path string true "Profile ID"
// @Param request body adaptiveProfileAction true "Expected revision and generation"
// @Success 200 {object} models.TranscriptionProfile
// @Failure 409 {object} ErrorResponse
// @Router /api/v1/profiles/{id}/reset-adaptive [post]
// @Security ApiKeyAuth
// @Security BearerAuth
func (h *Handler) ResetAdaptivePolicy(c *gin.Context) { h.adaptiveProfileAction(c, "reset") }

// FreezeAdaptivePolicy saves a selected measured plan in a new fixed revision.
// @Summary Freeze a measured plan as fixed profile settings
// @Tags profiles
// @Accept json
// @Produce json
// @Param id path string true "Profile ID"
// @Param request body adaptiveProfileAction true "Selected plan ID and expected revision and generation"
// @Success 200 {object} models.TranscriptionProfile
// @Failure 409 {object} ErrorResponse
// @Router /api/v1/profiles/{id}/freeze-adaptive [post]
// @Security ApiKeyAuth
// @Security BearerAuth
func (h *Handler) FreezeAdaptivePolicy(c *gin.Context) { h.adaptiveProfileAction(c, "freeze") }

// RestoreProfileRevision creates a new revision from a saved fixed configuration.
// @Summary Restore saved profile parameters in a new revision
// @Tags profiles
// @Accept json
// @Produce json
// @Param id path string true "Profile ID"
// @Param request body adaptiveProfileAction true "Saved revision and expected current revision and generation"
// @Success 200 {object} models.TranscriptionProfile
// @Failure 409 {object} ErrorResponse
// @Router /api/v1/profiles/{id}/restore-revision [post]
// @Security ApiKeyAuth
// @Security BearerAuth
func (h *Handler) RestoreProfileRevision(c *gin.Context) { h.adaptiveProfileAction(c, "restore") }
