package api

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jaysqvl/Jotist/internal/models"
	"github.com/jaysqvl/Jotist/pkg/middleware"
)

func registerUploadRoutes(transcription routeGroup, handler *Handler) {
	spec := transcription.spec
	// File upload routes - disable compression for these
	uploadRoutes := transcription.group("", middleware.NoCompressionMiddleware())
	uploadRoutes.route(http.MethodPost, "/upload", handler.UploadAudio, huma.Operation{
		OperationID: "upload-audio",
		Summary:     "Upload audio file",
		Description: "Upload an audio file without starting transcription",
		Tags:        []string{"transcription"},
		RequestBody: requestBody("multipart/form-data", &huma.Schema{Type: "object", Required: []string{"audio"}, Properties: map[string]*huma.Schema{
			"audio": {Type: "string", Format: "binary", Description: "Audio file"},
			"title": {Type: "string", Description: "Job title"},
		}}, true, ""),
		Responses: map[string]*huma.Response{
			"200": jsonResponse[models.TranscriptionJob](spec, "OK"),
			"400": errorResponse("Bad Request"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	uploadRoutes.route(http.MethodPost, "/upload-video", handler.UploadVideo, huma.Operation{
		OperationID: "upload-video",
		Summary:     "Upload video file for transcription",
		Description: "Upload a video file, extract audio from it using ffmpeg, and create a transcription job",
		Tags:        []string{"transcription"},
		RequestBody: requestBody("multipart/form-data", &huma.Schema{Type: "object", Required: []string{"video"}, Properties: map[string]*huma.Schema{
			"title": {Type: "string", Description: "Job title"},
			"video": {Type: "string", Format: "binary", Description: "Video file"},
		}}, true, ""),
		Responses: map[string]*huma.Response{
			"200": jsonResponse[models.TranscriptionJob](spec, "OK"),
			"400": errorResponse("Bad Request"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	uploadRoutes.route(http.MethodPost, "/upload-multitrack", handler.UploadMultiTrack, huma.Operation{
		OperationID: "upload-multi-track",
		Summary:     "Upload multi-track audio files",
		Description: "Upload multiple audio files for multi-track transcription",
		Tags:        []string{"transcription"},
		RequestBody: requestBody("multipart/form-data", &huma.Schema{Type: "object", Required: []string{"aup", "files"}, Properties: map[string]*huma.Schema{
			"aup":   {Type: "string", Format: "binary", Description: "Audacity .aup project file"},
			"files": {Type: "array", Description: "Audio track files. Repeat this field for each track.", MinItems: new(1), Items: &huma.Schema{Type: "string", Format: "binary", Description: "Audio track files. Repeat this field for each track."}},
			"title": {Type: "string", Description: "Job title"},
		}}, true, ""),
		Responses: map[string]*huma.Response{
			"200": jsonResponse[models.TranscriptionJob](spec, "OK"),
			"400": errorResponse("Bad Request"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	uploadRoutes.route(http.MethodPost, "/uploads", handler.CreateUploadSession, huma.Operation{
		OperationID: "create-upload-session",
		Summary:     "Create an upload session",
		Description: "Start a resumable upload with a kind and a list of files. Save the returned upload token for chunk, completion and cancellation requests.\nAudio, quick and submit uploads require one audio file. Video requires one video file. Multitrack requires one aup file and at least one track.",
		Tags:        []string{"uploads"},
		RequestBody: requestBody("application/json", schemaOf[createUploadSessionRequest](spec), true, "Upload kind, files and optional run settings"),
		Responses: map[string]*huma.Response{
			"200": jsonResponse[createUploadSessionResponse](spec, "OK"),
			"400": errorResponse("Bad Request"),
			"429": errorResponse("Too Many Requests"),
			"500": errorResponse("Internal Server Error"),
			"507": errorResponse("Insufficient Storage"),
		},
	})
	uploadRoutes.route(http.MethodGet, "/uploads/{id}", handler.GetUploadSession, huma.Operation{
		OperationID: "get-upload-session",
		Summary:     "Get upload session progress",
		Description: "Retrieve each file's accepted and missing chunk indexes.",
		Tags:        []string{"uploads"},
		Parameters: []*huma.Param{
			stringParam("path", "id", "Upload session ID", true),
		},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[uploadSessionResponse](spec, "OK"),
			"404": errorResponse("Not Found"),
		},
	})
	uploadRoutes.route(http.MethodPut, "/uploads/{id}/files/{file_id}/chunks/{index}", handler.UploadChunk, huma.Operation{
		OperationID: "upload-chunk",
		Summary:     "Upload a file chunk",
		Description: "Send the raw file bytes for one chunk. Compute the SHA-256 and Content-Range for those bytes.",
		Tags:        []string{"uploads"},
		Parameters: []*huma.Param{
			stringParam("path", "id", "Upload session ID", true),
			stringParam("path", "file_id", "File ID from the upload session", true),
			{In: "path", Name: "index", Description: "Zero-based chunk index", Required: true, Schema: &huma.Schema{Type: "integer", Minimum: new(float64(0))}},
			stringParam("header", "X-Upload-Token", "Token from the create-session response", true),
			stringParam("header", "Content-Range", "Byte range in the form bytes start-end/total", true),
			stringParam("header", "X-Chunk-SHA256", "Hexadecimal SHA-256 of the chunk bytes", true),
		},
		RequestBody: requestBody("application/octet-stream", binarySchema(), true, "Raw chunk bytes"),
		Responses: map[string]*huma.Response{
			"200": jsonResponse[map[string]bool](spec, "accepted is true for a stored chunk. duplicate is true for a retry."),
			"400": errorResponse("Bad Request"),
			"401": errorResponse("Unauthorized"),
			"404": errorResponse("Not Found"),
			"409": errorResponse("Conflict"),
			"413": errorResponse("Request Entity Too Large"),
			"429": errorResponse("Too Many Requests"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	uploadRoutes.route(http.MethodPost, "/uploads/{id}/complete", handler.CompleteUploadSession, huma.Operation{
		OperationID: "complete-upload-session",
		Summary:     "Complete an upload session",
		Description: "Assemble the uploaded chunks and create the recording or transcription job.",
		Tags:        []string{"uploads"},
		Parameters: []*huma.Param{
			stringParam("path", "id", "Upload session ID", true),
			stringParam("header", "X-Upload-Token", "Token from the create-session response", true),
		},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[map[string]any](spec, "Result fields depend on the upload kind."),
			"400": errorResponse("Bad Request"),
			"401": errorResponse("Unauthorized"),
			"404": errorResponse("Not Found"),
			"409": errorResponse("Conflict"),
			"429": errorResponse("Too Many Requests"),
			"500": errorResponse("Internal Server Error"),
			"507": errorResponse("Insufficient Storage"),
		},
	})
	uploadRoutes.route(http.MethodDelete, "/uploads/{id}", handler.CancelUploadSession, huma.Operation{
		OperationID: "cancel-upload-session",
		Summary:     "Cancel an upload session",
		Description: "Cancel the upload and remove its staged chunks.",
		Tags:        []string{"uploads"},
		Parameters: []*huma.Param{
			stringParam("path", "id", "Upload session ID", true),
			stringParam("header", "X-Upload-Token", "Token from the create-session response", true),
		},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[map[string]bool](spec, "cancelled is true"),
			"401": errorResponse("Unauthorized"),
			"404": errorResponse("Not Found"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	uploadRoutes.route(http.MethodGet, "/{id}/audio", handler.GetAudioFile, huma.Operation{
		OperationID: "get-audio-file",
		Summary:     "Get audio file",
		Description: "Serve the audio file for a transcription job",
		Tags:        []string{"transcription"},
		Parameters: []*huma.Param{
			stringParam("path", "id", "Job ID", true),
		},
		Responses: map[string]*huma.Response{
			"200": mediaResponse("OK", binarySchema(), "audio/mp4", "audio/mpeg", "audio/wav"),
			"404": errorResponse("Not Found"),
			"500": errorResponse("Internal Server Error"),
		},
	}) // Audio streaming shouldn't be compressed
}
