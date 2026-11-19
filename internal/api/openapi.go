package api

import (
	"encoding/json"
	"path"
	"reflect"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/gin-gonic/gin"
	"github.com/jaysqvl/Jotist/internal/auth"
	"github.com/jaysqvl/Jotist/pkg/middleware"
	"gorm.io/gorm"
)

// OpenAPISpec builds the same contract as the server, without starting
// services, opening a database or loading the frontend.
func OpenAPISpec() ([]byte, error) {
	spec := registerRoutes(gin.New(), &Handler{}, nil)
	return marshalOpenAPI(spec)
}

func marshalOpenAPI(spec *huma.OpenAPI) ([]byte, error) {
	data, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func newOpenAPI() *huma.OpenAPI {
	registry := huma.NewMapRegistry("#/components/schemas/", schemaName)
	// GORM's soft-delete timestamp is serialized as a timestamp or null.
	registry.RegisterTypeAlias(reflect.TypeFor[gorm.DeletedAt](), reflect.TypeFor[*time.Time]())
	return &huma.OpenAPI{
		OpenAPI: "3.1.0",
		Info: &huma.Info{
			Title:       "Jotist API",
			Version:     "1.0",
			Description: "Self-hosted audio transcription with local models and optional cloud providers",
			Contact:     &huma.Contact{Name: "Jotist support", URL: "https://github.com/jaysqvl/Jotist/issues"},
			License:     &huma.License{Name: "MIT", URL: "https://opensource.org/licenses/MIT"},
		},
		Servers: []*huma.Server{{URL: "http://localhost:8080", Description: "Change this to your Jotist server"}},
		Components: &huma.Components{
			Schemas: registry,
			SecuritySchemes: map[string]*huma.SecurityScheme{
				"ApiKeyAuth": {Type: "apiKey", In: "header", Name: "X-API-Key"},
				"BearerAuth": {Type: "http", Scheme: "bearer", BearerFormat: "JWT"},
			},
		},
	}
}

func schemaName(t reflect.Type, hint string) string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	name := huma.DefaultSchemaNamer(t, hint)
	if t.PkgPath() != "" {
		return path.Base(t.PkgPath()) + "." + name
	}
	return name
}

type routeGroup struct {
	*gin.RouterGroup
	spec     *huma.OpenAPI
	security []map[string][]string
}

func (r routeGroup) group(prefix string, handlers ...gin.HandlerFunc) routeGroup {
	r.RouterGroup = r.Group(prefix, handlers...)
	return r
}

func (r routeGroup) signedIn(prefix string, service *auth.AuthService) routeGroup {
	r = r.group(prefix, middleware.JWTOnlyMiddleware(service))
	r.security = []map[string][]string{{"BearerAuth": {}}}
	return r
}

func (r routeGroup) authenticated(prefix string, service *auth.AuthService) routeGroup {
	r = r.group(prefix, middleware.AuthMiddleware(service))
	r.security = []map[string][]string{{"ApiKeyAuth": {}}, {"BearerAuth": {}}}
	return r
}

var ginPath = strings.NewReplacer("{", ":", "}", "")

// Register one path for both Gin and Huma. Existing handlers keep responsibility
// for request parsing, validation and responses, including streamed bodies.
func (r routeGroup) route(method, relativePath string, handler gin.HandlerFunc, operation huma.Operation) {
	r.Handle(method, ginPath.Replace(relativePath), handler)
	operation.Method = method
	operation.Path = strings.TrimSuffix(r.BasePath(), "/") + relativePath
	operation.Security = r.security
	if len(r.security) > 0 && operation.Responses["401"] == nil {
		operation.Responses["401"] = errorResponse("Authentication required")
	}
	r.spec.AddOperation(&operation)
}

func schemaOf[T any](spec *huma.OpenAPI) *huma.Schema {
	return spec.Components.Schemas.Schema(reflect.TypeFor[T](), true, "")
}

func requestBody(mediaType string, schema *huma.Schema, required bool, description string) *huma.RequestBody {
	return &huma.RequestBody{
		Description: description,
		Required:    required,
		Content:     map[string]*huma.MediaType{mediaType: {Schema: schema}},
	}
}

func mediaResponse(description string, schema *huma.Schema, types ...string) *huma.Response {
	response := &huma.Response{Description: description, Content: make(map[string]*huma.MediaType, len(types))}
	for _, mediaType := range types {
		response.Content[mediaType] = &huma.MediaType{Schema: schema}
	}
	return response
}

func jsonResponse[T any](spec *huma.OpenAPI, description string) *huma.Response {
	return mediaResponse(description, schemaOf[T](spec), "application/json")
}

func stringParam(location, name, description string, required bool) *huma.Param {
	return &huma.Param{In: location, Name: name, Description: description, Required: required, Schema: &huma.Schema{Type: huma.TypeString}}
}

func binarySchema() *huma.Schema {
	return &huma.Schema{Type: huma.TypeString, Format: "binary"}
}

func errorResponse(description string) *huma.Response {
	return mediaResponse(description, &huma.Schema{
		Type: huma.TypeObject, Properties: map[string]*huma.Schema{"error": {Type: huma.TypeString}}, Required: []string{"error"},
	}, "application/json")
}
