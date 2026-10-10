package registry

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jaysqvl/Jotist/internal/transcription/adapters"
	"github.com/jaysqvl/Jotist/internal/transcription/interfaces"
	"github.com/stretchr/testify/require"
)

func TestLocalRuntimeStartupDoesNotRestoreRemovedWeights(t *testing.T) {
	root := t.TempDir()
	r := &ModelRegistry{
		transcriptionAdapters: map[string]interfaces.TranscriptionAdapter{
			"whisperx":    adapters.NewWhisperXAdapter(filepath.Join(root, "whisperx")),
			"parakeet":    adapters.NewParakeetAdapter(filepath.Join(root, "nvidia")),
			"canary":      adapters.NewCanaryAdapter(filepath.Join(root, "nvidia")),
			"canary_qwen": adapters.NewCanaryQwenAdapter(filepath.Join(root, "canary-qwen")),
		},
		diarizationAdapters: map[string]interfaces.DiarizationAdapter{
			"pyannote":   adapters.NewPyAnnoteAdapter(filepath.Join(root, "pyannote")),
			"sortformer": adapters.NewSortformerAdapter(filepath.Join(root, "nvidia")),
		},
	}
	require.NoError(t, r.InitializeModels(context.Background()))
	require.True(t, r.IsInitialized())
	files, err := os.ReadDir(root)
	require.NoError(t, err)
	require.Empty(t, files, "server startup must leave absent runtimes and model weights absent until a run uses them")
}
