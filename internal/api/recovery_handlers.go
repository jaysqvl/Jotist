package api

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"scriberr/internal/models"
	"scriberr/internal/serverlock"
	"scriberr/internal/transcription"
	"scriberr/internal/transcription/interfaces"

	"github.com/gin-gonic/gin"
)

// GetRunRecovery exposes validated checkpoints and exact attempt history.
// @Summary Get execution recovery details
// @Description Return saved stages, attempts, partial text and same-plan resume availability
// @Tags transcription
// @Produce json
// @Param id path string true "Recording ID"
// @Param run_id path string true "Execution ID"
// @Success 200 {object} map[string]interface{}
// @Failure 404 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/transcription/{id}/runs/{run_id}/recovery [get]
// @Security ApiKeyAuth
// @Security BearerAuth
func (h *Handler) GetRunRecovery(c *gin.Context) {
	jobID, runID := c.Param("id"), c.Param("run_id")
	job, err := h.jobRepo.FindByID(c.Request.Context(), jobID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Recording not found"})
		return
	}
	run, err := h.jobRepo.FindExecution(c.Request.Context(), jobID, runID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Execution not found"})
		return
	}
	response := gin.H{"execution_id": run.ID, "mode": run.ActualParameters.RecoveryMode, "status": run.RecoveryState, "resumable": false, "partial_transcript_available": false, "stages": []interface{}{}, "learning": recoveryLearningSnapshot(run.ActualParameters.AdaptivePolicy)}
	if run.RecoveryVersion == 0 || h.unifiedProcessor == nil || h.unifiedProcessor.GetUnifiedService().RecoveryRepository() == nil {
		response["available"] = false
		response["resume_unavailable_reason"] = "This execution predates durable checkpoints."
		c.JSON(http.StatusOK, response)
		return
	}
	store := h.unifiedProcessor.GetUnifiedService().RecoveryRepository()
	response["available"] = true
	stages, err := store.ListStages(c.Request.Context(), run.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not read recovery stages"})
		return
	}
	resumable := run.RecoveryState == "failed" || run.RecoveryState == "interrupted" || run.RecoveryState == "blocked"
	reason := ""
	if err := transcription.ValidateSavedRecoveryPlan(*run); err != nil {
		resumable = false
		reason = err.Error()
	}
	if run.ActualParameters.EffectiveHFTokenSource() == "custom" && (job.Parameters.HfToken == nil || *job.Parameters.HfToken == "") && !cloudASRWithoutDiarization(run.ActualParameters) {
		resumable = false
		reason = "The saved custom model credential is unavailable. A new submission can use your current default token."
	}
	if run.CancelledAt != nil {
		resumable = false
		reason = "Cancelled executions cannot resume. Start a new run."
	}
	if run.DeadlineAt != nil && !time.Now().Before(*run.DeadlineAt) {
		resumable = false
		reason = "The saved deadline has expired. Start a new run."
	}
	if !serverlock.PriorWorkersStopped() {
		resumable = false
		reason = "Previous worker termination is unverified. Restart the container before resuming."
	}
	retained := map[string]recoveryPartialOutput{}
	rows := make([]gin.H, 0, len(stages))
	for _, stage := range stages {
		rows = append(rows, gin.H{"id": stage.ID, "kind": stage.Kind, "label": recoveryStageLabel(stage), "status": stage.State, "recoverable_boundary": stage.RecoverableBoundary, "checkpoint_id": stage.CheckpointID, "reused_from_execution_id": stage.ReusedFromExecutionID, "attempts": stage.Attempts})
		if stage.State == models.RecoveryRunning || stage.State == "waiting_for_resource" {
			resumable = false
			reason = "A stage is still active."
		}
		if stage.CheckpointID == nil && stage.AttemptNumber >= 7 {
			resumable = false
			reason = "The saved stage attempt budget is exhausted. Start a new run."
		}
		if stage.CheckpointID != nil {
			_, data, err := store.SelectedCheckpoint(c.Request.Context(), stage.ID)
			if err != nil {
				resumable = false
				reason = "A selected checkpoint is missing or corrupt. Start a fresh run to recompute it."
				continue
			}
			if rank := recoveryTranscriptRank(stage.Kind); stage.State == models.RecoverySucceeded && rank > 0 {
				var result interfaces.TranscriptResult
				if json.Unmarshal(data, &result) == nil {
					track := recoveryTrackGroup(stage.NodeKey)
					if prior, exists := retained[track]; !exists || rank > prior.rank {
						retained[track] = recoveryPartialOutput{rank: rank, result: result}
					}
				}
			}
		}
	}
	response["stages"] = rows
	response["resumable"] = resumable
	response["resume_unavailable_reason"] = reason
	response["partial_transcript_available"] = len(retained) > 0 && run.Status != models.StatusCompleted
	if len(retained) > 0 && run.Status != models.StatusCompleted {
		response["partial_transcript"] = mergeRecoveryPartialOutputs(retained)
	}
	c.JSON(http.StatusOK, response)
}

// Plans are copied from this execution's immutable admission snapshot. Reading
// recovery must never substitute a newly edited profile or newer learned plan.
func recoveryLearningSnapshot(policy *models.AdaptiveExecutionPolicy) gin.H {
	result := gin.H{"status": "disabled", "reason": "Learning was not enabled for this execution. Retained checkpoints are not new inference measurements.", "plans": []models.AdaptivePlanSnapshot{}}
	if policy == nil {
		return result
	}
	result["enabled"], result["snapshot_taken"] = policy.Learn, policy.SnapshotTaken
	result["profile_id"], result["profile_revision"], result["learning_generation"] = policy.ProfileID, policy.ProfileRevision, policy.LearningGeneration
	if !policy.Learn {
		return result
	}
	if !policy.SnapshotTaken {
		result["status"], result["reason"] = "not_snapshotted", "This execution has no saved profile learning snapshot. No learned starting plan is inferred."
	} else if len(policy.LearnedPlans) == 0 {
		result["status"], result["reason"] = "no_saved_plans", "No learned candidate plans were available when this execution was admitted. Only qualified measured attempts can inform future profile starts."
	} else {
		result["status"], result["reason"] = "snapshotted", "These learned candidate plans were saved at admission. Eligibility and available memory are checked before use; the recorded attempts show which settings actually ran."
		result["plans"] = policy.LearnedPlans
	}
	return result
}

func recoveryTrackGroup(node string) string {
	if !strings.HasPrefix(node, "track-") {
		return ""
	}
	track, _, _ := strings.Cut(node, ".")
	return track
}

func recoveryStageLabel(stage models.RecoveryStage) string {
	label := map[string]string{"recognize": "Recognition", "recognition": "Recognition", "align": "Timestamp alignment", "alignment": "Timestamp alignment", "diarize": "Speaker diarization", "diarization": "Speaker diarization", "combined": "Recognition and requested alignment (combined backend stage)", "assemble": "Assemble output"}[stage.Kind]
	if label == "" {
		label = strings.ReplaceAll(stage.Kind, "_", " ")
	}
	if recoveryTrackGroup(stage.NodeKey) != "" {
		label = "Track · " + label
	}
	return label
}

type recoveryPartialOutput struct {
	rank   int
	result interfaces.TranscriptResult
}

func recoveryTranscriptRank(kind string) int {
	switch kind {
	case "recognize", "recognition":
		return 1
	case "combined":
		return 2
	case "align", "alignment":
		return 3
	}
	return 0
}

func mergeRecoveryPartialOutputs(retained map[string]recoveryPartialOutput) interfaces.TranscriptResult {
	if only, ok := retained[""]; ok && len(retained) == 1 {
		return only.result
	}
	// Track timestamps are local to their source audio. Until final assembly
	// applies each saved offset, expose text without inventing a shared timeline.
	keys := make([]string, 0, len(retained))
	for key := range retained {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	texts := make([]string, 0, len(keys))
	for _, key := range keys {
		texts = append(texts, retained[key].result.Text)
	}
	return interfaces.TranscriptResult{Text: strings.Join(texts, "\n\n")}
}

// ResumeRun admits the exact retained execution and its immutable settings.
// @Summary Resume a saved execution
// @Description Resume the same execution within its original deadline and attempt budget; accepts no quality overrides
// @Tags transcription
// @Produce json
// @Param id path string true "Recording ID"
// @Param run_id path string true "Execution ID"
// @Success 202 {object} map[string]interface{}
// @Failure 404 {object} ErrorResponse
// @Failure 409 {object} ErrorResponse
// @Failure 503 {object} ErrorResponse
// @Router /api/v1/transcription/{id}/runs/{run_id}/resume [post]
// @Security ApiKeyAuth
// @Security BearerAuth
func (h *Handler) ResumeRun(c *gin.Context) {
	jobID, runID := c.Param("id"), c.Param("run_id")
	job, err := h.jobRepo.FindByID(c.Request.Context(), jobID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Recording not found"})
		return
	}
	run, err := h.jobRepo.FindExecution(c.Request.Context(), jobID, runID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Execution not found"})
		return
	}
	if h.taskQueue == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Transcription queue unavailable"})
		return
	}
	// Only credentials may be refreshed; all quality/context fields are taken
	// from the exact execution. The endpoint accepts no parameter overrides.
	params := run.ActualParameters
	params.HfToken = job.Parameters.HfToken
	params.APIKey = job.Parameters.APIKey
	params.HFTokenResolved = false
	if params.TranscriptionContext == nil {
		empty := ""
		params.TranscriptionContext = &empty
	}
	if params.TranscriptionContextTerms == nil {
		empty := ""
		params.TranscriptionContextTerms = &empty
	}
	if err := h.resolveTranscriptionContext(c, &params); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	ctx := transcription.WithResumeParameters(c.Request.Context(), params)
	if err := h.taskQueue.ResumeExecution(ctx, jobID, runID); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"execution_id": runID, "message": "Saved execution admitted for resume"})
}
