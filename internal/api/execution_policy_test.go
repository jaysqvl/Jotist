package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/jaysqvl/Jotist/internal/models"
	"github.com/jaysqvl/Jotist/internal/transcription"
	"github.com/stretchr/testify/require"
)

func TestExecutionDefaultsPersistAndFenceNewRunSnapshots(t *testing.T) {
	h, db, user := hfTokenTestHandler(t)
	policy := models.DefaultExecutionPolicy()
	policy.MaxRetries, policy.BackoffSeconds = 2, 4
	body, err := json.Marshal(map[string]interface{}{"execution_policy": policy})
	require.NoError(t, err)
	c, w := hfTokenRequest("PUT", string(body), user.ID)
	h.UpdateUserSettings(c)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var stored models.User
	require.NoError(t, db.First(&stored, user.ID).Error)
	require.Equal(t, policy, stored.EffectiveExecutionPolicy())
	require.Equal(t, user.HFToken, stored.HFToken)

	profile := models.TranscriptionProfile{Name: "shared-GPU", Parameters: models.WhisperXParams{Model: "small", Device: "cuda", ExecutionPolicySource: "global"}}
	require.NoError(t, h.profileRepo.Create(context.Background(), &profile))
	c, _ = hfTokenRequest("POST", "", user.ID)
	admitted, err := h.admitSavedProfile(c, &profile)
	require.NoError(t, err)
	require.Equal(t, &policy, admitted.ExecutionPolicy)
	require.Equal(t, models.RecoveryStandard, admitted.RecoveryMode)
	require.True(t, *admitted.ReuseCheckpoints)
	require.Nil(t, profile.Parameters.ExecutionPolicy, "admission must not edit the saved profile")
	require.Equal(t, "cuda", admitted.Device)
	require.NotNil(t, admitted.AdaptivePolicy, "admission retains the profile revision fence")
	require.Empty(t, admitted.AdaptivePolicy.Stages, "shared defaults do not permit CPU fallback or shorter windows")
	require.False(t, admitted.AdaptivePolicy.Learn)

	// Repeated upload validation must not resolve new defaults after admission.
	changed := policy
	changed.AutomaticRecovery = false
	require.NoError(t, db.Model(&stored).Select("execution_policy").Updates(models.User{ExecutionPolicy: &changed}).Error)
	require.NoError(t, h.resolveExecutionPolicy(c, &admitted))
	require.Equal(t, &policy, admitted.ExecutionPolicy)
	newRun, err := h.admitSavedProfile(c, &profile)
	require.NoError(t, err)
	require.Equal(t, transcription.RecoveryFixed, newRun.RecoveryMode)
	require.False(t, newRun.ExecutionPolicy.AutomaticRecovery)

	// An explicit run reuse choice takes precedence without changing the profile.
	fresh := false
	profile.Parameters.ReuseCheckpoints = &fresh
	newRun, err = h.admitSavedProfile(c, &profile)
	require.NoError(t, err)
	require.False(t, *newRun.ReuseCheckpoints)
}

func TestExecutionDefaultsOldProfilesInheritAndExplicitOverridesStaySaved(t *testing.T) {
	h, _, user := hfTokenTestHandler(t)
	c, _ := hfTokenRequest("POST", "", user.ID)
	legacy := models.WhisperXParams{Device: "auto", RecoveryMode: ""}
	require.NoError(t, h.resolveExecutionPolicy(c, &legacy))
	require.Equal(t, models.RecoveryStandard, legacy.RecoveryMode)
	require.Equal(t, "global", legacy.ExecutionPolicySource)
	require.Equal(t, models.DefaultExecutionPolicy(), *legacy.ExecutionPolicy)

	policy := models.DefaultExecutionPolicy()
	policy.RecoveryStrength = "" // Explicit old profiles retain their saved mode.
	policy.MaxRetries = 1
	params := models.WhisperXParams{ExecutionPolicySource: "override", ExecutionPolicy: &policy, RecoveryMode: transcription.RecoveryCPUFallback, Device: "cuda", AdaptivePolicy: &models.AdaptiveExecutionPolicy{Stages: map[string]models.AdaptiveStagePolicy{"recognition": {AllowCPU: true, CPUPrecision: "float32"}}}}
	require.NoError(t, h.resolveExecutionPolicy(c, &params))
	require.Equal(t, transcription.RecoveryCPUFallback, params.RecoveryMode)
	require.True(t, params.AdaptivePolicy.Stages["recognition"].AllowCPU)
	require.Equal(t, 1, params.ExecutionPolicy.MaxRetries)
}

func TestExecutionDefaultsStrongAndAggressivePreserveOriginalProfileRowsAndConstraints(t *testing.T) {
	h, db, user := hfTokenTestHandler(t)
	for _, strength := range []string{models.RecoveryStrong, models.RecoveryAggressive} {
		policy := models.DefaultExecutionPolicy()
		policy.RecoveryStrength = strength
		require.NoError(t, db.Model(&user).Select("execution_policy").Updates(models.User{ExecutionPolicy: &policy}).Error)
		profile := models.TranscriptionProfile{Name: "old locked " + strength, Parameters: models.WhisperXParams{Model: "small", Device: "cuda", RecoveryMode: transcription.RecoveryBatchManagement, AdaptivePolicy: &models.AdaptiveExecutionPolicy{Stages: map[string]models.AdaptiveStagePolicy{"recognition": {DeviceLocked: true, MinBatchSize: 4}}}}}
		require.NoError(t, h.profileRepo.Create(context.Background(), &profile))
		before, err := json.Marshal(profile.Parameters)
		require.NoError(t, err)
		c, _ := hfTokenRequest("POST", "", user.ID)
		params, err := h.admitSavedProfile(c, &profile)
		require.NoError(t, err)
		require.Equal(t, strength, params.RecoveryMode)
		require.True(t, params.AdaptivePolicy.Stages["recognition"].DeviceLocked)
		require.Equal(t, 4, params.AdaptivePolicy.Stages["recognition"].MinBatchSize)
		var stored models.TranscriptionProfile
		require.NoError(t, db.First(&stored, "id = ?", profile.ID).Error)
		after, err := json.Marshal(stored.Parameters)
		require.NoError(t, err)
		require.JSONEq(t, string(before), string(after), "new-run policy resolution must not rewrite a saved profile")
	}
}

func TestExecutionDefaultsRejectInvalidUpdatesWithoutChangingSettings(t *testing.T) {
	h, _, user := hfTokenTestHandler(t)
	for _, policy := range []models.ExecutionPolicy{
		{RecoveryStrength: "level5"},
		{MaxRetries: 7}, {MaxRetries: -1}, {BackoffSeconds: 121, MaxBackoffSeconds: 150}, {BackoffSeconds: 10, MaxBackoffSeconds: 5}, {MaxBackoffSeconds: 301},
	} {
		body, err := json.Marshal(map[string]interface{}{"execution_policy": policy})
		require.NoError(t, err)
		c, w := hfTokenRequest("PUT", string(body), user.ID)
		h.UpdateUserSettings(c)
		require.Equal(t, http.StatusBadRequest, w.Code)
	}
	c, w := hfTokenRequest("GET", "", user.ID)
	h.GetUserSettings(c)
	var settings UserSettingsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &settings))
	require.Equal(t, models.DefaultExecutionPolicy(), settings.ExecutionPolicy)
}
