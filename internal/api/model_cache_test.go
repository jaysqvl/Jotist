package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jaysqvl/Jotist/internal/models"
	"github.com/jaysqvl/Jotist/internal/modelstorage"
	"github.com/jaysqvl/Jotist/internal/repository"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func ageModelCache(t *testing.T, db *gorm.DB, entry modelstorage.Entry, days int) {
	t.Helper()
	require.NoError(t, db.Model(&models.ModelCacheRecord{}).Where("id = ?", entry.ID).
		Update("cached_since", time.Now().UTC().Add(-time.Duration(days)*24*time.Hour)).Error)
}

func TestModelCacheNewFilesGetFullWindowAndExpiredFilesAreEvicted(t *testing.T) {
	h, db, user, path := modelStorageTestHandler(t)
	inv := storageInventory(t, h, user)
	require.True(t, inv.Retention.Enabled)
	require.Equal(t, 14, inv.Retention.RetentionDays)
	require.NotNil(t, inv.Entries[0].CachedSince)
	require.WithinDuration(t, inv.Entries[0].CachedSince.Add(14*24*time.Hour), *inv.Entries[0].EvictAfter, time.Millisecond)
	result, err := h.modelRetention.Collect(context.Background(), "expired", "")
	require.NoError(t, err)
	require.Zero(t, result.RemovedModels)
	require.DirExists(t, path)
	ageModelCache(t, db, inv.Entries[0], 13)
	result, err = h.modelRetention.Collect(context.Background(), "expired", "")
	require.NoError(t, err)
	require.Zero(t, result.RemovedModels)
	ageModelCache(t, db, inv.Entries[0], 15)
	result, err = h.modelRetention.Collect(context.Background(), "expired", "")
	require.NoError(t, err)
	require.Equal(t, 1, result.RemovedModels)
	require.EqualValues(t, len("model fixture"), result.RemovedBytes)
	require.NoDirExists(t, path)
	policy, err := repository.NewModelCacheRepository(db).LoadPolicy(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, policy.LastRemovedModels)
	require.NotNil(t, policy.LastCheckedAt)
}

func TestModelCacheRetentionUsesCompletionAndRemembersDeletedHistory(t *testing.T) {
	h, db, user, path := modelStorageTestHandler(t)
	job := models.TranscriptionJob{ID: "history", AudioPath: "preserve.wav", Status: models.StatusCompleted}
	require.NoError(t, db.Create(&job).Error)
	finished := time.Now().UTC().Add(-time.Hour)
	e := models.TranscriptionJobExecution{ID: "recent-completion", TranscriptionJobID: job.ID,
		StartedAt: time.Now().Add(-30 * 24 * time.Hour), CompletedAt: &finished, Status: models.StatusCompleted,
		ActualParameters: models.WhisperXParams{ModelFamily: "cohere_transcribe", Model: storageTestSource, NoAlign: true}}
	require.NoError(t, db.Create(&e).Error)
	inv := storageInventory(t, h, user)
	require.WithinDuration(t, finished, *inv.Entries[0].LastUsed, time.Millisecond)
	ageModelCache(t, db, inv.Entries[0], 30)
	require.NoError(t, db.Delete(&e).Error)
	result, err := h.modelRetention.Collect(context.Background(), "expired", "")
	require.NoError(t, err)
	require.Zero(t, result.RemovedModels)
	require.DirExists(t, path)
}

func TestModelCacheAutomaticCleanupProtectsActiveAndOfflineProfiles(t *testing.T) {
	for _, kind := range []string{"active", "download", "cached-only-profile", "cached-only-queue", "disabled"} {
		t.Run(kind, func(t *testing.T) {
			h, db, user, path := modelStorageTestHandler(t)
			inv := storageInventory(t, h, user)
			ageModelCache(t, db, inv.Entries[0], 30)
			params := models.WhisperXParams{ModelFamily: "cohere_transcribe", Model: storageTestSource, NoAlign: true, ModelCacheOnly: true}
			switch kind {
			case "active", "download":
				state := "in_use"
				if kind == "download" {
					state = "downloading"
				}
				release := modelstorage.ProcessGate().BeginUse(modelstorage.Use{Resources: []string{storageTestSource}, State: state})
				defer release()
			case "cached-only-profile":
				require.NoError(t, db.Create(&models.TranscriptionProfile{Name: "Offline", Parameters: params}).Error)
			case "cached-only-queue":
				job := models.TranscriptionJob{ID: "queued", AudioPath: "preserve.wav", Status: models.StatusPending, Parameters: params}
				require.NoError(t, db.Create(&job).Error)
			case "disabled":
				_, err := h.modelRetention.SavePolicy(context.Background(), false, 14)
				require.NoError(t, err)
			}
			result, err := h.modelRetention.Collect(context.Background(), "expired", "")
			require.NoError(t, err)
			require.Zero(t, result.RemovedModels)
			require.DirExists(t, path)
		})
	}
}

func TestModelCacheClearRequiresFreshConfirmationAndPreservesSavedData(t *testing.T) {
	h, db, _, path := modelStorageTestHandler(t)
	params := models.WhisperXParams{ModelFamily: "cohere_transcribe", Model: storageTestSource, NoAlign: true}
	profile := models.TranscriptionProfile{Name: "Keep", Parameters: params}
	job := models.TranscriptionJob{ID: "keep", AudioPath: "keep.wav", Status: models.StatusCompleted, Parameters: params}
	require.NoError(t, db.Create(&profile).Error)
	require.NoError(t, db.Create(&job).Error)
	plan, err := h.modelRetention.Preview(context.Background(), "all", true)
	require.NoError(t, err)
	require.Len(t, plan.Models, 1)
	_, err = h.modelRetention.Collect(context.Background(), "all", strings.Repeat("0", 64))
	require.ErrorIs(t, err, modelstorage.ErrChanged)
	require.DirExists(t, path)
	// A newly created cached-only profile changes the warning that was confirmed.
	require.NoError(t, db.Model(&profile).Update("model_cache_only", true).Error)
	_, err = h.modelRetention.Collect(context.Background(), "all", plan.Version)
	require.ErrorIs(t, err, modelstorage.ErrChanged)
	plan, err = h.modelRetention.Preview(context.Background(), "all", false)
	require.NoError(t, err)
	require.True(t, plan.CachedOnlyProfiles)
	result, err := h.modelRetention.Collect(context.Background(), "all", plan.Version)
	require.NoError(t, err)
	require.Equal(t, 1, result.RemovedModels)
	require.NoDirExists(t, path)
	var kept models.TranscriptionProfile
	require.NoError(t, db.First(&kept, "id = ?", profile.ID).Error)
	require.True(t, kept.Parameters.ModelCacheOnly)
	var keptJob models.TranscriptionJob
	require.NoError(t, db.First(&keptJob, "id = ?", job.ID).Error)
	require.Equal(t, job.AudioPath, keptJob.AudioPath)
}

func TestModelCacheClearKeepsUnregisteredSharedRepository(t *testing.T) {
	h, _, _, path := modelStorageTestHandler(t)
	unknown := filepath.Join(filepath.Dir(path), "models--someone--unregistered")
	require.NoError(t, os.MkdirAll(filepath.Join(unknown, "blobs"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(unknown, "blobs", "weights"), []byte("keep"), 0644))
	plan, err := h.modelRetention.Preview(context.Background(), "all", true)
	require.NoError(t, err)
	require.Len(t, plan.Models, 1)
	require.Equal(t, 1, plan.SkippedModels)
	result, err := h.modelRetention.Collect(context.Background(), "all", plan.Version)
	require.NoError(t, err)
	require.Equal(t, 1, result.RemovedModels)
	require.FileExists(t, filepath.Join(unknown, "blobs", "weights"))
}

func TestModelCacheRechecksRecentUseAndPolicyInsideRemovalBarrier(t *testing.T) {
	for _, change := range []string{"recent-use", "policy-disabled"} {
		t.Run(change, func(t *testing.T) {
			h, db, _, path := modelStorageTestHandler(t)
			store := repository.NewModelCacheRepository(db)
			checks, mutate := 0, false
			manager := modelstorage.New(modelstorage.Options{HubRoots: []string{filepath.Dir(path)}, Gate: modelstorage.NewGate(),
				Models: []modelstorage.Model{{Source: storageTestSource, Name: "Cohere", Resources: []string{storageTestSource}}},
				Details: func(ctx context.Context) (map[string]modelstorage.Details, error) {
					checks++
					details := modelstorage.Details{}
					if mutate && checks >= 2 {
						if change == "recent-use" {
							now := time.Now().UTC()
							details.LastUsed = &now
						} else {
							require.NoError(t, store.SavePolicy(ctx, false, 14))
						}
					}
					return map[string]modelstorage.Details{storageTestSource: details}, nil
				}})
			h.modelRetention = modelstorage.NewRetainer(manager, store)
			inv, err := h.modelRetention.Inventory(context.Background(), false)
			require.NoError(t, err)
			ageModelCache(t, db, inv.Entries[0], 30)
			checks, mutate = 0, true
			result, err := h.modelRetention.Collect(context.Background(), "expired", "")
			require.NoError(t, err)
			require.Zero(t, result.RemovedModels)
			require.Equal(t, 1, result.SkippedModels)
			require.GreaterOrEqual(t, checks, 2)
			require.DirExists(t, path)
		})
	}
}

func TestModelCacheSettingsValidationAndRoutesRequireOwnerSession(t *testing.T) {
	h, _, user, _ := modelStorageTestHandler(t)
	for _, body := range []string{`{}`, `{"enabled":true,"retention_days":0}`, `{"enabled":true,"retention_days":366}`, `{"enabled":true,"retention_days":1.5}`} {
		c, w := hfTokenRequest("PUT", body, user.ID)
		h.UpdateModelRetention(c)
		require.Equal(t, http.StatusBadRequest, w.Code)
	}
	c, w := hfTokenRequest("PUT", `{"enabled":false,"retention_days":21}`, user.ID)
	h.UpdateModelRetention(c)
	require.Equal(t, http.StatusOK, w.Code)
	var policy modelstorage.CacheRetentionPolicy
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &policy))
	require.False(t, policy.Enabled)
	require.Equal(t, 21, policy.RetentionDays)
	router := SetupRoutes(h, h.authService)
	for _, route := range []struct{ method, path string }{{"PUT", "/api/v1/settings/models/retention"}, {"GET", "/api/v1/settings/models/cleanup"}, {"POST", "/api/v1/settings/models/cleanup"}} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(route.method, route.path, nil)
		router.ServeHTTP(recorder, request)
		require.Equal(t, http.StatusUnauthorized, recorder.Code)
	}
}

func TestModelCacheMaintenanceEvictsExpiredFilesAndStops(t *testing.T) {
	h, db, user, path := modelStorageTestHandler(t)
	inv := storageInventory(t, h, user)
	ageModelCache(t, db, inv.Entries[0], 30)
	stop := h.StartModelCacheMaintenance()
	defer stop()
	require.Eventually(t, func() bool {
		_, err := os.Stat(path)
		policy, loadErr := repository.NewModelCacheRepository(db).LoadPolicy(context.Background())
		return os.IsNotExist(err) && loadErr == nil && policy.LastCheckedAt != nil && policy.LastRemovedModels == 1
	}, 5*time.Second, 10*time.Millisecond)
	stop()
	stop() // Server shutdown and deferred cleanup may both stop the owner.
}

func TestModelCacheMaintenanceObservesDownloadEventsWithoutUIRequests(t *testing.T) {
	h, db, user, path := modelStorageTestHandler(t)
	inv := storageInventory(t, h, user)
	store := repository.NewModelCacheRepository(db)
	stop := h.StartModelCacheMaintenance()
	defer stop()
	require.Eventually(t, func() bool {
		policy, err := store.LoadPolicy(context.Background())
		return err == nil && policy.LastCheckedAt != nil
	}, 5*time.Second, 10*time.Millisecond)
	require.NoError(t, os.WriteFile(filepath.Join(path, "blobs", "weights"), []byte("newly downloaded replacement weights"), 0644))
	modelstorage.ProcessGate().FilesChanged(modelstorage.Use{Resources: []string{storageTestSource}})
	require.Eventually(t, func() bool {
		record, err := store.ReadRecord(context.Background(), inv.Entries[0].ID)
		return err == nil && record.CacheVersion != inv.Entries[0].Version
	}, 5*time.Second, 10*time.Millisecond)
	require.DirExists(t, path)
}
