package api

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/gin-gonic/gin"
	"github.com/jaysqvl/Jotist/internal/auth"
)

func registerRoutes(router *gin.Engine, handler *Handler, authService *auth.AuthService) *huma.OpenAPI {
	spec := newOpenAPI()
	root := routeGroup{RouterGroup: &router.RouterGroup, spec: spec}
	// Health check endpoint (no auth required)
	root.route(http.MethodGet, "/health", handler.HealthCheck, huma.Operation{
		OperationID: "health-check",
		Summary:     "Service liveness and release identity",
		Description: "Confirms the HTTP process is responding. Use /ready for database and queue readiness.",
		Tags:        []string{"health"},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[livenessResponse](spec, "OK"),
		},
	})
	root.route(http.MethodGet, "/ready", handler.ReadinessCheck, huma.Operation{
		OperationID: "readiness-check",
		Summary:     "Check whether the service can accept transcription work",
		Description: "Read-only database and queue startup checks. Busy workers remain ready. Failure responses omit internal errors and private paths.",
		Tags:        []string{"health"},
		Responses: map[string]*huma.Response{
			"200": jsonResponse[readinessResponse](spec, "OK"),
			"503": jsonResponse[readinessResponse](spec, "Service Unavailable"),
		},
	})

	// CLI install script alias (root level for easier access)
	router.GET("/install.sh", handler.GetInstallScript)
	router.GET("/install-cli.sh", handler.GetInstallScript)

	// API v1 routes
	v1 := root.group("/api/v1")
	registerModelsRoutes(v1, handler, authService)
	registerAccountRoutes(v1, handler, authService)
	registerTranscriptionRoutes(v1, handler, authService)
	registerProfilesRoutes(v1, handler, authService)
	registerPreferencesRoutes(v1, handler, authService)
	registerContentRoutes(v1, handler, authService)
	registerEventsRoutes(v1, handler, authService)
	return spec
}
