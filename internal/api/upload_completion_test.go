package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"scriberr/internal/config"
	"scriberr/internal/database"
	"scriberr/internal/models"
	"scriberr/internal/queue"
	"scriberr/internal/repository"
	"scriberr/internal/service"
	"scriberr/internal/transcription"
	"scriberr/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func uploadCompletionFixture(t *testing.T, kind models.UploadSessionKind) (*Handler, *gorm.DB, models.UploadSession, string) {
	t.Helper()
	logger.Init("error")
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	previousDB := database.DB
	database.DB = db
	t.Cleanup(func() { database.DB = previousDB; _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&models.UploadSession{}, &models.UploadSessionFile{}, &models.TranscriptionJob{}, &models.MultiTrackFile{}, &models.TranscriptionQueueItem{}))
	cfg := &config.Config{UploadDir: t.TempDir(), TempDir: t.TempDir(), MaxUploadBytes: 1024, MaxConcurrentMedia: 1}
	repo := repository.NewJobRepository(db)
	tasks := queue.NewTaskQueue(1, nil, repo)
	tasks.SetTranscriptionQueueRepository(repository.NewTranscriptionQueueRepository(db))
	t.Cleanup(tasks.Stop)
	h := &Handler{config: cfg, fileService: service.NewFileService(), jobRepo: repo, taskQueue: tasks, resourceAdmission: newResourceAdmission(cfg)}
	if kind == models.UploadKindQuick {
		h.quickTranscription, err = transcription.NewQuickTranscriptionService(cfg, nil, nil)
		require.NoError(t, err)
		t.Cleanup(h.quickTranscription.Close)
	}
	token, hash, err := newUploadToken()
	require.NoError(t, err)
	session := models.UploadSession{Kind: kind, Status: models.UploadSessionActive, TokenHash: hash, ChunkSize: 1024, ExpiresAt: time.Now().Add(time.Hour)}
	require.NoError(t, db.Create(&session).Error)
	files := []models.UploadSessionFile{{ID: "audio", Role: models.UploadFileRoleAudio, OriginalName: "audio.mp3"}}
	if kind == models.UploadKindMultiTrack {
		files = []models.UploadSessionFile{{ID: "aup", Role: models.UploadFileRoleAup, OriginalName: "project.aup"}, {ID: "track", Role: models.UploadFileRoleTrack, OriginalName: "track.wav"}}
	}
	for _, file := range files {
		data := []byte("audio fixture")
		if file.Role == models.UploadFileRoleAup {
			data = []byte(`<project rate="16000"></project>`)
		}
		file.UploadSessionID = session.ID
		file.Size, file.ReceivedBytes = int64(len(data)), int64(len(data))
		file.ChunkCount, file.ReceivedChunks = 1, "[0]"
		require.NoError(t, db.Create(&file).Error)
		path := h.uploadChunkPath(session.ID, file.ID, 0)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
		require.NoError(t, os.WriteFile(path, data, 0600))
		session.Files = append(session.Files, file)
	}
	return h, db, session, token
}

func completeFixtureUpload(h *Handler, sessionID, token string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/uploads/"+sessionID+"/complete", nil)
	c.Request.Header.Set("X-Upload-Token", token)
	c.Params = gin.Params{{Key: "id", Value: sessionID}}
	h.CompleteUploadSession(c)
	return w
}

func rejectUploadCommit(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Exec(`CREATE TRIGGER reject_upload_completion BEFORE UPDATE OF status ON upload_sessions
		WHEN NEW.status = 'completed' BEGIN SELECT RAISE(ABORT, 'injected upload commit failure'); END`).Error)
}

func TestUploadCompletionCommitFailureRollsBackResultAndRetryIsStable(t *testing.T) {
	for _, kind := range []models.UploadSessionKind{models.UploadKindAudio, models.UploadKindSubmit, models.UploadKindMultiTrack} {
		t.Run(string(kind), func(t *testing.T) {
			h, db, session, token := uploadCompletionFixture(t, kind)
			rejectUploadCommit(t, db)
			w := completeFixtureUpload(h, session.ID, token)
			require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
			for _, model := range []any{&models.TranscriptionJob{}, &models.MultiTrackFile{}} {
				var count int64
				require.NoError(t, db.Model(model).Count(&count).Error)
				require.Zero(t, count)
			}
			require.Equal(t, 0, h.taskQueue.GetQueueStats()["queue_size"])
			for _, file := range session.Files {
				require.FileExists(t, h.uploadChunkPath(session.ID, file.ID, 0))
			}
			entries, err := os.ReadDir(h.config.UploadDir)
			require.NoError(t, err)
			require.Empty(t, entries, "failed commit must clean prepared destination media")
			saved, err := h.loadUploadSession(session.ID)
			require.NoError(t, err)
			require.Equal(t, models.UploadSessionActive, saved.Status)
			require.Nil(t, saved.ResultID)

			require.NoError(t, db.Exec("DROP TRIGGER reject_upload_completion").Error)
			w = completeFixtureUpload(h, session.ID, token)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			var first models.TranscriptionJob
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &first))
			require.NotEmpty(t, first.ID)
			w = completeFixtureUpload(h, session.ID, token)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			var retry models.TranscriptionJob
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &retry))
			require.Equal(t, first.ID, retry.ID)
			var count int64
			require.NoError(t, db.Model(&models.TranscriptionJob{}).Count(&count).Error)
			require.Equal(t, int64(1), count)
			saved, err = h.loadUploadSession(session.ID)
			require.NoError(t, err)
			require.Equal(t, models.UploadSessionCompleted, saved.Status)
			require.Equal(t, first.ID, *saved.ResultID)
			require.NoDirExists(t, h.uploadSessionRoot(session.ID))
			require.Equal(t, http.StatusUnauthorized, completeFixtureUpload(h, session.ID, "wrong-token").Code)
		})
	}
}

func TestUploadCompletionAcceptsBoundPendingJobWhenDispatchIsFull(t *testing.T) {
	h, db, session, token := uploadCompletionFixture(t, models.UploadKindSubmit)
	capacity := h.taskQueue.GetQueueStats()["queue_capacity"].(int)
	for index := 0; index < capacity; index++ {
		require.NoError(t, h.taskQueue.EnqueueJob(fmt.Sprintf("occupied-%d", index)))
	}
	w := completeFixtureUpload(h, session.ID, token)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var first models.TranscriptionJob
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &first))
	require.Equal(t, models.StatusPending, first.Status)
	w = completeFixtureUpload(h, session.ID, token)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var retry models.TranscriptionJob
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &retry))
	require.Equal(t, first.ID, retry.ID)
	var count int64
	require.NoError(t, db.Model(&models.TranscriptionJob{}).Count(&count).Error)
	require.Equal(t, int64(1), count)
	saved, err := h.loadUploadSession(session.ID)
	require.NoError(t, err)
	require.Equal(t, models.UploadSessionCompleted, saved.Status)
	require.Equal(t, first.ID, *saved.ResultID)
}

func TestQuickUploadCommitFailureDoesNotStartWorkOrConsumeCapacity(t *testing.T) {
	h, db, session, token := uploadCompletionFixture(t, models.UploadKindQuick)
	rejectUploadCommit(t, db)
	for attempt := 0; attempt < 2; attempt++ {
		w := completeFixtureUpload(h, session.ID, token)
		require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), "Failed to complete upload session", "a retry must reach commit rather than fail capacity admission")
		entries, err := os.ReadDir(filepath.Join(h.config.UploadDir, "quick_transcriptions"))
		require.NoError(t, err)
		require.Empty(t, entries)
		saved, err := h.loadUploadSession(session.ID)
		require.NoError(t, err)
		require.Equal(t, models.UploadSessionActive, saved.Status)
		require.Nil(t, saved.ResultID)
		require.FileExists(t, h.uploadChunkPath(session.ID, "audio", 0))
	}
	// This fixture deliberately has no processor. Starting any worker before
	// commit would panic; the failed handoff must never launch one.
}

func TestQuickUploadCompletionRetriesTheCommittedResult(t *testing.T) {
	h, db, session, token := uploadCompletionFixture(t, models.UploadKindQuick)
	h.quickTranscription.Close()
	// Fail initialization before any model setup so this exercises the actual
	// quick worker handoff without running inference or downloading assets.
	blockedDirectory := filepath.Join(t.TempDir(), "file-not-directory")
	require.NoError(t, os.WriteFile(blockedDirectory, []byte("fixture"), 0600))
	processor := transcription.NewUnifiedJobProcessor(nil, blockedDirectory, t.TempDir())
	quick, err := transcription.NewQuickTranscriptionService(h.config, processor, nil)
	require.NoError(t, err)
	t.Cleanup(quick.Close)
	h.quickTranscription = quick

	w := completeFixtureUpload(h, session.ID, token)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var first transcription.QuickTranscriptionJob
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &first))
	require.NotEmpty(t, first.ID)
	saved, err := h.loadUploadSession(session.ID)
	require.NoError(t, err)
	require.Equal(t, models.UploadSessionCompleted, saved.Status)
	require.Equal(t, first.ID, *saved.ResultID)
	require.Equal(t, "quick", *saved.ResultType)
	require.Eventually(t, func() bool {
		job, err := quick.GetQuickJob(first.ID)
		return err == nil && job.Status == models.StatusFailed
	}, time.Second, time.Millisecond)
	w = completeFixtureUpload(h, session.ID, token)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var retry transcription.QuickTranscriptionJob
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &retry))
	require.Equal(t, first.ID, retry.ID)
	var count int64
	require.NoError(t, db.Model(&models.TranscriptionJob{}).Count(&count).Error)
	require.Zero(t, count, "initialization failure must occur before temporary processing DB entry")
	entries, err := os.ReadDir(filepath.Join(h.config.UploadDir, "quick_transcriptions"))
	require.NoError(t, err)
	require.Len(t, entries, 1, "retry must not create another quick job's audio")
}
