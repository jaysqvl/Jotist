package modelstorage

import "github.com/jaysqvl/Jotist/internal/models"

// RequestUse identifies shared dependencies as well as recognition weights.
// Automatic alignment/language/speaker selection is deliberately conservative:
// active runs protect those files; queued runs show re-download warnings only.
func RequestUse(params models.WhisperXParams, recordingID, title, state string) Use {
	keys := []string{params.Model}
	if params.ModelFamily != "openai" {
		keys = append(keys, "@vad")
	}
	switch params.ModelFamily {
	case "", "whisper":
		keys = append(keys, "whisperx", "@vad")
	case "nvidia_parakeet":
		keys = append(keys, "parakeet")
	case "nvidia_canary":
		keys = append(keys, "canary")
	case "nvidia_canary_qwen":
		keys = append(keys, "canary_qwen")
	case "mistral_voxtral":
		keys = append(keys, "voxtral")
	case "vibevoice_bitnet", "vibevoice-bitnet":
		keys = append(keys, "vibevoice-bitnet")
	}
	if !params.NoAlign {
		keys = append(keys, "@alignment")
	}
	if params.AlignModel != nil {
		keys = append(keys, *params.AlignModel)
	}
	if params.Diarize {
		keys = append(keys, "@speakers", params.DiarizeModel, params.DiarizationCheckpoint)
		switch params.DiarizeModel {
		case "", "pyannote", "pyannote_community":
			keys = append(keys, "pyannote")
		case "nvidia_sortformer":
			keys = append(keys, "sortformer")
		}
	}
	return Use{Resources: keys, RecordingID: recordingID, RecordingTitle: title, State: state, CachedOnly: params.ModelCacheOnly}
}

func StageUse(modelID, kind, recordingID string, params map[string]interface{}) Use {
	keys := []string{modelID}
	for _, key := range []string{"model", "align_model", "diarize_model"} {
		if value, ok := params[key].(string); ok {
			keys = append(keys, value)
		}
	}
	switch kind {
	case "alignment", "align":
		keys = append(keys, "@alignment")
	case "diarization", "diarize":
		keys = append(keys, "@speakers")
	case "recognition", "recognize", "combined":
		keys = append(keys, "@vad")
		// Combined adapters may load auxiliary stages inside recognition.
		if params["align_words"] != false && params["no_align"] != true {
			keys = append(keys, "@alignment")
		}
		if params["diarize"] == true {
			keys = append(keys, "@speakers")
		}
	}
	return Use{Resources: keys, RecordingID: recordingID, Stage: kind, State: "in_use"}
}
