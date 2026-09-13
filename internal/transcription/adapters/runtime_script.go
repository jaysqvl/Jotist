package adapters

import (
	_ "embed"
	"os"
	"path/filepath"
)

//go:embed py/runtime_failure.py
var runtimeFailureHelper []byte

// Keep the helper beside each isolated runner, including refreshed legacy envs.
func writeRuntimeScript(path string, script []byte, mode os.FileMode) error {
	if err := writePythonProject(filepath.Join(filepath.Dir(path), "runtime_failure.py"), runtimeFailureHelper); err != nil {
		return err
	}
	return os.WriteFile(path, script, mode)
}
