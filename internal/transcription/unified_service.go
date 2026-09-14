package transcription

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"scriberr/internal/models"
	"scriberr/internal/processutil"
	"scriberr/internal/repository"
	"scriberr/internal/sse"
	"scriberr/internal/transcription/interfaces"
	"scriberr/internal/transcription/pipeline"
	"scriberr/internal/transcription/registry"
	"scriberr/internal/webhook"
	"scriberr/pkg/logger"
)

const (
	ModelWhisperX          = "whisperx"
	ModelPyannote          = "pyannote"
	ModelParakeet          = "parakeet"
	ModelCanary            = "canary"
	ModelCanaryQwen        = "canary_qwen"
	ModelSortformer        = "sortformer"
	ModelOpenAI            = "openai_whisper"
	ModelVoxtral           = "voxtral"
	ModelDiarization31     = "pyannote/speaker-diarization-3.1"
	FamilyNvidiaCanary     = "nvidia_canary"
	FamilyNvidiaCanaryQwen = "nvidia_canary_qwen"
	FamilyNvidiaParakeet   = "nvidia_parakeet"
	FamilyWhisper          = "whisper"
	FamilyOpenAI           = "openai"
	FamilyMistralVoxtral   = "mistral_voxtral"
	DiarizeSortformer      = "nvidia_sortformer"
	OutputFormatJSON       = "json"
)

// UnifiedTranscriptionService provides a unified interface for all transcription and diarization models
type UnifiedTranscriptionService struct {
	registry               *registry.ModelRegistry
	pipeline               *pipeline.ProcessingPipeline
	preprocessors          map[string]interfaces.Preprocessor
	postprocessors         map[string]interfaces.Postprocessor
	tempDirectory          string
	outputDirectory        string
	defaultModelIDs        map[string]string // Default model IDs for each task type
	multiTrackMutex        sync.RWMutex
	multiTrackTranscribers map[string]*MultiTrackTranscriber // Active transcribers keyed by parent job ID.
	jobRepo                repository.JobRepository
	webhookService         *webhook.Service
	broadcaster            *sse.Broadcaster
	recovery               *repository.RecoveryRepository
	lifecycle              repository.ExecutionLifecycleRepository
	recoveryDB             *gorm.DB
	recoveryInitError      error
}

// NewUnifiedTranscriptionService creates a new unified transcription service
func NewUnifiedTranscriptionService(jobRepo repository.JobRepository, tempDir, outputDir string) *UnifiedTranscriptionService {
	service := &UnifiedTranscriptionService{
		registry:        registry.GetRegistry(),
		pipeline:        pipeline.NewProcessingPipeline(),
		preprocessors:   make(map[string]interfaces.Preprocessor),
		postprocessors:  make(map[string]interfaces.Postprocessor),
		tempDirectory:   tempDir,
		outputDirectory: outputDir,
		defaultModelIDs: map[string]string{
			"transcription": ModelWhisperX,
			"diarization":   ModelPyannote,
		},
		multiTrackTranscribers: make(map[string]*MultiTrackTranscriber),
		jobRepo:                jobRepo,
		webhookService:         webhook.NewService(),
	}
	service.configureRecovery()
	return service
}

// SetBroadcaster sets the SSE broadcaster for the service
func (u *UnifiedTranscriptionService) SetBroadcaster(b *sse.Broadcaster) {
	u.broadcaster = b
}

// Initialize prepares all registered models for use
func (u *UnifiedTranscriptionService) Initialize(ctx context.Context) error {
	if u.recoveryInitError != nil {
		return u.recoveryInitError
	}
	logger.Info("Initializing unified transcription service")

	// Create necessary directories
	if err := os.MkdirAll(u.tempDirectory, 0755); err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	if err := os.MkdirAll(u.outputDirectory, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Initialize all registered models
	release, err := acquireGPUStage(ctx, map[string]interface{}{"device": "auto"})
	if err != nil {
		return err
	}
	defer release()
	if err := u.registry.InitializeModels(ctx); err != nil {
		return fmt.Errorf("failed to initialize models: %w", err)
	}

	logger.Info("Unified transcription service initialized successfully")
	return nil
}

// ProcessJob processes a transcription job using the new adapter architecture
//
//nolint:gocyclo // Complex orchestration required
func (u *UnifiedTranscriptionService) ProcessJob(ctx context.Context, jobID string) error {
	if u.recoveryInitError != nil {
		return u.recoveryInitError
	}
	if u.recovery != nil {
		return u.processRecoverableJob(ctx, jobID)
	}
	startTime := time.Now()
	logger.Info("Processing job with unified service", "job_id", jobID)

	// Get the job from database
	job, err := u.jobRepo.FindWithAssociations(ctx, jobID)
	if err != nil {
		return fmt.Errorf("failed to get job: %w", err)
	}

	// Create execution record
	execution := &models.TranscriptionJobExecution{
		TranscriptionJobID: jobID,
		StartedAt:          startTime,
		ActualParameters:   job.Parameters.WithoutSecrets(),
		Status:             models.StatusProcessing,
	}

	if err := u.jobRepo.CreateExecution(ctx, execution); err != nil {
		return fmt.Errorf("failed to create execution record: %w", err)
	}
	logPath := filepath.Join(u.outputDirectory, jobID, "runs", execution.ID, "transcription.log")
	execution.LogPath = &logPath
	if err := u.jobRepo.UpdateExecution(ctx, execution); err != nil {
		logger.Warn("Failed to update execution log path", "job_id", jobID, "execution_id", execution.ID, "error", err)
	}

	// Broadcast initial processing status
	if u.broadcaster != nil {
		u.broadcaster.Broadcast(jobID, "job_update", map[string]interface{}{
			"job_id": jobID,
			"status": models.StatusProcessing,
		})
	}

	// Helper function to update execution status
	updateExecutionStatus := func(status models.JobStatus, errorMsg string) {
		completedAt := time.Now()
		execution.CompletedAt = &completedAt
		execution.Status = status
		execution.CalculateProcessingDuration()

		if errorMsg != "" {
			execution.ErrorMessage = &errorMsg
		}

		// Cancellation is part of an execution's lifecycle, but a cancelled job
		// context cannot be used to persist that terminal state. Give bookkeeping
		// its own short-lived context so run history never remains stuck in
		// "processing" after a stop or timeout.
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if err := u.jobRepo.UpdateExecution(cleanupCtx, execution); err != nil {
			logger.Error("Failed to persist terminal execution status",
				"job_id", jobID,
				"execution_id", execution.ID,
				"status", status,
				"error", err)
		}

		// Broadcast update via SSE
		if u.broadcaster != nil {
			u.broadcaster.Broadcast(jobID, "job_update", map[string]interface{}{
				"job_id": jobID,
				"status": status,
				"error":  errorMsg,
			})
		}

		// Trigger webhook if callback URL is present
		if job.Parameters.CallbackURL != nil && *job.Parameters.CallbackURL != "" {
			payload := webhook.WebhookPayload{
				JobID:        job.ID,
				Status:       status,
				AudioPath:    job.AudioPath,
				Transcript:   job.Transcript,
				Summary:      job.Summary,
				ErrorMessage: execution.ErrorMessage,
				CompletedAt:  completedAt,
				Metadata: map[string]interface{}{
					"model":        job.Parameters.Model,
					"model_family": job.Parameters.ModelFamily,
					"duration_ms":  execution.ProcessingDuration,
				},
			}

			// Send webhook asynchronously to not block the main process
			go func() {
				// Create a new context with timeout for the webhook
				webhookCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()

				if err := u.webhookService.SendWebhook(webhookCtx, *job.Parameters.CallbackURL, payload); err != nil {
					logger.Error("Failed to send webhook", "job_id", job.ID, "error", err)
				}
			}()
		}
	}

	// Check for multi-track processing
	if job.IsMultiTrack && job.Parameters.IsMultiTrackEnabled {
		logger.Info("Processing multi-track job", "job_id", jobID)
		if err := u.processMultiTrackJob(ctx, job, execution); err != nil {
			errMsg := fmt.Sprintf("multi-track processing failed: %v", err)
			updateExecutionStatus(models.StatusFailed, errMsg)
			return fmt.Errorf("%s", errMsg)
		}
		if refreshedJob, err := u.jobRepo.FindByID(ctx, jobID); err == nil {
			execution.Transcript = refreshedJob.Transcript
			job.Transcript = refreshedJob.Transcript
		}
	} else {
		// Process single track
		if err := u.processSingleTrackJob(ctx, job, execution); err != nil {
			errMsg := fmt.Sprintf("single-track processing failed: %v", err)
			updateExecutionStatus(models.StatusFailed, errMsg)
			return fmt.Errorf("%s", errMsg)
		}
	}

	// Success
	updateExecutionStatus(models.StatusCompleted, "")
	logger.Info("Job processed successfully", "job_id", jobID, "duration", time.Since(startTime))
	return nil
}

// processSingleTrackJob handles single audio file transcription
//
//nolint:gocyclo // Orchestrator function with multiple steps
func (u *UnifiedTranscriptionService) processSingleTrackJob(ctx context.Context, job *models.TranscriptionJob, execution *models.TranscriptionJobExecution) error {
	logger.Info("Processing single-track job", "job_id", job.ID, "model_family", job.Parameters.ModelFamily)

	// Create processing context
	procCtx := interfaces.ProcessingContext{
		JobID:           job.ID,
		OutputDirectory: filepath.Join(u.outputDirectory, job.ID, "runs", execution.ID),
		TempDirectory:   u.tempDirectory,
		Metadata:        map[string]string{},
	}

	// Keep worker files inside this run so ownership and cleanup are exact.
	procCtx.TempDirectory = filepath.Join(procCtx.OutputDirectory, "work")

	// Create output directory
	if err := os.MkdirAll(procCtx.OutputDirectory, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Create audio input
	audioInput, err := u.createAudioInput(ctx, job.AudioPath)
	if err != nil {
		return fmt.Errorf("failed to create audio input: %w", err)
	}

	// Determine models to use first
	transcriptionModelID, diarizationModelID, err := u.selectModels(job.Parameters)
	if err != nil {
		return fmt.Errorf("failed to select models: %w", err)
	}

	// Apply preprocessing to ensure audio is in correct format (mono 16kHz)
	var preprocessedInput interfaces.AudioInput
	var tempFilesToCleanup []string

	// Get model capabilities for preprocessing decisions
	var capabilities interfaces.ModelCapabilities
	if transcriptionModelID != "" {
		if adapter, err := u.registry.GetTranscriptionAdapter(transcriptionModelID); err == nil {
			capabilities = adapter.GetCapabilities()
		}
	} else if diarizationModelID != "" {
		if adapter, err := u.registry.GetDiarizationAdapter(diarizationModelID); err == nil {
			capabilities = adapter.GetCapabilities()
		}
	}

	// Apply preprocessing
	preprocessedInput, err = u.pipeline.ProcessAudio(ctx, audioInput, capabilities)
	if err != nil {
		logger.Warn("Audio preprocessing failed, using original", "error", err)
		preprocessedInput = audioInput
	} else {
		// Track temporary file for cleanup if preprocessing created one
		if preprocessedInput.TempFilePath != "" && preprocessedInput.TempFilePath != audioInput.FilePath {
			tempFilesToCleanup = append(tempFilesToCleanup, preprocessedInput.TempFilePath)
			logger.Info("Audio preprocessing completed",
				"original", audioInput.FilePath,
				"converted", preprocessedInput.TempFilePath,
				"original_sr", audioInput.SampleRate,
				"converted_sr", preprocessedInput.SampleRate,
				"original_channels", audioInput.Channels,
				"converted_channels", preprocessedInput.Channels)
		}
	}

	// Ensure cleanup of temporary files when function exits
	defer func() {
		for _, tempFile := range tempFilesToCleanup {
			if err := os.Remove(tempFile); err != nil {
				logger.Warn("Failed to clean up temporary file", "file", tempFile, "error", err)
			} else {
				logger.Info("Cleaned up temporary file", "file", tempFile)
			}
		}
	}()

	var transcriptResult *interfaces.TranscriptResult
	var diarizationResult *interfaces.DiarizationResult
	recovery, err := u.makeRecoveryStageContext(ctx, job, execution, audioInput, preprocessedInput)
	if err != nil {
		return err
	}

	// Perform transcription using the preprocessed audio
	if transcriptionModelID != "" {
		logger.Info("Running transcription", "model_id", transcriptionModelID)
		transcriptionAdapter, err := u.registry.GetTranscriptionAdapter(transcriptionModelID)
		if err != nil {
			return fmt.Errorf("failed to get transcription adapter: %w", err)
		}

		// Convert parameters for this specific model
		params := u.convertParametersForModel(job.Parameters, transcriptionModelID)

		var retryMetadata map[string]string
		transcriptResult, retryMetadata, err = runRecoverableTranscription(ctx, recovery, transcriptionAdapter, preprocessedInput, params, procCtx)
		if err != nil {
			return fmt.Errorf("transcription failed: %w", err)
		}
		if transcriptResult != nil && len(retryMetadata) > 0 {
			if transcriptResult.Metadata == nil {
				transcriptResult.Metadata = map[string]string{}
			}
			for key, value := range retryMetadata {
				transcriptResult.Metadata[key] = value
			}
			if retryMetadata["asr_device_fallback"] == "cuda_to_cpu" {
				transcriptResult.Metadata["resolved_device"] = "cpu"
			}
		}
		if transcriptResult != nil && u.transcriptionIncludesDiarization(transcriptionModelID, job.Parameters) {
			if transcriptResult.Metadata == nil {
				transcriptResult.Metadata = map[string]string{}
			}
			// Integrated diarization uses this same process and resolved device.
			transcriptResult.Metadata["diarization_device"] = transcriptResult.Metadata["resolved_device"]
			if job.Parameters.DiarizeModel == "native" {
				transcriptResult.Metadata["diarization_model"] = transcriptResult.ModelUsed
			} else {
				transcriptResult.Metadata["diarization_model"] = selectedPyannoteCheckpoint(job.Parameters)
			}
		}
	}

	// Perform diarization if requested and not already done by transcription
	if job.Parameters.Diarize && diarizationModelID != "" {
		// Convert parameters for diarization model
		diarizationParams := u.convertParametersForModel(job.Parameters, diarizationModelID)
		followResolvedASRDevice(job.Parameters, transcriptResult, diarizationParams)

		if !u.transcriptionIncludesDiarization(transcriptionModelID, job.Parameters) {
			logger.Info("Running separate diarization", "model_id", diarizationModelID)
			diarizationAdapter, err := u.registry.GetDiarizationAdapter(diarizationModelID)
			if err != nil {
				return fmt.Errorf("failed to get diarization adapter: %w", err)
			}

			// Use the same preprocessed audio for diarization
			var retryMetadata map[string]string
			diarizationResult, retryMetadata, err = runRecoverableStage(ctx, recovery, "diarize", "diarization", diarizationAdapter, preprocessedInput, diarizationParams, procCtx, func(attemptParams map[string]interface{}) (*interfaces.DiarizationResult, error) {
				return diarizationAdapter.Diarize(ctx, preprocessedInput, attemptParams, procCtx)
			})
			if err != nil {
				return fmt.Errorf("diarization failed: %w", err)
			}

			if diarizationResult != nil && len(retryMetadata) > 0 {
				if diarizationResult.Metadata == nil {
					diarizationResult.Metadata = map[string]string{}
				}
				for key, value := range retryMetadata {
					diarizationResult.Metadata[key] = value
				}
				if retryMetadata["diarization_device_fallback"] == "cuda_to_cpu" {
					diarizationResult.Metadata["resolved_device"] = "cpu"
				}
			}
			// Merge diarization results with transcription
			if transcriptResult != nil && diarizationResult != nil {
				transcriptResult = u.mergeDiarizationWithTranscription(transcriptResult, diarizationResult)
				if transcriptResult.Metadata == nil {
					transcriptResult.Metadata = map[string]string{}
				}
				transcriptResult.Metadata["diarization_model"] = diarizationResult.ModelUsed
				transcriptResult.Metadata["diarization_device"] = diarizationResult.Metadata["resolved_device"]
				for key, value := range retryMetadata {
					if !strings.HasPrefix(key, "diarization_") {
						key = "diarization_" + key
					}
					transcriptResult.Metadata[key] = value
				}
			}
		}
	}

	// Save results to database
	if transcriptResult != nil {
		var resultJSON string
		var err error
		if u.recovery != nil {
			resultJSON, err = u.convertTranscriptResultToJSON(transcriptResult)
		} else {
			resultJSON, err = u.saveTranscriptionResults(job.ID, transcriptResult)
		}
		if err != nil {
			return fmt.Errorf("failed to save transcription results: %w", err)
		}
		execution.Transcript = &resultJSON
		job.Transcript = &resultJSON
	}

	return nil
}

// processMultiTrackJob handles multi-track audio processing
func (u *UnifiedTranscriptionService) processMultiTrackJob(ctx context.Context, job *models.TranscriptionJob, execution *models.TranscriptionJobExecution) error {
	logger.Info("Processing multi-track job", "job_id", job.ID, "track_count", len(job.MultiTrackFiles))

	// Create unified processor for this service
	unifiedProcessor := &UnifiedJobProcessor{
		unifiedService: u,
	}

	// Track each active parent independently. Different audio files may be
	// processed by different queue workers at the same time.
	transcriber := NewMultiTrackTranscriber(unifiedProcessor)
	if u.recoveryDB != nil {
		transcriber.db = u.recoveryDB
	}
	if err := u.registerMultiTrackTranscriber(job.ID, transcriber); err != nil {
		return err
	}
	defer u.unregisterMultiTrackTranscriber(job.ID, transcriber)

	// Process the multi-track transcription
	return transcriber.ProcessMultiTrackTranscription(ctx, job.ID, execution)
}

// TerminateMultiTrackJob terminates a multi-track job and all its individual track jobs
func (u *UnifiedTranscriptionService) TerminateMultiTrackJob(jobID string) error {
	u.multiTrackMutex.RLock()
	transcriber := u.multiTrackTranscribers[jobID]
	u.multiTrackMutex.RUnlock()
	if transcriber == nil {
		return fmt.Errorf("no active multi-track transcriber for job %s", jobID)
	}
	return transcriber.TerminateMultiTrackJob(jobID)
}

func (u *UnifiedTranscriptionService) registerMultiTrackTranscriber(jobID string, transcriber *MultiTrackTranscriber) error {
	u.multiTrackMutex.Lock()
	defer u.multiTrackMutex.Unlock()
	if _, exists := u.multiTrackTranscribers[jobID]; exists {
		return fmt.Errorf("multi-track job %s is already active", jobID)
	}
	u.multiTrackTranscribers[jobID] = transcriber
	return nil
}

func (u *UnifiedTranscriptionService) unregisterMultiTrackTranscriber(jobID string, transcriber *MultiTrackTranscriber) {
	u.multiTrackMutex.Lock()
	defer u.multiTrackMutex.Unlock()
	if u.multiTrackTranscribers[jobID] == transcriber {
		delete(u.multiTrackTranscribers, jobID)
	}
}

// IsMultiTrackJob checks if a job is a multi-track job
func (u *UnifiedTranscriptionService) IsMultiTrackJob(jobID string) bool {
	job, err := u.jobRepo.FindByID(context.Background(), jobID)
	if err != nil || job == nil {
		return false
	}
	return job.IsMultiTrack
}

// selectModels determines which models to use based on job parameters
func (u *UnifiedTranscriptionService) selectModels(params models.WhisperXParams) (transcriptionModelID, diarizationModelID string, err error) {
	// Determine transcription model
	switch params.ModelFamily {
	case FamilyNvidiaParakeet:
		transcriptionModelID = ModelParakeet
	case FamilyNvidiaCanary:
		transcriptionModelID = ModelCanary
	case FamilyNvidiaCanaryQwen:
		transcriptionModelID = ModelCanaryQwen
	case FamilyWhisper:
		transcriptionModelID = ModelWhisperX
	case FamilyOpenAI:
		transcriptionModelID = ModelOpenAI
	case FamilyMistralVoxtral:
		transcriptionModelID = ModelVoxtral
		if adapter, lookupErr := u.registry.GetTranscriptionAdapter(params.Model); lookupErr == nil && adapter.GetCapabilities().ModelFamily == FamilyMistralVoxtral {
			transcriptionModelID = params.Model
		} else if params.Model != "" && params.Model != "mistralai/Voxtral-mini" && params.Model != ModelVoxtral {
			return "", "", fmt.Errorf("unsupported Voxtral checkpoint %q", params.Model)
		}
	case "":
		transcriptionModelID = ModelWhisperX
	default:
		// New local adapters advertise their family and checkpoint instead of
		// requiring another hard-coded branch in every API and frontend.
		for _, id := range u.registry.GetTranscriptionModels() {
			adapter, lookupErr := u.registry.GetTranscriptionAdapter(id)
			if lookupErr != nil {
				continue
			}
			capability := adapter.GetCapabilities()
			if params.ModelFamily != id && params.ModelFamily != capability.ModelFamily {
				continue
			}
			for _, model := range adapter.GetSupportedModels() {
				if params.Model == "" || model == params.Model || id == params.Model {
					transcriptionModelID = id
					break
				}
			}
			if transcriptionModelID != "" {
				break
			}
		}
		if transcriptionModelID == "" {
			return "", "", fmt.Errorf("unsupported transcription model %q in family %q", params.Model, params.ModelFamily)
		}
	}

	// Determine diarization model if needed
	if params.Diarize {
		switch params.DiarizeModel {
		case DiarizeSortformer:
			diarizationModelID = ModelSortformer
		case ModelPyannote, ModelDiarization31:
			diarizationModelID = ModelPyannote
		case "pyannote/speaker-diarization-community-1", "":
			diarizationModelID = ModelPyannote
		case "native":
			adapter, lookupErr := u.registry.GetTranscriptionAdapter(transcriptionModelID)
			if lookupErr != nil || !adapter.GetCapabilities().Features["integrated_diarization"] {
				return "", "", fmt.Errorf("selected transcription model does not support native speaker attribution")
			}
		default:
			if _, lookupErr := u.registry.GetDiarizationAdapter(params.DiarizeModel); lookupErr != nil {
				return "", "", fmt.Errorf("unsupported diarization model %q", params.DiarizeModel)
			}
			diarizationModelID = params.DiarizeModel
		}
	}

	logger.Info("Selected models",
		"transcription", transcriptionModelID,
		"diarization", diarizationModelID,
		"original_family", params.ModelFamily,
		"original_diarize_model", params.DiarizeModel)

	return transcriptionModelID, diarizationModelID, nil
}

// transcriptionIncludesDiarization checks if the transcription model already includes diarization
func (u *UnifiedTranscriptionService) transcriptionIncludesDiarization(modelID string, params models.WhisperXParams) bool {
	if !params.Diarize {
		return false
	}
	if params.DiarizeModel == "native" {
		return true
	}
	if modelID != ModelWhisperX || resolvedDiarizationDevice(params) != nvidiaStringDefault(params.Device, "cpu") {
		return false
	}
	switch params.DiarizeModel {
	case "", ModelPyannote, ModelDiarization31, "pyannote/speaker-diarization-community-1":
		return true
	default:
		return false
	}
}

func diarizationFollowsASR(params models.WhisperXParams) bool {
	// Older standalone diarizers selected CUDA independently. Preserve that
	// behavior for saved profiles; new profiles explicitly choose "same".
	if params.DiarizationDevice == "" {
		switch params.ModelFamily {
		case FamilyNvidiaParakeet, FamilyNvidiaCanary, FamilyNvidiaCanaryQwen, FamilyMistralVoxtral:
			return false
		}
	}
	return params.DiarizationDevice == "" || params.DiarizationDevice == "same"
}

func resolvedDiarizationDevice(params models.WhisperXParams) string {
	if diarizationFollowsASR(params) {
		return nvidiaStringDefault(params.Device, "cpu")
	}
	if params.DiarizationDevice == "" {
		return "auto"
	}
	return params.DiarizationDevice
}

// ffprobeOutput represents the JSON output from ffprobe
type ffprobeOutput struct {
	Streams []struct {
		CodecType  string `json:"codec_type"`
		SampleRate string `json:"sample_rate"`
		Channels   int    `json:"channels"`
		Duration   string `json:"duration"`
		CodecName  string `json:"codec_name"`
		BitRate    string `json:"bit_rate"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
		Size     string `json:"size"`
	} `json:"format"`
}

// createAudioInput creates an AudioInput from a file path with real metadata
func (u *UnifiedTranscriptionService) createAudioInput(ctx context.Context, audioPath string) (interfaces.AudioInput, error) {
	// Get file info
	fileInfo, err := os.Stat(audioPath)
	if err != nil {
		return interfaces.AudioInput{}, fmt.Errorf("failed to stat audio file: %w", err)
	}

	// Determine format from extension
	ext := strings.ToLower(filepath.Ext(audioPath))
	format := strings.TrimPrefix(ext, ".")

	// Use ffprobe to get actual audio metadata
	audioInput := interfaces.AudioInput{
		FilePath: audioPath,
		Format:   format,
		Size:     fileInfo.Size(),
		Metadata: map[string]string{},
	}

	// Run ffprobe to get audio metadata
	cmd := processutil.CommandContext(ctx, "ffprobe",
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		audioPath)

	output, err := cmd.Output()
	if err != nil {
		logger.Warn("Failed to run ffprobe, using defaults", "error", err, "file", audioPath)
		// Fallback to defaults
		audioInput.SampleRate = 16000
		audioInput.Channels = 1
		audioInput.Duration = time.Duration(float64(fileInfo.Size()/32000)) * time.Second
		return audioInput, nil
	}

	// Parse ffprobe output
	var probeData ffprobeOutput
	if err := json.Unmarshal(output, &probeData); err != nil {
		logger.Warn("Failed to parse ffprobe output, using defaults", "error", err)
		audioInput.SampleRate = 16000
		audioInput.Channels = 1
		audioInput.Duration = time.Duration(float64(fileInfo.Size()/32000)) * time.Second
		return audioInput, nil
	}

	// Find the audio stream
	for _, stream := range probeData.Streams {
		if stream.CodecType == "audio" {
			// Parse sample rate
			if sampleRate, err := strconv.Atoi(stream.SampleRate); err == nil {
				audioInput.SampleRate = sampleRate
			} else {
				audioInput.SampleRate = 16000 // Default
			}

			// Set channels
			audioInput.Channels = stream.Channels
			if audioInput.Channels == 0 {
				audioInput.Channels = 1 // Default to mono
			}

			// Parse duration
			if duration, err := strconv.ParseFloat(stream.Duration, 64); err == nil {
				audioInput.Duration = time.Duration(duration * float64(time.Second))
			} else if duration, err := strconv.ParseFloat(probeData.Format.Duration, 64); err == nil {
				audioInput.Duration = time.Duration(duration * float64(time.Second))
			} else {
				// Fallback calculation
				audioInput.Duration = time.Duration(float64(fileInfo.Size()/32000)) * time.Second
			}

			// Store additional metadata
			audioInput.Metadata["codec"] = stream.CodecName
			if stream.BitRate != "" {
				audioInput.Metadata["bitrate"] = stream.BitRate
			}

			break
		}
	}

	// Set defaults if no audio stream found
	if audioInput.SampleRate == 0 {
		audioInput.SampleRate = 16000
	}
	if audioInput.Channels == 0 {
		audioInput.Channels = 1
	}

	logger.Info("Audio metadata extracted",
		"file", audioPath,
		"sample_rate", audioInput.SampleRate,
		"channels", audioInput.Channels,
		"duration", audioInput.Duration,
		"size", audioInput.Size)

	return audioInput, nil
}

// parametersToMap converts WhisperXParams to a generic parameter map
// convertParametersForModel converts WhisperX parameters to model-specific parameters
func (u *UnifiedTranscriptionService) convertParametersForModel(params models.WhisperXParams, modelID string) map[string]interface{} {
	var converted map[string]interface{}
	switch modelID {
	case ModelParakeet:
		converted = u.convertToParakeetParams(params)
	case ModelCanary:
		converted = u.convertToCanaryParams(params)
	case ModelCanaryQwen:
		converted = u.convertToCanaryQwenParams(params)
	case ModelWhisperX:
		converted = u.convertToWhisperXParams(params)
	case ModelPyannote:
		converted = u.convertToPyannoteParams(params)
	case ModelSortformer:
		converted = u.convertToSortformerParams(params)
	case ModelOpenAI:
		converted = u.convertToOpenAIParams(params)
	case ModelVoxtral:
		converted = u.convertToVoxtralParams(params)
	default:
		if _, err := u.registry.GetDiarizationAdapter(modelID); err == nil {
			converted = u.convertToPyannoteParams(params)
			if params.DiarizationCheckpoint != "" {
				converted["model"] = params.DiarizationCheckpoint
			}
		} else {
			converted = map[string]interface{}{
				"model": params.Model, "device": nvidiaStringDefault(params.Device, "cpu"),
				"precision":   nvidiaStringDefault(params.ComputeType, "float32"),
				"align_words": !params.NoAlign, "language": "en", "threads": params.Threads,
				"diarize": params.Diarize && params.DiarizeModel == "native", "diarize_model": params.DiarizeModel,
				"external_diarization_requested": params.Diarize && params.DiarizeModel != "native",
			}
			if params.Language != nil {
				converted["language"] = *params.Language
			}
			if params.MaxNewTokens != nil {
				converted["max_new_tokens"] = *params.MaxNewTokens
			}
			if params.AudioChunkDuration != nil {
				converted["chunk_duration"] = *params.AudioChunkDuration
			}
		}
	}
	if params.HfToken != nil && *params.HfToken != "" {
		converted["hf_token"] = *params.HfToken
	}
	if modelID == "vibevoice-bitnet" {
		converted["diarize"] = params.Diarize
	}
	// Unsupported recognizers and diarizers never receive transcription context.
	if adapter, err := u.registry.GetTranscriptionAdapter(modelID); err == nil && adapter.GetCapabilities().Features["context"] {
		if params.TranscriptionContext != nil && adapter.GetCapabilities().Metadata["context_mode"] != "terms" {
			converted["context"] = *params.TranscriptionContext
		}
		if params.TranscriptionContextTerms != nil {
			converted["context_terms"] = *params.TranscriptionContextTerms
		}
	}
	return converted
}

// convertToOpenAIParams converts to OpenAI-specific parameters
func (u *UnifiedTranscriptionService) convertToOpenAIParams(params models.WhisperXParams) map[string]interface{} {
	paramMap := map[string]interface{}{
		"model":       params.Model,
		"temperature": params.Temperature,
	}

	if params.Language != nil {
		paramMap["language"] = *params.Language
	}
	if params.InitialPrompt != nil {
		paramMap["prompt"] = *params.InitialPrompt
	}

	// Add API key if provided in params (e.g. from UI override)
	if params.APIKey != nil && *params.APIKey != "" {
		paramMap["api_key"] = *params.APIKey
	}

	return paramMap
}

// convertToVoxtralParams converts to Voxtral-specific parameters
func (u *UnifiedTranscriptionService) convertToVoxtralParams(params models.WhisperXParams) map[string]interface{} {
	// The legacy ID now uses the same bounded, aligned worker as exact variants.
	paramMap := map[string]interface{}{
		"device":      nvidiaStringDefault(params.Device, "cpu"),
		"precision":   nvidiaStringDefault(params.ComputeType, "float32"),
		"align_words": !params.NoAlign, "language": "en",
		"external_diarization_requested": params.Diarize && params.DiarizeModel != "native",
	}
	if params.Language != nil {
		paramMap["language"] = *params.Language
	}
	if params.MaxNewTokens != nil {
		paramMap["max_new_tokens"] = *params.MaxNewTokens
	}
	if params.AudioChunkDuration != nil {
		paramMap["chunk_duration"] = *params.AudioChunkDuration
	}
	return paramMap
}

// convertToParakeetParams converts to Parakeet-specific parameters
func (u *UnifiedTranscriptionService) convertToParakeetParams(params models.WhisperXParams) map[string]interface{} {
	return map[string]interface{}{
		"device":             nvidiaStringDefault(params.Device, "auto"),
		"timestamps":         nvidiaTimestamps(params),
		"context_left":       params.AttentionContextLeft,
		"context_right":      params.AttentionContextRight,
		"batch_size":         nvidiaBatchSize(params.BatchSize),
		"chunk_duration":     nvidiaChunkDuration(params, 300),
		"output_format":      OutputFormatJSON,
		"auto_convert_audio": true,
	}
}

// convertToCanaryParams converts to Canary-specific parameters
func (u *UnifiedTranscriptionService) convertToCanaryParams(params models.WhisperXParams) map[string]interface{} {
	paramMap := map[string]interface{}{
		"timestamps":         nvidiaTimestamps(params),
		"output_format":      OutputFormatJSON,
		"auto_convert_audio": true,
		"task":               params.Task,
		"batch_size":         nvidiaBatchSize(params.BatchSize),
		"chunking":           nvidiaBoolDefault(params.NvidiaUseChunking, false),
		"chunk_duration":     nvidiaChunkDuration(params, 40),
		"device":             nvidiaStringDefault(params.Device, "auto"),
		"precision":          nvidiaStringDefault(params.NvidiaPrecision, "float16"),
	}

	// Set source language
	if params.Language != nil {
		paramMap["source_lang"] = *params.Language
	} else {
		paramMap["source_lang"] = "en"
	}

	// Set target language for translation
	if params.Task == "translate" {
		paramMap["target_lang"] = nvidiaTargetLanguage(params)
	} else {
		paramMap["target_lang"] = paramMap["source_lang"]
	}

	return paramMap
}

// convertToCanaryQwenParams converts to Canary-Qwen-specific parameters.
func (u *UnifiedTranscriptionService) convertToCanaryQwenParams(params models.WhisperXParams) map[string]interface{} {
	paramMap := map[string]interface{}{
		"timestamps":         nvidiaTimestamps(params),
		"output_format":      OutputFormatJSON,
		"auto_convert_audio": true,
		"batch_size":         nvidiaBatchSize(params.BatchSize),
		"chunk_duration":     nvidiaChunkDuration(params, 40),
		"device":             nvidiaStringDefault(params.Device, "auto"),
		"precision":          nvidiaStringDefault(params.NvidiaPrecision, "float16"),
		"prompt":             "Transcribe the following:",
	}

	if params.MaxNewTokens != nil && *params.MaxNewTokens > 0 {
		paramMap["max_new_tokens"] = *params.MaxNewTokens
	} else {
		paramMap["max_new_tokens"] = 256
	}

	if params.Language != nil {
		paramMap["language"] = *params.Language
	} else {
		paramMap["language"] = "en"
	}

	if params.NvidiaPrompt != nil && strings.TrimSpace(*params.NvidiaPrompt) != "" {
		paramMap["prompt"] = strings.TrimSpace(*params.NvidiaPrompt)
	}

	return paramMap
}

func nvidiaTimestamps(params models.WhisperXParams) bool {
	if params.NvidiaTimestamps == nil {
		return true
	}
	return *params.NvidiaTimestamps
}

func nvidiaBoolDefault(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func nvidiaBatchSize(batchSize int) int {
	if batchSize < 1 {
		return 1
	}
	if batchSize > 8 {
		return 8
	}
	return batchSize
}

func nvidiaChunkDuration(params models.WhisperXParams, fallback int) int {
	if params.NvidiaChunkDuration > 0 {
		return params.NvidiaChunkDuration
	}
	return fallback
}

func nvidiaTargetLanguage(params models.WhisperXParams) string {
	if params.NvidiaTargetLanguage != nil && *params.NvidiaTargetLanguage != "" {
		return *params.NvidiaTargetLanguage
	}
	return "en"
}

func nvidiaStringDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

// convertToWhisperXParams converts to WhisperX-specific parameters
func (u *UnifiedTranscriptionService) convertToWhisperXParams(params models.WhisperXParams) map[string]interface{} {
	inlineDiarization := u.transcriptionIncludesDiarization(ModelWhisperX, params)
	inlineDiarizer := "pyannote"
	if inlineDiarization {
		inlineDiarizer = selectedPyannoteCheckpoint(params)
	}
	// For WhisperX, we use the standard WhisperX parameters (no NVIDIA-specific ones)
	paramMap := map[string]interface{}{
		// Core parameters
		"model":        params.Model,
		"device":       params.Device,
		"device_index": params.DeviceIndex,
		"batch_size":   params.BatchSize,
		"compute_type": params.ComputeType,
		"threads":      params.Threads,

		// Task and language
		"task": params.Task,

		// Diarization
		"diarize":       inlineDiarization,
		"diarize_model": inlineDiarizer,

		// Quality settings
		"temperature": params.Temperature,
		"best_of":     params.BestOf,
		"beam_size":   params.BeamSize,
		"patience":    params.Patience,

		// VAD settings
		"vad_method": params.VadMethod,
		"vad_onset":  params.VadOnset,
		"vad_offset": params.VadOffset,
	}

	// Handle pointer fields - only add if not nil
	if params.Language != nil {
		paramMap["language"] = *params.Language
	}
	if params.MinSpeakers != nil {
		paramMap["min_speakers"] = *params.MinSpeakers
	}
	if params.MaxSpeakers != nil {
		paramMap["max_speakers"] = *params.MaxSpeakers
	}
	if params.HfToken != nil {
		paramMap["hf_token"] = *params.HfToken
	}
	if params.ModelDir != nil {
		paramMap["model_dir"] = *params.ModelDir
	}
	if params.AlignModel != nil {
		paramMap["align_model"] = *params.AlignModel
	}
	if params.SuppressTokens != nil {
		paramMap["suppress_tokens"] = *params.SuppressTokens
	}
	if params.InitialPrompt != nil {
		paramMap["initial_prompt"] = *params.InitialPrompt
	}

	return paramMap
}

// convertToPyannoteParams converts to PyAnnote-specific parameters
func (u *UnifiedTranscriptionService) convertToPyannoteParams(params models.WhisperXParams) map[string]interface{} {
	paramMap := map[string]interface{}{
		"output_format":      OutputFormatJSON,
		"auto_convert_audio": true,
		"device":             resolvedDiarizationDevice(params),
	}
	if params.DiarizeModel == "" || params.DiarizeModel == ModelPyannote || params.DiarizeModel == ModelDiarization31 || params.DiarizeModel == "pyannote/speaker-diarization-community-1" {
		paramMap["model"] = selectedPyannoteCheckpoint(params)
	}

	if params.MinSpeakers != nil {
		paramMap["min_speakers"] = *params.MinSpeakers
	}
	if params.MaxSpeakers != nil {
		paramMap["max_speakers"] = *params.MaxSpeakers
	}
	if params.HfToken != nil {
		paramMap["hf_token"] = *params.HfToken
	}

	// Map VAD thresholds to Pyannote segmentation parameters
	// These control voice activity detection sensitivity for diarization
	if params.VadOnset > 0 {
		paramMap["segmentation_onset"] = params.VadOnset
	}
	if params.VadOffset > 0 {
		paramMap["segmentation_offset"] = params.VadOffset
	}

	return paramMap
}

// convertToSortformerParams converts to Sortformer-specific parameters
func (u *UnifiedTranscriptionService) convertToSortformerParams(params models.WhisperXParams) map[string]interface{} {
	return map[string]interface{}{
		"output_format":      OutputFormatJSON,
		"auto_convert_audio": true,
		"device":             resolvedDiarizationDevice(params),
	}
}

func (u *UnifiedTranscriptionService) parametersToMap(params models.WhisperXParams) map[string]interface{} {
	paramMap := map[string]interface{}{
		// Core parameters
		"model":        params.Model,
		"device":       params.Device,
		"device_index": params.DeviceIndex,
		"batch_size":   params.BatchSize,
		"compute_type": params.ComputeType,
		"threads":      params.Threads,

		// Language and task
		"task": params.Task,

		// Diarization
		"diarize":       params.Diarize,
		"diarize_model": params.DiarizeModel,
	}

	// Handle pointer fields - only add if not nil
	if params.Language != nil {
		paramMap["language"] = *params.Language
	}
	if params.MinSpeakers != nil {
		paramMap["min_speakers"] = *params.MinSpeakers
	}
	if params.MaxSpeakers != nil {
		paramMap["max_speakers"] = *params.MaxSpeakers
	}
	if params.HfToken != nil {
		paramMap["hf_token"] = *params.HfToken
	}
	if params.ModelDir != nil {
		paramMap["model_dir"] = *params.ModelDir
	}
	if params.AlignModel != nil {
		paramMap["align_model"] = *params.AlignModel
	}
	if params.SuppressTokens != nil {
		paramMap["suppress_tokens"] = *params.SuppressTokens
	}
	if params.InitialPrompt != nil {
		paramMap["initial_prompt"] = *params.InitialPrompt
	}

	// Add remaining non-pointer fields
	paramMap["temperature"] = params.Temperature
	paramMap["best_of"] = params.BestOf
	paramMap["beam_size"] = params.BeamSize
	paramMap["patience"] = params.Patience
	paramMap["vad_method"] = params.VadMethod
	paramMap["vad_onset"] = params.VadOnset
	paramMap["vad_offset"] = params.VadOffset
	paramMap["context_left"] = params.AttentionContextLeft
	paramMap["context_right"] = params.AttentionContextRight
	paramMap["timestamps"] = true
	paramMap["output_format"] = OutputFormatJSON
	paramMap["auto_convert_audio"] = true

	// For Canary model, set source and target languages
	if params.ModelFamily == FamilyNvidiaCanary {
		if params.Language != nil {
			paramMap["source_lang"] = *params.Language
		} else {
			paramMap["source_lang"] = "en"
		}

		if params.Task == "translate" {
			paramMap["target_lang"] = "en" // Default target for translation
		} else {
			paramMap["target_lang"] = paramMap["source_lang"]
		}
	}

	return paramMap
}

// mergeDiarizationWithTranscription combines diarization results with transcription
func (u *UnifiedTranscriptionService) mergeDiarizationWithTranscription(transcript *interfaces.TranscriptResult, diarization *interfaces.DiarizationResult) *interfaces.TranscriptResult {
	logger.Info("Merging diarization with transcription",
		"transcript_segments", len(transcript.Segments),
		"diarization_segments", len(diarization.Segments))

	// Create a copy of the transcript to avoid modifying the original
	mergedTranscript := *transcript
	mergedTranscript.Segments = make([]interfaces.TranscriptSegment, len(transcript.Segments))
	copy(mergedTranscript.Segments, transcript.Segments)

	// Assign speakers to transcript segments based on timing overlap
	for i := range mergedTranscript.Segments {
		segment := &mergedTranscript.Segments[i]
		bestSpeaker := u.findBestSpeakerForSegment(segment.Start, segment.End, diarization.Segments)
		if bestSpeaker != "" {
			segment.Speaker = &bestSpeaker
		}
	}

	// Also assign speakers to words if available
	if len(transcript.WordSegments) > 0 {
		mergedTranscript.WordSegments = make([]interfaces.TranscriptWord, len(transcript.WordSegments))
		copy(mergedTranscript.WordSegments, transcript.WordSegments)

		for i := range mergedTranscript.WordSegments {
			word := &mergedTranscript.WordSegments[i]
			bestSpeaker := u.findBestSpeakerForSegment(word.Start, word.End, diarization.Segments)
			if bestSpeaker != "" {
				word.Speaker = &bestSpeaker
			}
		}
	}

	return &mergedTranscript
}

// findBestSpeakerForSegment finds the speaker with maximum overlap for a given time segment
func (u *UnifiedTranscriptionService) findBestSpeakerForSegment(start, end float64, diarizationSegments []interfaces.DiarizationSegment) string {
	maxOverlap := 0.0
	bestSpeaker := ""

	for _, diarSeg := range diarizationSegments {
		// Calculate overlap
		overlapStart := max(start, diarSeg.Start)
		overlapEnd := min(end, diarSeg.End)
		overlap := max(0, overlapEnd-overlapStart)

		if overlap > maxOverlap {
			maxOverlap = overlap
			bestSpeaker = diarSeg.Speaker
		}
	}

	return bestSpeaker
}

// saveTranscriptionResults saves the transcription results to the database and returns the serialized transcript.
func (u *UnifiedTranscriptionService) saveTranscriptionResults(jobID string, result *interfaces.TranscriptResult) (string, error) {
	// Convert result to JSON string for database storage
	resultJSON, err := u.convertTranscriptResultToJSON(result)
	if err != nil {
		return "", fmt.Errorf("failed to convert result to JSON: %w", err)
	}

	// Update the job in the database
	if err := u.jobRepo.UpdateTranscript(context.Background(), jobID, resultJSON); err != nil {
		return "", fmt.Errorf("failed to update job transcript: %w", err)
	}

	logger.Info("Saved transcription results", "job_id", jobID, "text_length", len(result.Text))
	return resultJSON, nil
}

// convertTranscriptResultToJSON converts the interface result to JSON format
func (u *UnifiedTranscriptionService) convertTranscriptResultToJSON(result *interfaces.TranscriptResult) (string, error) {
	// Now that the struct fields match the JSON field names, we can directly marshal
	jsonBytes, err := json.Marshal(result)
	if err != nil {
		return "", err
	}

	return string(jsonBytes), nil
}

// GetSupportedModels returns all supported models through the new architecture
func (u *UnifiedTranscriptionService) GetSupportedModels() map[string]interfaces.ModelCapabilities {
	return withModelComparisonMetadata(u.registry.GetAllCapabilities())
}

// GetModelStatus returns the status of all models
func (u *UnifiedTranscriptionService) GetModelStatus(ctx context.Context) map[string]bool {
	return u.registry.GetModelStatus(ctx)
}

// ValidateModelParameters validates parameters for a specific model
func (u *UnifiedTranscriptionService) ValidateModelParameters(modelID string, params map[string]interface{}) error {
	return u.registry.ValidateModelParameters(modelID, params)
}

// Helper functions
func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
