package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/jaysqvl/Jotist/internal/models"
	"github.com/jaysqvl/Jotist/internal/repository"
)

func TestRunHistoryValidatesPaginationAndFilters(t *testing.T) {
	h, db, user := hfTokenTestHandler(t)
	require.NoError(t, db.AutoMigrate(&models.TranscriptionJobExecution{}, &models.TranscriptionQueueItem{}))
	h.jobRepo = repository.NewJobRepository(db)
	for _, query := range []string{"days=1", "page=0", "page=1000001", "limit=51", "limit=-1", "status=unrecognised", "page=invalid"} {
		c, w := hfTokenRequest("GET", "", user.ID)
		c.Request.URL.RawQuery = query
		h.GetRunHistory(c)
		require.Equal(t, http.StatusBadRequest, w.Code, query)
	}
	c, w := hfTokenRequest("GET", "", user.ID)
	h.GetRunHistory(c)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"runs":[]`)
	require.Contains(t, w.Body.String(), `"total":0`)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
}
