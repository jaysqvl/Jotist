package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"scriberr/internal/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func clearAdmissionSnapshot(params *models.WhisperXParams) {
	if params.AdaptivePolicy == nil {
		return
	}
	data, _ := json.Marshal(params.AdaptivePolicy)
	var copy models.AdaptiveExecutionPolicy
	_ = json.Unmarshal(data, &copy)
	copy.ProfileID, copy.ProfileRevision, copy.LearningGeneration = "", 0, 0
	copy.SnapshotTaken, copy.LearnedPlans = false, nil
	params.AdaptivePolicy = &copy
}

func saveProfileRevision(tx *gorm.DB, profile *models.TranscriptionProfile, reason string, sourceRevision *int64, sourcePlanID *string) error {
	parameters := profile.Parameters.WithoutSecrets()
	clearAdmissionSnapshot(&parameters)
	row := models.AdaptiveProfileRevision{ID: uuid.NewString(), ProfileID: profile.ID, Revision: profile.Revision, LearningGeneration: profile.LearningGeneration, Name: profile.Name, Description: profile.Description, IsDefault: profile.IsDefault, Parameters: parameters, Reason: reason, SourceRevision: sourceRevision, SourcePlanID: sourcePlanID, SavedAt: time.Now().UTC()}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error
}

// Update preserves exact zero/nil settings while advancing the fixed revision
// and learning generation in the same transaction as its immutable snapshot.
func (r *profileRepository) Update(ctx context.Context, profile *models.TranscriptionProfile) error {
	updated := *profile
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		current, err := adaptiveProfileFence(tx, profile.ID, profile.Revision, 0)
		if err != nil {
			return err
		}
		if err := saveProfileRevision(tx, current, "original", nil, nil); err != nil {
			return err
		}
		updated.CreatedAt, updated.Revision, updated.LearningGeneration = current.CreatedAt, current.Revision+1, current.LearningGeneration+1
		updated.Revisions = nil
		clearAdmissionSnapshot(&updated.Parameters)
		if err := tx.Model(&updated).Select("*").Omit("id", "created_at", "Revisions").Updates(&updated).Error; err != nil {
			return err
		}
		if sameAdaptiveFixedParameters(current.Parameters, updated.Parameters) {
			var selections []models.AdaptivePlanSelection
			if err := tx.Where("profile_id = ? AND profile_revision = ? AND learning_generation = ?", current.ID, current.Revision, current.LearningGeneration).Find(&selections).Error; err != nil {
				return err
			}
			for _, selection := range selections {
				selection.ProfileRevision, selection.LearningGeneration = updated.Revision, updated.LearningGeneration
				if err := tx.Create(&selection).Error; err != nil {
					return err
				}
			}
		}
		return saveProfileRevision(tx, &updated, "edit", nil, nil)
	})
	if err == nil {
		*profile = updated
	}
	return err
}

func sameAdaptiveFixedParameters(a, b models.WhisperXParams) bool {
	a, b = a.WithoutSecrets(), b.WithoutSecrets()
	clearAdmissionSnapshot(&a)
	clearAdmissionSnapshot(&b)
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

type AdaptivePolicySnapshot struct {
	ProfileID          string                           `json:"profile_id"`
	ProfileRevision    int64                            `json:"profile_revision"`
	LearningGeneration int64                            `json:"learning_generation"`
	Status             string                           `json:"status"`
	SelectedPlanID     *string                          `json:"selected_plan_id,omitempty"`
	SelectedPlanIDs    []string                         `json:"selected_plan_ids"`
	Plans              []models.AdaptiveLearnedPlan     `json:"plans"`
	Observations       []models.AdaptiveObservation     `json:"observations"`
	Revisions          []models.AdaptiveProfileRevision `json:"revisions"`
}

func (r *AdaptiveLearningRepository) Snapshot(ctx context.Context, profileID, scopeKey string) (*AdaptivePolicySnapshot, error) {
	var profile models.TranscriptionProfile
	if err := r.db.WithContext(ctx).First(&profile, "id = ?", profileID).Error; err != nil {
		return nil, err
	}
	result := &AdaptivePolicySnapshot{ProfileID: profile.ID, ProfileRevision: profile.Revision, LearningGeneration: profile.LearningGeneration, Status: "no_measurements", SelectedPlanIDs: []string{}, Plans: []models.AdaptiveLearnedPlan{}, Observations: []models.AdaptiveObservation{}, Revisions: []models.AdaptiveProfileRevision{}}
	selections := r.db.WithContext(ctx).Where("profile_id = ? AND profile_revision = ? AND learning_generation = ?", profile.ID, profile.Revision, profile.LearningGeneration)
	if scopeKey != "" {
		selections = selections.Where("scope_key = ?", scopeKey)
	}
	if err := selections.Model(&models.AdaptivePlanSelection{}).Order("scope_key").Pluck("plan_id", &result.SelectedPlanIDs).Error; err != nil {
		return nil, err
	}
	if len(result.SelectedPlanIDs) > 0 {
		if err := r.db.WithContext(ctx).Where("id IN ?", result.SelectedPlanIDs).Order("created_at DESC").Find(&result.Plans).Error; err != nil {
			return nil, err
		}
		result.Status = "verified"
		if len(result.SelectedPlanIDs) == 1 {
			result.SelectedPlanID = &result.SelectedPlanIDs[0]
		}
	}
	observations := r.db.WithContext(ctx).Where("profile_id = ?", profile.ID)
	if scopeKey != "" {
		observations = observations.Where("scope_key = ?", scopeKey)
	}
	if err := observations.Order("created_at DESC,id DESC").Limit(100).Find(&result.Observations).Error; err != nil {
		return nil, err
	}
	for _, observation := range result.Observations {
		if result.Status == "no_measurements" && observation.ProfileRevision == profile.Revision && observation.LearningGeneration == profile.LearningGeneration {
			result.Status = "candidate"
		}
	}
	if err := r.db.WithContext(ctx).Where("profile_id = ?", profile.ID).Order("revision DESC").Limit(50).Find(&result.Revisions).Error; err != nil {
		return nil, err
	}
	return result, nil
}

func (r *AdaptiveLearningRepository) Reset(ctx context.Context, profileID string, expectedRevision, expectedGeneration int64) (*models.TranscriptionProfile, error) {
	var result *models.TranscriptionProfile
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		profile, err := adaptiveProfileFence(tx, profileID, expectedRevision, expectedGeneration)
		if err != nil {
			return err
		}
		profile.LearningGeneration++
		if err := tx.Model(profile).UpdateColumn("learning_generation", profile.LearningGeneration).Error; err != nil {
			return err
		}
		result = profile
		return nil
	})
	return result, err
}

func (r *AdaptiveLearningRepository) Freeze(ctx context.Context, profileID string, expectedRevision, expectedGeneration int64, planID string) (*models.TranscriptionProfile, error) {
	var result *models.TranscriptionProfile
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		profile, err := adaptiveProfileFence(tx, profileID, expectedRevision, expectedGeneration)
		if err != nil {
			return err
		}
		var selected models.AdaptivePlanSelection
		if err := tx.First(&selected, "profile_id = ? AND profile_revision = ? AND learning_generation = ? AND plan_id = ?", profile.ID, profile.Revision, profile.LearningGeneration, planID).Error; err != nil {
			return ErrAdaptiveEvidence
		}
		var plan models.AdaptiveLearnedPlan
		if err := tx.First(&plan, "id = ? AND profile_id = ?", planID, profile.ID).Error; err != nil {
			return ErrAdaptiveEvidence
		}
		if len(plan.ObservationIDs) < 3 || !validateAdaptiveSettings(plan.Settings) {
			return ErrAdaptiveEvidence
		}
		// Inherited user defaults can change without editing the profile. Freeze
		// the effective fixed settings that produced this plan, rather than
		// applying its stage values to a newly resolved prompt or vocabulary.
		var evidence models.AdaptiveObservation
		if err := tx.First(&evidence, "id = ? AND profile_id = ? AND profile_revision = ? AND scope_key = ? AND candidate_hash = ?", plan.ObservationIDs[0], plan.ProfileID, plan.ProfileRevision, plan.ScopeKey, plan.CandidateHash).Error; err != nil {
			return ErrAdaptiveEvidence
		}
		var execution models.TranscriptionJobExecution
		if err := tx.First(&execution, "id = ? AND transcription_job_id = ?", evidence.ExecutionID, evidence.RecordingID).Error; err != nil {
			return ErrAdaptiveEvidence
		}
		if err := saveProfileRevision(tx, profile, "original", nil, nil); err != nil {
			return err
		}
		token, apiKey, reuseCheckpoints := profile.Parameters.HfToken, profile.Parameters.APIKey, profile.Parameters.ReuseCheckpoints
		profile.Parameters = execution.ActualParameters.WithoutSecrets()
		// A request's force-fresh override is not part of its measured model settings.
		profile.Parameters.ReuseCheckpoints = reuseCheckpoints
		if profile.Parameters.EffectiveHFTokenSource() == "custom" {
			profile.Parameters.HfToken = token
		}
		profile.Parameters.APIKey = apiKey
		clearAdmissionSnapshot(&profile.Parameters)
		if profile.Parameters.AdaptivePolicy == nil {
			profile.Parameters.AdaptivePolicy = &models.AdaptiveExecutionPolicy{}
		}
		policy := profile.Parameters.AdaptivePolicy
		if policy.Stages == nil {
			policy.Stages = map[string]models.AdaptiveStagePolicy{}
		}
		stage := policy.Stages[plan.Scope.StageKey]
		settings := plan.Settings
		stage.Fixed, stage.DeviceLocked = &settings, true
		stage.AllowCPU, stage.AllowShorterWindows = false, false
		policy.Stages[plan.Scope.StageKey] = stage
		policy.Learn, profile.Parameters.RecoveryMode = false, "fixed"
		profile.Revision++
		profile.LearningGeneration++
		profile.Revisions = nil
		if err := tx.Model(profile).Select("*").Omit("id", "created_at", "Revisions").Updates(profile).Error; err != nil {
			return err
		}
		if err := saveProfileRevision(tx, profile, "freeze", nil, &plan.ID); err != nil {
			return err
		}
		result = profile
		return nil
	})
	return result, err
}

func (r *AdaptiveLearningRepository) Restore(ctx context.Context, profileID string, expectedRevision, expectedGeneration, sourceRevision int64) (*models.TranscriptionProfile, error) {
	var result *models.TranscriptionProfile
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		profile, err := adaptiveProfileFence(tx, profileID, expectedRevision, expectedGeneration)
		if err != nil {
			return err
		}
		var prior models.AdaptiveProfileRevision
		if err := tx.First(&prior, "profile_id = ? AND revision = ?", profileID, sourceRevision).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAdaptiveEvidence
			}
			return err
		}
		if err := saveProfileRevision(tx, profile, "original", nil, nil); err != nil {
			return err
		}
		// Restore private fixed settings but never resurrect a historical secret.
		token, apiKey := profile.Parameters.HfToken, profile.Parameters.APIKey
		profile.Name, profile.Description, profile.Parameters = prior.Name, prior.Description, prior.Parameters
		if profile.Parameters.EffectiveHFTokenSource() == "custom" {
			profile.Parameters.HfToken = token
		}
		profile.Parameters.APIKey = apiKey
		clearAdmissionSnapshot(&profile.Parameters)
		// Current default-profile selection is an independent preference.
		profile.Revision++
		profile.LearningGeneration++
		profile.Revisions = nil
		if err := tx.Model(profile).Select("*").Omit("id", "created_at", "Revisions").Updates(profile).Error; err != nil {
			return err
		}
		if err := saveProfileRevision(tx, profile, "restore", &sourceRevision, nil); err != nil {
			return err
		}
		result = profile
		return nil
	})
	return result, err
}
