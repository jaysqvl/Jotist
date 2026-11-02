package adapters

import (
	"bufio"
	"context"
	"embed"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jaysqvl/Jotist/internal/modeldownloads"
	"github.com/jaysqvl/Jotist/internal/modelstorage"
	"github.com/jaysqvl/Jotist/internal/processutil"
	"github.com/jaysqvl/Jotist/pkg/downloader"
)

//go:embed py/model_downloads/*.py py/model_downloads/pyproject.toml
var modelDownloadFiles embed.FS

type downloadArtifact struct {
	Source      string   `json:"source"`
	Revision    string   `json:"revision,omitempty"`
	Files       []string `json:"files,omitempty"`
	CacheDir    string   `json:"cache_dir,omitempty"`
	LocalDir    string   `json:"local_dir,omitempty"`
	Destination string   `json:"destination,omitempty"`
	Root        string   `json:"root,omitempty"`
	URL         string   `json:"url,omitempty"`
	SHA256      string   `json:"sha256,omitempty"`
	MinimumSize int64    `json:"minimum_size,omitempty"`
}

type ModelDownloader struct {
	root   string
	models []modeldownloads.Model
	plans  map[string][]downloadArtifact
}

// NewModelDownloader derives the selectable catalog from the same registered
// model identities as inference/storage. It never prepares an ASR environment.
func NewModelDownloader(root string) *ModelDownloader {
	d := &ModelDownloader{root: root, plans: make(map[string][]downloadArtifact)}
	options := modelstorage.DefaultOptions(root, ModelStorageCatalog())
	bySource := make(map[string]modelstorage.Model)
	for _, model := range options.Models {
		bySource[model.Source] = model
	}
	for _, files := range options.Direct {
		bySource[files.Model.Source] = files.Model
	}
	// Voice detection is selected by the runtime, rather than offered as a
	// separate transcription/speaker model. Whisper's fixed asset is included.
	delete(bySource, "snakers4/silero-vad")
	delete(bySource, "jotist/whisperx-vad")
	revisions := make(map[string]string)
	for _, spec := range LocalASRModels() {
		revisions[spec.ID] = spec.Revision
	}
	direct := make(map[string]downloadArtifact)
	fileCache := filepath.Join(root, ".model-file-cache", "hub")
	for _, spec := range []struct{ source, file, revision string }{
		{"nvidia/parakeet-tdt-0.6b-v3", "parakeet-tdt-0.6b-v3.nemo", ""},
		{"nvidia/canary-1b-v2", "canary-1b-v2.nemo", canaryModelRevision},
		{"nvidia/diar_streaming_sortformer_4spk-v2.1", "diar_streaming_sortformer_4spk-v2.1.nemo", ""},
	} {
		direct[spec.source] = downloadArtifact{Source: spec.source, Files: []string{spec.file}, Revision: spec.revision, CacheDir: fileCache, Destination: filepath.Join(root, "parakeet", spec.file), MinimumSize: 1024 * 1024}
	}
	for _, files := range options.Direct {
		if strings.HasPrefix(files.Model.Source, "torchaudio/") {
			direct[files.Model.Source] = downloadArtifact{Source: files.Model.Source, URL: "https://download.pytorch.org/torchaudio/models/" + files.Paths[0], Destination: filepath.Join(files.Root, files.Paths[0]), MinimumSize: 1024 * 1024}
		}
	}
	for source, model := range bySource {
		entry := modeldownloads.Model{ID: modelstorage.ModelID(source), Source: source, Name: model.Name, Purpose: model.Purpose, Dependencies: []string{}}
		plan := []downloadArtifact{{Source: source, Revision: revisions[source]}}
		if artifact, ok := direct[source]; ok {
			plan = []downloadArtifact{artifact}
		}
		switch source {
		case "BUT-FIT/diarizen-wavlm-large-s80-md-v2":
			entry.Dependencies = []string{"pyannote/wespeaker-voxceleb-resnet34-LM"}
		case "pyannote/speaker-diarization-3.1":
			entry.Dependencies = []string{"pyannote/segmentation-3.0", "pyannote/wespeaker-voxceleb-resnet34-LM"}
			entry.Gated = true
		case "pyannote/speaker-diarization-community-1":
			entry.Gated = true
		case "nvidia/canary-qwen-2.5b":
			for _, files := range options.Direct {
				if files.Model.Source == source {
					plan[0].LocalDir = filepath.Join(files.Root, files.Paths[0])
				}
			}
		case vibeBitNetModel:
			plan = nil
			for _, file := range []string{vibeBitNetVAE, vibeBitNetLM} {
				plan = append(plan, downloadArtifact{Source: source, Revision: vibeBitNetModelRevision, Files: []string{file}, CacheDir: fileCache, Destination: filepath.Join(root, "vibevoice-bitnet", "models", file), MinimumSize: 100_000_000})
			}
		}
		for _, dependency := range entry.Dependencies {
			plan = append(plan, downloadArtifact{Source: dependency})
		}
		if strings.HasPrefix(source, "Systran/faster-whisper-") {
			entry.Dependencies = append(entry.Dependencies, "jotist/whisperx-vad")
			// Same pinned/hash-checked asset as WhisperX's on-demand VAD hook.
			var target string
			for _, files := range options.Direct {
				if files.Model.Source == "jotist/whisperx-vad" {
					target = filepath.Join(files.Root, files.Paths[0], "0b5b3216d60a2d32fc086b47ea8c67589aaeb26b7e07fcbe620d6d0b83e209ea.bin")
				}
			}
			plan = append(plan, downloadArtifact{Source: "jotist/whisperx-vad", URL: "https://raw.githubusercontent.com/m-bain/whisperx/2cfd7b7c5c7bba144954364db747319b50e8232b/whisperx/assets/pytorch_model.bin", Destination: target, SHA256: "0b5b3216d60a2d32fc086b47ea8c67589aaeb26b7e07fcbe620d6d0b83e209ea", MinimumSize: 17_719_102})
		}
		d.models = append(d.models, entry)
		for i := range plan {
			if plan[i].Destination != "" {
				plan[i].Root = filepath.Dir(plan[i].Destination)
				if plan[i].Source == "jotist/whisperx-vad" {
					plan[i].Root = filepath.Dir(plan[i].Root)
				}
			}
		}
		d.plans[entry.ID] = plan
	}
	sort.Slice(d.models, func(i, j int) bool { return d.models[i].Name < d.models[j].Name })
	return d
}

func (d *ModelDownloader) Catalog() []modeldownloads.Model {
	result := append([]modeldownloads.Model{}, d.models...)
	for i := range result {
		result[i].Dependencies = append([]string{}, result[i].Dependencies...)
	}
	return result
}

func (d *ModelDownloader) prepare(ctx context.Context) (string, error) {
	path := filepath.Join(d.root, "model-downloads")
	release, err := lockPythonPreparation(ctx, path)
	if err != nil {
		return "", err
	}
	defer release()
	project, err := modelDownloadFiles.ReadFile("py/model_downloads/pyproject.toml")
	if err != nil {
		return "", err
	}
	if err := refreshPythonProjectData(project, path); err != nil {
		return "", err
	}
	script, err := modelDownloadFiles.ReadFile("py/model_downloads/download.py")
	if err != nil {
		return "", err
	}
	if err := writePythonProject(filepath.Join(path, "download.py"), script); err != nil {
		return "", err
	}
	if !checkPythonEnvironmentReady(ctx, path, "import huggingface_hub, tqdm") {
		return "", modeldownloads.DownloadError{Code: "runtime_setup"}
	}
	return path, nil
}

func (d *ModelDownloader) Download(ctx context.Context, model modeldownloads.Model, token string, progress func(modeldownloads.Progress)) error {
	plan, ok := d.plans[model.ID]
	if !ok {
		return modeldownloads.ErrUnknown
	}
	path, err := d.prepare(ctx)
	if err != nil {
		return err
	}
	// Direct NeMo/BitNet/torchaudio destinations also used by automatic setup
	// share an ordered lock set. HF's own file locks coordinate Hub downloads.
	var destinations []string
	for _, artifact := range plan {
		if artifact.Destination != "" {
			destinations = append(destinations, artifact.Destination)
		}
	}
	sort.Strings(destinations)
	var locked func(int) error
	locked = func(index int) error {
		if index < len(destinations) {
			return downloader.WithDestination(ctx, destinations[index], func() error { return locked(index + 1) })
		}
		return d.run(ctx, path, plan, token, progress)
	}
	return locked(0)
}

func (d *ModelDownloader) run(ctx context.Context, path string, plan []downloadArtifact, token string, progress func(modeldownloads.Progress)) error {
	data, err := json.Marshal(struct {
		Artifacts   []downloadArtifact `json:"artifacts"`
		Token       string             `json:"token"`
		WatchParent bool               `json:"watch_parent"`
	}{plan, token, true})
	if err != nil {
		return err
	}
	cmd := processutil.CommandContext(ctx, "uv", "run", "--no-sync", "--project", path, "python", "-I", filepath.Join(path, "download.py"))
	cmd.Env = withEnvironmentValue(withEnvironmentValue(os.Environ(), "CUDA_VISIBLE_DEVICES", ""), "PYTHONUNBUFFERED", "1")
	// Keep stdin open as a lifetime pipe. The helper exits on EOF if this server
	// disappears, including platforms where canceling uv alone leaves a child.
	// The token is sent once through this pipe, never argv/files/logs/job rows.
	input, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	defer input.Close()
	cmd.Stderr = io.Discard
	output, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return modeldownloads.DownloadError{Code: "runtime_setup"}
	}
	if _, err := input.Write(append(data, '\n')); err != nil {
		_ = input.Close()
		_ = cmd.Cancel()
		_ = cmd.Wait()
		return modeldownloads.DownloadError{Code: "runtime_setup"}
	}
	watchDone := make(chan struct{})
	defer close(watchDone)
	go func() {
		select {
		case <-ctx.Done():
			_ = input.Close()
		case <-watchDone:
		}
	}()
	code := "download_failed"
	verified := false
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 4096), 65536)
	for scanner.Scan() {
		var event struct {
			Type string `json:"type"`
			Code string `json:"code"`
			modeldownloads.Progress
		}
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			continue
		}
		switch event.Type {
		case "progress":
			progress(event.Progress)
			verified = event.Phase == "verifying" && event.TotalFiles > 0 && event.CompletedFiles == event.TotalFiles && event.DownloadedBytes == event.TotalBytes
		case "error":
			code = event.Code
		}
	}
	if scanner.Err() != nil && cmd.Process != nil {
		_ = cmd.Cancel()
	}
	err = cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil || scanner.Err() != nil || !verified {
		return modeldownloads.DownloadError{Code: code}
	}
	return nil
}
