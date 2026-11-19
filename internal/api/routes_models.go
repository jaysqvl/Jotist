package api

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jaysqvl/Jotist/internal/auth"
	"github.com/jaysqvl/Jotist/internal/modeldownloads"
	"github.com/jaysqvl/Jotist/internal/modelstorage"
)

func registerModelsRoutes(v1 routeGroup, handler *Handler, authService *auth.AuthService) {
	spec := v1.spec
	modelFiles := v1.signedIn("/settings/models", authService)
	modelFiles.route(http.MethodGet, "", handler.GetModelStorage, huma.Operation{
		OperationID: "get-model-storage",
		Summary:     "Audit downloaded models and local cache storage",
		Tags:        []string{"settings"},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[modelstorage.Inventory](spec, "OK"),
		},
	})
	modelFiles.route(http.MethodGet, "/events", handler.ModelStorageEvents, huma.Operation{
		OperationID: "model-storage-events",
		Summary:     "Subscribe to model storage changes",
		Tags:        []string{"settings"},
		Responses: map[string]*huma.Response{
			"200": mediaResponse("Model storage invalidations", &huma.Schema{Type: "string"}, "text/event-stream"),
			"503": errorResponse("Service Unavailable"),
		},
	})
	modelFiles.route(http.MethodGet, "/catalog", handler.GetModelDownloadCatalog, huma.Operation{
		OperationID: "get-model-download-catalog",
		Summary:     "Browse models available for manual download",
		Tags:        []string{"settings"},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[ModelDownloadCatalogResponse](spec, "OK"),
		},
	})
	modelFiles.route(http.MethodGet, "/downloads", handler.ListModelDownloads, huma.Operation{
		OperationID: "list-model-downloads",
		Summary:     "List model downloads and recent outcomes",
		Tags:        []string{"settings"},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[ModelDownloadListResponse](spec, "OK"),
		},
	})
	modelFiles.route(http.MethodPost, "/downloads", handler.DownloadModel, huma.Operation{
		OperationID: "download-model",
		Summary:     "Download a model's files",
		Tags:        []string{"settings"},
		RequestBody: requestBody("application/json", schemaOf[DownloadModelRequest](spec), true, "Registered model and keep preference"),
		Responses: map[string]*huma.Response{
			"202": jsonResponse[modeldownloads.Job](spec, "Accepted"),
			"400": errorResponse("Bad Request"),
			"401": errorResponse("Unauthorized"),
			"409": errorResponse("Conflict"),
		},
	})
	modelFiles.route(http.MethodPost, "/downloads/{downloadID}/cancel", handler.CancelModelDownload, huma.Operation{
		OperationID: "cancel-model-download",
		Summary:     "Cancel a model download",
		Tags:        []string{"settings"},
		Parameters: []*huma.Param{
			stringParam("path", "downloadID", "Download job ID", true),
		},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[modeldownloads.Job](spec, "OK"),
			"401": errorResponse("Unauthorized"),
		},
	})
	modelFiles.route(http.MethodPut, "/retention", handler.UpdateModelRetention, huma.Operation{
		OperationID: "update-model-retention",
		Summary:     "Set automatic model cache retention",
		Tags:        []string{"settings"},
		RequestBody: requestBody("application/json", schemaOf[UpdateModelRetentionRequest](spec), true, "Automatic cleanup policy"),
		Responses: map[string]*huma.Response{
			"200": jsonResponse[modelstorage.CacheRetentionPolicy](spec, "OK"),
			"400": errorResponse("Bad Request"),
			"503": errorResponse("Service Unavailable"),
		},
	})
	modelFiles.route(http.MethodGet, "/cleanup", handler.PreviewModelCleanup, huma.Operation{
		OperationID: "preview-model-cleanup",
		Summary:     "Preview model cache cleanup",
		Tags:        []string{"settings"},
		Parameters: []*huma.Param{
			{In: "query", Name: "mode", Description: "Cleanup scope", Required: false, Schema: &huma.Schema{Type: "string", Enum: []any{"all", "expired"}}},
		},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[modelstorage.CleanupPlan](spec, "OK"),
			"400": errorResponse("Bad Request"),
			"503": errorResponse("Service Unavailable"),
		},
	})
	modelFiles.route(http.MethodPost, "/cleanup", handler.CleanupModelCache, huma.Operation{
		OperationID: "cleanup-model-cache",
		Summary:     "Clear confirmed downloaded model files",
		Tags:        []string{"settings"},
		RequestBody: requestBody("application/json", schemaOf[CleanupModelCacheRequest](spec), true, "Confirmed cleanup version"),
		Responses: map[string]*huma.Response{
			"200": jsonResponse[modelstorage.CleanupResult](spec, "OK"),
			"400": errorResponse("Bad Request"),
			"409": errorResponse("Conflict"),
			"503": errorResponse("Service Unavailable"),
		},
	})
	modelFiles.route(http.MethodPut, "/{id}/retention", handler.UpdateModelKeepDownloaded, huma.Operation{
		OperationID: "update-model-keep-downloaded",
		Summary:     "Keep a model downloaded or resume its retention policy",
		Tags:        []string{"settings"},
		Parameters: []*huma.Param{
			stringParam("path", "id", "Opaque cache entry ID", true),
		},
		RequestBody: requestBody("application/json", schemaOf[UpdateModelKeepDownloadedRequest](spec), true, "Keep downloaded preference and current file version"),
		Responses: map[string]*huma.Response{
			"200": jsonResponse[UpdateModelKeepDownloadedResponse](spec, "OK"),
			"400": errorResponse("Bad Request"),
			"409": errorResponse("Conflict"),
			"503": errorResponse("Service Unavailable"),
		},
	})
	modelFiles.route(http.MethodDelete, "/{id}", handler.RemoveModelFiles, huma.Operation{
		OperationID: "remove-model-files",
		Summary:     "Remove a downloaded model",
		Tags:        []string{"settings"},
		Parameters: []*huma.Param{
			stringParam("path", "id", "Opaque cache entry ID", true),
		},
		RequestBody: requestBody("application/json", schemaOf[RemoveModelFilesRequest](spec), true, "Confirmed inventory version"),
		Responses: map[string]*huma.Response{
			"200": jsonResponse[modelstorage.Removal](spec, "OK"),
			"400": errorResponse("Bad Request"),
			"503": errorResponse("Service Unavailable"),
		},
	})
}
