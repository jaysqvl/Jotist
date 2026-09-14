package adapters

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"scriberr/internal/transcription/interfaces"
	"scriberr/pkg/logger"
)

func TestFailedModelProcessingDoesNotLogCompletion(t *testing.T) {
	var output bytes.Buffer
	log := logger.Get()
	previous := log.Logger
	log.Logger = slog.New(slog.NewTextHandler(&output, nil))
	t.Cleanup(func() { log.Logger = previous })

	// Missing audio exercises each adapter's real error return without model
	// downloads or provider calls. Previously every deferred event said success.
	input := interfaces.AudioInput{FilePath: t.TempDir() + "/missing.wav"}
	proc := interfaces.ProcessingContext{JobID: "log-check", OutputDirectory: t.TempDir()}
	for _, adapter := range []interfaces.TranscriptionAdapter{
		NewWhisperXAdapter(t.TempDir()),
		NewParakeetAdapter(t.TempDir()),
		NewCanaryAdapter(t.TempDir()),
		NewCanaryQwenAdapter(t.TempDir()),
		NewOpenAIAdapter(""),
	} {
		output.Reset()
		if _, err := adapter.Transcribe(context.Background(), input, nil, proc); err == nil {
			t.Fatalf("%T accepted missing audio", adapter)
		}
		assertFailureLog(t, output.String())
	}
	for _, adapter := range []interfaces.DiarizationAdapter{
		NewPyAnnoteAdapter(t.TempDir()),
		NewSortformerAdapter(t.TempDir()),
	} {
		output.Reset()
		if _, err := adapter.Diarize(context.Background(), input, nil, proc); err == nil {
			t.Fatalf("%T accepted missing audio", adapter)
		}
		assertFailureLog(t, output.String())
	}
}

func assertFailureLog(t *testing.T, output string) {
	t.Helper()
	if !strings.Contains(output, "Model processing failed") || strings.Contains(output, "Model processing completed") {
		t.Fatalf("incorrect processing outcome: %s", output)
	}
}
