package adapters

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"scriberr/internal/processutil"
	"scriberr/internal/transcription/interfaces"
)

//go:embed py/diarizen/* py/suplime/* py/research/*
var researchDiarizationScripts embed.FS

// ResearchDiarizationAdapter isolates research packages from the production
// pyannote environment. Installation is requested explicitly by the caller.
type ResearchDiarizationAdapter struct {
	*BaseAdapter
	envPath string
	engine  string
	setupMu sync.Mutex
}

func NewDiariZenAdapter(envPath string) *ResearchDiarizationAdapter {
	return newResearchDiarizationAdapter(envPath, "diarizen", "DiariZen Large-s80-v2", "BUT-FIT/diarizen-wavlm-large-s80-md-v2", []string{"BUT-FIT/diarizen-wavlm-large-s80-md-v2"})
}

func NewSUPlimeAdapter(envPath string) *ResearchDiarizationAdapter {
	return newResearchDiarizationAdapter(envPath, "suplime", "SUPlime / SUPlime-L", "rewayai/suplime-large", []string{"rewayai/suplime", "rewayai/suplime-large"})
}

func newResearchDiarizationAdapter(envPath, engine, displayName, defaultModel string, models []string) *ResearchDiarizationAdapter {
	capabilities := interfaces.ModelCapabilities{
		ModelID: engine, ModelFamily: engine, DisplayName: displayName,
		Description: "Optional research diarization model with non-commercial weights; CPU and CUDA inference",
		Version:     "1.0.0", SupportedLanguages: []string{"*"}, SupportedFormats: []string{"wav", "flac", "mp3", "m4a", "ogg"},
		RequiresGPU: false, MemoryRequirement: 4096,
		Features: map[string]bool{"speaker_detection": true, "speaker_constraints": true, "flexible_speakers": true},
		Metadata: map[string]string{"engine": engine, "framework": "pytorch", "license": "CC-BY-NC-4.0", "commercial_use": "false", "optional_install": "true", "experimental": "true", "cpu_support": "unbenchmarked", "model_id": defaultModel},
	}
	if engine == "diarizen" {
		capabilities.Metadata["cpu_support"] = "upstream_supported"
	}
	variants, _ := json.Marshal(models)
	capabilities.Metadata["model_variants"] = string(variants)
	capabilities.Metadata["lazy_init"] = "true"
	schema := []interfaces.ParameterSchema{
		{Name: "model", Type: "string", Default: defaultModel, Options: models, Description: "Downloaded diarization model", Group: "basic"},
		{Name: "device", Type: "string", Default: "cpu", Options: []string{"auto", "cpu", "cuda"}, Description: "Device for inference", Group: "basic"},
		{Name: "min_speakers", Type: "int", Min: &[]float64{1}[0], Max: &[]float64{20}[0], Description: "Minimum number of speakers, if known", Group: "basic"},
		{Name: "max_speakers", Type: "int", Min: &[]float64{1}[0], Max: &[]float64{20}[0], Description: "Maximum number of speakers, if known", Group: "basic"},
	}
	return &ResearchDiarizationAdapter{BaseAdapter: NewBaseAdapter(engine, envPath, capabilities, schema), envPath: envPath, engine: engine}
}

func (r *ResearchDiarizationAdapter) GetMinSpeakers() int { return 1 }
func (r *ResearchDiarizationAdapter) GetMaxSpeakers() int { return 20 }

func (r *ResearchDiarizationAdapter) PrepareEnvironment(ctx context.Context) error {
	r.setupMu.Lock()
	defer r.setupMu.Unlock()
	if r.initialized {
		return nil
	}
	if err := os.MkdirAll(r.envPath, 0755); err != nil {
		return err
	}
	project, err := researchDiarizationScripts.ReadFile("py/" + r.engine + "/pyproject.toml")
	if err != nil {
		return err
	}
	// DiariZen's legacy PyTorch uses its matching CUDA 12.4 wheels. The other
	// pipeline uses the same configurable CPU/CUDA index as pyannote 4.
	if r.engine == "diarizen" {
		if GetPyTorchWheelURL() == "https://download.pytorch.org/whl/cpu" {
			project = []byte(strings.ReplaceAll(string(project), "https://download.pytorch.org/whl/cu124", GetPyTorchWheelURL()))
		}
	} else {
		project = []byte(strings.Replace(string(project), "https://download.pytorch.org/whl/cu126", GetPyTorchWheelURL(), 1))
	}
	if err := os.WriteFile(filepath.Join(r.envPath, "pyproject.toml"), project, 0644); err != nil {
		return err
	}
	script, err := researchDiarizationScripts.ReadFile("py/research/research_diarize.py")
	if err != nil {
		return err
	}
	if err := writeRuntimeScript(filepath.Join(r.envPath, "research_diarize.py"), script, 0644); err != nil {
		return err
	}
	cmd := processutil.CommandContext(ctx, "uv", "sync", "--system-certs", "--project", r.envPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s environment setup failed: %w: %s", r.engine, err, output)
	}
	r.initialized = true
	return nil
}

func (r *ResearchDiarizationAdapter) buildDiarizationArgs(input interfaces.AudioInput, params map[string]interface{}, directory string) []string {
	args := []string{"run", "--system-certs", "--project", r.envPath, "python", filepath.Join(r.envPath, "research_diarize.py"), input.FilePath,
		"--output", filepath.Join(directory, "result.json"), "--engine", r.engine, "--model", r.GetStringParameter(params, "model"), "--device", r.GetStringParameter(params, "device")}
	for _, key := range []string{"min_speakers", "max_speakers"} {
		if value := r.GetIntParameter(params, key); value > 0 {
			args = append(args, "--"+strings.ReplaceAll(key, "_", "-"), strconv.Itoa(value))
		}
	}
	return args
}

func (r *ResearchDiarizationAdapter) Diarize(ctx context.Context, input interfaces.AudioInput, params map[string]interface{}, procCtx interfaces.ProcessingContext) (*interfaces.DiarizationResult, error) {
	if err := r.ValidateAudioInput(input); err != nil {
		return nil, err
	}
	if err := r.ValidateParameters(params); err != nil {
		return nil, err
	}
	if min, max := r.GetIntParameter(params, "min_speakers"), r.GetIntParameter(params, "max_speakers"); min > 0 && max > 0 && min > max {
		return nil, fmt.Errorf("min_speakers cannot exceed max_speakers")
	}
	if err := r.PrepareEnvironment(ctx); err != nil {
		return nil, err
	}
	start := time.Now()
	directory, err := r.CreateTempDirectory(procCtx)
	if err != nil {
		return nil, err
	}
	defer r.CleanupTempDirectory(directory)
	cmd := processutil.CommandContext(ctx, "uv", r.buildDiarizationArgs(input, params, directory)...)
	cmd.Env = append(os.Environ(), "PYTHONUNBUFFERED=1")
	if token := strings.TrimSpace(r.GetStringParameter(params, "hf_token")); token != "" {
		cmd.Env = withEnvironmentValue(cmd.Env, "HF_TOKEN", token)
	}
	log, err := os.OpenFile(filepath.Join(procCtx.OutputDirectory, "transcription.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("open diarization log: %w", err)
	}
	defer log.Close()
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s diarization failed: %w; see transcription log", r.engine, err)
	}
	data, err := os.ReadFile(filepath.Join(directory, "result.json"))
	if err != nil {
		return nil, err
	}
	result, err := parseResearchDiarization(data)
	if err != nil {
		return nil, err
	}
	result.ProcessingTime = time.Since(start)
	result.ModelUsed = r.GetStringParameter(params, "model")
	result.Metadata = mergeRuntimeMetadata(r.CreateDefaultMetadata(params), readRuntimeMetadata(directory))
	return result, nil
}

func parseResearchDiarization(data []byte) (*interfaces.DiarizationResult, error) {
	var payload struct {
		Segments     []interfaces.DiarizationSegment `json:"segments"`
		Speakers     []string                        `json:"speakers"`
		SpeakerCount int                             `json:"speaker_count"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("invalid diarization result: %w", err)
	}
	return &interfaces.DiarizationResult{Segments: payload.Segments, Speakers: payload.Speakers, SpeakerCount: payload.SpeakerCount}, nil
}
