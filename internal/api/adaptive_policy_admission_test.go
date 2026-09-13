package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"scriberr/internal/models"
	"scriberr/internal/repository"

	"github.com/stretchr/testify/require"
)

func TestLearningAdmissionProfileSnapshotSurvivesEditResetAndDefaultChanges(t *testing.T) {
	h, db, user := hfTokenTestHandler(t)
	require.NoError(t, db.AutoMigrate(&models.AdaptiveObservation{}, &models.AdaptiveLearnedPlan{}, &models.AdaptivePlanSelection{}))
	require.NoError(t, db.Model(&user).Update("transcription_context", "Engineering before").Error)
	profile := models.TranscriptionProfile{Name: "learning", Parameters: defaultTranscriptionParams()}
	profile.Parameters.RecoveryMode = "batch_management"
	profile.Parameters.AdaptivePolicy = &models.AdaptiveExecutionPolicy{Learn: true}
	require.NoError(t, h.profileRepo.Create(context.Background(), &profile))
	c, _ := hfTokenRequest(http.MethodPost, "", user.ID)
	params, _, _, ok := h.resolveQueuedRun(c, &queueRunRequest{ProfileID: &profile.ID})
	require.True(t, ok)
	require.Equal(t, "Engineering before", *params.TranscriptionContext)
	require.True(t, params.AdaptivePolicy.SnapshotTaken)
	require.Equal(t, profile.ID, params.AdaptivePolicy.ProfileID)
	require.Equal(t, int64(1), params.AdaptivePolicy.ProfileRevision)
	require.Equal(t, int64(1), params.AdaptivePolicy.LearningGeneration)
	job := models.TranscriptionJob{AudioPath: "fixture.wav", Parameters: *params}
	require.NoError(t, db.Create(&job).Error)
	item := models.TranscriptionQueueItem{TranscriptionJobID: job.ID, Parameters: *params}
	require.NoError(t, db.Create(&item).Error)
	_, err := repository.NewAdaptiveLearningRepository(db).Reset(context.Background(), profile.ID, 1, 1)
	require.NoError(t, err)
	require.NoError(t, db.Model(&user).Update("transcription_context", "Engineering after").Error)
	profile.Name = "edited profile"
	require.NoError(t, h.profileRepo.Update(context.Background(), &profile))
	var queued models.TranscriptionQueueItem
	require.NoError(t, db.First(&queued, "id = ?", item.ID).Error)
	require.Equal(t, int64(1), queued.Parameters.AdaptivePolicy.ProfileRevision)
	require.Equal(t, int64(1), queued.Parameters.AdaptivePolicy.LearningGeneration)
	require.Equal(t, "Engineering before", *queued.Parameters.TranscriptionContext)
	next, _, _, ok := h.resolveQueuedRun(c, &queueRunRequest{ProfileID: &profile.ID})
	require.True(t, ok)
	require.Equal(t, int64(2), next.AdaptivePolicy.ProfileRevision)
	require.Equal(t, int64(3), next.AdaptivePolicy.LearningGeneration)
	require.Equal(t, "Engineering after", *next.TranscriptionContext)
	_, err = h.admitSavedProfile(c, &models.TranscriptionProfile{ID: profile.ID, Revision: 1, Parameters: profile.Parameters})
	require.ErrorIs(t, err, repository.ErrAdaptiveConflict)
}

func TestLearningAdmissionClearsCopiedSnapshotsOnEveryExplicitJSONPath(t *testing.T) {
	h, _, user := hfTokenTestHandler(t)
	raw := `{"model_family":"whisper","model":"large-v3","device":"cpu","compute_type":"float32","adaptive_policy":{"learn":true,"profile_id":"another-profile","profile_revision":7,"learning_generation":9,"snapshot_taken":true,"learned_plans":[{"plan_id":"forged","scope_key":"forged"}],"stages":{"recognition":{"min_batch_size":1}}}}`
	assertCleared := func(t *testing.T, p models.WhisperXParams) {
		t.Helper()
		require.NotNil(t, p.AdaptivePolicy)
		require.Empty(t, p.AdaptivePolicy.ProfileID)
		require.Zero(t, p.AdaptivePolicy.ProfileRevision)
		require.Zero(t, p.AdaptivePolicy.LearningGeneration)
		require.False(t, p.AdaptivePolicy.SnapshotTaken)
		require.Empty(t, p.AdaptivePolicy.LearnedPlans)
		require.Equal(t, 1, p.AdaptivePolicy.Stages["recognition"].MinBatchSize)
	}
	c, _ := hfTokenRequest(http.MethodPost, "", user.ID)
	message := json.RawMessage(raw)
	queued, _, _, ok := h.resolveQueuedRun(c, &queueRunRequest{Parameters: &message})
	require.True(t, ok)
	assertCleared(t, *queued)
	c, _ = hfTokenRequest(http.MethodPost, raw, user.ID)
	immediate, err := h.getValidatedTranscriptionParams(c, &models.TranscriptionJob{}, "fixture")
	require.NoError(t, err)
	assertCleared(t, *immediate)
	submitted, err := submitParamsFromJSON(&raw)
	require.NoError(t, err)
	assertCleared(t, submitted)
	c, _ = hfTokenRequest(http.MethodPost, "", user.ID)
	quick, err := h.quickParamsForUploadSession(c, &models.UploadSession{ParametersJSON: &raw})
	require.NoError(t, err)
	assertCleared(t, quick)
}

func TestLearningAdmissionQuickProfileUsesAuthoritativeStoredSettings(t *testing.T) {
	h, db, user := hfTokenTestHandler(t)
	require.NoError(t, db.AutoMigrate(&models.AdaptivePlanSelection{}, &models.AdaptiveLearnedPlan{}))
	profile := models.TranscriptionProfile{Name: "saved quick", Parameters: defaultTranscriptionParams()}
	profile.Parameters.AdaptivePolicy = &models.AdaptiveExecutionPolicy{Learn: true}
	require.NoError(t, h.profileRepo.Create(context.Background(), &profile))
	malicious := `{"model":"override","adaptive_policy":{"profile_id":"wrong","snapshot_taken":true}}`
	c, _ := hfTokenRequest(http.MethodPost, "", user.ID)
	params, err := h.quickParamsForUploadSession(c, &models.UploadSession{ProfileName: &profile.Name, ParametersJSON: &malicious})
	require.NoError(t, err)
	require.Equal(t, profile.Parameters.Model, params.Model)
	require.Equal(t, profile.ID, params.AdaptivePolicy.ProfileID)
	require.True(t, params.AdaptivePolicy.SnapshotTaken)
}
