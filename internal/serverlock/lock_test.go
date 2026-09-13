package serverlock

import (
	"github.com/stretchr/testify/require"
	"path/filepath"
	"testing"
)

func TestExclusiveLeaseAndCleanShutdownMarker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scriberr.db")
	first, err := Acquire(path)
	require.NoError(t, err)
	require.True(t, first.PreviousShutdownClean)
	_, err = Acquire(path)
	require.ErrorIs(t, err, ErrAlreadyRunning)
	// Closing only the descriptor simulates a crashed owner: OS releases its
	// lock, but the next coordinator cannot infer child-process termination.
	require.NoError(t, first.file.Close())
	first.file = nil
	second, err := Acquire(path)
	require.NoError(t, err)
	require.False(t, second.PreviousShutdownClean)
	require.False(t, PriorWorkersStopped())
	require.NoError(t, second.MarkWorkersStopped())
	require.NoError(t, second.Close())
	third, err := Acquire(path)
	require.NoError(t, err)
	require.False(t, third.PreviousShutdownClean, "an unproven orphan is not cleared by closing the new server")
	require.NoError(t, third.Close())
	cleanPath := filepath.Join(t.TempDir(), "clean.db")
	clean, err := Acquire(cleanPath)
	require.NoError(t, err)
	require.NoError(t, clean.MarkWorkersStopped())
	require.NoError(t, clean.Close())
	reopened, err := Acquire(cleanPath)
	require.NoError(t, err)
	require.True(t, reopened.PreviousShutdownClean)
	require.NoError(t, reopened.Close())
}
