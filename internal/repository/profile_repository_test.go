package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"scriberr/internal/models"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestProfileRepositoryListOrdersByName(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&models.TranscriptionProfile{}, &models.AdaptiveProfileRevision{}))

	createdAt := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	profiles := []models.TranscriptionProfile{
		{ID: "zeta", Name: "zeta", CreatedAt: createdAt.Add(4 * time.Minute)},
		{ID: "beta", Name: "beta", CreatedAt: createdAt.Add(3 * time.Minute)},
		{ID: "alpha-later", Name: "Alpha", CreatedAt: createdAt.Add(2 * time.Minute)},
		{ID: "alpha-earlier", Name: "alpha", CreatedAt: createdAt.Add(time.Minute)},
	}

	for i := range profiles {
		require.NoError(t, db.Create(&profiles[i]).Error)
	}

	repo := NewProfileRepository(db)
	got, count, err := repo.List(context.Background(), 0, 10)
	require.NoError(t, err)
	require.Equal(t, int64(4), count)
	require.Equal(t, []string{"alpha", "Alpha", "beta", "zeta"}, profileNames(got))

	gotPage, count, err := repo.List(context.Background(), 1, 2)
	require.NoError(t, err)
	require.Equal(t, int64(4), count)
	require.Equal(t, []string{"Alpha", "beta"}, profileNames(gotPage))
}

func profileNames(profiles []models.TranscriptionProfile) []string {
	names := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		names = append(names, profile.Name)
	}
	return names
}

func TestProfileCreatePreservesExactParameters(t *testing.T) {
	falseValue, zero, empty := false, 0, ""
	contextText, token, apiKey := "C++ foo.bar engineering review", "test-only-hf-token", "test-only-api-key"
	createdAt := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name       string
		id         string
		createdAt  time.Time
		parameters models.WhisperXParams
	}{
		{
			name: "explicit values", id: "imported-preset", createdAt: createdAt,
			parameters: models.WhisperXParams{
				ModelFamily: "qwen3_asr", Model: "Qwen/Qwen3-ASR-1.7B-hf", Device: "cpu", ComputeType: "float32", BatchSize: 1,
				Verbose: false, Fp16: false, VadOnset: 0, VadOffset: 0, LogprobThreshold: 0,
				NvidiaTimestamps: &falseValue, AudioChunkDuration: &zero,
				TranscriptionContext: &contextText, TranscriptionContextTerms: &empty,
				HfToken: &token, HFTokenSource: "custom", APIKey: &apiKey,
			},
		},
		{
			name:       "nil inheritance",
			parameters: models.WhisperXParams{ModelFamily: "whisper", Model: "large-v3", Device: "cpu", ComputeType: "float32", BatchSize: 1},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
			require.NoError(t, err)
			require.NoError(t, db.AutoMigrate(&models.TranscriptionProfile{}, &models.AdaptiveProfileRevision{}))
			repo := NewProfileRepository(db)
			profile := models.TranscriptionProfile{ID: test.id, Name: test.name, CreatedAt: test.createdAt, Parameters: test.parameters}
			require.NoError(t, repo.Create(context.Background(), &profile))
			require.NotEmpty(t, profile.ID)
			require.False(t, profile.CreatedAt.IsZero())
			require.False(t, profile.UpdatedAt.IsZero())
			if test.id != "" {
				require.Equal(t, test.id, profile.ID)
				require.Equal(t, test.createdAt, profile.CreatedAt)
			}
			require.Equal(t, test.parameters, profile.Parameters)
			stored, err := repo.FindByID(context.Background(), profile.ID)
			require.NoError(t, err)
			require.Equal(t, test.parameters, stored.Parameters)
			require.True(t, profile.CreatedAt.Equal(stored.CreatedAt))
			publicJSON, err := json.Marshal(stored)
			require.NoError(t, err)
			require.NotContains(t, string(publicJSON), token)
			require.NotContains(t, string(publicJSON), apiKey)
		})
	}
}

func TestProfileCreateDefaultReplacementRollsBackOnParameterWriteFailure(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&models.TranscriptionProfile{}, &models.AdaptiveProfileRevision{}))
	repo := NewProfileRepository(db)
	ctx := context.Background()
	previous := models.TranscriptionProfile{Name: "previous", IsDefault: true}
	require.NoError(t, repo.Create(ctx, &previous))
	replacement := models.TranscriptionProfile{Name: "replacement", IsDefault: true}
	require.NoError(t, repo.Create(ctx, &replacement))
	stored, err := repo.FindByID(ctx, previous.ID)
	require.NoError(t, err)
	require.False(t, stored.IsDefault)
	current, err := repo.FindDefault(ctx)
	require.NoError(t, err)
	require.Equal(t, replacement.ID, current.ID)

	// Fail after INSERT and the default-profile hook, precisely when the
	// requested false value replaces GORM's true default in the second write.
	require.NoError(t, db.Exec(`CREATE TRIGGER reject_profile_parameters
		BEFORE UPDATE OF fp16 ON transcription_profiles
		WHEN NEW.name = 'rejected' AND NEW.fp16 = 0
		BEGIN SELECT RAISE(ABORT, 'parameter write rejected'); END`).Error)
	rejected := models.TranscriptionProfile{Name: "rejected", IsDefault: true}
	require.ErrorContains(t, repo.Create(ctx, &rejected), "parameter write rejected")
	require.Empty(t, rejected.ID, "failed creation must not publish an uncommitted ID to its caller")
	current, err = repo.FindDefault(ctx)
	require.NoError(t, err)
	require.Equal(t, replacement.ID, current.ID, "rollback must restore the previous default")
	var count int64
	require.NoError(t, db.Model(&models.TranscriptionProfile{}).Where("name = ?", "rejected").Count(&count).Error)
	require.Zero(t, count, "rollback must remove the partially inserted profile")
}
