package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"scriberr/internal/processutil"
	"scriberr/internal/transcription/interfaces"
	"scriberr/internal/transcription/pipeline"
)

// Opt-in actual inference. CI uses the small public Whisper checkpoint; target
// qualification reuses installed runtimes through private temporary projects.
// No production environment, database, profile or recording is modified. Only
// numeric output evidence is printed or retained by the test.
func TestRuntimeQualification(t *testing.T) {
	model := os.Getenv("JOTIST_RUNTIME_MODEL")
	if model == "" {
		t.Skip("actual model inference is an explicit release/target qualification gate")
	}
	audio, source := os.Getenv("JOTIST_RUNTIME_AUDIO"), os.Getenv("JOTIST_RUNTIME_ENV")
	if audio == "" || source == "" {
		t.Fatal("qualification requires an audio fixture and installed runtime")
	}
	// Each isolated worker may restore a full model. Serialize this opt-in
	// gate across test binaries so parallel release checks cannot duplicate
	// that memory load on a shared target host.
	lock := filepath.Join(os.TempDir(), "jotist-runtime-qualification.lock")
	if err := os.Mkdir(lock, 0700); err != nil {
		t.Fatal("qualification is already reserved; verify the previous test has exited before clearing a stale reservation")
	}
	t.Cleanup(func() { _ = os.Remove(lock) })
	root := t.TempDir()
	environment := filepath.Join(root, "runtime")
	if model == "whisperx" {
		environment = filepath.Join(root, "WhisperX")
	}
	if err := os.MkdirAll(environment, 0700); err != nil {
		t.Fatal("cannot create isolated runtime")
	}
	device := os.Getenv("JOTIST_RUNTIME_DEVICE")
	if device == "" {
		device = "cpu"
	}
	precision := "float32"
	if device == "cuda" {
		precision = "float16"
	}
	t.Setenv("UV_NO_SYNC", "1")
	copyFile := func(name string) {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(source, name))
		if err != nil || os.WriteFile(filepath.Join(environment, name), data, 0600) != nil {
			t.Fatalf("cannot copy runtime metadata %s", name)
		}
	}
	link := func(name string) {
		t.Helper()
		if err := os.Symlink(filepath.Join(source, name), filepath.Join(environment, name)); err != nil {
			t.Fatalf("cannot link installed runtime component %s", name)
		}
	}
	if model != "vibevoice-bitnet" {
		link(".venv")
		copyFile("pyproject.toml")
		copyFile("uv.lock")
		if info, err := os.Stat(filepath.Join(source, "vendor")); err == nil && info.IsDir() {
			link("vendor")
		}
	}
	ctx := processutil.WithSupervision(context.Background(), processutil.DefaultSupervisionPolicy())
	probe := processutil.CommandContext(ctx, "ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", audio)
	data, err := probe.Output()
	if err != nil {
		t.Fatal("cannot probe qualification audio")
	}
	seconds, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64)
	stat, statErr := os.Stat(audio)
	if err != nil || seconds <= 0 || statErr != nil {
		t.Fatal("invalid qualification audio")
	}
	privateAudio := filepath.Join(root, "input"+filepath.Ext(audio))
	if os.Symlink(audio, privateAudio) != nil {
		t.Fatal("cannot link read-only audio into qualification workspace")
	}
	input := interfaces.AudioInput{FilePath: privateAudio, Format: strings.TrimPrefix(filepath.Ext(audio), "."), Size: stat.Size(), Duration: time.Duration(seconds * float64(time.Second))}
	input, err = (&pipeline.AudioFormatPreprocessor{}).Process(ctx, input)
	if err != nil {
		t.Fatal("qualification audio conversion failed")
	}
	procCtx := interfaces.ProcessingContext{JobID: "runtime-qualification", TempDirectory: filepath.Join(root, "tmp"), OutputDirectory: filepath.Join(root, "output")}
	for _, directory := range []string{procCtx.TempDirectory, procCtx.OutputDirectory} {
		if os.MkdirAll(directory, 0700) != nil {
			t.Fatal("cannot create private qualification output")
		}
	}
	params := map[string]interface{}{"device": device, "precision": precision, "compute_type": "int8", "batch_size": 1, "threads": 4, "language": "en", "align_words": true, "diarize": false, "max_new_tokens": 0}
	// Private target qualification can reuse context without displaying it or
	// accepting runtime paths, credentials or checkpoint substitutions.
	if path := os.Getenv("JOTIST_RUNTIME_CONTEXT_FILE"); path != "" {
		data, readErr := os.ReadFile(path)
		var contextFields map[string]string
		if readErr != nil || json.Unmarshal(data, &contextFields) != nil {
			t.Fatal("cannot read private qualification context")
		}
		for key, value := range contextFields {
			if (key != "context" && key != "context_terms") || len([]byte(value)) > 32768 {
				t.Fatal("invalid qualification context field")
			}
			params[key] = value
		}
	}
	started := time.Now()
	var transcript *interfaces.TranscriptResult
	var diarization *interfaces.DiarizationResult
	var stages []interfaces.StageDescriptor
	var staged interface {
		interfaces.StagedTranscriptionAdapter
		ResolveStageParameters(interfaces.StageDescriptor, map[string]interface{}, []byte) (map[string]interface{}, error)
	}
	switch model {
	case "canary":
		adapter := NewCanaryAdapter(environment)
		link("canary-1b-v2.nemo")
		compat, _ := nvidiaScripts.ReadFile("py/nvidia/nvidia_compat.py")
		if writePythonProject(filepath.Join(environment, "nvidia_compat.py"), compat) != nil {
			t.Fatal("cannot materialize NVIDIA compatibility import")
		}
		if err := adapter.copyTranscriptionScript(); err != nil {
			t.Fatal("cannot materialize Canary worker")
		}
		staged, stages = adapter, adapter.Stages()
	case "whisperx":
		adapter := NewWhisperXAdapter(root)
		adapter.initialized = true
		for _, name := range []string{"whisperx_run.py", "whisperx_stage.py"} {
			data, _ := whisperxScripts.ReadFile("py/whisperx/" + name)
			if writeRuntimeScript(filepath.Join(environment, name), data, 0600) != nil {
				t.Fatal("cannot materialize WhisperX worker")
			}
		}
		params["model"], params["no_align"] = "tiny", false
		staged, stages = adapter, adapter.Stages()
	case "diarizen", "suplime":
		adapter := NewDiariZenAdapter(environment)
		if model == "suplime" {
			adapter = NewSUPlimeAdapter(environment)
		}
		script, _ := researchDiarizationScripts.ReadFile("py/research_diarize.py")
		if writeRuntimeScript(filepath.Join(environment, "research_diarize.py"), script, 0600) != nil {
			t.Fatal("cannot materialize speaker worker")
		}
		adapter.initialized = true
		diarization, err = adapter.Diarize(ctx, input, params, procCtx)
	case "vibevoice-bitnet":
		// The native runtime has no Python project. Use its already qualified
		// executable and weights without running setup against production.
		link("source")
		link("models")
		adapter := NewVibeVoiceBitNetAdapter(environment)
		adapter.initialized = true
		params["context_size"], params["max_new_tokens"], params["diarize"], params["diarize_model"] = 16384, 16384, true, "pyannote"
		alignment := filepath.Join(environment, "alignment")
		assets, digest, assetErr := localASREnvironmentAssets()
		if assetErr != nil || os.MkdirAll(alignment, 0700) != nil {
			t.Fatal("cannot prepare isolated alignment runtime")
		}
		alignmentSource := os.Getenv("JOTIST_RUNTIME_ALIGNMENT_ENV")
		if alignmentSource == "" || os.Symlink(filepath.Join(alignmentSource, ".venv"), filepath.Join(alignment, ".venv")) != nil {
			t.Fatal("qualification requires installed alignment runtime")
		}
		if (&LocalASRAdapter{envPath: alignment}).writeEnvironmentAssets(assets) != nil {
			t.Fatal("cannot materialize alignment runtime")
		}
		localASREnvironmentFor(alignment).digest.Store(digest)
		staged, stages = adapter, adapter.Stages()
	default:
		adapter, createErr := NewLocalASRAdapter(environment, model)
		if createErr != nil {
			t.Fatal("unknown qualification checkpoint")
		}
		assets, digest, assetErr := localASREnvironmentAssets()
		if assetErr != nil || adapter.writeEnvironmentAssets(assets) != nil {
			t.Fatal("cannot materialize local ASR worker")
		}
		localASREnvironmentFor(environment).digest.Store(digest)
		params["align_words"] = os.Getenv("JOTIST_RUNTIME_ALIGNMENT") != "off"
		params["chunk_duration"] = adapter.spec.DefaultChunkSeconds
		if value, parseErr := strconv.Atoi(os.Getenv("JOTIST_RUNTIME_CHUNK_SECONDS")); parseErr == nil {
			params["chunk_duration"] = value
		}
		if adapter.spec.NativeSpeakers {
			params["diarize"], params["diarize_model"] = true, "native"
		}
		transcript, err = adapter.Transcribe(ctx, input, params, procCtx)
	}
	if staged != nil {
		var upstream []byte
		for _, stage := range stages {
			if stage.Kind == "speaker_assignment" {
				continue
			}
			resolved, resolveErr := staged.ResolveStageParameters(stage, params, upstream)
			if resolveErr != nil {
				err = resolveErr
				break
			}
			upstream, err = staged.RunStage(ctx, stage, input, resolved, procCtx, upstream)
			if err != nil {
				break
			}
		}
		if err == nil {
			transcript = &interfaces.TranscriptResult{}
			err = json.Unmarshal(upstream, transcript)
		}
	}
	report := map[string]interface{}{"model": model, "device": device, "audio_seconds": seconds, "elapsed_seconds": time.Since(started).Seconds(), "completed": err == nil}
	if err != nil {
		code := "unclassified_runtime_failure"
		if diagnostic, ok := interfaces.RuntimeDiagnostic(err); ok {
			code = diagnostic.Code()
		}
		if errors.Is(err, processutil.ErrWorkerStalled) {
			code = "worker_stalled"
		}
		report["error_code"] = code
		if log, readErr := os.ReadFile(filepath.Join(procCtx.OutputDirectory, "transcription.log")); readErr == nil {
			for _, line := range strings.Split(string(log), "\n") {
				if !strings.HasPrefix(line, "JOTIST_RUNTIME_DIAGNOSTIC=") {
					continue
				}
				var row map[string]string
				if json.Unmarshal([]byte(strings.TrimPrefix(line, "JOTIST_RUNTIME_DIAGNOSTIC=")), &row) == nil {
					for _, key := range []string{"parsed_segment_count", "trailing_character_count", "trailing_timestamp_count", "trailing_speaker_count", "window_index", "window_count", "token_limit"} {
						if value, parseErr := strconv.Atoi(row[key]); parseErr == nil && value >= 0 && value <= 1000000 {
							report[key] = value
						}
					}
				}
			}
		}
	} else if transcript != nil {
		report["segments"], report["words"], report["text_characters"] = len(transcript.Segments), len(transcript.WordSegments), len(transcript.Text)
		for _, key := range []string{"auto_token_split_windows", "native_timing_retry_windows", "output_repair_count", "token_retries"} {
			if value, parseErr := strconv.Atoi(transcript.Metadata[key]); parseErr == nil && value >= 0 && value <= 100000 {
				report[key] = value
			}
		}
		speakers := map[string]bool{}
		end := 0.0
		valid := strings.TrimSpace(transcript.Text) != ""
		for _, segment := range transcript.Segments {
			valid = valid && !math.IsNaN(segment.Start) && !math.IsNaN(segment.End) && segment.Start >= 0 && segment.End >= segment.Start && segment.End <= seconds+0.25
			end = max(end, segment.End)
			if segment.Speaker != nil {
				speakers[*segment.Speaker] = true
			}
		}
		report["transcript_end_seconds"], report["speakers"], report["valid_output"] = end, len(speakers), valid
		if !valid {
			err = errors.New("qualification output failed validation")
		}
	} else if diarization != nil {
		report["segments"], report["speakers"] = len(diarization.Segments), diarization.SpeakerCount
		if len(diarization.Segments) == 0 {
			err = errors.New("qualification speaker output is empty")
		}
	}
	report["completed"] = err == nil
	encoded, _ := json.Marshal(report)
	if path := os.Getenv("JOTIST_RUNTIME_REPORT"); path != "" {
		if os.WriteFile(path, encoded, 0600) != nil {
			t.Fatal("cannot retain numeric qualification evidence")
		}
	}
	t.Logf("JOTIST_QUALIFICATION=%s", encoded)
	if err != nil {
		t.Fatal("actual runtime qualification failed; see numeric diagnostic evidence")
	}
}
