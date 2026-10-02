package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"scriberr/internal/models"
	"scriberr/internal/queue"
	"scriberr/internal/repository"
)

func TestQueueActivityReadOnlyResponseAndUnavailableScheduler(t *testing.T) {
	t.Setenv("QUEUE_WORKERS", "2")
	h, db, user := hfTokenTestHandler(t)
	require.NoError(t, db.AutoMigrate(&models.TranscriptionQueueItem{}, &models.TranscriptionJobExecution{}, &models.RecoveryStage{}, &models.RecoveryAttempt{}))
	h.jobRepo = repository.NewJobRepository(db)
	c, w := hfTokenRequest("GET", "", user.ID)
	h.GetQueueActivity(c)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	h.taskQueue = queue.NewTaskQueue(2, nil, h.jobRepo)
	c, w = hfTokenRequest("GET", "", user.ID)
	h.GetQueueActivity(c)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"workers":2`)
	require.Contains(t, w.Body.String(), `"recordings":[]`)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
}
