package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/jaysqvl/Jotist/internal/models"
	"github.com/jaysqvl/Jotist/internal/queue"
	"github.com/jaysqvl/Jotist/internal/repository"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type queueRunInput[Params any] struct {
	Parameters       *Params `json:"parameters,omitempty"`
	ProfileID        *string `json:"profile_id,omitempty"`
	ProfileName      *string `json:"profile_name,omitempty"`
	ReuseCheckpoints *bool   `json:"reuse_checkpoints,omitempty"`
}

// Decode parameters over server defaults, preserving explicit false and zero values.
type queueRunRequest = queueRunInput[json.RawMessage]

// TranscriptionQueueResponse is the public per-audio queue envelope.
type TranscriptionQueueResponse struct {
	JobID      string                          `json:"job_id"`
	ActiveItem *models.TranscriptionQueueItem  `json:"active_item"`
	Items      []models.TranscriptionQueueItem `json:"items"`
	History    []models.TranscriptionQueueItem `json:"history,omitempty"`
	Cleared    *int64                          `json:"cleared,omitempty"`
}

// ReorderTranscriptionQueueRequest replaces the complete future-run order.
type ReorderTranscriptionQueueRequest struct {
	OrderedIDs []string `json:"ordered_ids" binding:"required" nullable:"false"`
}

// ListTranscriptionQueue returns the active sequential run and its waiting
// successors. Terminal audit records are available with include_terminal=true.
func (h *Handler) ListTranscriptionQueue(c *gin.Context) {
	jobID := c.Param("id")
	if !h.requireQueueJob(c, jobID) {
		return
	}

	includeTerminal, _ := strconv.ParseBool(c.Query("include_terminal"))
	items, err := h.taskQueue.ListSequentialRuns(c.Request.Context(), jobID, includeTerminal)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list queued runs"})
		return
	}
	c.JSON(http.StatusOK, transcriptionQueueEnvelope(jobID, items))
}

// QueueTranscriptionRun persists one future run. When the audio is idle, this
// item is promoted immediately; otherwise it waits behind the active run.
func (h *Handler) QueueTranscriptionRun(c *gin.Context) {
	jobID := c.Param("id")
	job, err := h.jobRepo.FindByID(c.Request.Context(), jobID)
	if err != nil {
		writeQueueJobError(c, err)
		return
	}

	var request queueRunRequest
	if err := bindLimitedJSON(c, &request, maxAuthBodyBytes); err != nil {
		c.JSON(requestBodyErrorStatus(err), gin.H{"error": "Invalid queue request"})
		return
	}

	params, profileID, profileName, ok := h.resolveQueuedRun(c, &request)
	if !ok {
		return
	}
	if _, err := h.validateTranscriptionParams(c, job, jobID, params); err != nil {
		return
	}

	var preserveErr error
	items, err := h.taskQueue.AddSequentialRunsWithPreparation(c.Request.Context(), jobID, []models.TranscriptionQueueItem{{
		Parameters:  *params,
		ProfileID:   profileID,
		ProfileName: profileName,
	}}, func() error {
		// Preserve a legacy/latest transcript before an idle promotion clears the
		// parent job's mutable result fields. Preparation and admission share the
		// TaskQueue lock so deletion or an immediate rerun cannot interleave.
		currentJob, currentErr := h.jobRepo.FindByID(c.Request.Context(), jobID)
		if currentErr != nil {
			preserveErr = currentErr
			return preserveErr
		}
		preserveErr = h.preserveCurrentRunSnapshot(c.Request.Context(), currentJob)
		return preserveErr
	})
	if err != nil {
		if preserveErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to preserve the current run"})
			return
		}
		if errors.Is(err, repository.ErrQueueLimitReached) || errors.Is(err, queue.ErrJobStateChanged) {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to queue transcription run"})
		return
	}
	c.JSON(http.StatusCreated, transcriptionQueueEnvelope(jobID, items))
}

// ReorderTranscriptionQueue changes the order of every future queued run.
func (h *Handler) ReorderTranscriptionQueue(c *gin.Context) {
	jobID := c.Param("id")
	if !h.requireQueueJob(c, jobID) {
		return
	}

	var request ReorderTranscriptionQueueRequest
	if err := bindLimitedJSON(c, &request, maxAuthBodyBytes); err != nil || request.OrderedIDs == nil {
		status := http.StatusBadRequest
		if err != nil {
			status = requestBodyErrorStatus(err)
		}
		c.JSON(status, gin.H{"error": "ordered_ids is required"})
		return
	}
	items, err := h.taskQueue.ReorderSequentialRuns(c.Request.Context(), jobID, request.OrderedIDs)
	if err != nil {
		if errors.Is(err, repository.ErrQueueOrderMismatch) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to reorder queued runs"})
		return
	}
	c.JSON(http.StatusOK, transcriptionQueueEnvelope(jobID, items))
}

// CancelQueuedTranscriptionRun cancels one future run before it is promoted.
func (h *Handler) CancelQueuedTranscriptionRun(c *gin.Context) {
	jobID := c.Param("id")
	if !h.requireQueueJob(c, jobID) {
		return
	}
	items, err := h.taskQueue.CancelSequentialRun(c.Request.Context(), jobID, c.Param("queue_id"))
	if err != nil {
		switch {
		case errors.Is(err, repository.ErrQueueItemNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		case errors.Is(err, repository.ErrQueueItemNotQueued):
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to cancel queued run"})
		}
		return
	}
	c.JSON(http.StatusOK, transcriptionQueueEnvelope(jobID, items))
}

// ClearTranscriptionQueue cancels every future run without stopping the active run.
func (h *Handler) ClearTranscriptionQueue(c *gin.Context) {
	jobID := c.Param("id")
	if !h.requireQueueJob(c, jobID) {
		return
	}
	items, cleared, err := h.taskQueue.ClearSequentialRuns(c.Request.Context(), jobID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to clear queued runs"})
		return
	}
	response := transcriptionQueueEnvelope(jobID, items)
	response["cleared"] = cleared
	c.JSON(http.StatusOK, response)
}

func (h *Handler) resolveQueuedRun(c *gin.Context, request *queueRunRequest) (*models.WhisperXParams, *string, *string, bool) {
	if request.ProfileID != nil && strings.TrimSpace(*request.ProfileID) != "" {
		profileID := strings.TrimSpace(*request.ProfileID)
		profile, err := h.profileRepo.FindByID(c.Request.Context(), profileID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "Profile not found"})
			} else {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load profile"})
			}
			return nil, nil, nil, false
		}
		// Stored profile data is authoritative. In particular, redacted browser
		// payloads cannot accidentally erase the credentials needed later.
		name := profile.Name
		if request.ReuseCheckpoints != nil {
			profile.Parameters.ReuseCheckpoints = request.ReuseCheckpoints
		}
		params, err := h.admitSavedProfile(c, profile)
		if err != nil {
			if errors.Is(err, repository.ErrAdaptiveConflict) {
				c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			} else {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Could not snapshot profile settings; reload the profile and retry"})
			}
			return nil, nil, nil, false
		}
		return &params, &profile.ID, &name, true
	}
	if request.Parameters == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "parameters or profile_id is required"})
		return nil, nil, nil, false
	}
	params := defaultTranscriptionParams()
	if err := json.Unmarshal(*request.Parameters, &params); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid transcription parameters"})
		return nil, nil, nil, false
	}
	if request.ReuseCheckpoints != nil {
		params.ReuseCheckpoints = request.ReuseCheckpoints
	}
	clearClientLearningSnapshot(&params)
	return &params, nil, nil, true
}

func (h *Handler) requireQueueJob(c *gin.Context, jobID string) bool {
	_, err := h.jobRepo.FindByID(c.Request.Context(), jobID)
	if err != nil {
		writeQueueJobError(c, err)
		return false
	}
	return true
}

func writeQueueJobError(c *gin.Context, err error) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Job not found"})
		return
	}
	c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get job"})
}

func transcriptionQueueEnvelope(jobID string, records []models.TranscriptionQueueItem) gin.H {
	queued := make([]models.TranscriptionQueueItem, 0)
	history := make([]models.TranscriptionQueueItem, 0)
	var active *models.TranscriptionQueueItem
	for index := range records {
		item := records[index]
		switch item.Status {
		case models.QueueStatusPending, models.QueueStatusProcessing:
			if active == nil {
				itemCopy := item
				active = &itemCopy
			}
		case models.QueueStatusQueued:
			queued = append(queued, item)
		default:
			history = append(history, item)
		}
	}

	response := gin.H{
		"job_id":      jobID,
		"active_item": active,
		"items":       queued,
	}
	if len(history) > 0 {
		response["history"] = history
	}
	return response
}
