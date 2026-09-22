package transcription

import (
	"encoding/json"
	"os"
	"path/filepath"

	"scriberr/internal/transcription/interfaces"
)

func appendStageDiagnostic(directory, node, attempt string, err error) {
	if directory == "" || os.MkdirAll(directory, 0700) != nil {
		return
	}
	row := map[string]string{"stage": node, "attempt": attempt, "code": "adapter_failed"}
	if diagnostic, ok := interfaces.RuntimeDiagnostic(err); ok {
		row["code"], row["message"] = diagnostic.Code(), diagnostic.Error()
	}
	// Unclassified errors can contain credentials or transcript text. Only
	// application-owned diagnostics enter the durable log.
	file, openErr := os.OpenFile(filepath.Join(directory, "transcription.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if openErr != nil {
		return
	}
	defer file.Close()
	data, _ := json.Marshal(row)
	_, _ = file.Write(append(append([]byte("JOTIST_STAGE_DIAGNOSTIC="), data...), '\n'))
}
