package api

import (
	"encoding/json"
	"path"
	"regexp"
	"strings"
	"testing"

	apidocs "github.com/jaysqvl/Jotist/api-docs"
)

func TestSwaggerPathsResolveToServerRoutes(t *testing.T) {
	var spec struct {
		BasePath string                                `json:"basePath"`
		Paths    map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal([]byte(apidocs.SwaggerInfo.ReadDoc()), &spec); err != nil {
		t.Fatal(err)
	}
	if len(spec.Paths) == 0 {
		t.Fatal("generated Swagger spec has no paths")
	}

	parameter := regexp.MustCompile(`/:[^/]+`)
	routes := make(map[string]bool)
	for _, route := range SetupRoutes(&Handler{}, nil).Routes() {
		// Match route shapes. The parameter's spelling is local to each handler.
		name := parameter.ReplaceAllString(route.Path, "/{}")
		routes[route.Method+" "+path.Clean(name)] = true
	}
	swaggerParameter := regexp.MustCompile(`/\{[^/]+\}`)
	for endpoint, operations := range spec.Paths {
		// Swagger paths are relative to basePath, including ones with a leading slash.
		url := path.Join("/", spec.BasePath, swaggerParameter.ReplaceAllString(endpoint, "/{}"))
		for method := range operations {
			switch method {
			case "get", "post", "put", "patch", "delete", "head", "options":
				if !routes[strings.ToUpper(method)+" "+url] {
					t.Errorf("Swagger %s %s resolves to unregistered path %s", strings.ToUpper(method), endpoint, url)
				}
			}
		}
	}
}
