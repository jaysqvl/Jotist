package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"scriberr/internal/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

var (
	ErrRecoveryStaleOwner = errors.New("stage ownership is stale or execution is no longer authorized")
	ErrRecoveryConflict   = errors.New("immutable stage or checkpoint selection conflicts with the request")
	ErrRecoveryCorrupt    = errors.New("checkpoint is missing, corrupt, or incompatible; recomputation is required")
	ErrRecoveryQuota      = errors.New("checkpoint storage quota is full; retained recovery data will not be evicted")
)

type RecoveryOptions struct{ MaxBytes int64 }
type AttemptSettings struct {
	Device           string                `json:"device"`
	Precision        string                `json:"precision"`
	BatchSize        int                   `json:"batch_size"`
	WindowSeconds    float64               `json:"window_seconds"`
	SettingsHash     string                `json:"settings_hash"`
	OverlapSeconds   float64               `json:"overlap_seconds,omitempty"`
	StitchingVersion string                `json:"stitching_version,omitempty"`
	CompatibilityKey string                `json:"-"`
	Provenance       *CheckpointProvenance `json:"-"`
	Reason           string                `json:"reason"` // controlled reason code, not an exception or user prompt
}
type CheckpointInput struct {
	ID           string `json:"id"`
	ResultSHA256 string `json:"result_sha256"`
}
type ModelArtifactIdentity struct {
	ModelID  string `json:"model_id"`
	Revision string `json:"revision"`
	SHA256   string `json:"sha256,omitempty"`
}

// Hash sensitive settings before this boundary. There is intentionally no
// arbitrary metadata, credentials, prompt, URL or request-header field.
type CheckpointProvenance struct {
	AudioSHA256         string                  `json:"audio_sha256"`
	PreparedAudioSHA256 string                  `json:"prepared_audio_sha256"`
	PreprocessingHash   string                  `json:"preprocessing_hash"`
	SettingsHash        string                  `json:"settings_hash"`
	RuntimeFingerprint  string                  `json:"runtime_fingerprint"`
	Implementation      string                  `json:"implementation"`
	Models              []ModelArtifactIdentity `json:"models"`
	Upstream            []CheckpointInput       `json:"upstream,omitempty"`
}
type StageSpec struct {
	RecordingID, ExecutionID, NodeKey, Kind, SchemaVersion, CompatibilityKey string
	OwnerGeneration                                                          int64
	DurationSeconds                                                          float64
	RecoverableBoundary                                                      bool
	Provenance                                                               CheckpointProvenance
}
type CheckpointFile struct {
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
}
type CheckpointManifest struct {
	Version           int                  `json:"version"`
	ID                string               `json:"id"`
	RecordingHash     string               `json:"recording_hash"`
	ExecutionID       string               `json:"execution_id"`
	StageID           string               `json:"stage_id"`
	AttemptID         string               `json:"attempt_id"`
	OwnerGeneration   int64                `json:"owner_generation"`
	Kind              string               `json:"kind"`
	SchemaVersion     string               `json:"schema_version"`
	CompatibilityKey  string               `json:"compatibility_key"`
	DurationSeconds   float64              `json:"duration_seconds"`
	CreatedAt         time.Time            `json:"created_at"`
	Provenance        CheckpointProvenance `json:"provenance"`
	EffectiveSettings AttemptSettings      `json:"effective_settings"`
	Files             []CheckpointFile     `json:"files"`
}

type RecoveryRepository struct {
	db      *gorm.DB
	root    string
	options RecoveryOptions
	mu      *sync.Mutex
	// Fault injection covers crash windows without weakening production APIs.
	afterRename func() error
	removeAll   func(string) error
}

var recoveryRootLocks sync.Map

func NewRecoveryRepository(db *gorm.DB, root string, options RecoveryOptions) (*RecoveryRepository, error) {
	if db == nil || root == "" || options.MaxBytes < 0 {
		return nil, errors.New("invalid checkpoint storage configuration")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absolute, 0700); err != nil {
		return nil, err
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, err
	}
	lock, _ := recoveryRootLocks.LoadOrStore(absolute, &sync.Mutex{})
	return &RecoveryRepository{db: db, root: absolute, options: options, mu: lock.(*sync.Mutex), removeAll: os.RemoveAll}, nil
}

func recoveryHash(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func (r *RecoveryRepository) Database() *gorm.DB { return r.db }

var recoverySHA = regexp.MustCompile(`^[a-f0-9]{64}$`)
var recoveryLabel = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/+\-]{0,199}$`)

func validateStageSpec(spec StageSpec) error {
	if spec.RecordingID == "" || spec.ExecutionID == "" || !recoveryLabel.MatchString(spec.NodeKey) || !recoveryLabel.MatchString(spec.SchemaVersion) || !recoverySHA.MatchString(spec.CompatibilityKey) || spec.OwnerGeneration < 1 || math.IsNaN(spec.DurationSeconds) || math.IsInf(spec.DurationSeconds, 0) || spec.DurationSeconds < 0 {
		return errors.New("invalid stage identity or duration")
	}
	switch spec.Kind {
	case "recognize", "recognition", "align", "alignment", "diarize", "diarization", "combined", "assemble":
	default:
		return errors.New("unsupported checkpoint stage kind")
	}
	for _, hash := range []string{spec.Provenance.AudioSHA256, spec.Provenance.PreparedAudioSHA256, spec.Provenance.PreprocessingHash, spec.Provenance.SettingsHash, spec.Provenance.RuntimeFingerprint} {
		if !recoverySHA.MatchString(hash) {
			return errors.New("checkpoint provenance requires SHA256 identities")
		}
	}
	if !recoveryLabel.MatchString(spec.Provenance.Implementation) {
		return errors.New("invalid stage implementation identity")
	}
	for _, model := range spec.Provenance.Models {
		if !recoveryLabel.MatchString(model.ModelID) || !recoveryLabel.MatchString(model.Revision) || model.SHA256 != "" && !recoverySHA.MatchString(model.SHA256) {
			return errors.New("invalid model artifact identity")
		}
	}
	for _, input := range spec.Provenance.Upstream {
		if _, err := uuid.Parse(input.ID); err != nil || !recoverySHA.MatchString(input.ResultSHA256) {
			return errors.New("invalid upstream checkpoint identity")
		}
	}
	return nil
}

// Updating the exact row (even to the same value) obtains SQLite's write lock
// before reading attempt state, serializing cancellation/reclaim with commit.
func (r *RecoveryRepository) fence(tx *gorm.DB, recordingID, executionID string, generation int64) error {
	result := tx.Model(&models.TranscriptionJobExecution{}).Where("id = ? AND transcription_job_id = ? AND owner_generation = ? AND status = ? AND recovery_state = ? AND cancelled_at IS NULL", executionID, recordingID, generation, models.StatusProcessing, "running").Where("EXISTS (SELECT 1 FROM transcription_jobs WHERE id = ?)", recordingID).Where("NOT EXISTS (SELECT 1 FROM recovery_deletions WHERE recording_id = ?)", recordingID).UpdateColumn("owner_generation", generation)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrRecoveryStaleOwner
	}
	// Compare instants in Go: SQLite text ordering does not normalize stored
	// timezone offsets. This read remains under the transaction's write lock.
	var execution models.TranscriptionJobExecution
	if err := tx.Select("deadline_at").First(&execution, "id = ?", executionID).Error; err != nil {
		return err
	}
	if execution.DeadlineAt != nil && !time.Now().Before(*execution.DeadlineAt) {
		return ErrRecoveryStaleOwner
	}
	return nil
}

func (r *RecoveryRepository) EnsureStage(ctx context.Context, spec StageSpec) (*models.RecoveryStage, error) {
	if err := validateStageSpec(spec); err != nil {
		return nil, err
	}
	provenance, _ := json.Marshal(spec.Provenance)
	var stage models.RecoveryStage
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := r.fence(tx, spec.RecordingID, spec.ExecutionID, spec.OwnerGeneration); err != nil {
			return err
		}
		err := tx.Where("execution_id = ? AND node_key = ?", spec.ExecutionID, spec.NodeKey).First(&stage).Error
		if err == nil {
			if stage.RecordingID != spec.RecordingID || stage.Kind != spec.Kind || stage.SchemaVersion != spec.SchemaVersion || stage.CompatibilityKey != spec.CompatibilityKey || stage.ProvenanceJSON != string(provenance) || stage.DurationSeconds != spec.DurationSeconds || stage.RecoverableBoundary != spec.RecoverableBoundary {
				return ErrRecoveryConflict
			}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		stage = models.RecoveryStage{ID: uuid.NewString(), RecordingID: spec.RecordingID, ExecutionID: spec.ExecutionID, NodeKey: spec.NodeKey, Kind: spec.Kind, SchemaVersion: spec.SchemaVersion, CompatibilityKey: spec.CompatibilityKey, ProvenanceJSON: string(provenance), DurationSeconds: spec.DurationSeconds, RecoverableBoundary: spec.RecoverableBoundary, State: models.RecoveryPending, OwnerGeneration: spec.OwnerGeneration}
		return tx.Create(&stage).Error
	})
	return &stage, err
}

func (r *RecoveryRepository) ClaimStage(ctx context.Context, stageID string, generation int64, settings AttemptSettings) (*models.RecoveryAttempt, error) {
	if math.IsNaN(settings.OverlapSeconds) || math.IsInf(settings.OverlapSeconds, 0) || settings.OverlapSeconds < 0 || (settings.StitchingVersion != "" && !recoveryLabel.MatchString(settings.StitchingVersion)) {
		return nil, errors.New("invalid stage window policy")
	}
	if settings.Device != "cpu" && settings.Device != "cuda" || !recoveryLabel.MatchString(settings.Precision) || settings.BatchSize < 0 || math.IsNaN(settings.WindowSeconds) || math.IsInf(settings.WindowSeconds, 0) || settings.WindowSeconds < 0 || !recoverySHA.MatchString(settings.SettingsHash) || settings.Reason != "" && !recoveryLabel.MatchString(settings.Reason) {
		return nil, errors.New("invalid effective stage settings")
	}
	var attempt models.RecoveryAttempt
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Take the SQLite write lock before any snapshot read. A competing
		// cancellation can otherwise turn a read-to-write upgrade into BUSY.
		if err := tx.Model(&models.RecoveryStage{}).Where("id = ?", stageID).UpdateColumn("state", gorm.Expr("state")).Error; err != nil {
			return err
		}
		var stage models.RecoveryStage
		if err := tx.First(&stage, "id = ?", stageID).Error; err != nil {
			return err
		}
		if err := r.fence(tx, stage.RecordingID, stage.ExecutionID, generation); err != nil {
			return err
		}
		if stage.State == models.RecoveryRunning || stage.State == models.RecoveryWaiting || stage.State == models.RecoverySucceeded || stage.State == models.RecoveryCancelled || stage.CheckpointID != nil {
			return ErrRecoveryConflict
		}
		attempt = models.RecoveryAttempt{ID: uuid.NewString(), StageID: stage.ID, ExecutionID: stage.ExecutionID, OwnerGeneration: generation, AttemptNumber: stage.AttemptNumber + 1, State: models.RecoveryRunning, Device: settings.Device, Precision: settings.Precision, BatchSize: settings.BatchSize, WindowSeconds: settings.WindowSeconds, SettingsHash: settings.SettingsHash, Reason: settings.Reason, StartedAt: time.Now().UTC()}
		attempt.PlanVersion = attempt.AttemptNumber
		attempt.OverlapSeconds = settings.OverlapSeconds
		attempt.StitchingVersion = settings.StitchingVersion
		if settings.Provenance != nil {
			var original CheckpointProvenance
			if json.Unmarshal([]byte(stage.ProvenanceJSON), &original) != nil || !recoverySHA.MatchString(settings.CompatibilityKey) || settings.Provenance.SettingsHash != settings.SettingsHash {
				return ErrRecoveryConflict
			}
			original.SettingsHash = settings.SettingsHash
			encoded, _ := json.Marshal(original)
			provided, _ := json.Marshal(settings.Provenance)
			if string(encoded) != string(provided) {
				return ErrRecoveryConflict
			}
			attempt.ProvenanceJSON = string(provided)
			attempt.CompatibilityKey = settings.CompatibilityKey
		}
		if err := tx.Create(&attempt).Error; err != nil {
			return err
		}
		return tx.Model(&stage).Updates(map[string]interface{}{"state": models.RecoveryRunning, "owner_generation": generation, "attempt_number": attempt.AttemptNumber}).Error
	})
	return &attempt, err
}

func (r *RecoveryRepository) FailAttempt(ctx context.Context, attemptID string, generation int64, state, errorCode string) error {
	if state != models.RecoveryRetryable && state != models.RecoveryBlocked && state != models.RecoveryFailed && state != models.RecoveryCancelled && state != models.RecoveryInterrupted {
		return errors.New("invalid stage failure state")
	}
	if errorCode != "" && !recoveryLabel.MatchString(errorCode) {
		return errors.New("failure must use a safe error code")
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		attempt, stage, err := r.ownedAttempt(tx, attemptID, generation)
		if err != nil {
			return err
		}
		if (attempt.State != models.RecoveryRunning && attempt.State != models.RecoveryWaiting) || (stage.State != models.RecoveryRunning && stage.State != models.RecoveryWaiting) {
			return ErrRecoveryConflict
		}
		now := time.Now().UTC()
		if err := tx.Model(attempt).Updates(map[string]interface{}{"state": state, "error_code": errorCode, "completed_at": now}).Error; err != nil {
			return err
		}
		return tx.Model(stage).Update("state", state).Error
	})
}

func (r *RecoveryRepository) ownedAttempt(tx *gorm.DB, attemptID string, generation int64) (*models.RecoveryAttempt, *models.RecoveryStage, error) {
	if err := tx.Model(&models.RecoveryAttempt{}).Where("id = ?", attemptID).UpdateColumn("state", gorm.Expr("state")).Error; err != nil {
		return nil, nil, err
	}
	var attempt models.RecoveryAttempt
	if err := tx.First(&attempt, "id = ?", attemptID).Error; err != nil {
		return nil, nil, err
	}
	var stage models.RecoveryStage
	if err := tx.First(&stage, "id = ?", attempt.StageID).Error; err != nil {
		return nil, nil, err
	}
	if err := r.fence(tx, stage.RecordingID, stage.ExecutionID, generation); err != nil {
		return nil, nil, err
	}
	if attempt.OwnerGeneration != generation || stage.OwnerGeneration != generation || stage.AttemptNumber != attempt.AttemptNumber {
		return nil, nil, ErrRecoveryStaleOwner
	}
	return &attempt, &stage, nil
}

func (r *RecoveryRepository) InterruptExecutionStages(ctx context.Context, executionID string, generation int64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		if err := tx.Model(&models.RecoveryAttempt{}).Where("execution_id = ? AND owner_generation = ? AND state IN ?", executionID, generation, []string{models.RecoveryRunning, models.RecoveryWaiting}).Updates(map[string]interface{}{"state": models.RecoveryInterrupted, "error_code": "worker_interrupted", "completed_at": now}).Error; err != nil {
			return err
		}
		return tx.Model(&models.RecoveryStage{}).Where("execution_id = ? AND owner_generation = ? AND state IN ?", executionID, generation, []string{models.RecoveryRunning, models.RecoveryWaiting}).Update("state", models.RecoveryInterrupted).Error
	})
}

func (r *RecoveryRepository) ListStages(ctx context.Context, executionID string) ([]models.RecoveryStage, error) {
	var stages []models.RecoveryStage
	err := r.db.WithContext(ctx).Where("execution_id = ?", executionID).Order("created_at,id").Preload("Attempts", func(tx *gorm.DB) *gorm.DB { return tx.Order("attempt_number") }).Find(&stages).Error
	return stages, err
}
