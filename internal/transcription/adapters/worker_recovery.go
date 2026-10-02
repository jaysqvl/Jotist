package adapters

import (
	"encoding/json"
	"io"
	"strings"
)

// Never inherit a server environment grant. The coordinator supplies a saved,
// per-invocation budget, without credentials or user content.
func withWorkerRecoveryPolicy(env []string, params map[string]interface{}) []string {
	value := ""
	if policy, ok := params["worker_recovery_policy"].(map[string]interface{}); ok && policy != nil {
		data, err := json.Marshal(policy)
		if err == nil {
			value = string(data)
		}
	}
	return withEnvironmentValue(env, "JOTIST_RECOVERY_POLICY", value)
}

// Local ASR suppresses third-party stdout/stderr. Persist only this bounded
// bundled marker so failed invocations also consume their shared retry budget.
type recoveryEvidenceWriter struct {
	destination io.Writer
	partial     []byte
	dropping    bool
}

func (w *recoveryEvidenceWriter) Write(data []byte) (int, error) {
	for _, b := range data {
		if b == '\n' {
			if !w.dropping && strings.HasPrefix(string(w.partial), "JOTIST_RECOVERY=") {
				var event struct {
					Action string `json:"action"`
					Retry  int    `json:"retry"`
				}
				if json.Unmarshal(w.partial[len("JOTIST_RECOVERY="):], &event) == nil && event.Retry > 0 && event.Retry <= 6 {
					switch event.Action {
					case "decoder_budget_retry", "token_window_split", "native_timing_split", "native_timing_repair":
						payload, _ := json.Marshal(event)
						if _, err := w.destination.Write(append(append([]byte("JOTIST_RECOVERY="), payload...), '\n')); err != nil {
							return 0, err
						}
					}
				}
			}
			w.partial, w.dropping = w.partial[:0], false
		} else if !w.dropping {
			if len(w.partial) >= 1024 {
				w.partial, w.dropping = w.partial[:0], true
			} else {
				w.partial = append(w.partial, b)
			}
		}
	}
	return len(data), nil
}
