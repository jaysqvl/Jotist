package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"scriberr/internal/processutil"
	"scriberr/internal/transcription/interfaces"
)

// WhisperX already releases each model before loading the next one. These
// descriptors make those existing process boundaries durable so alignment or
// inline diarization can resume without repeating recognition.
func (w *WhisperXAdapter) Stages() []interfaces.StageDescriptor {
	return []interfaces.StageDescriptor{
		{
			Kind: "recognition", SchemaVersion: "transcript-result-v1", ImplementationVersion: "whisperx-recognition-v1",
			Recoverable: true, Cancellable: true,
			DevicePrecisions:   map[string][]string{"cpu": {"float32", "int8"}, "cuda": {"float16", "float32", "int8"}},
			PrecisionParameter: "compute_type", BatchParameter: "batch_size", QualifiedBatches: []int{1, 2, 4, 8},
			QualificationNotes: []string{"Recognition commits before WhisperX loads an optional alignment or diarization model."},
		},
		{
			Kind: "alignment", SchemaVersion: "transcript-result-v1", ImplementationVersion: "whisperx-alignment-v1",
			Recoverable: true, Cancellable: true,
			DevicePrecisions: map[string][]string{"cpu": {"float32"}, "cuda": {"float32"}}, DefaultPrecision: "float32",
			QualificationNotes: []string{"Alignment consumes the durable recognition transcript and original audio without loading Whisper."},
		},
		{
			Kind: "speaker_assignment", SchemaVersion: "transcript-result-v1", ImplementationVersion: "whisperx-diarization-v1",
			Recoverable: true, Cancellable: true,
			DevicePrecisions: map[string][]string{"cpu": {"float32"}, "cuda": {"float32"}}, DefaultPrecision: "float32",
			QualificationNotes: []string{"Inline Pyannote speaker assignment consumes the durable aligned transcript and original audio."},
		},
	}
}

func (w *WhisperXAdapter) RecoveryStage(kind string) (interfaces.StageDescriptor, bool) {
	if kind == "diarization" {
		kind = "speaker_assignment"
	}
	for _, stage := range w.Stages() {
		if stage.Kind == kind {
			return stage, true
		}
	}
	return interfaces.StageDescriptor{}, false
}

func (w *WhisperXAdapter) GetCapabilities() interfaces.ModelCapabilities {
	capabilities := w.BaseAdapter.GetCapabilities()
	metadata := make(map[string]string, len(capabilities.Metadata)+2)
	for key, value := range capabilities.Metadata {
		metadata[key] = value
	}
	encoded, _ := json.Marshal(w.Stages())
	metadata["adaptive_stages"] = string(encoded)
	metadata["resilience_contract"] = "durable_recognition_alignment_inline_diarization"
	capabilities.Metadata = metadata
	return capabilities
}

func (w *WhisperXAdapter) ResolveStageParameters(stage interfaces.StageDescriptor, params map[string]interface{}, _ []byte) (map[string]interface{}, error) {
	if _, ok := w.RecoveryStage(stage.Kind); !ok {
		return nil, fmt.Errorf("unsupported WhisperX stage %q", stage.Kind)
	}
	resolved := copyAdapterParameters(params)
	switch stage.Kind {
	case "recognition":
		resolved["no_align"] = true
		resolved["diarize"] = false
	case "alignment":
		resolved["no_align"] = false
		resolved["diarize"] = false
	}
	return resolved, nil
}

func (w *WhisperXAdapter) RunStage(ctx context.Context, stage interfaces.StageDescriptor, input interfaces.AudioInput, params map[string]interface{}, procCtx interfaces.ProcessingContext, upstream []byte) ([]byte, error) {
	known, ok := w.RecoveryStage(stage.Kind)
	if !ok || stage.SchemaVersion != known.SchemaVersion || stage.ImplementationVersion != known.ImplementationVersion {
		return nil, fmt.Errorf("unsupported WhisperX stage contract")
	}
	resolved, err := w.ResolveStageParameters(stage, params, upstream)
	if err != nil {
		return nil, err
	}
	if stage.Kind == "recognition" {
		result, err := w.Transcribe(ctx, input, resolved, procCtx)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	}
	if stage.Kind != "alignment" && stage.Kind != "speaker_assignment" {
		return nil, fmt.Errorf("unsupported WhisperX stage %q", stage.Kind)
	}
	return w.runPostRecognitionStage(ctx, stage.Kind, input, resolved, procCtx, upstream)
}

func (w *WhisperXAdapter) runPostRecognitionStage(ctx context.Context, kind string, input interfaces.AudioInput, params map[string]interface{}, procCtx interfaces.ProcessingContext, upstream []byte) ([]byte, error) {
	var prior interfaces.TranscriptResult
	if json.Unmarshal(upstream, &prior) != nil {
		return nil, fmt.Errorf("invalid WhisperX upstream checkpoint")
	}
	if err := os.MkdirAll(procCtx.TempDirectory, 0700); err != nil {
		return nil, err
	}
	tempDir, err := os.MkdirTemp(procCtx.TempDirectory, "whisperx-"+kind+"-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tempDir)
	transcriptPath := filepath.Join(tempDir, "upstream.json")
	outputPath := filepath.Join(tempDir, "result.json")
	if err := os.WriteFile(transcriptPath, upstream, 0600); err != nil {
		return nil, err
	}
	environment := filepath.Join(w.envPath, "WhisperX")
	pythonStage := kind
	if kind == "speaker_assignment" {
		pythonStage = "diarization"
	}
	args := []string{"run", "--system-certs", "--project", environment, "python", "-I", filepath.Join(environment, "whisperx_stage.py"),
		"--stage", pythonStage, "--audio", input.FilePath, "--transcript", transcriptPath, "--output", outputPath,
		"--device", w.GetStringParameter(params, "device")}
	language := prior.Language
	if language == "" {
		language = w.GetStringParameter(params, "language")
	}
	if language != "" && language != "auto" {
		args = append(args, "--language", language)
	}
	if modelDir := w.GetStringParameter(params, "model_dir"); modelDir != "" {
		args = append(args, "--model-dir", modelDir)
	}
	if w.GetBoolParameter(params, "model_cache_only") {
		args = append(args, "--model-cache-only")
	}
	if kind == "alignment" {
		args = append(args, "--interpolate-method", w.GetStringParameter(params, "interpolate_method"))
		if alignModel := w.GetStringParameter(params, "align_model"); alignModel != "" {
			args = append(args, "--align-model", alignModel)
		}
		if w.GetBoolParameter(params, "return_char_alignments") {
			args = append(args, "--return-char-alignments")
		}
	} else {
		diarizeModel := w.GetStringParameter(params, "diarize_model")
		if diarizeModel == "pyannote" || diarizeModel == "" {
			diarizeModel = "pyannote/speaker-diarization-community-1"
		}
		args = append(args, "--diarize-model", diarizeModel)
		if minSpeakers := w.GetIntParameter(params, "min_speakers"); minSpeakers > 0 {
			args = append(args, "--min-speakers", strconv.Itoa(minSpeakers))
		}
		if maxSpeakers := w.GetIntParameter(params, "max_speakers"); maxSpeakers > 0 {
			args = append(args, "--max-speakers", strconv.Itoa(maxSpeakers))
		}
	}

	env := append(os.Environ(), "PYTHONUNBUFFERED=1")
	if nvidiaPaths, pathErr := w.findNvidiaLibPaths(); pathErr == nil && len(nvidiaPaths) > 0 {
		newPath := strings.Join(nvidiaPaths, string(os.PathListSeparator))
		if existing := os.Getenv("LD_LIBRARY_PATH"); existing != "" {
			newPath += string(os.PathListSeparator) + existing
		}
		env = withEnvironmentValue(env, "LD_LIBRARY_PATH", newPath)
	}
	env = withRequestedDevice(env, w.GetStringParameter(params, "device"))
	if token := w.GetStringParameter(params, "hf_token"); token != "" {
		env = withEnvironmentValue(env, "HF_TOKEN", token)
	}
	cmd := processutil.CommandContext(ctx, "uv", args...)
	cmd.Env = env
	phase := kind
	appendLocalASRDiagnostic(procCtx.OutputDirectory, phase, nil, nil)
	log, logErr := os.OpenFile(filepath.Join(procCtx.OutputDirectory, "transcription.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if logErr == nil {
		defer log.Close()
		cmd.Stdout, cmd.Stderr = log, log
	}
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if data, readErr := os.ReadFile(filepath.Join(tempDir, "error.json")); readErr == nil {
			failure, fields := localASRWorkerDiagnostic(data, err)
			appendLocalASRDiagnostic(procCtx.OutputDirectory, phase, failure, fields)
			return nil, failure
		}
		failure := localASRWorkerStartError(err)
		appendLocalASRDiagnostic(procCtx.OutputDirectory, phase, failure, nil)
		return nil, failure
	}
	data, err := os.ReadFile(outputPath)
	if err != nil {
		return nil, localASRDiagnostic("worker_output_invalid", err)
	}
	var result interfaces.TranscriptResult
	if json.Unmarshal(data, &result) != nil || result.Metadata == nil {
		return nil, localASRDiagnostic("worker_output_invalid", fmt.Errorf("invalid WhisperX %s output", kind))
	}
	return data, nil
}

func copyAdapterParameters(params map[string]interface{}) map[string]interface{} {
	copy := make(map[string]interface{}, len(params)+2)
	for key, value := range params {
		copy[key] = value
	}
	return copy
}

var _ interfaces.StagedTranscriptionAdapter = (*WhisperXAdapter)(nil)
var _ interfaces.StageParameterResolver = (*WhisperXAdapter)(nil)
