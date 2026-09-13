package api

import (
	"fmt"

	"scriberr/internal/models"
	"scriberr/internal/repository"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Explicit settings are independent requests. A browser cannot authorize
// observations for a saved profile by copying an old run's private snapshot.
func clearClientLearningSnapshot(params *models.WhisperXParams) {
	if params.AdaptivePolicy == nil {
		return
	}
	policy := *params.AdaptivePolicy
	policy.ProfileID, policy.ProfileRevision, policy.LearningGeneration = "", 0, 0
	policy.SnapshotTaken, policy.LearnedPlans = false, nil
	params.AdaptivePolicy = &policy
}

// All authoritative saved-profile paths share one admission boundary. Resolve
// inherited context first, then fence the profile revision and freeze selected
// plan IDs. Later validation must preserve this already admitted snapshot.
func (h *Handler) admitSavedProfile(c *gin.Context, profile *models.TranscriptionProfile) (models.WhisperXParams, error) {
	params := profile.Parameters
	if err := h.resolveTranscriptionContext(c, &params); err != nil {
		return params, err
	}
	provider, ok := h.profileRepo.(interface{ Database() *gorm.DB })
	if !ok || provider.Database() == nil {
		return params, fmt.Errorf("Profile learning storage is unavailable")
	}
	policy, err := repository.NewAdaptiveLearningRepository(provider.Database()).SnapshotAtAdmission(c.Request.Context(), profile.ID, profile.Revision, params.AdaptivePolicy)
	if err != nil {
		return params, err
	}
	params.AdaptivePolicy = policy
	return params, nil
}
