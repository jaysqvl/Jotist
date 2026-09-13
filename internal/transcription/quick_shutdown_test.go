package transcription

import (
	"context"
	"github.com/stretchr/testify/require"
	"scriberr/internal/models"
	"strings"
	"testing"
	"time"
)

func TestQuickShutdownCancelsAndWaitsForWorkerCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	service := &QuickTranscriptionService{ctx: ctx, cancel: cancel}
	service.workers.Add(1)
	cancelObserved := make(chan struct{})
	cleanup := make(chan struct{})
	closed := make(chan struct{})
	go func() { defer service.workers.Done(); <-ctx.Done(); close(cancelObserved); <-cleanup }()
	go func() { service.Close(); close(closed) }()
	select {
	case <-cancelObserved:
	case <-time.After(time.Second):
		t.Fatal("worker did not receive cancellation")
	}
	select {
	case <-closed:
		t.Fatal("shutdown returned before cleanup")
	default:
	}
	_, err := service.SubmitQuickJob(strings.NewReader("fixture"), "audio.wav", models.WhisperXParams{})
	require.ErrorContains(t, err, "shutting down")
	close(cleanup)
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not wait for cleanup")
	}
	service.Close() // Idempotent; no channel double-close or added worker after wait.
}
