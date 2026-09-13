package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"scriberr/internal/models"
	"scriberr/internal/repository"
)

func hfTokenTestHandler(t *testing.T) (*Handler, *gorm.DB, models.User) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&models.AdaptiveProfileRevision{}, &models.AdaptiveObservation{}, &models.AdaptiveLearnedPlan{}, &models.AdaptivePlanSelection{}, &models.User{}, &models.TranscriptionProfile{}, &models.TranscriptionJob{}, &models.TranscriptionQueueItem{}))
	user := models.User{Username: "hf-settings-test", Password: "unused", HFToken: "hf_saved_before"}
	require.NoError(t, db.Create(&user).Error)
	return &Handler{userRepo: repository.NewUserRepository(db), profileRepo: repository.NewProfileRepository(db)}, db, user
}

func hfTokenRequest(method, body string, userID uint) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, "/", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	if userID != 0 {
		c.Set("user_id", userID)
	}
	return c, w
}

func TestHFTokenSettingsWriteOnlyPreserveReplaceAndClear(t *testing.T) {
	h, db, user := hfTokenTestHandler(t)
	for _, step := range []struct {
		body, wantToken string
		status          int
	}{
		{`{"hf_token":" hf_replacement "}`, "hf_replacement", http.StatusOK},
		{`{"transcription_context":"Engineering"}`, "hf_replacement", http.StatusOK},
		{`{"hf_token":null}`, "hf_replacement", http.StatusOK},
		{`{"hf_token":"hf_bad\nvalue"}`, "hf_replacement", http.StatusBadRequest},
		{`{"hf_token":""}`, "", http.StatusOK},
	} {
		c, w := hfTokenRequest("PUT", step.body, user.ID)
		h.UpdateUserSettings(c)
		require.Equal(t, step.status, w.Code)
		require.NotContains(t, w.Body.String(), `"hf_token":`)
		require.NotContains(t, w.Body.String(), "hf_replacement")
		require.NotContains(t, w.Body.String(), "hf_bad")
		var stored models.User
		require.NoError(t, db.First(&stored, user.ID).Error)
		require.Equal(t, step.wantToken, stored.HFToken)
		c, w = hfTokenRequest("GET", "", user.ID)
		h.GetUserSettings(c)
		require.Equal(t, http.StatusOK, w.Code)
		var response UserSettingsResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		require.Equal(t, step.wantToken != "", response.HasHFToken)
		encoded, err := json.Marshal(stored)
		require.NoError(t, err)
		require.NotContains(t, string(encoded), `"hf_token":`)
	}
}

func TestProfileHFTokenRedactedUpdatesAndExplicitSourceChanges(t *testing.T) {
	h, _, user := hfTokenTestHandler(t)
	secret := "hf_existing_profile"
	profile := models.TranscriptionProfile{Name: "custom-token-profile", Parameters: models.WhisperXParams{HfToken: &secret}}
	require.NoError(t, h.profileRepo.Create(context.Background(), &profile))
	for _, step := range []struct {
		parameters, wantSource, wantToken string
	}{
		{`{}`, "custom", secret},
		{`{"hf_token_source":"custom"}`, "custom", secret},
		{`{"hf_token_source":"default"}`, "default", ""},
		{`{"hf_token_source":"custom","hf_token":"hf_new_profile"}`, "custom", "hf_new_profile"},
		{`{"hf_token_source":"none"}`, "none", ""},
	} {
		c, w := hfTokenRequest("PUT", `{"name":"custom-token-profile","parameters":`+step.parameters+`}`, user.ID)
		c.Params = gin.Params{{Key: "id", Value: profile.ID}}
		h.UpdateProfile(c)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		require.NotContains(t, w.Body.String(), secret)
		require.NotContains(t, w.Body.String(), "hf_new_profile")
		require.NotContains(t, w.Body.String(), `"hf_token":`)
		stored, err := h.profileRepo.FindByID(context.Background(), profile.ID)
		require.NoError(t, err)
		require.Equal(t, step.wantSource, stored.Parameters.HFTokenSource)
		if step.wantToken == "" {
			require.Nil(t, stored.Parameters.HfToken, "profile defaults must remain unresolved")
		} else {
			require.Equal(t, step.wantToken, *stored.Parameters.HfToken)
		}
		var response struct {
			Parameters struct {
				HasHFToken bool `json:"has_hf_token"`
			} `json:"parameters"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		require.Equal(t, step.wantToken != "", response.Parameters.HasHFToken)
	}
}

func TestHFTokenAdmissionSnapshotSurvivesSettingsChangesAndQueueStorage(t *testing.T) {
	h, db, user := hfTokenTestHandler(t)
	blank := ""
	profile := models.TranscriptionProfile{Name: "inherit-token", Parameters: models.WhisperXParams{HFTokenSource: "default", TranscriptionContext: &blank, TranscriptionContextTerms: &blank}}
	require.NoError(t, prepareProfileHFToken(&profile.Parameters, nil))
	require.NoError(t, h.profileRepo.Create(context.Background(), &profile))
	c, _ := hfTokenRequest("POST", "", user.ID)
	params, _, _, ok := h.resolveQueuedRun(c, &queueRunRequest{ProfileID: &profile.ID})
	require.True(t, ok)
	require.Equal(t, "hf_saved_before", *params.HfToken, "saved-profile admission resolves defaults before freezing its plan")
	require.NoError(t, h.resolveTranscriptionContext(c, params))
	require.Equal(t, "hf_saved_before", *params.HfToken)
	require.Equal(t, "default", params.HFTokenSource)
	require.NoError(t, db.Model(&user).Update("hf_token", "hf_saved_after").Error)
	require.NoError(t, h.resolveTranscriptionContext(c, params), "finalization's second admission pass must retain the first snapshot")
	require.Equal(t, "hf_saved_before", *params.HfToken)
	job := models.TranscriptionJob{AudioPath: "public-fixture.wav", Parameters: *params}
	require.NoError(t, db.Create(&job).Error)
	item := models.TranscriptionQueueItem{TranscriptionJobID: job.ID, Parameters: *params}
	require.NoError(t, db.Create(&item).Error)
	var loaded models.TranscriptionQueueItem
	require.NoError(t, db.First(&loaded, "id = ?", item.ID).Error)
	require.Equal(t, "hf_saved_before", *loaded.Parameters.HfToken)
	require.Equal(t, "default", loaded.Parameters.HFTokenSource)
	encoded, err := json.Marshal(loaded)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "hf_saved_before")
	storedProfile, err := h.profileRepo.FindByID(context.Background(), profile.ID)
	require.NoError(t, err)
	require.Nil(t, storedProfile.Parameters.HfToken)
	next := storedProfile.Parameters
	require.NoError(t, h.resolveTranscriptionContext(c, &next))
	require.Equal(t, "hf_saved_after", *next.HfToken)
}

func TestHFTokenAdmissionCustomNoneAndNoUser(t *testing.T) {
	h, _, user := hfTokenTestHandler(t)
	custom, blank := "hf_per_run", ""
	for _, step := range []struct {
		params models.WhisperXParams
		userID uint
		want   string
	}{
		{models.WhisperXParams{HfToken: &custom}, user.ID, custom},
		{models.WhisperXParams{HFTokenSource: "custom", HfToken: &custom}, user.ID, custom},
		{models.WhisperXParams{HFTokenSource: "none", HfToken: &custom}, user.ID, ""},
		{models.WhisperXParams{HfToken: &blank}, user.ID, ""},
		{models.WhisperXParams{}, 0, ""},
		{models.WhisperXParams{HFTokenSource: "default", HfToken: &custom}, 0, ""},
	} {
		c, _ := hfTokenRequest("POST", "", step.userID)
		require.NoError(t, h.resolveTranscriptionContext(c, &step.params))
		require.NotNil(t, step.params.HfToken)
		require.Equal(t, step.want, *step.params.HfToken)
	}
	c, _ := hfTokenRequest("POST", "", user.ID)
	missing := models.WhisperXParams{HFTokenSource: "custom"}
	require.ErrorContains(t, h.resolveTranscriptionContext(c, &missing), "missing")
	invalid := models.WhisperXParams{HFTokenSource: "invalid"}
	require.ErrorContains(t, validateModelRunOptions(invalid), "hf_token_source")
}

func TestHFTokenSourcePreservedBySubmitParsers(t *testing.T) {
	for _, source := range []string{"default", "custom", "none"} {
		body := `{"hf_token_source":"` + source + `","hf_token":"hf_request"}`
		params, err := submitParamsFromJSON(&body)
		require.NoError(t, err)
		require.Equal(t, source, params.HFTokenSource)
		require.Equal(t, "hf_request", *params.HfToken)
		values := url.Values{"hf_token_source": {source}, "hf_token": {"hf_request"}}
		c, _ := hfTokenRequest("POST", values.Encode(), 0)
		c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		params, err = submitParamsFromForm(c)
		require.NoError(t, err)
		require.Equal(t, source, params.HFTokenSource)
		require.Equal(t, "hf_request", *params.HfToken)
	}
}

func TestImmediateProfileRunUsesStoredCredentialsAndDefaults(t *testing.T) {
	h, db, user := hfTokenTestHandler(t)
	secret := "hf_stored_custom"
	for _, source := range []string{"custom", "default"} {
		profile := models.TranscriptionProfile{Name: "immediate-" + source, Parameters: models.WhisperXParams{ModelFamily: "qwen3_asr", Model: "Qwen/Qwen3-ASR-0.6B-hf", HFTokenSource: source}}
		if source == "custom" {
			profile.Parameters.HfToken = &secret
		}
		require.NoError(t, h.profileRepo.Create(context.Background(), &profile))
		// Redacted/stale accompanying parameters cannot replace the stored profile.
		c, w := hfTokenRequest("POST", `{"profile_id":"`+profile.ID+`","model":"wrong-model","hf_token_source":"none","has_hf_token":true}`, user.ID)
		params, err := h.getValidatedTranscriptionParams(c, &models.TranscriptionJob{}, "job")
		require.NoError(t, err, w.Body.String())
		require.Equal(t, profile.Parameters.Model, params.Model)
		require.Equal(t, source, params.HFTokenSource)
		if source == "custom" {
			require.Equal(t, secret, *params.HfToken)
		} else {
			originalDefault := user.HFToken
			require.Equal(t, originalDefault, *params.HfToken)
			require.NoError(t, db.Model(&user).Update("hf_token", "hf_later_default").Error)
			require.Equal(t, originalDefault, *params.HfToken)
		}
		stored, err := h.profileRepo.FindByID(context.Background(), profile.ID)
		require.NoError(t, err)
		if source == "default" {
			require.Nil(t, stored.Parameters.HfToken)
		}
	}
}

func TestImmediateProfileRunRejectsMissingIDsAndMultiTrackMismatch(t *testing.T) {
	h, _, user := hfTokenTestHandler(t)
	profile := models.TranscriptionProfile{Name: "single-track", Parameters: models.WhisperXParams{HFTokenSource: "default"}}
	require.NoError(t, h.profileRepo.Create(context.Background(), &profile))
	for _, step := range []struct {
		body   string
		job    models.TranscriptionJob
		status int
	}{
		{`{"profile_id":"does-not-exist","model":"small"}`, models.TranscriptionJob{}, http.StatusNotFound},
		{`{"profile_id":"","model":"small"}`, models.TranscriptionJob{}, http.StatusBadRequest},
		{`{"profile_id":"` + profile.ID + `","is_multi_track_enabled":true}`, models.TranscriptionJob{IsMultiTrack: true}, http.StatusBadRequest},
	} {
		c, w := hfTokenRequest("POST", step.body, user.ID)
		params, err := h.getValidatedTranscriptionParams(c, &step.job, "job")
		require.Error(t, err)
		require.Nil(t, params)
		require.Equal(t, step.status, w.Code)
	}
	profile.Parameters.IsMultiTrackEnabled = true
	profile.Parameters.Diarize = false
	require.NoError(t, h.profileRepo.Update(context.Background(), &profile))
	c, w := hfTokenRequest("POST", `{"profile_id":"`+profile.ID+`"}`, user.ID)
	params, err := h.getValidatedTranscriptionParams(c, &models.TranscriptionJob{IsMultiTrack: true}, "job")
	require.NoError(t, err, w.Body.String())
	require.True(t, params.IsMultiTrackEnabled)
	require.False(t, params.Diarize)
}

func TestImmediateParametersRetainExplicitValuesWithoutProfile(t *testing.T) {
	h, _, user := hfTokenTestHandler(t)
	c, w := hfTokenRequest("POST", `{"model":"large-v3","batch_size":0,"verbose":false,"hf_token_source":"custom","hf_token":"hf_direct_run"}`, user.ID)
	params, err := h.getValidatedTranscriptionParams(c, &models.TranscriptionJob{}, "job")
	require.NoError(t, err, w.Body.String())
	require.Equal(t, "large-v3", params.Model)
	require.Zero(t, params.BatchSize)
	require.False(t, params.Verbose)
	require.Equal(t, "hf_direct_run", *params.HfToken)
}

func TestMissingCustomHFTokenIsIrrelevantOnlyForCloudWithoutDiarization(t *testing.T) {
	c, _ := hfTokenRequest("POST", "", 0)
	for _, family := range []string{"openai", "openai_whisper", "whisper", "qwen3_asr"} {
		for _, diarize := range []bool{false, true} {
			params := models.WhisperXParams{ModelFamily: family, Diarize: diarize, HFTokenSource: "custom"}
			profileParams := params
			profileErr := prepareProfileHFToken(&profileParams, nil)
			err := (&Handler{}).resolveTranscriptionContext(c, &params)
			if (family == "openai" || family == "openai_whisper") && !diarize {
				require.NoError(t, profileErr)
				require.NoError(t, err)
				require.Equal(t, "custom", params.HFTokenSource)
				require.Empty(t, *params.HfToken)
			} else {
				require.Error(t, profileErr)
				require.ErrorContains(t, err, "Custom Hugging Face token is missing")
			}
		}
	}
}
