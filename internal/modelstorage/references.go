package modelstorage

import (
	"strings"

	"github.com/jaysqvl/Jotist/internal/models"
)

// ReferencesSelection describes a saved or executed configuration, separately
// from the intentionally conservative deletion guard. A Whisper small profile
// must not count against every Whisper size; a chosen speaker checkpoint must
// not count against all the other diarizers. Automatic/shared dependencies may
// match several configurations because their selection happens at runtime.
func ReferencesSelection(model Model, params models.WhisperXParams) bool {
	if strings.HasPrefix(model.Source, "Systran/faster-whisper-") {
		if params.ModelFamily != "" && params.ModelFamily != "whisper" {
			return false
		}
		size := params.Model
		if size == "" {
			size = "small"
		}
		if size == "large" {
			size = "large-v3"
		}
		if strings.Contains(size, "/") {
			return model.Source == size
		}
		return model.Source == "Systran/faster-whisper-"+size
	}
	switch model.Source {
	case "pyannote/speaker-diarization-3.1", "pyannote/speaker-diarization-community-1",
		"BUT-FIT/diarizen-wavlm-large-s80-md-v2", "rewayai/suplime", "rewayai/suplime-large":
		if !params.Diarize || params.DiarizeModel == "native" {
			return false
		}
		checkpoint := params.DiarizationCheckpoint
		if checkpoint == "" {
			switch params.DiarizeModel {
			case "", "pyannote", "pyannote_community":
				checkpoint = "pyannote/speaker-diarization-community-1"
			case "diarizen":
				checkpoint = "BUT-FIT/diarizen-wavlm-large-s80-md-v2"
			case "suplime":
				checkpoint = "rewayai/suplime-large"
			default:
				checkpoint = params.DiarizeModel
			}
		}
		return checkpoint == model.Source
	default:
		return Referenced(model, RequestUse(params, "", "", ""))
	}
}

// SavedProfileReference labels the existing conservative reference projection.
// It does not change admission, removal or cached-only retention protection.
func SavedProfileReference(model Model, profile models.TranscriptionProfile) ProfileReference {
	params := profile.Parameters
	ref := ProfileReference{ID: profile.ID, Name: profile.Name, Role: model.Purpose, CachedOnly: params.ModelCacheOnly}
	switch model.Purpose {
	case "Recognition", "Recognition and word timing":
	case "Word timing":
		ref.Automatic = params.AlignModel == nil || *params.AlignModel != model.Source
	case "Speakers":
		selected := params.DiarizationCheckpoint
		if selected == "" {
			switch params.DiarizeModel {
			case "", "pyannote", "pyannote_community":
				selected = "pyannote/speaker-diarization-community-1"
			case "diarizen":
				selected = "BUT-FIT/diarizen-wavlm-large-s80-md-v2"
			case "suplime":
				selected = "rewayai/suplime-large"
			case "nvidia_sortformer":
				selected = "nvidia/diar_streaming_sortformer_4spk-v2.1"
			default:
				selected = params.DiarizeModel
			}
		}
		ref.Automatic = selected != model.Source
	default:
		ref.Automatic = true
	}
	return ref
}
