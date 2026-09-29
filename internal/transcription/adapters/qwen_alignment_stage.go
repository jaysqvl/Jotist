package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"scriberr/internal/processutil"
	"scriberr/internal/transcription/interfaces"
)

func runQwenAlignmentWorker(ctx context.Context, environment string, input interfaces.AudioInput, params map[string]interface{}, procCtx interfaces.ProcessingContext, upstream []byte, modelUsed string, metadata map[string]string) ([]byte, error) {
	if err := os.MkdirAll(procCtx.TempDirectory, 0700); err != nil {
		return nil, err
	}
	tempDir, err := os.MkdirTemp(procCtx.TempDirectory, "qwen-alignment-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tempDir)
	transcriptPath := filepath.Join(tempDir, "recognition.json")
	outputPath := filepath.Join(tempDir, "result.json")
	if err := os.WriteFile(transcriptPath, upstream, 0600); err != nil {
		return nil, err
	}
	device, _ := params["device"].(string)
	if device == "" || device == "auto" {
		device = "cpu"
	}
	precision, _ := params["precision"].(string)
	if precision == "" || device == "cpu" {
		precision = "float32"
	}
	language, _ := params["language"].(string)
	if language == "" {
		language = "en"
	}
	args := []string{"run", "--no-sync", "--project", environment, "python", filepath.Join(environment, "align_transcript.py"),
		"--audio", input.FilePath, "--transcript", transcriptPath, "--output", outputPath,
		"--device", device, "--precision", precision, "--language", language}
	env := withEnvironmentValue(os.Environ(), "PYTHONUNBUFFERED", "1")
	if token, _ := params["hf_token"].(string); token != "" {
		env = withEnvironmentValue(env, "HF_TOKEN", token)
	}
	if device == "cpu" {
		env = withEnvironmentValue(env, "CUDA_VISIBLE_DEVICES", "")
	}
	phase := "alignment"
	appendLocalASRDiagnostic(procCtx.OutputDirectory, phase, nil, nil)
	started := time.Now()
	cmd := processutil.CommandContext(ctx, "uv", args...)
	cmd.Env = env
	if log, logErr := os.OpenFile(filepath.Join(procCtx.OutputDirectory, "transcription.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600); logErr == nil {
		defer log.Close()
		cmd.Stdout, cmd.Stderr = log, log
	}
	if err := processutil.Run(ctx, cmd); err != nil {
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
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, localASRDiagnostic("worker_output_invalid", err)
	}
	if result.Metadata == nil {
		result.Metadata = map[string]string{}
	}
	validTiming := func(start, end float64) bool {
		return !math.IsNaN(start) && !math.IsNaN(end) && !math.IsInf(start, 0) && !math.IsInf(end, 0) && start >= 0 && end >= start
	}
	for _, segment := range result.Segments {
		if !validTiming(segment.Start, segment.End) {
			return nil, localASRDiagnostic("worker_output_invalid", fmt.Errorf("invalid aligned segment timing"))
		}
	}
	for _, word := range result.WordSegments {
		if !validTiming(word.Start, word.End) {
			return nil, localASRDiagnostic("worker_output_invalid", fmt.Errorf("invalid aligned word timing"))
		}
	}
	if strings.TrimSpace(result.Text) != "" && len(result.Segments) == 0 {
		return nil, localASRDiagnostic("worker_output_invalid", fmt.Errorf("aligned text has no segments"))
	}
	for key, value := range metadata {
		if _, exists := result.Metadata[key]; !exists {
			result.Metadata[key] = value
		}
	}
	result.ProcessingTime = time.Since(started)
	result.ModelUsed = modelUsed
	return json.Marshal(&result)
}

func transcriptHasLexicalText(result *interfaces.TranscriptResult) bool {
	for _, value := range result.Text {
		if unicode.IsLetter(value) || unicode.IsNumber(value) {
			return true
		}
	}
	return false
}

func validateAlignedTranscript(data []byte) (*interfaces.TranscriptResult, error) {
	var result interfaces.TranscriptResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse aligned transcript: %w", err)
	}
	if transcriptHasLexicalText(&result) && len(result.WordSegments) == 0 {
		return nil, localASRDiagnostic("application_e3029a2f37f6", nil)
	}
	return &result, nil
}
