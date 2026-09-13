package adapters

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResearchDiarizationMaterializesVendoredRuntime(t *testing.T) {
	fakeLocalASRUV(t)
	adapter := NewDiariZenAdapter(t.TempDir())
	if err := os.MkdirAll(adapter.envPath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(adapter.envPath, "uv.lock"), []byte("old dependency graph"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := adapter.PrepareEnvironment(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(adapter.envPath, "uv.lock")); !os.IsNotExist(err) {
		t.Fatal("research diarization upgrade retained old dependency resolutions")
	}
	for _, relative := range []string{
		"vendor/diarizen/diarizen/__init__.py",
		"vendor/diarizen/diarizen/pipelines/inference.py",
		"vendor/diarizen/UPSTREAM.json",
		"vendor/pyannote-audio/pyannote/__init__.py",
		"vendor/pyannote-audio/pyannote/audio/utils/checkpoint.py",
		"vendor/pyannote-audio/LICENSE",
	} {
		if _, err := os.Stat(filepath.Join(adapter.envPath, relative)); err != nil {
			t.Fatalf("bundled runtime file %s: %v", relative, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(adapter.envPath, "pyproject.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `path = "vendor/diarizen"`) || !strings.Contains(string(data), "https://download.pytorch.org/whl/cpu") {
		t.Fatal("runtime must resolve bundled packages with the selected wheel backend")
	}
}

func TestResearchDiarizationBundleExcludesDevelopmentState(t *testing.T) {
	err := fs.WalkDir(researchDiarizationScripts, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		for _, part := range strings.Split(path, "/") {
			if part == "__pycache__" || part == ".venv" || part == ".git" || strings.HasSuffix(part, ".pyc") {
				t.Errorf("development state embedded in runtime: %s", path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestResearchDiarizationBundleContainsEveryVendoredPythonModule(t *testing.T) {
	err := fs.WalkDir(os.DirFS("."), "py/diarizen/vendor", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == "__pycache__" || strings.HasPrefix(entry.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".py") {
			if _, err := researchDiarizationScripts.ReadFile(path); err != nil {
				t.Errorf("vendored Python module missing from runtime bundle: %s: %v", path, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestResearchDiarizationRejectsRetiredBackendBeforeWriting(t *testing.T) {
	fakeLocalASRUV(t)
	t.Setenv("PYTORCH_CUDA_VERSION", "cu128")
	path := filepath.Join(t.TempDir(), "not-created")
	adapter := NewDiariZenAdapter(path)
	if err := adapter.PrepareEnvironment(context.Background()); err == nil {
		t.Fatal("retired CUDA wheel backend was accepted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("rejected preparation wrote runtime state: %v", err)
	}
}
