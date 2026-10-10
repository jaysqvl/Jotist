package queue

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jaysqvl/Jotist/internal/models"
	"github.com/jaysqvl/Jotist/internal/modelstorage"
	"github.com/jaysqvl/Jotist/internal/repository"
	"github.com/stretchr/testify/require"
)

func TestQueueAdmissionCannotRaceModelRemoval(t *testing.T) {
	for _, kind := range []string{"sequential", "immediate"} {
		t.Run(kind, func(t *testing.T) {
			tq, jobRepo, runRepo, _, jobID, _ := newSequentialQueueTest(t, models.StatusCompleted, func(repo repository.JobRepository) JobProcessor { return &sequentialRecordingProcessor{repo: repo} })
			root := t.TempDir()
			file := filepath.Join(root, "model.nemo")
			require.NoError(t, os.WriteFile(file, []byte("weights"), 0644))
			entered, finish := make(chan struct{}), make(chan struct{})
			var hold atomic.Bool
			manager := modelstorage.New(modelstorage.Options{Gate: modelstorage.ProcessGate(), Direct: []modelstorage.DirectFiles{{Root: root, Paths: []string{"model.nemo"}, Model: modelstorage.Model{Source: "org/model", Name: "Model", Resources: []string{"org/model"}}}}, Uses: func(context.Context) ([]modelstorage.Use, error) {
				if hold.Load() {
					close(entered)
					<-finish
				}
				return nil, nil
			}})
			inv, err := manager.Inventory(context.Background(), false)
			require.NoError(t, err)
			hold.Store(true)
			removed := make(chan error, 1)
			go func() {
				_, err := manager.Remove(context.Background(), inv.Entries[0].ID, inv.Entries[0].Version)
				removed <- err
			}()
			<-entered
			params := models.WhisperXParams{Model: "org/model", NoAlign: true}
			job, err := jobRepo.FindByID(context.Background(), jobID)
			require.NoError(t, err)
			job.Parameters = params
			job.Status = models.StatusPending
			started, admitted := make(chan struct{}), make(chan error, 1)
			go func() {
				close(started)
				if kind == "sequential" {
					_, err := tq.AddSequentialRuns(context.Background(), jobID, []models.TranscriptionQueueItem{{Parameters: params}})
					admitted <- err
				} else {
					admitted <- tq.StartImmediateRun(context.Background(), job)
				}
			}()
			<-started
			select {
			case err := <-admitted:
				t.Fatalf("admission bypassed removal: %v", err)
			case <-time.After(25 * time.Millisecond):
			}
			current, err := jobRepo.FindByID(context.Background(), jobID)
			require.NoError(t, err)
			require.Equal(t, models.StatusCompleted, current.Status)
			items, err := runRepo.List(context.Background(), jobID, true)
			require.NoError(t, err)
			require.Empty(t, items)
			close(finish)
			require.NoError(t, <-removed)
			select {
			case err := <-admitted:
				require.NoError(t, err)
			case <-time.After(time.Second):
				t.Fatal("admission did not resume")
			}
			_, err = os.Stat(file)
			require.ErrorIs(t, err, os.ErrNotExist)
			current, err = jobRepo.FindByID(context.Background(), jobID)
			require.NoError(t, err)
			require.Equal(t, models.StatusPending, current.Status)
		})
	}
}
