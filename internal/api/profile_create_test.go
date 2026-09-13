package api

import (
	"context"
	"net/http"
	"testing"

	"scriberr/internal/models"

	"github.com/stretchr/testify/require"
)

func TestProfileCreateOmittedParametersKeepLegacyDefaults(t *testing.T) {
	language := "en"
	for _, test := range []struct {
		name, body string
		parameters models.WhisperXParams
	}{
		{name: "omitted", body: `{"name":"omitted"}`},
		{name: "empty", body: `{"name":"empty","parameters":{}}`},
		{
			name: "partial", body: `{"name":"partial","parameters":{"model":"large-v3","language":"en"}}`,
			parameters: models.WhisperXParams{Model: "large-v3", Language: &language},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			h, db, user := hfTokenTestHandler(t)
			// Direct GORM insertion reproduces the old create-path defaults. Compare
			// every persisted parameter, including legacy NVIDIA/diarization fields.
			legacy := models.TranscriptionProfile{Name: "legacy", Parameters: test.parameters}
			legacy.Parameters.HFTokenSource = "default"
			require.NoError(t, db.Create(&legacy).Error)
			c, w := hfTokenRequest(http.MethodPost, test.body, user.ID)
			h.CreateProfile(c)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			stored, err := h.profileRepo.FindByName(context.Background(), test.name)
			require.NoError(t, err)
			require.Equal(t, legacy.Parameters, stored.Parameters)
			require.Nil(t, stored.Parameters.TranscriptionContext)
			require.Nil(t, stored.Parameters.TranscriptionContextTerms)
			require.Nil(t, stored.Parameters.HfToken)
		})
	}
}

func TestProfileCreateExplicitFalseAndZeroOverrideLegacyDefaults(t *testing.T) {
	h, _, user := hfTokenTestHandler(t)
	c, w := hfTokenRequest(http.MethodPost, `{
		"name":"exact-preset",
		"parameters":{
			"model":"large-v3", "verbose":false, "fp16":false,
			"vad_onset":0, "vad_offset":0, "logprob_threshold":0,
			"temperature_increment_on_fallback":0, "patience":0,
			"attention_context_left":0, "attention_context_right":0,
			"nvidia_chunk_duration":0, "nvidia_timestamps":false,
			"audio_chunk_duration":0, "transcription_context":"",
			"hf_token_source":"custom", "hf_token":"hf_test_only_profile"
		}
	}`, user.ID)
	h.CreateProfile(c)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NotContains(t, w.Body.String(), "hf_test_only_profile")
	stored, err := h.profileRepo.FindByName(context.Background(), "exact-preset")
	require.NoError(t, err)
	params := stored.Parameters
	require.Equal(t, "large-v3", params.Model)
	require.False(t, params.Verbose)
	require.False(t, params.Fp16)
	require.Zero(t, params.VadOnset)
	require.Zero(t, params.VadOffset)
	require.Zero(t, params.LogprobThreshold)
	require.Zero(t, params.TemperatureIncrementOnFallback)
	require.Zero(t, params.Patience)
	require.Zero(t, params.AttentionContextLeft)
	require.Zero(t, params.AttentionContextRight)
	require.Zero(t, params.NvidiaChunkDuration)
	require.NotNil(t, params.NvidiaTimestamps)
	require.False(t, *params.NvidiaTimestamps)
	require.NotNil(t, params.AudioChunkDuration)
	require.Zero(t, *params.AudioChunkDuration)
	require.NotNil(t, params.TranscriptionContext)
	require.Empty(t, *params.TranscriptionContext)
	require.Nil(t, params.TranscriptionContextTerms)
	require.Equal(t, "custom", params.HFTokenSource)
	require.NotNil(t, params.HfToken)
	require.Equal(t, "hf_test_only_profile", *params.HfToken)
	require.Equal(t, 8, params.BatchSize, "omitted fields still receive legacy defaults")
}
