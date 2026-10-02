package transcription

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jaysqvl/Jotist/internal/models"
)

// A worker gets only the remaining retry budget of its current stage. Context
// changes need Strong/Aggressive permission as well as adapter qualification.
func workerRecoveryPolicy(params models.WhisperXParams, used int, kind string) map[string]interface{} {
	p := params.ExecutionPolicy
	if p == nil || p.RecoveryStrength == "" {
		return nil
	} // Immutable legacy plans keep their old behavior.
	remaining := p.MaxRetries - used
	if remaining < 0 {
		remaining = 0
	}
	if !p.AutomaticRecovery {
		remaining = 0
	}
	outputChanges := p.AutomaticRecovery && permitsShorterWindows(params.RecoveryMode)
	if params.AdaptivePolicy != nil {
		if rule, exists := params.AdaptivePolicy.Stages[recoveryStageKey(kind)]; exists {
			allowed := rule.AllowShorterWindows
			if rule.AllowOutputChanges != nil {
				allowed = *rule.AllowOutputChanges
			}
			outputChanges = outputChanges && allowed && rule.Fixed == nil
		}
	}
	return map[string]interface{}{
		"version": 1, "remaining_retries": remaining, "retries_used": used,
		"backoff_seconds": p.BackoffSeconds, "max_backoff_seconds": p.MaxBackoffSeconds,
		"allow_output_changes": outputChanges,
	}
}

// Scan the whole current invocation, with bounded lines and marker retention.
// An hour-long worker can otherwise push its first retry out of a log tail.
func attemptWorkerRecoveryEvidence(directory string, offset int64, limit int) (int, map[string]int, error) {
	file, err := os.Open(filepath.Join(directory, "transcription.log"))
	if os.IsNotExist(err) {
		return 0, nil, nil
	}
	if err != nil {
		return 0, nil, err
	}
	defer file.Close()
	if _, err = file.Seek(offset, io.SeekStart); err != nil {
		return 0, nil, err
	}
	reader := bufio.NewReaderSize(file, 4096)
	var markers strings.Builder
	line := []byte{}
	dropping := false
	for {
		part, more, readErr := reader.ReadLine()
		if readErr != nil && readErr != io.EOF {
			return 0, nil, readErr
		}
		if len(line)+len(part) > 1024 {
			dropping = true
			line = nil
		} else if !dropping {
			line = append(line, part...)
		}
		if !more {
			if !dropping && strings.HasPrefix(string(line), "JOTIST_RECOVERY=") && markers.Len() < 8192 {
				markers.Write(line)
				markers.WriteByte('\n')
			}
			line, dropping = line[:0], false
		}
		if readErr == io.EOF {
			break
		}
	}
	count, actions := workerRecoveryEvidence(markers.String(), limit)
	return count, actions, nil
}

func savedRetryUsage(history []models.RecoveryAttempt) int {
	used := 0
	for _, a := range history {
		switch a.Reason {
		case "cleanup_retry", "smaller_batch", "cpu_fallback", "shorter_window", "exact_alignment_retry":
			used++
		}
		if a.Measurements != nil {
			used += a.Measurements.WorkerRetryCount
		}
	}
	return used
}

// An interrupted invocation without saved evidence may already have spent its
// inner retries. Manual resume may run once, but must not grant a fresh budget.
func stageRetryUsage(history []models.RecoveryAttempt, policy *models.ExecutionPolicy) int {
	used := savedRetryUsage(history)
	if policy != nil && policy.RecoveryStrength != "" {
		for _, attempt := range history {
			if attempt.Measurements == nil {
				return policyMax(used, policy.MaxRetries)
			}
		}
	}
	return used
}

func workerChangedOutput(m models.StageMeasurements) bool {
	return m.WorkerRecoveryActions["token_window_split"] > 0 || m.WorkerRecoveryActions["native_timing_split"] > 0 || m.WorkerRecoveryActions["native_timing_repair"] > 0
}

// Only bundled, bounded action records are accepted, from this invocation's
// output range. No third-party messages, transcript text or settings are stored.
func workerRecoveryEvidence(log string, limit int) (int, map[string]int) {
	count := 0
	actions := map[string]int{}
	for _, line := range strings.Split(log, "\n") {
		if !strings.HasPrefix(line, "JOTIST_RECOVERY=") {
			continue
		}
		var event struct {
			Action string `json:"action"`
			Retry  int    `json:"retry"`
		}
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "JOTIST_RECOVERY=")), &event) != nil || event.Retry != count+1 || count >= limit {
			continue
		}
		switch event.Action {
		case "decoder_budget_retry", "token_window_split", "native_timing_split", "native_timing_repair":
			count++
			actions[event.Action]++
		}
	}
	return count, actions
}
