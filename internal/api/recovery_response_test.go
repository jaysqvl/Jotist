package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scriberr/internal/models"
	"scriberr/internal/repository"
	"scriberr/internal/transcription"
	"scriberr/internal/transcription/interfaces"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type recoveryResponseFixture struct {
	h         *Handler
	db        *gorm.DB
	userID    uint
	job       models.TranscriptionJob
	run       models.TranscriptionJobExecution
	store     *repository.RecoveryRepository
	lifecycle repository.ExecutionLifecycleRepository
}

func newRecoveryResponseFixture(t *testing.T, params models.WhisperXParams) *recoveryResponseFixture {
	t.Helper()
	h, db, user := hfTokenTestHandler(t)
	require.NoError(t, db.AutoMigrate(&models.TranscriptionJobExecution{}, &models.RecoveryStage{}, &models.RecoveryAttempt{}, &models.RecoveryCheckpoint{}, &models.RecoveryCheckpointDependency{}, &models.RecoveryDeletion{}))
	t.Setenv("CHECKPOINT_DIR", t.TempDir())
	h.jobRepo = repository.NewJobRepository(db)
	h.unifiedProcessor = transcription.NewUnifiedJobProcessor(h.jobRepo, t.TempDir(), filepath.Join(t.TempDir(), "transcripts"))
	job := models.TranscriptionJob{ID: uuid.NewString(), AudioPath: "unused-synthetic.wav", Status: models.StatusProcessing}
	require.NoError(t, db.Create(&job).Error)
	deadline := time.Now().Add(time.Hour)
	run := models.TranscriptionJobExecution{ID: uuid.NewString(), TranscriptionJobID: job.ID, StartedAt: time.Now(), DeadlineAt: &deadline, RecoveryVersion: 1, ActualParameters: params}
	lifecycle := repository.NewExecutionLifecycleRepository(db)
	require.NoError(t, lifecycle.Begin(context.Background(), &run))
	canonical := params.WithoutSecrets()
	canonical.CallbackURL, canonical.ReuseCheckpoints, canonical.HFTokenSource = nil, nil, ""
	encoded, err := json.Marshal(canonical)
	require.NoError(t, err)
	plan, err := json.Marshal(map[string]interface{}{"version": 1, "mode": params.RecoveryMode, "requested_settings_hash": fmt.Sprintf("%x", sha256.Sum256(encoded)), "max_stage_attempts": 7, "boundary_version": "adapter-boundary-v1"})
	require.NoError(t, err)
	require.NoError(t, db.Model(&run).Update("plan_json", string(plan)).Error)
	return &recoveryResponseFixture{h: h, db: db, userID: user.ID, job: job, run: run, store: h.unifiedProcessor.GetUnifiedService().RecoveryRepository(), lifecycle: lifecycle}
}

func (f *recoveryResponseFixture) stage(t *testing.T, node, kind string, output *interfaces.TranscriptResult) {
	t.Helper()
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(node)))
	stage, err := f.store.EnsureStage(context.Background(), repository.StageSpec{RecordingID: f.job.ID, ExecutionID: f.run.ID, NodeKey: node, Kind: kind, SchemaVersion: "1", CompatibilityKey: hash, OwnerGeneration: f.run.OwnerGeneration, DurationSeconds: 3, RecoverableBoundary: true, Provenance: repository.CheckpointProvenance{AudioSHA256: hash, PreparedAudioSHA256: hash, PreprocessingHash: hash, SettingsHash: hash, RuntimeFingerprint: hash, Implementation: "synthetic-response-test", Models: []repository.ModelArtifactIdentity{{ModelID: "synthetic", Revision: strings.Repeat("a", 40)}}}})
	require.NoError(t, err)
	attempt, err := f.store.ClaimStage(context.Background(), stage.ID, f.run.OwnerGeneration, repository.AttemptSettings{Device: "cpu", Precision: "float32", BatchSize: 1, SettingsHash: hash, Reason: "initial"})
	require.NoError(t, err)
	if output == nil {
		require.NoError(t, f.store.FailAttempt(context.Background(), attempt.ID, f.run.OwnerGeneration, models.RecoveryFailed, "adapter_failed"))
		return
	}
	data, err := json.Marshal(output)
	require.NoError(t, err)
	_, err = f.store.CommitCheckpoint(context.Background(), attempt.ID, f.run.OwnerGeneration, data, nil)
	require.NoError(t, err)
}

func (f *recoveryResponseFixture) response(t *testing.T) (map[string]interface{}, string) {
	t.Helper()
	c, w := hfTokenRequest(http.MethodGet, "", f.userID)
	c.Params = gin.Params{{Key: "id", Value: f.job.ID}, {Key: "run_id", Value: f.run.ID}}
	f.h.GetRunRecovery(c)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var result map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	return result, w.Body.String()
}

func partialResponseTranscript(t *testing.T, response map[string]interface{}) interfaces.TranscriptResult {
	t.Helper()
	data, err := json.Marshal(response["partial_transcript"])
	require.NoError(t, err)
	var result interfaces.TranscriptResult
	require.NoError(t, json.Unmarshal(data, &result))
	return result
}

func TestRecoveryResponsePrefersAlignmentWithoutDuplicatingRecognizedWords(t *testing.T) {
	f := newRecoveryResponseFixture(t, models.WhisperXParams{RecoveryMode: "stage_management"})
	text := "Synthetic C++ API transcript."
	f.stage(t, "recognition", "recognition", &interfaces.TranscriptResult{Text: text, Language: "en"})
	aligned := &interfaces.TranscriptResult{Text: text, Language: "en", WordSegments: []interfaces.TranscriptWord{{Start: 0.5, End: 1.5, Word: "Synthetic"}}, Metadata: map[string]string{"timestamp_source": "native_word_timestamps", "hf_token": "synthetic-secret-must-not-leak"}}
	f.stage(t, "alignment", "alignment", aligned)
	f.stage(t, "diarization", "diarization", nil)
	require.NoError(t, f.lifecycle.Finish(context.Background(), f.run.ID, f.run.OwnerGeneration, models.StatusFailed, nil, "synthetic speaker failure"))
	response, raw := f.response(t)
	require.Equal(t, true, response["available"])
	require.Equal(t, true, response["partial_transcript_available"])
	partial := partialResponseTranscript(t, response)
	require.Equal(t, text, partial.Text)
	require.Equal(t, aligned.WordSegments, partial.WordSegments)
	require.NotContains(t, raw, "synthetic-secret-must-not-leak")
	labels := map[string]string{}
	for _, value := range response["stages"].([]interface{}) {
		stage := value.(map[string]interface{})
		labels[stage["kind"].(string)] = stage["label"].(string)
	}
	require.Equal(t, "Recognition", labels["recognition"])
	require.Equal(t, "Timestamp alignment", labels["alignment"])
	require.Equal(t, "Speaker diarization", labels["diarization"])
	// A recovery read never publishes partial text into the recording or run.
	var job models.TranscriptionJob
	var run models.TranscriptionJobExecution
	require.NoError(t, f.db.First(&job, "id = ?", f.job.ID).Error)
	require.NoError(t, f.db.First(&run, "id = ?", f.run.ID).Error)
	require.Nil(t, job.Transcript)
	require.Nil(t, run.Transcript)
}

func TestRecoveryResponseRetainsRecognitionWhenAlignmentFails(t *testing.T) {
	for _, text := range []string{"Synthetic recognized words stay downloadable.", ""} {
		t.Run(fmt.Sprintf("characters-%d", len(text)), func(t *testing.T) {
			f := newRecoveryResponseFixture(t, models.WhisperXParams{RecoveryMode: "fixed"})
			f.stage(t, "recognition", "recognize", &interfaces.TranscriptResult{Text: text, Language: "en"})
			f.stage(t, "alignment", "align", nil)
			require.NoError(t, f.lifecycle.Finish(context.Background(), f.run.ID, f.run.OwnerGeneration, models.StatusFailed, nil, "synthetic alignment failure"))
			response, _ := f.response(t)
			require.Equal(t, true, response["partial_transcript_available"], "a successful silent-audio checkpoint is still a real retained output")
			partial := partialResponseTranscript(t, response)
			require.Equal(t, text, partial.Text)
			require.Empty(t, partial.WordSegments, "failed alignment cannot manufacture word timestamps")
		})
	}
}

func TestRecoveryResponseKeepsDistinctTracksAndCombinedOutput(t *testing.T) {
	f := newRecoveryResponseFixture(t, models.WhisperXParams{RecoveryMode: "stage_management"})
	// Reverse completion order; grouping must use track identity, not adjacency
	// or text deduplication. The same words on separate tracks remain distinct.
	f.stage(t, "track-b.asr", "combined", &interfaces.TranscriptResult{Text: "Shared synthetic words.", Segments: []interfaces.TranscriptSegment{{Start: 0, End: 1, Text: "Shared synthetic words."}}})
	f.stage(t, "track-a.align", "align", &interfaces.TranscriptResult{Text: "Shared synthetic words.", WordSegments: []interfaces.TranscriptWord{{Start: 1, End: 2, Word: "Shared"}}})
	f.stage(t, "track-a.recognize", "recognize", &interfaces.TranscriptResult{Text: "Shared synthetic words."})
	f.stage(t, "track-c.recognition", "recognition", &interfaces.TranscriptResult{Text: "Third synthetic track."})
	f.stage(t, "track-c.alignment", "alignment", nil)
	require.NoError(t, f.lifecycle.Finish(context.Background(), f.run.ID, f.run.OwnerGeneration, models.StatusFailed, nil, "synthetic unfinished track"))
	response, _ := f.response(t)
	partial := partialResponseTranscript(t, response)
	require.Equal(t, "Shared synthetic words.\n\nShared synthetic words.\n\nThird synthetic track.", partial.Text)
	require.Empty(t, partial.Segments, "unassembled track-local timestamps must not be presented on a shared recording timeline")
	require.Empty(t, partial.WordSegments)
	for _, value := range response["stages"].([]interface{}) {
		stage := value.(map[string]interface{})
		require.True(t, strings.HasPrefix(stage["label"].(string), "Track · "))
	}
}

func TestRecoveryResponseLearningUsesOnlyImmutableAdmissionSnapshot(t *testing.T) {
	plan := models.AdaptivePlanSnapshot{PlanID: "saved-plan", ScopeKey: strings.Repeat("b", 64), ProfileRevision: 2, LearningGeneration: 3, StageKey: "alignment", Settings: models.AdaptiveStageSettings{Device: "cpu", Precision: "float32", BatchSize: 2, Concurrency: 1, WindowSeconds: 30}}
	policy := &models.AdaptiveExecutionPolicy{Learn: true, SnapshotTaken: true, ProfileID: "original-profile", ProfileRevision: 2, LearningGeneration: 3, LearnedPlans: []models.AdaptivePlanSnapshot{plan}}
	f := newRecoveryResponseFixture(t, models.WhisperXParams{RecoveryMode: "cpu_fallback", AdaptivePolicy: policy})
	// Current profile state deliberately differs from the admitted execution.
	profile := models.TranscriptionProfile{ID: "original-profile", Name: "Newer profile state", Revision: 9, LearningGeneration: 10, Parameters: models.WhisperXParams{RecoveryMode: "fixed"}}
	require.NoError(t, f.db.Create(&profile).Error)
	response, _ := f.response(t)
	learning := response["learning"].(map[string]interface{})
	require.Equal(t, "snapshotted", learning["status"])
	require.Equal(t, float64(2), learning["profile_revision"])
	require.Equal(t, float64(3), learning["learning_generation"])
	data, err := json.Marshal(learning["plans"])
	require.NoError(t, err)
	var plans []models.AdaptivePlanSnapshot
	require.NoError(t, json.Unmarshal(data, &plans))
	require.Equal(t, []models.AdaptivePlanSnapshot{plan}, plans)
	require.Contains(t, learning["reason"], "candidate")
	require.Contains(t, learning["reason"], "actually ran")
	for _, tc := range []struct {
		policy *models.AdaptiveExecutionPolicy
		status string
	}{
		{nil, "disabled"},
		{&models.AdaptiveExecutionPolicy{Learn: false, SnapshotTaken: true}, "disabled"},
		{&models.AdaptiveExecutionPolicy{Learn: true, LearnedPlans: []models.AdaptivePlanSnapshot{plan}}, "not_snapshotted"},
		{&models.AdaptiveExecutionPolicy{Learn: true, SnapshotTaken: true}, "no_saved_plans"},
	} {
		snapshot := recoveryLearningSnapshot(tc.policy)
		require.Equal(t, tc.status, snapshot["status"])
		require.Empty(t, snapshot["plans"], "missing/disabled snapshots must not invent learned starts")
	}
}
