package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jaysqvl/Jotist/internal/models"
)

// GetRunResources returns numeric telemetry without reading checkpoint files.
// @Summary Get run resource measurements
// @Description Return invocation measurements and stage attempt timing, settings and resource usage
// @Tags transcription
// @Produce json
// @Param id path string true "Recording ID"
// @Param run_id path string true "Execution ID"
// @Success 200 {object} map[string]interface{}
// @Failure 404 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/transcription/{id}/runs/{run_id}/resources [get]
// @Security ApiKeyAuth
// @Security BearerAuth
func (h *Handler) GetRunResources(c *gin.Context) {
	run, err := h.jobRepo.FindExecution(c.Request.Context(), c.Param("id"), c.Param("run_id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Execution not found"})
		return
	}
	rows := []gin.H{}
	if run.RecoveryVersion > 0 && h.unifiedProcessor != nil {
		if store := h.unifiedProcessor.GetUnifiedService().RecoveryRepository(); store != nil {
			stages, err := store.ListStages(c.Request.Context(), run.ID)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not read run resource measurements"})
				return
			}
			for _, stage := range stages {
				attempts := make([]gin.H, 0, len(stage.Attempts))
				for _, attempt := range stage.Attempts {
					attempts = append(attempts, runResourceAttempt(attempt))
				}
				rows = append(rows, gin.H{"id": stage.ID, "kind": stage.Kind, "label": recoveryStageLabel(stage), "status": stage.State, "reused_from_execution_id": stage.ReusedFromExecutionID, "attempts": attempts})
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{"execution_id": run.ID, "available": len(rows) > 0 || len(run.ResourceMeasurements) > 0, "invocations": run.ResourceMeasurements, "stages": rows})
}

func runResourceAttempt(attempt models.RecoveryAttempt) gin.H {
	result := gin.H{
		"id": attempt.ID, "attempt_number": attempt.AttemptNumber, "status": attempt.State,
		"device": attempt.Device, "precision": attempt.Precision,
		"window_seconds": attempt.WindowSeconds, "plan_version": attempt.PlanVersion,
		"reason": attempt.Reason, "error_code": attempt.ErrorCode,
		"started_at": attempt.StartedAt, "completed_at": attempt.CompletedAt,
		"measurements": attempt.Measurements,
	}
	if attempt.BatchSize > 0 {
		result["batch_size"] = attempt.BatchSize
	}
	return result
}
