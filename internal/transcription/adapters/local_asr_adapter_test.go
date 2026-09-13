package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"scriberr/internal/transcription/interfaces"
)

// The fake package manager verifies process orchestration only; it neither
// installs dependencies nor claims to execute speech recognition.
func fakeLocalASRUV(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	logPath := filepath.Join(bin, "calls")
	script := `#!/bin/sh
set -eu
mode="$1"
project=""
config=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --project) shift; project="$1" ;;
    --config) shift; config="$1" ;;
  esac
  shift
done
if [ "$mode" = sync ]; then
  mkdir "$project/.installing"
  trap 'rmdir "$project/.installing"' EXIT
  printf 'sync\n' >> "$LOCAL_ASR_TEST_LOG"
  sleep 0.05
  mkdir -p "$project/.venv"
  printf 'home = fixture\n' > "$project/.venv/pyvenv.cfg"
elif [ -n "$config" ]; then
  printf 'transcribe\n' >> "$LOCAL_ASR_TEST_LOG"
  printf '%s' '{"text":"fixture","language":"en","segments":[{"start":0,"end":1,"text":"fixture"}],"metadata":{"resolved_device":"cpu"}}' > "${config%/*}/result.json"
else
  printf 'import\n' >> "$LOCAL_ASR_TEST_LOG"
fi
`
	if err := os.WriteFile(filepath.Join(bin, "uv"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("LOCAL_ASR_TEST_LOG", logPath)
	t.Setenv("PYTORCH_CUDA_VERSION", "cpu")
	return logPath
}

func TestLocalASRFirstUsePreparesAndRefreshesRuntime(t *testing.T) {
	logPath := fakeLocalASRUV(t)
	envPath := filepath.Join(t.TempDir(), "shared environment")
	a, err := NewLocalASRAdapter(envPath, "Qwen/Qwen3-ASR-1.7B-hf")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if a.IsReady(ctx) {
		t.Fatal("uninstalled adapter is ready")
	}
	inputPath := filepath.Join(t.TempDir(), "source.wav")
	if err := os.WriteFile(inputPath, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := a.Transcribe(ctx, interfaces.AudioInput{FilePath: inputPath, Format: "wav", Size: 7}, nil, interfaces.ProcessingContext{TempDirectory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "fixture" || !a.IsReady(ctx) {
		t.Fatal("first-use preparation did not reach the worker")
	}
	if err := a.PrepareEnvironment(ctx); err != nil {
		t.Fatal(err)
	}
	// A stale dependency project or runner must not be used with uv --no-sync.
	for _, name := range []string{"pyproject.toml", "transcribe.py"} {
		if err := os.WriteFile(filepath.Join(envPath, name), []byte("stale runtime"), 0644); err != nil {
			t.Fatal(err)
		}
		if a.IsReady(ctx) {
			t.Fatalf("stale %s considered ready", name)
		}
		if err := a.PrepareEnvironment(ctx); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PYTORCH_CUDA_VERSION", "cu126")
	if a.IsReady(ctx) {
		t.Fatal("changed wheel index considered ready")
	}
	if err := a.PrepareEnvironment(ctx); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(envPath, ".venv")); err != nil {
		t.Fatal(err)
	}
	if a.IsReady(ctx) {
		t.Fatal("removed virtual environment considered ready")
	}
	if err := a.PrepareEnvironment(ctx); err != nil {
		t.Fatal(err)
	}
	calls, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(calls), "sync\n") != 5 || strings.Count(string(calls), "transcribe\n") != 1 {
		t.Fatalf("unexpected process calls: %s", calls)
	}
}

func TestLocalASRConcurrentModelsShareOnePreparation(t *testing.T) {
	logPath := fakeLocalASRUV(t)
	envPath := t.TempDir()
	ctx := context.Background()
	var wg sync.WaitGroup
	failures := make(chan error, len(LocalASRModels()))
	for _, spec := range LocalASRModels() {
		a, err := NewLocalASRAdapter(envPath, spec.ID)
		if err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := a.PrepareEnvironment(ctx); err != nil {
				failures <- err
			} else if !a.IsReady(ctx) {
				failures <- errors.New("shared adapter not ready after preparation")
			}
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	calls, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(calls) != "sync\nimport\n" {
		t.Fatalf("shared environment installed more than once: %s", calls)
	}
}

func TestLocalASRPreparationWaitCanBeCanceled(t *testing.T) {
	a, err := NewLocalASRAdapter(t.TempDir(), "Qwen/Qwen3-ASR-1.7B-hf")
	if err != nil {
		t.Fatal(err)
	}
	state := localASREnvironmentFor(a.envPath)
	if err := state.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer state.release()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- a.PrepareEnvironment(ctx) }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled job kept waiting for installation lock")
	}
}

func TestLocalASRCatalogAndContexts(t *testing.T) {
	seen := map[string]bool{}
	for _, spec := range LocalASRModels() {
		if seen[spec.ID] {
			t.Fatalf("duplicate model %s", spec.ID)
		}
		seen[spec.ID] = true
		a, err := NewLocalASRAdapter(t.TempDir(), spec.ID)
		if err != nil {
			t.Fatal(err)
		}
		if a.GetCapabilities().Metadata["model_id"] != spec.ID || a.GetCapabilities().Metadata["lazy_init"] != "true" {
			t.Fatal("catalog metadata drift")
		}
		if err := a.ValidateParameters(map[string]interface{}{}); err != nil {
			t.Fatal(err)
		}
		if a.GetStringParameter(nil, "device") != "cpu" || a.GetStringParameter(nil, "precision") != "float32" {
			t.Fatal("CPU float32 default lost")
		}
		if err := a.ValidateParameters(map[string]interface{}{"device": "cpu", "precision": "float16"}); err == nil {
			t.Fatal("accepted reduced CPU precision")
		}
		if err := a.ValidateParameters(map[string]interface{}{"context": "engineering meeting"}); (err == nil) != (spec.ContextMode == "prose_and_terms") {
			t.Fatalf("wrong prose support: %s", spec.ID)
		}
		if err := a.ValidateParameters(map[string]interface{}{"context_terms": "PostgreSQL\nScriberr"}); (err == nil) != (spec.ContextMode != "none") {
			t.Fatalf("wrong vocabulary support: %s", spec.ID)
		}
	}
	if _, err := NewLocalASRAdapter(t.TempDir(), "untrusted/arbitrary-code"); err == nil {
		t.Fatal("unknown remote model accepted")
	}
}

func TestLocalASRPrivateRequestAndCredentialEnvironment(t *testing.T) {
	a, err := NewLocalASRAdapter(t.TempDir(), "Qwen/Qwen3-ASR-1.7B-hf")
	if err != nil {
		t.Fatal(err)
	}
	temp := t.TempDir()
	const token = "hf_local_asr_secret"
	args, env, err := a.buildRequest(interfaces.AudioInput{FilePath: "/tmp/source.wav"}, map[string]interface{}{"hf_token": token, "context": "private meeting context", "context_terms": "Kubernetes"}, temp)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(temp, "request.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), token) || strings.Contains(strings.Join(args, " "), token) || strings.Contains(strings.Join(args, " "), "private meeting context") {
		t.Fatal("secret or context exposed through command arguments")
	}
	if !strings.Contains(strings.Join(env, "\n"), "HF_TOKEN="+token) {
		t.Fatal("HF_TOKEN not passed to worker")
	}
	if !strings.Contains(strings.Join(env, "\n"), "CUDA_VISIBLE_DEVICES=\n") && env[len(env)-1] != "CUDA_VISIBLE_DEVICES=" {
		t.Fatal("CPU does not hide CUDA")
	}
	info, err := os.Stat(filepath.Join(temp, "request.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("request mode %o", info.Mode().Perm())
	}
	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if config["precision"] != "float32" || config["context_terms"] != "Kubernetes" {
		t.Fatal("request omitted recognition parameters")
	}
}

func TestLocalASREmbeddedEnvironment(t *testing.T) {
	a, err := NewLocalASRAdapter(t.TempDir(), "Edge0/ARK-ASR-3B")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PYTORCH_CUDA_VERSION", "")
	if err := os.MkdirAll(a.envPath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.envPath, "uv.lock"), []byte("old dependency graph"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := a.materializeEnvironment(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(a.envPath, "uv.lock")); !os.IsNotExist(err) {
		t.Fatal("local ASR upgrade retained old dependency resolutions")
	}
	for _, name := range []string{"models.json", "transcribe.py", "backends.py", "qwen_backend.py", "pyproject.toml"} {
		if _, err := os.Stat(filepath.Join(a.envPath, name)); err != nil {
			t.Fatal(err)
		}
	}
	project, err := os.ReadFile(filepath.Join(a.envPath, "pyproject.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(project), "https://download.pytorch.org/whl/cpu") || strings.Contains(string(project), "nemo-toolkit") {
		t.Fatal("modern ASR dependency isolation lost")
	}
}

func TestLocalASRRejectsRetiredCUDAWheelsBeforePreparing(t *testing.T) {
	logPath := fakeLocalASRUV(t)
	t.Setenv("PYTORCH_CUDA_VERSION", "cu128")
	adapter, err := NewLocalASRAdapter(t.TempDir(), "Edge0/ARK-ASR-3B")
	if err != nil {
		t.Fatal(err)
	}
	err = adapter.PrepareEnvironment(context.Background())
	if err == nil || !strings.Contains(err.Error(), "cu130 for Blackwell") {
		t.Fatalf("expected CUDA wheel migration guidance, got %v", err)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("unsupported CUDA setting invoked package manager: %v", err)
	}
}

func TestLocalASRResultPreservesSpeakersAndTimingProvenance(t *testing.T) {
	a, err := NewLocalASRAdapter(t.TempDir(), "OpenMOSS-Team/MOSS-Transcribe-Diarize")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "result.json")
	data := `{"text":"hello","language":"en","segments":[{"start":0.3,"end":0.8,"text":"hello","speaker":"S01"}],"metadata":{"timestamp_source":"native","resolved_device":"cpu"}}`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := a.parseResult(path)
	if err != nil {
		t.Fatal(err)
	}
	if result.Segments[0].Speaker == nil || *result.Segments[0].Speaker != "S01" || result.Metadata["timestamp_source"] != "native" {
		t.Fatal("lost speaker or timing provenance")
	}
	if err := os.WriteFile(path, []byte(strings.Replace(data, `"end":0.8`, `"end":0.1`, 1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.parseResult(path); err == nil {
		t.Fatal("accepted reversed timestamps")
	}
}

func TestLocalASRExternalDiarizationRequiresUsableTiming(t *testing.T) {
	for _, model := range []string{"Qwen/Qwen3-ASR-1.7B-hf", "mistralai/Voxtral-Mini-3B-2507", "ibm-granite/granite-speech-4.1-2b-plus", "OpenMOSS-Team/MOSS-Transcribe-Diarize"} {
		adapter, err := NewLocalASRAdapter(t.TempDir(), model)
		if err != nil {
			t.Fatal(err)
		}
		params := map[string]interface{}{"align_words": false, "external_diarization_requested": true}
		err = adapter.ValidateParameters(params)
		nativeTiming := adapter.spec.NativeTimestamps || adapter.spec.Engine == "granite_plus"
		if (err == nil) != nativeTiming {
			t.Fatalf("%s accepted unavailable timing or rejected native timing: %v", model, err)
		}
		params["align_words"] = true
		if err := adapter.ValidateParameters(params); err != nil {
			t.Fatal(err)
		}
		directory := t.TempDir()
		if _, _, err := adapter.buildRequest(interfaces.AudioInput{FilePath: "fixture.wav"}, params, directory); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(directory, "request.json"))
		if err != nil {
			t.Fatal(err)
		}
		var config map[string]interface{}
		if err := json.Unmarshal(data, &config); err != nil {
			t.Fatal(err)
		}
		if config["external_diarization_requested"] != true {
			t.Fatal("external diarization request was lost before Python validation")
		}
	}
}

func TestLocalASRCancellationRemovesOwnedConvertedAudio(t *testing.T) {
	logPath := fakeLocalASRUV(t)
	adapter, err := NewLocalASRAdapter(t.TempDir(), "Qwen/Qwen3-ASR-1.7B-hf")
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.PrepareEnvironment(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Stand in for a Python worker killed while ffmpeg owns a converted WAV.
	// It has no cleanup trap, so only the Go caller can remove the nested file.
	stub := `#!/bin/sh
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--config" ]; then shift; config="$1"; break; fi
  shift
done
test -n "$config" || exit 21
job="${config%/*}"
mkdir "$job/scriberr-local-asr-fixture"
printf 'converted audio fixture' > "$job/scriberr-local-asr-fixture/audio.wav"
printf '%s' "$job" > "$LOCAL_ASR_CONVERSION_READY"
sleep 30
`
	if err := os.WriteFile(filepath.Join(filepath.Dir(logPath), "uv"), []byte(stub), 0755); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	marker := filepath.Join(directory, "conversion-ready")
	t.Setenv("LOCAL_ASR_CONVERSION_READY", marker)
	input := filepath.Join(directory, "source.wav")
	if err := os.WriteFile(input, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := adapter.Transcribe(ctx, interfaces.AudioInput{FilePath: input, Format: "wav", Size: 7}, nil, interfaces.ProcessingContext{TempDirectory: directory})
		result <- err
	}()
	deadline := time.Now().Add(3 * time.Second)
	var job []byte
	for len(job) == 0 && time.Now().Before(deadline) {
		job, _ = os.ReadFile(marker)
		if len(job) == 0 {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if len(job) == 0 {
		t.Fatal("worker never reached audio conversion")
	}
	if _, err := os.Stat(filepath.Join(string(job), "scriberr-local-asr-fixture", "audio.wav")); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled worker returned %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled worker did not exit")
	}
	if _, err := os.Stat(string(job)); !os.IsNotExist(err) {
		t.Fatalf("cancelled job left its converted audio directory behind: %v", err)
	}
	if _, err := os.Stat(input); err != nil {
		t.Fatal("job cleanup removed the source recording")
	}
}
