package api

import (
	"context"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/jaysqvl/Jotist/internal/auth"
	"github.com/jaysqvl/Jotist/internal/config"
	"github.com/stretchr/testify/require"
)

type specRoute struct {
	method string
	path   string
}

func openAPIPath(ginPath string) string {
	segments := strings.Split(ginPath, "/")
	for index, segment := range segments {
		if name, dynamic := strings.CutPrefix(segment, ":"); dynamic {
			segments[index] = "{" + name + "}"
		}
	}
	return strings.Join(segments, "/")
}

func TestAPISpecMatchesRegisteredRoutes(t *testing.T) {
	// The CLI's bootstrap/authentication protocol and the reference's own spec
	// delivery endpoint are implementation routes outside the supported REST API.
	excluded := map[specRoute]string{
		{"GET", "/api/v1/docs/spec"}:           "embedded API reference bootstrap",
		{"GET", "/api/v1/auth/cli/authorize"}:  "CLI browser authorization",
		{"POST", "/api/v1/auth/cli/authorize"}: "CLI browser authorization",
		{"POST", "/api/v1/auth/cli/start"}:     "CLI session handshake",
		{"POST", "/api/v1/auth/cli/token"}:     "CLI session handshake",
		{"GET", "/api/v1/cli/download"}:        "CLI distribution",
		{"GET", "/api/v1/cli/install"}:         "CLI distribution",
		{"GET", "/install.sh"}:                 "CLI installer alias",
		{"GET", "/install-cli.sh"}:             "CLI installer alias",
	}
	handler := &Handler{config: &config.Config{}}
	router := SetupRoutes(handler, auth.NewAuthService("isolated-route-coverage"))
	registered := make(map[specRoute]bool)
	for _, route := range router.Routes() {
		key := specRoute{route.Method, openAPIPath(route.Path)}
		registered[key] = true
	}

	specification := loadOpenAPISpec(t)
	require.Equal(t, "3.1.0", specification.OpenAPI)
	require.NotEmpty(t, specification.Paths.Map())
	documented := make(map[specRoute]bool)
	for path, item := range specification.Paths.Map() {
		for method := range item.Operations() {
			key := specRoute{method, path}
			documented[key] = true
			if !registered[key] {
				t.Errorf("documented operation has no registered route: %s %s", key.method, key.path)
			}
			if reason := excluded[key]; reason != "" {
				t.Errorf("remove the exclusion for documented operation %s %s: %s", key.method, key.path, reason)
			}
		}
	}
	for key := range registered {
		if !documented[key] && excluded[key] == "" {
			t.Errorf("registered route missing from spec: %s %s", key.method, key.path)
		}
	}
	for key, reason := range excluded {
		if !registered[key] {
			t.Errorf("remove the exclusion for deleted route %s %s: %s", key.method, key.path, reason)
		}
	}
}

func loadOpenAPISpec(t *testing.T) *openapi3.T {
	t.Helper()
	data, err := OpenAPISpec()
	require.NoError(t, err)
	spec, err := openapi3.NewLoader().LoadFromData(data)
	require.NoError(t, err)
	return spec
}

func TestOpenAPIContract(t *testing.T) {
	spec := loadOpenAPISpec(t)
	require.NoError(t, spec.Validate(context.Background()))

	events := spec.Paths.Find("/api/v1/events/").Get
	require.Len(t, events.Parameters, 1)
	require.Equal(t, "job_id", events.Parameters[0].Value.Name)
	require.True(t, events.Parameters[0].Value.Required)
	require.Contains(t, events.Responses.Status(200).Value.Content, "text/event-stream")
	require.Contains(t, events.Responses.Status(400).Value.Content, "text/plain")

	multitrack := spec.Paths.Find("/api/v1/transcription/upload-multitrack").Post.RequestBody.Value
	require.True(t, multitrack.Required)
	parts := multitrack.Content["multipart/form-data"].Schema.Value
	require.ElementsMatch(t, []string{"aup", "files"}, parts.Required)
	require.Equal(t, "binary", parts.Properties["aup"].Value.Format)
	tracks := parts.Properties["files"].Value
	require.True(t, tracks.Type.Is("array"))
	require.EqualValues(t, 1, tracks.MinItems)
	require.Equal(t, "binary", tracks.Items.Value.Format)

	chunk := spec.Paths.Find("/api/v1/transcription/uploads/{id}/files/{file_id}/chunks/{index}").Put
	require.True(t, chunk.RequestBody.Value.Required)
	require.Len(t, chunk.RequestBody.Value.Content, 1)
	require.Equal(t, "binary", chunk.RequestBody.Value.Content["application/octet-stream"].Schema.Value.Format)

	create := spec.Paths.Find("/api/v1/transcription/uploads").Post.Responses.Status(200).Value.Content["application/json"].Schema.Value
	require.Contains(t, create.Required, "token")
	progress := spec.Paths.Find("/api/v1/transcription/uploads/{id}").Get.Responses.Status(200).Value.Content["application/json"].Schema.Value
	require.NotContains(t, progress.Properties, "token")

	settings := spec.Paths.Find("/api/v1/user/settings").Get
	require.Equal(t, openapi3.SecurityRequirements{{"BearerAuth": {}}}, *settings.Security)
	require.Equal(t, openapi3.SecurityRequirements{{"ApiKeyAuth": {}}, {"BearerAuth": {}}}, *events.Security)

	createProfile := spec.Paths.Find("/api/v1/profiles/").Post
	require.NotNil(t, createProfile.Responses.Status(200))
	require.Contains(t, createProfile.RequestBody.Value.Content["application/json"].Schema.Value.Required, "name")
	updateProfile := spec.Paths.Find("/api/v1/profiles/{id}").Put.RequestBody.Value.Content["application/json"].Schema.Value
	require.Contains(t, updateProfile.Required, "name")
	require.NotContains(t, updateProfile.Required, "expected_revision")
	require.NotNil(t, updateProfile.Properties["expected_revision"])

	queue := spec.Paths.Find("/api/v1/transcription/{id}/queue").Post.RequestBody.Value.Content["application/json"].Schema.Value
	require.Error(t, queue.VisitJSON(map[string]any{}, openapi3.EnableJSONSchema2020()))
	require.Error(t, queue.VisitJSON(map[string]any{"profile_id": " "}, openapi3.EnableJSONSchema2020()))
	require.NoError(t, queue.VisitJSON(map[string]any{"profile_id": "saved-profile"}, openapi3.EnableJSONSchema2020()))
	require.NoError(t, queue.VisitJSON(map[string]any{"parameters": map[string]any{}}, openapi3.EnableJSONSchema2020()))
	reorder := spec.Paths.Find("/api/v1/transcription/{id}/queue/order").Put.RequestBody.Value.Content["application/json"].Schema.Value
	require.Contains(t, reorder.Required, "ordered_ids")
	require.NoError(t, reorder.VisitJSON(map[string]any{"ordered_ids": []any{}}, openapi3.EnableJSONSchema2020()))
}
