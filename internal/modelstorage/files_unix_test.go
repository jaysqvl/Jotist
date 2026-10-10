//go:build linux || darwin

package modelstorage

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestActiveDownloadLockBlocksRemovalButStaleLockDoesNot(t *testing.T) {
	root := t.TempDir()
	cacheFixture(t, root, "org/model", "weights")
	lockDir := filepath.Join(root, ".locks/models--org--model")
	require.NoError(t, os.MkdirAll(lockDir, 0755))
	f, err := os.Create(filepath.Join(lockDir, "weights.lock"))
	require.NoError(t, err)
	defer f.Close()
	require.NoError(t, unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB))
	m := fixtureManager(root)
	e := entryFor(t, m, "org/model")
	_, err = m.Remove(context.Background(), e.ID, e.Version)
	require.ErrorIs(t, err, ErrBusy)
	require.NoError(t, unix.Flock(int(f.Fd()), unix.LOCK_UN))
	_, err = m.Remove(context.Background(), e.ID, e.Version)
	require.NoError(t, err)
	_, err = os.Stat(f.Name())
	require.NoError(t, err) // HF owns retained lock files.
}
