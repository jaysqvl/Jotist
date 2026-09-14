package api

import (
	"errors"
	"os"
	"testing"

	"scriberr/internal/models"
	"scriberr/internal/service"

	"github.com/stretchr/testify/require"
)

type cleanupFileService struct {
	service.FileService
	removed        []string
	fileError      error
	directoryError error
}

func (s *cleanupFileService) RemoveFile(path string) error {
	s.removed = append(s.removed, path)
	return s.fileError
}

func (s *cleanupFileService) RemoveDirectory(path string) error {
	s.removed = append(s.removed, path)
	return s.directoryError
}

func TestRecordingMediaCleanupAttemptsRemainingPathsAfterFailure(t *testing.T) {
	blocked := errors.New("directory cleanup failed")
	files := &cleanupFileService{directoryError: blocked, fileError: os.ErrNotExist}
	handler := &Handler{fileService: files}
	folder, aup := "tracks", "tracks/project.aup"
	err := handler.removeRecordingMedia(&models.TranscriptionJob{IsMultiTrack: true, MultiTrackFolder: &folder, AupFilePath: &aup})
	require.ErrorIs(t, err, blocked)
	require.Equal(t, []string{folder, aup}, files.removed)
}

func TestRecordingMediaCleanupTreatsAlreadyRemovedPathsAsClean(t *testing.T) {
	files := &cleanupFileService{fileError: os.ErrNotExist}
	handler := &Handler{fileService: files}
	require.NoError(t, handler.removeRecordingMedia(&models.TranscriptionJob{AudioPath: "already-removed.wav"}))
}
