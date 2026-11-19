package api

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jaysqvl/Jotist/internal/auth"
)

func registerEventsRoutes(v1 routeGroup, handler *Handler, authService *auth.AuthService) {
	spec := v1.spec
	// Config routes (require authentication)
	config := v1.signedIn("/config", authService)
	config.route(http.MethodPost, "/openai/validate", handler.ValidateOpenAIKey, huma.Operation{
		OperationID: "validate-open-ai-key",
		Summary:     "Validate OpenAI API Key",
		Description: "Validate the provided OpenAI API key and return available Whisper models",
		Tags:        []string{"config"},
		RequestBody: requestBody("application/json", schemaOf[ValidateOpenAIKeyRequest](spec), true, "API Key"),
		Responses: map[string]*huma.Response{
			"200": jsonResponse[map[string]any](spec, "OK"),
			"400": errorResponse("Bad Request"),
			"401": errorResponse("Unauthorized"),
			"500": errorResponse("Internal Server Error"),
		},
	})

	// SSE Events (require authentication)
	events := v1.authenticated("/events", authService)
	events.route(http.MethodGet, "/", handler.Events, huma.Operation{
		OperationID: "events",
		Summary:     "SSE Events",
		Description: "Subscribe to server-sent events for one recording. The connection stays open for future updates.\nThe first frame is `data: {\"type\":\"connected\",\"job_id\":\"RECORDING_ID\"}`. Updates are data frames with a type and payload.",
		Tags:        []string{"events"},
		Parameters: []*huma.Param{
			stringParam("query", "job_id", "Recording ID returned by List recordings", true),
		},
		Responses: map[string]*huma.Response{
			"200": mediaResponse("Open event stream", &huma.Schema{Type: "string"}, "text/event-stream"),
			"400": mediaResponse("job_id is required", &huma.Schema{Type: huma.TypeString}, "text/plain"),
			"401": errorResponse("Authentication required"),
		},
	})
}
