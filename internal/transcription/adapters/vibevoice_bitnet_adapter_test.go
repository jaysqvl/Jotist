package adapters

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scriberr/internal/transcription/interfaces"
)

func TestBitNetWaitingPreparationCanCancel(t *testing.T) {
	adapter := NewVibeVoiceBitNetAdapter(t.TempDir())
	release, err := lockPythonPreparation(context.Background(), adapter.envPath)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- adapter.PrepareEnvironment(ctx) }()
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("waiting preparation returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled BitNet preparation kept waiting for another installation")
	}
}

func TestBitNetDoesNotAdvertiseNativeSpeakers(t *testing.T) {
	adapter := NewVibeVoiceBitNetAdapter(t.TempDir())
	if adapter.GetCapabilities().Features["integrated_diarization"] {
		t.Fatal("compressed 1.5B model has no documented native speaker output")
	}
	if err := adapter.ValidateParameters(map[string]interface{}{"device": "cuda"}); err == nil {
		t.Fatal("CPU-only runtime accepted CUDA")
	}
	if err := adapter.ValidateParameters(map[string]interface{}{"diarize": true, "align_words": false}); err == nil {
		t.Fatal("coarse chunk bounds must not be used for external speaker assignment")
	}
	if err := adapter.ValidateParameters(map[string]interface{}{"diarize_model": "native"}); err == nil {
		t.Fatal("native speakers must be rejected")
	}
	if err := adapter.ValidateParameters(map[string]interface{}{"diarize": true, "align_words": true, "device": "cpu"}); err != nil {
		t.Fatal(err)
	}
}

func TestBitNetChunksBeforeSeparateWordAlignment(t *testing.T) {
	directory := t.TempDir()
	bin := filepath.Join(directory, "bin")
	if err := os.MkdirAll(bin, 0755); err != nil {
		t.Fatal(err)
	}
	writeExecutable := func(path, script string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(script), 0755); err != nil {
			t.Fatal(err)
		}
	}
	writeExecutable(filepath.Join(bin, "ffprobe"), "#!/bin/sh\nprintf '62.5\\n'\n")
	writeExecutable(filepath.Join(bin, "ffmpeg"), "#!/bin/sh\nfor last; do :; done\nprintf 'audio fixture' > \"$last\"\n")
	writeExecutable(filepath.Join(bin, "uv"), `#!/bin/sh
if [ "$1" = "sync" ]; then exit 0; fi
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--output" ]; then
    shift
    test "$HF_TOKEN" = "test-token" || exit 3
    printf '%s' '{"text":"hello hello hello","language":"en","segments":[{"start":0.1,"end":0.8,"text":"hello"},{"start":30.1,"end":30.8,"text":"hello"},{"start":60.1,"end":60.8,"text":"hello"}],"word_segments":[{"start":0.1,"end":0.8,"word":"hello"},{"start":30.1,"end":30.8,"word":"hello"},{"start":60.1,"end":60.8,"word":"hello"}],"metadata":{"resolved_device":"cpu"}}' > "$1"
    exit 0
  fi
  shift
done
exit 0
`)
	adapter := NewVibeVoiceBitNetAdapter(filepath.Join(directory, "runtime"))
	writeExecutable(adapter.binaryPath(), "#!/bin/sh\nprintf 'hello' > \"$SCRIBERR_RESULT_PATH\"\nprintf 'chunk\\n' >> \"$CHUNK_LOG\"\n")
	adapter.initialized = true // Native model setup is outside this wiring test.
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CHUNK_LOG", filepath.Join(directory, "chunks"))
	inputPath := filepath.Join(directory, "input.wav")
	if err := os.WriteFile(inputPath, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := adapter.Transcribe(context.Background(), interfaces.AudioInput{FilePath: inputPath, Format: "wav", Size: 7}, map[string]interface{}{"diarize": true, "diarize_model": "pyannote", "hf_token": "test-token"}, interfaces.ProcessingContext{JobID: "test", TempDirectory: directory, OutputDirectory: directory})
	if err != nil {
		t.Fatal(err)
	}
	chunks, err := os.ReadFile(filepath.Join(directory, "chunks"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(chunks), "chunk") != 3 {
		t.Fatal("62.5-second input must use three bounded windows")
	}
	if len(result.WordSegments) != 3 || result.WordSegments[2].Start != 60.1 {
		t.Fatal("alignment word offsets were lost")
	}
	if result.Metadata["resolved_device"] != "cpu" || result.Metadata["timestamp_source"] != "forced_alignment" {
		t.Fatal("actual runtime metadata is missing")
	}
}

func TestBitNetUsesDocumentedPlainTextPromptAndContext(t *testing.T) {
	adapter := NewVibeVoiceBitNetAdapter(t.TempDir())
	args := adapter.buildArgs("/tmp/chunk.wav", map[string]interface{}{"context": "Database planning", "context_terms": "PostgreSQL"})
	assertArgValue(t, args, "--prompt-format", "text")
	assertArgValue(t, args, "--context", "Database planning\nVocabulary: PostgreSQL")
	assertArgValue(t, args, "--max-tokens", "16384")
}

func TestBitNetOutputPatchFailsClosedAndPreservesSurroundingCode(t *testing.T) {
	source := "before\n        // Parse and display transcription segments\nlossy formatter\n        // Print timing summary to stderr\nafter"
	patched, err := patchVibeBitNetCLI(source)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(patched, "before\n") || !strings.HasSuffix(patched, "after") || strings.Contains(patched, "lossy formatter") {
		t.Fatal("patch changed surrounding inference code")
	}
	for _, guard := range []string{"new_token != EOG_IM_END", "new_token != EOG_ENDOFTEXT", "SCRIBERR_RESULT_PATH", "return 2;", "std::fwrite(output_text.data()"} {
		if !strings.Contains(patched, guard) {
			t.Fatalf("missing output integrity guard %s", guard)
		}
	}
	if _, err := patchVibeBitNetCLI("unexpected upstream source"); err == nil {
		t.Fatal("unknown upstream contract should fail before building")
	}
}
