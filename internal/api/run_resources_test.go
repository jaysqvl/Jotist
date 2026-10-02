package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/jaysqvl/Jotist/internal/models"
)

func TestRunResourcesReturnNumericEvidenceWithoutCheckpointReadsOrPrivateMetadata(t *testing.T) {
	f := newRecoveryResponseFixture(t, models.WhisperXParams{})
	f.stage(t, "recognition", "recognition", nil)
	peak := int64(123456)
	measurement := models.StageMeasurements{InvocationID: uuid.NewString(), ProcessPeakRSSBytes: &peak, Samples: 2, ElapsedSeconds: 1}
	require.NoError(t, f.store.AppendExecutionMeasurements(context.Background(), f.run.ID, f.run.OwnerGeneration, measurement))
	c, w := hfTokenRequest(http.MethodGet, "", f.userID)
	c.Params = gin.Params{{Key: "id", Value: f.job.ID}, {Key: "run_id", Value: f.run.ID}}
	f.h.GetRunResources(c)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var result map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	require.Equal(t, true, result["available"])
	invocations := result["invocations"].([]interface{})
	require.Len(t, invocations, 1)
	require.Equal(t, float64(peak), invocations[0].(map[string]interface{})["process_peak_rss_bytes"])
	require.NotContains(t, w.Body.String(), "provenance")
	require.NotContains(t, w.Body.String(), "transcript")
	require.NotContains(t, w.Body.String(), "settings_hash")
	c, w = hfTokenRequest(http.MethodGet, "", f.userID)
	c.Params = gin.Params{{Key: "id", Value: "different-recording"}, {Key: "run_id", Value: f.run.ID}}
	f.h.GetRunResources(c)
	require.Equal(t, http.StatusNotFound, w.Code)
}
