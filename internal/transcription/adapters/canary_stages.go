package adapters

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"scriberr/internal/processutil"
	"scriberr/internal/transcription/interfaces"
)

const canaryModelRevision = "d455706339a6b32e1aa40f82c713a482a0c938e2"
const canaryModelSHA256 = "ae5ef1bf06812a95a1594a8f5f0ee9c51f35418e5ba96939fa6b98ab00431094"
const canaryCTCSHA256 = "2155f5642f9d27d73bd0c41693ddbb2420a95b32acc11576c9b36638f7ece795"
const canaryTokenizerSHA256 = "c36395c4fc6074512648baa557586c535f92b9d9682f66bf967bf4cc3ab749b8"
const canaryRecognitionSchema = "canary-native-recognition-v1"

func (c *CanaryAdapter) Stages() []interfaces.StageDescriptor {
	return []interfaces.StageDescriptor{
		{Kind: "recognition", SchemaVersion: canaryRecognitionSchema, ImplementationVersion: "canary-nemo-2.7.3-stages-v1", Recoverable: true, Cancellable: true,
			ModelArtifacts:     map[string]string{"nvidia/canary-1b-v2": canaryModelRevision, "canary-1b-v2.nemo": canaryModelSHA256},
			DevicePrecisions:   map[string][]string{"cpu": {"float32"}, "cuda": {"float16", "bfloat16", "float32"}},
			PrecisionParameter: "precision", BatchParameter: "batch_size", MeasurementSupport: []string{"process_peak_rss", "structured_cuda_failure"},
			QualificationNotes: []string{"Native NeMo recognition windows, overlap, timestamp prompt and hypotheses are retained. Alignment has a separate process boundary.", "Recognition batch reduction is not qualified: one source can expand to many native chunks independently of the requested batch size."}},
		{Kind: "alignment", SchemaVersion: "transcript-result-v1", ImplementationVersion: "canary-nemo-2.7.3-ctc-v1", Recoverable: true, Cancellable: true,
			ModelArtifacts:     map[string]string{"embedded_canary_ctc": canaryCTCSHA256, "canary_tokenizer": canaryTokenizerSHA256, "canary_archive": canaryModelSHA256},
			DevicePrecisions:   map[string][]string{"cpu": {"float32"}, "cuda": {"float32"}},
			PrecisionParameter: "alignment_precision", DefaultPrecision: "float32", BatchParameter: "alignment_batch_size",
			QualifiedBatches: []int{1}, WindowParameter: "alignment_window_seconds",
			WindowPolicy:       &interfaces.StageWindowPolicy{Unit: "seconds", Candidates: []int{20, 10}, MinimumOverlap: 2, StitchingVersion: "canary-ctc-center-logits-v1"},
			MeasurementSupport: []string{"process_peak_rss", "structured_cuda_failure"},
			QualificationNotes: []string{"Loads the existing embedded NeMo CTC artifact and tokenizer only; the recognizer is absent. Native CTC precision is FP32, including when recognition uses FP16.", "Batch 2 to 1 preserved text and all timestamps on the 77.5-second public boundary fixture; this is not a universal numerical equivalence guarantee.", "Level 4 windows bound only CTC encoder input, including at least 2 seconds of context on each side (4-second adjacent overlap). Centers tile the exact 80ms frame grid; full native-cut Viterbi, recognized text and native transcript merging remain unchanged.", "20/10-second windows preserved all 196 words on the public fixture, with up to 0.24-second word-boundary shifts on CPU. Reduced acoustic context may alter timestamps; recognition and speaker windows are unchanged."}},
	}
}

// Called under the environment preparation lock. A cached name alone is not
// proof that it contains the pinned model whose recovery identity we publish.
func (c *CanaryAdapter) verifyCanaryModel(ctx context.Context) error {
	path := filepath.Join(c.envPath, "canary-1b-v2.nemo")
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Size() == c.verifiedModelSize && info.ModTime().Equal(c.verifiedModelTime) {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	buffer := make([]byte, 1024*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := file.Read(buffer)
		if n > 0 {
			_, _ = hash.Write(buffer[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	if hex.EncodeToString(hash.Sum(nil)) != canaryModelSHA256 {
		return fmt.Errorf("cached Canary archive differs from the pinned model; replace the incompatible model cache")
	}
	c.verifiedModelSize, c.verifiedModelTime = info.Size(), info.ModTime()
	return nil
}

func (c *CanaryAdapter) RecoveryStage(kind string) (interfaces.StageDescriptor, bool) {
	for _, stage := range c.Stages() {
		if stage.Kind == kind {
			return stage, true
		}
	}
	return interfaces.StageDescriptor{}, false
}

// ResolveStageParameters makes the original native alignment batch explicit.
// The recognizer's requested batch is not the number of chunks NeMo sends to CTC.
func (c *CanaryAdapter) ResolveStageParameters(stage interfaces.StageDescriptor, params map[string]interface{}, upstream []byte) (map[string]interface{}, error) {
	resolved := make(map[string]interface{}, len(params)+2)
	for key, value := range params {
		resolved[key] = value
	}
	if stage.Kind == "recognition" {
		return resolved, nil
	}
	if stage.Kind != "alignment" {
		return nil, fmt.Errorf("unsupported Canary stage %q", stage.Kind)
	}
	var saved struct {
		State struct {
			Schema      string `json:"schema"`
			NativeBatch int    `json:"native_alignment_batch_size"`
		} `json:"recognition_state"`
	}
	if json.Unmarshal(upstream, &saved) != nil || saved.State.Schema != canaryRecognitionSchema || saved.State.NativeBatch < 1 {
		return nil, fmt.Errorf("invalid Canary recognition state")
	}
	if value, ok := resolved["alignment_precision"].(string); !ok || value == "" {
		resolved["alignment_precision"] = "float32"
	}
	if c.GetIntParameter(resolved, "alignment_batch_size") <= 0 {
		resolved["alignment_batch_size"] = saved.State.NativeBatch
	}
	if _, ok := resolved["alignment_window_seconds"]; !ok {
		resolved["alignment_window_seconds"] = 0 // Native CTC input, not a new window policy.
	}
	return resolved, nil
}

func (c *CanaryAdapter) RunStage(ctx context.Context, stage interfaces.StageDescriptor, input interfaces.AudioInput, params map[string]interface{}, procCtx interfaces.ProcessingContext, upstream []byte) ([]byte, error) {
	known, ok := c.RecoveryStage(stage.Kind)
	if !ok || stage.SchemaVersion != known.SchemaVersion || stage.ImplementationVersion != known.ImplementationVersion {
		return nil, fmt.Errorf("unsupported Canary stage contract")
	}
	if err := c.ValidateAudioInput(input); err != nil {
		return nil, err
	}
	resolved, err := c.ResolveStageParameters(stage, params, upstream)
	if err != nil {
		return nil, err
	}
	device := c.GetStringParameter(resolved, "device")
	precision := c.GetStringParameter(resolved, known.PrecisionParameter)
	if precision == "" {
		precision = known.DefaultPrecision
	}
	valid := false
	for _, candidate := range known.DevicePrecisions[device] {
		if candidate == precision {
			valid = true
		}
	}
	if !valid {
		return nil, fmt.Errorf("Canary %s requires a declared device/precision pair", stage.Kind)
	}
	temp, err := c.CreateTempDirectory(procCtx)
	if err != nil {
		return nil, err
	}
	defer c.CleanupTempDirectory(temp)
	args := []string{"run", "--system-certs", "--project", c.envPath, "python", filepath.Join(c.envPath, "canary_stages.py"), stage.Kind,
		"--audio", input.FilePath, "--output", filepath.Join(temp, "result.json"), "--model", filepath.Join(c.envPath, "canary-1b-v2.nemo"),
		"--assets", filepath.Join(c.envPath, "canary-stage-assets"), "--device", device, "--precision", precision,
		"--source-lang", c.GetStringParameter(resolved, "source_lang"), "--target-lang", c.GetStringParameter(resolved, "target_lang"), "--task", c.GetStringParameter(resolved, "task"),
		"--batch-size", strconv.Itoa(c.GetIntParameter(resolved, "batch_size")), "--chunk-len", strconv.Itoa(c.GetIntParameter(resolved, "chunk_duration"))}
	if c.GetBoolParameter(resolved, "chunking") {
		args = append(args, "--chunking")
	}
	if !c.GetBoolParameter(resolved, "timestamps") {
		args = append(args, "--no-timestamps")
	}
	if !c.GetBoolParameter(resolved, "include_confidence") {
		args = append(args, "--no-include-confidence")
	}
	if stage.Kind == "alignment" {
		path := filepath.Join(temp, "recognition.json")
		if err := os.WriteFile(path, upstream, 0600); err != nil {
			return nil, err
		}
		args = append(args, "--upstream", path, "--alignment-batch-size", strconv.Itoa(c.GetIntParameter(resolved, "alignment_batch_size")))
		if window := c.GetIntParameter(resolved, "alignment_window_seconds"); window > 0 {
			args = append(args, "--alignment-window-seconds", strconv.Itoa(window), "--overlap-seconds", strconv.FormatFloat(c.GetFloatParameter(resolved, "overlap_seconds"), 'f', -1, 64))
		}
	}
	if err := os.MkdirAll(procCtx.OutputDirectory, 0755); err != nil {
		return nil, err
	}
	log, err := os.OpenFile(filepath.Join(procCtx.OutputDirectory, "transcription.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	defer log.Close()
	cmd := processutil.CommandContext(ctx, "uv", args...)
	cmd.Env = withRequestedDevice(append(os.Environ(), "PYTHONUNBUFFERED=1", "PYTORCH_CUDA_ALLOC_CONF=expandable_segments:True", "TMPDIR="+temp), device)
	cmd.Stdout, cmd.Stderr = log, log
	started := time.Now()
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("Canary %s process failed; inspect the current attempt log", stage.Kind)
	}
	data, err := os.ReadFile(filepath.Join(temp, "result.json"))
	if err != nil {
		return nil, err
	}
	var result map[string]json.RawMessage
	if json.Unmarshal(data, &result) != nil {
		return nil, fmt.Errorf("invalid Canary stage output")
	}
	result["processing_time"], _ = json.Marshal(time.Since(started))
	return json.Marshal(result)
}

var _ interfaces.StagedTranscriptionAdapter = (*CanaryAdapter)(nil)
var _ interfaces.StageParameterResolver = (*CanaryAdapter)(nil)
