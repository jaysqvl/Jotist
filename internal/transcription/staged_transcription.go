package transcription

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"scriberr/internal/repository"
	"scriberr/internal/transcription/interfaces"
)

// Each declared boundary commits before the next subprocess starts. The raw
// recognition artifact retains backend state needed for exact alignment resume.
func runRecoverableTranscription(ctx context.Context, recovery recoveryStageContext, adapter interfaces.TranscriptionAdapter, input interfaces.AudioInput, params map[string]interface{}, proc interfaces.ProcessingContext) (*interfaces.TranscriptResult, map[string]string, error) {
	staged, ok := adapter.(interfaces.StagedTranscriptionAdapter)
	if !ok || recovery.store == nil {
		return runRecoverableStage(ctx, recovery, "combined", "asr", adapter, input, params, proc, func(actual map[string]interface{}) (*interfaces.TranscriptResult, error) {
			return adapter.Transcribe(ctx, input, actual, proc)
		})
	}
	var upstream json.RawMessage
	var result *interfaces.TranscriptResult
	var recognitionMetadata map[string]string
	finalMetadata := map[string]string{}
	for _, descriptor := range staged.Stages() {
		if descriptor.Kind == "alignment" && params["timestamps"] == false {
			continue
		}
		if !descriptor.Recoverable {
			return nil, nil, fmt.Errorf("%s does not expose a recoverable boundary", descriptor.Kind)
		}
		stageParams := copyStageParameters(params)
		if resolver, ok := adapter.(interfaces.StageParameterResolver); ok {
			var err error
			stageParams, err = resolver.ResolveStageParameters(descriptor, stageParams, upstream)
			if err != nil {
				return nil, nil, err
			}
		}
		if recovery.mode == "" && descriptor.Kind == "alignment" && recognitionMetadata["resolved_device"] == "cpu" {
			stageParams["device"] = "cpu"
		}
		stageRecovery := recovery
		stageRecovery.descriptor = &descriptor
		data, metadata, err := runRecoverableStage(ctx, stageRecovery, descriptor.Kind, descriptor.Kind, adapter, input, stageParams, proc, func(actual map[string]interface{}) (json.RawMessage, error) {
			output, err := staged.RunStage(ctx, descriptor, input, actual, proc, upstream)
			return json.RawMessage(output), err
		})
		if err != nil {
			return nil, nil, err
		}
		var decoded interfaces.TranscriptResult
		if json.Unmarshal(data, &decoded) != nil {
			return nil, nil, repository.ErrRecoveryCorrupt
		}
		if decoded.Metadata == nil {
			decoded.Metadata = map[string]string{}
		}
		for key, value := range metadata {
			decoded.Metadata[key] = value
		}
		if descriptor.Kind == "recognition" {
			recognitionMetadata = map[string]string{}
			for key, value := range decoded.Metadata {
				recognitionMetadata[key] = value
				if strings.HasPrefix(key, "recognition_device_") || key == "recognition_fallback_reason" {
					alias := "asr_" + strings.TrimPrefix(key, "recognition_")
					recognitionMetadata[alias] = value
					decoded.Metadata[alias] = value
				}
			}
			decoded.Metadata["recognition_device"] = decoded.Metadata["resolved_device"]
			decoded.Metadata["recognition_precision"] = decoded.Metadata["precision"]
		} else if descriptor.Kind == "alignment" {
			decoded.Metadata["alignment_device"] = decoded.Metadata["resolved_device"]
			decoded.Metadata["alignment_precision"] = decoded.Metadata["precision"]
			decoded.Metadata["alignment_batch_size"] = decoded.Metadata["actual_batch_size"]
			decoded.Metadata["alignment_window_seconds"] = decoded.Metadata["actual_window_seconds"]
			// A CPU aligner does not relabel recognition or change a diarizer following
			// the recognizer's actual device. Each stage retains its own provenance.
			for _, key := range []string{"resolved_device", "precision", "actual_batch_size", "actual_window_seconds"} {
				decoded.Metadata[key] = recognitionMetadata[key]
			}
			decoded.Metadata["recognition_device"] = recognitionMetadata["resolved_device"]
			decoded.Metadata["recognition_precision"] = recognitionMetadata["precision"]
			decoded.Metadata["resolved_precision"] = recognitionMetadata["precision"]
			for key, value := range recognitionMetadata {
				if strings.HasPrefix(key, "recognition_device_") || key == "recognition_fallback_reason" {
					decoded.Metadata[key] = value
				}
				if strings.HasPrefix(key, "asr_") {
					decoded.Metadata[key] = value
				}
			}
		}
		if metadata["checkpoint_id"] == "" || metadata["checkpoint_result_sha256"] == "" {
			return nil, nil, fmt.Errorf("%s did not produce a durable upstream checkpoint", descriptor.Kind)
		}
		recovery.upstream = []repository.CheckpointInput{{ID: metadata["checkpoint_id"], ResultSHA256: metadata["checkpoint_result_sha256"]}}
		upstream = data
		result = &decoded
	}
	if result == nil {
		return nil, nil, fmt.Errorf("adapter exposed no transcription stages")
	}
	for key, value := range result.Metadata {
		finalMetadata[key] = value
	}
	return result, finalMetadata, nil
}
