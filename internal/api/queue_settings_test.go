package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/jaysqvl/Jotist/internal/models"
	"github.com/jaysqvl/Jotist/internal/queue"
	"github.com/jaysqvl/Jotist/internal/repository"
)

func TestQueueSettingsPersistGloballyWithoutChangingUserPreferences(t *testing.T) {
	t.Setenv("QUEUE_WORKERS", "")
	h, db, user := hfTokenTestHandler(t)
	require.NoError(t, db.AutoMigrate(&models.QueueSetting{}))
	jobs := repository.NewJobRepository(db)
	settings := repository.NewQueueSettingsRepository(db)
	h.taskQueue = queue.NewTaskQueue(models.DefaultQueueWorkers, nil, jobs)
	h.taskQueue.SetQueueSettingsRepository(settings)
	defer h.taskQueue.Stop()
	c, w := hfTokenRequest("GET", "", user.ID)
	h.GetQueueSettings(c)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"workers":1`)
	for _, body := range []string{`{}`, `{"workers":null}`, `{"workers":0}`, `{"workers":-1}`, `{"workers":17}`, `{"workers":1.5}`, `{"workers":"2"}`} {
		c, w = hfTokenRequest("PUT", body, user.ID)
		h.UpdateQueueSettings(c)
		require.Equal(t, http.StatusBadRequest, w.Code, body)
		require.Equal(t, 1, h.taskQueue.QueueSettings().Workers)
	}
	c, w = hfTokenRequest("PUT", `{"workers":2}`, user.ID)
	h.UpdateQueueSettings(c)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var response models.QueueSettingsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.Equal(t, 2, response.Workers)
	stored, err := settings.LoadWorkers(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, stored)
	var unchanged models.User
	require.NoError(t, db.First(&unchanged, user.ID).Error)
	require.Equal(t, user.HFToken, unchanged.HFToken)
	require.Equal(t, user.ExecutionPolicy, unchanged.ExecutionPolicy)
	// Reconstructing the persistence adapter still sees the server-wide value.
	stored, err = repository.NewQueueSettingsRepository(db).LoadWorkers(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, stored)
}

func TestQueueSettingsDeploymentOverrideRejectsUIWrites(t *testing.T) {
	t.Setenv("QUEUE_WORKERS", "2")
	h, db, user := hfTokenTestHandler(t)
	require.NoError(t, db.AutoMigrate(&models.QueueSetting{}))
	h.taskQueue = queue.NewTaskQueue(1, nil, nil)
	h.taskQueue.SetQueueSettingsRepository(repository.NewQueueSettingsRepository(db))
	defer h.taskQueue.Stop()
	c, w := hfTokenRequest("PUT", `{"workers":1}`, user.ID)
	h.UpdateQueueSettings(c)
	require.Equal(t, http.StatusConflict, w.Code)
	var count int64
	require.NoError(t, db.Model(&models.QueueSetting{}).Count(&count).Error)
	require.Zero(t, count)
	require.Equal(t, 2, h.taskQueue.QueueSettings().Workers)
}
