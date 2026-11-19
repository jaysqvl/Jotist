package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jaysqvl/Jotist/internal/auth"
	"github.com/jaysqvl/Jotist/internal/config"
	"github.com/jaysqvl/Jotist/internal/database"
	"github.com/jaysqvl/Jotist/internal/modeldownloads"
	"github.com/jaysqvl/Jotist/internal/models"
	"github.com/jaysqvl/Jotist/internal/modelstorage"
	"github.com/jaysqvl/Jotist/internal/netpolicy"
	"github.com/jaysqvl/Jotist/internal/platform"
	"github.com/jaysqvl/Jotist/internal/processing"
	"github.com/jaysqvl/Jotist/internal/processutil"
	"github.com/jaysqvl/Jotist/internal/queue"
	"github.com/jaysqvl/Jotist/internal/repository"
	"github.com/jaysqvl/Jotist/internal/service"
	"github.com/jaysqvl/Jotist/internal/sse"
	"github.com/jaysqvl/Jotist/internal/transcription"
	appversion "github.com/jaysqvl/Jotist/internal/version"
	"github.com/jaysqvl/Jotist/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Handler contains all the API handlers
type Handler struct {
	config              *config.Config
	authService         *auth.AuthService
	loginLimiter        *auth.LoginAttemptLimiter
	userService         service.UserService
	fileService         service.FileService
	jobRepo             repository.JobRepository
	apiKeyRepo          repository.APIKeyRepository
	profileRepo         repository.ProfileRepository
	userRepo            repository.UserRepository
	llmConfigRepo       repository.LLMConfigRepository
	summaryRepo         repository.SummaryRepository
	chatRepo            repository.ChatRepository
	noteRepo            repository.NoteRepository
	speakerMappingRepo  repository.SpeakerMappingRepository
	refreshTokenRepo    repository.RefreshTokenRepository
	taskQueue           *queue.TaskQueue
	unifiedProcessor    *transcription.UnifiedJobProcessor
	quickTranscription  *transcription.QuickTranscriptionService
	multiTrackProcessor *processing.MultiTrackProcessor
	broadcaster         *sse.Broadcaster
	cliAuthStore        *cliAuthorizationStore
	outboundHTTPClient  *http.Client
	resourceAdmission   *resourceAdmission
	modelStorage        *modelstorage.Manager
	modelRetention      *modelstorage.Retainer
	modelDownloads      *modeldownloads.Service
}

// NewHandler creates a new handler
func NewHandler(
	cfg *config.Config,
	authService *auth.AuthService,
	userService service.UserService,
	fileService service.FileService,
	jobRepo repository.JobRepository,
	apiKeyRepo repository.APIKeyRepository,
	profileRepo repository.ProfileRepository,
	userRepo repository.UserRepository,
	llmConfigRepo repository.LLMConfigRepository,
	summaryRepo repository.SummaryRepository,
	chatRepo repository.ChatRepository,
	noteRepo repository.NoteRepository,
	speakerMappingRepo repository.SpeakerMappingRepository,
	refreshTokenRepo repository.RefreshTokenRepository,
	taskQueue *queue.TaskQueue,
	unifiedProcessor *transcription.UnifiedJobProcessor,
	quickTranscription *transcription.QuickTranscriptionService,
	multiTrackProcessor *processing.MultiTrackProcessor,
	broadcaster *sse.Broadcaster,
) *Handler {
	resourceAdmission := newResourceAdmission(cfg)
	if quickTranscription != nil {
		quickTranscription.UseSharedMediaSlots(resourceAdmission.mediaSlots)
	}

	modelStorage := newModelStorageManager(cfg, database.DB)
	h := &Handler{
		config:              cfg,
		authService:         authService,
		loginLimiter:        newLoginAttemptLimiter(cfg),
		userService:         userService,
		fileService:         fileService,
		jobRepo:             jobRepo,
		apiKeyRepo:          apiKeyRepo,
		profileRepo:         profileRepo,
		userRepo:            userRepo,
		llmConfigRepo:       llmConfigRepo,
		summaryRepo:         summaryRepo,
		chatRepo:            chatRepo,
		noteRepo:            noteRepo,
		speakerMappingRepo:  speakerMappingRepo,
		refreshTokenRepo:    refreshTokenRepo,
		taskQueue:           taskQueue,
		unifiedProcessor:    unifiedProcessor,
		quickTranscription:  quickTranscription,
		multiTrackProcessor: multiTrackProcessor,
		broadcaster:         broadcaster,
		cliAuthStore:        newCLIAuthorizationStore(),
		outboundHTTPClient:  netpolicy.NewPublicHTTPClient(5 * time.Minute),
		resourceAdmission:   resourceAdmission,
		modelStorage:        modelStorage,
		modelRetention:      modelstorage.NewRetainer(modelStorage, repository.NewModelCacheRepository(database.DB)),
	}
	h.modelDownloads = newModelDownloads(h, database.DB)
	return h
}

// SetOutboundHTTPClient replaces the custom-provider client for an embedding
// or test harness. The standalone server always uses the hardened default.
func (h *Handler) SetOutboundHTTPClient(client *http.Client) {
	if client != nil {
		h.outboundHTTPClient = client
	}
}

// SubmitJobRequest represents the submit job request
type SubmitJobRequest struct {
	Title       *string               `json:"title,omitempty"`
	Diarization bool                  `json:"diarization"`
	Parameters  models.WhisperXParams `json:"parameters"`
}

// LoginRequest represents the login request
type LoginRequest struct {
	Username string `json:"username" binding:"required,min=1,max=50"`
	Password string `json:"password" binding:"required,max=1024"`
}

// LoginResponse represents the login response
type LoginResponse struct {
	Token string `json:"token"`
	User  struct {
		ID       uint   `json:"id"`
		Username string `json:"username"`
	} `json:"user"`
}

// RegisterRequest represents the registration request
type RegisterRequest struct {
	Username        string `json:"username" binding:"required,min=3,max=50"`
	Password        string `json:"password" binding:"required,min=6,max=1024"`
	ConfirmPassword string `json:"confirmPassword" binding:"required,max=1024"`
}

// RegistrationStatusResponse represents the registration status
type RegistrationStatusResponse struct {
	// Match tests expecting snake_case key
	RegistrationEnabled bool `json:"registration_enabled"`
}

// ChangePasswordRequest represents the change password request
type ChangePasswordRequest struct {
	CurrentPassword string `json:"currentPassword" binding:"required,max=1024"`
	NewPassword     string `json:"newPassword" binding:"required,min=6,max=1024"`
	ConfirmPassword string `json:"confirmPassword" binding:"required,max=1024"`
}

// ChangeUsernameRequest represents the change username request
type ChangeUsernameRequest struct {
	NewUsername string `json:"newUsername" binding:"required,min=3,max=50"`
	Password    string `json:"password" binding:"required,max=1024"`
}

// CreateAPIKeyRequest represents the create API key request
type CreateAPIKeyRequest struct {
	Name        string `json:"name" binding:"required,min=1,max=100"`
	Description string `json:"description,omitempty"`
}

// CreateAPIKeyResponse represents the create API key response
type CreateAPIKeyResponse struct {
	ID          uint   `json:"id"`
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// YouTubeDownloadRequest represents the YouTube download request
type YouTubeDownloadRequest struct {
	URL   string  `json:"url" binding:"required,max=2048"`
	Title *string `json:"title,omitempty" binding:"omitempty,max=500"`
}

// YouTubeDownloadResponse represents the YouTube download response
type YouTubeDownloadResponse struct {
	JobID    string `json:"job_id"`
	Status   string `json:"status"`
	Message  string `json:"message,omitempty"`
	Title    string `json:"title,omitempty"`
	Progress int    `json:"progress,omitempty"`
}

// LLMConfigRequest represents the LLM configuration request
type LLMConfigRequest struct {
	Provider      string  `json:"provider" binding:"required,oneof=ollama openai"`
	BaseURL       *string `json:"base_url,omitempty"`
	OpenAIBaseURL *string `json:"openai_base_url,omitempty"`
	APIKey        *string `json:"api_key,omitempty"`
	IsActive      bool    `json:"is_active" required:"false"`
}

// LLMConfigResponse represents the LLM configuration response
type LLMConfigResponse struct {
	ID            uint    `json:"id"`
	Provider      string  `json:"provider"`
	BaseURL       *string `json:"base_url,omitempty"`
	OpenAIBaseURL *string `json:"openai_base_url,omitempty"`
	HasAPIKey     bool    `json:"has_api_key"` // Don't return actual API key
	IsActive      bool    `json:"is_active"`
	CreatedAt     string  `json:"created_at"`
	UpdatedAt     string  `json:"updated_at"`
}

// APIKeyListResponse represents an API key in the list (without the actual key)
type APIKeyListResponse struct {
	ID          uint   `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	KeyPreview  string `json:"key_preview"`
	IsActive    bool   `json:"is_active"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
	LastUsed    string `json:"last_used,omitempty"`
}

// APIKeysWrapper wraps the API keys list response
type APIKeysWrapper struct {
	APIKeys []APIKeyListResponse `json:"api_keys"`
}

// transformAPIKeyForList converts a models.APIKey to APIKeyListResponse
func transformAPIKeyForList(apiKey models.APIKey) APIKeyListResponse {
	keyPreview := ""
	if len(apiKey.Key) > 8 {
		keyPreview = apiKey.Key[:8] + "..."
	} else if apiKey.Key != "" {
		keyPreview = apiKey.Key + "..."
	}

	lastUsed := ""
	if apiKey.LastUsed != nil {
		lastUsed = apiKey.LastUsed.Format(time.RFC3339)
	}

	description := ""
	if apiKey.Description != nil {
		description = *apiKey.Description
	}

	return APIKeyListResponse{
		ID:          apiKey.ID,
		Name:        apiKey.Name,
		Description: description,
		KeyPreview:  keyPreview,
		IsActive:    apiKey.IsActive,
		CreatedAt:   apiKey.CreatedAt.Format(time.RFC3339),
		UpdatedAt:   apiKey.UpdatedAt.Format(time.RFC3339),
		LastUsed:    lastUsed,
	}
}

func (h *Handler) UploadAudio(c *gin.Context) {
	release, ok := h.beginMultipartUpload(c, h.config.UploadDir)
	if !ok {
		return
	}
	defer release()
	// Note: This endpoint is also used by the CLI watcher to upload files.
	// The CLI authenticates using a long-lived JWT token.
	header, err := c.FormFile(paramAudio)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Audio file is required"})
		return
	}
	if !h.acceptUploadSize(c, h.config.UploadDir, header.Size) {
		return
	}

	filePath, err := h.fileService.SaveUpload(header, h.config.UploadDir)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save file"})
		return
	}

	job, err := h.createUploadedAudioJob(c, filePath, c.PostForm(paramTitle), "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, job)
}

func (h *Handler) UploadVideo(c *gin.Context) {
	release, ok := h.beginMultipartUpload(c, h.config.UploadDir)
	if !ok {
		return
	}
	defer release()
	header, err := c.FormFile("video")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Video file is required"})
		return
	}
	if !h.acceptUploadSize(c, h.config.UploadDir, header.Size) {
		return
	}

	videoPath, err := h.fileService.SaveUpload(header, h.config.UploadDir)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save file"})
		return
	}

	job, err := h.createUploadedVideoJob(c, videoPath, c.PostForm(paramTitle), "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, job)
}

func (h *Handler) UploadMultiTrack(c *gin.Context) {
	release, ok := h.beginMultipartUpload(c, h.config.TempDir)
	if !ok {
		return
	}
	defer release()
	form, err := c.MultipartForm()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to parse multipart form"})
		return
	}

	files := form.File["files"]
	if len(files) == 0 {
		files = form.File["tracks"]
	}
	if len(files) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No files uploaded"})
		return
	}
	aupFiles := form.File["aup"]
	if len(aupFiles) != 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Exactly one .aup file is required"})
		return
	}
	totalSize := aupFiles[0].Size
	for _, file := range files {
		totalSize += file.Size
	}
	if !h.acceptUploadSize(c, h.config.TempDir, totalSize) {
		return
	}

	tempDir := filepath.Join(h.config.TempDir, "legacy_multitrack", uuid.New().String())
	defer os.RemoveAll(tempDir)

	aupPath := filepath.Join(tempDir, "aup", safeFilename(aupFiles[0].Filename))
	if err := saveMultipartFileToPath(aupFiles[0], aupPath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save AUP file"})
		return
	}
	aup := assembledUploadFile{
		Role:         models.UploadFileRoleAup,
		OriginalName: aupFiles[0].Filename,
		ContentType:  aupFiles[0].Header.Get("Content-Type"),
		Path:         aupPath,
		Size:         aupFiles[0].Size,
	}

	tracks := make([]assembledUploadFile, 0, len(files))
	for i, fileHeader := range files {
		trackPath := filepath.Join(tempDir, "tracks", fmt.Sprintf("%03d-%s", i, safeFilename(fileHeader.Filename)))
		if err := saveMultipartFileToPath(fileHeader, trackPath); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to save file %s", fileHeader.Filename)})
			return
		}
		tracks = append(tracks, assembledUploadFile{
			Role:         models.UploadFileRoleTrack,
			OriginalName: fileHeader.Filename,
			ContentType:  fileHeader.Header.Get("Content-Type"),
			Path:         trackPath,
			Size:         fileHeader.Size,
		})
	}

	job, err := h.createMultiTrackJobFromPaths(c, c.PostForm(paramTitle), aup, tracks, "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, job)
}

func (h *Handler) GetMergeStatus(c *gin.Context) {
	jobID := c.Param("id")

	status, errorMsg, err := h.multiTrackProcessor.GetMergeStatus(jobID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Job not found"})
		return
	}

	response := gin.H{
		"merge_status": status,
	}

	if errorMsg != nil {
		response["merge_error"] = *errorMsg
	}

	c.JSON(http.StatusOK, response)
}

func (h *Handler) GetTrackProgress(c *gin.Context) {
	jobID := c.Param("id")

	// Get the main job details using repository
	job, err := h.jobRepo.FindWithAssociations(c.Request.Context(), jobID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Job not found"})
		return
	}

	// Only provide track progress for multi-track jobs
	if !job.IsMultiTrack {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Not a multi-track job"})
		return
	}

	// Get individual transcripts to see which tracks are completed
	var individualTranscripts map[string]string
	if job.IndividualTranscripts != nil {
		_ = json.Unmarshal([]byte(*job.IndividualTranscripts), &individualTranscripts)
	}

	// Find active track jobs (temp jobs still in progress)
	activeTrackJobs, _ := h.jobRepo.FindActiveTrackJobs(c.Request.Context(), jobID)

	// Build track progress information
	trackProgress := make([]map[string]interface{}, 0)
	totalTracks := len(job.MultiTrackFiles)
	completedTracks := 0

	for _, trackFile := range job.MultiTrackFiles {
		trackInfo := map[string]interface{}{
			"track_name":  trackFile.FileName,
			"track_index": trackFile.TrackIndex,
		}

		// Check if this track is completed (only count if actually in individualTranscripts)
		if _, exists := individualTranscripts[trackFile.FileName]; exists {
			trackInfo["status"] = "completed"
			completedTracks++
		} else {
			// Check if there's an active job for this track
			isActive := false
			for _, activeJob := range activeTrackJobs {
				if strings.Contains(activeJob.ID, trackFile.FileName) {
					trackInfo["status"] = "processing"
					isActive = true
					break
				}
			}
			if !isActive {
				if job.Status == "processing" {
					trackInfo["status"] = "pending"
				} else {
					trackInfo["status"] = "failed"
				}
			}
		}

		trackProgress = append(trackProgress, trackInfo)
	}

	// Calculate overall progress based on actual track status
	progressPercentage := 0.0
	if totalTracks > 0 {
		progressPercentage = float64(completedTracks) / float64(totalTracks) * 100
	}

	response := gin.H{
		paramJobID:       job.ID,
		"is_multi_track": true,
		"overall_status": job.Status,
		"merge_status":   job.MergeStatus,
		"tracks":         trackProgress,
		"progress": map[string]interface{}{
			"completed_tracks": completedTracks,
			"total_tracks":     totalTracks,
			"percentage":       progressPercentage,
		},
	}

	c.JSON(http.StatusOK, response)
}

func (h *Handler) SubmitJob(c *gin.Context) {
	release, ok := h.beginMultipartUpload(c, h.config.UploadDir)
	if !ok {
		return
	}
	defer release()
	// Parse multipart form
	header, err := c.FormFile(paramAudio)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Audio file is required"})
		return
	}
	if !h.acceptUploadSize(c, h.config.UploadDir, header.Size) {
		return
	}

	// Validate options and snapshot inherited context before persisting the audio.
	// Client errors must not leave a saved file without a transcription job.
	params, err := submitParamsFromForm(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.validateContextForRun(c, &params); err != nil {
		return
	}

	// Save file using FileService
	uploadDir := h.config.UploadDir
	filePath, err := h.fileService.SaveUpload(header, uploadDir)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save file"})
		return
	}

	job, err := h.createSubmittedJobWithParams(c, filePath, c.PostForm(paramTitle), params, "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, job)
}

func (h *Handler) GetJobStatus(c *gin.Context) {
	jobID := c.Param("id")

	job, err := h.taskQueue.GetJobStatus(jobID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Job not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get job status"})
		return
	}

	c.JSON(http.StatusOK, job)
}

type transcriptResponse struct {
	JobID           string           `json:"job_id"`
	Title           *string          `json:"title"`
	RunID           *string          `json:"run_id,omitempty"`
	ActiveRunPinned *bool            `json:"active_run_pinned,omitempty"`
	PinnedRunID     *string          `json:"pinned_run_id,omitempty"`
	Transcript      any              `json:"transcript"`
	Status          models.JobStatus `json:"status"`
	Available       bool             `json:"available"`
	Message         string           `json:"message,omitempty"`
	CreatedAt       time.Time        `json:"created_at"`
	UpdatedAt       time.Time        `json:"updated_at"`
}

func (h *Handler) GetTranscript(c *gin.Context) {
	var query struct {
		ReadOnly bool `form:"read_only"`
	}
	if err := c.ShouldBindQuery(&query); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "read_only must be a boolean"})
		return
	}
	jobID := c.Param("id")

	job, err := h.jobRepo.FindByID(c.Request.Context(), jobID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Job not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get job"})
		return
	}

	if !query.ReadOnly {
		if err := h.preserveCurrentRunSnapshot(c.Request.Context(), job); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to prepare active transcript"})
			return
		}
	}

	response := transcriptResponse{
		JobID:     job.ID,
		Title:     job.Title,
		Status:    job.Status,
		CreatedAt: job.CreatedAt,
		UpdatedAt: job.UpdatedAt,
	}
	if activeExecution, activePinned, err := h.resolveActiveExecution(c.Request.Context(), job); err == nil && activeExecution.Transcript != nil {
		transcript, err := parseTranscriptPayload(*activeExecution.Transcript)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse transcript"})
			return
		}

		pinnedRunID := ""
		if job.PinnedExecutionID != nil {
			pinnedRunID = *job.PinnedExecutionID
		}
		response.RunID = &activeExecution.ID
		response.ActiveRunPinned = &activePinned
		response.PinnedRunID = &pinnedRunID
		response.Transcript = transcript
		response.Available = true
		c.JSON(http.StatusOK, response)
		return
	} else if err != nil && err != gorm.ErrRecordNotFound {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to resolve active transcript"})
		return
	}

	switch {
	case job.Status != models.StatusCompleted:
		response.Message = fmt.Sprintf("Transcript not ready, current status: %s", job.Status)
	case job.Transcript == nil:
		response.Message = "Transcript not available"
	default:
		transcript, err := parseTranscriptPayload(*job.Transcript)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse transcript"})
			return
		}
		response.Transcript = transcript
		response.Available = true
	}
	c.JSON(http.StatusOK, response)
}

type recordingListResponse struct {
	Jobs       []models.TranscriptionJob `json:"jobs"`
	Pagination recordingPagination       `json:"pagination"`
}

type recordingPagination struct {
	Page  int   `json:"page"`
	Limit int   `json:"limit"`
	Total int64 `json:"total"`
	Pages int64 `json:"pages"`
}

func (h *Handler) ListTranscriptionJobs(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "10"))
	offset := (page - 1) * limit

	sortBy := c.Query("sort_by")
	sortOrder := c.Query("sort_order")
	searchQuery := c.Query("q")
	updatedAfterStr := c.Query("updated_after")

	var updatedAfter *time.Time
	if updatedAfterStr != "" {
		if t, err := time.Parse(time.RFC3339, updatedAfterStr); err == nil {
			updatedAfter = &t
		}
	}

	jobs, total, err := h.jobRepo.ListWithParams(c.Request.Context(), offset, limit, sortBy, sortOrder, searchQuery, updatedAfter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list jobs"})
		return
	}

	c.JSON(http.StatusOK, recordingListResponse{
		Jobs: jobs,
		Pagination: recordingPagination{
			Page:  page,
			Limit: limit,
			Total: total,
			Pages: (total + int64(limit) - 1) / int64(limit),
		},
	})
}

func (h *Handler) GetTranscriptionJob(c *gin.Context) {
	id := c.Param("id")

	job, err := h.jobRepo.FindWithAssociations(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Job not found"})
		return
	}

	c.JSON(http.StatusOK, job)
}

func (h *Handler) StartTranscription(c *gin.Context) {
	jobID := c.Param("id")

	job, err := h.getJobForTranscription(c, jobID)
	if err != nil {
		return
	}

	requestParams, err := h.getValidatedTranscriptionParams(c, job, jobID)
	if err != nil {
		return
	}

	if err := h.preserveCurrentRunSnapshot(c.Request.Context(), job); err != nil {
		logger.Error("Failed to preserve current run before rerun", "job_id", jobID, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to preserve current run before starting transcription"})
		return
	}

	// Update job with parameters
	job.Parameters = *requestParams
	job.Diarization = requestParams.Diarize
	job.Status = models.StatusPending

	// Keep the published result until the replacement execution commits all
	// requested outputs. A failed or cancelled rerun must not erase it.
	job.ErrorMessage = nil

	// Atomically recheck idle/queue state, save the parameters, and dispatch.
	// This prevents a concurrent sequential POST from being overwritten by a
	// legacy immediate rerun.
	if err := h.taskQueue.StartImmediateRun(c.Request.Context(), job); err != nil {
		if errors.Is(err, queue.ErrJobStateChanged) {
			c.JSON(http.StatusConflict, gin.H{"error": "Cannot start an immediate run because the job or queue changed"})
			return
		}
		logger.Error("Failed to enqueue job", "job_id", jobID, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to enqueue job"})
		return
	}

	// Log job started
	params := make(map[string]any)
	params["model"] = requestParams.Model
	params["model_family"] = requestParams.ModelFamily
	params["diarization"] = requestParams.Diarize
	if requestParams.Diarize && requestParams.DiarizeModel != "" {
		params["diarize_model"] = requestParams.DiarizeModel
	}
	params["language"] = requestParams.Language
	params["device"] = requestParams.Device

	filename := filepath.Base(job.AudioPath)
	logger.JobStarted(jobID, filename, requestParams.ModelFamily, params)

	c.JSON(http.StatusOK, job)
}

func (h *Handler) getJobForTranscription(c *gin.Context, jobID string) (*models.TranscriptionJob, error) {
	job, err := h.jobRepo.FindByID(c.Request.Context(), jobID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Job not found"})
			return nil, err
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get job"})
		return nil, err
	}

	// Allow transcription for uploaded, completed, and failed jobs (re-transcription)
	if job.Status != models.StatusUploaded && job.Status != models.StatusCompleted && job.Status != models.StatusFailed {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Cannot start transcription: job is currently processing or pending"})
		return nil, fmt.Errorf("invalid job status")
	}
	return job, nil
}

// ImmediateTranscriptionRequest preserves the flat parameter API. When
// profile_id is supplied, the stored profile is authoritative, including its
// write-only credentials, and accompanying parameter values are ignored.
type ImmediateTranscriptionRequest struct {
	models.WhisperXParams
	ProfileID *string `json:"profile_id,omitempty"`
}

func (h *Handler) getValidatedTranscriptionParams(c *gin.Context, job *models.TranscriptionJob, jobID string) (*models.WhisperXParams, error) {
	request := ImmediateTranscriptionRequest{WhisperXParams: defaultTranscriptionParams()}

	// Parse request body parameters, overriding defaults
	if err := bindJSON(c, &request); err != nil && !errors.Is(err, io.EOF) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid transcription parameters JSON"})
		return nil, err
	}
	params := &request.WhisperXParams
	if request.ProfileID != nil {
		if strings.TrimSpace(*request.ProfileID) == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "profile_id must identify a saved profile"})
			return nil, fmt.Errorf("empty transcription profile ID")
		}
		var ok bool
		params, _, _, ok = h.resolveQueuedRun(c, &queueRunRequest{ProfileID: request.ProfileID, ReuseCheckpoints: request.ReuseCheckpoints})
		if !ok {
			return nil, fmt.Errorf("failed to load transcription profile")
		}
	} else {
		clearClientLearningSnapshot(params)
	}
	return h.validateTranscriptionParams(c, job, jobID, params)
}

func defaultTranscriptionParams() models.WhisperXParams {
	return models.WhisperXParams{
		ModelFamily:                    "whisper", // Default to whisper for backward compatibility
		Model:                          "small",
		ModelCacheOnly:                 false,
		Device:                         "cpu",
		DeviceIndex:                    0,
		BatchSize:                      8,
		ComputeType:                    "float32",
		Threads:                        0,
		OutputFormat:                   "all",
		Verbose:                        true,
		Task:                           "transcribe",
		InterpolateMethod:              "nearest",
		NoAlign:                        false,
		ReturnCharAlignments:           false,
		VadMethod:                      "pyannote",
		VadOnset:                       0.5,
		VadOffset:                      0.363,
		ChunkSize:                      30,
		Diarize:                        false,
		DiarizeModel:                   "pyannote",
		DiarizationDevice:              "same",
		SpeakerEmbeddings:              false,
		Temperature:                    0,
		BestOf:                         5,
		BeamSize:                       5,
		Patience:                       1.0,
		LengthPenalty:                  1.0,
		SuppressNumerals:               false,
		ConditionOnPreviousText:        false,
		Fp16:                           true,
		TemperatureIncrementOnFallback: 0.2,
		CompressionRatioThreshold:      2.4,
		LogprobThreshold:               -1.0,
		NoSpeechThreshold:              0.6,
		HighlightWords:                 false,
		SegmentResolution:              "sentence",
		PrintProgress:                  false,
		AttentionContextLeft:           256,
		AttentionContextRight:          256,
		IsMultiTrackEnabled:            false,
	}
}

func (h *Handler) validateTranscriptionParams(c *gin.Context, job *models.TranscriptionJob, jobID string, requestParams *models.WhisperXParams) (*models.WhisperXParams, error) {
	if err := h.validateContextForRun(c, requestParams); err != nil {
		return nil, err
	}
	// Debug: log what we received
	logger.Debug("Parsed transcription parameters",
		"job_id", jobID,
		"model_family", requestParams.ModelFamily,
		"model", requestParams.Model,
		"diarization", requestParams.Diarize,
		"diarize_model", requestParams.DiarizeModel,
		"language", requestParams.Language)

	// Model access can use a write-only profile token, HF_TOKEN, or the local
	// Hugging Face login/cache. Let the adapter check access instead of requiring
	// the client to resend a credential for every NVIDIA run.

	// Validate multi-track compatibility
	if job.IsMultiTrack && !requestParams.IsMultiTrackEnabled {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Multi-track audio requires multi-track transcription to be enabled in the parameters"})
		return nil, fmt.Errorf("multi-track mismatch")
	}

	if !job.IsMultiTrack && requestParams.IsMultiTrackEnabled {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Multi-track transcription cannot be used with single-track audio files"})
		return nil, fmt.Errorf("single-track mismatch")
	}

	// Multi-track transcription should automatically disable diarization
	if requestParams.IsMultiTrackEnabled && requestParams.Diarize {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Diarization must be disabled when using multi-track transcription"})
		return nil, fmt.Errorf("diarization conflict")
	}

	return requestParams, nil
}

type killJobRequest struct {
	QueueItemID string `json:"queue_item_id" required:"false"`
}

func (h *Handler) KillJob(c *gin.Context) {
	jobID := c.Param("id")
	var request killJobRequest
	hasTargetPrecondition := c.Request.ContentLength != 0
	if hasTargetPrecondition {
		if err := bindLimitedJSON(c, &request, maxAuthBodyBytes); err != nil {
			c.JSON(requestBodyErrorStatus(err), gin.H{"error": "Invalid cancellation request"})
			return
		}
	}

	job, err := h.jobRepo.FindByID(c.Request.Context(), jobID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Job not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get job"})
		return
	}

	// Check if job can be cancelled
	if job.Status != models.StatusProcessing && job.Status != models.StatusPending {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Job is not currently running or queued"})
		return
	}

	// Attempt to kill the job
	var killErr error
	if hasTargetPrecondition {
		killErr = h.taskQueue.KillJobIfCurrent(jobID, request.QueueItemID)
	} else {
		killErr = h.taskQueue.KillJob(jobID)
	}
	if killErr != nil {
		if errors.Is(killErr, queue.ErrQueueTargetChanged) {
			c.JSON(http.StatusConflict, gin.H{"error": killErr.Error()})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": killErr.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Job cancellation requested"})
}

// UpdateTranscriptionTitle updates the title of a transcription job
func (h *Handler) UpdateTranscriptionTitle(c *gin.Context) {
	jobID := c.Param("id")
	if jobID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Job ID required"})
		return
	}

	var body struct {
		Title string `json:"title" binding:"required,min=1,max=255"`
	}
	if err := bindJSON(c, &body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	job, err := h.jobRepo.FindByID(c.Request.Context(), jobID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Job not found"})
		return
	}

	job.Title = &body.Title
	if err := h.jobRepo.Update(c.Request.Context(), job); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update title"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":         job.ID,
		"title":      job.Title,
		"status":     job.Status,
		"created_at": job.CreatedAt,
		"audio_path": job.AudioPath,
	})
}

func (h *Handler) DeleteTranscriptionJob(c *gin.Context) {
	jobID := c.Param("id")

	job, releaseDeletion, err := h.taskQueue.ReserveJobDeletion(c.Request.Context(), jobID)
	if err != nil {
		if errors.Is(err, queue.ErrJobStateChanged) {
			c.JSON(http.StatusConflict, gin.H{"error": "Cannot delete a job with active or waiting transcription runs"})
			return
		}
		c.JSON(http.StatusNotFound, gin.H{"error": "Job not found"})
		return
	}
	defer releaseDeletion()
	if h.unifiedProcessor != nil {
		if err := h.unifiedProcessor.GetUnifiedService().DeleteRecoveryRecording(c.Request.Context(), jobID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Checkpoint cleanup is pending; retry deletion. The original recording has been retained."})
			return
		}
	}

	// Commit the database aggregate before removing original media. A failed
	// transaction leaves the recording and its source available for a retry.
	if err := h.jobRepo.DeleteWithAssociations(c.Request.Context(), jobID); err != nil {
		logger.Error("Failed to delete recording", "job_id", jobID, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete job"})
		return
	}

	response := gin.H{"message": "Job deleted successfully"}
	modelstorage.ProcessGate().ActivityChanged()
	if err := h.removeRecordingMedia(job); err != nil {
		// Filesystem cleanup cannot roll back the database commit. Keep the
		// tombstone's paths and report the incomplete cleanup explicitly.
		logger.Error("Deleted recording media cleanup failed", "job_id", jobID, "error", err)
		response["warning"] = "The job was deleted, but some media files could not be removed."
	}
	c.JSON(http.StatusOK, response)
}

// removeRecordingMedia attempts every independent removal. Missing paths are
// already clean, including an AUP file removed with its multi-track directory.
func (h *Handler) removeRecordingMedia(job *models.TranscriptionJob) error {
	var cleanupErrors []error
	keepError := func(err error) {
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanupErrors = append(cleanupErrors, err)
		}
	}
	if job.IsMultiTrack && job.MultiTrackFolder != nil {
		keepError(h.fileService.RemoveDirectory(*job.MultiTrackFolder))
	} else {
		keepError(h.fileService.RemoveFile(job.AudioPath))
	}
	if job.AupFilePath != nil {
		keepError(h.fileService.RemoveFile(*job.AupFilePath))
	}
	return errors.Join(cleanupErrors...)
}

func (h *Handler) GetJobExecutionData(c *gin.Context) {
	jobID := c.Param("id")

	// Get the transcription job to check if it's multi-track
	job, err := h.jobRepo.FindWithAssociations(c.Request.Context(), jobID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Transcription job not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get transcription job"})
		return
	}

	if err := h.preserveCurrentRunSnapshot(c.Request.Context(), job); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to prepare execution data"})
		return
	}

	execution, activePinned, err := h.resolveActiveExecution(c.Request.Context(), job)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			// Return graceful empty response instead of 404
			c.JSON(http.StatusOK, gin.H{
				"transcription_job_id": jobID,
				"available":            false,
				"message":              "No execution data available for this job",
				"is_multi_track":       job.IsMultiTrack,
				"active_run_pinned":    false,
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get execution data"})
		return
	}

	pinnedRunID := ""
	if job.PinnedExecutionID != nil {
		pinnedRunID = *job.PinnedExecutionID
	}

	// Create enhanced response with multi-track data
	response := gin.H{
		"id":                   execution.ID,
		"transcription_job_id": execution.TranscriptionJobID,
		"active_run_pinned":    activePinned,
		"pinned_run_id":        pinnedRunID,
		"started_at":           execution.StartedAt,
		"completed_at":         execution.CompletedAt,
		"processing_duration":  execution.ProcessingDuration,
		"actual_parameters":    execution.ActualParameters,
		"status":               execution.Status,
		"error_message":        execution.ErrorMessage,
		"created_at":           execution.CreatedAt,
		"updated_at":           execution.UpdatedAt,
		"is_multi_track":       job.IsMultiTrack,
	}

	// Add multi-track specific data if available
	if job.IsMultiTrack && execution.MultiTrackTimings != nil {
		// Deserialize track timings
		var trackTimings []models.MultiTrackTiming
		if err := json.Unmarshal([]byte(*execution.MultiTrackTimings), &trackTimings); err == nil {
			response["multi_track_timings"] = trackTimings
		}

		// Add merge timing data
		response["merge_start_time"] = execution.MergeStartTime
		response["merge_end_time"] = execution.MergeEndTime
		response["merge_duration"] = execution.MergeDuration

		// Add multi-track files information
		response["multi_track_files"] = job.MultiTrackFiles
	}

	c.JSON(http.StatusOK, response)
}

func (h *Handler) GetAudioFile(c *gin.Context) {
	jobID := c.Param("id")

	job, err := h.jobRepo.FindByID(c.Request.Context(), jobID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Job not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get job"})
		return
	}

	// Debug logging
	fmt.Printf("DEBUG: GetAudioFile for job %s\n", jobID)
	fmt.Printf("DEBUG: Job status: %s\n", job.Status)
	fmt.Printf("DEBUG: Audio path: '%s'\n", job.AudioPath)

	// For multi-track jobs, prefer merged audio if available
	audioPath := job.AudioPath
	if job.IsMultiTrack && job.MergedAudioPath != nil && *job.MergedAudioPath != "" {
		// Check if merged audio file exists
		if _, err := os.Stat(*job.MergedAudioPath); err == nil {
			audioPath = *job.MergedAudioPath
			fmt.Printf("DEBUG: Using merged audio: %s\n", audioPath)
		} else {
			fmt.Printf("DEBUG: Merged audio not found, falling back to original: %s\n", job.AudioPath)
		}
	}

	// Check if audio file exists
	if audioPath == "" {
		fmt.Printf("DEBUG: Audio path is empty\n")
		c.JSON(http.StatusNotFound, gin.H{"error": "Audio file path not found"})
		return
	}

	// Check if file exists on filesystem
	if _, err := os.Stat(audioPath); os.IsNotExist(err) {
		fmt.Printf("DEBUG: Audio file does not exist on disk: %s\n", audioPath)
		c.JSON(http.StatusNotFound, gin.H{"error": "Audio file not found on disk"})
		return
	}

	fmt.Printf("DEBUG: Audio file exists, serving: %s\n", audioPath)

	// Set appropriate content type based on file extension
	ext := filepath.Ext(job.AudioPath)
	switch ext {
	case ".mp3":
		c.Header("Content-Type", "audio/mpeg")
	case ".wav":
		c.Header("Content-Type", "audio/wav")
	case ".m4a":
		c.Header("Content-Type", "audio/mp4")
	case ".ogg":
		c.Header("Content-Type", "audio/ogg")
	default:
		c.Header("Content-Type", "audio/mpeg")
	}

	// Add CORS headers for audio visualization and streaming
	origin := c.Request.Header.Get("Origin")
	allowOrigin := "*"
	if h.config.IsProduction() && len(h.config.AllowedOrigins) > 0 {
		// In production, validate against configured origins
		allowOrigin = ""
		for _, allowed := range h.config.AllowedOrigins {
			if origin == allowed {
				allowOrigin = origin
				break
			}
		}
	} else if origin != "" {
		// In development, echo back the origin for credentials support
		allowOrigin = origin
	}

	if allowOrigin != "" {
		c.Header("Access-Control-Allow-Origin", allowOrigin)
		c.Header("Access-Control-Allow-Credentials", "true")
	}
	c.Header("Access-Control-Expose-Headers", "Content-Range, Accept-Ranges, Content-Length")
	c.Header("Accept-Ranges", "bytes")

	// Open the file
	file, err := os.Open(audioPath)
	if err != nil {
		fmt.Printf("ERROR: Failed to open audio file: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to open audio file"})
		return
	}
	defer file.Close()

	// Get file stats
	fileInfo, err := file.Stat()
	if err != nil {
		fmt.Printf("ERROR: Failed to stat audio file: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to stat audio file"})
		return
	}

	// Use http.ServeContent for efficient streaming and range request support
	http.ServeContent(c.Writer, c.Request, filepath.Base(audioPath), fileInfo.ModTime(), file)
}

func (h *Handler) Login(c *gin.Context) {
	var req LoginRequest
	if err := bindLimitedJSON(c, &req, maxAuthBodyBytes); err != nil {
		c.JSON(requestBodyErrorStatus(err), gin.H{"error": "Invalid request"})
		return
	}

	clientIP := c.ClientIP()
	if decision := h.loginLimiter.Check(req.Username, clientIP); !decision.Allowed {
		h.writeLoginRateLimit(c, req.Username, clientIP, decision.RetryAfter, decision.Reason)
		return
	}

	user, err := h.userRepo.FindByUsername(c.Request.Context(), req.Username)
	if err != nil {
		if record := h.loginLimiter.RecordFailure(req.Username, clientIP); record.Locked {
			h.writeLoginRateLimit(c, req.Username, clientIP, record.RetryAfter, record.Reason)
			return
		}
		logger.AuthEvent("login", req.Username, clientIP, false, "reason", "user_not_found")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
		return
	}

	if !auth.CheckPassword(req.Password, user.Password) {
		if record := h.loginLimiter.RecordFailure(req.Username, clientIP); record.Locked {
			h.writeLoginRateLimit(c, req.Username, clientIP, record.RetryAfter, record.Reason)
			return
		}
		logger.AuthEvent("login", req.Username, clientIP, false, "reason", "invalid_password")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
		return
	}

	token, err := h.authService.GenerateToken(user)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
		return
	}

	// Set refresh token cookie
	if err := h.issueRefreshToken(c, user.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create session"})
		return
	}

	// Native <audio>/<video> requests cannot attach the bearer header.
	h.setAccessTokenCookie(c, token)

	response := LoginResponse{Token: token}
	response.User.ID = user.ID
	response.User.Username = user.Username

	h.loginLimiter.RecordSuccess(req.Username, clientIP)
	logger.AuthEvent("login", req.Username, clientIP, true)
	c.JSON(http.StatusOK, response)
}

func (h *Handler) writeLoginRateLimit(c *gin.Context, username, clientIP string, retryAfter time.Duration, reason string) {
	retryAfterHeaderSeconds := retryAfterSeconds(retryAfter)
	if retryAfterHeaderSeconds > 0 {
		c.Header("Retry-After", strconv.Itoa(retryAfterHeaderSeconds))
	}

	logger.AuthEvent("login", username, clientIP, false,
		"reason", reason,
		"retry_after_seconds", retryAfterHeaderSeconds,
	)
	c.JSON(http.StatusTooManyRequests, gin.H{"error": "Too many login attempts. Try again later."})
}

func (h *Handler) Logout(c *gin.Context) {
	revocationFailed := false
	if rawToken := accessTokenFromRequest(c); rawToken != "" {
		if claims, err := h.authService.ValidateToken(rawToken); err == nil && claims.ExpiresAt != nil {
			revoked := models.RevokedAccessToken{
				TokenHash: sha256Hex(rawToken),
				ExpiresAt: claims.ExpiresAt.Time,
			}
			if database.DB == nil || database.DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&revoked).Error != nil {
				revocationFailed = true
			}
		}
	}
	// Revoke the refresh token and clear both browser credentials.
	if cookie, err := c.Cookie("jotist_refresh_token"); err == nil {
		if err := h.revokeRefreshToken(c, cookie); err != nil {
			revocationFailed = true
		}
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     "jotist_refresh_token",
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   h.cookieSecure(c),
	})
	// Also clear access token
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     "jotist_access_token",
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   h.cookieSecure(c),
	})
	if revocationFailed {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to revoke session"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Logged out successfully"})
}

func accessTokenFromRequest(c *gin.Context) string {
	parts := strings.SplitN(c.GetHeader("Authorization"), " ", 2)
	if len(parts) == 2 && parts[0] == "Bearer" {
		return parts[1]
	}
	if cookie, err := c.Cookie("jotist_access_token"); err == nil {
		return cookie
	}
	return ""
}

func (h *Handler) GetRegistrationStatus(c *gin.Context) {
	userCount, err := h.userRepo.Count(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check registration status"})
		return
	}

	response := RegistrationStatusResponse{
		RegistrationEnabled: userCount == 0,
	}

	c.JSON(http.StatusOK, response)
}

func (h *Handler) Register(c *gin.Context) {
	// Check if any users already exist
	userCount, err := h.userRepo.Count(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check existing users"})
		return
	}

	if userCount > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "Registration is not allowed. Admin user already exists"})
		return
	}

	var req RegisterRequest
	if err := bindLimitedJSON(c, &req, maxAuthBodyBytes); err != nil {
		c.JSON(requestBodyErrorStatus(err), gin.H{"error": "Invalid request"})
		return
	}

	// Validate password confirmation
	if req.Password != req.ConfirmPassword {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Passwords do not match"})
		return
	}

	// Hash password
	hashedPassword, err := auth.HashPassword(req.Password)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to secure password"})
		return
	}

	// Create user
	user := models.User{
		Username: req.Username,
		Password: hashedPassword,
	}

	if err := h.userRepo.CreateInitialAdmin(c.Request.Context(), &user); err != nil {
		if errors.Is(err, repository.ErrInitialUserExists) {
			c.JSON(http.StatusConflict, gin.H{"error": "Registration is not allowed. Admin user already exists"})
			return
		}
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			c.JSON(http.StatusConflict, gin.H{"error": "Username already exists"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create user"})
		return
	}

	// Generate token for immediate login
	token, err := h.authService.GenerateToken(&user)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate login token"})
		return
	}
	// Set refresh token cookie
	if err := h.issueRefreshToken(c, user.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create session"})
		return
	}
	h.setAccessTokenCookie(c, token)
	response := LoginResponse{Token: token}
	response.User.ID = user.ID
	response.User.Username = user.Username

	c.JSON(http.StatusCreated, response)
}

// RefreshTokenResponse represents the refresh response
type RefreshTokenResponse struct {
	Token string `json:"token"`
}

func (h *Handler) Refresh(c *gin.Context) {
	cookie, err := c.Cookie("jotist_refresh_token")
	if err != nil || cookie == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Missing refresh token"})
		return
	}
	userID, err := h.validateAndRotateRefreshToken(c, cookie)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid refresh token"})
		return
	}
	user, err := h.userRepo.FindByID(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not found"})
		return
	}
	token, err := h.authService.GenerateToken(user)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
		return
	}

	h.setAccessTokenCookie(c, token)

	c.JSON(http.StatusOK, RefreshTokenResponse{Token: token})
}

// issueRefreshToken creates a refresh token and sets cookie
func (h *Handler) issueRefreshToken(c *gin.Context, userID uint) error {
	tokenValue, rt := newRefreshToken(userID)
	if err := h.refreshTokenRepo.Create(c.Request.Context(), &rt); err != nil {
		return err
	}
	h.setRefreshTokenCookie(c, tokenValue, rt.ExpiresAt)
	return nil
}

func newRefreshToken(userID uint) (string, models.RefreshToken) {
	tokenValue := generateSecureAPIKey(64)
	return tokenValue, models.RefreshToken{
		UserID:    userID,
		FamilyID:  uuid.NewString(),
		Hashed:    sha256Hex(tokenValue),
		ExpiresAt: time.Now().Add(14 * 24 * time.Hour),
		Revoked:   false,
	}
}

func (h *Handler) setRefreshTokenCookie(c *gin.Context, tokenValue string, expiresAt time.Time) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     "jotist_refresh_token",
		Value:    tokenValue,
		Path:     "/",
		Expires:  expiresAt,
		MaxAge:   int((14 * 24 * time.Hour).Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   h.cookieSecure(c),
	})
}

// validateAndRotateRefreshToken validates refresh token, revokes old, and issues new
func (h *Handler) validateAndRotateRefreshToken(c *gin.Context, tokenValue string) (uint, error) {
	hashed := sha256Hex(tokenValue)
	replacementValue, replacement := newRefreshToken(0)
	userID, err := h.refreshTokenRepo.Rotate(c.Request.Context(), hashed, &replacement, time.Now())
	if err != nil {
		return 0, err
	}
	h.setRefreshTokenCookie(c, replacementValue, replacement.ExpiresAt)
	return userID, nil
}

func (h *Handler) revokeRefreshToken(c *gin.Context, tokenValue string) error {
	hashed := sha256Hex(tokenValue)
	return h.refreshTokenRepo.RevokeByHash(c.Request.Context(), hashed)
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func (h *Handler) ChangePassword(c *gin.Context) {
	// Get user ID from context (set by auth middleware)
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
		return
	}

	var req ChangePasswordRequest
	if err := bindLimitedJSON(c, &req, maxAuthBodyBytes); err != nil {
		c.JSON(requestBodyErrorStatus(err), gin.H{"error": "Invalid request"})
		return
	}

	// Validate password confirmation
	if req.NewPassword != req.ConfirmPassword {
		c.JSON(http.StatusBadRequest, gin.H{"error": "New passwords do not match"})
		return
	}

	// Use UserService to change password
	if err := h.userService.ChangePassword(c.Request.Context(), userID.(uint), req.CurrentPassword, req.NewPassword); err != nil {
		if err.Error() == "incorrect password" || err.Error() == "incorrect current password" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Current password is incorrect"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update password"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Password changed successfully"})
}

func (h *Handler) ChangeUsername(c *gin.Context) {
	// Get user ID from context (set by auth middleware)
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
		return
	}

	var req ChangeUsernameRequest
	if err := bindLimitedJSON(c, &req, maxAuthBodyBytes); err != nil {
		c.JSON(requestBodyErrorStatus(err), gin.H{"error": "Invalid request"})
		return
	}
	// Use UserService to change username
	if err := h.userService.ChangeUsername(c.Request.Context(), userID.(uint), req.NewUsername, req.Password); err != nil {
		if err.Error() == "incorrect password" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Password is incorrect"})
			return
		}
		if err.Error() == "username already exists" {
			c.JSON(http.StatusConflict, gin.H{"error": "Username already exists"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update username"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Username changed successfully"})
}

func (h *Handler) ListAPIKeys(c *gin.Context) {
	apiKeys, err := h.apiKeyRepo.ListActive(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch API keys"})
		return
	}

	// Transform API keys to list response format
	var responseKeys []APIKeyListResponse
	for _, apiKey := range apiKeys {
		responseKeys = append(responseKeys, transformAPIKeyForList(apiKey))
	}

	c.JSON(http.StatusOK, APIKeysWrapper{APIKeys: responseKeys})
}

func (h *Handler) CreateAPIKey(c *gin.Context) {
	var req CreateAPIKeyRequest
	if err := bindJSON(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	// Generate a secure API key
	apiKey := generateSecureAPIKey(32)

	// Create the API key record
	newKey := models.APIKey{
		Key:         apiKey,
		Name:        req.Name,
		Description: &req.Description,
		IsActive:    true,
	}

	if err := h.apiKeyRepo.Create(c.Request.Context(), &newKey); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create API key"})
		return
	}

	// Return full model with 200 to match tests
	c.JSON(http.StatusOK, newKey)
}

func (h *Handler) DeleteAPIKey(c *gin.Context) {
	idParam := c.Param("id")
	id, err := strconv.ParseUint(idParam, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid API key ID"})
		return
	}

	// Check if the API key exists
	_, err = h.apiKeyRepo.FindByID(c.Request.Context(), uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "API key not found"})
		return
	}

	// Delete the API key (soft delete by setting is_active to false)
	if err := h.apiKeyRepo.Revoke(c.Request.Context(), uint(id)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete API key"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "API key deleted successfully"})
}

func (h *Handler) GetLLMConfig(c *gin.Context) {
	config, err := h.llmConfigRepo.GetActive(c.Request.Context())
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "No active LLM configuration found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch LLM configuration"})
		return
	}

	response := LLMConfigResponse{
		ID:            config.ID,
		Provider:      config.Provider,
		BaseURL:       config.BaseURL,
		OpenAIBaseURL: config.OpenAIBaseURL,
		HasAPIKey:     config.APIKey != nil && *config.APIKey != "",
		IsActive:      config.IsActive,
		CreatedAt:     config.CreatedAt.Format("2006-01-02 15:04:05"),
		UpdatedAt:     config.UpdatedAt.Format("2006-01-02 15:04:05"),
	}

	c.JSON(http.StatusOK, response)
}

func (h *Handler) SaveLLMConfig(c *gin.Context) {
	var req LLMConfigRequest
	if err := bindLimitedJSON(c, &req, maxAuthBodyBytes); err != nil {
		c.JSON(requestBodyErrorStatus(err), gin.H{"error": "Invalid request"})
		return
	}

	// Validate provider-specific requirements
	if req.Provider == "ollama" && (req.BaseURL == nil || *req.BaseURL == "") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Base URL is required for Ollama provider"})
		return
	}
	if req.Provider == "ollama" {
		normalized, err := normalizeProviderBaseURL(*req.BaseURL, false)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid Ollama base URL: " + err.Error()})
			return
		}
		req.BaseURL = &normalized
		req.OpenAIBaseURL = nil
	} else {
		req.BaseURL = nil
		if req.OpenAIBaseURL != nil && strings.TrimSpace(*req.OpenAIBaseURL) != "" {
			normalized, err := normalizeProviderBaseURL(*req.OpenAIBaseURL, true)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid OpenAI base URL: " + err.Error()})
				return
			}
			req.OpenAIBaseURL = &normalized
		} else {
			req.OpenAIBaseURL = nil
		}
	}

	// Check if there's an existing active configuration
	existingConfig, err := h.llmConfigRepo.GetActive(c.Request.Context())
	if err != nil && err != gorm.ErrRecordNotFound {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check existing configuration"})
		return
	}

	// Handle API Key logic for OpenAI
	var apiKeyToSave *string
	if req.Provider == "openai" {
		if req.APIKey != nil && *req.APIKey != "" {
			// New key provided
			apiKeyToSave = req.APIKey
		} else if existingConfig != nil && existingConfig.Provider == "openai" &&
			existingConfig.APIKey != nil && *existingConfig.APIKey != "" &&
			sameProviderOrigin(existingConfig.OpenAIBaseURL, req.OpenAIBaseURL) {
			// Reuse only when the credential remains bound to the same origin.
			apiKeyToSave = existingConfig.APIKey
		} else {
			// No key provided and no existing key
			c.JSON(http.StatusBadRequest, gin.H{"error": "API key is required for OpenAI provider"})
			return
		}
	}

	var config *models.LLMConfig

	if err == gorm.ErrRecordNotFound {
		// No existing active config, create new one
		config = &models.LLMConfig{
			Provider:      req.Provider,
			BaseURL:       req.BaseURL,
			OpenAIBaseURL: req.OpenAIBaseURL,
			APIKey:        apiKeyToSave,
			IsActive:      req.IsActive,
		}

		if err := h.llmConfigRepo.Create(c.Request.Context(), config); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create LLM configuration"})
			return
		}
	} else {
		// Update existing config
		existingConfig.Provider = req.Provider
		existingConfig.BaseURL = req.BaseURL
		existingConfig.OpenAIBaseURL = req.OpenAIBaseURL
		existingConfig.APIKey = apiKeyToSave
		existingConfig.IsActive = req.IsActive

		if err := h.llmConfigRepo.Update(c.Request.Context(), existingConfig); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update LLM configuration"})
			return
		}
		config = existingConfig
	}

	response := LLMConfigResponse{
		ID:            config.ID,
		Provider:      config.Provider,
		BaseURL:       config.BaseURL,
		OpenAIBaseURL: config.OpenAIBaseURL,
		HasAPIKey:     config.APIKey != nil && *config.APIKey != "",
		IsActive:      config.IsActive,
		CreatedAt:     config.CreatedAt.Format("2006-01-02 15:04:05"),
		UpdatedAt:     config.UpdatedAt.Format("2006-01-02 15:04:05"),
	}

	c.JSON(http.StatusOK, response)
}

func normalizeProviderBaseURL(rawURL string, publicOnly bool) (string, error) {
	if publicOnly {
		parsed, err := netpolicy.ValidatePublicURL(rawURL, true)
		if err != nil {
			return "", err
		}
		parsed.RawQuery = ""
		return strings.TrimRight(parsed.String(), "/"), nil
	}

	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || !parsed.IsAbs() || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return "", fmt.Errorf("must be an absolute HTTP URL without credentials or a fragment")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("must use HTTP or HTTPS")
	}
	parsed.RawQuery = ""
	return strings.TrimRight(parsed.String(), "/"), nil
}

func sameProviderOrigin(left, right *string) bool {
	origin := func(value *string) string {
		base := "https://api.openai.com/v1"
		if value != nil && strings.TrimSpace(*value) != "" {
			base = strings.TrimSpace(*value)
		}
		parsed, err := url.Parse(base)
		if err != nil {
			return ""
		}
		return strings.ToLower(parsed.Scheme + "://" + parsed.Host)
	}
	return origin(left) != "" && origin(left) == origin(right)
}

// generateSecureAPIKey generates a cryptographically secure API key
func generateSecureAPIKey(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	randomBytes := make([]byte, length)

	if _, err := rand.Read(randomBytes); err != nil {
		// Fallback to a UUID if crypto/rand fails
		return uuid.New().String()
	}

	for i := range b {
		b[i] = charset[randomBytes[i]%byte(len(charset))]
	}
	return string(b)
}

func (h *Handler) GetQueueStats(c *gin.Context) {
	stats := h.taskQueue.GetQueueStats()
	c.JSON(http.StatusOK, stats)
}

type modelCatalogResponse struct {
	Models            map[string]any  `json:"models"`
	Languages         []string        `json:"languages"`
	ExecutionDefaults platform.Target `json:"execution_defaults"`
}

func (h *Handler) GetSupportedModels(c *gin.Context) {
	c.JSON(http.StatusOK, modelCatalogResponse{
		Models:            h.unifiedProcessor.GetSupportedModels(),
		Languages:         h.unifiedProcessor.GetSupportedLanguages(),
		ExecutionDefaults: platform.Host(),
	})
}

type livenessResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Built   string `json:"built"`
}

func (h *Handler) HealthCheck(c *gin.Context) {
	c.JSON(http.StatusOK, livenessResponse{
		Status:  "healthy",
		Version: appversion.Version,
		Commit:  appversion.Commit,
		Built:   appversion.Date,
	})
}

// Helper functions
func getFormValueWithDefault(c *gin.Context, key, defaultValue string) string {
	if value := c.PostForm(key); value != "" {
		return value
	}
	return defaultValue
}

func getFormIntWithDefault(c *gin.Context, key string, defaultValue int) int {
	if value := c.PostForm(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}

func getFormFloatWithDefault(c *gin.Context, key string, defaultValue float64) float64 {
	if value := c.PostForm(key); value != "" {
		if floatValue, err := strconv.ParseFloat(value, 64); err == nil {
			return floatValue
		}
	}
	return defaultValue
}

func getFormBoolWithDefault(c *gin.Context, key string, defaultValue bool) bool {
	if value := c.PostForm(key); value != "" {
		if boolValue, err := strconv.ParseBool(value); err == nil {
			return boolValue
		}
	}
	return defaultValue
}

// Profile API Handlers

func (h *Handler) ListProfiles(c *gin.Context) {
	// TODO: Add pagination support to API if needed. For now, list all (limit 1000)
	profiles, _, err := h.profileRepo.List(c.Request.Context(), 0, 1000)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch profiles"})
		return
	}
	c.JSON(http.StatusOK, profiles)
}

func (h *Handler) CreateProfile(c *gin.Context) {
	// Seed omitted fields before decoding so explicit false/zero values remain
	// distinguishable from the legacy defaults previously supplied by GORM.
	profile := models.TranscriptionProfile{Parameters: defaultTranscriptionParams()}
	profile.Parameters.DiarizationDevice = ""
	profile.Parameters.NvidiaChunkDuration = 300
	profile.Parameters.NvidiaPrecision = "float16"
	timestamps := true
	profile.Parameters.NvidiaTimestamps = &timestamps
	if err := bindJSON(c, &profile); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request data"})
		return
	}

	if err := prepareProfileHFToken(&profile.Parameters, nil); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := validateModelRunOptions(profile.Parameters); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate required fields
	if profile.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Profile name is required"})
		return
	}

	// Check if profile name already exists
	// TODO: Add FindByName to ProfileRepository if needed, or rely on unique constraint error
	// For now, we'll skip explicit check or implement it in repository.
	// Assuming unique constraint on Name in DB or we can check via List.

	if err := h.profileRepo.Create(c.Request.Context(), &profile); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create profile"})
		return
	}

	// Tests expect 200 on create
	modelstorage.ProcessGate().FilesChanged(modelstorage.RequestUse(profile.Parameters, "", "", ""))
	c.JSON(http.StatusOK, profile)
}

func (h *Handler) GetProfile(c *gin.Context) {
	profileID := c.Param("id")

	profile, err := h.profileRepo.FindByID(c.Request.Context(), profileID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Profile not found"})
		return
	}

	c.JSON(http.StatusOK, profile)
}

type updateProfileRequest struct {
	models.TranscriptionProfile
	ExpectedRevision *int64 `json:"expected_revision" required:"false" minimum:"1"`
}

func (h *Handler) UpdateProfile(c *gin.Context) {
	profileID := c.Param("id")

	existingProfile, err := h.profileRepo.FindByID(c.Request.Context(), profileID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Profile not found"})
		return
	}

	var request updateProfileRequest
	if err := bindJSON(c, &request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request data"})
		return
	}
	updatedProfile := request.TranscriptionProfile
	// Older clients omit the revision. They retain their request behavior while
	// the revision read above still fences concurrent writes during this update.
	if request.ExpectedRevision != nil {
		if *request.ExpectedRevision < 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "expected_revision must be positive"})
			return
		}
		updatedProfile.Revision = *request.ExpectedRevision
	} else if updatedProfile.Revision < 1 {
		updatedProfile.Revision = existingProfile.Revision
	}

	if err := prepareProfileHFToken(&updatedProfile.Parameters, &existingProfile.Parameters); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := validateModelRunOptions(updatedProfile.Parameters); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate required fields
	if updatedProfile.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Profile name is required"})
		return
	}

	// Check if profile name already exists (excluding current profile)
	// TODO: Add check to repository

	// Update the profile
	// We need to preserve ID and CreatedAt, and update other fields
	// GORM Save updates all fields.
	updatedProfile.ID = existingProfile.ID
	updatedProfile.CreatedAt = existingProfile.CreatedAt
	if updatedProfile.Parameters.APIKey == nil {
		updatedProfile.Parameters.APIKey = existingProfile.Parameters.APIKey
	}

	if err := h.profileRepo.Update(c.Request.Context(), &updatedProfile); err != nil {
		if errors.Is(err, repository.ErrAdaptiveConflict) {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update profile"})
		return
	}

	modelstorage.ProcessGate().FilesChanged(modelstorage.RequestUse(updatedProfile.Parameters, "", "", ""))
	modelstorage.ProcessGate().FilesChanged(modelstorage.RequestUse(existingProfile.Parameters, "", "", ""))
	c.JSON(http.StatusOK, updatedProfile)
}

func (h *Handler) DeleteProfile(c *gin.Context) {
	profileID := c.Param("id")

	profile, err := h.profileRepo.FindByID(c.Request.Context(), profileID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Profile not found"})
		return
	}

	if err := h.profileRepo.Delete(c.Request.Context(), profileID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete profile"})
		return
	}

	modelstorage.ProcessGate().FilesChanged(modelstorage.RequestUse(profile.Parameters, "", "", ""))
	c.JSON(http.StatusOK, gin.H{"message": "Profile deleted successfully"})
}

// SetDefaultProfile sets a profile as the default profile
func (h *Handler) SetDefaultProfile(c *gin.Context) {
	profileID := c.Param("id")
	if profileID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Profile ID is required"})
		return
	}

	// Find the profile
	profile, err := h.profileRepo.FindByID(c.Request.Context(), profileID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Profile not found"})
		return
	}

	// Set this profile as default (the BeforeSave hook will handle unsetting other defaults)
	profile.IsDefault = true
	if err := h.profileRepo.Update(c.Request.Context(), profile); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to set default profile"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Default profile set successfully", "profile": profile})
}

// QuickTranscriptionRequest represents the quick transcription request
type QuickTranscriptionRequest struct {
	Parameters  *models.WhisperXParams `json:"parameters,omitempty"`
	ProfileName *string                `json:"profile_name,omitempty"`
}

func (h *Handler) SubmitQuickTranscription(c *gin.Context) {
	release, ok := h.beginMultipartUpload(c, h.config.UploadDir)
	if !ok {
		return
	}
	defer release()
	// Parse multipart form
	file, header, err := c.Request.FormFile("audio")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Audio file is required"})
		return
	}
	defer file.Close()
	if !h.acceptUploadSize(c, h.config.UploadDir, header.Size) {
		return
	}

	var params models.WhisperXParams

	// Check if profile_name was provided
	if profileName := c.PostForm("profile_name"); profileName != "" {
		// Load parameters from profile
		profile, err := h.profileRepo.FindByName(c.Request.Context(), profileName)
		if err != nil {
			if err == gorm.ErrRecordNotFound {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Profile '%s' not found", profileName)})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load profile"})
			return
		}
		params, err = h.admitSavedProfile(c, profile)
		if err != nil {
			c.JSON(http.StatusConflict, gin.H{"error": "Could not snapshot profile settings; reload the profile and retry"})
			return
		}

	} else if parametersJSON := c.PostForm("parameters"); parametersJSON != "" {
		// Parse parameters from JSON string
		if err := json.Unmarshal([]byte(parametersJSON), &params); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid parameters JSON"})
			return
		}
		clearClientLearningSnapshot(&params)
	} else {
		// Use default parameters with all required fields
		params = models.WhisperXParams{
			// Model parameters
			Model:          "small",
			ModelCacheOnly: false,

			// Device and computation
			Device:      "cpu",
			DeviceIndex: 0,
			BatchSize:   8,
			ComputeType: "float32",
			Threads:     0,

			// Output settings
			OutputFormat: "all",
			Verbose:      true,

			// Task and language
			Task: "transcribe",

			// Alignment settings
			InterpolateMethod:    "nearest",
			NoAlign:              false,
			ReturnCharAlignments: false,

			// VAD (Voice Activity Detection) settings
			VadMethod: "pyannote",
			VadOnset:  0.5,
			VadOffset: 0.363,
			ChunkSize: 30,

			// Diarization settings
			Diarize:           false,
			DiarizeModel:      "pyannote/speaker-diarization-3.1",
			SpeakerEmbeddings: false,

			// Transcription quality settings
			Temperature:                    0,
			BestOf:                         5,
			BeamSize:                       5,
			Patience:                       1.0,
			LengthPenalty:                  1.0,
			SuppressNumerals:               false,
			ConditionOnPreviousText:        false,
			Fp16:                           true,
			TemperatureIncrementOnFallback: 0.2,
			CompressionRatioThreshold:      2.4,
			LogprobThreshold:               -1.0,
			NoSpeechThreshold:              0.6,

			// Output formatting
			HighlightWords:    false,
			SegmentResolution: "sentence",
			PrintProgress:     false,
		}
	}

	if err := h.validateContextForRun(c, &params); err != nil {
		return
	}

	// Submit quick transcription job
	job, err := h.quickTranscription.SubmitQuickJob(file, header.Filename, params)
	if err != nil {
		if errors.Is(err, transcription.ErrQuickCapacity) {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "Quick transcription capacity is full"})
			return
		}
		if errors.Is(err, transcription.ErrQuickUploadTooLarge) {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "Quick transcription upload is too large"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to submit quick transcription: %v", err)})
		return
	}

	c.JSON(http.StatusOK, job)
}

func (h *Handler) GetQuickTranscriptionStatus(c *gin.Context) {
	jobID := c.Param("id")

	job, err := h.quickTranscription.GetQuickJob(jobID)
	if err != nil {
		if err.Error() == "job not found" || err.Error() == "job expired" {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get job status"})
		return
	}

	// GetQuickJob returns a clone. Preserve its string-valued API contract while
	// giving quick/CLI readers the same projection as persisted recordings.
	if job.Transcript != nil {
		if transcript, err := parseTranscriptPayload(*job.Transcript); err == nil {
			if data, err := json.Marshal(transcript); err == nil {
				value := string(data)
				job.Transcript = &value
			}
		}
	}
	c.JSON(http.StatusOK, job)
}

func (h *Handler) DownloadFromYouTube(c *gin.Context) {
	var req YouTubeDownloadRequest
	if err := bindLimitedJSON(c, &req, maxAuthBodyBytes); err != nil {
		c.JSON(requestBodyErrorStatus(err), gin.H{"error": "Invalid request"})
		return
	}

	// Validate YouTube URL before passing it to yt-dlp. Substring checks can be
	// bypassed with attacker-controlled hosts such as youtube.com.evil.test.
	if !isAllowedYouTubeURL(req.URL) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid YouTube URL"})
		return
	}

	release, err := h.resourceAdmission.tryAcquire()
	if err != nil {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "Media processing capacity is full"})
		return
	}
	defer release()
	processCtx, cancel := context.WithTimeout(c.Request.Context(), h.resourceAdmission.timeout)
	defer cancel()
	downloadLimit, releaseDisk, err := h.reserveYouTubeCapacity(h.config.UploadDir)
	if err != nil {
		c.JSON(http.StatusInsufficientStorage, gin.H{"error": err.Error()})
		return
	}
	defer releaseDisk()

	// Create upload directory
	uploadDir := h.config.UploadDir
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create upload directory"})
		return
	}

	// Generate unique job ID and filename
	jobID := uuid.New().String()
	filename := fmt.Sprintf("%s.%%(ext)s", jobID)
	filePath := filepath.Join(uploadDir, filename)

	// Get video title if not provided
	var title string
	if req.Title != nil && *req.Title != "" {
		title = *req.Title
	} else {
		// Get title first using standalone yt-dlp
		titleStart := time.Now()
		titleCtx, titleCancel := context.WithTimeout(processCtx, 30*time.Second)
		defer titleCancel()
		cmd := processutil.CommandContext(titleCtx, "yt-dlp", "--get-title", "--no-playlist", req.URL)
		out := newBoundedBuffer(maxUploadTitleLength)
		cmd.Stdout = out
		err := cmd.Run()
		if err != nil {
			title = "YouTube Audio"
			logger.Warn("Failed to get YouTube title", "url", req.URL, "error", err.Error(), "duration", time.Since(titleStart))
		} else {
			title = strings.TrimSpace(out.String())
			logger.Info("YouTube title retrieved", "title", title, "duration", time.Since(titleStart))
		}
	}

	// Download audio using yt-dlp in Python environment
	logger.Info("Starting YouTube download", "url", req.URL, "job_id", jobID)
	downloadStart := time.Now()

	// Executing yt-dlp directly (standalone binary)
	ytDlpCmd := processutil.CommandContext(processCtx, "yt-dlp",
		"--extract-audio",
		"--audio-format", "mp3",
		"--audio-quality", "0", // best quality
		"--output", filePath,
		"--no-playlist",
		"--max-filesize", strconv.FormatInt(downloadLimit, 10),
		"--no-progress",
		req.URL,
	)

	// Execute download and capture stderr for better error messages
	stderr := newBoundedBuffer(16 * 1024)
	ytDlpCmd.Stderr = stderr

	if err := ytDlpCmd.Run(); err != nil {
		stderrOutput := stderr.String()
		logger.Error("YouTube download failed",
			"url", req.URL,
			"job_id", jobID,
			"error", err.Error(),
			"stderr", stderrOutput,
			"duration", time.Since(downloadStart))

		for _, partial := range matchingDownloadedFiles(uploadDir, jobID) {
			_ = os.Remove(partial)
		}
		status := http.StatusBadGateway
		if processCtx.Err() != nil {
			status = http.StatusGatewayTimeout
		}
		c.JSON(status, gin.H{"error": "Failed to download YouTube audio"})
		return
	}

	// Find the actual downloaded file (yt-dlp changes the extension)
	pattern := fmt.Sprintf("%s.*", jobID)
	matches, err := filepath.Glob(filepath.Join(uploadDir, pattern))
	if err != nil || len(matches) == 0 {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Downloaded file not found"})
		return
	}

	actualFilePath := matches[0]

	// Get file size for performance logging
	fileInfo, err := os.Stat(actualFilePath)
	if err == nil {
		if fileInfo.Size() > downloadLimit {
			_ = os.Remove(actualFilePath)
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "Downloaded media exceeds the configured size limit"})
			return
		}
		fileSizeMB := float64(fileInfo.Size()) / 1024 / 1024
		logger.Info("YouTube download completed",
			"url", req.URL,
			"job_id", jobID,
			"file_path", actualFilePath,
			"file_size_mb", fmt.Sprintf("%.2f", fileSizeMB),
			"duration", time.Since(downloadStart))
	}

	// Create transcription record
	job := models.TranscriptionJob{
		ID:        jobID,
		AudioPath: actualFilePath,
		Status:    models.StatusUploaded,
	}

	// Set title
	if title != "" {
		job.Title = &title
	}

	// Save to database
	if err := h.jobRepo.Create(c.Request.Context(), &job); err != nil {
		// Clean up downloaded file on database error
		os.Remove(actualFilePath)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save transcription record"})
		return
	}

	c.JSON(http.StatusOK, job)
}

func matchingDownloadedFiles(uploadDir, jobID string) []string {
	matches, _ := filepath.Glob(filepath.Join(uploadDir, jobID+".*"))
	return matches
}

func isAllowedYouTubeURL(rawURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || !parsed.IsAbs() || parsed.User != nil {
		return false
	}

	if parsed.Scheme != "https" {
		return false
	}

	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	return host == "youtu.be" || host == "youtube.com" || strings.HasSuffix(host, ".youtube.com")
}

func (h *Handler) GetUserDefaultProfile(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
		return
	}

	// Get user with default profile ID
	user, err := h.userRepo.FindByID(c.Request.Context(), userID.(uint))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get user"})
		return
	}

	// If user has no default profile set, return the first available profile or no profile
	if user.DefaultProfileID == nil {
		// Try to find a default profile from profiles table
		profile, err := h.profileRepo.FindDefault(c.Request.Context())
		if err == nil {
			c.JSON(http.StatusOK, profile)
			return
		}

		// If no default marked, get first one
		profiles, _, err := h.profileRepo.List(c.Request.Context(), 0, 1)
		if err != nil || len(profiles) == 0 {
			c.JSON(http.StatusNotFound, gin.H{"error": "No profiles available"})
			return
		}
		c.JSON(http.StatusOK, profiles[0])
		return
	}

	// Get the user's default profile
	profile, err := h.profileRepo.FindByID(c.Request.Context(), *user.DefaultProfileID)
	if err != nil {
		// Default profile no longer exists, fall back to first available
		profiles, _, err := h.profileRepo.List(c.Request.Context(), 0, 1)
		if err != nil || len(profiles) == 0 {
			c.JSON(http.StatusNotFound, gin.H{"error": "No profiles available"})
			return
		}
		c.JSON(http.StatusOK, profiles[0])
		return
	}

	c.JSON(http.StatusOK, profile)
}

// SetUserDefaultProfileRequest represents the request to set user's default profile
type SetUserDefaultProfileRequest struct {
	ProfileID string `json:"profile_id" binding:"required"`
}

func (h *Handler) SetUserDefaultProfile(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
		return
	}

	var req SetUserDefaultProfileRequest
	if err := bindJSON(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	// Verify the profile exists
	_, err := h.profileRepo.FindByID(c.Request.Context(), req.ProfileID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Profile not found"})
		return
	}

	// Get user
	user, err := h.userRepo.FindByID(c.Request.Context(), userID.(uint))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get user"})
		return
	}

	// Update user's default profile
	user.DefaultProfileID = &req.ProfileID
	if err := h.userRepo.Update(c.Request.Context(), user); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to set default profile"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Default profile set successfully", "profile_id": req.ProfileID})
}

// UserSettingsResponse represents the user's settings
type UserSettingsResponse struct {
	ExecutionPolicy           models.ExecutionPolicy `json:"execution_policy"`
	AutoTranscriptionEnabled  bool                   `json:"auto_transcription_enabled"`
	DefaultProfileID          *string                `json:"default_profile_id,omitempty"`
	TranscriptionContext      string                 `json:"transcription_context"`
	TranscriptionContextTerms string                 `json:"transcription_context_terms"`
	HasHFToken                bool                   `json:"has_hf_token"`
}

// UpdateUserSettingsRequest represents the request to update user settings
type UpdateUserSettingsRequest struct {
	ExecutionPolicy           *models.ExecutionPolicy `json:"execution_policy,omitempty"`
	AutoTranscriptionEnabled  *bool                   `json:"auto_transcription_enabled,omitempty"`
	TranscriptionContext      *string                 `json:"transcription_context,omitempty"`
	TranscriptionContextTerms *string                 `json:"transcription_context_terms,omitempty"`
	// Write-only: omitted/null preserves the saved token; an empty string clears it.
	HFToken *string `json:"hf_token,omitempty"`
}

func (h *Handler) GetUserSettings(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
		return
	}

	user, err := h.userRepo.FindByID(c.Request.Context(), userID.(uint))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get user"})
		return
	}

	response := UserSettingsResponse{
		ExecutionPolicy:           user.EffectiveExecutionPolicy(),
		AutoTranscriptionEnabled:  user.AutoTranscriptionEnabled,
		DefaultProfileID:          user.DefaultProfileID,
		TranscriptionContext:      user.TranscriptionContext,
		TranscriptionContextTerms: user.TranscriptionContextTerms,
		HasHFToken:                user.HFToken != "",
	}

	c.JSON(http.StatusOK, response)
}

func (h *Handler) UpdateUserSettings(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
		return
	}

	var req UpdateUserSettingsRequest
	if err := bindJSON(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	user, err := h.userRepo.FindByID(c.Request.Context(), userID.(uint))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get user"})
		return
	}

	// Update fields if provided
	fields := []string{}
	if req.ExecutionPolicy != nil {
		if err := transcription.ValidateExecutionPolicy(*req.ExecutionPolicy); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		user.ExecutionPolicy = req.ExecutionPolicy
		fields = append(fields, "execution_policy")
	}
	if err := validateContext(req.TranscriptionContext, req.TranscriptionContextTerms); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := validateHFToken(req.HFToken); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.HFToken != nil {
		user.HFToken = strings.TrimSpace(*req.HFToken)
		fields = append(fields, "hf_token")
	}
	if req.TranscriptionContext != nil {
		user.TranscriptionContext = strings.TrimSpace(*req.TranscriptionContext)
		fields = append(fields, "transcription_context")
	}
	if req.TranscriptionContextTerms != nil {
		user.TranscriptionContextTerms = strings.TrimSpace(*req.TranscriptionContextTerms)
		fields = append(fields, "transcription_context_terms")
	}
	if req.AutoTranscriptionEnabled != nil {
		user.AutoTranscriptionEnabled = *req.AutoTranscriptionEnabled
		fields = append(fields, "auto_transcription_enabled")
	}

	// Save updated user
	if err := h.userRepo.UpdateSettings(c.Request.Context(), user, fields); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update settings"})
		return
	}

	response := UserSettingsResponse{
		ExecutionPolicy:           user.EffectiveExecutionPolicy(),
		AutoTranscriptionEnabled:  user.AutoTranscriptionEnabled,
		DefaultProfileID:          user.DefaultProfileID,
		TranscriptionContext:      user.TranscriptionContext,
		TranscriptionContextTerms: user.TranscriptionContextTerms,
		HasHFToken:                user.HFToken != "",
	}

	c.JSON(http.StatusOK, response)
}

func (h *Handler) Events(c *gin.Context) {
	h.broadcaster.ServeHTTP(c.Writer, c.Request)
}
