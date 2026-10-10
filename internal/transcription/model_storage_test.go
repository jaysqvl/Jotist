package transcription

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jaysqvl/Jotist/internal/modelstorage"
	"github.com/jaysqvl/Jotist/internal/transcription/interfaces"
	"github.com/stretchr/testify/require"
)

func TestModelFilesStayReservedForWholeStageIncludingLegacyPath(t *testing.T) {
	for _, durable := range []bool{true, false} {
		name := "legacy"
		if durable {
			name = "durable"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newStageTestFixture(t)
			adapter := newStageTestAdapter(t, "fixture-model")
			recovery := recoveryStageContext{}
			if durable {
				recovery = fixture.execution(t, RecoveryFixed, false)
			}
			root := t.TempDir()
			file := filepath.Join(root, "model.bin")
			require.NoError(t, os.WriteFile(file, []byte("weights"), 0644))
			manager := modelstorage.New(modelstorage.Options{Gate: modelstorage.ProcessGate(), Direct: []modelstorage.DirectFiles{{Root: root, Paths: []string{"model.bin"}, Model: modelstorage.Model{Source: "test/model", Name: "Model", Resources: []string{"fixture-model"}}}}})
			inv, err := manager.Inventory(context.Background(), false)
			require.NoError(t, err)
			entered, finish := make(chan struct{}), make(chan struct{})
			result := make(chan error, 1)
			go func() {
				_, _, err := runRecoverableStage(context.Background(), recovery, "recognition", "asr", adapter, fixture.input, stageTestParams(), fixture.proc, func(map[string]interface{}) (*interfaces.TranscriptResult, error) {
					close(entered)
					<-finish
					_, err := os.ReadFile(file)
					return stageTestTranscript("fixture result"), err
				})
				result <- err
			}()
			select {
			case <-entered:
			case err := <-result:
				t.Fatalf("stage failed before test barrier: %v", err)
			case <-time.After(3 * time.Second):
				t.Fatal("stage did not start")
			}
			_, err = manager.Remove(context.Background(), inv.Entries[0].ID, inv.Entries[0].Version)
			require.ErrorIs(t, err, modelstorage.ErrBusy)
			close(finish)
			require.NoError(t, <-result)
			_, err = manager.Remove(context.Background(), inv.Entries[0].ID, inv.Entries[0].Version)
			require.NoError(t, err)
		})
	}
}

type modelStoragePreparingAdapter struct {
	*stageTestAdapter
	prepare func(context.Context) error
}

func (a *modelStoragePreparingAdapter) PrepareEnvironment(ctx context.Context) error {
	return a.prepare(ctx)
}

func TestWaitingStageDownloadsAgainOnlyAfterRemovalFinishes(t *testing.T) {
	for _, durable := range []bool{true, false} {
		name := "legacy"
		if durable {
			name = "durable"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newStageTestFixture(t)
			base := newStageTestAdapter(t, "fixture-model")
			recovery := recoveryStageContext{}
			if durable {
				recovery = fixture.execution(t, RecoveryFixed, false)
			}
			root := t.TempDir()
			file := filepath.Join(root, "model.bin")
			require.NoError(t, os.WriteFile(file, []byte("old weights"), 0644))
			manager := modelstorage.New(modelstorage.Options{Gate: modelstorage.ProcessGate(), Direct: []modelstorage.DirectFiles{{Root: root, Paths: []string{"model.bin"}, Model: modelstorage.Model{Source: "test/model", Name: "Model", Resources: []string{"fixture-model"}}}}, Uses: func(context.Context) ([]modelstorage.Use, error) {
				return []modelstorage.Use{{Resources: []string{"fixture-model"}, State: "queued"}}, nil
			}})
			inv, err := manager.Inventory(context.Background(), false)
			require.NoError(t, err)
			require.True(t, inv.Entries[0].CanRemove)
			// Block removal inside its durable activity check, before it renames
			// any weights. This is the moment a queued stage can begin starting.
			entered, finish := make(chan struct{}), make(chan struct{})
			gateManager := modelstorage.New(modelstorage.Options{Gate: modelstorage.ProcessGate(), Direct: []modelstorage.DirectFiles{{Root: root, Paths: []string{"model.bin"}, Model: modelstorage.Model{Source: "test/model", Name: "Model", Resources: []string{"fixture-model"}}}}, Uses: func(context.Context) ([]modelstorage.Use, error) {
				close(entered)
				<-finish
				return []modelstorage.Use{{Resources: []string{"fixture-model"}, State: "queued"}}, nil
			}})
			removed := make(chan error, 1)
			go func() {
				_, err := gateManager.Remove(context.Background(), inv.Entries[0].ID, inv.Entries[0].Version)
				removed <- err
			}()
			<-entered
			prepared := make(chan struct{})
			adapter := &modelStoragePreparingAdapter{stageTestAdapter: base, prepare: func(ctx context.Context) error {
				close(prepared)
				if err := base.PrepareEnvironment(ctx); err != nil {
					return err
				}
				if _, err := os.Stat(file); !errors.Is(err, os.ErrNotExist) {
					return errors.New("preparation began before old weights were removed")
				}
				return os.WriteFile(file, []byte("downloaded again"), 0644)
			}}
			result := make(chan error, 1)
			go func() {
				_, _, err := runRecoverableStage(context.Background(), recovery, "recognition", "asr", adapter, fixture.input, stageTestParams(), fixture.proc, func(map[string]interface{}) (*interfaces.TranscriptResult, error) {
					data, err := os.ReadFile(file)
					if err != nil {
						return nil, err
					}
					return stageTestTranscript(string(data)), nil
				})
				result <- err
			}()
			select {
			case <-prepared:
				t.Fatal("stage preparation bypassed removal barrier")
			case <-time.After(25 * time.Millisecond):
			}
			close(finish)
			require.NoError(t, <-removed)
			select {
			case err := <-result:
				require.NoError(t, err)
			case <-time.After(3 * time.Second):
				t.Fatal("stage did not resume after removal")
			}
			data, err := os.ReadFile(file)
			require.NoError(t, err)
			require.Equal(t, "downloaded again", string(data))
			updated, err := manager.Inventory(context.Background(), false)
			require.NoError(t, err)
			require.Len(t, updated.Entries, 1)
			require.EqualValues(t, len("downloaded again"), updated.Entries[0].Bytes)
			require.NotEqual(t, inv.Entries[0].Version, updated.Entries[0].Version)
			require.Equal(t, inv.ScannedAt, updated.ScannedAt, "stage events update the model without a full inventory scan")
		})
	}
}

func TestLegacyPreparationFailureStopsInferenceAndReleasesFiles(t *testing.T) {
	fixture := newStageTestFixture(t)
	base := newStageTestAdapter(t, "fixture-model")
	root := t.TempDir()
	file := filepath.Join(root, "model.bin.tmp")
	manager := modelstorage.New(modelstorage.Options{Gate: modelstorage.ProcessGate(), Direct: []modelstorage.DirectFiles{{Root: root, Paths: []string{"model.bin.tmp"}, Model: modelstorage.Model{Source: "test/model", Name: "Model", Resources: []string{"fixture-model"}}}}})
	initial, err := manager.Inventory(context.Background(), false)
	require.NoError(t, err)
	require.Empty(t, initial.Entries)
	adapter := &modelStoragePreparingAdapter{stageTestAdapter: base, prepare: func(context.Context) error {
		if err := os.WriteFile(file, []byte("partial download"), 0600); err != nil {
			return err
		}
		return errors.New("download unavailable")
	}}
	_, _, err = runRecoverableStage(context.Background(), recoveryStageContext{}, "recognition", "asr", adapter, fixture.input, stageTestParams(), fixture.proc, func(map[string]interface{}) (*interfaces.TranscriptResult, error) {
		t.Fatal("inference began without model files")
		return nil, nil
	})
	require.ErrorContains(t, err, "runtime preparation failed")
	for _, use := range modelstorage.ProcessGate().Uses() {
		require.NotContains(t, use.Resources, "fixture-model")
	}
	after, err := manager.Inventory(context.Background(), false)
	require.NoError(t, err)
	require.Len(t, after.Entries, 1)
	require.Equal(t, "partial", after.Entries[0].Status)
	require.True(t, after.Entries[0].CanRemove)
}
