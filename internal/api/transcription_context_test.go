package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"scriberr/internal/models"
	"scriberr/internal/repository"
	"scriberr/internal/transcription/adapters"
)

func TestContextDefaultsSnapshotAndExplicitEmpty(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&models.AdaptiveProfileRevision{}, &models.User{}, &models.TranscriptionProfile{}))
	user := models.User{Username: "context-test", Password: "unused", TranscriptionContext: "Engineering review", TranscriptionContextTerms: "PostgreSQL\nScriberr"}
	require.NoError(t, db.Create(&user).Error)
	h := &Handler{userRepo: repository.NewUserRepository(db)}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/", nil)
	c.Set("user_id", user.ID)
	params := models.WhisperXParams{}
	require.NoError(t, h.resolveTranscriptionContext(c, &params))
	require.Equal(t, "Engineering review", *params.TranscriptionContext)
	require.Equal(t, "PostgreSQL\nScriberr", *params.TranscriptionContextTerms)
	require.NoError(t, db.Model(&user).Update("transcription_context", "New default").Error)
	require.Equal(t, "Engineering review", *params.TranscriptionContext, "queued parameters must not change with settings")
	blank := ""
	cleared := models.WhisperXParams{TranscriptionContext: &blank}
	require.NoError(t, h.resolveTranscriptionContext(c, &cleared))
	require.Equal(t, "", *cleared.TranscriptionContext)
	require.Equal(t, "PostgreSQL\nScriberr", *cleared.TranscriptionContextTerms)

	// Profile storage retains inheritance rather than resolving it when saved.
	profile := models.TranscriptionProfile{Name: "inherited", Parameters: models.WhisperXParams{TranscriptionContextTerms: &blank}}
	repo := repository.NewProfileRepository(db)
	require.NoError(t, repo.Create(context.Background(), &profile))
	loaded, err := repo.FindByID(context.Background(), profile.ID)
	require.NoError(t, err)
	require.Nil(t, loaded.Parameters.TranscriptionContext)
	require.NotNil(t, loaded.Parameters.TranscriptionContextTerms)
	require.Empty(t, *loaded.Parameters.TranscriptionContextTerms)
	require.Empty(t, loaded.Parameters.DiarizationDevice, "migration must preserve legacy device semantics")
}

func TestModelSpecificChunkLimits(t *testing.T) {
	for _, spec := range adapters.LocalASRModels() {
		limit := spec.MaxChunkSeconds
		params := models.WhisperXParams{ModelFamily: spec.Family, Model: spec.ID, AudioChunkDuration: &limit}
		require.NoError(t, validateModelRunOptions(params), spec.ID)
		limit++
		require.Error(t, validateModelRunOptions(params), spec.ID)
	}
}

func TestSubmitFormDefaultsForLocalModels(t *testing.T) {
	for _, family := range []string{"whisper", "qwen3_asr", "moss_asr", "mistral_voxtral", "ibm_granite_speech", "vibevoice-bitnet"} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		values := url.Values{"model_family": {family}}
		c.Request = httptest.NewRequest("POST", "/", strings.NewReader(values.Encode()))
		c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		params, err := submitParamsFromForm(c)
		require.NoError(t, err)
		if family == "whisper" {
			require.Equal(t, "int8", params.ComputeType)
			require.Equal(t, "base", params.Model)
		} else if family == "vibevoice-bitnet" {
			require.Equal(t, "i2_s+i8_s", params.ComputeType)
			require.Equal(t, "vibevoice-bitnet", params.Model)
		} else {
			require.Equal(t, "float32", params.ComputeType)
			require.NotEqual(t, "base", params.Model)
		}
	}
}

func TestSubmitFormPreservesCheckpointAndChunkOptions(t *testing.T) {
	for _, chunk := range []string{"0", "30", "invalid", "1.5"} {
		t.Run(chunk, func(t *testing.T) {
			values := url.Values{"model_family": {"qwen3_asr"}, "diarize": {"true"}, "diarize_model": {"suplime"}, "diarization_checkpoint": {"rewayai/suplime"}, "diarization_device": {"cpu"}, "audio_chunk_duration": {chunk}}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/", strings.NewReader(values.Encode()))
			c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			params, err := submitParamsFromForm(c)
			if chunk == "invalid" || chunk == "1.5" {
				require.ErrorContains(t, err, "audio_chunk_duration")
				return
			}
			require.NoError(t, err)
			require.NoError(t, validateModelRunOptions(params))
			require.True(t, params.Diarize)
			require.Equal(t, "suplime", params.DiarizeModel)
			require.Equal(t, "rewayai/suplime", params.DiarizationCheckpoint)
			require.Equal(t, "cpu", params.DiarizationDevice)
			require.NotNil(t, params.AudioChunkDuration)
			if chunk == "0" {
				require.Zero(t, *params.AudioChunkDuration, "explicit automatic chunking must survive admission")
			} else {
				require.Equal(t, 30, *params.AudioChunkDuration)
			}
		})
	}
}

func TestResumableSubmitDefaultsPreserveExplicitOverrides(t *testing.T) {
	for _, family := range []string{"qwen3_asr", "vibevoice-bitnet"} {
		body := fmt.Sprintf(`{"model_family":%q}`, family)
		params, err := submitParamsFromJSON(&body)
		require.NoError(t, err)
		require.NotEqual(t, "base", params.Model)
		require.NotEqual(t, "int8", params.ComputeType)
	}
	body := `{"model_family":"qwen3_asr","model":"Qwen/Qwen3-ASR-0.6B-hf","compute_type":"float16","transcription_context":"","hf_token":"test-token"}`
	params, err := submitParamsFromJSON(&body)
	require.NoError(t, err)
	require.Equal(t, "Qwen/Qwen3-ASR-0.6B-hf", params.Model)
	require.Equal(t, "float16", params.ComputeType)
	require.NotNil(t, params.TranscriptionContext)
	require.Empty(t, *params.TranscriptionContext)
	require.Equal(t, "test-token", *params.HfToken)
}

func TestResumableSubmitRejectsInvalidOptionsBeforeMovingAudio(t *testing.T) {
	for _, body := range []string{`{"diarization_device":"invalid"}`, `{"audio_chunk_duration":-1}`, `{"transcription_context":"` + strings.Repeat("x", 4001) + `"}`, `{`} {
		t.Run(body[:min(len(body), 30)], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "assembled.wav")
			require.NoError(t, os.WriteFile(path, []byte("audio"), 0600))
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/", nil)
			session := &models.UploadSession{Kind: models.UploadKindSubmit, ParametersJSON: &body}
			_, _, _, err := (&Handler{}).finalizeAssembledUpload(c, session, []assembledUploadFile{{Role: models.UploadFileRoleAudio, Path: path}})
			var invalidParams invalidUploadParametersError
			require.ErrorAs(t, err, &invalidParams)
			data, readErr := os.ReadFile(path)
			require.NoError(t, readErr)
			require.Equal(t, "audio", string(data), "validation must leave assembled audio available for retry or cancellation")
		})
	}
}

func TestContextWithoutUserDoesNotBorrowAnotherUsersDefaults(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/", nil)
	params := models.WhisperXParams{}
	require.NoError(t, (&Handler{}).resolveTranscriptionContext(c, &params))
	require.NotNil(t, params.TranscriptionContext)
	require.Empty(t, *params.TranscriptionContext)
}

func TestContextValidationAndWireFormat(t *testing.T) {
	tooLong, nul := strings.Repeat("語", 4001), "name\x00suffix"
	require.Error(t, validateContext(&tooLong, nil))
	require.Error(t, validateContext(nil, &nul))
	normal, blank, token := "工程会議", "", "hf_not_for_output"
	params := models.WhisperXParams{TranscriptionContext: &normal, TranscriptionContextTerms: &blank, HfToken: &token, DiarizationDevice: "cuda"}
	data, err := json.Marshal(params)
	require.NoError(t, err)
	require.NotContains(t, string(data), token)
	var roundtrip models.WhisperXParams
	require.NoError(t, json.Unmarshal(data, &roundtrip))
	require.Equal(t, normal, *roundtrip.TranscriptionContext)
	require.Equal(t, blank, *roundtrip.TranscriptionContextTerms)
	require.Error(t, validateModelRunOptions(models.WhisperXParams{DiarizationDevice: "typo"}))
}
