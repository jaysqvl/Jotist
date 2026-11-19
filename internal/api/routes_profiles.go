package api

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jaysqvl/Jotist/internal/auth"
	"github.com/jaysqvl/Jotist/internal/models"
	"github.com/jaysqvl/Jotist/internal/repository"
)

func registerProfilesRoutes(v1 routeGroup, handler *Handler, authService *auth.AuthService) {
	spec := v1.spec
	// Profile routes (require authentication)
	profiles := v1.authenticated("/profiles", authService)
	profiles.route(http.MethodGet, "/", handler.ListProfiles, huma.Operation{
		OperationID: "list-profiles",
		Summary:     "List transcription profiles",
		Description: "Get list of all transcription profiles",
		Tags:        []string{"profiles"},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[[]models.TranscriptionProfile](spec, "OK"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	profiles.route(http.MethodPost, "/", handler.CreateProfile, huma.Operation{
		OperationID: "create-profile",
		Summary:     "Create transcription profile",
		Description: "Create a new transcription profile",
		Tags:        []string{"profiles"},
		RequestBody: requestBody("application/json", schemaOf[models.TranscriptionProfile](spec), true, "Profile data"),
		Responses: map[string]*huma.Response{
			"200": jsonResponse[models.TranscriptionProfile](spec, "OK"),
			"400": errorResponse("Bad Request"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	profiles.route(http.MethodGet, "/{id}", handler.GetProfile, huma.Operation{
		OperationID: "get-profile",
		Summary:     "Get transcription profile",
		Description: "Get a transcription profile by ID",
		Tags:        []string{"profiles"},
		Parameters: []*huma.Param{
			stringParam("path", "id", "Profile ID", true),
		},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[models.TranscriptionProfile](spec, "OK"),
			"404": errorResponse("Not Found"),
		},
	})
	profiles.route(http.MethodPut, "/{id}", handler.UpdateProfile, huma.Operation{
		OperationID: "update-profile",
		Summary:     "Update transcription profile",
		Description: "Update a transcription profile",
		Tags:        []string{"profiles"},
		Parameters: []*huma.Param{
			stringParam("path", "id", "Profile ID", true),
		},
		RequestBody: requestBody("application/json", schemaOf[updateProfileRequest](spec), true, "Updated profile data"),
		Responses: map[string]*huma.Response{
			"200": jsonResponse[models.TranscriptionProfile](spec, "OK"),
			"400": errorResponse("Bad Request"),
			"404": errorResponse("Not Found"),
			"409": errorResponse("Profile changed since the supplied revision"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	profiles.route(http.MethodDelete, "/{id}", handler.DeleteProfile, huma.Operation{
		OperationID: "delete-profile",
		Summary:     "Delete transcription profile",
		Description: "Delete a transcription profile",
		Tags:        []string{"profiles"},
		Parameters: []*huma.Param{
			stringParam("path", "id", "Profile ID", true),
		},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[map[string]string](spec, "OK"),
			"404": errorResponse("Not Found"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	profiles.route(http.MethodPost, "/{id}/set-default", handler.SetDefaultProfile, huma.Operation{
		OperationID: "set-default-profile",
		Summary:     "Set default transcription profile",
		Description: "Mark the specified profile as the default profile",
		Tags:        []string{"profiles"},
		Parameters: []*huma.Param{
			stringParam("path", "id", "Profile ID", true),
		},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[map[string]any](spec, "OK"),
			"400": errorResponse("Bad Request"),
			"404": errorResponse("Not Found"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	profiles.route(http.MethodGet, "/{id}/adaptive-policy", handler.GetAdaptivePolicy, huma.Operation{
		OperationID: "get-adaptive-policy",
		Summary:     "Get profile learning state",
		Tags:        []string{"profiles"},
		Parameters: []*huma.Param{
			stringParam("path", "id", "Profile ID", true),
			stringParam("query", "scope_key", "Optional exact scope fingerprint", false),
		},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[repository.AdaptivePolicySnapshot](spec, "OK"),
		},
	})
	profiles.route(http.MethodPost, "/{id}/reset-adaptive", handler.ResetAdaptivePolicy, huma.Operation{
		OperationID: "reset-adaptive-policy",
		Summary:     "Reset profile learned starting plans",
		Tags:        []string{"profiles"},
		Parameters: []*huma.Param{
			stringParam("path", "id", "Profile ID", true),
		},
		RequestBody: requestBody("application/json", schemaOf[adaptiveProfileAction](spec), true, "Expected revision and generation"),
		Responses: map[string]*huma.Response{
			"200": jsonResponse[models.TranscriptionProfile](spec, "OK"),
			"409": jsonResponse[ErrorResponse](spec, "Conflict"),
		},
	})
	profiles.route(http.MethodPost, "/{id}/freeze-adaptive", handler.FreezeAdaptivePolicy, huma.Operation{
		OperationID: "freeze-adaptive-policy",
		Summary:     "Freeze a measured plan as fixed profile settings",
		Tags:        []string{"profiles"},
		Parameters: []*huma.Param{
			stringParam("path", "id", "Profile ID", true),
		},
		RequestBody: requestBody("application/json", schemaOf[adaptiveProfileAction](spec), true, "Selected plan ID and expected revision and generation"),
		Responses: map[string]*huma.Response{
			"200": jsonResponse[models.TranscriptionProfile](spec, "OK"),
			"409": jsonResponse[ErrorResponse](spec, "Conflict"),
		},
	})
	profiles.route(http.MethodPost, "/{id}/restore-revision", handler.RestoreProfileRevision, huma.Operation{
		OperationID: "restore-profile-revision",
		Summary:     "Restore saved profile parameters in a new revision",
		Tags:        []string{"profiles"},
		Parameters: []*huma.Param{
			stringParam("path", "id", "Profile ID", true),
		},
		RequestBody: requestBody("application/json", schemaOf[adaptiveProfileAction](spec), true, "Saved revision and expected current revision and generation"),
		Responses: map[string]*huma.Response{
			"200": jsonResponse[models.TranscriptionProfile](spec, "OK"),
			"409": jsonResponse[ErrorResponse](spec, "Conflict"),
		},
	})
}
