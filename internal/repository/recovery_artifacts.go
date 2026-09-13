package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"scriberr/internal/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Validate and normalize JSON without touching transcript surface text or
// imposing non-overlap/order constraints on speaker turns or native words.
func validateCheckpointPayload(kind string, duration float64, payload []byte) ([]byte, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(payload, &object); err != nil || object == nil {
		return nil, fmt.Errorf("%w: invalid result JSON", ErrRecoveryCorrupt)
	}
	allowed := map[string]bool{"text": true, "language": true, "segments": true, "word_segments": true, "confidence": true, "processing_time": true, "model_used": true, "metadata": true, "speaker_count": true, "speakers": true}
	if kind == "recognition" || kind == "recognize" {
		allowed["recognition_state"] = true
	}
	for key := range object {
		if !allowed[key] {
			return nil, fmt.Errorf("%w: unsupported result field", ErrRecoveryCorrupt)
		}
	}
	if kind != "diarize" && kind != "diarization" {
		var text string
		if data, ok := object["text"]; !ok || bytes.Equal(bytes.TrimSpace(data), []byte("null")) || json.Unmarshal(data, &text) != nil {
			return nil, fmt.Errorf("%w: transcript text is required", ErrRecoveryCorrupt)
		}
	}
	for _, key := range []string{"segments", "word_segments"} {
		data, exists := object[key]
		if !exists {
			if (kind == "diarize" || kind == "diarization") && key == "segments" {
				return nil, fmt.Errorf("%w: speaker intervals are required", ErrRecoveryCorrupt)
			}
			continue
		}
		var rows []map[string]json.RawMessage
		if err := json.Unmarshal(data, &rows); err != nil {
			return nil, fmt.Errorf("%w: invalid timestamp rows", ErrRecoveryCorrupt)
		}
		for _, row := range rows {
			var start, end *float64
			if json.Unmarshal(row["start"], &start) != nil || json.Unmarshal(row["end"], &end) != nil || start == nil || end == nil || math.IsNaN(*start) || math.IsInf(*start, 0) || math.IsNaN(*end) || math.IsInf(*end, 0) || *start < 0 || *end < *start || *end > duration+1e-6 {
				return nil, fmt.Errorf("%w: timestamp outside source audio", ErrRecoveryCorrupt)
			}
			if kind == "diarize" || kind == "diarization" {
				var speaker string
				if json.Unmarshal(row["speaker"], &speaker) != nil || speaker == "" {
					return nil, fmt.Errorf("%w: speaker identity is required", ErrRecoveryCorrupt)
				}
			}
			if key == "word_segments" {
				var word string
				if json.Unmarshal(row["word"], &word) != nil || word == "" {
					return nil, fmt.Errorf("%w: aligned word is required", ErrRecoveryCorrupt)
				}
			}
		}
	}
	// Result metadata is diagnostic, not another parameter/credential store.
	// Unknown fields are omitted rather than copying third-party request data.
	if data, ok := object["metadata"]; ok && string(data) != "null" {
		var metadata map[string]string
		if json.Unmarshal(data, &metadata) != nil {
			return nil, fmt.Errorf("%w: invalid result metadata", ErrRecoveryCorrupt)
		}
		safe := map[string]string{}
		for _, key := range []string{"actual_batch_size", "actual_window_seconds", "learned_plan_id", "alignment_device", "alignment_precision", "alignment_batch_size", "alignment_window_seconds", "recognition_device", "recognition_precision", "resolved_device", "precision", "timestamp_source", "speaker_scope", "duration_seconds", "chunk_count", "model_revision", "context_mode", "diarization_model", "diarization_device", "model_id", "framework", "version", "asr_device_fallback", "diarization_device_fallback", "asr_fallback_reason", "diarization_fallback_reason"} {
			if value, exists := metadata[key]; exists {
				safe[key] = value
			}
		}
		object["metadata"], _ = json.Marshal(safe)
	}
	return json.Marshal(object)
}

func safeArtifactRelative(path string) bool {
	if path == "" || filepath.IsAbs(path) || strings.Contains(path, "\\") {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." || strings.Contains(part, ":") || !recoveryLabel.MatchString(part) {
			return false
		}
	}
	return true
}

func (r *RecoveryRepository) artifactPath(checkpoint *models.RecoveryCheckpoint) (string, error) {
	expected := filepath.Join(recoveryHash([]byte(checkpoint.RecordingID)), checkpoint.CompatibilityKey, checkpoint.ID)
	if _, err := uuid.Parse(checkpoint.ID); err != nil || !recoverySHA.MatchString(checkpoint.CompatibilityKey) || checkpoint.RelativePath != expected {
		return "", ErrRecoveryCorrupt
	}
	path := r.root
	for _, part := range strings.Split(expected, string(filepath.Separator)) {
		path = filepath.Join(path, part)
		stat, err := os.Lstat(path)
		if err != nil || !stat.IsDir() || stat.Mode()&os.ModeSymlink != 0 {
			return "", ErrRecoveryCorrupt
		}
	}
	return path, nil
}

func durableFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, err = file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}
func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func (r *RecoveryRepository) CommitCheckpoint(ctx context.Context, attemptID string, generation int64, payload []byte, auxiliary map[string][]byte) (*models.RecoveryCheckpoint, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var attempt *models.RecoveryAttempt
	var stage *models.RecoveryStage
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		attempt, stage, err = r.ownedAttempt(tx, attemptID, generation)
		return err
	})
	if err != nil {
		return nil, err
	}
	clean, err := validateCheckpointPayload(stage.Kind, stage.DurationSeconds, payload)
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{"result.json": clean}
	for path, data := range auxiliary {
		if !safeArtifactRelative(path) {
			return nil, errors.New("invalid auxiliary artifact path")
		}
		files["auxiliary-artifacts/"+path] = data
	}
	var existing models.RecoveryCheckpoint
	if err := r.db.WithContext(ctx).Where("producer_attempt_id = ?", attemptID).First(&existing).Error; err == nil {
		if existing.ResultSHA256 != recoveryHash(clean) {
			return nil, ErrRecoveryConflict
		}
		_, manifest, err := r.readCheckpoint(ctx, &existing)
		if err != nil {
			return nil, err
		}
		if len(manifest.Files) != len(files) {
			return nil, ErrRecoveryConflict
		}
		for _, file := range manifest.Files {
			data, exists := files[file.Path]
			if !exists || int64(len(data)) != file.SizeBytes || recoveryHash(data) != file.SHA256 {
				return nil, ErrRecoveryConflict
			}
		}
		return &existing, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if attempt.State != models.RecoveryRunning || stage.State != models.RecoveryRunning {
		return nil, ErrRecoveryConflict
	}
	// The stage keeps its requested identity. Each adaptive attempt owns a
	// distinct immutable effective plan and writes under that compatibility key.
	if attempt.ProvenanceJSON != "" {
		stage.ProvenanceJSON = attempt.ProvenanceJSON
		stage.CompatibilityKey = attempt.CompatibilityKey
	}
	provenance := CheckpointProvenance{}
	if err := json.Unmarshal([]byte(stage.ProvenanceJSON), &provenance); err != nil {
		return nil, ErrRecoveryCorrupt
	}
	for _, input := range provenance.Upstream {
		var upstream models.RecoveryCheckpoint
		if err := r.db.WithContext(ctx).First(&upstream, "id = ? AND recording_id = ?", input.ID, stage.RecordingID).Error; err != nil {
			return nil, ErrRecoveryCorrupt
		}
		if upstream.ResultSHA256 != input.ResultSHA256 {
			return nil, ErrRecoveryCorrupt
		}
		if _, _, err := r.readCheckpoint(ctx, &upstream); err != nil {
			return nil, err
		}
	}
	now := time.Now().UTC()
	checkpoint := &models.RecoveryCheckpoint{ID: uuid.NewString(), RecordingID: stage.RecordingID, CompatibilityKey: stage.CompatibilityKey, Kind: stage.Kind, SchemaVersion: stage.SchemaVersion, ProducerExecutionID: stage.ExecutionID, ProducerAttemptID: attempt.ID, ResultSHA256: recoveryHash(clean), CreatedAt: now}
	checkpoint.RelativePath = filepath.Join(recoveryHash([]byte(stage.RecordingID)), stage.CompatibilityKey, checkpoint.ID)
	manifest := CheckpointManifest{Version: 1, ID: checkpoint.ID, RecordingHash: recoveryHash([]byte(stage.RecordingID)), ExecutionID: stage.ExecutionID, StageID: stage.ID, AttemptID: attempt.ID, OwnerGeneration: generation, Kind: stage.Kind, SchemaVersion: stage.SchemaVersion, CompatibilityKey: stage.CompatibilityKey, DurationSeconds: stage.DurationSeconds, CreatedAt: now, Provenance: provenance}
	manifest.EffectiveSettings = AttemptSettings{Device: attempt.Device, Precision: attempt.Precision, BatchSize: attempt.BatchSize, WindowSeconds: attempt.WindowSeconds, SettingsHash: attempt.SettingsHash, Reason: attempt.Reason}
	manifest.EffectiveSettings.OverlapSeconds = attempt.OverlapSeconds
	manifest.EffectiveSettings.StitchingVersion = attempt.StitchingVersion
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		data := files[path]
		manifest.Files = append(manifest.Files, CheckpointFile{Path: path, SHA256: recoveryHash(data), SizeBytes: int64(len(data))})
		checkpoint.SizeBytes += int64(len(data))
	}
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	checkpoint.ManifestSHA256 = recoveryHash(manifestData)
	checkpoint.SizeBytes += int64(len(manifestData))
	if r.options.MaxBytes > 0 {
		usage, err := r.diskUsage()
		if err != nil {
			return nil, err
		}
		if usage+checkpoint.SizeBytes > r.options.MaxBytes {
			return nil, ErrRecoveryQuota
		}
	}
	parent := filepath.Dir(filepath.Join(r.root, checkpoint.RelativePath))
	if err := r.makeArtifactParents(stage.RecordingID, stage.CompatibilityKey); err != nil {
		return nil, err
	}
	temporary, err := os.MkdirTemp(parent, ".attempt-"+attempt.ID+"-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(temporary)
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := durableFile(filepath.Join(temporary, filepath.FromSlash(path)), files[path]); err != nil {
			return nil, err
		}
	}
	if err := durableFile(filepath.Join(temporary, "manifest.json"), manifestData); err != nil {
		return nil, err
	}
	// Flush nested directories before making the complete artifact visible.
	if err := filepath.WalkDir(temporary, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return syncDirectory(path)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.Rename(temporary, filepath.Join(r.root, checkpoint.RelativePath)); err != nil {
		return nil, err
	}
	if err := syncDirectory(parent); err != nil {
		return nil, err
	}
	// An error/crash here leaves an orphan for reconciliation, never a DB hit.
	if r.afterRename != nil {
		if err := r.afterRename(); err != nil {
			return nil, err
		}
	}
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		currentAttempt, currentStage, err := r.ownedAttempt(tx, attemptID, generation)
		if err != nil {
			return err
		}
		if currentAttempt.State != models.RecoveryRunning || currentStage.State != models.RecoveryRunning || currentStage.CheckpointID != nil {
			return ErrRecoveryConflict
		}
		if err := tx.Create(checkpoint).Error; err != nil {
			return err
		}
		for _, input := range provenance.Upstream {
			if err := tx.Create(&models.RecoveryCheckpointDependency{CheckpointID: checkpoint.ID, UpstreamCheckpointID: input.ID}).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(currentAttempt).Updates(map[string]interface{}{"state": models.RecoverySucceeded, "checkpoint_id": checkpoint.ID, "completed_at": now}).Error; err != nil {
			return err
		}
		return tx.Model(currentStage).Updates(map[string]interface{}{"state": models.RecoverySucceeded, "checkpoint_id": checkpoint.ID}).Error
	})
	if err != nil {
		return nil, err
	}
	return checkpoint, nil
}

func (r *RecoveryRepository) makeArtifactParents(recordingID, compatibility string) error {
	path := r.root
	for _, part := range []string{recoveryHash([]byte(recordingID)), compatibility} {
		path = filepath.Join(path, part)
		if err := os.Mkdir(path, 0700); err != nil && !os.IsExist(err) {
			return err
		}
		stat, err := os.Lstat(path)
		if err != nil || !stat.IsDir() || stat.Mode()&os.ModeSymlink != 0 {
			return ErrRecoveryCorrupt
		}
		if err := syncDirectory(filepath.Dir(path)); err != nil {
			return err
		}
	}
	return nil
}

func (r *RecoveryRepository) readCheckpoint(ctx context.Context, checkpoint *models.RecoveryCheckpoint) ([]byte, *CheckpointManifest, error) {
	return r.readCheckpointChain(ctx, checkpoint, map[string]bool{})
}

func (r *RecoveryRepository) readCheckpointChain(ctx context.Context, checkpoint *models.RecoveryCheckpoint, ancestors map[string]bool) ([]byte, *CheckpointManifest, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if ancestors[checkpoint.ID] || len(ancestors) >= 64 {
		return nil, nil, ErrRecoveryCorrupt
	}
	ancestors[checkpoint.ID] = true
	defer delete(ancestors, checkpoint.ID)
	var deleted int64
	if err := r.db.WithContext(ctx).Model(&models.RecoveryDeletion{}).Where("recording_id = ?", checkpoint.RecordingID).Count(&deleted).Error; err != nil {
		return nil, nil, err
	}
	if deleted > 0 {
		return nil, nil, ErrRecoveryCorrupt
	}
	path, err := r.artifactPath(checkpoint)
	if err != nil {
		return nil, nil, err
	}
	data, err := readRegularFile(filepath.Join(path, "manifest.json"))
	if err != nil || recoveryHash(data) != checkpoint.ManifestSHA256 {
		return nil, nil, ErrRecoveryCorrupt
	}
	var manifest CheckpointManifest
	if json.Unmarshal(data, &manifest) != nil || manifest.Version != 1 || manifest.ID != checkpoint.ID || manifest.RecordingHash != recoveryHash([]byte(checkpoint.RecordingID)) || manifest.ExecutionID != checkpoint.ProducerExecutionID || manifest.AttemptID != checkpoint.ProducerAttemptID || manifest.Kind != checkpoint.Kind || manifest.SchemaVersion != checkpoint.SchemaVersion || manifest.CompatibilityKey != checkpoint.CompatibilityKey {
		return nil, nil, ErrRecoveryCorrupt
	}
	seen := map[string]bool{}
	var payload []byte
	size := int64(len(data))
	for _, file := range manifest.Files {
		if !safeArtifactRelative(file.Path) || seen[file.Path] || !recoverySHA.MatchString(file.SHA256) || file.SizeBytes < 0 {
			return nil, nil, ErrRecoveryCorrupt
		}
		seen[file.Path] = true
		fullPath := path
		parts := strings.Split(file.Path, "/")
		for _, part := range parts[:len(parts)-1] {
			fullPath = filepath.Join(fullPath, part)
			stat, err := os.Lstat(fullPath)
			if err != nil || !stat.IsDir() || stat.Mode()&os.ModeSymlink != 0 {
				return nil, nil, ErrRecoveryCorrupt
			}
		}
		bytes, err := readRegularFile(filepath.Join(path, filepath.FromSlash(file.Path)))
		if err != nil || int64(len(bytes)) != file.SizeBytes || recoveryHash(bytes) != file.SHA256 {
			return nil, nil, ErrRecoveryCorrupt
		}
		size += file.SizeBytes
		if file.Path == "result.json" {
			payload = bytes
			if file.SHA256 != checkpoint.ResultSHA256 {
				return nil, nil, ErrRecoveryCorrupt
			}
		}
	}
	if payload == nil || size != checkpoint.SizeBytes {
		return nil, nil, ErrRecoveryCorrupt
	}
	validated, err := validateCheckpointPayload(checkpoint.Kind, manifest.DurationSeconds, payload)
	if err != nil || recoveryHash(validated) != checkpoint.ResultSHA256 {
		return nil, nil, ErrRecoveryCorrupt
	}
	for _, input := range manifest.Provenance.Upstream {
		var upstream models.RecoveryCheckpoint
		if err := r.db.WithContext(ctx).First(&upstream, "id = ? AND recording_id = ?", input.ID, checkpoint.RecordingID).Error; err != nil || upstream.ResultSHA256 != input.ResultSHA256 {
			return nil, nil, ErrRecoveryCorrupt
		}
		if _, _, err := r.readCheckpointChain(ctx, &upstream, ancestors); err != nil {
			return nil, nil, err
		}
	}
	return payload, &manifest, nil
}
func readRegularFile(path string) ([]byte, error) {
	stat, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !stat.Mode().IsRegular() {
		return nil, ErrRecoveryCorrupt
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(file)
}

func (r *RecoveryRepository) SelectedCheckpoint(ctx context.Context, stageID string) (*models.RecoveryCheckpoint, []byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var stage models.RecoveryStage
	if err := r.db.WithContext(ctx).First(&stage, "id = ?", stageID).Error; err != nil {
		return nil, nil, err
	}
	if stage.State != models.RecoverySucceeded || stage.CheckpointID == nil {
		return nil, nil, gorm.ErrRecordNotFound
	}
	var checkpoint models.RecoveryCheckpoint
	if err := r.db.WithContext(ctx).First(&checkpoint, "id = ? AND recording_id = ?", *stage.CheckpointID, stage.RecordingID).Error; err != nil {
		return nil, nil, ErrRecoveryCorrupt
	}
	if checkpoint.ProducerExecutionID == stage.ExecutionID {
		var producer models.RecoveryAttempt
		if err := r.db.WithContext(ctx).First(&producer, "id = ? AND stage_id = ? AND checkpoint_id = ? AND state = ?", checkpoint.ProducerAttemptID, stage.ID, checkpoint.ID, models.RecoverySucceeded).Error; err != nil {
			return nil, nil, ErrRecoveryCorrupt
		}
		if producer.ProvenanceJSON != "" {
			stage.ProvenanceJSON = producer.ProvenanceJSON
			stage.CompatibilityKey = producer.CompatibilityKey
		}
	}
	if checkpoint.CompatibilityKey != stage.CompatibilityKey {
		return nil, nil, ErrRecoveryCorrupt
	}
	data, manifest, err := r.readCheckpoint(ctx, &checkpoint)
	if err == nil {
		provenance, _ := json.Marshal(manifest.Provenance)
		if checkpoint.Kind != stage.Kind || checkpoint.SchemaVersion != stage.SchemaVersion || manifest.DurationSeconds != stage.DurationSeconds || string(provenance) != stage.ProvenanceJSON {
			return nil, nil, ErrRecoveryCorrupt
		}
	}
	return &checkpoint, data, err
}
func (r *RecoveryRepository) FindReusable(ctx context.Context, recordingID, compatibilityKey string) (*models.RecoveryCheckpoint, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var rows []models.RecoveryCheckpoint
	if err := r.db.WithContext(ctx).Where("recording_id = ? AND compatibility_key = ?", recordingID, compatibilityKey).Order("created_at DESC,id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, checkpoint := range rows {
		if _, _, err := r.readCheckpoint(ctx, &checkpoint); err == nil {
			return &checkpoint, nil
		} else if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return nil, gorm.ErrRecordNotFound
}
func (r *RecoveryRepository) ReuseCheckpoint(ctx context.Context, stageID string, generation int64, checkpointID string) (*models.RecoveryCheckpoint, []byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var stage models.RecoveryStage
	if err := r.db.WithContext(ctx).First(&stage, "id = ?", stageID).Error; err != nil {
		return nil, nil, err
	}
	var checkpoint models.RecoveryCheckpoint
	if err := r.db.WithContext(ctx).First(&checkpoint, "id = ? AND recording_id = ? AND compatibility_key = ?", checkpointID, stage.RecordingID, stage.CompatibilityKey).Error; err != nil {
		return nil, nil, ErrRecoveryConflict
	}
	data, manifest, err := r.readCheckpoint(ctx, &checkpoint)
	if err != nil {
		return nil, nil, err
	}
	provenance, _ := json.Marshal(manifest.Provenance)
	if checkpoint.Kind != stage.Kind || checkpoint.SchemaVersion != stage.SchemaVersion || manifest.DurationSeconds != stage.DurationSeconds || string(provenance) != stage.ProvenanceJSON {
		return nil, nil, ErrRecoveryConflict
	}
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := r.fence(tx, stage.RecordingID, stage.ExecutionID, generation); err != nil {
			return err
		}
		if err := tx.First(&stage, "id = ?", stageID).Error; err != nil {
			return err
		}
		if stage.CheckpointID != nil {
			if *stage.CheckpointID == checkpointID {
				return nil
			}
			return ErrRecoveryConflict
		}
		if stage.State == models.RecoveryRunning || stage.State == models.RecoveryWaiting || stage.State == models.RecoveryCancelled {
			return ErrRecoveryConflict
		}
		return tx.Model(&stage).Updates(map[string]interface{}{"state": models.RecoverySucceeded, "owner_generation": generation, "checkpoint_id": checkpointID, "reused_from_execution_id": checkpoint.ProducerExecutionID}).Error
	})
	return &checkpoint, data, err
}
