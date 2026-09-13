package adapters

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestWhisperXVendorIncludesPackageInitializersAndLicense(t *testing.T) {
	environment := t.TempDir()
	if err := materializeWhisperXVendor(environment); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"whisperx/__init__.py", "whisperx/__main__.py", "whisperx/vads/__init__.py",
		"whisperx/asr.py", "whisperx/assets/mel_filters.npz", "LICENSE", "UPSTREAM.md",
	} {
		expected, err := whisperxScripts.ReadFile("py/whisperx/vendor/whisperx/" + name)
		if err != nil {
			t.Fatalf("missing embedded package file %s: %v", name, err)
		}
		actual, err := os.ReadFile(filepath.Join(environment, "vendor", "whisperx", name))
		if err != nil || !bytes.Equal(actual, expected) {
			t.Fatalf("package file %s was not materialized intact: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(environment, "vendor", "whisperx", "whisperx", "assets", "pytorch_model.bin")); !os.IsNotExist(err) {
		t.Fatal("checkpoint must be verified in the model cache, not embedded in the server")
	}
}
