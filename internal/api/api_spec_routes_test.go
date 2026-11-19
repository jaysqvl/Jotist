package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	docs "github.com/jaysqvl/Jotist/api-docs"
	"github.com/jaysqvl/Jotist/internal/auth"
	"github.com/jaysqvl/Jotist/internal/config"
	"github.com/stretchr/testify/require"
)

type specRoute struct {
	method string
	path   string
}

// Match parameterized segments by shape. Keep the trailing slash, because
// copied requests should reach the registered handler directly.
func routeShape(path string) string {
	segments := strings.Split(path, "/")
	for index, segment := range segments {
		if strings.HasPrefix(segment, ":") || (strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}")) {
			segments[index] = "{}"
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
		key := specRoute{route.Method, routeShape(route.Path)}
		registered[key] = true
	}

	w := httptest.NewRecorder()
	docs.ServeSpecification(w, httptest.NewRequest("GET", "/", nil))
	var specification struct {
		OpenAPI string                                `json:"openapi"`
		Paths   map[string]map[string]json.RawMessage `json:"paths"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &specification))
	require.True(t, strings.HasPrefix(specification.OpenAPI, "3."))
	require.NotEmpty(t, specification.Paths)
	documented := make(map[specRoute]bool)
	for path, operations := range specification.Paths {
		for name := range operations {
			switch name {
			case "get", "post", "put", "patch", "delete", "head", "options", "trace":
				key := specRoute{strings.ToUpper(name), routeShape(path)}
				documented[key] = true
				if !registered[key] {
					t.Errorf("documented operation has no registered route: %s %s", key.method, key.path)
				}
				if reason := excluded[key]; reason != "" {
					t.Errorf("remove the exclusion for documented operation %s %s: %s", key.method, key.path, reason)
				}
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
