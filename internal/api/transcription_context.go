package api

import (
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"scriberr/internal/models"
	"scriberr/internal/transcription"
	"scriberr/internal/transcription/adapters"
)

func validateContext(context, terms *string) error {
	for _, field := range []struct {
		name  string
		value *string
		limit int
	}{{"Transcription context", context, 4000}, {"Vocabulary", terms, 8000}} {
		if field.value != nil && (!utf8.ValidString(*field.value) || strings.ContainsRune(*field.value, '\x00') || utf8.RuneCountInString(*field.value) > field.limit) {
			return fmt.Errorf("%s must be valid text of at most %d characters", field.name, field.limit)
		}
	}
	return nil
}

func validateHFToken(token *string) error {
	if token == nil {
		return nil
	}
	value := strings.TrimSpace(*token)
	if len(value) > 4096 || strings.IndexFunc(value, func(r rune) bool { return r < 33 || r > 126 }) >= 0 {
		return fmt.Errorf("Hugging Face token must be a single value of at most 4096 ASCII characters")
	}
	return nil
}

func cloudASRWithoutDiarization(params models.WhisperXParams) bool {
	return (params.ModelFamily == "openai" || params.ModelFamily == "openai_whisper") && !params.Diarize
}

// Profile updates arrive without their write-only token. An explicit source
// change takes precedence over retaining that token; omission preserves it.
func prepareProfileHFToken(params *models.WhisperXParams, previous *models.WhisperXParams) error {
	if params.HFTokenSource == "" && params.HfToken == nil && previous != nil {
		params.HFTokenSource = previous.EffectiveHFTokenSource()
		params.HfToken = previous.HfToken
	}
	source := params.EffectiveHFTokenSource()
	switch source {
	case "default", "none":
		params.HfToken = nil
	case "custom":
		if params.HfToken == nil && previous != nil && previous.EffectiveHFTokenSource() == "custom" {
			params.HfToken = previous.HfToken
		}
		if params.HfToken == nil || strings.TrimSpace(*params.HfToken) == "" {
			if !cloudASRWithoutDiarization(*params) {
				return fmt.Errorf("Enter a custom Hugging Face token or choose the saved default")
			}
		} else {
			value := strings.TrimSpace(*params.HfToken)
			params.HfToken = &value
		}
	default:
		return fmt.Errorf("hf_token_source must be default, custom, or none")
	}
	params.HFTokenSource = source
	params.HFTokenResolved = false
	return validateHFToken(params.HfToken)
}

// resolveTranscriptionContext snapshots defaults at admission, never during
// execution. Editing Settings cannot change a run that is already queued.
func (h *Handler) resolveTranscriptionContext(c *gin.Context, params *models.WhisperXParams) error {
	if err := validateContext(params.TranscriptionContext, params.TranscriptionContextTerms); err != nil {
		return err
	}
	if err := validateHFTokenOptions(*params); err != nil {
		return err
	}
	source := params.EffectiveHFTokenSource()
	inheritToken := !params.HFTokenResolved && source == "default"
	defaultToken := ""
	if params.TranscriptionContext == nil || params.TranscriptionContextTerms == nil || inheritToken {
		if userID, ok := c.Get("user_id"); ok && h.userRepo != nil {
			id, valid := userID.(uint)
			if !valid {
				return fmt.Errorf("invalid user identity")
			}
			user, err := h.userRepo.FindByID(c.Request.Context(), id)
			if err != nil {
				return fmt.Errorf("failed to load transcription defaults")
			}
			if params.TranscriptionContext == nil {
				value := user.TranscriptionContext
				params.TranscriptionContext = &value
			}
			if params.TranscriptionContextTerms == nil {
				value := user.TranscriptionContextTerms
				params.TranscriptionContextTerms = &value
			}
			if inheritToken {
				defaultToken = user.HFToken
			}
		}
	}
	if params.TranscriptionContext == nil {
		value := ""
		params.TranscriptionContext = &value
	}
	if params.TranscriptionContextTerms == nil {
		value := ""
		params.TranscriptionContextTerms = &value
	}
	if !params.HFTokenResolved {
		value := ""
		switch source {
		case "default":
			value = defaultToken
		case "custom":
			if params.HfToken == nil || strings.TrimSpace(*params.HfToken) == "" {
				// Cloud ASR without a local diarization stage does not use HF
				// credentials retained in a prior local model configuration.
				if !cloudASRWithoutDiarization(*params) {
					return fmt.Errorf("Custom Hugging Face token is missing; choose a profile with a token or provide one")
				}
			} else {
				value = strings.TrimSpace(*params.HfToken)
			}
		}
		if err := validateHFToken(&value); err != nil {
			return err
		}
		params.HfToken = &value
		params.HFTokenSource = source
		params.HFTokenResolved = true
	}
	return validateContext(params.TranscriptionContext, params.TranscriptionContextTerms)
}

func validateHFTokenOptions(params models.WhisperXParams) error {
	switch params.EffectiveHFTokenSource() {
	case "default", "custom", "none":
	default:
		return fmt.Errorf("hf_token_source must be default, custom, or none")
	}
	return validateHFToken(params.HfToken)
}

func (h *Handler) validateContextForRun(c *gin.Context, params *models.WhisperXParams) error {
	if err := h.resolveTranscriptionContext(c, params); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return err
	}
	if err := validateModelRunOptions(*params); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return err
	}
	return nil
}

// Profile validation preserves nil inheritance fields; only job admission resolves them.
func validateModelRunOptions(params models.WhisperXParams) error {
	if err := transcription.ValidateAdaptivePolicy(params); err != nil {
		return err
	}
	if err := validateContext(params.TranscriptionContext, params.TranscriptionContextTerms); err != nil {
		return err
	}
	if err := validateHFTokenOptions(params); err != nil {
		return err
	}
	switch params.DiarizationDevice {
	case "", "same", "cpu", "cuda", "auto":
	default:
		return fmt.Errorf("diarization_device must be same, cpu, cuda, or auto")
	}
	maxChunk := 300
	for _, spec := range adapters.LocalASRModels() {
		if spec.ID == params.Model && (params.ModelFamily == spec.Family || params.ModelFamily == spec.ID) {
			maxChunk = spec.MaxChunkSeconds
			break
		}
	}
	if params.AudioChunkDuration != nil && (*params.AudioChunkDuration < 0 || *params.AudioChunkDuration > maxChunk) {
		return fmt.Errorf("audio_chunk_duration must be between 0 and %d seconds", maxChunk)
	}
	return nil
}
