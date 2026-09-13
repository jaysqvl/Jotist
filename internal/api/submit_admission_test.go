package api

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"scriberr/internal/config"
	"scriberr/internal/database"
	"scriberr/internal/models"
	"scriberr/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type submitUploadProbe struct {
	service.FileService
	saves int
}

func (p *submitUploadProbe) SaveUpload(file *multipart.FileHeader, directory string) (string, error) {
	p.saves++
	return p.FileService.SaveUpload(file, directory)
}

func TestSubmitRejectsInvalidParametersBeforeSavingAudio(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&models.UploadSession{}, &models.UploadSessionFile{}))
	previousDB := database.DB
	database.DB = db
	t.Cleanup(func() { database.DB = previousDB })

	for _, test := range []struct {
		name, field, value, errorText string
	}{
		{"context too long", "transcription_context", strings.Repeat("x", 4001), "Transcription context"},
		{"vocabulary too long", "transcription_context_terms", strings.Repeat("x", 8001), "Vocabulary"},
		{"invalid device", "diarization_device", "unknown-device", "diarization_device"},
		{"invalid diarizer", "diarize_model", "unknown-diarizer", "diarize_model"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := &config.Config{UploadDir: t.TempDir(), MaxUploadBytes: 1024 * 1024}
			files := &submitUploadProbe{FileService: service.NewFileService()}
			handler := &Handler{config: cfg, fileService: files, resourceAdmission: newResourceAdmission(cfg)}
			var body bytes.Buffer
			form := multipart.NewWriter(&body)
			part, err := form.CreateFormFile("audio", "meeting.wav")
			require.NoError(t, err)
			_, err = part.Write([]byte("audio fixture"))
			require.NoError(t, err)
			require.NoError(t, form.WriteField(test.field, test.value))
			require.NoError(t, form.Close())

			request := httptest.NewRequest(http.MethodPost, "/submit", &body)
			request.Header.Set("Content-Type", form.FormDataContentType())
			router := gin.New()
			router.POST("/submit", handler.SubmitJob)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
			require.Contains(t, response.Body.String(), test.errorText)
			require.Zero(t, files.saves, "invalid requests must not copy audio into persistent uploads")
			entries, err := os.ReadDir(cfg.UploadDir)
			require.NoError(t, err)
			require.Empty(t, entries)
			require.Empty(t, handler.resourceAdmission.uploadSlots, "admission slot must be released")
			require.Zero(t, handler.resourceAdmission.reservedDisk, "disk reservation must be released")
		})
	}
}
