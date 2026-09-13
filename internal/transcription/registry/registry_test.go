package registry

import (
	"context"
	"scriberr/internal/transcription/interfaces"
	"sync/atomic"
	"testing"
)

type preparationProbe struct {
	interfaces.TranscriptionAdapter
	metadata map[string]string
	calls    atomic.Int32
}

func (p *preparationProbe) GetCapabilities() interfaces.ModelCapabilities {
	return interfaces.ModelCapabilities{Metadata: p.metadata}
}
func (p *preparationProbe) PrepareEnvironment(context.Context) error { p.calls.Add(1); return nil }

func TestOptionalModelsDoNotInstallOnStartup(t *testing.T) {
	eager := &preparationProbe{}
	lazy := &preparationProbe{metadata: map[string]string{"lazy_init": "true"}}
	research := &preparationProbe{metadata: map[string]string{"optional_install": "true"}}
	r := &ModelRegistry{transcriptionAdapters: map[string]interfaces.TranscriptionAdapter{"eager": eager, "lazy": lazy, "research": research}}
	if err := r.InitializeModels(context.Background()); err != nil {
		t.Fatal(err)
	}
	if eager.calls.Load() != 1 || lazy.calls.Load() != 0 || research.calls.Load() != 0 {
		t.Fatal("startup downloaded an optional model or omitted existing preparation")
	}
}
func TestComparisonMetadataCannotMutateRegistry(t *testing.T) {
	r := &ModelRegistry{capabilities: map[string]interfaces.ModelCapabilities{"one": {Metadata: map[string]string{"version": "original"}}}}
	got := r.GetAllCapabilities()
	got["one"].Metadata["version"] = "changed"
	again := r.GetAllCapabilities()
	if again["one"].Metadata["version"] != "original" {
		t.Fatal("returned metadata aliases registry state")
	}
}
