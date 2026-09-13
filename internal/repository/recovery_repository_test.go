package repository

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"scriberr/internal/models"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func recoveryFixture(t *testing.T) (*RecoveryRepository, *gorm.DB, string) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "test.db")+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(10)
	t.Cleanup(func() { sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&models.TranscriptionJob{}, &models.TranscriptionJobExecution{}, &models.RecoveryStage{}, &models.RecoveryAttempt{}, &models.RecoveryCheckpoint{}, &models.RecoveryCheckpointDependency{}, &models.RecoveryDeletion{}))
	store, err := NewRecoveryRepository(db, t.TempDir(), RecoveryOptions{})
	require.NoError(t, err)
	recordingID := "track_../../private/audio.wav"
	require.NoError(t, db.Create(&models.TranscriptionJob{ID: recordingID, AudioPath: "private.wav", Status: models.StatusProcessing}).Error)
	return store, db, recordingID
}
func recoveryExecution(t *testing.T, db *gorm.DB, recordingID string) string {
	t.Helper()
	id := uuid.NewString()
	deadline := time.Now().Add(time.Hour)
	require.NoError(t, db.Create(&models.TranscriptionJobExecution{ID: id, TranscriptionJobID: recordingID, OwnerGeneration: 1, RecoveryVersion: 1, RecoveryState: "running", Status: models.StatusProcessing, StartedAt: time.Now(), DeadlineAt: &deadline}).Error)
	return id
}
func recoverySpec(recordingID, executionID string) StageSpec {
	hash := recoveryHash([]byte("fixed-content"))
	return StageSpec{RecordingID: recordingID, ExecutionID: executionID, NodeKey: "recognition", Kind: "combined", SchemaVersion: "transcript.v1", CompatibilityKey: hash, OwnerGeneration: 1, DurationSeconds: 10, RecoverableBoundary: true, Provenance: CheckpointProvenance{AudioSHA256: hash, PreparedAudioSHA256: hash, PreprocessingHash: hash, SettingsHash: hash, RuntimeFingerprint: hash, Implementation: "test-adapter-v1", Models: []ModelArtifactIdentity{{ModelID: "test/model", Revision: "immutable-revision"}}}}
}
func recoveryAttempt(t *testing.T, store *RecoveryRepository, spec StageSpec) (*models.RecoveryStage, *models.RecoveryAttempt) {
	t.Helper()
	stage, err := store.EnsureStage(context.Background(), spec)
	require.NoError(t, err)
	attempt, err := store.ClaimStage(context.Background(), stage.ID, spec.OwnerGeneration, AttemptSettings{Device: "cpu", Precision: "float32", BatchSize: 1, WindowSeconds: 30, SettingsHash: spec.Provenance.SettingsHash, Reason: "initial"})
	require.NoError(t, err)
	return stage, attempt
}
func recoveryText(text string) []byte {
	data, _ := json.Marshal(map[string]interface{}{"text": text, "language": "en", "segments": []map[string]interface{}{{"start": 0.0, "end": 2.0, "text": text, "speaker": "A"}, {"start": 1.0, "end": 3.0, "text": "overlap", "speaker": "B"}}, "metadata": map[string]string{"resolved_device": "cpu", "precision": "float32", "hf_token": "must-never-persist", "context": "private prompt"}})
	return data
}

func TestRecoveryAtomicCheckpointPreservesSurfaceAndOverlap(t *testing.T) {
	store, db, recording := recoveryFixture(t)
	execution := recoveryExecution(t, db, recording)
	spec := recoverySpec(recording, execution)
	stage, attempt := recoveryAttempt(t, store, spec)
	checkpoint, err := store.CommitCheckpoint(context.Background(), attempt.ID, 1, recoveryText("C++ foo.bar, punctuation!"), map[string][]byte{"hypothesis.json": []byte(`{"offset":0}`)})
	require.NoError(t, err)
	selected, data, err := store.SelectedCheckpoint(context.Background(), stage.ID)
	require.NoError(t, err)
	require.Equal(t, checkpoint.ID, selected.ID)
	require.Contains(t, string(data), "C++ foo.bar, punctuation!")
	require.Contains(t, string(data), `"start":1`)
	require.NotContains(t, string(data), "must-never-persist")
	require.NotContains(t, string(data), "private prompt")
	manifestData, err := os.ReadFile(filepath.Join(store.root, checkpoint.RelativePath, "manifest.json"))
	require.NoError(t, err)
	require.NotContains(t, string(manifestData), "private prompt")
	require.NotContains(t, checkpoint.RelativePath, "private")
	require.False(t, strings.Contains(checkpoint.RelativePath, ".."))
	var manifest CheckpointManifest
	require.NoError(t, json.Unmarshal(manifestData, &manifest))
	require.Equal(t, 1, manifest.Version)
	require.Len(t, manifest.Files, 2)
	require.Equal(t, "cpu", manifest.EffectiveSettings.Device)
	require.Equal(t, "float32", manifest.EffectiveSettings.Precision)
	again, err := store.CommitCheckpoint(context.Background(), attempt.ID, 1, recoveryText("C++ foo.bar, punctuation!"), map[string][]byte{"hypothesis.json": []byte(`{"offset":0}`)})
	require.NoError(t, err)
	require.Equal(t, checkpoint.ID, again.ID)
	_, err = store.CommitCheckpoint(context.Background(), attempt.ID, 1, recoveryText("C++ foo.bar, punctuation!"), map[string][]byte{"hypothesis.json": []byte(`{"offset":1}`)})
	require.ErrorIs(t, err, ErrRecoveryConflict)
	_, err = store.CommitCheckpoint(context.Background(), attempt.ID, 1, recoveryText("C++ foo.bar, punctuation!"), nil)
	require.ErrorIs(t, err, ErrRecoveryConflict)
	_, err = store.CommitCheckpoint(context.Background(), attempt.ID, 1, recoveryText("different output"), nil)
	require.ErrorIs(t, err, ErrRecoveryConflict)
	stages, err := store.ListStages(context.Background(), execution)
	require.NoError(t, err)
	require.Len(t, stages, 1)
	require.Len(t, stages[0].Attempts, 1)
	require.Equal(t, models.RecoverySucceeded, stages[0].Attempts[0].State)
}

func TestRecoveryFreshArtifactsAndImmutableReuseSelection(t *testing.T) {
	store, db, recording := recoveryFixture(t)
	firstExecution := recoveryExecution(t, db, recording)
	spec := recoverySpec(recording, firstExecution)
	firstStage, firstAttempt := recoveryAttempt(t, store, spec)
	first, err := store.CommitCheckpoint(context.Background(), firstAttempt.ID, 1, recoveryText("first"), nil)
	require.NoError(t, err)
	secondExecution := recoveryExecution(t, db, recording)
	spec.ExecutionID = secondExecution
	_, secondAttempt := recoveryAttempt(t, store, spec)
	second, err := store.CommitCheckpoint(context.Background(), secondAttempt.ID, 1, recoveryText("second"), nil)
	require.NoError(t, err)
	require.NotEqual(t, first.ID, second.ID)
	require.Equal(t, first.CompatibilityKey, second.CompatibilityKey)
	found, err := store.FindReusable(context.Background(), recording, spec.CompatibilityKey)
	require.NoError(t, err)
	require.Equal(t, second.ID, found.ID)
	spec.ExecutionID = recoveryExecution(t, db, recording)
	third, err := store.EnsureStage(context.Background(), spec)
	require.NoError(t, err)
	reused, data, err := store.ReuseCheckpoint(context.Background(), third.ID, 1, first.ID)
	require.NoError(t, err)
	require.Equal(t, first.ID, reused.ID)
	require.Contains(t, string(data), "first")
	_, _, err = store.ReuseCheckpoint(context.Background(), third.ID, 1, second.ID)
	require.ErrorIs(t, err, ErrRecoveryConflict)
	selected, _, err := store.SelectedCheckpoint(context.Background(), firstStage.ID)
	require.NoError(t, err)
	require.Equal(t, first.ID, selected.ID)
	_, err = store.FindReusable(context.Background(), "another-recording", spec.CompatibilityKey)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	spec.Provenance.SettingsHash = recoveryHash([]byte("changed prompt"))
	_, err = store.EnsureStage(context.Background(), spec)
	require.ErrorIs(t, err, ErrRecoveryConflict)
}

func TestRecoveryRejectsCorruptAndInvalidArtifacts(t *testing.T) {
	for _, payload := range []string{`{"text":null}`, `{"text":"x","segments":[{"start":null,"end":1}]}`, `{"text":"x","segments":[{"start":0,"end":null}]}`} {
		_, err := validateCheckpointPayload("combined", 10, []byte(payload))
		require.ErrorIs(t, err, ErrRecoveryCorrupt, payload)
	}
	for _, payload := range []string{`{"text":"x","segments":[{"start":-1,"end":1}]}`, `{"text":"x","segments":[{"start":1,"end":11}]}`, `{"text":"x","segments":[{"start":2,"end":1}]}`, `{"text":"x","segments":[{"end":1}]}`, `{"text":"x","segments":[{"start":0,"end":NaN}]}`, `{"text":"x","hf_token":"secret"}`} {
		_, err := validateCheckpointPayload("combined", 10, []byte(payload))
		require.ErrorIs(t, err, ErrRecoveryCorrupt, payload)
	}
	store, db, recording := recoveryFixture(t)
	spec := recoverySpec(recording, recoveryExecution(t, db, recording))
	stage, attempt := recoveryAttempt(t, store, spec)
	_, err := store.CommitCheckpoint(context.Background(), attempt.ID, 1, recoveryText("x"), map[string][]byte{"../escape": []byte("no")})
	require.Error(t, err)
	checkpoint, err := store.CommitCheckpoint(context.Background(), attempt.ID, 1, recoveryText("x"), nil)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(store.root, checkpoint.RelativePath, "result.json"), []byte(`{"text":"truncated"`), 0600))
	_, _, err = store.SelectedCheckpoint(context.Background(), stage.ID)
	require.ErrorIs(t, err, ErrRecoveryCorrupt)
	_, err = store.FindReusable(context.Background(), recording, spec.CompatibilityKey)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestRecoveryOwnershipCancellationAndCrashWindows(t *testing.T) {
	for _, change := range []string{"generation", "cancelled", "deadline", "after_rename_cancel", "after_rename_crash", "database_insert_failure"} {
		t.Run(change, func(t *testing.T) {
			store, db, recording := recoveryFixture(t)
			execution := recoveryExecution(t, db, recording)
			spec := recoverySpec(recording, execution)
			stage, attempt := recoveryAttempt(t, store, spec)
			mutate := func() {
				switch change {
				case "generation":
					require.NoError(t, db.Model(&models.TranscriptionJobExecution{}).Where("id = ?", execution).Update("owner_generation", 2).Error)
				case "deadline":
					require.NoError(t, db.Model(&models.TranscriptionJobExecution{}).Where("id = ?", execution).Update("deadline_at", time.Now().Add(-time.Minute)).Error)
				default:
					require.NoError(t, db.Model(&models.TranscriptionJobExecution{}).Where("id = ?", execution).Update("cancelled_at", time.Now()).Error)
				}
			}
			if strings.HasPrefix(change, "after_rename") {
				store.afterRename = func() error {
					if change == "after_rename_crash" {
						return errors.New("simulated process loss")
					}
					mutate()
					return nil
				}
			} else if change == "database_insert_failure" {
				require.NoError(t, db.Exec(`CREATE TRIGGER reject_checkpoint BEFORE INSERT ON recovery_checkpoints BEGIN SELECT RAISE(ABORT, 'simulated database failure'); END`).Error)
			} else {
				mutate()
			}
			_, err := store.CommitCheckpoint(context.Background(), attempt.ID, 1, recoveryText("durable candidate"), nil)
			require.Error(t, err)
			var count int64
			require.NoError(t, db.Model(&models.RecoveryCheckpoint{}).Count(&count).Error)
			require.Zero(t, count)
			_, _, err = store.SelectedCheckpoint(context.Background(), stage.ID)
			require.ErrorIs(t, err, gorm.ErrRecordNotFound)
			if strings.HasPrefix(change, "after_rename") || change == "database_insert_failure" {
				entries, err := os.ReadDir(filepath.Join(store.root, recoveryHash([]byte(recording)), spec.CompatibilityKey))
				require.NoError(t, err)
				require.Len(t, entries, 1, "rename-before-commit leaves an unreferenced artifact, never an advertised checkpoint")
			}
		})
	}
}

func TestRecoveryClaimsAreExclusiveAndResumeNeedsInterruptedAttempt(t *testing.T) {
	store, db, recording := recoveryFixture(t)
	execution := recoveryExecution(t, db, recording)
	spec := recoverySpec(recording, execution)
	stage, attempt := recoveryAttempt(t, store, spec)
	settings := AttemptSettings{Device: "cpu", Precision: "float32", SettingsHash: spec.Provenance.SettingsHash}
	_, err := store.ClaimStage(context.Background(), stage.ID, 1, settings)
	require.ErrorIs(t, err, ErrRecoveryConflict)
	require.NoError(t, db.Model(&models.TranscriptionJobExecution{}).Where("id = ?", execution).Update("owner_generation", 2).Error)
	_, err = store.ClaimStage(context.Background(), stage.ID, 2, settings)
	require.ErrorIs(t, err, ErrRecoveryConflict, "a lease change alone does not prove the prior process stopped")
	require.NoError(t, store.InterruptExecutionStages(context.Background(), execution, 1))
	next, err := store.ClaimStage(context.Background(), stage.ID, 2, settings)
	require.NoError(t, err)
	require.Equal(t, 2, next.AttemptNumber)
	_, err = store.CommitCheckpoint(context.Background(), attempt.ID, 1, recoveryText("late"), nil)
	require.ErrorIs(t, err, ErrRecoveryStaleOwner)
	_, err = store.CommitCheckpoint(context.Background(), next.ID, 2, recoveryText("current"), nil)
	require.NoError(t, err)
	// A separate node can be claimed exactly once under concurrent dispatch.
	spec.NodeKey = "speakers"
	spec.Kind = "diarize"
	spec.OwnerGeneration = 2
	other, err := store.EnsureStage(context.Background(), spec)
	require.NoError(t, err)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := store.ClaimStage(context.Background(), other.ID, 2, settings)
			results <- e
		}()
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else {
			require.ErrorIs(t, err, ErrRecoveryConflict)
		}
	}
	require.Equal(t, 1, success)
}

func TestRecoveryRetentionProtectsSelectionsAndDeletionIsRetryable(t *testing.T) {
	store, db, recording := recoveryFixture(t)
	execution := recoveryExecution(t, db, recording)
	spec := recoverySpec(recording, execution)
	stage, attempt := recoveryAttempt(t, store, spec)
	checkpoint, err := store.CommitCheckpoint(context.Background(), attempt.ID, 1, recoveryText("keep partial work"), nil)
	require.NoError(t, err)
	result, err := store.Collect(context.Background(), RetentionPolicy{MaxBytes: 1, Now: time.Now().Add(60 * 24 * time.Hour)})
	require.ErrorIs(t, err, ErrRecoveryQuota)
	require.Equal(t, 0, result.RemovedArtifacts)
	_, _, err = store.SelectedCheckpoint(context.Background(), stage.ID)
	require.NoError(t, err)
	store.options.MaxBytes = 1
	secondSpec := recoverySpec(recording, recoveryExecution(t, db, recording))
	_, secondAttempt := recoveryAttempt(t, store, secondSpec)
	_, err = store.CommitCheckpoint(context.Background(), secondAttempt.ID, 1, recoveryText("new"), nil)
	require.ErrorIs(t, err, ErrRecoveryQuota)
	store.removeAll = func(string) error { return errors.New("disk is temporarily read only") }
	require.Error(t, store.DeleteRecording(context.Background(), recording))
	var deletion models.RecoveryDeletion
	require.NoError(t, db.First(&deletion, "recording_id = ?", recording).Error)
	require.Nil(t, deletion.CompletedAt)
	require.Equal(t, "checkpoint_delete_failed", deletion.ErrorCode)
	_, err = store.CommitCheckpoint(context.Background(), secondAttempt.ID, 1, recoveryText("late"), nil)
	require.ErrorIs(t, err, ErrRecoveryStaleOwner)
	store.removeAll = os.RemoveAll
	require.NoError(t, store.RetryDeletions(context.Background()))
	require.NoError(t, store.DeleteRecording(context.Background(), recording))
	_, err = os.Stat(filepath.Join(store.root, checkpoint.RelativePath))
	require.True(t, os.IsNotExist(err))
}

func TestRecoveryRetentionCollectsOnlyUnreferencedAndInactiveOrphans(t *testing.T) {
	store, db, recording := recoveryFixture(t)
	execution := recoveryExecution(t, db, recording)
	spec := recoverySpec(recording, execution)
	_, attempt := recoveryAttempt(t, store, spec)
	store.afterRename = func() error { return errors.New("crash after rename") }
	_, err := store.CommitCheckpoint(context.Background(), attempt.ID, 1, recoveryText("orphan"), nil)
	require.Error(t, err)
	result, err := store.Collect(context.Background(), RetentionPolicy{Now: time.Now().Add(72 * time.Hour)})
	require.NoError(t, err)
	require.Zero(t, result.RemovedArtifacts, "the active owning attempt protects its uncommitted durable files")
	require.NoError(t, store.InterruptExecutionStages(context.Background(), execution, 1))
	result, err = store.Collect(context.Background(), RetentionPolicy{Now: time.Now().Add(72 * time.Hour)})
	require.NoError(t, err)
	require.Equal(t, 1, result.RemovedArtifacts)
	store.afterRename = nil
	spec.ExecutionID = recoveryExecution(t, db, recording)
	_, attempt = recoveryAttempt(t, store, spec)
	checkpoint, err := store.CommitCheckpoint(context.Background(), attempt.ID, 1, recoveryText("old result"), nil)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(store.root, checkpoint.RelativePath, "result.json"), []byte(strings.Repeat("corrupt", 1024)), 0600))
	require.NoError(t, db.Delete(&models.TranscriptionJobExecution{}, "id = ?", spec.ExecutionID).Error)
	result, err = store.Collect(context.Background(), RetentionPolicy{MaxBytes: 1, Now: time.Now().Add(40 * 24 * time.Hour)})
	require.NoError(t, err)
	require.Equal(t, 1, result.RemovedArtifacts)
	require.Zero(t, result.RemainingBytes, "quota accounting uses actual removed bytes, including damaged files")
}

func TestRecoveryUpstreamClosureAndAuxiliaryIntegrity(t *testing.T) {
	store, db, recording := recoveryFixture(t)
	firstExecution := recoveryExecution(t, db, recording)
	_, firstAttempt := recoveryAttempt(t, store, recoverySpec(recording, firstExecution))
	first, err := store.CommitCheckpoint(context.Background(), firstAttempt.ID, 1, recoveryText("C++ foo.bar"), map[string][]byte{"chunks/map.json": []byte(`{"offset":0}`)})
	require.NoError(t, err)
	secondSpec := recoverySpec(recording, recoveryExecution(t, db, recording))
	secondSpec.Kind, secondSpec.NodeKey = "align", "alignment"
	secondSpec.CompatibilityKey = recoveryHash([]byte("alignment with exact upstream"))
	secondSpec.Provenance.Upstream = []CheckpointInput{{ID: first.ID, ResultSHA256: first.ResultSHA256}}
	secondStage, secondAttempt := recoveryAttempt(t, store, secondSpec)
	second, err := store.CommitCheckpoint(context.Background(), secondAttempt.ID, 1, recoveryText("C++ foo.bar"), nil)
	require.NoError(t, err)
	// Only the downstream execution is retained. Its dependency closure must
	// protect the original recognition data even under age and quota pressure.
	require.NoError(t, db.Delete(&models.TranscriptionJobExecution{}, "id = ?", firstExecution).Error)
	result, err := store.Collect(context.Background(), RetentionPolicy{MaxBytes: 1, Now: time.Now().Add(60 * 24 * time.Hour)})
	require.ErrorIs(t, err, ErrRecoveryQuota)
	require.Zero(t, result.RemovedArtifacts)
	require.Equal(t, first.SizeBytes+second.SizeBytes, result.ProtectedBytes)
	_, _, err = store.SelectedCheckpoint(context.Background(), secondStage.ID)
	require.NoError(t, err)
	// Tampering with auxiliary provenance invalidates its downstream result,
	// even though the downstream result.json and manifest remain unchanged.
	require.NoError(t, os.WriteFile(filepath.Join(store.root, first.RelativePath, "auxiliary-artifacts", "chunks", "map.json"), []byte(`{"offset":1}`), 0600))
	_, _, err = store.SelectedCheckpoint(context.Background(), secondStage.ID)
	require.ErrorIs(t, err, ErrRecoveryCorrupt)
	_, err = store.FindReusable(context.Background(), recording, secondSpec.CompatibilityKey)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestRecoveryManifestAndSymlinkIntegrity(t *testing.T) {
	for _, target := range []string{"manifest", "result_symlink", "directory_symlink"} {
		t.Run(target, func(t *testing.T) {
			store, db, recording := recoveryFixture(t)
			stage, attempt := recoveryAttempt(t, store, recoverySpec(recording, recoveryExecution(t, db, recording)))
			checkpoint, err := store.CommitCheckpoint(context.Background(), attempt.ID, 1, recoveryText("original"), nil)
			require.NoError(t, err)
			path := filepath.Join(store.root, checkpoint.RelativePath)
			switch target {
			case "manifest":
				require.NoError(t, os.WriteFile(filepath.Join(path, "manifest.json"), []byte(`{"version":999}`), 0600))
			case "result_symlink":
				original, err := os.ReadFile(filepath.Join(path, "result.json"))
				require.NoError(t, err)
				outside := filepath.Join(t.TempDir(), "outside.json")
				require.NoError(t, os.WriteFile(outside, original, 0600))
				require.NoError(t, os.Remove(filepath.Join(path, "result.json")))
				require.NoError(t, os.Symlink(outside, filepath.Join(path, "result.json")))
			case "directory_symlink":
				outside := filepath.Join(t.TempDir(), "outside")
				require.NoError(t, os.Rename(path, outside))
				require.NoError(t, os.Symlink(outside, path))
			}
			_, _, err = store.SelectedCheckpoint(context.Background(), stage.ID)
			require.ErrorIs(t, err, ErrRecoveryCorrupt)
		})
	}
}

func TestRecoveryCancelledContextCannotPublishDurableCandidate(t *testing.T) {
	store, db, recording := recoveryFixture(t)
	stage, attempt := recoveryAttempt(t, store, recoverySpec(recording, recoveryExecution(t, db, recording)))
	ctx, cancel := context.WithCancel(context.Background())
	store.afterRename = func() error { cancel(); return nil }
	_, err := store.CommitCheckpoint(ctx, attempt.ID, 1, recoveryText("late result"), nil)
	require.ErrorIs(t, err, context.Canceled)
	var count int64
	require.NoError(t, db.Model(&models.RecoveryCheckpoint{}).Count(&count).Error)
	require.Zero(t, count)
	_, _, err = store.SelectedCheckpoint(context.Background(), stage.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}
