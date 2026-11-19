package api

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jaysqvl/Jotist/internal/auth"
	"github.com/jaysqvl/Jotist/internal/models"
)

func registerContentRoutes(v1 routeGroup, handler *Handler, authService *auth.AuthService) {
	spec := v1.spec
	// Summarization templates routes (require authentication)
	summaries := v1.authenticated("/summaries", authService)
	summaries.route(http.MethodGet, "/", handler.ListSummaryTemplates, huma.Operation{
		OperationID: "list-summary-templates",
		Summary:     "List summarization templates",
		Description: "Get all summarization templates",
		Tags:        []string{"summaries"},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[[]models.SummaryTemplate](spec, "OK"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	summaries.route(http.MethodPost, "/", handler.CreateSummaryTemplate, huma.Operation{
		OperationID: "create-summary-template",
		Summary:     "Create summarization template",
		Description: "Create a new summarization template",
		Tags:        []string{"summaries"},
		RequestBody: requestBody("application/json", schemaOf[SummaryTemplateRequest](spec), true, "Template payload"),
		Responses: map[string]*huma.Response{
			"201": jsonResponse[models.SummaryTemplate](spec, "Created"),
			"400": errorResponse("Bad Request"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	summaries.route(http.MethodGet, "/{id}", handler.GetSummaryTemplate, huma.Operation{
		OperationID: "get-summary-template",
		Summary:     "Get summarization template",
		Description: "Get a summarization template by ID",
		Tags:        []string{"summaries"},
		Parameters: []*huma.Param{
			stringParam("path", "id", "Template ID", true),
		},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[models.SummaryTemplate](spec, "OK"),
			"404": errorResponse("Not Found"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	summaries.route(http.MethodPut, "/{id}", handler.UpdateSummaryTemplate, huma.Operation{
		OperationID: "update-summary-template",
		Summary:     "Update summarization template",
		Description: "Update a summarization template by ID",
		Tags:        []string{"summaries"},
		Parameters: []*huma.Param{
			stringParam("path", "id", "Template ID", true),
		},
		RequestBody: requestBody("application/json", schemaOf[SummaryTemplateRequest](spec), true, "Template payload"),
		Responses: map[string]*huma.Response{
			"200": jsonResponse[models.SummaryTemplate](spec, "OK"),
			"400": errorResponse("Bad Request"),
			"404": errorResponse("Not Found"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	summaries.route(http.MethodDelete, "/{id}", handler.DeleteSummaryTemplate, huma.Operation{
		OperationID: "delete-summary-template",
		Summary:     "Delete summarization template",
		Description: "Delete a summarization template by ID",
		Tags:        []string{"summaries"},
		Parameters: []*huma.Param{
			stringParam("path", "id", "Template ID", true),
		},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[map[string]string](spec, "OK"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	summaries.route(http.MethodGet, "/settings", handler.GetSummarySettings, huma.Operation{
		OperationID: "get-summary-settings",
		Summary:     "Get summary settings",
		Description: "Get global summarization settings",
		Tags:        []string{"summaries"},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[SummarySettingsResponse](spec, "OK"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	summaries.route(http.MethodPost, "/settings", handler.SaveSummarySettings, huma.Operation{
		OperationID: "save-summary-settings",
		Summary:     "Save summary settings",
		Description: "Create or update global summarization settings",
		Tags:        []string{"summaries"},
		RequestBody: requestBody("application/json", schemaOf[SummarySettingsRequest](spec), true, "Settings payload"),
		Responses: map[string]*huma.Response{
			"200": jsonResponse[SummarySettingsResponse](spec, "OK"),
			"400": errorResponse("Bad Request"),
			"500": errorResponse("Internal Server Error"),
		},
	})

	// Chat routes (require authentication)
	chat := v1.authenticated("/chat", authService)
	chat.route(http.MethodGet, "/models", handler.GetChatModels, huma.Operation{
		OperationID: "get-chat-models",
		Summary:     "Get available chat models",
		Description: "Get list of available OpenAI chat models",
		Tags:        []string{"chat"},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[ChatModelsResponse](spec, "OK"),
			"400": errorResponse("Bad Request"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	chat.route(http.MethodPost, "/sessions", handler.CreateChatSession, huma.Operation{
		OperationID: "create-chat-session",
		Summary:     "Create a new chat session",
		Description: "Create a new chat session for a transcription",
		Tags:        []string{"chat"},
		RequestBody: requestBody("application/json", schemaOf[ChatCreateRequest](spec), true, "Chat session creation request"),
		Responses: map[string]*huma.Response{
			"201": jsonResponse[ChatSessionResponse](spec, "Created"),
			"400": errorResponse("Bad Request"),
			"404": errorResponse("Not Found"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	chat.route(http.MethodGet, "/transcriptions/{transcription_id}/sessions", handler.GetChatSessions, huma.Operation{
		OperationID: "get-chat-sessions",
		Summary:     "Get chat sessions for a transcription",
		Description: "Get all chat sessions for a specific transcription",
		Tags:        []string{"chat"},
		Parameters: []*huma.Param{
			stringParam("path", "transcription_id", "Transcription ID", true),
		},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[[]ChatSessionResponse](spec, "OK"),
			"400": errorResponse("Bad Request"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	chat.route(http.MethodGet, "/sessions/{session_id}", handler.GetChatSession, huma.Operation{
		OperationID: "get-chat-session",
		Summary:     "Get a chat session with messages",
		Description: "Get a specific chat session with all its messages",
		Tags:        []string{"chat"},
		Parameters: []*huma.Param{
			stringParam("path", "session_id", "Chat Session ID", true),
		},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[ChatSessionWithMessages](spec, "OK"),
			"400": errorResponse("Bad Request"),
			"404": errorResponse("Not Found"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	chat.route(http.MethodPost, "/sessions/{session_id}/messages", handler.SendChatMessage, huma.Operation{
		OperationID: "send-chat-message",
		Summary:     "Send a message to a chat session",
		Description: "Send a message to a chat session and get streaming response",
		Tags:        []string{"chat"},
		Parameters: []*huma.Param{
			stringParam("path", "session_id", "Chat Session ID", true),
		},
		RequestBody: requestBody("application/json", schemaOf[ChatMessageRequest](spec), true, "Message content"),
		Responses: map[string]*huma.Response{
			"200": mediaResponse("Streaming response", &huma.Schema{Type: "string"}, "text/plain"),
			"400": errorResponse("Bad Request"),
			"404": errorResponse("Not Found"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	chat.route(http.MethodPut, "/sessions/{session_id}/title", handler.UpdateChatSessionTitle, huma.Operation{
		OperationID: "update-chat-session-title",
		Summary:     "Update chat session title",
		Description: "Update the title of a chat session",
		Tags:        []string{"chat"},
		Parameters: []*huma.Param{
			stringParam("path", "session_id", "Chat Session ID", true),
		},
		RequestBody: requestBody("application/json", schemaOf[map[string]string](spec), true, "Title update request"),
		Responses: map[string]*huma.Response{
			"200": jsonResponse[ChatSessionResponse](spec, "OK"),
			"400": errorResponse("Bad Request"),
			"404": errorResponse("Not Found"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	chat.route(http.MethodPost, "/sessions/{session_id}/title/auto", handler.AutoGenerateChatTitle, huma.Operation{
		OperationID: "auto-generate-chat-title",
		Summary:     "Auto-generate chat session title",
		Description: "Uses the configured LLM to summarize the first exchange into a concise title. Only updates if the current title appears default/user-unset.",
		Tags:        []string{"chat"},
		Parameters: []*huma.Param{
			stringParam("path", "session_id", "Chat Session ID", true),
		},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[ChatSessionResponse](spec, "OK"),
			"400": errorResponse("Bad Request"),
			"404": errorResponse("Not Found"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	chat.route(http.MethodDelete, "/sessions/{session_id}", handler.DeleteChatSession, huma.Operation{
		OperationID: "delete-chat-session",
		Summary:     "Delete a chat session",
		Description: "Delete a chat session and all its messages",
		Tags:        []string{"chat"},
		Parameters: []*huma.Param{
			stringParam("path", "session_id", "Chat Session ID", true),
		},
		Responses: map[string]*huma.Response{
			"204": {Description: "No Content"},
			"400": errorResponse("Bad Request"),
			"404": errorResponse("Not Found"),
			"500": errorResponse("Internal Server Error"),
		},
	})

	// Notes routes (require authentication)
	notes := v1.authenticated("/notes", authService)
	notes.route(http.MethodGet, "/{note_id}", handler.GetNote, huma.Operation{
		OperationID: "get-note",
		Summary:     "Get a note",
		Description: "Get a note by its ID",
		Tags:        []string{"notes"},
		Parameters: []*huma.Param{
			stringParam("path", "note_id", "Note ID", true),
		},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[models.Note](spec, "OK"),
			"404": errorResponse("Not Found"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	notes.route(http.MethodPut, "/{note_id}", handler.UpdateNote, huma.Operation{
		OperationID: "update-note",
		Summary:     "Update a note",
		Description: "Update the content of a note",
		Tags:        []string{"notes"},
		Parameters: []*huma.Param{
			stringParam("path", "note_id", "Note ID", true),
		},
		RequestBody: requestBody("application/json", schemaOf[NoteUpdateRequest](spec), true, "Note update payload"),
		Responses: map[string]*huma.Response{
			"200": jsonResponse[models.Note](spec, "OK"),
			"400": errorResponse("Bad Request"),
			"404": errorResponse("Not Found"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	notes.route(http.MethodDelete, "/{note_id}", handler.DeleteNote, huma.Operation{
		OperationID: "delete-note",
		Summary:     "Delete a note",
		Description: "Delete a note by its ID",
		Tags:        []string{"notes"},
		Parameters: []*huma.Param{
			stringParam("path", "note_id", "Note ID", true),
		},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[map[string]string](spec, "OK"),
			"500": errorResponse("Internal Server Error"),
		},
	})

	// Summarization route (require authentication)
	summarize := v1.authenticated("/summarize", authService)
	summarize.route(http.MethodPost, "/", handler.Summarize, huma.Operation{
		OperationID: "summarize",
		Summary:     "Summarize content",
		Description: "Stream an LLM-generated summary for provided content; persists latest summary for the transcription",
		Tags:        []string{"summarize"},
		RequestBody: requestBody("application/json", schemaOf[SummarizeRequest](spec), true, "Summarize request"),
		Responses: map[string]*huma.Response{
			"200": mediaResponse("Event stream", &huma.Schema{Type: "string"}, "text/event-stream"),
			"400": errorResponse("Bad Request"),
			"500": errorResponse("Internal Server Error"),
		},
	})
}
