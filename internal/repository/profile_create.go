package repository

import (
	"context"

	"scriberr/internal/models"

	"gorm.io/gorm"
)

// Create preserves the supplied preset, including explicit zero values and nil
// inheritance fields. GORM substitutes default-tagged values during INSERT, so
// restore the parameters before committing the profile and default-profile hook.
func (r *profileRepository) Create(ctx context.Context, profile *models.TranscriptionProfile) error {
	created := *profile
	parameters := profile.Parameters
	clearAdmissionSnapshot(&parameters)
	created.Revision, created.LearningGeneration, created.Revisions = 1, 1, nil
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&created).Error; err != nil {
			return err
		}
		created.Parameters = parameters
		if err := tx.Model(&created).Select("*").Omit("id", "created_at", "Revisions").Updates(&created).Error; err != nil {
			return err
		}
		return saveProfileRevision(tx, &created, "create", nil, nil)
	})
	if err == nil {
		*profile = created
	}
	return err
}
