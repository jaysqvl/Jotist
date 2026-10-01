package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"scriberr/internal/models"
	"scriberr/internal/repository"
)

func TestRunStatisticsHandlerValidatesPeriodAndReturnsEmptyEvidence(t *testing.T) {
	h, db, user := hfTokenTestHandler(t)
	require.NoError(t, db.AutoMigrate(&models.TranscriptionJobExecution{}, &models.RecoveryStage{}, &models.RecoveryAttempt{}))
	h.jobRepo = repository.NewJobRepository(db)
	for _, value := range []string{"-1", "1", "10000", "invalid"} {
		c, w := hfTokenRequest("GET", "", user.ID)
		c.Request.URL.RawQuery = "days=" + value
		h.GetRunStatistics(c)
		require.Equal(t, http.StatusBadRequest, w.Code)
	}
	c, w := hfTokenRequest("GET", "", user.ID)
	h.GetRunStatistics(c)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"runs":0`)
	require.Contains(t, w.Body.String(), `"median_hour_seconds":null`)
	require.Contains(t, w.Body.String(), `"models":[]`)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
}
