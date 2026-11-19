package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jaysqvl/Jotist/internal/auth"
	"github.com/jaysqvl/Jotist/internal/config"
	"github.com/jaysqvl/Jotist/internal/database"
	"github.com/jaysqvl/Jotist/internal/models"
	"github.com/jaysqvl/Jotist/internal/repository"
	"github.com/jaysqvl/Jotist/internal/service"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSwaggerSpecificationRequiresCurrentSessionAndDisablesCache(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&models.User{}, &models.RevokedAccessToken{}, &models.APIKey{}))
	previous := database.DB
	database.DB = db
	t.Cleanup(func() { database.DB = previous })

	const secret = "isolated-swagger-test"
	authService := auth.NewAuthService(secret)
	user := models.User{Username: "swagger-preview", Password: "unused"}
	require.NoError(t, db.Create(&user).Error)
	token, err := authService.GenerateToken(&user)
	require.NoError(t, err)
	expired, err := jwt.NewWithClaims(jwt.SigningMethodHS256, auth.Claims{
		UserID: user.ID, Username: user.Username, TokenVersion: user.TokenVersion,
		RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Hour))},
	}).SignedString([]byte(secret))
	require.NoError(t, err)
	key := models.APIKey{Key: "isolated-swagger-api-key", Name: "preview", IsActive: true}
	require.NoError(t, db.Create(&key).Error)
	router := SetupRoutes(&Handler{config: &config.Config{}}, authService)
	exported, err := OpenAPISpec()
	require.NoError(t, err)

	for _, tt := range []struct {
		name          string
		path          string
		authorization string
		key           string
		cookie        string
		want          int
	}{
		{name: "no credentials", want: http.StatusUnauthorized},
		{name: "bad token", authorization: "Bearer invalid", want: http.StatusUnauthorized},
		{name: "expired token", authorization: "Bearer " + expired, want: http.StatusUnauthorized},
		{name: "API key only", key: key.Key, want: http.StatusUnauthorized},
		{name: "cookie only", cookie: token, want: http.StatusUnauthorized},
		{name: "signed in", authorization: "Bearer " + token, want: http.StatusOK},
		{name: "catalog without credentials", path: "/api/v1/transcription/models", want: http.StatusUnauthorized},
		{name: "list without credentials", path: "/api/v1/transcription/list?page=1&limit=10", want: http.StatusUnauthorized},
		{name: "status without credentials", path: "/api/v1/transcription/sample/status", want: http.StatusUnauthorized},
		{name: "transcript without credentials", path: "/api/v1/transcription/sample/transcript?read_only=true", want: http.StatusUnauthorized},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := tt.path
			if path == "" {
				path = "/api/v1/docs/spec"
			}
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Authorization", tt.authorization)
			req.Header.Set("X-API-Key", tt.key)
			if tt.cookie != "" {
				req.AddCookie(&http.Cookie{Name: "jotist_access_token", Value: tt.cookie})
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.Equal(t, tt.want, w.Code)
			require.Equal(t, "private, no-store", w.Header().Get("Cache-Control"))
			if w.Code == http.StatusOK {
				require.Contains(t, w.Header().Get("Content-Type"), "application/json")
				require.Equal(t, exported, w.Body.Bytes())
			} else {
				require.NotContains(t, w.Body.String(), `"openapi"`)
			}
		})
	}
}

func TestReadOnlyTranscriptDoesNotBackfillLegacyRun(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&models.TranscriptionJob{}, &models.MultiTrackFile{}, &models.TranscriptionJobExecution{}))

	payload := `{"text":"Sample transcript","segments":[{"start":0,"end":1,"text":"Sample transcript"}]}`
	job := models.TranscriptionJob{ID: "sample", AudioPath: "sample.wav", Status: models.StatusCompleted, Transcript: &payload}
	require.NoError(t, db.Create(&job).Error)
	h := &Handler{
		jobRepo: repository.NewJobRepository(db), config: &config.Config{TranscriptsDir: t.TempDir()},
		fileService: service.NewFileService(),
	}
	router := gin.New()
	router.GET("/transcription/:id/transcript", h.GetTranscript)
	request := func(query string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/transcription/sample/transcript"+query, nil))
		return w
	}
	require.Equal(t, http.StatusBadRequest, request("?read_only=invalid").Code)

	readOnly := request("?read_only=true")
	require.Equal(t, http.StatusOK, readOnly.Code)
	require.Contains(t, readOnly.Body.String(), `"available":true`)
	require.Contains(t, readOnly.Body.String(), "Sample transcript")
	require.NotContains(t, readOnly.Body.String(), `"run_id"`)
	var count int64
	require.NoError(t, db.Model(&models.TranscriptionJobExecution{}).Count(&count).Error)
	require.Zero(t, count)

	regular := request("")
	require.Equal(t, http.StatusOK, regular.Code)
	require.Contains(t, regular.Body.String(), `"run_id"`)
	require.JSONEq(t, regular.Body.String(), request("?read_only=true").Body.String())
	require.NoError(t, db.Model(&models.TranscriptionJobExecution{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
}
