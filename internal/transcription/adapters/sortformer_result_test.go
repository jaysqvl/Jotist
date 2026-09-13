package adapters

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scriberr/internal/models"
	"scriberr/internal/repository"
	"scriberr/internal/transcription/interfaces"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Public 15-second deployment fixture: the final Sortformer frame ended 30ms
// beyond the physical input. This is the exact failed interval, without audio.
const deployedSortformerJSON = `{"segments":[{"start":0.72,"end":15.03,"speaker":"speaker_0","duration":14.31,"confidence":1}],"speakers":["speaker_0"],"speaker_count":1}`

const paddedSortformerJSON = `{"segments":[
	{"start":0,"end":1,"speaker":"speaker_0","confidence":0.9},
	{"start":14,"end":15.04,"speaker":"speaker_0","confidence":0.8},
	{"start":14.9,"end":15.02,"speaker":"speaker_1","confidence":0.7},
	{"start":15.04,"end":15.12,"speaker":"padding_only","confidence":0.6}],
	"speakers":["speaker_0","speaker_1","padding_only"],"speaker_count":3}`

func TestSortformerResultBoundsPaddedFramesAndPreservesOverlap(t *testing.T) {
	for _, format := range []string{"json", "rttm"} {
		t.Run(format, func(t *testing.T) {
			folder := t.TempDir()
			body := paddedSortformerJSON
			if format == "rttm" {
				body = "SPEAKER fixture 1 0 1 <NA> <NA> speaker_0 <NA> <NA>\n" +
					"SPEAKER fixture 1 14 1.04 <NA> <NA> speaker_0 <NA> <NA>\n" +
					"SPEAKER fixture 1 14.9 0.12 <NA> <NA> speaker_1 <NA> <NA>\n" +
					"SPEAKER fixture 1 15.04 0.08 <NA> <NA> padding_only <NA> <NA>\n"
			}
			require.NoError(t, os.WriteFile(filepath.Join(folder, "result."+format), []byte(body), 0600))
			adapter := NewSortformerAdapter(t.TempDir())
			result, err := adapter.parseResult(folder, interfaces.AudioInput{Duration: 15 * time.Second}, map[string]interface{}{"output_format": format})
			require.NoError(t, err)
			require.Len(t, result.Segments, 3)
			require.Equal(t, float64(1), result.Segments[0].End)
			require.Equal(t, float64(14), result.Segments[1].Start)
			require.Equal(t, float64(15), result.Segments[1].End)
			require.Equal(t, 14.9, result.Segments[2].Start)
			require.Equal(t, float64(15), result.Segments[2].End)
			require.Equal(t, []string{"speaker_0", "speaker_1"}, result.Speakers)
			require.Equal(t, 2, result.SpeakerCount)
			if format == "json" {
				require.Equal(t, 0.8, result.Segments[1].Confidence)
			}
		})
	}
}

func TestSortformerBoundsRejectMalformedIntervalsWithoutInventingDuration(t *testing.T) {
	for _, segment := range []interfaces.DiarizationSegment{
		{Start: math.NaN(), End: 1, Speaker: "speaker_0"},
		{Start: 0, End: math.Inf(1), Speaker: "speaker_0"},
		{Start: 2, End: 1, Speaker: "speaker_0"},
		{Start: 0, End: 1, Speaker: ""},
	} {
		_, err := boundSortformerResult(&interfaces.DiarizationResult{Segments: []interfaces.DiarizationSegment{segment}}, 15*time.Second)
		require.Error(t, err)
	}
	result, err := boundSortformerResult(&interfaces.DiarizationResult{Segments: []interfaces.DiarizationSegment{
		{Start: 14, End: 15.04, Speaker: "speaker_0"},
	}}, 0)
	require.NoError(t, err)
	require.Equal(t, 15.04, result.Segments[0].End, "unknown physical duration must not be inferred from predictions")
	result, err = boundSortformerResult(&interfaces.DiarizationResult{Segments: []interfaces.DiarizationSegment{
		{Start: -0.04, End: 0.12, Speaker: "speaker_0"},
		{Start: 15, End: 15.04, Speaker: "padding_only"},
	}}, 15*time.Second)
	require.NoError(t, err)
	require.Equal(t, []interfaces.DiarizationSegment{{Start: 0, End: 0.12, Speaker: "speaker_0"}}, result.Segments)
}

func TestSortformerPaddedResultPersistsAndReusesStrictCheckpoint(t *testing.T) {
	ctx := context.Background()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&models.TranscriptionJob{}, &models.TranscriptionJobExecution{}, &models.RecoveryStage{}, &models.RecoveryAttempt{}, &models.RecoveryCheckpoint{}, &models.RecoveryCheckpointDependency{}, &models.RecoveryDeletion{}))
	store, err := repository.NewRecoveryRepository(db, t.TempDir(), repository.RecoveryOptions{})
	require.NoError(t, err)
	require.NoError(t, db.Create(&models.TranscriptionJob{ID: "fixture", AudioPath: "never-read.wav", Status: models.StatusProcessing}).Error)
	deadline := time.Now().Add(time.Hour)
	require.NoError(t, db.Create(&models.TranscriptionJobExecution{ID: "run", TranscriptionJobID: "fixture", RecoveryVersion: 1, OwnerGeneration: 1, RecoveryState: "running", Status: models.StatusProcessing, DeadlineAt: &deadline}).Error)
	hash := strings.Repeat("a", 64)
	stage, err := store.EnsureStage(ctx, repository.StageSpec{RecordingID: "fixture", ExecutionID: "run", NodeKey: "diarization", Kind: "diarize", SchemaVersion: "1", CompatibilityKey: hash, OwnerGeneration: 1, DurationSeconds: 15, RecoverableBoundary: true,
		Provenance: repository.CheckpointProvenance{AudioSHA256: hash, PreparedAudioSHA256: hash, PreprocessingHash: hash, SettingsHash: hash, RuntimeFingerprint: hash, Implementation: "test-v1", Models: []repository.ModelArtifactIdentity{{ModelID: "sortformer", Revision: "test"}}}})
	require.NoError(t, err)
	attempt, err := store.ClaimStage(ctx, stage.ID, 1, repository.AttemptSettings{Device: "cpu", Precision: "float32", BatchSize: 1, SettingsHash: hash})
	require.NoError(t, err)
	folder := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(folder, "result.json"), []byte(deployedSortformerJSON), 0600))
	adapter := NewSortformerAdapter(t.TempDir())
	raw, err := adapter.parseJSONResult(folder)
	require.NoError(t, err)
	payload, err := json.Marshal(raw)
	require.NoError(t, err)
	_, err = store.CommitCheckpoint(ctx, attempt.ID, 1, payload, nil)
	require.ErrorIs(t, err, repository.ErrRecoveryCorrupt, "unbounded model output must remain invalid")
	require.ErrorContains(t, err, "timestamp outside source audio")
	bounded, err := adapter.parseResult(folder, interfaces.AudioInput{Duration: 15 * time.Second}, map[string]interface{}{"output_format": "json"})
	require.NoError(t, err)
	require.Equal(t, []interfaces.DiarizationSegment{{Start: 0.72, End: 15, Speaker: "speaker_0", Confidence: 1}}, bounded.Segments)
	payload, err = json.Marshal(bounded)
	require.NoError(t, err)
	checkpoint, err := store.CommitCheckpoint(ctx, attempt.ID, 1, payload, nil)
	require.NoError(t, err)
	selected, saved, err := store.SelectedCheckpoint(ctx, stage.ID)
	require.NoError(t, err)
	require.Equal(t, checkpoint.ID, selected.ID)
	var restored interfaces.DiarizationResult
	require.NoError(t, json.Unmarshal(saved, &restored))
	require.Equal(t, *bounded, restored, "checkpoint validation must preserve overlapping normalized annotations")
	found, err := store.FindReusable(ctx, "fixture", hash)
	require.NoError(t, err)
	require.Equal(t, checkpoint.ID, found.ID)
}
