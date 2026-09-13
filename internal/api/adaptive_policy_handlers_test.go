package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"scriberr/internal/models"
	"scriberr/internal/repository"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAdaptiveProfileAPIEmptyEvidenceAndGuardedActions(t *testing.T) {
	h, db, user := hfTokenTestHandler(t)
	require.NoError(t, db.AutoMigrate(&models.AdaptiveObservation{}, &models.AdaptiveLearnedPlan{}, &models.AdaptivePlanSelection{}))
	secret := "test-only-private-profile-token"
	profile := models.TranscriptionProfile{Name: "unmeasured", Parameters: models.WhisperXParams{HFTokenSource: "custom", HfToken: &secret, Fp16: false, VadOnset: 0}}
	require.NoError(t, h.profileRepo.Create(context.Background(), &profile))
	c, w := hfTokenRequest(http.MethodGet, "", user.ID)
	c.Params = gin.Params{{Key: "id", Value: profile.ID}}
	h.GetAdaptivePolicy(c)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NotContains(t, w.Body.String(), secret)
	var snapshot repository.AdaptivePolicySnapshot
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &snapshot))
	require.Equal(t, "no_measurements", snapshot.Status)
	require.Empty(t, snapshot.Plans)
	require.Empty(t, snapshot.SelectedPlanIDs)
	require.Len(t, snapshot.Revisions, 1)
	require.False(t, snapshot.Revisions[0].Parameters.Fp16)
	require.Zero(t, snapshot.Revisions[0].Parameters.VadOnset)
	for _, test := range []struct {
		body    string
		status  int
		handler gin.HandlerFunc
	}{
		{`{"expected_revision":1,"expected_generation":1,"plan_id":"invented"}`, http.StatusConflict, h.FreezeAdaptivePolicy},
		{`{"expected_revision":1}`, http.StatusBadRequest, h.ResetAdaptivePolicy},
		{`{"expected_revision":2,"expected_generation":1}`, http.StatusConflict, h.ResetAdaptivePolicy},
		{`{"expected_revision":1,"expected_generation":1}`, http.StatusOK, h.ResetAdaptivePolicy},
		{`{"expected_revision":1,"expected_generation":1}`, http.StatusConflict, h.ResetAdaptivePolicy},
	} {
		c, w = hfTokenRequest(http.MethodPost, test.body, user.ID)
		c.Params = gin.Params{{Key: "id", Value: profile.ID}}
		test.handler(c)
		require.Equal(t, test.status, w.Code, w.Body.String())
		require.NotContains(t, w.Body.String(), secret)
	}
	stored, err := h.profileRepo.FindByID(context.Background(), profile.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1), stored.Revision)
	require.Equal(t, int64(2), stored.LearningGeneration)
	require.Equal(t, &secret, stored.Parameters.HfToken)
}

func TestProfileUpdateRevisionCASAndRestoreAPI(t *testing.T) {
	h, db, user := hfTokenTestHandler(t)
	require.NoError(t, db.AutoMigrate(&models.AdaptiveObservation{}, &models.AdaptiveLearnedPlan{}, &models.AdaptivePlanSelection{}))
	secret := "test-only-preserved-token"
	profile := models.TranscriptionProfile{Name: "original", IsDefault: true, Parameters: models.WhisperXParams{HFTokenSource: "custom", HfToken: &secret, Fp16: false, VadOnset: 0}}
	require.NoError(t, h.profileRepo.Create(context.Background(), &profile))
	for _, test := range []struct {
		body   string
		status int
	}{
		{`{"name":"updated","expected_revision":1,"parameters":{"fp16":false,"vad_onset":0}}`, http.StatusOK},
		{`{"name":"stale overwrite","expected_revision":1,"parameters":{}}`, http.StatusConflict},
		{`{"name":"invalid","expected_revision":0,"parameters":{}}`, http.StatusBadRequest},
		{`{"name":"legacy update","parameters":{"fp16":false,"vad_onset":0}}`, http.StatusOK},
	} {
		c, w := hfTokenRequest(http.MethodPut, test.body, user.ID)
		c.Params = gin.Params{{Key: "id", Value: profile.ID}}
		h.UpdateProfile(c)
		require.Equal(t, test.status, w.Code, w.Body.String())
		require.NotContains(t, w.Body.String(), secret)
	}
	stored, err := h.profileRepo.FindByID(context.Background(), profile.ID)
	require.NoError(t, err)
	require.Equal(t, "legacy update", stored.Name)
	require.Equal(t, int64(3), stored.Revision)
	require.Equal(t, int64(3), stored.LearningGeneration)
	require.Equal(t, &secret, stored.Parameters.HfToken)
	c, w := hfTokenRequest(http.MethodPost, `{"expected_revision":3,"expected_generation":3,"revision":1}`, user.ID)
	c.Params = gin.Params{{Key: "id", Value: profile.ID}}
	h.RestoreProfileRevision(c)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NotContains(t, w.Body.String(), secret)
	var restored models.TranscriptionProfile
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &restored))
	require.Equal(t, "original", restored.Name)
	require.Equal(t, int64(4), restored.Revision)
	require.Equal(t, int64(4), restored.LearningGeneration)
	require.False(t, restored.Parameters.Fp16)
	require.Zero(t, restored.Parameters.VadOnset)
	require.False(t, restored.IsDefault, "restore preserves current independent default choice")
	c, w = hfTokenRequest(http.MethodPost, `{"expected_revision":3,"expected_generation":3,"revision":1}`, user.ID)
	c.Params = gin.Params{{Key: "id", Value: profile.ID}}
	h.RestoreProfileRevision(c)
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
}

func TestAdaptivePolicyAPIMissingProfile(t *testing.T) {
	h, _, user := hfTokenTestHandler(t)
	c, w := hfTokenRequest(http.MethodGet, "", user.ID)
	c.Params = gin.Params{{Key: "id", Value: "missing"}}
	h.GetAdaptivePolicy(c)
	require.Equal(t, http.StatusNotFound, w.Code)
}
