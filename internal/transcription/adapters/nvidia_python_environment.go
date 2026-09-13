package adapters

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// nvidiaPythonProject keeps NeMo's Numba CUDA runtime in the same CUDA major
// family as the selected PyTorch wheels. NeMo's own cu12/cu13 extras also pin
// its training Torch version, so inference declares these dependencies itself.
func nvidiaPythonProject(project []byte, wheelURL string) []byte {
	content := strings.Replace(string(project), "https://download.pytorch.org/whl/cu126", wheelURL, 1)
	if strings.Contains(wheelURL, "/cu13") {
		content = strings.ReplaceAll(content, "numba-cuda[cu12]", "numba-cuda[cu13]")
		content = strings.ReplaceAll(content, "cuda-python>=12,<13", "cuda-python>=13,<14")
	}
	return []byte(content)
}

// copyNvidiaPythonVendors installs the reviewed OneLogger compatibility source
// before uv resolves the environment. Callers hold the preparation lock.
func copyNvidiaPythonVendors(envPath string) error {
	if err := validatePyTorchBackend(); err != nil {
		return err
	}
	if err := os.MkdirAll(envPath, 0755); err != nil {
		return err
	}
	compatibility, err := nvidiaScripts.ReadFile("py/nvidia/nvidia_compat.py")
	if err != nil {
		return err
	}
	if err := writePythonProject(filepath.Join(envPath, "nvidia_compat.py"), compatibility); err != nil {
		return err
	}
	return fs.WalkDir(nvidiaScripts, "py/nvidia/vendor", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(envPath, filepath.FromSlash(strings.TrimPrefix(path, "py/nvidia/")))
		if entry.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		data, err := nvidiaScripts.ReadFile(path)
		if err != nil {
			return err
		}
		return writePythonProject(target, data)
	})
}

func nvidiaPythonImport(envPath, statement string) string {
	return fmt.Sprintf("import runpy; runpy.run_path(%q); %s", filepath.Join(envPath, "nvidia_compat.py"), statement)
}
