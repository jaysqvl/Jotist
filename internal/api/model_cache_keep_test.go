package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jaysqvl/Jotist/internal/database"
	"github.com/jaysqvl/Jotist/internal/models"
	"github.com/jaysqvl/Jotist/internal/modelstorage"
	"github.com/jaysqvl/Jotist/internal/repository"
	"github.com/stretchr/testify/require"
)

func storageKeep(h *Handler, user models.User, entry modelstorage.Entry, keep bool) *httptest.ResponseRecorder {
	body, _ := json.Marshal(UpdateModelKeepDownloadedRequest{KeepDownloaded: &keep, Version: entry.Version})
	c, recorder := hfTokenRequest("PUT", string(body), user.ID)
	c.Params = gin.Params{{Key: "id", Value: entry.ID}}
	h.UpdateModelKeepDownloaded(c)
	return recorder
}

func TestKeptModelBlocksEveryRemovalUntilUnpinned(t *testing.T) {
	for _, mode := range []string{"single", "all", "expired"} {
		t.Run(mode, func(t *testing.T) {
			h, db, user, path := modelStorageTestHandler(t)
			entry := storageInventory(t, h, user).Entries[0]
			ageModelCache(t, db, entry, 30)
			oldPlan, err := h.modelRetention.Preview(context.Background(), "all", false)
			require.NoError(t, err)
			require.Len(t, oldPlan.Models, 1)
			require.Equal(t, http.StatusOK, storageKeep(h, user, entry, true).Code)
			kept := storageInventory(t, h, user).Entries[0]
			require.True(t, kept.KeepDownloaded)
			require.True(t, kept.CanKeepDownloaded)
			require.False(t, kept.CanRemove)
			require.Nil(t, kept.EvictAfter)
			switch mode {
			case "single":
				// Includes a confirmation created before Keep downloaded was enabled.
				require.Equal(t, http.StatusConflict, storageDelete(h, user, entry).Code)
			case "all":
				_, err := h.modelRetention.Collect(context.Background(), "all", oldPlan.Version)
				require.ErrorIs(t, err, modelstorage.ErrChanged)
				plan, err := h.modelRetention.Preview(context.Background(), "all", false)
				require.NoError(t, err)
				require.Empty(t, plan.Models)
				require.Equal(t, 1, plan.SkippedModels)
				result, err := h.modelRetention.Collect(context.Background(), "all", plan.Version)
				require.NoError(t, err)
				require.Zero(t, result.RemovedModels)
			case "expired":
				result, err := h.modelRetention.Collect(context.Background(), "expired", "")
				require.NoError(t, err)
				require.Zero(t, result.RemovedModels)
			}
			require.DirExists(t, path)
			require.Equal(t, http.StatusOK, storageKeep(h, user, kept, false).Code)
			result, err := h.modelRetention.Collect(context.Background(), "expired", "")
			require.NoError(t, err)
			require.Equal(t, 1, result.RemovedModels, "unpinning resumes the existing age-based policy")
			require.NoDirExists(t, path)
		})
	}
}

func TestKeepDownloadedCanBeEnabledDuringActiveUse(t *testing.T) {
	h, _, user, path := modelStorageTestHandler(t)
	entry := storageInventory(t, h, user).Entries[0]
	release := modelstorage.ProcessGate().BeginUse(modelstorage.Use{Resources: []string{storageTestSource}, State: "in_use"})
	defer release()
	require.Equal(t, http.StatusOK, storageKeep(h, user, entry, true).Code)
	kept := storageInventory(t, h, user).Entries[0]
	require.True(t, kept.KeepDownloaded)
	require.Equal(t, "in_use", kept.Status)
	require.DirExists(t, path)
}

func TestKeepDownloadedWaitsForAnAdmittedRemovalAndCannotAcknowledgeMissingFiles(t *testing.T) {
	_, db, _, path := modelStorageTestHandler(t)
	store := repository.NewModelCacheRepository(db)
	entered, proceed := make(chan struct{}), make(chan struct{})
	var block atomic.Bool
	var once, release sync.Once
	defer release.Do(func() { close(proceed) })
	m := modelstorage.New(modelstorage.Options{HubRoots: []string{filepath.Dir(path)}, Gate: modelstorage.NewGate(),
		Models: []modelstorage.Model{{Source: storageTestSource, Name: "Cohere", Resources: []string{storageTestSource}}},
		Details: func(ctx context.Context) (map[string]modelstorage.Details, error) {
			if block.Load() {
				once.Do(func() { close(entered); <-proceed })
			}
			return repository.ReadModelStorageDetails(ctx, db, nil)
		}})
	r := modelstorage.NewRetainer(m, store)
	inv, err := r.Inventory(context.Background(), false)
	require.NoError(t, err)
	entry := inv.Entries[0]
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	removed, pinned := make(chan error, 1), make(chan error, 1)
	block.Store(true)
	go func() { _, err := m.Remove(ctx, entry.ID, entry.Version); removed <- err }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("removal did not enter its guarded section")
	}
	go func() { pinned <- r.SetKeepDownloaded(ctx, entry.ID, entry.Version, true) }()
	select {
	case err := <-pinned:
		t.Fatalf("pin completed while removal still owned the files: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	release.Do(func() { close(proceed) })
	require.NoError(t, <-removed)
	require.ErrorIs(t, <-pinned, modelstorage.ErrNotFound)
	require.NoDirExists(t, path)
	record, err := store.ReadRecord(ctx, entry.ID)
	require.NoError(t, err)
	require.False(t, record.KeepDownloaded)
}

func TestKeepDownloadedValidationAndOwnerSession(t *testing.T) {
	h, db, user, path := modelStorageTestHandler(t)
	previous := database.DB
	database.DB = db
	t.Cleanup(func() { database.DB = previous })
	entry := storageInventory(t, h, user).Entries[0]
	for _, body := range []string{`{}`, `{"keep_downloaded":true}`, `{"keep_downloaded":"yes","version":"` + entry.Version + `"}`} {
		c, recorder := hfTokenRequest("PUT", body, user.ID)
		c.Params = gin.Params{{Key: "id", Value: entry.ID}}
		h.UpdateModelKeepDownloaded(c)
		require.Equal(t, http.StatusBadRequest, recorder.Code)
	}
	stale := entry
	stale.Version = strings.Repeat("0", 64)
	require.Equal(t, http.StatusConflict, storageKeep(h, user, stale, true).Code)
	router := SetupRoutes(h, h.authService)
	body := `{"keep_downloaded":true,"version":"` + entry.Version + `"}`
	for _, token := range []string{"", "not-a-jwt"} {
		req := httptest.NewRequest("PUT", "/api/v1/settings/models/"+entry.ID+"/retention", strings.NewReader(body))
		req.Header.Set("X-API-Key", "any-upload-key")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusUnauthorized, w.Code)
	}
	jwt, err := h.authService.GenerateToken(&user)
	require.NoError(t, err)
	req := httptest.NewRequest("PUT", "/api/v1/settings/models/"+entry.ID+"/retention", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	require.True(t, storageInventory(t, h, user).Entries[0].KeepDownloaded)
	require.DirExists(t, path)
}
