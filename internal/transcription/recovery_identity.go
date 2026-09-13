package transcription

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"scriberr/internal/models"
	"scriberr/internal/transcription/interfaces"
)

func digestBytes(data []byte) string {
	value := sha256.Sum256(data)
	return hex.EncodeToString(value[:])
}
func digestJSON(value interface{}) string { data, _ := json.Marshal(value); return digestBytes(data) }

func fileDigest(ctx context.Context, path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	h := sha256.New()
	buffer := make([]byte, 128*1024)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := file.Read(buffer)
		if n > 0 {
			_, _ = h.Write(buffer[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func privateSettingsHash(params map[string]interface{}) string {
	settings := copyStageParameters(params)
	for _, key := range []string{"hf_token", "api_key", "token", "callback_url", "model_dir", "output_dir", "log_path"} {
		delete(settings, key)
	}
	return digestJSON(settings)
}

func recoveryRequestHash(params models.WhisperXParams) string {
	params = params.WithoutSecrets()
	params.CallbackURL = nil
	params.ReuseCheckpoints = nil
	params.HFTokenSource = ""
	return digestJSON(params)
}

var executableDigest struct {
	sync.Once
	value string
}

// Unknown model revisions deliberately prevent cross-execution reuse. Their
// selected artifact can still be resumed as the output of that exact run.
// A model name or memory estimate is never presented as an immutable revision.
func stageRuntimeIdentity(ctx context.Context, adapter interfaces.ModelAdapter, executionID string) (fingerprint, modelRevision string, reusable bool) {
	executableDigest.Do(func() {
		path, err := os.Executable()
		if err == nil {
			executableDigest.value, _ = fileDigest(context.Background(), path)
		}
		if executableDigest.value == "" {
			executableDigest.value = digestBytes([]byte("unknown-executable"))
		}
	})
	capability := adapter.GetCapabilities()
	identity := map[string]interface{}{"binary": executableDigest.value, "go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH, "adapter": capability.ModelID, "adapter_version": capability.Version}
	files := map[string]string{}
	base := adapter.GetModelPath()
	for _, name := range []string{"uv.lock", "pyproject.toml", ".venv/pyvenv.cfg"} {
		if value, err := fileDigest(ctx, filepath.Join(base, name)); err == nil {
			files[name] = value
		}
	}
	identity["runtime_files"] = files
	modelRevision = capability.Metadata["revision"]
	// Only immutable commit hashes and a locked runtime qualify for new-run reuse.
	reusable = immutableModelIdentity(modelRevision) && len(files) == 3
	if modelRevision == "" {
		modelRevision = "unresolved"
	}
	identity["model_revision"] = modelRevision
	return digestJSON(identity), modelRevision, reusable
}

func immutableModelIdentity(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func stagePrecision(params map[string]interface{}, modelID string) string {
	for _, key := range []string{"precision", "compute_type"} {
		if value, ok := params[key].(string); ok && value != "" {
			return value
		}
	}
	if modelID == "vibevoice-bitnet" {
		return "quantized"
	}
	if strings.Contains(modelID, "suplime") {
		return "mixed_backend_fixed"
	}
	return "float32"
}

func stageWindow(params map[string]interface{}) float64 {
	if chunking, ok := params["chunking"].(bool); ok && !chunking {
		return 0
	}
	for _, key := range []string{"chunk_duration", "chunk_size"} {
		switch value := params[key].(type) {
		case int:
			return float64(value)
		case float64:
			return value
		}
	}
	return 0
}

func stageBatch(params map[string]interface{}) int {
	switch value := params["batch_size"].(type) {
	case int:
		return value
	case float64:
		return int(value)
	}
	return 1
}

func safeStageError(kind, code string) error {
	switch code {
	case "cuda_out_of_memory":
		return fmt.Errorf("%s stopped after GPU memory exhaustion; completed checkpoints retained", kind)
	case "host_out_of_memory":
		return fmt.Errorf("%s stopped after host memory exhaustion; completed checkpoints retained", kind)
	case "cuda_runtime_error":
		return fmt.Errorf("%s stopped after a CUDA runtime error; completed checkpoints retained", kind)
	case "cancelled":
		return context.Canceled
	case "deadline_exceeded":
		return context.DeadlineExceeded
	default:
		return fmt.Errorf("%s failed; check this attempt's model runtime, input and access. Completed checkpoints retained", kind)
	}
}
