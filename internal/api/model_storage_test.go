package api

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jaysqvl/Jotist/internal/auth"
	"github.com/jaysqvl/Jotist/internal/config"
	"github.com/jaysqvl/Jotist/internal/database"
	"github.com/jaysqvl/Jotist/internal/models"
	"github.com/jaysqvl/Jotist/internal/modelstorage"
	"github.com/jaysqvl/Jotist/internal/repository"
	"github.com/jaysqvl/Jotist/internal/sse"
	"github.com/jaysqvl/Jotist/internal/transcription/adapters"
	"github.com/jaysqvl/Jotist/pkg/logger"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const storageTestSource = "CohereLabs/cohere-transcribe-03-2026"

func modelStorageTestHandler(t *testing.T) (*Handler, *gorm.DB, models.User, string) {
	t.Helper()
	h, db, user := hfTokenTestHandler(t)
	require.NoError(t, db.AutoMigrate(&models.TranscriptionJobExecution{}, &models.RevokedAccessToken{}))
	root := t.TempDir()
	for key, value := range map[string]string{"XDG_CACHE_HOME": filepath.Join(root, "cache"), "HF_HOME": filepath.Join(root, "hf"), "HF_HUB_CACHE": filepath.Join(root, "hf", "hub"), "TORCH_HOME": filepath.Join(root, "torch"), "UV_CACHE_DIR": filepath.Join(root, "uv"), "NEMO_CACHE_DIR": filepath.Join(root, "nemo"), "PYANNOTE_CACHE": filepath.Join(root, "pyannote"), "HF_XET_CACHE": filepath.Join(root, "xet"), "HF_ASSETS_CACHE": filepath.Join(root, "assets")} {
		t.Setenv(key, value)
	}
	h.config = &config.Config{WhisperXEnv: filepath.Join(root, "env"), Environment: "development"}
	h.modelStorage = newModelStorageManager(h.config, db)
	h.authService = auth.NewAuthService("isolated-storage-test")
	repo := filepath.Join(root, "hf", "hub", "models--CohereLabs--cohere-transcribe-03-2026")
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "snapshots", "revision"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "blobs"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "blobs", "weights"), []byte("model fixture"), 0644))
	require.NoError(t, os.Symlink("../../blobs/weights", filepath.Join(repo, "snapshots", "revision", "model.safetensors")))
	return h, db, user, repo
}

func storageInventory(t *testing.T, h *Handler, user models.User) modelstorage.Inventory {
	t.Helper()
	c, w := hfTokenRequest("GET", "", user.ID)
	h.GetModelStorage(c)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var inventory modelstorage.Inventory
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &inventory))
	return inventory
}

func storageDelete(h *Handler, user models.User, entry modelstorage.Entry) *httptest.ResponseRecorder {
	ctx, w := hfTokenRequest("DELETE", `{"version":"`+entry.Version+`"}`, user.ID)
	ctx.Params = gin.Params{{Key: "id", Value: entry.ID}}
	h.RemoveModelFiles(ctx)
	return w
}

func TestModelRemovalPreservesProfilesCredentialsRecordingsAndExecutionHistory(t *testing.T) {
	h, db, user, repo := modelStorageTestHandler(t)
	secret, prompt, text := "hf_never_return_this", "Private context", "Private completed transcript"
	params := models.WhisperXParams{ModelFamily: "cohere_transcribe", Model: storageTestSource, Device: "cuda", NoAlign: true, HfToken: &secret, TranscriptionContext: &prompt, ModelCacheOnly: true}
	profile := models.TranscriptionProfile{Name: "Saved production profile", Parameters: params, IsDefault: true}
	require.NoError(t, db.Create(&profile).Error)
	executionID := "preserved-execution"
	job := models.TranscriptionJob{ID: "recording", AudioPath: "audio.wav", Status: models.StatusCompleted, Parameters: params, Transcript: &text, PinnedExecutionID: &executionID}
	require.NoError(t, db.Create(&job).Error)
	execution := models.TranscriptionJobExecution{ID: executionID, TranscriptionJobID: job.ID, StartedAt: time.Now().Add(-time.Hour), Status: models.StatusCompleted, ActualParameters: params, Transcript: &text}
	require.NoError(t, db.Create(&execution).Error)
	var beforeProfile models.TranscriptionProfile
	var beforeJob models.TranscriptionJob
	var beforeExecution models.TranscriptionJobExecution
	var beforeUser models.User
	require.NoError(t, db.First(&beforeProfile, "id = ?", profile.ID).Error)
	require.NoError(t, db.First(&beforeJob, "id = ?", job.ID).Error)
	require.NoError(t, db.First(&beforeExecution, "id = ?", executionID).Error)
	require.NoError(t, db.First(&beforeUser, user.ID).Error)
	inv := storageInventory(t, h, user)
	require.Len(t, inv.Entries, 1)
	entry := inv.Entries[0]
	require.Equal(t, 1, entry.Profiles)
	require.Equal(t, 1, entry.CachedOnlyProfiles)
	require.NotNil(t, entry.LastUsed)
	encoded, err := json.Marshal(inv)
	require.NoError(t, err)
	for _, private := range []string{secret, prompt, text, repo} {
		require.NotContains(t, string(encoded), private)
	}
	w := storageDelete(h, user, entry)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	_, err = os.Stat(repo)
	require.ErrorIs(t, err, os.ErrNotExist)
	var afterProfile models.TranscriptionProfile
	var afterJob models.TranscriptionJob
	var afterExecution models.TranscriptionJobExecution
	var afterUser models.User
	require.NoError(t, db.First(&afterProfile, "id = ?", profile.ID).Error)
	require.NoError(t, db.First(&afterJob, "id = ?", job.ID).Error)
	require.NoError(t, db.First(&afterExecution, "id = ?", executionID).Error)
	require.NoError(t, db.First(&afterUser, user.ID).Error)
	require.Equal(t, beforeProfile, afterProfile)
	require.Equal(t, beforeJob, afterJob)
	require.Equal(t, beforeExecution, afterExecution)
	require.Equal(t, beforeUser, afterUser)
	require.Empty(t, storageInventory(t, h, user).Entries)
}

func TestQueuedModelRemovalLeavesImmutableWaitingRequestUnchanged(t *testing.T) {
	for _, status := range []models.TranscriptionQueueStatus{models.QueueStatusQueued, models.QueueStatusPending} {
		t.Run(string(status), func(t *testing.T) {
			h, db, user, repo := modelStorageTestHandler(t)
			entry := storageInventory(t, h, user).Entries[0]
			job := models.TranscriptionJob{ID: "waiting-recording", AudioPath: "audio.wav", Status: models.StatusPending}
			require.NoError(t, db.Create(&job).Error)
			queued := models.TranscriptionQueueItem{TranscriptionJobID: job.ID, Status: status, Parameters: models.WhisperXParams{ModelFamily: "cohere_transcribe", Model: storageTestSource, NoAlign: true, ModelCacheOnly: true}}
			require.NoError(t, db.Create(&queued).Error)
			var before models.TranscriptionQueueItem
			require.NoError(t, db.First(&before, "id = ?", queued.ID).Error)
			inv := storageInventory(t, h, user)
			require.True(t, inv.Entries[0].CanRemove)
			require.Equal(t, "queued", inv.Entries[0].Status)
			require.True(t, inv.Entries[0].Uses[0].CachedOnly)
			w := storageDelete(h, user, entry)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			_, err := os.Stat(repo)
			require.ErrorIs(t, err, os.ErrNotExist)
			var after models.TranscriptionQueueItem
			require.NoError(t, db.First(&after, "id = ?", queued.ID).Error)
			require.Equal(t, before, after)
			require.Empty(t, storageInventory(t, h, user).Entries)
		})
	}
}

func TestQueuedRunBecomingActiveAfterConfirmationBlocksRemoval(t *testing.T) {
	h, db, user, repo := modelStorageTestHandler(t)
	job := models.TranscriptionJob{ID: "starting-recording", AudioPath: "audio.wav", Status: models.StatusPending}
	require.NoError(t, db.Create(&job).Error)
	queued := models.TranscriptionQueueItem{TranscriptionJobID: job.ID, Status: models.QueueStatusPending, Parameters: models.WhisperXParams{ModelFamily: "cohere_transcribe", Model: storageTestSource, NoAlign: true}}
	require.NoError(t, db.Create(&queued).Error)
	entry := storageInventory(t, h, user).Entries[0]
	require.True(t, entry.CanRemove)
	require.NoError(t, db.Model(&queued).Update("status", models.QueueStatusProcessing).Error)
	w := storageDelete(h, user, entry)
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	_, err := os.Stat(repo)
	require.NoError(t, err)
	inv := storageInventory(t, h, user)
	require.False(t, inv.Entries[0].CanRemove)
	require.Equal(t, "in_use", inv.Entries[0].Status)
}

func TestPendingExecutionOnlyBlocksRemovalAfterItStarts(t *testing.T) {
	h, db, user, repo := modelStorageTestHandler(t)
	job := models.TranscriptionJob{ID: "waiting-resume", AudioPath: "audio.wav", Status: models.StatusPending, Parameters: models.WhisperXParams{Model: "other/model"}}
	require.NoError(t, db.Create(&job).Error)
	params := models.WhisperXParams{Model: storageTestSource, NoAlign: true, ModelCacheOnly: true}
	execution := models.TranscriptionJobExecution{TranscriptionJobID: job.ID, Status: models.StatusPending, ActualParameters: params}
	require.NoError(t, db.Create(&execution).Error)
	entry := storageInventory(t, h, user).Entries[0]
	require.True(t, entry.CanRemove)
	require.Equal(t, "queued", entry.Status)
	require.True(t, entry.Uses[0].CachedOnly)
	require.NoError(t, db.Model(&execution).Update("status", models.StatusProcessing).Error)
	w := storageDelete(h, user, entry)
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	_, err := os.Stat(repo)
	require.NoError(t, err)
}

func TestModelStorageReferencesSelectedWhisperSizeAndSpeakerCheckpoint(t *testing.T) {
	_, db, _, _ := modelStorageTestHandler(t)
	params := models.WhisperXParams{ModelFamily: "whisper", Model: "small", NoAlign: true, Diarize: true, DiarizeModel: "pyannote", DiarizationCheckpoint: "pyannote/speaker-diarization-community-1"}
	require.NoError(t, db.Create(&models.TranscriptionProfile{Name: "Whisper small and Community-1", Parameters: params}).Error)
	require.NoError(t, db.Create(&models.TranscriptionJob{ID: "selection-history", AudioPath: "a.wav", Status: models.StatusCompleted}).Error)
	require.NoError(t, db.Create(&models.TranscriptionJobExecution{TranscriptionJobID: "selection-history", StartedAt: time.Now(), Status: models.StatusCompleted, ActualParameters: params}).Error)
	details, err := repository.ReadModelStorageDetails(context.Background(), db, adapters.ModelStorageCatalog())
	require.NoError(t, err)
	for _, selected := range []string{"Systran/faster-whisper-small", "pyannote/speaker-diarization-community-1"} {
		require.Equal(t, 1, details[selected].Profiles, selected)
		require.NotNil(t, details[selected].LastUsed, selected)
	}
	for _, other := range []string{"Systran/faster-whisper-tiny", "Systran/faster-whisper-large-v3", "pyannote/speaker-diarization-3.1", "BUT-FIT/diarizen-wavlm-large-s80-md-v2", "rewayai/suplime-large"} {
		require.Zero(t, details[other].Profiles, other)
		require.Nil(t, details[other].LastUsed, other)
	}
}

func TestResumedExecutionReservesItsActualModelAndSpeakerDependencies(t *testing.T) {
	h, db, user, _ := modelStorageTestHandler(t)
	job := models.TranscriptionJob{ID: "resumed", AudioPath: "a.wav", Status: models.StatusProcessing, Parameters: models.WhisperXParams{Model: "other/model"}}
	require.NoError(t, db.Create(&job).Error)
	params := models.WhisperXParams{Model: storageTestSource, NoAlign: false, Diarize: true, DiarizeModel: "diarizen"}
	execution := models.TranscriptionJobExecution{TranscriptionJobID: job.ID, StartedAt: time.Now(), Status: models.StatusPending, ActualParameters: params}
	require.NoError(t, db.Create(&execution).Error)
	uses, err := repository.ReadModelStorageUses(context.Background(), db)
	require.NoError(t, err)
	for _, resource := range []string{storageTestSource, "@alignment", "@speakers"} {
		matched := false
		for _, use := range uses {
			for _, key := range use.Resources {
				if key == resource {
					matched = true
				}
			}
		}
		require.True(t, matched, resource)
	}
	require.False(t, storageInventory(t, h, user).Entries[0].CanRemove)
}

func TestModelStorageRoutesRequireOwnerJWTAndRejectAPIKeys(t *testing.T) {
	h, db, user, _ := modelStorageTestHandler(t)
	previous := database.DB
	database.DB = db
	t.Cleanup(func() { database.DB = previous })
	router := SetupRoutes(h, h.authService)
	jwt, err := h.authService.GenerateToken(&user)
	require.NoError(t, err)
	for _, endpoint := range []string{"GET", "DELETE", "EVENTS"} {
		method := endpoint
		path := "/api/v1/settings/models"
		body := ""
		if endpoint == "EVENTS" {
			method = "GET"
			path += "/events"
		}
		if method == "DELETE" {
			entry := storageInventory(t, h, user).Entries[0]
			path += "/" + entry.ID
			body = `{"version":"` + entry.Version + `"}`
		}
		for _, token := range []string{"", "not-a-jwt"} {
			req := httptest.NewRequest(method, path, strings.NewReader(body))
			req.Header.Set("X-API-Key", "any-upload-key")
			if token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.Equal(t, http.StatusUnauthorized, w.Code)
		}
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if endpoint == "EVENTS" {
			ctx, cancel := context.WithCancel(req.Context())
			cancel()
			req = req.WithContext(ctx)
		}
		req.Header.Set("Authorization", "Bearer "+jwt)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	}
}

func TestModelStorageEventsDeliverInvalidationsWithoutPrivatePayloads(t *testing.T) {
	h, db, user, _ := modelStorageTestHandler(t)
	// Production initializes logging before starting event goroutines.
	logger.Get()
	h.broadcaster = sse.NewBroadcaster()
	defer func() {
		select {
		case <-h.broadcaster.Done():
		default:
			h.broadcaster.Shutdown()
		}
	}()
	previous := database.DB
	database.DB = db
	t.Cleanup(func() { database.DB = previous })
	server := httptest.NewServer(SetupRoutes(h, h.authService))
	defer server.Close()
	token, err := h.authService.GenerateToken(&user)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/v1/settings/models/events", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	response, err := server.Client().Do(req)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.Equal(t, "text/event-stream", response.Header.Get("Content-Type"))
	require.Equal(t, "no", response.Header.Get("X-Accel-Buffering"))
	reader := bufio.NewReader(response.Body)
	line, err := reader.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "data: {\"type\":\"connected\"}\n", line)
	_, err = reader.ReadString('\n')
	require.NoError(t, err)
	release := modelstorage.ProcessGate().BeginUse(modelstorage.Use{Resources: []string{"private/model"}, State: "in_use", RecordingID: "private-recording", RecordingTitle: "private-title"})
	defer release()
	line, err = reader.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "data: {\"type\":\"model_storage_changed\"}\n", line)
	_, err = reader.ReadString('\n')
	require.NoError(t, err)
	h.broadcaster.Shutdown()
	_, err = reader.ReadString('\n')
	require.ErrorIs(t, err, io.EOF, "storage streams must close with the server, without waiting for keepalive or request timeout")
}

func TestModelStorageDoesNotExposePathsOnFilesystemErrors(t *testing.T) {
	h, _, user, repo := modelStorageTestHandler(t)
	entry := storageInventory(t, h, user).Entries[0]
	require.NoError(t, os.WriteFile(filepath.Join(repo, "unowned"), []byte("never delete"), 0644))
	w := storageDelete(h, user, entry)
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	require.NotContains(t, w.Body.String(), repo)
	_, err := os.Stat(filepath.Join(repo, "unowned"))
	require.NoError(t, err)
}
