package adapters

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"scriberr/internal/processutil"
	"scriberr/internal/transcription/interfaces"
)

//go:embed py/local_asr/*.py py/local_asr/*.json py/local_asr/pyproject.toml
var localASRScripts embed.FS

// All model adapters sharing a runtime directory also share installation state.
// A channel lock lets canceled jobs stop waiting for another job's installation.
type localASREnvironment struct {
	gate   chan struct{}
	digest atomic.Value // string; published only after successful installation
}

var localASREnvironments sync.Map

func localASREnvironmentFor(path string) *localASREnvironment {
	key, err := filepath.Abs(path)
	if err != nil {
		key = filepath.Clean(path)
	}
	state, _ := localASREnvironments.LoadOrStore(key, &localASREnvironment{gate: make(chan struct{}, 1)})
	return state.(*localASREnvironment)
}

func (e *localASREnvironment) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case e.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *localASREnvironment) release() { <-e.gate }

// LocalASRModel is the shared Go/Python catalog entry. Revisions are pinned for
// checkpoints which execute their publisher's custom Transformers code.
type LocalASRModel struct {
	ID                  string   `json:"id"`
	Family              string   `json:"family"`
	FamilyName          string   `json:"family_name"`
	Name                string   `json:"name"`
	Engine              string   `json:"engine"`
	ContextMode         string   `json:"context_mode"`
	Revision            string   `json:"revision"`
	License             string   `json:"license"`
	MemoryMB            int      `json:"memory_mb"`
	MaxChunkSeconds     int      `json:"max_chunk_seconds"`
	DefaultChunkSeconds int      `json:"default_chunk_seconds"`
	NativeSpeakers      bool     `json:"native_speakers"`
	NativeTimestamps    bool     `json:"native_timestamps"`
	Languages           []string `json:"languages"`
}

func LocalASRModels() []LocalASRModel {
	data, err := localASRScripts.ReadFile("py/local_asr/models.json")
	if err != nil {
		panic(err)
	}
	var models []LocalASRModel
	if err := json.Unmarshal(data, &models); err != nil {
		panic(err)
	}
	return models
}

func LocalASRModelIDs() []string {
	var ids []string
	for _, model := range LocalASRModels() {
		ids = append(ids, model.ID)
	}
	return ids
}

// LocalASRAdapter runs one fixed checkpoint in an environment isolated from
// NeMo. The caller registers each catalog ID and provides its environment path.
type LocalASRAdapter struct {
	*BaseAdapter
	envPath string
	spec    LocalASRModel
}

func NewLocalASRAdapter(envPath, modelID string) (*LocalASRAdapter, error) {
	var spec LocalASRModel
	for _, model := range LocalASRModels() {
		if model.ID == modelID {
			spec = model
			break
		}
	}
	if spec.ID == "" {
		return nil, fmt.Errorf("unsupported local ASR model: %s", modelID)
	}
	features := map[string]bool{
		"timestamps": true, "word_level": true, "high_quality": true,
		"context": spec.ContextMode != "none", "context_terms": spec.ContextMode != "none",
		"context_prose": spec.ContextMode == "prose_and_terms", "integrated_diarization": spec.NativeSpeakers,
	}
	metadata := map[string]string{
		"engine": "transformers", "license": "Apache-2.0", "model_id": spec.ID,
		"family_display_name": spec.FamilyName, "context_mode": spec.ContextMode,
		"timestamp_source": "forced_alignment", "cpu_support": "float32",
		"lazy_init": "true", "default_device": "cpu", "default_precision": "float32",
		"default_chunk_duration": strconv.Itoa(spec.DefaultChunkSeconds), "default_max_new_tokens": "0",
		"max_chunk_duration":    strconv.Itoa(spec.MaxChunkSeconds),
		"memory_estimate_notes": "Conservative CPU float32 planning estimate, not measured. ASR unloads before alignment. Long recording context increases RAM.",
	}
	if spec.NativeTimestamps {
		metadata["timestamp_source"] = "native_segments_optional_word_alignment"
	}
	if spec.License != "" {
		metadata["license"] = spec.License
	}
	if spec.Engine == "granite_plus" {
		metadata["timestamp_source"] = "native_word_ends_previous_end_starts"
	}
	if spec.Revision != "" {
		metadata["revision"] = spec.Revision
	}
	capabilities := interfaces.ModelCapabilities{
		ModelID: spec.ID, ModelFamily: spec.Family, DisplayName: spec.Name,
		Description: "Downloadable speech recognition with explicit CPU execution and optional word alignment",
		Version:     "1", SupportedLanguages: spec.Languages,
		SupportedFormats:  []string{"wav", "flac", "mp3", "m4a", "mp4", "ogg", "opus", "aac", "webm"},
		MemoryRequirement: spec.MemoryMB, Features: features, Metadata: metadata,
	}
	maxChunk := float64(spec.MaxChunkSeconds)
	minChunk, minTokens, maxTokens := float64(0), float64(0), float64(65536)
	schema := []interfaces.ParameterSchema{
		{Name: "language", Type: "string", Default: "en", Description: "Source language code; used for recognition where supported and forced alignment", Group: "basic"},
		{Name: "device", Type: "string", Default: "cpu", Options: []string{"cpu", "cuda", "auto"}, Description: "CPU stays on CPU; CUDA fails if unavailable. Auto explicitly permits GPU selection.", Group: "advanced"},
		{Name: "precision", Type: "string", Default: "float32", Options: []string{"float32", "bfloat16", "float16"}, Description: "Arithmetic precision. CPU requires float32; reduced precision is an explicit CUDA option.", Group: "quality"},
		{Name: "align_words", Type: "bool", Default: !spec.NativeTimestamps, Description: "Run Qwen3 forced alignment for word timing and external speaker assignment; downloads a separate alignment checkpoint", Group: "quality"},
		{Name: "chunk_duration", Type: "int", Default: spec.DefaultChunkSeconds, Min: &minChunk, Max: &maxChunk, Description: "Audio window in seconds. Zero uses the model default; MOSS Diarize uses the complete recording for consistent speakers.", Group: "advanced"},
		{Name: "max_new_tokens", Type: "int", Default: 0, Min: &minTokens, Max: &maxTokens, Description: "Maximum output tokens per window. Zero scales with audio duration; exhausted budgets fail instead of returning partial transcripts.", Group: "advanced"},
		{Name: "hf_token", Type: "string", Default: "", Description: "Optional Hugging Face access token, passed only through HF_TOKEN; otherwise use the server's cached login or environment", Group: "advanced"},
	}
	if spec.NativeSpeakers {
		schema = append(schema,
			interfaces.ParameterSchema{Name: "diarize", Type: "bool", Default: false, Description: "Include speaker labels", Group: "basic"},
			interfaces.ParameterSchema{Name: "diarize_model", Type: "string", Default: "none", Description: "Use native for this model's integrated speakers; external diarizers are applied by the pipeline", Group: "basic"},
		)
	}
	if spec.ContextMode != "none" {
		schema = append(schema, interfaces.ParameterSchema{Name: "context_terms", Type: "string", Default: "", Description: "Vocabulary hints, one technical term or name per line", Group: "quality"})
	}
	if spec.ContextMode == "prose_and_terms" {
		schema = append(schema, interfaces.ParameterSchema{Name: "context", Type: "string", Default: "", Description: "Brief meeting context used during recognition", Group: "quality"})
	}
	return &LocalASRAdapter{BaseAdapter: NewBaseAdapter(spec.ID, envPath, capabilities, schema), envPath: envPath, spec: spec}, nil
}

func (a *LocalASRAdapter) GetSupportedModels() []string { return []string{a.spec.ID} }

// PrepareLocalASREnvironment prepares the shared Python runtime without loading
// or downloading a recognition checkpoint. Other adapters may use its aligner.
func PrepareLocalASREnvironment(ctx context.Context, envPath string) error {
	return (&LocalASRAdapter{envPath: envPath}).PrepareEnvironment(ctx)
}

func (a *LocalASRAdapter) ValidateParameters(params map[string]interface{}) error {
	if err := a.BaseAdapter.ValidateParameters(params); err != nil {
		return err
	}
	if a.GetStringParameter(params, "device") == "cpu" && a.GetStringParameter(params, "precision") != "float32" {
		return fmt.Errorf("CPU inference requires precision float32")
	}
	if requested, _ := params["external_diarization_requested"].(bool); requested && !a.GetBoolParameter(params, "align_words") && !a.spec.NativeTimestamps && a.spec.Engine != "granite_plus" {
		return fmt.Errorf("%s requires word alignment for external diarization; enable align_words", a.spec.Name)
	}
	if value, ok := params["context"].(string); ok && strings.TrimSpace(value) != "" && a.spec.ContextMode != "prose_and_terms" {
		return fmt.Errorf("%s does not support prose context; use vocabulary terms on supported models", a.spec.Name)
	}
	if value, ok := params["context_terms"].(string); ok && strings.TrimSpace(value) != "" && a.spec.ContextMode == "none" {
		return fmt.Errorf("%s does not support vocabulary hints", a.spec.Name)
	}
	for _, name := range []string{"context", "context_terms"} {
		if value, ok := params[name].(string); ok && len(value) > 32768 {
			return fmt.Errorf("%s exceeds 32768 bytes", name)
		}
	}
	return nil
}

func (a *LocalASRAdapter) PrepareEnvironment(ctx context.Context) error {
	assets, digest, err := localASREnvironmentAssets()
	if err != nil {
		return err
	}
	state := localASREnvironmentFor(a.envPath)
	if err := state.acquire(ctx); err != nil {
		return err
	}
	defer state.release()
	if state.digest.Load() == digest && a.environmentMatches(assets) {
		return nil
	}
	state.digest.Store("")
	if err := a.writeEnvironmentAssets(assets); err != nil {
		return err
	}
	cmd := processutil.CommandContext(ctx, "uv", "sync", "--system-certs", "--project", a.envPath)
	// Do not include package-manager output: authenticated URLs may appear there.
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if runtime.GOOS == "darwin" && runtime.GOARCH == "amd64" {
			return fmt.Errorf("modern Transformers ASR requires recent PyTorch wheels unavailable for Intel macOS; run this worker on Linux x86_64 (including a Linux container)")
		}
		return fmt.Errorf("local ASR environment installation failed: %w", err)
	}
	cmd = processutil.CommandContext(ctx, "uv", "run", "--no-sync", "--project", a.envPath, "python", "-c", "import torch, transformers, soundfile; from transformers import AutoProcessor")
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("local ASR environment import check failed: %w", err)
	}
	state.digest.Store(digest)
	return nil
}

// IsReady never installs dependencies. Readiness expires when the embedded
// runtime, wheel index, materialized scripts, or virtual environment changes.
func (a *LocalASRAdapter) IsReady(ctx context.Context) bool {
	if ctx.Err() != nil {
		return false
	}
	assets, digest, err := localASREnvironmentAssets()
	if err != nil {
		return false
	}
	state := localASREnvironmentFor(a.envPath)
	// A health/status request must not block behind a long dependency install.
	return state.digest.Load() == digest && a.environmentMatches(assets)
}

func localASREnvironmentAssets() (map[string][]byte, string, error) {
	entries, err := localASRScripts.ReadDir("py/local_asr")
	if err != nil {
		return nil, "", err
	}
	assets := make(map[string][]byte, len(entries))
	digest := sha256.New()
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := localASRScripts.ReadFile("py/local_asr/" + entry.Name())
		if err != nil {
			return nil, "", err
		}
		if entry.Name() == "pyproject.toml" {
			if cuda := os.Getenv("PYTORCH_CUDA_VERSION"); cuda != "" && cuda != "cpu" {
				if cuda != "cu126" && cuda != "cu128" && cuda != "cu130" {
					return nil, "", fmt.Errorf("unsupported PYTORCH_CUDA_VERSION")
				}
				data = []byte(strings.ReplaceAll(string(data), "https://download.pytorch.org/whl/cpu", "https://download.pytorch.org/whl/"+cuda))
			}
		}
		assets[entry.Name()] = data
		fmt.Fprintf(digest, "%s\x00%d\x00", entry.Name(), len(data))
		digest.Write(data)
	}
	assets["runtime_failure.py"] = runtimeFailureHelper
	digest.Write(runtimeFailureHelper)
	return assets, fmt.Sprintf("%x", digest.Sum(nil)), nil
}

func (a *LocalASRAdapter) environmentMatches(assets map[string][]byte) bool {
	if _, err := os.Stat(filepath.Join(a.envPath, ".venv", "pyvenv.cfg")); err != nil {
		return false
	}
	for name, data := range assets {
		onDisk, err := os.ReadFile(filepath.Join(a.envPath, name))
		if err != nil || string(onDisk) != string(data) {
			return false
		}
	}
	return true
}

func (a *LocalASRAdapter) materializeEnvironment() error {
	assets, _, err := localASREnvironmentAssets()
	if err != nil {
		return err
	}
	return a.writeEnvironmentAssets(assets)
}

func (a *LocalASRAdapter) writeEnvironmentAssets(assets map[string][]byte) error {
	if err := os.MkdirAll(a.envPath, 0755); err != nil {
		return err
	}
	for name, data := range assets {
		if name == "runtime_failure.py" {
			if err := writePythonProject(filepath.Join(a.envPath, name), data); err != nil {
				return err
			}
			continue
		}
		if err := os.WriteFile(filepath.Join(a.envPath, name), data, 0644); err != nil {
			return err
		}
	}
	return nil
}

// buildRequest keeps prompts in a private file and credentials out of both the
// request and process arguments. Hugging Face's usual token/cache lookup works.
func (a *LocalASRAdapter) buildRequest(input interfaces.AudioInput, params map[string]interface{}, tempDir string) ([]string, []string, error) {
	config := map[string]interface{}{"model_id": a.spec.ID, "audio_file": input.FilePath, "output": filepath.Join(tempDir, "result.json")}
	config["external_diarization_requested"], _ = params["external_diarization_requested"].(bool)
	for _, schema := range a.schema {
		if schema.Name != "hf_token" {
			config[schema.Name] = a.GetParameterWithDefault(params, schema.Name)
		}
	}
	data, err := json.Marshal(config)
	if err != nil {
		return nil, nil, err
	}
	configPath := filepath.Join(tempDir, "request.json")
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		return nil, nil, err
	}
	env := withEnvironmentValue(os.Environ(), "PYTHONUNBUFFERED", "1")
	if token := a.GetStringParameter(params, "hf_token"); token != "" {
		env = withEnvironmentValue(env, "HF_TOKEN", token)
	}
	if a.GetStringParameter(params, "device") == "cpu" {
		env = withEnvironmentValue(env, "CUDA_VISIBLE_DEVICES", "")
	}
	return []string{"run", "--no-sync", "--project", a.envPath, "python", filepath.Join(a.envPath, "transcribe.py"), "--config", configPath}, env, nil
}

func (a *LocalASRAdapter) Transcribe(ctx context.Context, input interfaces.AudioInput, params map[string]interface{}, procCtx interfaces.ProcessingContext) (*interfaces.TranscriptResult, error) {
	start := time.Now()
	if err := a.ValidateAudioInput(input); err != nil {
		return nil, err
	}
	if err := a.ValidateParameters(params); err != nil {
		return nil, err
	}
	if err := a.PrepareEnvironment(ctx); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(procCtx.TempDirectory, 0700); err != nil {
		return nil, err
	}
	tempDir, err := os.MkdirTemp(procCtx.TempDirectory, "local-asr-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tempDir)
	args, env, err := a.buildRequest(input, params, tempDir)
	if err != nil {
		return nil, err
	}
	cmd := processutil.CommandContext(ctx, "uv", args...)
	cmd.Env = env
	// The runner emits only progress counters and sanitised error classes. Keep
	// third-party stderr out of user logs; Python writes a safe error.json itself.
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var failure struct {
			Error          string `json:"error"`
			ResolvedDevice string `json:"resolved_device"`
			GPUFailureKind string `json:"gpu_failure_kind"`
		}
		if data, e := os.ReadFile(filepath.Join(tempDir, "error.json")); e == nil && json.Unmarshal(data, &failure) == nil && failure.Error != "" {
			cause := fmt.Errorf("local ASR failed: %s", failure.Error)
			if failure.ResolvedDevice == "cuda" && (failure.GPUFailureKind == "cuda_out_of_memory" || failure.GPUFailureKind == "cuda_runtime_error") {
				return nil, &interfaces.GPUExecutionError{Kind: failure.GPUFailureKind, Err: cause}
			}
			return nil, cause
		}
		return nil, fmt.Errorf("local ASR worker failed: %w", err)
	}
	result, err := a.parseResult(filepath.Join(tempDir, "result.json"))
	if err != nil {
		return nil, err
	}
	result.ProcessingTime = time.Since(start)
	result.ModelUsed = a.spec.ID
	for key, value := range a.CreateDefaultMetadata(params) {
		result.Metadata[key] = value
	}
	return result, nil
}

func (a *LocalASRAdapter) parseResult(path string) (*interfaces.TranscriptResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read local ASR output: %w", err)
	}
	var result interfaces.TranscriptResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse local ASR output: %w", err)
	}
	if result.Metadata == nil {
		result.Metadata = map[string]string{}
	}
	valid := func(start, end float64) bool {
		return !math.IsNaN(start) && !math.IsNaN(end) && !math.IsInf(start, 0) && !math.IsInf(end, 0) && start >= 0 && end >= start
	}
	for _, seg := range result.Segments {
		if !valid(seg.Start, seg.End) {
			return nil, fmt.Errorf("local ASR returned invalid segment timing")
		}
	}
	for _, word := range result.WordSegments {
		if !valid(word.Start, word.End) {
			return nil, fmt.Errorf("local ASR returned invalid word timing")
		}
	}
	if strings.TrimSpace(result.Text) != "" && len(result.Segments) == 0 {
		return nil, fmt.Errorf("local ASR returned text without segments")
	}
	return &result, nil
}
