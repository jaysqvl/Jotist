package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"scriberr/internal/audio"
	"scriberr/internal/database"
	"scriberr/internal/models"
	"scriberr/internal/repository"
	"scriberr/internal/transcription"
	"scriberr/internal/transcription/adapters"
	"scriberr/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type assembledUploadFile struct {
	ID           string
	Role         models.UploadFileRole
	OriginalName string
	ContentType  string
	Path         string
	Size         int64
}

func (h *Handler) finalizeAssembledUpload(c *gin.Context, session *models.UploadSession, files []assembledUploadFile) (interface{}, error) {
	title := ""
	if session.Title != nil {
		title = *session.Title
	}

	switch session.Kind {
	case models.UploadKindAudio:
		file, err := singleAssembledFile(files, models.UploadFileRoleAudio)
		if err != nil {
			return nil, err
		}
		path, err := h.moveAssembledToUpload(file)
		if err != nil {
			return nil, err
		}
		job, err := h.createUploadedAudioJob(c, path, title, session.ID)
		if err != nil {
			return nil, err
		}
		return job, nil
	case models.UploadKindVideo:
		file, err := singleAssembledFile(files, models.UploadFileRoleVideo)
		if err != nil {
			return nil, err
		}
		path, err := h.moveAssembledToUpload(file)
		if err != nil {
			return nil, err
		}
		job, err := h.createUploadedVideoJob(c, path, title, session.ID)
		if err != nil {
			return nil, err
		}
		return job, nil
	case models.UploadKindQuick:
		file, err := singleAssembledFile(files, models.UploadFileRoleAudio)
		if err != nil {
			return nil, err
		}
		params, err := h.quickParamsForUploadSession(c, session)
		if err != nil {
			return nil, err
		}
		if err := h.resolveTranscriptionContext(c, &params); err != nil {
			return nil, err
		}
		if err := validateModelRunOptions(params); err != nil {
			return nil, err
		}
		quickJob, err := h.createQuickTranscriptionFromPath(c.Request.Context(), file.Path, file.OriginalName, params, session.ID)
		if err != nil {
			return nil, err
		}
		return quickJob, nil
	case models.UploadKindSubmit:
		file, err := singleAssembledFile(files, models.UploadFileRoleAudio)
		if err != nil {
			return nil, err
		}
		params, err := submitParamsFromJSON(session.ParametersJSON)
		if err != nil {
			return nil, invalidUploadParametersError{err}
		}
		if err := validateModelRunOptions(params); err != nil {
			return nil, invalidUploadParametersError{err}
		}
		if err := h.resolveTranscriptionContext(c, &params); err != nil {
			return nil, err
		}
		path, err := h.moveAssembledToUpload(file)
		if err != nil {
			return nil, err
		}
		job, err := h.createSubmittedJobWithParams(c, path, title, params, session.ID)
		if err != nil {
			return nil, err
		}
		return job, nil
	case models.UploadKindMultiTrack:
		aup, err := singleAssembledFile(files, models.UploadFileRoleAup)
		if err != nil {
			return nil, err
		}
		tracks := filesByRole(files, models.UploadFileRoleTrack)
		if len(tracks) == 0 {
			return nil, fmt.Errorf("Multi-track upload has no tracks")
		}
		job, err := h.createMultiTrackJobFromPaths(c, title, aup, tracks, session.ID)
		if err != nil {
			return nil, err
		}
		return job, nil
	default:
		return nil, fmt.Errorf("Unsupported upload kind")
	}
}

func (h *Handler) respondWithCompletedUpload(c *gin.Context, session *models.UploadSession) {
	switch {
	case session.ResultType != nil && *session.ResultType == "quick" && session.ResultID != nil:
		job, err := h.quickTranscription.GetQuickJob(*session.ResultID)
		if err == nil {
			c.JSON(http.StatusOK, job)
			return
		}
	case session.ResultID != nil:
		job, err := h.jobRepo.FindWithAssociations(c.Request.Context(), *session.ResultID)
		if err == nil {
			c.JSON(http.StatusOK, job)
			return
		}
	}
	c.JSON(http.StatusOK, buildUploadSessionResponse(*session, ""))
}

// persistUploadedJob binds resumable sessions in the same transaction as the
// recording. Multipart uploads retain their existing repository operation.
func (h *Handler) persistUploadedJob(ctx context.Context, job *models.TranscriptionJob, sessionID string) error {
	if sessionID == "" {
		return h.jobRepo.Create(ctx, job)
	}
	return repository.CommitUploadResult(ctx, database.DB, sessionID, job)
}

func (h *Handler) createUploadedAudioJob(c *gin.Context, filePath, title, sessionID string) (*models.TranscriptionJob, error) {
	finalPath, err := h.convertWebMToMP3IfNeeded(c.Request.Context(), filePath)
	if err != nil {
		_ = h.fileService.RemoveFile(filePath)
		return nil, err
	}

	jobID := filenameWithoutExt(finalPath)
	job := models.TranscriptionJob{
		ID:        jobID,
		AudioPath: finalPath,
		Status:    models.StatusUploaded,
	}
	if strings.TrimSpace(title) != "" {
		job.Title = stringPtr(strings.TrimSpace(title))
	}

	if err := h.persistUploadedJob(c.Request.Context(), &job, sessionID); err != nil {
		_ = h.fileService.RemoveFile(finalPath)
		return nil, fmt.Errorf("Failed to create job")
	}

	h.applyAutoTranscription(c, &job)
	return &job, nil
}

func (h *Handler) createUploadedVideoJob(c *gin.Context, videoPath, title, sessionID string) (*models.TranscriptionJob, error) {
	jobID := filenameWithoutExt(videoPath)
	audioPath := strings.TrimSuffix(videoPath, filepath.Ext(videoPath)) + ".mp3"

	if err := h.runMediaCommand(c.Request.Context(), "ffmpeg", "-i", videoPath, "-vn", "-acodec", "libmp3lame", "-q:a", "2", audioPath); err != nil {
		_ = h.fileService.RemoveFile(videoPath)
		return nil, fmt.Errorf("Failed to extract audio from video")
	}

	job := models.TranscriptionJob{
		ID:        jobID,
		AudioPath: audioPath,
		Status:    models.StatusUploaded,
	}
	if strings.TrimSpace(title) != "" {
		job.Title = stringPtr(strings.TrimSpace(title))
	}

	if err := h.persistUploadedJob(c.Request.Context(), &job, sessionID); err != nil {
		_ = h.fileService.RemoveFile(videoPath)
		_ = h.fileService.RemoveFile(audioPath)
		return nil, fmt.Errorf("Failed to create job")
	}

	_ = h.fileService.RemoveFile(videoPath)
	h.applyAutoTranscription(c, &job)
	return &job, nil
}

type invalidUploadParametersError struct{ error }

func submitModelDefaults(family string) (precision, model string) {
	if family == "vibevoice-bitnet" {
		return "i2_s+i8_s", "vibevoice-bitnet"
	}
	for _, spec := range adapters.LocalASRModels() {
		if family == spec.Family || family == spec.ID {
			return "float32", spec.ID
		}
	}
	return "int8", "base"
}

func submitParamsFromJSON(parametersJSON *string) (models.WhisperXParams, error) {
	params := defaultSubmitParams()
	if parametersJSON != nil && strings.TrimSpace(*parametersJSON) != "" {
		var route struct {
			ModelFamily string `json:"model_family"`
		}
		if err := json.Unmarshal([]byte(*parametersJSON), &route); err != nil {
			return params, fmt.Errorf("Invalid parameters JSON")
		}
		params.ComputeType, params.Model = submitModelDefaults(route.ModelFamily)
		if err := json.Unmarshal([]byte(*parametersJSON), &params); err != nil {
			return params, fmt.Errorf("Invalid parameters JSON")
		}
	}
	clearClientLearningSnapshot(&params)
	return params, nil
}

func (h *Handler) createSubmittedJobWithParams(c *gin.Context, filePath, title string, params models.WhisperXParams, sessionID string) (*models.TranscriptionJob, error) {
	clearClientLearningSnapshot(&params)
	if err := validateModelRunOptions(params); err != nil {
		return nil, err
	}
	if err := h.resolveTranscriptionContext(c, &params); err != nil {
		return nil, err
	}
	jobID := filenameWithoutExt(filePath)
	job := models.TranscriptionJob{
		ID:          jobID,
		AudioPath:   filePath,
		Status:      models.StatusPending,
		Diarization: params.Diarize,
		Parameters:  params,
	}
	if strings.TrimSpace(title) != "" {
		job.Title = stringPtr(strings.TrimSpace(title))
	}

	if err := h.persistUploadedJob(c.Request.Context(), &job, sessionID); err != nil {
		_ = h.fileService.RemoveFile(filePath)
		return nil, fmt.Errorf("Failed to create job")
	}
	if err := h.taskQueue.EnqueueJob(jobID); err != nil {
		if sessionID == "" {
			return nil, fmt.Errorf("Failed to enqueue job")
		}
		// The session and pending job already committed. Startup recovery or
		// the durable reconciler retries dispatch when capacity is available.
		logger.Warn("Uploaded job persisted for later dispatch", "job_id", jobID, "error", err)
	}
	return &job, nil
}

func submitParamsFromForm(c *gin.Context) (models.WhisperXParams, error) {
	diarize := false
	if v := c.PostForm("diarization"); v != "" {
		diarize = strings.EqualFold(v, "true") || v == "1"
	} else {
		diarize = getFormBoolWithDefault(c, "diarize", false)
	}

	family := getFormValueWithDefault(c, "model_family", "whisper")
	defaultPrecision, defaultModel := submitModelDefaults(family)
	params := models.WhisperXParams{
		ModelFamily:           family,
		Model:                 getFormValueWithDefault(c, "model", defaultModel),
		BatchSize:             getFormIntWithDefault(c, "batch_size", 16),
		ComputeType:           getFormValueWithDefault(c, "compute_type", defaultPrecision),
		Device:                getFormValueWithDefault(c, "device", "cpu"),
		VadOnset:              getFormFloatWithDefault(c, "vad_onset", 0.500),
		VadOffset:             getFormFloatWithDefault(c, "vad_offset", 0.363),
		Diarize:               diarize,
		DiarizationDevice:     getFormValueWithDefault(c, "diarization_device", "same"),
		DiarizationCheckpoint: c.PostForm("diarization_checkpoint"),
		HFTokenSource:         c.PostForm("hf_token_source"),
	}
	if value, exists := c.GetPostForm("audio_chunk_duration"); exists {
		seconds, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return models.WhisperXParams{}, fmt.Errorf("audio_chunk_duration must be an integer number of seconds")
		}
		params.AudioChunkDuration = &seconds
	}
	if value, exists := c.GetPostForm("transcription_context"); exists {
		params.TranscriptionContext = &value
	}
	if value, exists := c.GetPostForm("transcription_context_terms"); exists {
		params.TranscriptionContextTerms = &value
	}

	if lang := c.PostForm("language"); lang != "" {
		params.Language = &lang
	}

	if minSpeakers := c.PostForm("min_speakers"); minSpeakers != "" {
		if min, err := strconv.Atoi(minSpeakers); err == nil {
			params.MinSpeakers = &min
		}
	}

	if maxSpeakers := c.PostForm("max_speakers"); maxSpeakers != "" {
		if max, err := strconv.Atoi(maxSpeakers); err == nil {
			params.MaxSpeakers = &max
		}
	}

	if hfToken, exists := c.GetPostForm("hf_token"); exists {
		params.HfToken = &hfToken
	}

	diarizeModel := getFormValueWithDefault(c, "diarize_model", "pyannote")
	if diarizeModel != "pyannote" && diarizeModel != transcription.ModelDiarization31 && diarizeModel != "pyannote/speaker-diarization-community-1" && diarizeModel != "nvidia_sortformer" && diarizeModel != "diarizen" && diarizeModel != "suplime" && diarizeModel != "native" {
		return models.WhisperXParams{}, fmt.Errorf("Invalid diarize_model")
	}
	params.DiarizeModel = diarizeModel

	return params, nil
}

func (h *Handler) createMultiTrackJobFromPaths(c *gin.Context, title string, aup assembledUploadFile, tracks []assembledUploadFile, sessionID string) (*models.TranscriptionJob, error) {
	jobID := uuidString()
	jobDir := filepath.Join(h.config.UploadDir, jobID)
	if err := h.fileService.CreateDirectory(jobDir); err != nil {
		return nil, fmt.Errorf("Failed to create job directory")
	}

	aupPath := filepath.Join(jobDir, "project-"+safeFilename(aup.OriginalName))
	if err := moveOrCopyFile(aup.Path, aupPath); err != nil {
		_ = h.fileService.RemoveDirectory(jobDir)
		return nil, fmt.Errorf("Failed to save AUP file")
	}

	trackInfoByName := map[string]audio.AupTrack{}
	if parsedTracks, err := audio.NewAupParser().ParseAupFile(aupPath); err == nil {
		for _, parsedTrack := range parsedTracks {
			trackInfoByName[filepath.Base(parsedTrack.Filename)] = parsedTrack
		}
	}

	trackFiles := make([]models.MultiTrackFile, 0, len(tracks))
	usedNames := map[string]int{}
	for i, track := range tracks {
		originalBase := filepath.Base(track.OriginalName)
		safeBase := uniqueSafeFilename(originalBase, usedNames)
		trackPath := filepath.Join(jobDir, safeBase)
		if err := moveOrCopyFile(track.Path, trackPath); err != nil {
			_ = h.fileService.RemoveDirectory(jobDir)
			return nil, fmt.Errorf("Failed to save track %s", originalBase)
		}

		trackFile := models.MultiTrackFile{
			TranscriptionJobID: jobID,
			FilePath:           trackPath,
			FileName:           originalBase,
			TrackIndex:         i,
			Offset:             0,
			Gain:               1,
			Pan:                0,
			Mute:               false,
		}
		if parsedTrack, ok := trackInfoByName[originalBase]; ok {
			trackFile.Offset = parsedTrack.Offset
			trackFile.Gain = parsedTrack.Gain
			trackFile.Pan = parsedTrack.Pan
			trackFile.Mute = parsedTrack.Mute == 1
		}
		trackFiles = append(trackFiles, trackFile)
	}

	jobTitle := strings.TrimSpace(title)
	if jobTitle == "" {
		jobTitle = fmt.Sprintf("Multi-track Job %s", jobID)
	}
	mergeStatus := "pending"
	job := models.TranscriptionJob{
		ID:               jobID,
		Title:            &jobTitle,
		AudioPath:        filepath.Join(jobDir, "merged.mp3"),
		Status:           models.StatusUploaded,
		IsMultiTrack:     true,
		AupFilePath:      &aupPath,
		MultiTrackFolder: &jobDir,
		MergeStatus:      mergeStatus,
		MultiTrackFiles:  trackFiles,
	}

	if err := h.persistUploadedJob(c.Request.Context(), &job, sessionID); err != nil {
		_ = h.fileService.RemoveDirectory(jobDir)
		return nil, fmt.Errorf("Failed to create job")
	}
	return &job, nil
}

func (h *Handler) applyAutoTranscription(c *gin.Context, job *models.TranscriptionJob) {
	userID, exists := c.Get("user_id")
	if !exists {
		return
	}
	user, err := h.userService.GetUser(c.Request.Context(), userID.(uint))
	if err != nil || !user.AutoTranscriptionEnabled {
		return
	}

	var profile *models.TranscriptionProfile
	if user.DefaultProfileID != nil {
		profile, _ = h.profileRepo.FindByID(c.Request.Context(), *user.DefaultProfileID)
	}
	if profile == nil {
		profile, _ = h.profileRepo.FindDefault(c.Request.Context())
	}
	if profile == nil {
		profiles, _, _ := h.profileRepo.List(c.Request.Context(), 0, 1)
		if len(profiles) > 0 {
			profile = &profiles[0]
		}
	}
	if profile == nil {
		return
	}

	params, err := h.admitSavedProfile(c, profile)
	if err != nil {
		return
	}
	job.Parameters = params
	job.Diarization = profile.Parameters.Diarize
	job.Status = models.StatusPending
	if err := h.jobRepo.Update(c.Request.Context(), job); err == nil {
		if err := h.taskQueue.EnqueueJob(job.ID); err != nil {
			job.Status = models.StatusUploaded
			_ = h.jobRepo.Update(c.Request.Context(), job)
		}
	}
}

func (h *Handler) convertWebMToMP3IfNeeded(ctx context.Context, filePath string) (string, error) {
	if strings.ToLower(filepath.Ext(filePath)) != ".webm" {
		return filePath, nil
	}

	mp3Path := strings.TrimSuffix(filePath, filepath.Ext(filePath)) + ".mp3"
	if err := h.runMediaCommand(ctx, "ffmpeg", "-i", filePath, "-vn", "-af", "loudnorm", "-acodec", "libmp3lame", "-b:a", "320k", mp3Path); err != nil {
		return "", fmt.Errorf("Failed to convert WebM audio to MP3")
	}
	_ = h.fileService.RemoveFile(filePath)
	return mp3Path, nil
}

func (h *Handler) quickParamsForUploadSession(c *gin.Context, session *models.UploadSession) (models.WhisperXParams, error) {
	if session.ProfileName != nil && strings.TrimSpace(*session.ProfileName) != "" {
		profile, err := h.profileRepo.FindByName(c.Request.Context(), strings.TrimSpace(*session.ProfileName))
		if err != nil {
			return models.WhisperXParams{}, fmt.Errorf("Profile %q not found", *session.ProfileName)
		}
		return h.admitSavedProfile(c, profile)
	}
	params := defaultQuickTranscriptionParams()
	if session.ParametersJSON != nil && strings.TrimSpace(*session.ParametersJSON) != "" {
		if err := json.Unmarshal([]byte(*session.ParametersJSON), &params); err != nil {
			return models.WhisperXParams{}, fmt.Errorf("Invalid parameters JSON")
		}
	}
	clearClientLearningSnapshot(&params)
	return params, nil
}

func (h *Handler) createQuickTranscriptionFromPath(ctx context.Context, path, filename string, params models.WhisperXParams, sessionID string) (*transcription.QuickTranscriptionJob, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("Failed to open uploaded audio")
	}
	defer file.Close()

	job, err := h.quickTranscription.SubmitQuickJobWithCommit(file, filename, params, func(jobID string) error {
		if err := repository.CommitQuickUploadResult(ctx, database.DB, sessionID, jobID); err != nil {
			logger.Error("Failed to commit quick upload result", "session_id", sessionID, "error", err)
			return fmt.Errorf("Failed to complete upload session")
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("Failed to submit quick transcription: %w", err)
	}
	return job, nil
}

func (h *Handler) moveAssembledToUpload(file assembledUploadFile) (string, error) {
	if err := h.fileService.CreateDirectory(h.config.UploadDir); err != nil {
		return "", fmt.Errorf("Failed to create upload directory")
	}
	ext := filepath.Ext(file.OriginalName)
	if ext == "" {
		ext = filepath.Ext(file.Path)
	}
	dest := filepath.Join(h.config.UploadDir, uuidString()+ext)
	if err := moveOrCopyFile(file.Path, dest); err != nil {
		return "", fmt.Errorf("Failed to save uploaded file")
	}
	return dest, nil
}

func saveMultipartFileToPath(fileHeader *multipart.FileHeader, destPath string) error {
	src, err := fileHeader.Open()
	if err != nil {
		return err
	}
	defer src.Close()

	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return err
	}
	dst, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer dst.Close()

	_, err = io.Copy(dst, src)
	return err
}

func moveOrCopyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Remove(src)
}

func singleAssembledFile(files []assembledUploadFile, role models.UploadFileRole) (assembledUploadFile, error) {
	matches := filesByRole(files, role)
	if len(matches) != 1 {
		return assembledUploadFile{}, fmt.Errorf("Expected exactly one %s file", role)
	}
	return matches[0], nil
}

func filesByRole(files []assembledUploadFile, role models.UploadFileRole) []assembledUploadFile {
	var matches []assembledUploadFile
	for _, file := range files {
		if file.Role == role {
			matches = append(matches, file)
		}
	}
	return matches
}

func defaultSubmitParams() models.WhisperXParams {
	return models.WhisperXParams{
		Model:        "base",
		BatchSize:    16,
		ComputeType:  "int8",
		Device:       "cpu",
		VadOnset:     0.500,
		VadOffset:    0.363,
		Diarize:      false,
		DiarizeModel: "pyannote",
	}
}

func defaultQuickTranscriptionParams() models.WhisperXParams {
	return models.WhisperXParams{
		Model:             "small",
		Device:            "cpu",
		DeviceIndex:       0,
		BatchSize:         8,
		ComputeType:       "float32",
		OutputFormat:      "all",
		Verbose:           true,
		Task:              "transcribe",
		InterpolateMethod: "nearest",
		VadMethod:         "pyannote",
		VadOnset:          0.5,
		VadOffset:         0.363,
		ChunkSize:         30,
		Diarize:           false,
		DiarizeModel:      "pyannote/speaker-diarization-3.1",
		Temperature:       0,
		BestOf:            5,
		BeamSize:          5,
		Patience:          1.0,
		LengthPenalty:     1.0,
		Fp16:              true,
		SegmentResolution: "sentence",
	}
}

func filenameWithoutExt(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func stringPtr(value string) *string {
	return &value
}

func safeFilename(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	if name == "." || name == "" {
		return "file"
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.', r == '-', r == '_', r == ' ':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	clean := strings.TrimSpace(b.String())
	if clean == "" {
		return "file"
	}
	return clean
}

func uniqueSafeFilename(name string, used map[string]int) string {
	safe := safeFilename(name)
	count := used[safe]
	used[safe] = count + 1
	if count == 0 {
		return safe
	}
	ext := filepath.Ext(safe)
	stem := strings.TrimSuffix(safe, ext)
	return stem + "-" + strconv.Itoa(count+1) + ext
}

func uuidString() string {
	return uuid.New().String()
}
