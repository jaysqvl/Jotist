package adapters

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"scriberr/internal/transcription/interfaces"
)

// contextParameters describes vocabulary guidance, never a replacement transcript.
func contextParameters() []interfaces.ParameterSchema {
	return []interfaces.ParameterSchema{
		{Name: "context", Type: "string", Default: "", Description: "Brief meeting background to help recognize speech; only words supported by the audio should be transcribed", Group: "basic"},
		{Name: "context_terms", Type: "string", Default: "", Description: "Names and technical terms, one per line or separated by commas", Group: "basic"},
	}
}

func recognitionContext(params map[string]interface{}) string {
	var parts []string
	if value, ok := params["context"].(string); ok && strings.TrimSpace(value) != "" {
		parts = append(parts, strings.TrimSpace(value))
	}
	if value, ok := params["context_terms"].(string); ok && strings.TrimSpace(value) != "" {
		terms := strings.FieldsFunc(value, func(r rune) bool { return r == '\n' || r == '\r' || r == ',' })
		for i := range terms {
			terms[i] = strings.TrimSpace(terms[i])
		}
		parts = append(parts, "Vocabulary: "+strings.Join(terms, ", "))
	}
	return strings.Join(parts, "\n")
}

// mergeRuntimeMetadata keeps the backend's actual device when request defaults
// are attached to the result. In particular, "auto" is not an actual device.
func mergeRuntimeMetadata(defaults, runtime map[string]string) map[string]string {
	for key, value := range runtime {
		defaults[key] = value
	}
	return defaults
}

func readRuntimeMetadata(directory string) map[string]string {
	result := map[string]string{}
	for _, name := range []string{"result.json", "runtime.json"} {
		data, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			continue
		}
		var payload struct {
			ResolvedDevice string `json:"resolved_device"`
		}
		if json.Unmarshal(data, &payload) == nil && payload.ResolvedDevice != "" {
			result["resolved_device"] = payload.ResolvedDevice
		}
	}
	return result
}

// withRequestedDevice isolates CPU jobs before Python or NeMo can initialize
// CUDA while restoring a checkpoint. Auto and explicit CUDA preserve the host.
func withRequestedDevice(environment []string, device string) []string {
	if device == "cpu" {
		return withEnvironmentValue(environment, "CUDA_VISIBLE_DEVICES", "")
	}
	return environment
}
