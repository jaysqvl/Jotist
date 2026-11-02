package adapters

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jaysqvl/Jotist/internal/modeldownloads"
	"github.com/jaysqvl/Jotist/internal/modelstorage"
	"github.com/jaysqvl/Jotist/internal/processutil"
)

func TestManualDownloadCatalogCoversEveryRegisteredLocalModel(t *testing.T) {
	d := NewModelDownloader(t.TempDir())
	sources := map[string]bool{}
	for _, model := range d.Catalog() {
		if sources[model.Source] {
			t.Fatalf("duplicate source: %s", model.Source)
		}
		sources[model.Source] = true
		if model.ID != modelstorage.ModelID(model.Source) || len(d.plans[model.ID]) == 0 {
			t.Fatalf("missing recipe: %+v", model)
		}
	}
	for _, model := range ModelStorageCatalog() {
		if !sources[model.Source] {
			t.Errorf("registered model missing: %s", model.Source)
		}
	}
	options := modelstorage.DefaultOptions(d.root, nil)
	for _, files := range options.Direct {
		if files.Model.Purpose != "Speech detection" && !sources[files.Model.Source] {
			t.Errorf("direct model missing: %s", files.Model.Source)
		}
	}
}

// This opt-in CI smoke exercises the actual Go -> isolated uv helper -> Hub ->
// fixed VAD path. Ordinary unit tests remain offline and use synthetic files.
func TestManualModelDownloadQualification(t *testing.T) {
	if os.Getenv("JOTIST_MODEL_DOWNLOAD_SMOKE") != "1" {
		t.Skip("explicit public-weight download qualification only")
	}
	root := t.TempDir()
	for key, value := range map[string]string{
		"HF_HOME": filepath.Join(root, "hf"), "HF_HUB_CACHE": filepath.Join(root, "hf", "hub"), "TORCH_HOME": filepath.Join(root, "torch"),
		"HF_TOKEN": "", "HUGGING_FACE_HUB_TOKEN": "", "HF_ENDPOINT": "https://huggingface.co", "HF_HUB_OFFLINE": "", "TRANSFORMERS_OFFLINE": "",
		"HF_XET_CACHE": filepath.Join(root, "xet"), "HF_ASSETS_CACHE": filepath.Join(root, "assets"),
	} {
		t.Setenv(key, value)
	}
	d := NewModelDownloader(filepath.Join(root, "runtimes"))
	var selectedFound bool
	for _, model := range d.Catalog() {
		if model.Source != "Systran/faster-whisper-tiny" {
			continue
		}
		selectedFound = true
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if err := d.Download(ctx, model, "", func(p modeldownloads.Progress) {}); err != nil {
			t.Fatal(err)
		}
		vad := filepath.Join(root, "torch", "jotist-whisperx-vad", "0b5b3216d60a2d32fc086b47ea8c67589aaeb26b7e07fcbe620d6d0b83e209ea.bin")
		info, err := os.Stat(vad)
		if err != nil || info.Size() != 17_719_103 {
			t.Fatalf("VAD was not prepared in the normal runtime cache: %v", err)
		}
		code := "import sys; from huggingface_hub import snapshot_download,hf_hub_download; snapshot_download('Systran/faster-whisper-tiny',local_files_only=True); hf_hub_download('Systran/faster-whisper-tiny','model.bin',local_files_only=True); assert 'torch' not in sys.modules"
		cmd := processutil.CommandContext(ctx, "uv", "run", "--no-sync", "--project", filepath.Join(root, "runtimes", "model-downloads"), "python", "-I", "-c", code)
		cmd.Env = withEnvironmentValue(os.Environ(), "HF_HUB_OFFLINE", "1")
		if err := cmd.Run(); err != nil {
			t.Fatal("downloaded model was not available to an offline runtime", err)
		}
	}
	if !selectedFound {
		t.Fatal("Whisper Tiny disappeared from manual catalog")
	}
}

func TestManualDownloadRecipesMatchAutomaticDestinationsAndPinnedRevisions(t *testing.T) {
	root := t.TempDir()
	d := NewModelDownloader(root)
	for _, spec := range LocalASRModels() {
		p := d.plans[modelstorage.ModelID(spec.ID)]
		if p[0].Revision != spec.Revision {
			t.Fatalf("revision drift for %s", spec.ID)
		}
	}
	for _, source := range []string{"nvidia/parakeet-tdt-0.6b-v3", "nvidia/canary-1b-v2", "nvidia/diar_streaming_sortformer_4spk-v2.1"} {
		p := d.plans[modelstorage.ModelID(source)][0]
		if filepath.Dir(p.Destination) != filepath.Join(root, "parakeet") || p.CacheDir != filepath.Join(root, ".model-file-cache", "hub") {
			t.Fatalf("unusable model destination %+v", p)
		}
		if source == "nvidia/canary-1b-v2" && p.Revision != canaryModelRevision {
			t.Fatal("canary revision drift")
		}
	}
	bitnet := d.plans[modelstorage.ModelID(vibeBitNetModel)]
	if len(bitnet) != 2 || bitnet[0].Revision != vibeBitNetModelRevision || bitnet[1].Revision != vibeBitNetModelRevision {
		t.Fatal(bitnet)
	}
	diarizen := d.plans[modelstorage.ModelID("BUT-FIT/diarizen-wavlm-large-s80-md-v2")]
	if len(diarizen) != 2 || diarizen[1].Source != "pyannote/wespeaker-voxceleb-resnet34-LM" {
		t.Fatal("missing shared embedding")
	}
}

func TestDownloadHelperCannotLoadASROrWriteToASREnvironments(t *testing.T) {
	data, err := modelDownloadFiles.ReadFile("py/model_downloads/download.py")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"import torch", "import nemo", "from transformers", "from_pretrained(", "torch.load(", "trust_remote_code"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("file helper contains inference path: %s", forbidden)
		}
	}
	d := NewModelDownloader(t.TempDir())
	data, err = json.Marshal(d.Catalog())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), d.root) || strings.Contains(string(data), "destination") {
		t.Fatal("catalog exposes filesystem paths")
	}
	// The helper recipe contains no Torch wheel source/dependency.
	project, err := modelDownloadFiles.ReadFile("py/model_downloads/pyproject.toml")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(project), "torch") {
		t.Fatal("download helper requires inference packages")
	}
	if _, err := os.Stat(filepath.Join(d.root, "parakeet")); !os.IsNotExist(err) {
		t.Fatal("catalog created an ASR environment")
	}
}
