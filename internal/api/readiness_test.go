package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jaysqvl/Jotist/internal/auth"
	"github.com/jaysqvl/Jotist/internal/config"
	"github.com/jaysqvl/Jotist/internal/queue"
	"github.com/jaysqvl/Jotist/internal/repository"
	"github.com/stretchr/testify/require"
)

func TestReadinessChecksDatabaseAndQueueWithoutAuthentication(t *testing.T) {
	h, db, _ := hfTokenTestHandler(t)
	h.config = &config.Config{Environment: "development"}
	h.jobRepo = repository.NewJobRepository(db)
	h.taskQueue = queue.NewTaskQueue(1, nil, h.jobRepo)
	defer h.taskQueue.Stop()
	router := SetupRoutes(h, auth.NewAuthService("isolated-readiness-test"))
	request := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ready", nil))
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
		return w
	}
	w := request()
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.JSONEq(t, `{"status":"not_ready","checks":{"database":"ready","queue":"unavailable"}}`, w.Body.String())
	require.NoError(t, h.taskQueue.Start())
	w = request()
	require.Equal(t, http.StatusOK, w.Code)
	require.JSONEq(t, `{"status":"ready","checks":{"database":"ready","queue":"ready"}}`, w.Body.String())
	connection, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, connection.Close())
	w = request()
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.JSONEq(t, `{"status":"not_ready","checks":{"database":"unavailable","queue":"ready"}}`, w.Body.String())
}

func TestReadinessUnavailableComponentsAndCanceledRequest(t *testing.T) {
	h := &Handler{}
	c, w := hfTokenRequest(http.MethodGet, "", 0)
	h.ReadinessCheck(c)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.JSONEq(t, `{"status":"not_ready","checks":{"database":"unavailable","queue":"unavailable"}}`, w.Body.String())
	h, db, _ := hfTokenTestHandler(t)
	h.jobRepo = repository.NewJobRepository(db)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c, w = hfTokenRequest(http.MethodGet, "", 0)
	c.Request = c.Request.WithContext(ctx)
	h.ReadinessCheck(c)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.JSONEq(t, `{"status":"not_ready","checks":{"database":"unavailable","queue":"unavailable"}}`, w.Body.String())
}

func TestLivenessRemainsAvailableDuringDependencyFailure(t *testing.T) {
	h := &Handler{}
	c, w := hfTokenRequest(http.MethodGet, "", 0)
	h.HealthCheck(c)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"version":`)
}
