package api

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jaysqvl/Jotist/internal/auth"
	"github.com/jaysqvl/Jotist/internal/models"
)

func registerPreferencesRoutes(v1 routeGroup, handler *Handler, authService *auth.AuthService) {
	spec := v1.spec
	// User routes (require authentication)
	user := v1.signedIn("/user", authService)
	user.route(http.MethodGet, "/default-profile", handler.GetUserDefaultProfile, huma.Operation{
		OperationID: "get-user-default-profile",
		Summary:     "Get user's default profile",
		Description: "Get the default transcription profile for the current user",
		Tags:        []string{"profiles"},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[models.TranscriptionProfile](spec, "OK"),
			"401": errorResponse("Unauthorized"),
			"404": errorResponse("Not Found"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	user.route(http.MethodPost, "/default-profile", handler.SetUserDefaultProfile, huma.Operation{
		OperationID: "set-user-default-profile",
		Summary:     "Set user's default profile",
		Description: "Set the default transcription profile for the current user",
		Tags:        []string{"profiles"},
		RequestBody: requestBody("application/json", schemaOf[SetUserDefaultProfileRequest](spec), true, "Default profile request"),
		Responses: map[string]*huma.Response{
			"200": jsonResponse[map[string]string](spec, "OK"),
			"400": errorResponse("Bad Request"),
			"401": errorResponse("Unauthorized"),
			"404": errorResponse("Not Found"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	user.route(http.MethodGet, "/settings", handler.GetUserSettings, huma.Operation{
		OperationID: "get-user-settings",
		Summary:     "Get user settings",
		Description: "Get the current user's settings including auto-transcription preference",
		Tags:        []string{"user"},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[UserSettingsResponse](spec, "OK"),
			"401": errorResponse("Unauthorized"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	user.route(http.MethodPut, "/settings", handler.UpdateUserSettings, huma.Operation{
		OperationID: "update-user-settings",
		Summary:     "Update user settings",
		Description: "Update the current user's settings",
		Tags:        []string{"user"},
		RequestBody: requestBody("application/json", schemaOf[UpdateUserSettingsRequest](spec), true, "Settings update request"),
		Responses: map[string]*huma.Response{
			"200": jsonResponse[UserSettingsResponse](spec, "OK"),
			"400": errorResponse("Bad Request"),
			"401": errorResponse("Unauthorized"),
			"500": errorResponse("Internal Server Error"),
		},
	})

	// Admin routes (require authentication)
	admin := v1.authenticated("/admin", authService)
	queue := admin.group("/queue")
	queueSettings := queue.signedIn("", authService)
	queue.route(http.MethodGet, "/stats", handler.GetQueueStats, huma.Operation{
		OperationID: "get-queue-stats",
		Summary:     "Get queue statistics",
		Description: "Get current queue statistics",
		Tags:        []string{"admin"},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[map[string]any](spec, "OK"),
		},
	})
	queue.route(http.MethodGet, "/activity", handler.GetQueueActivity, huma.Operation{
		OperationID: "get-queue-activity",
		Summary:     "Show activity across recording queues",
		Description: "Sample shared worker ownership, current stages and waiting recordings without private request or transcript content.",
		Tags:        []string{"admin"},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[models.QueueActivity](spec, "OK"),
			"500": errorResponse("Internal Server Error"),
			"503": errorResponse("Service Unavailable"),
		},
	})
	queueSettings.route(http.MethodGet, "/settings", handler.GetQueueSettings, huma.Operation{
		OperationID: "get-queue-settings",
		Summary:     "Read recording queue settings",
		Tags:        []string{"admin"},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[models.QueueSettingsResponse](spec, "OK"),
			"503": errorResponse("Service Unavailable"),
		},
	})
	queueSettings.route(http.MethodPut, "/settings", handler.UpdateQueueSettings, huma.Operation{
		OperationID: "update-queue-settings",
		Summary:     "Set recording concurrency",
		Description: "Save a server-wide limit. Active executions finish normally when the limit is lowered. QUEUE_WORKERS is an explicit deployment override.",
		Tags:        []string{"admin"},
		RequestBody: requestBody("application/json", schemaOf[UpdateQueueSettingsRequest](spec), true, "Recording concurrency"),
		Responses: map[string]*huma.Response{
			"200": jsonResponse[models.QueueSettingsResponse](spec, "OK"),
			"400": errorResponse("Bad Request"),
			"409": errorResponse("Conflict"),
			"500": errorResponse("Internal Server Error"),
			"503": errorResponse("Service Unavailable"),
		},
	})

	// LLM configuration routes (require authentication)
	llm := v1.signedIn("/llm", authService)
	llm.route(http.MethodGet, "/config", handler.GetLLMConfig, huma.Operation{
		OperationID: "get-llm-config",
		Summary:     "Get LLM configuration",
		Description: "Get the current active LLM configuration",
		Tags:        []string{"llm"},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[LLMConfigResponse](spec, "OK"),
			"404": errorResponse("Not Found"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	llm.route(http.MethodPost, "/config", handler.SaveLLMConfig, huma.Operation{
		OperationID: "save-llm-config",
		Summary:     "Create or update LLM configuration",
		Description: "Create or update LLM configuration settings",
		Tags:        []string{"llm"},
		RequestBody: requestBody("application/json", schemaOf[LLMConfigRequest](spec), true, "LLM configuration details"),
		Responses: map[string]*huma.Response{
			"200": jsonResponse[LLMConfigResponse](spec, "OK"),
			"400": errorResponse("Bad Request"),
			"500": errorResponse("Internal Server Error"),
		},
	})
}
