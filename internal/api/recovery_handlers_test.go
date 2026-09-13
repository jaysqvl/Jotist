package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scriberr/internal/models"
	"scriberr/internal/repository"
	"scriberr/internal/transcription"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestRecoveryResponseScopesAndValidatesPartialOutput(t *testing.T) {
	h, db, user := hfTokenTestHandler(t)
	require.NoError(t, db.AutoMigrate(&models.TranscriptionJobExecution{}, &models.RecoveryStage{}, &models.RecoveryAttempt{}, &models.RecoveryCheckpoint{}, &models.RecoveryCheckpointDependency{}, &models.RecoveryDeletion{}))
	root := t.TempDir()
	t.Setenv("CHECKPOINT_DIR", root)
	h.jobRepo = repository.NewJobRepository(db)
	h.unifiedProcessor = transcription.NewUnifiedJobProcessor(h.jobRepo, t.TempDir(), filepath.Join(t.TempDir(), "transcripts"))
	job := models.TranscriptionJob{ID: uuid.NewString(), AudioPath: "unused.wav", Status: models.StatusProcessing}
	require.NoError(t, db.Create(&job).Error)
	deadline := time.Now().Add(time.Hour)
	run := models.TranscriptionJobExecution{ID: uuid.NewString(), TranscriptionJobID: job.ID, StartedAt: time.Now(), DeadlineAt: &deadline, RecoveryVersion: 1, ActualParameters: models.WhisperXParams{RecoveryMode: "fixed"}}
	lifecycle := repository.NewExecutionLifecycleRepository(db)
	require.NoError(t, lifecycle.Begin(context.Background(), &run))
	params := run.ActualParameters.WithoutSecrets()
	params.CallbackURL = nil
	params.ReuseCheckpoints = nil
	params.HFTokenSource = ""
	encoded, _ := json.Marshal(params)
	plan, _ := json.Marshal(map[string]interface{}{"version": 1, "mode": params.RecoveryMode, "requested_settings_hash": fmt.Sprintf("%x", sha256.Sum256(encoded)), "max_stage_attempts": 7, "boundary_version": "adapter-boundary-v1"})
	require.NoError(t, db.Model(&run).Update("plan_json", string(plan)).Error)
	store := h.unifiedProcessor.GetUnifiedService().RecoveryRepository()
	hash := strings.Repeat("a", 64)
	stage, err := store.EnsureStage(context.Background(), repository.StageSpec{RecordingID: job.ID, ExecutionID: run.ID, NodeKey: "asr", Kind: "combined", SchemaVersion: "1", CompatibilityKey: hash, OwnerGeneration: run.OwnerGeneration, DurationSeconds: 3, RecoverableBoundary: true, Provenance: repository.CheckpointProvenance{AudioSHA256: hash, PreparedAudioSHA256: hash, PreprocessingHash: hash, SettingsHash: hash, RuntimeFingerprint: hash, Implementation: "test-v1", Models: []repository.ModelArtifactIdentity{{ModelID: "fake", Revision: "pinned"}}}})
	require.NoError(t, err)
	attempt, err := store.ClaimStage(context.Background(), stage.ID, run.OwnerGeneration, repository.AttemptSettings{Device: "cpu", Precision: "float32", BatchSize: 1, SettingsHash: hash})
	require.NoError(t, err)
	checkpoint, err := store.CommitCheckpoint(context.Background(), attempt.ID, run.OwnerGeneration, []byte(`{"text":"Remember the C++ API.","segments":[{"start":0,"end":2,"text":"Remember the C++ API."}],"metadata":{"hf_token":"hf_private"}}`), nil)
	require.NoError(t, err)
	require.NoError(t, lifecycle.Finish(context.Background(), run.ID, run.OwnerGeneration, models.StatusFailed, nil, "Speaker stage failed"))
	request := func(jobID string) map[string]interface{} {
		c, w := hfTokenRequest(http.MethodGet, "", user.ID)
		c.Params = gin.Params{{Key: "id", Value: jobID}, {Key: "run_id", Value: run.ID}}
		h.GetRunRecovery(c)
		if jobID != job.ID {
			require.Equal(t, http.StatusNotFound, w.Code)
			return nil
		}
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		require.NotContains(t, w.Body.String(), "hf_private")
		var result map[string]interface{}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
		return result
	}
	result := request(job.ID)
	require.Equal(t, true, result["resumable"])
	require.Equal(t, true, result["partial_transcript_available"])
	require.Equal(t, "Remember the C++ API.", result["partial_transcript"].(map[string]interface{})["text"])
	request(uuid.NewString())
	require.NoError(t, os.Remove(filepath.Join(root, checkpoint.RelativePath, "result.json")))
	result = request(job.ID)
	require.Equal(t, false, result["resumable"])
	require.Equal(t, false, result["partial_transcript_available"])
	require.Contains(t, result["resume_unavailable_reason"], "corrupt")
}

func TestRecoveryPolicyAdmissionAndFreshProfileOverride(t *testing.T) {
	h, db, user := hfTokenTestHandler(t)
	profile := models.TranscriptionProfile{Name: "saved", Parameters: models.WhisperXParams{RecoveryMode: "fixed"}}
	require.NoError(t, db.Create(&profile).Error)
	c, _ := hfTokenRequest(http.MethodPost, "", user.ID)
	fresh := false
	params, _, _, ok := h.resolveQueuedRun(c, &queueRunRequest{ProfileID: &profile.ID, ReuseCheckpoints: &fresh})
	require.True(t, ok)
	require.NotNil(t, params.ReuseCheckpoints)
	require.False(t, *params.ReuseCheckpoints)
	stored, err := h.profileRepo.FindByID(context.Background(), profile.ID)
	require.NoError(t, err)
	require.Nil(t, stored.Parameters.ReuseCheckpoints, "run override must not edit profile")
	for _, mode := range []string{"invented_policy", "batch"} {
		require.Error(t, validateModelRunOptions(models.WhisperXParams{RecoveryMode: mode}))
	}
}
