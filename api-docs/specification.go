package api_docs

import (
	_ "embed"
	"net/http"
)

//go:embed openapi.json
var specification []byte

// ServeSpecification writes the specification packaged with this server build.
func ServeSpecification(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(specification)
}
