package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
)

func TestFrontendRoutes(t *testing.T) {
	files := fstest.MapFS{
		"index.html":           {Data: []byte("<!doctype html><title>Jotist</title>")},
		"assets/app.js":        {Data: []byte("console.log('jotist')")},
		"jotist-logo.svg":      {Data: []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)},
		"manifest.webmanifest": {Data: []byte(`{"name":"Jotist"}`)},
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.NoRoute(gin.WrapH(newSPAHandler(files)))

	for _, tc := range []struct {
		name, method, path, contentType string
		status                          int
	}{
		{"root", "GET", "/", "text/html", 200},
		{"client route", "GET", "/audio/recording-1", "text/html", 200},
		{"client trailing slash", "GET", "/settings/", "text/html", 200},
		{"client route head", "HEAD", "/settings", "text/html", 200},
		{"bundle", "GET", "/assets/app.js", "javascript", 200},
		{"logo", "GET", "/jotist-logo.svg", "image/svg+xml", 200},
		{"manifest", "GET", "/manifest.webmanifest", "application/manifest+json", 200},
		{"missing bundle", "GET", "/assets/old.js", "text/plain", 404},
		{"missing asset without extension", "GET", "/assets/missing", "text/plain", 404},
		{"asset directory", "GET", "/assets/", "text/plain", 404},
		{"missing pwa file", "GET", "/sw.js", "text/plain", 404},
		{"missing api", "GET", "/api/v1/missing", "application/json", 404},
		{"api post", "POST", "/api/v1/missing", "application/json", 404},
		{"invalid path", "GET", "/../index.html", "text/plain", 403},
		{"unsupported method", "POST", "/settings", "text/plain", 405},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(tc.method, tc.path, nil))
			if response.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", response.Code, tc.status, response.Body.String())
			}
			if got := response.Header().Get("Content-Type"); !strings.Contains(got, tc.contentType) {
				t.Errorf("Content-Type = %q, want %q", got, tc.contentType)
			}
			if tc.method == "HEAD" && response.Body.Len() != 0 {
				t.Error("HEAD returned a response body")
			}
		})
	}

	t.Run("asset range", func(t *testing.T) {
		request := httptest.NewRequest("GET", "/assets/app.js", nil)
		request.Header.Set("Range", "bytes=0-6")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusPartialContent || response.Body.String() != "console" {
			t.Fatalf("range response = %d %q", response.Code, response.Body.String())
		}
	})
}
