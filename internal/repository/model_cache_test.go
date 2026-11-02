package repository

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/jaysqvl/Jotist/internal/models"
	"github.com/jaysqvl/Jotist/internal/modelstorage"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func cacheRepositoryFixture(t *testing.T) (*ModelCacheRepository, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "cache.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&models.ModelCacheSetting{}, &models.ModelCacheRecord{}))
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	return NewModelCacheRepository(db), db
}

func TestModelCacheDefaultsAndDisabledPolicySurviveReload(t *testing.T) {
	r, db := cacheRepositoryFixture(t)
	ctx := context.Background()
	p, err := r.LoadPolicy(ctx)
	require.NoError(t, err)
	require.True(t, p.Enabled)
	require.Equal(t, 14, p.RetentionDays)
	require.NoError(t, r.SavePolicy(ctx, false, 21))
	require.NoError(t, r.SaveCleanup(ctx, modelstorage.CleanupResult{CheckedAt: time.Now(), RemovedModels: 2, RemovedBytes: 100}))
	p, err = NewModelCacheRepository(db).LoadPolicy(ctx)
	require.NoError(t, err)
	require.False(t, p.Enabled, "a cleanup result must not re-enable a disabled policy")
	require.Equal(t, 21, p.RetentionDays)
	require.Equal(t, 2, p.LastRemovedModels)
	for _, invalid := range []int{-1, 0, 366} {
		require.Error(t, r.SavePolicy(ctx, true, invalid))
	}
	p, err = r.LoadPolicy(ctx)
	require.NoError(t, err)
	require.False(t, p.Enabled)
	require.Equal(t, 21, p.RetentionDays)
}

func TestModelCacheObservationDoesNotExtendRetentionOnReads(t *testing.T) {
	r, db := cacheRepositoryFixture(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	used := now.Add(-time.Hour)
	entry := modelstorage.Entry{ID: "one", Source: "org/model", Version: "v1", LastUsed: &used}
	rows, err := r.Observe(ctx, []modelstorage.Entry{entry}, now)
	require.NoError(t, err)
	require.True(t, rows[entry.ID].CachedSince.Equal(now))
	entry.LastUsed = nil // Deleting a recording does not erase remembered use.
	rows, err = NewModelCacheRepository(db).Observe(ctx, []modelstorage.Entry{entry}, now.Add(7*24*time.Hour))
	require.NoError(t, err)
	require.True(t, rows[entry.ID].CachedSince.Equal(now))
	require.NotNil(t, rows[entry.ID].LastUsed)
	require.True(t, rows[entry.ID].LastUsed.Equal(used))
	entry.Version = "v2" // A re-download gets a complete retention window.
	rows, err = r.Observe(ctx, []modelstorage.Entry{entry}, now.Add(10*24*time.Hour))
	require.NoError(t, err)
	require.True(t, rows[entry.ID].CachedSince.Equal(now.Add(10*24*time.Hour)))
	entry.Status, entry.Version = "removal_pending", "v3"
	rows, err = r.Observe(ctx, []modelstorage.Entry{entry}, now.Add(20*24*time.Hour))
	require.NoError(t, err)
	require.True(t, rows[entry.ID].CachedSince.Equal(now.Add(10*24*time.Hour)), "a failed removal must not reset its retention clock")
	require.NoError(t, db.Model(&models.ModelCacheRecord{}).Where("id = ?", entry.ID).Update("cached_since", time.Time{}).Error)
	rows, err = r.Observe(ctx, []modelstorage.Entry{entry}, now.Add(21*24*time.Hour))
	require.NoError(t, err)
	require.True(t, rows[entry.ID].CachedSince.Equal(now.Add(21*24*time.Hour)), "missing age metadata gets a full retention window")
	entry.Source = "different/model"
	_, err = r.Observe(ctx, []modelstorage.Entry{entry}, now)
	require.ErrorIs(t, err, modelstorage.ErrUnverified)
}

func TestModelKeepDownloadedSurvivesReloadAndChangedWeights(t *testing.T) {
	r, db := cacheRepositoryFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	entry := modelstorage.Entry{ID: "kept", Source: "org/diarizer", Version: "first"}
	_, err := r.Observe(ctx, []modelstorage.Entry{entry}, now)
	require.NoError(t, err)
	require.NoError(t, r.SaveKeepDownloaded(ctx, entry.ID, entry.Source, entry.Version, true))
	r = NewModelCacheRepository(db) // A restart must not lose the owner's preference.
	entry.Version = "replacement"
	used := now.Add(time.Hour)
	entry.LastUsed = &used
	rows, err := r.Observe(ctx, []modelstorage.Entry{entry}, used)
	require.NoError(t, err)
	require.True(t, rows[entry.ID].KeepDownloaded)
	require.Equal(t, entry.Version, rows[entry.ID].CacheVersion)
	require.ErrorIs(t, r.SaveKeepDownloaded(ctx, entry.ID, "different/source", entry.Version, false), modelstorage.ErrChanged)
	require.ErrorIs(t, r.SaveKeepDownloaded(ctx, entry.ID, entry.Source, "stale", false), modelstorage.ErrChanged)
	record, err := r.ReadRecord(ctx, entry.ID)
	require.NoError(t, err)
	require.True(t, record.KeepDownloaded, "stale or mismatched identities cannot clear a pin")
	require.NoError(t, r.SaveKeepDownloaded(ctx, entry.ID, entry.Source, entry.Version, false))
	record, err = NewModelCacheRepository(db).ReadRecord(ctx, entry.ID)
	require.NoError(t, err)
	require.False(t, record.KeepDownloaded, "false must persist rather than being skipped as a zero value")
	require.WithinDuration(t, used, *record.LastUsed, time.Millisecond)
}
