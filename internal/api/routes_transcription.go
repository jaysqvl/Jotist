package api

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jaysqvl/Jotist/internal/auth"
	"github.com/jaysqvl/Jotist/internal/models"
	transcriptionapi "github.com/jaysqvl/Jotist/internal/transcription"
)

func registerTranscriptionRoutes(v1 routeGroup, handler *Handler, authService *auth.AuthService) {
	spec := v1.spec
	// Transcription routes (require authentication)
	// Apply the cache policy before authentication so denied requests also
	// carry it. These routes are also used in the signed-in API reference.
	privateReads := v1.group("/transcription", privateNoStore).authenticated("", authService)
	privateReads.Use(handler.syncBrowserAccessCookie)
	privateReads.route(http.MethodGet, "/models", handler.GetSupportedModels, huma.Operation{
		OperationID: "get-supported-models",
		Summary:     "Get supported models",
		Description: "List recognition, word-timing and diarization models, languages and execution defaults.",
		Tags:        []string{"transcription"},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[modelCatalogResponse](spec, "OK"),
		},
	})
	privateReads.route(http.MethodGet, "/list", handler.ListTranscriptionJobs, huma.Operation{
		OperationID: "list-transcription-jobs",
		Summary:     "List all transcription records",
		Description: "List recordings with pagination and optional search.\nExample response fields `{\"jobs\":[{\"id\":\"RECORDING_ID\",\"title\":\"Library opening\",\"status\":\"completed\"}],\"pagination\":{\"page\":1,\"limit\":10,\"total\":1,\"pages\":1}}`",
		Tags:        []string{"transcription"},
		Parameters: []*huma.Param{
			{In: "query", Name: "page", Description: "Page number", Required: false, Schema: &huma.Schema{Type: "integer", Default: 1}},
			{In: "query", Name: "limit", Description: "Items per page", Required: false, Schema: &huma.Schema{Type: "integer", Default: 10}},
			stringParam("query", "sort_by", "Sort By", false),
			stringParam("query", "sort_order", "Sort Order (asc/desc)", false),
			stringParam("query", "q", "Search in title and audio filename", false),
			stringParam("query", "updated_after", "Filter by updated_at > timestamp (RFC3339)", false),
		},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[recordingListResponse](spec, "OK"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	privateReads.route(http.MethodGet, "/{id}/status", handler.GetJobStatus, huma.Operation{
		OperationID: "get-job-status",
		Summary:     "Get job status",
		Description: "Get the current status of a transcription job\nExample response fields `{\"id\":\"RECORDING_ID\",\"title\":\"Library opening\",\"status\":\"completed\"}`",
		Tags:        []string{"transcription"},
		Parameters: []*huma.Param{
			stringParam("path", "id", "Job ID", true),
		},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[models.TranscriptionJob](spec, "OK"),
			"404": errorResponse("Not Found"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	privateReads.route(http.MethodGet, "/{id}/transcript", handler.GetTranscript, huma.Operation{
		OperationID: "get-transcript",
		Summary:     "Get transcript",
		Description: "Get the transcript for a completed transcription job\nExample response fields `{\"job_id\":\"RECORDING_ID\",\"status\":\"completed\",\"available\":true,\"transcript\":{\"text\":\"The library opens at nine.\"}}`\nIf a transcript is unavailable, the response contains `\"available\":false` and `\"transcript\":null`.",
		Tags:        []string{"transcription"},
		Parameters: []*huma.Param{
			stringParam("path", "id", "Job ID", true),
			{In: "query", Name: "read_only", Description: "Read the saved transcript without backfilling legacy run history", Required: false, Schema: &huma.Schema{Type: "boolean"}},
		},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[transcriptResponse](spec, "OK"),
			"400": errorResponse("Bad Request"),
			"404": errorResponse("Not Found"),
			"500": errorResponse("Internal Server Error"),
		},
	})

	transcription := v1.authenticated("/transcription", authService)
	transcription.Use(handler.syncBrowserAccessCookie)
	registerUploadRoutes(transcription, handler)

	// Regular API routes with compression
	transcription.route(http.MethodPost, "/youtube", handler.DownloadFromYouTube, huma.Operation{
		OperationID: "download-from-you-tube",
		Summary:     "Download audio from YouTube URL",
		Description: "Download audio from a YouTube video URL and prepare it for transcription",
		Tags:        []string{"transcription"},
		RequestBody: requestBody("application/json", schemaOf[YouTubeDownloadRequest](spec), true, "YouTube download request"),
		Responses: map[string]*huma.Response{
			"200": jsonResponse[models.TranscriptionJob](spec, "OK"),
			"400": errorResponse("Bad Request"),
			"413": errorResponse("Request Entity Too Large"),
			"429": errorResponse("Too Many Requests"),
			"500": errorResponse("Internal Server Error"),
			"507": errorResponse("Insufficient Storage"),
		},
	})
	transcription.route(http.MethodPost, "/submit", handler.SubmitJob, huma.Operation{
		OperationID: "submit-job",
		Summary:     "Submit a transcription job",
		Description: "Submit an audio file using a supported transcription model. Omitted context fields inherit the authenticated user's defaults; empty fields explicitly disable those hints.",
		Tags:        []string{"transcription"},
		RequestBody: requestBody("multipart/form-data", &huma.Schema{Type: "object", Required: []string{"audio"}, Properties: map[string]*huma.Schema{
			"audio":                       {Type: "string", Format: "binary", Description: "Audio file"},
			"audio_chunk_duration":        {Type: "integer", Description: "Audio chunk duration in seconds; omit for the model default, 0 requests full-recording context where supported"},
			"batch_size":                  {Type: "integer", Description: "Batch size", Default: 16},
			"compute_type":                {Type: "string", Description: "Compute precision; Whisper defaults to int8, local ASR to float32, and BitNet uses fixed quantized weights"},
			"device":                      {Type: "string", Description: "Transcription device; supported choices depend on the model", Default: "cpu"},
			"diarization":                 {Type: "boolean", Description: "Enable speaker diarization"},
			"diarization_checkpoint":      {Type: "string", Description: "Optional research diarizer checkpoint, such as rewayai/suplime or rewayai/suplime-large"},
			"diarization_device":          {Type: "string", Description: "Device used for speaker diarization; same follows the transcription device", Default: "same", Enum: []any{"same", "cpu", "cuda", "auto"}},
			"diarize_model":               {Type: "string", Description: "Speaker diarization model; native requires an integrated diarizer", Default: "pyannote", Enum: []any{"pyannote", "nvidia_sortformer", "diarizen", "suplime", "native"}},
			"language":                    {Type: "string", Description: "Language code"},
			"max_speakers":                {Type: "integer", Description: "Maximum speakers for diarization"},
			"min_speakers":                {Type: "integer", Description: "Minimum speakers for diarization"},
			"model":                       {Type: "string", Description: "Checkpoint or Whisper model size; defaults to base for Whisper and the family default for local models"},
			"model_family":                {Type: "string", Description: "Model family from /transcription/models", Default: "whisper"},
			"title":                       {Type: "string", Description: "Job title"},
			"transcription_context":       {Type: "string", Description: "Meeting context, at most 4000 characters; omitted inherits and empty disables"},
			"transcription_context_terms": {Type: "string", Description: "Vocabulary, one term per line, at most 8000 characters; omitted inherits and empty disables"},
			"vad_filter":                  {Type: "boolean", Description: "Enable VAD filter"},
			"vad_offset":                  {Type: "number", Description: "VAD offset", Default: 0.363},
			"vad_onset":                   {Type: "number", Description: "VAD onset", Default: 0.5},
		}}, true, ""),
		Responses: map[string]*huma.Response{
			"200": jsonResponse[models.TranscriptionJob](spec, "OK"),
			"400": errorResponse("Bad Request"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	start := huma.Operation{
		OperationID: "start-transcription",
		Summary:     "Start transcription for uploaded file",
		Description: "Start transcription for an already uploaded audio file",
		Tags:        []string{"transcription"},
		Parameters: []*huma.Param{
			stringParam("path", "id", "Job ID", true),
		},
		RequestBody: requestBody("application/json", schemaOf[ImmediateTranscriptionRequest](spec), true, "Transcription parameters or authoritative saved profile ID"),
		Responses: map[string]*huma.Response{
			"200": jsonResponse[models.TranscriptionJob](spec, "OK"),
			"400": errorResponse("Bad Request"),
			"404": errorResponse("Not Found"),
			"409": errorResponse("Conflict"),
			"500": errorResponse("Internal Server Error"),
		},
	}
	transcription.route(http.MethodPost, "/{id}/start", handler.StartTranscription, start)
	start.OperationID = "rerun-transcription"
	start.Summary = "Run transcription again"
	transcription.route(http.MethodPost, "/{id}/rerun", handler.StartTranscription, start)
	transcription.route(http.MethodPost, "/{id}/kill", handler.KillJob, huma.Operation{
		OperationID: "kill-job",
		Summary:     "Kill running transcription job",
		Description: "Cancel a currently running or queued transcription job. A queue_item_id targets that exact queued run; an empty JSON object targets only an unqueued legacy run. Omitting the body preserves unrestricted legacy behavior.",
		Tags:        []string{"transcription"},
		Parameters: []*huma.Param{
			stringParam("path", "id", "Job ID", true),
		},
		RequestBody: requestBody("application/json", schemaOf[killJobRequest](spec), false, "Optional expected queue item identity"),
		Responses: map[string]*huma.Response{
			"200": jsonResponse[map[string]string](spec, "OK"),
			"400": errorResponse("Bad Request"),
			"404": errorResponse("Not Found"),
			"409": errorResponse("Conflict"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	transcription.route(http.MethodGet, "/{id}/queue", handler.ListTranscriptionQueue, huma.Operation{
		OperationID: "list-transcription-queue",
		Summary:     "List sequential transcription runs",
		Description: "Return the active per-audio queue item and all future queued runs",
		Tags:        []string{"transcription"},
		Parameters: []*huma.Param{
			stringParam("path", "id", "Job ID", true),
			{In: "query", Name: "include_terminal", Description: "Include terminal queue audit records", Required: false, Schema: &huma.Schema{Type: "boolean"}},
		},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[TranscriptionQueueResponse](spec, "OK"),
			"404": jsonResponse[ErrorResponse](spec, "Not Found"),
			"500": jsonResponse[ErrorResponse](spec, "Internal Server Error"),
		},
	})
	queueRunSchema := schemaOf[queueRunInput[models.WhisperXParams]](spec)
	queueRunSchema.AnyOf = []*huma.Schema{
		{Required: []string{"profile_id"}, Properties: map[string]*huma.Schema{
			"profile_id": {Type: huma.TypeString, Pattern: "\\S"},
		}},
		{Required: []string{"parameters"}, Properties: map[string]*huma.Schema{
			"parameters": {Type: huma.TypeObject},
		}},
	}
	transcription.route(http.MethodPost, "/{id}/queue", handler.QueueTranscriptionRun, huma.Operation{
		OperationID: "queue-transcription-run",
		Summary:     "Queue a sequential transcription run",
		Description: "Persist one immutable parameter or profile snapshot to run against this audio file",
		Tags:        []string{"transcription"},
		Parameters: []*huma.Param{
			stringParam("path", "id", "Job ID", true),
		},
		RequestBody: requestBody("application/json", queueRunSchema, true, "Supply a saved profile ID or transcription parameters"),
		Responses: map[string]*huma.Response{
			"201": jsonResponse[TranscriptionQueueResponse](spec, "Created"),
			"400": jsonResponse[ErrorResponse](spec, "Bad Request"),
			"404": jsonResponse[ErrorResponse](spec, "Not Found"),
			"409": jsonResponse[ErrorResponse](spec, "Conflict"),
			"500": jsonResponse[ErrorResponse](spec, "Internal Server Error"),
		},
	})
	transcription.route(http.MethodPut, "/{id}/queue/order", handler.ReorderTranscriptionQueue, huma.Operation{
		OperationID: "reorder-transcription-queue",
		Summary:     "Reorder future transcription runs",
		Tags:        []string{"transcription"},
		Parameters: []*huma.Param{
			stringParam("path", "id", "Job ID", true),
		},
		RequestBody: requestBody("application/json", schemaOf[ReorderTranscriptionQueueRequest](spec), true, "Complete ordered list of waiting queue item IDs"),
		Responses: map[string]*huma.Response{
			"200": jsonResponse[TranscriptionQueueResponse](spec, "OK"),
			"400": jsonResponse[ErrorResponse](spec, "Bad Request"),
			"404": jsonResponse[ErrorResponse](spec, "Not Found"),
			"500": jsonResponse[ErrorResponse](spec, "Internal Server Error"),
		},
	})
	transcription.route(http.MethodDelete, "/{id}/queue/{queue_id}", handler.CancelQueuedTranscriptionRun, huma.Operation{
		OperationID: "cancel-queued-transcription-run",
		Summary:     "Cancel a future queued transcription run",
		Tags:        []string{"transcription"},
		Parameters: []*huma.Param{
			stringParam("path", "id", "Job ID", true),
			stringParam("path", "queue_id", "Queue item ID", true),
		},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[TranscriptionQueueResponse](spec, "OK"),
			"404": jsonResponse[ErrorResponse](spec, "Not Found"),
			"409": jsonResponse[ErrorResponse](spec, "Conflict"),
			"500": jsonResponse[ErrorResponse](spec, "Internal Server Error"),
		},
	})
	transcription.route(http.MethodDelete, "/{id}/queue", handler.ClearTranscriptionQueue, huma.Operation{
		OperationID: "clear-transcription-queue",
		Summary:     "Clear future queued transcription runs",
		Tags:        []string{"transcription"},
		Parameters: []*huma.Param{
			stringParam("path", "id", "Job ID", true),
		},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[TranscriptionQueueResponse](spec, "OK"),
			"404": jsonResponse[ErrorResponse](spec, "Not Found"),
			"500": jsonResponse[ErrorResponse](spec, "Internal Server Error"),
		},
	})
	transcription.route(http.MethodGet, "/{id}/logs", handler.GetJobLogs, huma.Operation{
		OperationID: "get-job-logs",
		Summary:     "Get transcription logs",
		Description: "Get the raw transcription logs for a job",
		Tags:        []string{"transcription"},
		Parameters: []*huma.Param{
			stringParam("path", "id", "Job ID", true),
		},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[map[string]any](spec, "OK"),
			"401": jsonResponse[ErrorResponse](spec, "Unauthorized"),
			"404": jsonResponse[ErrorResponse](spec, "Not Found"),
			"500": jsonResponse[ErrorResponse](spec, "Internal Server Error"),
		},
	})
	transcription.route(http.MethodGet, "/{id}/execution", handler.GetJobExecutionData, huma.Operation{
		OperationID: "get-job-execution-data",
		Summary:     "Get transcription job execution data",
		Description: "Get execution parameters and timing for a transcription job",
		Tags:        []string{"transcription"},
		Parameters: []*huma.Param{
			stringParam("path", "id", "Job ID", true),
		},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[map[string]any](spec, "OK"),
			"404": errorResponse("Not Found"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	transcription.route(http.MethodGet, "/{id}/runs", handler.ListJobRuns, huma.Operation{
		OperationID: "list-job-runs",
		Summary:     "List transcription runs",
		Description: "List every transcription attempt for a job, including timing, status, and captured parameter metadata",
		Tags:        []string{"transcription"},
		Parameters: []*huma.Param{
			stringParam("path", "id", "Job ID", true),
		},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[map[string]any](spec, "OK"),
			"404": errorResponse("Not Found"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	transcription.route(http.MethodDelete, "/{id}/runs/active", handler.ClearActiveRun, huma.Operation{
		OperationID: "clear-active-run",
		Summary:     "Clear active transcription run",
		Description: "Clear a pinned active run so the latest completed run becomes active again",
		Tags:        []string{"transcription"},
		Parameters: []*huma.Param{
			stringParam("path", "id", "Job ID", true),
		},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[map[string]any](spec, "OK"),
			"404": errorResponse("Not Found"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	transcription.route(http.MethodGet, "/{id}/runs/{run_id}/transcript", handler.GetRunTranscript, huma.Operation{
		OperationID: "get-run-transcript",
		Summary:     "Get run transcript",
		Description: "Get the transcript snapshot captured for a specific transcription run",
		Tags:        []string{"transcription"},
		Parameters: []*huma.Param{
			stringParam("path", "id", "Job ID", true),
			stringParam("path", "run_id", "Run ID", true),
		},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[map[string]any](spec, "OK"),
			"404": errorResponse("Not Found"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	transcription.route(http.MethodGet, "/{id}/runs/{run_id}/logs", handler.GetRunLogs, huma.Operation{
		OperationID: "get-run-logs",
		Summary:     "Get run logs",
		Description: "Get the log snapshot captured for a specific transcription run",
		Tags:        []string{"transcription"},
		Parameters: []*huma.Param{
			stringParam("path", "id", "Job ID", true),
			stringParam("path", "run_id", "Run ID", true),
		},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[map[string]any](spec, "OK"),
			"403": errorResponse("Forbidden"),
			"404": errorResponse("Not Found"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	transcription.route(http.MethodGet, "/{id}/runs/{run_id}/recovery", handler.GetRunRecovery, huma.Operation{
		OperationID: "get-run-recovery",
		Summary:     "Get execution recovery details",
		Description: "Return saved stages, attempts, partial text and same-plan resume availability",
		Tags:        []string{"transcription"},
		Parameters: []*huma.Param{
			stringParam("path", "id", "Recording ID", true),
			stringParam("path", "run_id", "Execution ID", true),
		},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[map[string]any](spec, "OK"),
			"404": jsonResponse[ErrorResponse](spec, "Not Found"),
			"500": jsonResponse[ErrorResponse](spec, "Internal Server Error"),
		},
	})
	transcription.route(http.MethodGet, "/{id}/runs/{run_id}/resources", handler.GetRunResources, huma.Operation{
		OperationID: "get-run-resources",
		Summary:     "Get run resource measurements",
		Description: "Return invocation measurements and stage attempt timing, settings and resource usage",
		Tags:        []string{"transcription"},
		Parameters: []*huma.Param{
			stringParam("path", "id", "Recording ID", true),
			stringParam("path", "run_id", "Execution ID", true),
		},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[map[string]any](spec, "OK"),
			"404": jsonResponse[ErrorResponse](spec, "Not Found"),
			"500": jsonResponse[ErrorResponse](spec, "Internal Server Error"),
		},
	})
	transcription.route(http.MethodPost, "/{id}/runs/{run_id}/resume", handler.ResumeRun, huma.Operation{
		OperationID: "resume-run",
		Summary:     "Resume a saved execution",
		Description: "Resume the same execution within its original deadline and attempt budget; accepts no quality overrides",
		Tags:        []string{"transcription"},
		Parameters: []*huma.Param{
			stringParam("path", "id", "Recording ID", true),
			stringParam("path", "run_id", "Execution ID", true),
		},
		Responses: map[string]*huma.Response{
			"202": jsonResponse[map[string]any](spec, "Accepted"),
			"404": jsonResponse[ErrorResponse](spec, "Not Found"),
			"409": jsonResponse[ErrorResponse](spec, "Conflict"),
			"503": jsonResponse[ErrorResponse](spec, "Service Unavailable"),
		},
	})
	transcription.route(http.MethodPost, "/{id}/runs/{run_id}/active", handler.SetActiveRun, huma.Operation{
		OperationID: "set-active-run",
		Summary:     "Set active transcription run",
		Description: "Pin a completed run as the active transcript source for a transcription job",
		Tags:        []string{"transcription"},
		Parameters: []*huma.Param{
			stringParam("path", "id", "Job ID", true),
			stringParam("path", "run_id", "Run ID", true),
		},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[map[string]any](spec, "OK"),
			"400": errorResponse("Bad Request"),
			"404": errorResponse("Not Found"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	transcription.route(http.MethodGet, "/{id}/merge-status", handler.GetMergeStatus, huma.Operation{
		OperationID: "get-merge-status",
		Summary:     "Get multi-track merge status",
		Description: "Get the current merge status for a multi-track job",
		Tags:        []string{"transcription"},
		Parameters: []*huma.Param{
			stringParam("path", "id", "Job ID", true),
		},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[map[string]any](spec, "OK"),
			"404": errorResponse("Not Found"),
		},
	})
	transcription.route(http.MethodGet, "/{id}/track-progress", handler.GetTrackProgress, huma.Operation{
		OperationID: "get-track-progress",
		Summary:     "Get multi-track job progress",
		Description: "Get real-time progress information for individual tracks in a multi-track job",
		Tags:        []string{"transcription"},
		Parameters: []*huma.Param{
			stringParam("path", "id", "Job ID", true),
		},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[map[string]any](spec, "OK"),
			"400": errorResponse("Bad Request"),
			"404": errorResponse("Not Found"),
		},
	})
	transcription.route(http.MethodPut, "/{id}/title", handler.UpdateTranscriptionTitle, huma.Operation{
		OperationID: "update-transcription-title",
		Summary:     "Update transcription title",
		Description: "Update the title of an audio file / transcription",
		Tags:        []string{"transcription"},
		Parameters: []*huma.Param{
			stringParam("path", "id", "Job ID", true),
		},
		RequestBody: requestBody("application/json", schemaOf[map[string]string](spec), true, "Title update request"),
		Responses: map[string]*huma.Response{
			"200": jsonResponse[map[string]any](spec, "OK"),
			"400": errorResponse("Bad Request"),
			"404": errorResponse("Not Found"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	transcription.route(http.MethodGet, "/{id}/summary", handler.GetSummaryForTranscription, huma.Operation{
		OperationID: "get-summary-for-transcription",
		Summary:     "Get latest summary for transcription",
		Description: "Get the most recent saved summary for the given transcription",
		Tags:        []string{"summarize"},
		Parameters: []*huma.Param{
			stringParam("path", "id", "Transcription ID", true),
		},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[models.Summary](spec, "OK"),
			"400": errorResponse("Bad Request"),
			"404": errorResponse("Not Found"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	transcription.route(http.MethodGet, "/{id}", handler.GetTranscriptionJob, huma.Operation{
		OperationID: "get-transcription-job",
		Summary:     "Get transcription job details",
		Description: "Get details of a specific transcription job",
		Tags:        []string{"transcription"},
		Parameters: []*huma.Param{
			stringParam("path", "id", "Job ID", true),
		},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[models.TranscriptionJob](spec, "OK"),
			"404": errorResponse("Not Found"),
		},
	})
	transcription.route(http.MethodDelete, "/{id}", handler.DeleteTranscriptionJob, huma.Operation{
		OperationID: "delete-transcription-job",
		Summary:     "Delete transcription job",
		Description: "Delete a transcription job and its associated files",
		Tags:        []string{"transcription"},
		Parameters: []*huma.Param{
			stringParam("path", "id", "Job ID", true),
		},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[map[string]string](spec, "OK"),
			"400": errorResponse("Bad Request"),
			"404": errorResponse("Not Found"),
			"409": errorResponse("Conflict"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	transcription.route(http.MethodGet, "/statistics", handler.GetRunStatistics, huma.Operation{
		OperationID: "get-run-statistics",
		Summary:     "Get global run statistics",
		Tags:        []string{"transcription"},
		Parameters: []*huma.Param{
			{In: "query", Name: "days", Description: "Window: 0 (all), 7, 30 or 90 days", Required: false, Schema: &huma.Schema{Type: "integer", Default: 30}},
		},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[models.RunStatistics](spec, "OK"),
			"400": errorResponse("Bad Request"),
			"500": errorResponse("Internal Server Error"),
			"503": errorResponse("Service Unavailable"),
		},
	})
	transcription.route(http.MethodGet, "/statistics/runs", handler.GetRunHistory, huma.Operation{
		OperationID: "get-run-history",
		Summary:     "List run diagnostics history",
		Tags:        []string{"transcription"},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[models.RunHistory](spec, "OK"),
			"400": errorResponse("Bad Request"),
			"500": errorResponse("Internal Server Error"),
			"503": errorResponse("Service Unavailable"),
		},
	})
	// Notes for a transcription
	transcription.route(http.MethodGet, "/{id}/notes", handler.ListNotes, huma.Operation{
		OperationID: "list-notes",
		Summary:     "List notes for a transcription",
		Description: "Get all notes attached to a transcription, ordered by time and creation",
		Tags:        []string{"notes"},
		Parameters: []*huma.Param{
			stringParam("path", "id", "Transcription ID", true),
		},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[[]models.Note](spec, "OK"),
			"400": errorResponse("Bad Request"),
			"404": errorResponse("Not Found"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	transcription.route(http.MethodPost, "/{id}/notes", handler.CreateNote, huma.Operation{
		OperationID: "create-note",
		Summary:     "Create a note for a transcription",
		Description: "Create a new note attached to the specified transcription",
		Tags:        []string{"notes"},
		Parameters: []*huma.Param{
			stringParam("path", "id", "Transcription ID", true),
		},
		RequestBody: requestBody("application/json", schemaOf[NoteCreateRequest](spec), true, "Note create payload"),
		Responses: map[string]*huma.Response{
			"200": jsonResponse[models.Note](spec, "OK"),
			"400": errorResponse("Bad Request"),
			"404": errorResponse("Not Found"),
			"500": errorResponse("Internal Server Error"),
		},
	})

	// Speaker mappings for a transcription
	transcription.route(http.MethodGet, "/{id}/speakers", handler.GetSpeakerMappings, huma.Operation{
		OperationID: "get-speaker-mappings",
		Summary:     "Get speaker mappings for a transcription",
		Description: "Retrieves all custom speaker names for a transcription job",
		Tags:        []string{"transcription"},
		Parameters: []*huma.Param{
			stringParam("path", "id", "Transcription Job ID", true),
		},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[[]SpeakerMappingResponse](spec, "OK"),
			"400": errorResponse("Bad Request"),
			"404": errorResponse("Not Found"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	transcription.route(http.MethodPost, "/{id}/speakers", handler.UpdateSpeakerMappings, huma.Operation{
		OperationID: "update-speaker-mappings",
		Summary:     "Update speaker mappings for a transcription",
		Description: "Updates or creates custom speaker names for a transcription job",
		Tags:        []string{"transcription"},
		Parameters: []*huma.Param{
			stringParam("path", "id", "Transcription Job ID", true),
		},
		RequestBody: requestBody("application/json", schemaOf[SpeakerMappingsUpdateRequest](spec), true, "Speaker mappings to update"),
		Responses: map[string]*huma.Response{
			"200": jsonResponse[[]SpeakerMappingResponse](spec, "OK"),
			"400": errorResponse("Bad Request"),
			"404": errorResponse("Not Found"),
			"500": errorResponse("Internal Server Error"),
		},
	})

	// Quick transcription endpoints
	transcription.route(http.MethodPost, "/quick", handler.SubmitQuickTranscription, huma.Operation{
		OperationID: "submit-quick-transcription",
		Summary:     "Submit quick transcription job",
		Description: "Submit an audio file for temporary transcription (data discarded after 6 hours)",
		Tags:        []string{"transcription"},
		RequestBody: requestBody("multipart/form-data", &huma.Schema{Type: "object", Required: []string{"audio"}, Properties: map[string]*huma.Schema{
			"audio":        {Type: "string", Format: "binary", Description: "Audio file"},
			"parameters":   {Type: "string", Description: "JSON string of transcription parameters"},
			"profile_name": {Type: "string", Description: "Profile name to use for transcription"},
		}}, true, ""),
		Responses: map[string]*huma.Response{
			"200": jsonResponse[transcriptionapi.QuickTranscriptionJob](spec, "OK"),
			"400": errorResponse("Bad Request"),
			"409": errorResponse("Conflict"),
			"413": errorResponse("Request Entity Too Large"),
			"429": errorResponse("Too Many Requests"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	transcription.route(http.MethodGet, "/quick/{id}", handler.GetQuickTranscriptionStatus, huma.Operation{
		OperationID: "get-quick-transcription-status",
		Summary:     "Get quick transcription status",
		Description: "Get the current status of a quick transcription job",
		Tags:        []string{"transcription"},
		Parameters: []*huma.Param{
			stringParam("path", "id", "Job ID", true),
		},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[transcriptionapi.QuickTranscriptionJob](spec, "OK"),
			"404": errorResponse("Not Found"),
			"500": errorResponse("Internal Server Error"),
		},
	})
}
