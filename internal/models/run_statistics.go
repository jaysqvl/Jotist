package models

import "time"

// RunStatistics is a content-free projection of retained execution evidence.
// It does not contain transcripts, recording titles, prompts or credentials.
type RunStatistics struct {
	GeneratedAt       time.Time              `json:"generated_at"`
	WindowDays        int                    `json:"window_days"`
	LibraryRecordings int64                  `json:"library_recordings"`
	Summary           RunStatisticsSummary   `json:"summary"`
	Models            []RunModelStatistics   `json:"models"`
	Activity          []RunActivity          `json:"activity"`
	Stages            []RunStageStatistics   `json:"stages"`
	Failures          []RunFailureStatistics `json:"failures"`
}

type RunStatisticsSummary struct {
	Runs                 int                 `json:"runs"`
	Completed            int                 `json:"completed"`
	Failed               int                 `json:"failed"`
	Active               int                 `json:"active"`
	Other                int                 `json:"other"`
	Recovered            int                 `json:"recovered"`
	CPUFallback          int                 `json:"cpu_fallback"`
	Reused               int                 `json:"reused"`
	Resumed              int                 `json:"resumed"`
	CompletionRate       *float64            `json:"completion_rate"`
	AudioSeconds         float64             `json:"audio_seconds"`
	AudioMeasuredRuns    int                 `json:"audio_measured_runs"`
	TimingMeasuredRuns   int                 `json:"timing_measured_runs"`
	MedianHourSeconds    *float64            `json:"median_hour_seconds"`
	ResourceMeasuredRuns int                 `json:"resource_measured_runs"`
	Memory               RunMemoryStatistics `json:"memory"`
}

type RunMemoryStatistics struct {
	PeakGPUBytes         *int64   `json:"peak_gpu_bytes"`
	AverageGPUBytes      *float64 `json:"average_gpu_bytes"`
	GPUSampledSeconds    float64  `json:"gpu_sampled_seconds"`
	PeakWorkerGPUBytes   *int64   `json:"peak_worker_gpu_bytes"`
	PeakRAMBytes         *int64   `json:"peak_ram_bytes"`
	AverageRAMBytes      *float64 `json:"average_ram_bytes"`
	RAMSampledSeconds    float64  `json:"ram_sampled_seconds"`
	ContentionRuns       int      `json:"contention_runs"`
	UnknownOwnershipRuns int      `json:"unknown_ownership_runs"`
}

type RunModelStatistics struct {
	ModelFamily        string              `json:"model_family"`
	Model              string              `json:"model"`
	Diarizer           string              `json:"diarizer"`
	RecognitionDevice  string              `json:"recognition_device"`
	SpeakerDevice      string              `json:"speaker_device"`
	Precision          string              `json:"precision"`
	Runs               int                 `json:"runs"`
	Completed          int                 `json:"completed"`
	Failed             int                 `json:"failed"`
	Recovered          int                 `json:"recovered"`
	CPUFallback        int                 `json:"cpu_fallback"`
	Reused             int                 `json:"reused"`
	TimingMeasuredRuns int                 `json:"timing_measured_runs"`
	MedianHourSeconds  *float64            `json:"median_hour_seconds"`
	Memory             RunMemoryStatistics `json:"memory"`
}

type RunActivity struct {
	Date      string `json:"date"`
	Completed int    `json:"completed"`
	Failed    int    `json:"failed"`
	Other     int    `json:"other"`
}

type RunStageStatistics struct {
	Kind          string   `json:"kind"`
	Attempts      int      `json:"attempts"`
	Succeeded     int      `json:"succeeded"`
	Failed        int      `json:"failed"`
	Retries       int      `json:"retries"`
	WorkerRetries int      `json:"worker_retries,omitempty"`
	TimingSamples int      `json:"timing_samples"`
	MedianSeconds *float64 `json:"median_seconds"`
}

type RunFailureStatistics struct {
	Stage    string `json:"stage"`
	Code     string `json:"code"`
	Attempts int    `json:"attempts"`
}
