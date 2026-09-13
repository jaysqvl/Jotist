package repository

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"scriberr/internal/models"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type RetentionPolicy struct {
	UnreferencedAge time.Duration
	TemporaryAge    time.Duration
	MaxBytes        int64 // zero uses the repository's configured quota; zero there is unbounded
	Now             time.Time
}
type RecoveryCollectionResult struct {
	RemovedArtifacts int   `json:"removed_artifacts"`
	RemovedBytes     int64 `json:"removed_bytes"`
	RemainingBytes   int64 `json:"remaining_bytes"`
	ProtectedBytes   int64 `json:"protected_bytes"`
}

func (r *RecoveryRepository) diskUsage() (int64, error) {
	return recoveryTreeSize(r.root)
}

func recoveryTreeSize(root string) (int64, error) {
	var size int64
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return ErrRecoveryCorrupt
		}
		if !entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			size += info.Size()
		}
		return nil
	})
	return size, err
}

// DeleteRecording tombstones the recording before removing its private derived
// directory. It never receives or removes source audio/model-cache paths.
func (r *RecoveryRepository) DeleteRecording(ctx context.Context, recordingID string) error {
	if recordingID == "" {
		return errors.New("recording ID is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	deletion := models.RecoveryDeletion{RecordingID: recordingID, RequestedAt: time.Now().UTC()}
	if err := r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&deletion).Error; err != nil {
		return err
	}
	path := filepath.Join(r.root, recoveryHash([]byte(recordingID)))
	if err := r.removeAll(path); err != nil {
		_ = r.db.WithContext(context.WithoutCancel(ctx)).Model(&models.RecoveryDeletion{}).Where("recording_id = ?", recordingID).Update("error_code", "checkpoint_delete_failed").Error
		return errors.New("checkpoint deletion failed; cleanup is retained for retry")
	}
	if err := syncDirectory(r.root); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ids []string
		if err := tx.Model(&models.RecoveryCheckpoint{}).Where("recording_id = ?", recordingID).Pluck("id", &ids).Error; err != nil {
			return err
		}
		if len(ids) > 0 {
			if err := tx.Where("checkpoint_id IN ? OR upstream_checkpoint_id IN ?", ids, ids).Delete(&models.RecoveryCheckpointDependency{}).Error; err != nil {
				return err
			}
		}
		stageIDs := tx.Model(&models.RecoveryStage{}).Select("id").Where("recording_id = ?", recordingID)
		if err := tx.Where("stage_id IN (?)", stageIDs).Delete(&models.RecoveryAttempt{}).Error; err != nil {
			return err
		}
		if err := tx.Where("recording_id = ?", recordingID).Delete(&models.RecoveryStage{}).Error; err != nil {
			return err
		}
		if err := tx.Where("recording_id = ?", recordingID).Delete(&models.RecoveryCheckpoint{}).Error; err != nil {
			return err
		}
		return tx.Model(&models.RecoveryDeletion{}).Where("recording_id = ?", recordingID).Updates(map[string]interface{}{"completed_at": time.Now().UTC(), "error_code": ""}).Error
	})
}

func (r *RecoveryRepository) RetryDeletions(ctx context.Context) error {
	var rows []models.RecoveryDeletion
	if err := r.db.WithContext(ctx).Where("completed_at IS NULL").Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		if err := r.DeleteRecording(ctx, row.RecordingID); err != nil {
			return err
		}
	}
	return nil
}

// Collection is serialized with reads/selections/commits on this server. Every
// retained execution selection, its dependency closure and running attempt is
// protected; a quota cannot silently evict resumable work.
func (r *RecoveryRepository) Collect(ctx context.Context, policy RetentionPolicy) (RecoveryCollectionResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := RecoveryCollectionResult{}
	if policy.UnreferencedAge <= 0 {
		policy.UnreferencedAge = 30 * 24 * time.Hour
	}
	if policy.TemporaryAge <= 0 {
		policy.TemporaryAge = 24 * time.Hour
	}
	if policy.Now.IsZero() {
		policy.Now = time.Now().UTC()
	}
	if policy.MaxBytes == 0 {
		policy.MaxBytes = r.options.MaxBytes
	}
	if policy.MaxBytes < 0 {
		return result, errors.New("invalid checkpoint quota")
	}
	usage, err := r.diskUsage()
	if err != nil {
		return result, err
	}
	var checkpoints []models.RecoveryCheckpoint
	if err := r.db.WithContext(ctx).Find(&checkpoints).Error; err != nil {
		return result, err
	}
	byPath := map[string]models.RecoveryCheckpoint{}
	byID := map[string]models.RecoveryCheckpoint{}
	for _, checkpoint := range checkpoints {
		byPath[filepath.Clean(checkpoint.RelativePath)] = checkpoint
		byID[checkpoint.ID] = checkpoint
	}
	var selected []string
	if err := r.db.WithContext(ctx).Model(&models.RecoveryStage{}).Where("checkpoint_id IS NOT NULL AND EXISTS (SELECT 1 FROM transcription_job_executions WHERE transcription_job_executions.id = recovery_stages.execution_id)").Pluck("checkpoint_id", &selected).Error; err != nil {
		return result, err
	}
	protected := map[string]bool{}
	for _, id := range selected {
		protected[id] = true
	}
	var dependencies []models.RecoveryCheckpointDependency
	if err := r.db.WithContext(ctx).Find(&dependencies).Error; err != nil {
		return result, err
	}
	for changed := true; changed; {
		changed = false
		for _, dep := range dependencies {
			if protected[dep.CheckpointID] && !protected[dep.UpstreamCheckpointID] {
				protected[dep.UpstreamCheckpointID] = true
				changed = true
			}
		}
	}
	for id := range protected {
		result.ProtectedBytes += byID[id].SizeBytes
	}
	var active []models.RecoveryAttempt
	if err := r.db.WithContext(ctx).Where("state IN ?", []string{models.RecoveryRunning, models.RecoveryWaiting}).Find(&active).Error; err != nil {
		return result, err
	}
	activeAttempts := map[string]bool{}
	for _, attempt := range active {
		activeAttempts[attempt.ID] = true
	}
	type candidate struct {
		path       string
		age        time.Time
		size       int64
		checkpoint *models.RecoveryCheckpoint
		temporary  bool
	}
	candidates := []candidate{}
	// Artifacts are exactly three levels below root. Do not traverse a symlink
	// or infer a source/model-cache path from user-controlled recording names.
	err = filepath.WalkDir(r.root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return ErrRecoveryCorrupt
		}
		if !entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(r.root, path)
		if err != nil {
			return err
		}
		parts := strings.Split(rel, string(filepath.Separator))
		if len(parts) != 3 {
			return nil
		}
		if !recoverySHA.MatchString(parts[0]) || !recoverySHA.MatchString(parts[1]) {
			return filepath.SkipDir
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if checkpoint, ok := byPath[rel]; ok {
			if !protected[checkpoint.ID] {
				// Quotas count bytes currently on disk, including a damaged file
				// whose size no longer agrees with its immutable manifest.
				size, err := recoveryTreeSize(path)
				if err != nil {
					return err
				}
				copy := checkpoint
				candidates = append(candidates, candidate{path: path, age: checkpoint.CreatedAt, size: size, checkpoint: &copy})
			}
			return filepath.SkipDir
		}
		if strings.HasPrefix(parts[2], ".attempt-") {
			for attemptID := range activeAttempts {
				if strings.HasPrefix(parts[2], ".attempt-"+attemptID+"-") {
					return filepath.SkipDir
				}
			}
		} else {
			var manifest CheckpointManifest
			if data, err := readRegularFile(filepath.Join(path, "manifest.json")); err == nil && json.Unmarshal(data, &manifest) == nil && activeAttempts[manifest.AttemptID] {
				return filepath.SkipDir
			}
		}
		size, err := recoveryTreeSize(path)
		if err != nil {
			return err
		}
		candidates = append(candidates, candidate{path: path, age: info.ModTime(), size: size, temporary: true})
		return filepath.SkipDir
	})
	if err != nil {
		return result, err
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].age.Before(candidates[j].age) })
	for _, candidate := range candidates {
		age := policy.UnreferencedAge
		if candidate.temporary {
			age = policy.TemporaryAge
		}
		old := policy.Now.Sub(candidate.age) >= age
		overQuota := policy.MaxBytes > 0 && usage > policy.MaxBytes
		if !old && !overQuota {
			continue
		}
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if err := r.removeAll(candidate.path); err != nil {
			return result, errors.New("checkpoint collection failed; retry cleanup")
		}
		if candidate.checkpoint != nil {
			err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				if err := tx.Where("checkpoint_id = ?", candidate.checkpoint.ID).Delete(&models.RecoveryCheckpointDependency{}).Error; err != nil {
					return err
				}
				return tx.Delete(candidate.checkpoint).Error
			})
			if err != nil {
				return result, err
			}
		}
		result.RemovedArtifacts++
		result.RemovedBytes += candidate.size
		usage -= candidate.size
	}
	result.RemainingBytes = usage
	if policy.MaxBytes > 0 && usage > policy.MaxBytes {
		return result, ErrRecoveryQuota
	}
	return result, nil
}
