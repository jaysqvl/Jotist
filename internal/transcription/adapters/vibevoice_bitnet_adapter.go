package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"scriberr/internal/processutil"
	"scriberr/internal/transcription/interfaces"
	"scriberr/pkg/downloader"
)

const vibeBitNetSourceRevision = "94cddff7216df8069cbf1b9095714b8327ffeedc"
const vibeBitNetModelRevision = "66e78021ab8f5f06133d1ab421ba4d348bda97c9"
const vibeBitNetModel = "microsoft/VibeVoice-ASR-BitNet"
const vibeBitNetVAE = "vibeasr-vae-encoder-i8_s.gguf"
const vibeBitNetLM = "vibeasr-lm-i2_s-embed-q6_k.gguf"

// VibeVoiceBitNetAdapter uses Microsoft's CPU-only C++ runtime. Its compressed
// 1.5B decoder is a distinct checkpoint from VibeVoice-ASR-7B.
type VibeVoiceBitNetAdapter struct {
	*BaseAdapter
	envPath string
}

func NewVibeVoiceBitNetAdapter(envPath string) *VibeVoiceBitNetAdapter {
	caps := interfaces.ModelCapabilities{
		ModelID: "vibevoice-bitnet", ModelFamily: "vibevoice-bitnet", DisplayName: "VibeVoice ASR BitNet 1.5B (CPU)",
		Description: "Optional Microsoft CPU inference with compressed weights, vocabulary hints and separate word alignment",
		Version:     "2026-07", SupportedLanguages: []string{"*"}, SupportedFormats: []string{"wav", "flac", "mp3", "m4a", "mp4", "ogg", "opus", "aac", "webm"},
		MemoryRequirement: 8192,
		Features:          map[string]bool{"timestamps": true, "word_level": true, "integrated_diarization": false, "context": true, "context_terms": true, "context_prose": true},
		Metadata: map[string]string{
			"engine": "vibeasr.cpp", "license": "MIT", "model_id": vibeBitNetModel,
			"revision": vibeBitNetModelRevision, "runtime_revision": vibeBitNetSourceRevision,
			"optional_install": "true", "lazy_init": "true", "experimental": "true", "cpu_support": "upstream_benchmarked",
			"supported_devices": "cpu", "default_device": "cpu", "default_precision": "i2_s+i8_s", "default_chunk_duration": "30", "default_max_new_tokens": "16384", "context_mode": "prose_and_terms",
			"timestamp_source": "forced_alignment", "memory_estimate_notes": "8 GB planning estimate, not measured. ASR uses 30-second windows, then Qwen3 word alignment after the C++ process exits. Compressed 1.5B decoder differs from VibeVoice 7B.",
		},
	}
	minZero, maxThreads, minContext, maxContext, chunkSeconds := float64(0), float64(256), float64(2048), float64(65536), float64(30)
	schema := []interfaces.ParameterSchema{
		{Name: "device", Type: "string", Default: "cpu", Options: []string{"cpu", "auto"}, Description: "The official BitNet runtime uses CPU", Group: "basic"},
		{Name: "threads", Type: "int", Default: 4, Min: &minZero, Max: &maxThreads, Description: "CPU inference threads", Group: "advanced"},
		{Name: "context_size", Type: "int", Default: 16384, Min: &minContext, Max: &maxContext, Description: "Combined audio and output token budget; larger values use more RAM", Group: "advanced"},
		{Name: "max_new_tokens", Type: "int", Default: 16384, Min: &minZero, Max: &maxContext, Description: "Output token budget; incomplete generation fails instead of saving partial text", Group: "advanced"},
		{Name: "chunk_duration", Type: "int", Default: 30, Min: &minZero, Max: &chunkSeconds, Description: "BitNet uses 30-second recognition windows before word alignment; zero keeps that default", Group: "advanced"},
		{Name: "align_words", Type: "bool", Default: true, Description: "Align recognized words with Qwen3 after CPU ASR releases its memory; required for external speaker assignment", Group: "quality"},
		{Name: "language", Type: "string", Default: "en", Description: "Source language for forced word alignment", Group: "basic"},
		{Name: "diarize", Type: "bool", Default: false, Description: "Include speaker labels", Group: "basic"},
		{Name: "diarize_model", Type: "string", Default: "none", Description: "External diarizers run after word alignment; this compressed model has no native speaker labels", Group: "basic"},
	}
	schema = append(schema, contextParameters()...)
	return &VibeVoiceBitNetAdapter{BaseAdapter: NewBaseAdapter("vibevoice-bitnet", envPath, caps, schema), envPath: envPath}
}

func (v *VibeVoiceBitNetAdapter) GetSupportedModels() []string { return []string{vibeBitNetModel} }

func (v *VibeVoiceBitNetAdapter) binaryPath() string {
	return filepath.Join(v.envPath, "source", "build", "bin", "asr_infer")
}

// patchVibeBitNetCLI preserves model output before the upstream human-readable
// formatter, and fails if decoding stops before an end token.
func patchVibeBitNetCLI(source string) (string, error) {
	startMarker := "        // Parse and display transcription segments"
	endMarker := "        // Print timing summary to stderr"
	start, end := strings.Index(source, startMarker), strings.Index(source, endMarker)
	if start < 0 || end <= start {
		return "", fmt.Errorf("pinned VibeASR.cpp output contract was not found")
	}
	replacement := `        // Scriberr: retain model output and never publish a truncated transcript.
        if (new_token != EOG_IM_END && new_token != EOG_ENDOFTEXT) {
            fprintf(stderr, "Scriberr: token/context budget exhausted or decoding failed.\n");
            return 2;
        }
        const char * result_path = std::getenv("SCRIBERR_RESULT_PATH");
        if (!result_path) return 2;
        FILE * result_file = std::fopen(result_path, "w");
        if (!result_file) return 2;
        if (std::fwrite(output_text.data(), 1, output_text.size(), result_file) != output_text.size()) {
            std::fclose(result_file);
            return 2;
        }
        if (std::fclose(result_file) != 0) return 2;

`
	return source[:start] + replacement + source[end:], nil
}

func (v *VibeVoiceBitNetAdapter) PrepareEnvironment(ctx context.Context) error {
	release, err := lockPythonPreparation(ctx, v.envPath)
	if err != nil {
		return err
	}
	defer release()
	if v.initialized {
		return nil
	}
	if runtime.GOOS == "windows" {
		return fmt.Errorf("VibeVoice BitNet setup currently supports Linux and macOS; upstream Windows requires a separate MinGW build")
	}
	for _, program := range []string{"git", "cmake", "ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(program); err != nil {
			return fmt.Errorf("VibeVoice BitNet setup requires %s and a C/C++ compiler on the worker", program)
		}
	}
	if err := os.MkdirAll(v.envPath, 0755); err != nil {
		return err
	}
	sourceDir := filepath.Join(v.envPath, "source")
	marker := filepath.Join(v.envPath, "runtime-version")
	expectedVersion := vibeBitNetSourceRevision + ":scriberr-output-v1"
	version, _ := os.ReadFile(marker)
	_, binaryErr := os.Stat(v.binaryPath())
	if string(version) != expectedVersion || binaryErr != nil {
		if err := os.MkdirAll(sourceDir, 0755); err != nil {
			return err
		}
		commands := [][]string{
			{"git", "init", sourceDir},
			{"git", "-C", sourceDir, "fetch", "--depth", "1", "https://github.com/microsoft/VibeASR.cpp.git", vibeBitNetSourceRevision},
			{"git", "-C", sourceDir, "checkout", "--detach", "--force", "FETCH_HEAD"},
			{"git", "-C", sourceDir, "submodule", "update", "--init", "--recursive", "--depth", "1"},
		}
		for _, args := range commands {
			if output, err := processutil.CommandContext(ctx, args[0], args[1:]...).CombinedOutput(); err != nil {
				return fmt.Errorf("VibeVoice BitNet source setup failed: %w: %s", err, output)
			}
		}
		cliPath := filepath.Join(sourceDir, "demo", "asr_infer.cpp")
		source, err := os.ReadFile(cliPath)
		if err != nil {
			return err
		}
		patched, err := patchVibeBitNetCLI(string(source))
		if err != nil {
			return err
		}
		if err := os.WriteFile(cliPath, []byte(patched), 0644); err != nil {
			return err
		}
		commands = [][]string{
			{"cmake", "-S", sourceDir, "-B", filepath.Join(sourceDir, "build"), "-DCMAKE_BUILD_TYPE=Release", "-DGGML_CUDA=OFF", "-DGGML_METAL=OFF", "-DLLAMA_CURL=OFF"},
			{"cmake", "--build", filepath.Join(sourceDir, "build"), "--target", "asr_infer", "--parallel", "2"},
		}
		for _, args := range commands {
			if output, err := processutil.CommandContext(ctx, args[0], args[1:]...).CombinedOutput(); err != nil {
				return fmt.Errorf("VibeVoice BitNet CPU build failed: %w: %s", err, output)
			}
		}
		if err := os.WriteFile(marker, []byte(expectedVersion), 0644); err != nil {
			return err
		}
	}
	for _, name := range []string{vibeBitNetVAE, vibeBitNetLM} {
		path := filepath.Join(v.envPath, "models", name)
		if stat, err := os.Stat(path); err == nil && stat.Size() > 100_000_000 {
			continue
		}
		url := "https://huggingface.co/" + vibeBitNetModel + "/resolve/" + vibeBitNetModelRevision + "/" + name
		if err := downloader.DownloadFile(ctx, url, path); err != nil {
			return fmt.Errorf("download BitNet model: %w", err)
		}
	}
	v.initialized = true
	return nil
}

func (v *VibeVoiceBitNetAdapter) buildArgs(audioPath string, params map[string]interface{}) []string {
	threads := v.GetIntParameter(params, "threads")
	if threads <= 0 {
		threads = 4
	}
	maxTokens := v.GetIntParameter(params, "max_new_tokens")
	if maxTokens <= 0 {
		maxTokens = 16384
	}
	args := []string{"--vae-model", filepath.Join(v.envPath, "models", vibeBitNetVAE), "--lm-model", filepath.Join(v.envPath, "models", vibeBitNetLM),
		"--audio", audioPath, "-t", strconv.Itoa(threads), "-c", strconv.Itoa(v.GetIntParameter(params, "context_size")),
		"--max-tokens", strconv.Itoa(maxTokens), "--prompt-format", "text", "--greedy"}
	if guidance := recognitionContext(params); guidance != "" {
		args = append(args, "--context", guidance)
	}
	return args
}

func (v *VibeVoiceBitNetAdapter) ValidateParameters(params map[string]interface{}) error {
	if v.GetStringParameter(params, "device") == "cuda" {
		return fmt.Errorf("VibeVoice BitNet uses CPU only; select cpu or auto")
	}
	if err := v.BaseAdapter.ValidateParameters(params); err != nil {
		return err
	}
	if chunk := v.GetIntParameter(params, "chunk_duration"); chunk != 0 && chunk != 30 {
		return fmt.Errorf("VibeVoice BitNet uses 30-second recognition windows; choose 30 or zero for the default")
	}
	if v.GetStringParameter(params, "diarize_model") == "native" {
		return fmt.Errorf("VibeVoice BitNet has no native speaker labels; select an external diarizer with word alignment")
	}
	if v.GetBoolParameter(params, "diarize") && !v.GetBoolParameter(params, "align_words") {
		return fmt.Errorf("VibeVoice BitNet requires word alignment for external diarization; enable align_words")
	}
	return nil
}

func (v *VibeVoiceBitNetAdapter) Transcribe(ctx context.Context, input interfaces.AudioInput, params map[string]interface{}, procCtx interfaces.ProcessingContext) (*interfaces.TranscriptResult, error) {
	if err := v.ValidateAudioInput(input); err != nil {
		return nil, err
	}
	if err := v.ValidateParameters(params); err != nil {
		return nil, err
	}
	if err := v.PrepareEnvironment(ctx); err != nil {
		return nil, err
	}
	started := time.Now()
	directory, err := v.CreateTempDirectory(procCtx)
	if err != nil {
		return nil, err
	}
	defer v.CleanupTempDirectory(directory)
	log, err := os.OpenFile(filepath.Join(procCtx.OutputDirectory, "transcription.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	defer log.Close()
	audioPath := filepath.Join(directory, "audio.wav")
	convert := processutil.CommandContext(ctx, "ffmpeg", "-nostdin", "-v", "error", "-i", input.FilePath, "-ar", "24000", "-ac", "1", "-c:a", "pcm_s16le", audioPath)
	convert.Stdout, convert.Stderr = log, log
	if err := convert.Run(); err != nil {
		return nil, fmt.Errorf("BitNet audio conversion failed: %w", err)
	}
	probe := processutil.CommandContext(ctx, "ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", audioPath)
	durationBytes, err := probe.Output()
	if err != nil {
		return nil, fmt.Errorf("probe BitNet audio duration: %w", err)
	}
	duration, err := strconv.ParseFloat(strings.TrimSpace(string(durationBytes)), 64)
	if err != nil || duration <= 0 {
		return nil, fmt.Errorf("invalid BitNet audio duration")
	}
	result := &interfaces.TranscriptResult{Language: v.GetStringParameter(params, "language")}
	var texts []string
	// The official compressed 1.5B model outputs plain text. Keep recognition
	// windows within the shared aligner's 30-second limit; never invent words or
	// speaker boundaries from a whole-recording timestamp.
	for index, offset := 0, 0.0; offset < duration; index, offset = index+1, offset+30 {
		length := min(30.0, duration-offset)
		chunkPath := filepath.Join(directory, fmt.Sprintf("chunk-%05d.wav", index))
		chunk := processutil.CommandContext(ctx, "ffmpeg", "-nostdin", "-v", "error", "-ss", strconv.FormatFloat(offset, 'f', 3, 64), "-i", audioPath, "-t", strconv.FormatFloat(length, 'f', 3, 64), "-c:a", "pcm_s16le", chunkPath)
		chunk.Stdout, chunk.Stderr = log, log
		if err := chunk.Run(); err != nil {
			return nil, fmt.Errorf("BitNet audio chunk failed: %w", err)
		}
		outputPath := filepath.Join(directory, fmt.Sprintf("result-%05d.txt", index))
		cmd := processutil.CommandContext(ctx, v.binaryPath(), v.buildArgs(chunkPath, params)...)
		cmd.Env = withEnvironmentValue(os.Environ(), "SCRIBERR_RESULT_PATH", outputPath)
		cmd.Stdout, cmd.Stderr = log, log
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("VibeVoice BitNet inference failed: %w; check transcription log and context/token budgets", err)
		}
		data, err := os.ReadFile(outputPath)
		if err != nil {
			return nil, fmt.Errorf("VibeVoice BitNet produced no completed transcript: %w", err)
		}
		text := strings.TrimSpace(string(data))
		if text != "" {
			result.Segments = append(result.Segments, interfaces.TranscriptSegment{Start: offset, End: offset + length, Text: text})
			texts = append(texts, text)
		}
		if err := os.Remove(chunkPath); err != nil {
			return nil, err
		}
	}
	result.Text = strings.Join(texts, " ")
	timestampSource := "audio_window_bounds"
	if v.GetBoolParameter(params, "align_words") && len(result.Segments) > 0 {
		alignmentEnv := filepath.Join(v.envPath, "alignment")
		if err := PrepareLocalASREnvironment(ctx, alignmentEnv); err != nil {
			return nil, fmt.Errorf("prepare BitNet word alignment: %w", err)
		}
		transcriptPath := filepath.Join(directory, "alignment-input.json")
		encoded, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(transcriptPath, encoded, 0600); err != nil {
			return nil, err
		}
		alignedPath := filepath.Join(directory, "aligned.json")
		cmd := processutil.CommandContext(ctx, "uv", "run", "--no-sync", "--project", alignmentEnv, "python", filepath.Join(alignmentEnv, "align_transcript.py"), "--audio", audioPath, "--transcript", transcriptPath, "--output", alignedPath, "--device", "cpu", "--precision", "float32", "--language", result.Language)
		cmd.Env = os.Environ()
		if token := strings.TrimSpace(v.GetStringParameter(params, "hf_token")); token != "" {
			cmd.Env = withEnvironmentValue(cmd.Env, "HF_TOKEN", token)
		}
		cmd.Stdout, cmd.Stderr = log, log
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("BitNet word alignment failed: %w; see transcription log", err)
		}
		aligned, err := os.ReadFile(alignedPath)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(aligned, result); err != nil {
			return nil, fmt.Errorf("invalid BitNet word alignment: %w", err)
		}
		if len(result.WordSegments) == 0 {
			return nil, fmt.Errorf("BitNet word alignment produced no word timestamps")
		}
		timestampSource = "forced_alignment"
	}
	result.ModelUsed, result.ProcessingTime = vibeBitNetModel, time.Since(started)
	result.Metadata = v.CreateDefaultMetadata(params)
	result.Metadata["resolved_device"] = "cpu"
	result.Metadata["precision"] = "i2_s+i8_s"
	result.Metadata["timestamp_source"] = timestampSource
	result.Metadata["chunk_duration"] = "30"
	return result, nil
}
