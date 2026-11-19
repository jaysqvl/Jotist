package api

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jaysqvl/Jotist/internal/auth"
)

func registerAccountRoutes(v1 routeGroup, handler *Handler, authService *auth.AuthService) {
	spec := v1.spec
	// Authentication routes (no auth required)
	auth := v1.group("/auth")
	auth.route(http.MethodGet, "/registration-status", handler.GetRegistrationStatus, huma.Operation{
		OperationID: "get-registration-status",
		Summary:     "Check registration status",
		Description: "Check if the application requires initial user registration",
		Tags:        []string{"auth"},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[RegistrationStatusResponse](spec, "OK"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	auth.route(http.MethodPost, "/register", handler.Register, huma.Operation{
		OperationID: "register",
		Summary:     "Register initial admin user",
		Description: "Register the initial admin user (only allowed when no users exist)",
		Tags:        []string{"auth"},
		RequestBody: requestBody("application/json", schemaOf[RegisterRequest](spec), true, "Registration details"),
		Responses: map[string]*huma.Response{
			"201": jsonResponse[LoginResponse](spec, "Created"),
			"400": errorResponse("Bad Request"),
			"409": errorResponse("Conflict"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	auth.route(http.MethodPost, "/login", handler.Login, huma.Operation{
		OperationID: "login",
		Summary:     "Login",
		Description: "Authenticate user and return JWT token",
		Tags:        []string{"auth"},
		RequestBody: requestBody("application/json", schemaOf[LoginRequest](spec), true, "User credentials"),
		Responses: map[string]*huma.Response{
			"200": jsonResponse[LoginResponse](spec, "OK"),
			"400": errorResponse("Bad Request"),
			"401": errorResponse("Unauthorized"),
			"429": errorResponse("Too Many Requests"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	auth.route(http.MethodPost, "/refresh", handler.Refresh, huma.Operation{
		OperationID: "refresh",
		Summary:     "Refresh access token",
		Description: "Rotate refresh token and return new access token",
		Tags:        []string{"auth"},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[RefreshTokenResponse](spec, "OK"),
			"401": errorResponse("Unauthorized"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	auth.route(http.MethodPost, "/logout", handler.Logout, huma.Operation{
		OperationID: "logout",
		Summary:     "Logout user",
		Description: "Logout user and invalidate token (client-side action)",
		Tags:        []string{"auth"},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[map[string]string](spec, "OK"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	auth.RouterGroup.POST("/cli/start", handler.StartCLIAuthorization)
	auth.RouterGroup.POST("/cli/token", handler.RedeemCLIAuthorization)

	// Account management routes (require authentication)
	authProtected := auth.signedIn("", authService)
	authProtected.route(http.MethodPost, "/change-password", handler.ChangePassword, huma.Operation{
		OperationID: "change-password",
		Summary:     "Change user password",
		Description: "Change the current user's password",
		Tags:        []string{"auth"},
		RequestBody: requestBody("application/json", schemaOf[ChangePasswordRequest](spec), true, "Password change details"),
		Responses: map[string]*huma.Response{
			"200": jsonResponse[map[string]string](spec, "OK"),
			"400": errorResponse("Bad Request"),
			"401": errorResponse("Unauthorized"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	authProtected.route(http.MethodPost, "/change-username", handler.ChangeUsername, huma.Operation{
		OperationID: "change-username",
		Summary:     "Change username",
		Description: "Change the current user's username",
		Tags:        []string{"auth"},
		RequestBody: requestBody("application/json", schemaOf[ChangeUsernameRequest](spec), true, "Username change details"),
		Responses: map[string]*huma.Response{
			"200": jsonResponse[map[string]string](spec, "OK"),
			"400": errorResponse("Bad Request"),
			"401": errorResponse("Unauthorized"),
			"409": errorResponse("Conflict"),
			"500": errorResponse("Internal Server Error"),
		},
	})

	// CLI Authentication routes
	cliAuth := authProtected.group("/cli")
	cliAuth.RouterGroup.GET("/authorize", handler.AuthorizeCLI)
	cliAuth.RouterGroup.POST("/authorize", handler.ConfirmCLIAuthorization)

	// Public CLI routes (no auth required to download, script handles auth)
	cliPublic := v1.group("/cli")
	cliPublic.RouterGroup.GET("/download", handler.DownloadCLIBinary)
	cliPublic.RouterGroup.GET("/install", handler.GetInstallScript)
	// API Key management routes (require authentication)
	apiKeys := v1.signedIn("/api-keys", authService)
	apiKeys.route(http.MethodGet, "/", handler.ListAPIKeys, huma.Operation{
		OperationID: "list-api-keys",
		Summary:     "List API keys",
		Description: "Get all API keys for the current user (without exposing the actual keys)",
		Tags:        []string{"api-keys"},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[APIKeysWrapper](spec, "OK"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	apiKeys.route(http.MethodPost, "/", handler.CreateAPIKey, huma.Operation{
		OperationID: "create-api-key",
		Summary:     "Create API key",
		Description: "Create a new API key for external API access",
		Tags:        []string{"api-keys"},
		RequestBody: requestBody("application/json", schemaOf[CreateAPIKeyRequest](spec), true, "API key creation details"),
		Responses: map[string]*huma.Response{
			"200": jsonResponse[CreateAPIKeyResponse](spec, "OK"),
			"400": errorResponse("Bad Request"),
			"500": errorResponse("Internal Server Error"),
		},
	})
	apiKeys.route(http.MethodDelete, "/{id}", handler.DeleteAPIKey, huma.Operation{
		OperationID: "delete-api-key",
		Summary:     "Delete API key",
		Description: "Delete an API key",
		Tags:        []string{"api-keys"},
		Parameters: []*huma.Param{
			{In: "path", Name: "id", Description: "API Key ID", Required: true, Schema: &huma.Schema{Type: "integer"}},
		},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[map[string]string](spec, "OK"),
			"400": errorResponse("Bad Request"),
			"404": errorResponse("Not Found"),
			"500": errorResponse("Internal Server Error"),
		},
	})
}
