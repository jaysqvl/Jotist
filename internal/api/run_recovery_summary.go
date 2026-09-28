package api

import (
	"context"
	"strings"

	"scriberr/internal/models"
)

type runRecoverySummary struct {
	EvidenceAvailable bool     `json:"evidence_available"`
	RetryCount        int      `json:"retry_count"`
	CPUFallback       bool     `json:"cpu_fallback"`
	Reasons           []string `json:"reasons"`
}

func summarizeRunAttempts(attempts []models.RecoveryAttempt) runRecoverySummary {
	result := runRecoverySummary{EvidenceAvailable: true, Reasons: []string{}}
	stageCounts, gpuStages := map[string]int{}, map[string]bool{}
	reasons := map[string]bool{}
	for _, attempt := range attempts {
		if stageCounts[attempt.StageID] > 0 {
			result.RetryCount++
		}
		stageCounts[attempt.StageID]++
		device := strings.ToLower(attempt.Device)
		if device == "cpu" && (gpuStages[attempt.StageID] || attempt.Reason == "cpu_fallback") {
			result.CPUFallback = true
		}
		if strings.HasPrefix(device, "cuda") {
			gpuStages[attempt.StageID] = true
		}
		if attempt.AttemptNumber > 1 || attempt.Reason == "cpu_fallback" {
			switch attempt.Reason {
			case "cleanup_retry", "smaller_batch", "safer_precision", "cpu_fallback", "shorter_window", "exact_alignment_retry", "resume_same_settings":
				if !reasons[attempt.Reason] {
					result.Reasons = append(result.Reasons, attempt.Reason)
					reasons[attempt.Reason] = true
				}
			}
		}
	}
	return result
}

func (h *Handler) runRecoverySummaries(ctx context.Context, executions []models.TranscriptionJobExecution) (map[string]runRecoverySummary, error) {
	result := map[string]runRecoverySummary{}
	if h.unifiedProcessor == nil || h.unifiedProcessor.GetUnifiedService().RecoveryRepository() == nil {
		return result, nil
	}
	ids := []string{}
	for _, execution := range executions {
		if execution.RecoveryVersion > 0 {
			ids = append(ids, execution.ID)
		}
	}
	rows, err := h.unifiedProcessor.GetUnifiedService().RecoveryRepository().ListAttemptSummaries(ctx, ids)
	if err != nil {
		return nil, err
	}
	byRun := map[string][]models.RecoveryAttempt{}
	for _, row := range rows {
		byRun[row.ExecutionID] = append(byRun[row.ExecutionID], row)
	}
	for _, id := range ids {
		result[id] = summarizeRunAttempts(byRun[id])
	}
	return result, nil
}
