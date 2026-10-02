package api

import (
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/jaysqvl/Jotist/internal/models"
	"github.com/jaysqvl/Jotist/internal/transcription"
)

// Only new-run admission resolves shared preferences. Resume restores the saved
// parameters instead. The in-memory flag fences repeated upload validation.
func (h *Handler) resolveExecutionPolicy(c *gin.Context, params *models.WhisperXParams) error {
	if err := transcription.ValidateExecutionPolicyOptions(*params); err != nil {
		return err
	}
	if params.ExecutionPolicyResolved || params.ExecutionPolicySource == "" {
		return nil
	}
	if params.ExecutionPolicySource == "global" {
		policy := models.DefaultExecutionPolicy()
		if userID, ok := c.Get("user_id"); ok && h.userRepo != nil {
			id, valid := userID.(uint)
			if !valid {
				return fmt.Errorf("invalid user identity")
			}
			user, err := h.userRepo.FindByID(c.Request.Context(), id)
			if err != nil {
				return fmt.Errorf("failed to load execution defaults")
			}
			policy = user.EffectiveExecutionPolicy()
		}
		if err := transcription.ValidateExecutionPolicy(policy); err != nil {
			return err
		}
		params.ExecutionPolicy = &policy
		params.RecoveryMode = transcription.RecoveryFixed
		if policy.AutomaticRecovery {
			params.RecoveryMode = transcription.RecoveryStageManagement
			if policy.ReduceBatchSize {
				params.RecoveryMode = transcription.RecoveryBatchManagement
			}
		}
		// Shared defaults permit cleanup and qualified smaller batches only.
		// Per-stage fallback, precision and windows require an explicit override.
		params.AdaptivePolicy = nil
	}
	if !params.ExecutionPolicy.AutomaticRecovery {
		params.RecoveryMode = transcription.RecoveryFixed
	}
	if params.ReuseCheckpoints == nil {
		reuse := params.ExecutionPolicy.ReuseCheckpoints
		params.ReuseCheckpoints = &reuse
	}
	params.ExecutionPolicyResolved = true
	return nil
}
