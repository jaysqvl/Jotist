package repository

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jaysqvl/Jotist/internal/models"
	"gorm.io/gorm"
)

type statisticsRun struct {
	ID, TranscriptionJobID, Status, RecoveryState string
	ModelFamily, Model, Diarizer, RequestedDevice string
	RequestedSpeakerDevice                        string
	Diarize                                       bool
	IsMultiTrack                                  bool
	StartedAt                                     time.Time
	CompletedAt                                   *time.Time
	ProcessingDuration                            *int64
	ResumeOfExecutionID                           *string
	ResourceJSON, RuntimeJSON                     string
}

type statisticsStage struct {
	ID, ExecutionID, Kind string
	DurationSeconds       float64
	ReusedFromExecutionID *string
}

type statisticsAttempt struct {
	StageID, ExecutionID, State, Device, Precision, Reason, ErrorCode string
	AttemptNumber                                                     int
	StartedAt                                                         time.Time
	CompletedAt                                                       *time.Time
	MeasurementsJSON                                                  string
}

// ReadRunStatistics selects only identity, settings and numeric evidence. In
// particular, SQLite projects whitelisted metadata without loading transcript
// text, credentials, prompts, logs or checkpoint files into the application.
func ReadRunStatistics(ctx context.Context, db *gorm.DB, days int, now time.Time) (models.RunStatistics, error) {
	result := models.RunStatistics{GeneratedAt: now.UTC(), WindowDays: days, Models: []models.RunModelStatistics{}, Activity: []models.RunActivity{}, Stages: []models.RunStageStatistics{}, Failures: []models.RunFailureStatistics{}}
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.TranscriptionJob{}).Count(&result.LibraryRecordings).Error; err != nil {
			return err
		}
		base := func() *gorm.DB {
			q := tx.Table("transcription_job_executions AS e").Joins("JOIN transcription_jobs AS j ON j.id = e.transcription_job_id AND j.deleted_at IS NULL")
			if days > 0 {
				q = q.Where("e.started_at >= ?", now.UTC().AddDate(0, 0, -days))
			}
			return q
		}
		keys := []string{"resolved_device", "precision", "diarization_resolved_device", "diarization_device", "diarization_model", "diarization_model_id", "asr_device_fallback", "diarization_device_fallback", "auto_token_split_windows", "native_timing_retry_windows", "output_repair_count", "token_retries", "token_splits"}
		parts := make([]string, 0, len(keys)*2)
		for _, key := range keys {
			parts = append(parts, "'"+key+"'", "json_extract(e.transcript, '$.metadata."+key+"')")
		}
		projection := "e.id, e.transcription_job_id, e.status, e.recovery_state, e.started_at, e.completed_at, e.processing_duration, e.resume_of_execution_id, e.actual_model_family AS model_family, e.actual_model AS model, e.actual_diarize AS diarize, j.is_multi_track AS is_multi_track, e.actual_device AS requested_device, e.actual_diarization_device AS requested_speaker_device, CASE WHEN e.actual_diarize_model = 'native' THEN 'native' ELSE COALESCE(NULLIF(e.actual_diarization_checkpoint, ''), e.actual_diarize_model) END AS diarizer, COALESCE(e.resource_measurements, '[]') AS resource_json, CASE WHEN json_valid(e.transcript) THEN json_object(" + strings.Join(parts, ",") + ") ELSE '{}' END AS runtime_json"
		runs := []statisticsRun{}
		if err := base().Select(projection).Order("e.started_at, e.id").Scan(&runs).Error; err != nil {
			return err
		}
		stages := []statisticsStage{}
		if err := tx.Table("recovery_stages").Select("id, execution_id, kind, duration_seconds, reused_from_execution_id").Where("execution_id IN (?)", base().Select("e.id")).Scan(&stages).Error; err != nil {
			return err
		}
		attempts := []statisticsAttempt{}
		if err := tx.Table("recovery_attempts").Select("stage_id, execution_id, state, device, precision, reason, error_code, attempt_number, started_at, completed_at, COALESCE(measurements, '{}') AS measurements_json").Where("execution_id IN (?)", base().Select("e.id")).Order("execution_id, stage_id, attempt_number").Scan(&attempts).Error; err != nil {
			return err
		}
		aggregateRunStatistics(&result, runs, stages, attempts)
		return nil
	})
	return result, err
}

type statisticsAccumulator struct {
	row    models.RunModelStatistics
	hours  []float64
	memory memoryAccumulator
}

type memoryAccumulator struct {
	value          models.RunMemoryStatistics
	gpuSum, ramSum float64
}

func (m *memoryAccumulator) add(reading models.StageMeasurements, average bool) {
	peak := func(dst **int64, value *int64) {
		if value != nil && *value >= 0 && (*dst == nil || *value > **dst) {
			v := *value
			*dst = &v
		}
	}
	peak(&m.value.PeakGPUBytes, reading.DevicePeakUsedBytes)
	peak(&m.value.PeakRAMBytes, reading.ProcessPeakRSSBytes)
	peak(&m.value.PeakWorkerGPUBytes, reading.ProcessPeakVRAMBytes)
	if !average {
		return
	}
	if reading.DeviceAverageUsedBytes != nil && *reading.DeviceAverageUsedBytes >= 0 && positiveFinite(reading.DeviceSampledSeconds) {
		m.gpuSum += float64(*reading.DeviceAverageUsedBytes) * reading.DeviceSampledSeconds
		m.value.GPUSampledSeconds += reading.DeviceSampledSeconds
	}
	if reading.ProcessAverageRSSBytes != nil && *reading.ProcessAverageRSSBytes >= 0 && positiveFinite(reading.RSSSampledSeconds) {
		m.ramSum += float64(*reading.ProcessAverageRSSBytes) * reading.RSSSampledSeconds
		m.value.RAMSampledSeconds += reading.RSSSampledSeconds
	}
}

func (m *memoryAccumulator) finish() models.RunMemoryStatistics {
	if m.value.GPUSampledSeconds > 0 {
		v := m.gpuSum / m.value.GPUSampledSeconds
		m.value.AverageGPUBytes = &v
	}
	if m.value.RAMSampledSeconds > 0 {
		v := m.ramSum / m.value.RAMSampledSeconds
		m.value.AverageRAMBytes = &v
	}
	return m.value
}

func positiveFinite(value float64) bool {
	return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func median(values []float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	sort.Float64s(values)
	mid := len(values) / 2
	v := values[mid]
	if len(values)%2 == 0 {
		v = (values[mid-1] + values[mid]) / 2
	}
	return &v
}

func runtimeFields(raw string) map[string]string {
	fields, result := map[string]json.RawMessage{}, map[string]string{}
	if json.Unmarshal([]byte(raw), &fields) != nil {
		return result
	}
	for key, rawValue := range fields {
		var value string
		if json.Unmarshal(rawValue, &value) == nil {
			result[key] = value
		}
	}
	return result
}

func statisticsDevice(value string) string {
	value = strings.ToLower(value)
	if value == "gpu" || strings.HasPrefix(value, "cuda") {
		return "cuda"
	}
	switch value {
	case "cpu", "mps", "api":
		return value
	}
	return "unknown"
}

func aggregateRunStatistics(result *models.RunStatistics, runs []statisticsRun, stages []statisticsStage, attempts []statisticsAttempt) {
	byRunStages, byRunAttempts := map[string][]statisticsStage{}, map[string][]statisticsAttempt{}
	stageKinds := map[string]string{}
	for _, stage := range stages {
		byRunStages[stage.ExecutionID] = append(byRunStages[stage.ExecutionID], stage)
		stageKinds[stage.ID] = stage.Kind
	}
	for _, attempt := range attempts {
		byRunAttempts[attempt.ExecutionID] = append(byRunAttempts[attempt.ExecutionID], attempt)
	}
	groups, activity := map[string]*statisticsAccumulator{}, map[string]*models.RunActivity{}
	hours := []float64{}
	allMemory := memoryAccumulator{}
	for _, run := range runs {
		result.Summary.Runs++
		day := run.StartedAt.UTC().Format("2006-01-02")
		if activity[day] == nil {
			activity[day] = &models.RunActivity{Date: day}
		}
		switch run.Status {
		case "completed":
			result.Summary.Completed++
			activity[day].Completed++
		case "failed":
			result.Summary.Failed++
			activity[day].Failed++
		case "processing", "running", "pending", "waiting_for_resource":
			result.Summary.Active++
			activity[day].Other++
		default:
			result.Summary.Other++
			activity[day].Other++
		}
		meta := runtimeFields(run.RuntimeJSON)
		asrDevice, speakerDevice, precision := statisticsDevice(meta["resolved_device"]), statisticsDevice(meta["diarization_resolved_device"]), meta["precision"]
		if speakerDevice == "unknown" {
			speakerDevice = statisticsDevice(meta["diarization_device"])
		}
		diarizer := run.Diarizer
		if model := meta["diarization_model"]; model != "" {
			diarizer = model
		} else if model = meta["diarization_model_id"]; model != "" {
			diarizer = model
		}
		reportedASR, reportedSpeakers, reportedPrecision := asrDevice != "unknown", speakerDevice != "unknown", precision != ""
		lastASR, lastSpeakers := time.Time{}, time.Time{}
		duration, reused, recovered, fallback := 0.0, false, false, false
		for _, stage := range byRunStages[run.ID] {
			// Track-stage durations describe individual inputs rather than the
			// complete multi-track recording, so they do not supply run RTF.
			if !strings.Contains(stage.Kind, "track") && (statisticsStageKind(stage.Kind) == "recognition" || statisticsStageKind(stage.Kind) == "alignment" || statisticsStageKind(stage.Kind) == "diarization") && positiveFinite(stage.DurationSeconds) && stage.DurationSeconds > duration {
				duration = stage.DurationSeconds
			}
			if stage.ReusedFromExecutionID != nil && *stage.ReusedFromExecutionID != "" {
				reused = true
			}
		}
		gpuStages := map[string]bool{}
		stageAttemptCounts := map[string]int{}
		stageMeasurements := []models.StageMeasurements{}
		for _, attempt := range byRunAttempts[run.ID] {
			if stageAttemptCounts[attempt.StageID] > 0 {
				recovered = true
			}
			stageAttemptCounts[attempt.StageID]++
			device := statisticsDevice(attempt.Device)
			if device == "cpu" && (gpuStages[attempt.StageID] || attempt.Reason == "cpu_fallback") {
				fallback = true
			}
			if device == "cuda" {
				gpuStages[attempt.StageID] = true
			}
			if attempt.State == models.RecoverySucceeded {
				switch statisticsStageKind(stageKinds[attempt.StageID]) {
				case "recognition":
					if !attempt.StartedAt.Before(lastASR) {
						if !reportedASR {
							asrDevice = device
						}
						if !reportedPrecision {
							precision = attempt.Precision
						}
						lastASR = attempt.StartedAt
					}
				case "diarization":
					if !reportedSpeakers && !attempt.StartedAt.Before(lastSpeakers) {
						speakerDevice = device
						lastSpeakers = attempt.StartedAt
					}
				}
			}
			var m models.StageMeasurements
			if json.Unmarshal([]byte(attempt.MeasurementsJSON), &m) == nil {
				stageMeasurements = append(stageMeasurements, m)
				recovered = recovered || m.WorkerRetryCount > 0 && m.WorkerRetryCount <= 6
			}
		}
		if !run.Diarize {
			diarizer, speakerDevice = "none", "none"
		} else if run.Diarizer == "native" {
			speakerDevice = asrDevice
			diarizer = "native"
		}
		if meta["asr_device_fallback"] == "cuda_to_cpu" || meta["diarization_device_fallback"] == "cuda_to_cpu" || (statisticsDevice(run.RequestedDevice) == "cuda" && asrDevice == "cpu" && run.ModelFamily != "openai" && run.ModelFamily != "openai_whisper") {
			fallback = true
		}
		requestedSpeaker := run.RequestedSpeakerDevice
		if requestedSpeaker == "same" {
			requestedSpeaker = run.RequestedDevice
		}
		if run.Diarize && statisticsDevice(requestedSpeaker) == "cuda" && speakerDevice == "cpu" {
			fallback = true
		}
		for _, key := range []string{"auto_token_split_windows", "native_timing_retry_windows", "output_repair_count", "token_retries", "token_splits"} {
			if n, err := strconv.Atoi(meta[key]); err == nil && n > 0 && n <= 100000 {
				recovered = true
			}
		}
		recovered = recovered || fallback
		if fallback {
			result.Summary.CPUFallback++
		}
		if recovered && run.Status == "completed" {
			result.Summary.Recovered++
		}
		if reused {
			result.Summary.Reused++
		}
		resumed := run.ResumeOfExecutionID != nil && *run.ResumeOfExecutionID != ""
		invocations := []models.StageMeasurements{}
		_ = json.Unmarshal([]byte(run.ResourceJSON), &invocations)
		if len(invocations) > 1 {
			resumed = true
		}
		if resumed {
			result.Summary.Resumed++
		}
		keyBytes, _ := json.Marshal([]string{run.ModelFamily, run.Model, diarizer, asrDevice, speakerDevice, precision})
		key := string(keyBytes)
		if groups[key] == nil {
			groups[key] = &statisticsAccumulator{row: models.RunModelStatistics{ModelFamily: run.ModelFamily, Model: run.Model, Diarizer: diarizer, RecognitionDevice: asrDevice, SpeakerDevice: speakerDevice, Precision: precision}}
		}
		group := groups[key]
		group.row.Runs++
		if run.Status == "completed" {
			group.row.Completed++
			if recovered {
				group.row.Recovered++
			}
		}
		if run.Status == "failed" {
			group.row.Failed++
		}
		if fallback {
			group.row.CPUFallback++
		}
		if reused {
			group.row.Reused++
		}
		if run.Status == "completed" && duration > 0 {
			result.Summary.AudioSeconds += duration
			result.Summary.AudioMeasuredRuns++
			elapsed := 0.0
			if run.ProcessingDuration != nil {
				elapsed = float64(*run.ProcessingDuration) / 1000
			} else if run.CompletedAt != nil {
				elapsed = run.CompletedAt.Sub(run.StartedAt).Seconds()
			}
			if !reused && !resumed && !run.IsMultiTrack && positiveFinite(elapsed) {
				value := elapsed / duration * 3600
				if positiveFinite(value) {
					hours = append(hours, value)
					group.hours = append(group.hours, value)
					result.Summary.TimingMeasuredRuns++
					group.row.TimingMeasuredRuns++
				}
			}
		}
		contention, unknownOwnership := false, false
		if len(invocations) > 0 {
			result.Summary.ResourceMeasuredRuns++
		}
		// Stage peaks supplement missing full-run peaks, but overlapping stage
		// intervals must never be counted again in full-run weighted averages.
		for _, m := range stageMeasurements {
			allMemory.add(m, false)
			group.memory.add(m, false)
			contention = contention || m.ExternalContention
			unknownOwnership = unknownOwnership || m.OwnershipUnknown
		}
		for _, m := range invocations {
			allMemory.add(m, true)
			group.memory.add(m, true)
			contention = contention || m.ExternalContention
			unknownOwnership = unknownOwnership || m.OwnershipUnknown
		}
		if contention {
			allMemory.value.ContentionRuns++
			group.memory.value.ContentionRuns++
		}
		if unknownOwnership {
			allMemory.value.UnknownOwnershipRuns++
			group.memory.value.UnknownOwnershipRuns++
		}
	}
	if total := result.Summary.Completed + result.Summary.Failed; total > 0 {
		v := float64(result.Summary.Completed) / float64(total) * 100
		result.Summary.CompletionRate = &v
	}
	result.Summary.MedianHourSeconds, result.Summary.Memory = median(hours), allMemory.finish()
	for _, group := range groups {
		group.row.MedianHourSeconds = median(group.hours)
		group.row.Memory = group.memory.finish()
		result.Models = append(result.Models, group.row)
	}
	sort.Slice(result.Models, func(i, j int) bool {
		if result.Models[i].Runs != result.Models[j].Runs {
			return result.Models[i].Runs > result.Models[j].Runs
		}
		return result.Models[i].Model+result.Models[i].Diarizer+result.Models[i].RecognitionDevice+result.Models[i].SpeakerDevice+result.Models[i].Precision < result.Models[j].Model+result.Models[j].Diarizer+result.Models[j].RecognitionDevice+result.Models[j].SpeakerDevice+result.Models[j].Precision
	})
	for _, day := range activity {
		result.Activity = append(result.Activity, *day)
	}
	sort.Slice(result.Activity, func(i, j int) bool { return result.Activity[i].Date < result.Activity[j].Date })
	stageStats, stageTimes, failures := map[string]*models.RunStageStatistics{}, map[string][]float64{}, map[string]*models.RunFailureStatistics{}
	for _, attempt := range attempts {
		kind := stageKinds[attempt.StageID]
		if kind == "" {
			kind = "unknown"
		}
		if stageStats[kind] == nil {
			stageStats[kind] = &models.RunStageStatistics{Kind: kind}
		}
		row := stageStats[kind]
		row.Attempts++
		if attempt.State == models.RecoverySucceeded {
			row.Succeeded++
		}
		if attempt.State == models.RecoveryFailed || attempt.State == models.RecoveryRetryable || attempt.State == models.RecoveryBlocked {
			row.Failed++
		}
		if attempt.AttemptNumber > 1 {
			row.Retries++
		}
		var measurements models.StageMeasurements
		if json.Unmarshal([]byte(attempt.MeasurementsJSON), &measurements) == nil && measurements.WorkerRetryCount > 0 && measurements.WorkerRetryCount <= 6 {
			row.WorkerRetries += measurements.WorkerRetryCount
		}
		if attempt.CompletedAt != nil {
			elapsed := attempt.CompletedAt.Sub(attempt.StartedAt).Seconds()
			if positiveFinite(elapsed) {
				stageTimes[kind] = append(stageTimes[kind], elapsed)
				row.TimingSamples++
			}
		}
		if attempt.ErrorCode != "" {
			key := kind + "\x00" + attempt.ErrorCode
			if failures[key] == nil {
				failures[key] = &models.RunFailureStatistics{Stage: kind, Code: attempt.ErrorCode}
			}
			failures[key].Attempts++
		}
	}
	for kind, row := range stageStats {
		row.MedianSeconds = median(stageTimes[kind])
		result.Stages = append(result.Stages, *row)
	}
	sort.Slice(result.Stages, func(i, j int) bool { return result.Stages[i].Kind < result.Stages[j].Kind })
	for _, row := range failures {
		result.Failures = append(result.Failures, *row)
	}
	sort.Slice(result.Failures, func(i, j int) bool {
		if result.Failures[i].Attempts != result.Failures[j].Attempts {
			return result.Failures[i].Attempts > result.Failures[j].Attempts
		}
		return result.Failures[i].Stage+result.Failures[i].Code < result.Failures[j].Stage+result.Failures[j].Code
	})
}

// Runtime evidence uses canonical phase names without relabelling combined
// attempt timing as if it measured recognition alone.
func statisticsStageKind(kind string) string {
	switch kind {
	case "asr", "recognize", "recognition", "combined":
		return "recognition"
	case "align", "alignment":
		return "alignment"
	case "diarize", "diarization", "speaker_assignment":
		return "diarization"
	}
	return kind
}
