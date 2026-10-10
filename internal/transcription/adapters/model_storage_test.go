package adapters

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jaysqvl/Jotist/internal/models"
	"github.com/jaysqvl/Jotist/internal/modelstorage"
	"github.com/jaysqvl/Jotist/pkg/downloader"
	"github.com/stretchr/testify/require"
)

type zeroWeights struct{}

func (zeroWeights) Read(data []byte) (int, error) { clear(data); return len(data), nil }

// This uses the real HTTP-to-temp-file downloader and the real initialized
// adapter preparation path. Weights are synthetic; no inference is claimed.
func TestBitNetProfileDownloadsAgainAfterRemovalWithoutRestart(t *testing.T) {
	const size = 100_000_001 // Must pass the adapter's existing cache-size check.
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+vibeBitNetVAE && r.URL.Path != "/"+vibeBitNetLM {
			http.NotFound(w, r)
			return
		}
		requests.Add(1)
		w.Header().Set("Content-Length", "100000001")
		_, _ = io.CopyN(w, zeroWeights{}, size)
	}))
	defer server.Close()
	root := t.TempDir()
	adapter := NewVibeVoiceBitNetAdapter(filepath.Join(root, "vibevoice-bitnet"))
	adapter.initialized = true
	require.NoError(t, os.MkdirAll(filepath.Dir(adapter.binaryPath()), 0755))
	require.NoError(t, os.WriteFile(adapter.binaryPath(), []byte("preserve compiled runtime"), 0755))
	adapter.downloadWeights = func(ctx context.Context, source, dest string) error {
		require.Equal(t, "https://huggingface.co/"+vibeBitNetModel+"/resolve/"+vibeBitNetModelRevision+"/"+filepath.Base(dest), source)
		return downloader.DownloadFile(ctx, server.URL+"/"+filepath.Base(dest), dest)
	}
	profile := models.TranscriptionProfile{ID: "saved-profile", Name: "Saved BitNet profile", Parameters: models.WhisperXParams{ModelFamily: "vibevoice_bitnet", Model: vibeBitNetModel, Device: "cpu", BatchSize: 1}}
	before := profile
	require.NoError(t, adapter.PrepareEnvironment(context.Background()))
	require.EqualValues(t, 2, requests.Load())
	require.NoError(t, adapter.PrepareEnvironment(context.Background()))
	require.EqualValues(t, 2, requests.Load())
	options := modelstorage.DefaultOptions(root, ModelStorageCatalog())
	var direct []modelstorage.DirectFiles
	for _, files := range options.Direct {
		if files.Model.Source == vibeBitNetModel {
			direct = append(direct, files)
		}
	}
	options.Direct = direct
	options.HubRoots = nil
	options.OtherRuntimeRoots = nil
	options.OtherDownloadRoots = nil
	options.PackageRoot = ""
	options.Gate = modelstorage.NewGate()
	m := modelstorage.New(options)
	inv, err := m.Inventory(context.Background(), false)
	require.NoError(t, err)
	require.Len(t, inv.Entries, 1)
	_, err = m.Remove(context.Background(), inv.Entries[0].ID, inv.Entries[0].Version)
	require.NoError(t, err)
	for _, name := range []string{vibeBitNetVAE, vibeBitNetLM} {
		_, err = os.Stat(filepath.Join(adapter.envPath, "models", name))
		require.ErrorIs(t, err, os.ErrNotExist)
	}
	require.NoError(t, adapter.PrepareEnvironment(context.Background()))
	require.EqualValues(t, 4, requests.Load())
	require.NoError(t, adapter.PrepareEnvironment(context.Background()))
	require.EqualValues(t, 4, requests.Load())
	for _, name := range []string{vibeBitNetVAE, vibeBitNetLM} {
		info, err := os.Stat(filepath.Join(adapter.envPath, "models", name))
		require.NoError(t, err)
		require.EqualValues(t, size, info.Size())
	}
	binary, err := os.ReadFile(adapter.binaryPath())
	require.NoError(t, err)
	require.Equal(t, "preserve compiled runtime", string(binary))
	require.Equal(t, before, profile)
	require.True(t, adapter.initialized)
}

func TestBitNetRepullFailureDoesNotReuseMissingWeights(t *testing.T) {
	adapter := NewVibeVoiceBitNetAdapter(t.TempDir())
	adapter.initialized = true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	adapter.downloadWeights = func(ctx context.Context, _, dest string) error { return downloader.DownloadFile(ctx, server.URL, dest) }
	err := adapter.PrepareEnvironment(context.Background())
	require.ErrorContains(t, err, "download BitNet model")
	_, err = os.Stat(filepath.Join(adapter.envPath, "models", vibeBitNetVAE))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestStorageCatalogCoversEveryLocalRecognizerAndDefaultWhisperAligner(t *testing.T) {
	sources := map[string]modelstorage.Model{}
	for _, model := range ModelStorageCatalog() {
		sources[model.Source] = model
	}
	for _, model := range LocalASRModels() {
		require.Contains(t, sources, model.ID)
	}
	data, err := whisperxScripts.ReadFile("py/whisperx/vendor/whisperx/whisperx/alignment.py")
	require.NoError(t, err)
	block := strings.SplitN(strings.SplitN(string(data), "DEFAULT_ALIGN_MODELS_HF = {", 2)[1], "}", 2)[0]
	for _, line := range strings.Split(block, "\n") {
		if !strings.Contains(line, ":") {
			continue
		}
		right := strings.SplitN(line, ":", 2)[1]
		source := strings.Trim(strings.TrimSpace(strings.TrimSuffix(right, ",")), "\"'")
		require.Contains(t, sources, source)
		require.True(t, modelstorage.Referenced(sources[source], modelstorage.Use{Resources: []string{"@alignment"}}))
	}
}
