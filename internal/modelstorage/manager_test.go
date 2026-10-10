package modelstorage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func cacheFixture(t *testing.T, root, source, contents string) string {
	t.Helper()
	repo := filepath.Join(root, "models--"+strings.ReplaceAll(source, "/", "--"))
	for _, dir := range []string{"blobs", "snapshots/revision-a", "snapshots/revision-b", "refs"} {
		require.NoError(t, os.MkdirAll(filepath.Join(repo, dir), 0755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(repo, "blobs", "weights"), []byte(contents), 0644))
	for _, rev := range []string{"revision-a", "revision-b"} {
		require.NoError(t, os.Symlink("../../blobs/weights", filepath.Join(repo, "snapshots", rev, "model.bin")))
	}
	return repo
}

func fixtureManager(root string) *Manager {
	return New(Options{HubRoots: []string{root}, Gate: NewGate(), Models: []Model{
		{Source: "org/model", Name: "Recognition model", Resources: []string{"recognizer"}},
		{Source: "org/other", Name: "Other model", Resources: []string{"other"}},
	}})
}

func entryFor(t *testing.T, m *Manager, source string) Entry {
	t.Helper()
	inv, err := m.Inventory(context.Background(), true)
	require.NoError(t, err)
	for _, entry := range inv.Entries {
		if entry.Source == source {
			return entry
		}
	}
	t.Fatalf("model %q not discovered", source)
	return Entry{}
}

func TestInventoryCountsSnapshotLinksOnceAndMergesLocations(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	cacheFixture(t, a, "org/model", "abcd")
	cacheFixture(t, b, "org/model", "123456")
	m := fixtureManager(a)
	m.options.HubRoots = []string{a, b, a}
	inv, err := m.Inventory(context.Background(), false)
	require.NoError(t, err)
	require.Len(t, inv.Entries, 1)
	e := inv.Entries[0]
	require.EqualValues(t, 10, inv.ModelBytes)
	require.EqualValues(t, 10, e.Bytes)
	require.EqualValues(t, 10, e.RemovableBytes)
	require.Equal(t, 4, e.Revisions)
	require.Equal(t, 2, e.Locations)
	require.True(t, e.CanRemove)
	_, err = m.Remove(context.Background(), e.ID, e.Version)
	require.NoError(t, err)
	for _, root := range []string{a, b} {
		_, err = os.Stat(filepath.Join(root, "models--org--model"))
		require.ErrorIs(t, err, os.ErrNotExist)
	}
}

func TestInventoryAlwaysSerializesEmptyCollectionsAsArrays(t *testing.T) {
	root := t.TempDir()
	m := fixtureManager(root)
	inv, err := m.Inventory(context.Background(), false)
	require.NoError(t, err)
	encoded, err := json.Marshal(inv)
	require.NoError(t, err)
	for _, field := range []string{"entries", "warnings", "volumes"} {
		require.Contains(t, string(encoded), `"`+field+`":[`)
	}
	cacheFixture(t, root, "org/model", "weights")
	m.cached.ScannedAt = time.Time{}
	e := entryFor(t, m, "org/model")
	encoded, err = json.Marshal(e)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"uses":[]`)
	_, err = m.Remove(context.Background(), e.ID, e.Version)
	require.NoError(t, err)
	inv, err = m.Inventory(context.Background(), false)
	require.NoError(t, err)
	encoded, err = json.Marshal(inv)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"entries":[]`)
}

func TestRemovalPreservesOtherRepositoriesAndSharedBlobs(t *testing.T) {
	root := t.TempDir()
	a := cacheFixture(t, root, "org/model", "shared weights")
	b := cacheFixture(t, root, "org/other", "discard")
	require.NoError(t, os.Remove(filepath.Join(b, "blobs/weights")))
	require.NoError(t, os.Link(filepath.Join(a, "blobs/weights"), filepath.Join(b, "blobs/weights")))
	m := fixtureManager(root)
	inv, err := m.Inventory(context.Background(), false)
	require.NoError(t, err)
	require.EqualValues(t, len("shared weights"), inv.ModelBytes)
	e := entryFor(t, m, "org/model")
	require.EqualValues(t, 0, e.RemovableBytes)
	require.EqualValues(t, len("shared weights"), e.SharedBytes)
	_, err = m.Remove(context.Background(), e.ID, e.Version)
	require.NoError(t, err)
	data, err := os.ReadFile(filepath.Join(b, "snapshots/revision-a/model.bin"))
	require.NoError(t, err)
	require.Equal(t, "shared weights", string(data))
}

func TestExternalHardLinkIsNotReportedAsReclaimable(t *testing.T) {
	root := t.TempDir()
	repo := cacheFixture(t, root, "org/model", "weight")
	require.NoError(t, os.Link(filepath.Join(repo, "blobs/weights"), filepath.Join(t.TempDir(), "external")))
	e := entryFor(t, fixtureManager(root), "org/model")
	require.Zero(t, e.RemovableBytes)
	require.EqualValues(t, 6, e.SharedBytes)
}

func TestUnsafeAndUnknownCachesAreAuditOnly(t *testing.T) {
	for _, kind := range []string{"unknown", "external-link", "symlink-repo", "unexpected-file"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			source := "org/model"
			if kind == "unknown" {
				source = "not/registered"
			}
			repo := cacheFixture(t, root, source, "safe")
			switch kind {
			case "external-link":
				require.NoError(t, os.Symlink(t.TempDir(), filepath.Join(repo, "snapshots/revision-a/external")))
			case "symlink-repo":
				require.NoError(t, os.RemoveAll(repo))
				require.NoError(t, os.Symlink(t.TempDir(), repo))
			case "unexpected-file":
				require.NoError(t, os.WriteFile(filepath.Join(repo, "unowned-file"), []byte("preserve me"), 0644))
			}
			m := fixtureManager(root)
			e := entryFor(t, m, source)
			require.False(t, e.CanRemove)
			_, err := m.Remove(context.Background(), e.ID, e.Version)
			require.ErrorIs(t, err, ErrReadOnly)
			_, err = os.Lstat(repo)
			require.NoError(t, err)
		})
	}
}

func TestChangedFilesRequireNewConfirmation(t *testing.T) {
	root := t.TempDir()
	repo := cacheFixture(t, root, "org/model", "before")
	m := fixtureManager(root)
	e := entryFor(t, m, "org/model")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "blobs/another-revision"), []byte("new"), 0644))
	_, err := m.Remove(context.Background(), e.ID, e.Version)
	require.ErrorIs(t, err, ErrChanged)
	data, err := os.ReadFile(filepath.Join(repo, "blobs/weights"))
	require.NoError(t, err)
	require.Equal(t, "before", string(data))
	_, err = m.Remove(context.Background(), "../../arbitrary", e.Version)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestRunningDownloadingAndUnknownActivityCannotBeRemoved(t *testing.T) {
	for _, state := range []string{"in_use", "downloading", "unknown"} {
		t.Run(state, func(t *testing.T) {
			root := t.TempDir()
			repo := cacheFixture(t, root, "org/model", "weights")
			m := fixtureManager(root)
			e := entryFor(t, m, "org/model")
			use := Use{Resources: []string{"recognizer"}, State: state, RecordingID: "recording", RecordingTitle: "Project planning"}
			release := m.gate.BeginUse(use)
			defer release()
			_, err := m.Remove(context.Background(), e.ID, e.Version)
			require.ErrorIs(t, err, ErrBusy)
			e = entryFor(t, m, "org/model")
			require.False(t, e.CanRemove)
			require.Equal(t, state, e.Status)
			require.Equal(t, "Project planning", e.Uses[0].RecordingTitle)
			data, err := os.ReadFile(filepath.Join(repo, "blobs/weights"))
			require.NoError(t, err)
			require.Equal(t, "weights", string(data))
		})
	}
}

func TestQueuedModelsCanBeRemovedIncludingCachedOnlyRequests(t *testing.T) {
	for _, cachedOnly := range []bool{false, true} {
		t.Run(fmt.Sprint(cachedOnly), func(t *testing.T) {
			root := t.TempDir()
			repo := cacheFixture(t, root, "org/model", "weights")
			m := fixtureManager(root)
			use := Use{Resources: []string{"recognizer"}, State: "queued", RecordingID: "recording", RecordingTitle: "Waiting recording", CachedOnly: cachedOnly}
			m.options.Uses = func(context.Context) ([]Use, error) { return []Use{use}, nil }
			e := entryFor(t, m, "org/model")
			require.True(t, e.CanRemove)
			require.Equal(t, "queued", e.Status)
			require.Equal(t, cachedOnly, e.Uses[0].CachedOnly)
			_, err := m.Remove(context.Background(), e.ID, e.Version)
			require.NoError(t, err)
			_, err = os.Stat(repo)
			require.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}

func TestActiveOrDownloadingUseOverridesQueuedWarnings(t *testing.T) {
	for _, state := range []string{"in_use", "downloading"} {
		t.Run(state, func(t *testing.T) {
			root := t.TempDir()
			cacheFixture(t, root, "org/model", "weights")
			m := fixtureManager(root)
			m.options.Uses = func(context.Context) ([]Use, error) {
				return []Use{{Resources: []string{"recognizer"}, State: "queued"}, {Resources: []string{"recognizer"}, State: state}}, nil
			}
			e := entryFor(t, m, "org/model")
			require.False(t, e.CanRemove)
			require.Equal(t, state, e.Status)
			_, err := m.Remove(context.Background(), e.ID, e.Version)
			require.ErrorIs(t, err, ErrBusy)
		})
	}
}

func TestDuplicateQueuedUsesRetainCachedOnlyWarning(t *testing.T) {
	for _, cachedFirst := range []bool{false, true} {
		uses := matchingUses([]string{"recognizer"}, []Use{
			{Resources: []string{"recognizer"}, State: "queued", RecordingID: "recording", CachedOnly: cachedFirst},
			{Resources: []string{"recognizer"}, State: "queued", RecordingID: "recording", CachedOnly: !cachedFirst},
		})
		require.Len(t, uses, 1)
		require.True(t, uses[0].CachedOnly)
	}
}

func TestUnverifiableQueueFailsClosed(t *testing.T) {
	root := t.TempDir()
	cacheFixture(t, root, "org/model", "weights")
	m := fixtureManager(root)
	m.options.Uses = func(context.Context) ([]Use, error) { return nil, errors.New("database unavailable") }
	e := entryFor(t, m, "org/model")
	require.False(t, e.CanRemove)
	_, err := m.Remove(context.Background(), e.ID, e.Version)
	require.Error(t, err)
	_, err = os.Stat(filepath.Join(root, "models--org--model"))
	require.NoError(t, err)
}

func TestUnverifiableProfileReferencesAfterConfirmationPreventRemoval(t *testing.T) {
	root := t.TempDir()
	repo := cacheFixture(t, root, "org/model", "weights")
	m := fixtureManager(root)
	m.options.Details = func(context.Context) (map[string]Details, error) {
		return map[string]Details{"org/model": {Profiles: 1}}, nil
	}
	e := entryFor(t, m, "org/model")
	require.True(t, e.CanRemove)
	m.options.Details = func(context.Context) (map[string]Details, error) {
		return nil, errors.New("profile database unavailable")
	}
	_, err := m.Remove(context.Background(), e.ID, e.Version)
	require.ErrorIs(t, err, ErrUnverified)
	require.False(t, entryFor(t, m, "org/model").CanRemove)
	data, err := os.ReadFile(filepath.Join(repo, "blobs/weights"))
	require.NoError(t, err)
	require.Equal(t, "weights", string(data))
}

func TestIncompleteConfiguredCacheLocationPreventsPartialRemoval(t *testing.T) {
	root := t.TempDir()
	repo := cacheFixture(t, root, "org/model", "weights")
	// A symlink cycle is a deterministic unreadable root even when tests run as
	// root. We cannot verify whether another copy of this model exists there.
	unknown := filepath.Join(t.TempDir(), "uninspectable")
	require.NoError(t, os.Symlink("uninspectable", unknown))
	m := fixtureManager(root)
	e := entryFor(t, m, "org/model")
	m.options.HubRoots = append(m.options.HubRoots, unknown)
	_, err := m.Remove(context.Background(), e.ID, e.Version)
	require.ErrorIs(t, err, ErrReadOnly)
	_, err = os.Stat(repo)
	require.NoError(t, err)
}

func TestUnrelatedActiveRunContinuesDuringRemoval(t *testing.T) {
	root := t.TempDir()
	cacheFixture(t, root, "org/model", "weights")
	m := fixtureManager(root)
	release := m.gate.BeginUse(Use{Resources: []string{"other"}, State: "in_use"})
	defer release()
	e := entryFor(t, m, "org/model")
	_, err := m.Remove(context.Background(), e.ID, e.Version)
	require.NoError(t, err)
	require.Len(t, m.gate.Uses(), 1)
}

func TestMultiLocationRenameFailureRestoresAllFiles(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	cacheFixture(t, a, "org/model", "first")
	cacheFixture(t, b, "org/model", "second")
	m := fixtureManager(a)
	m.options.HubRoots = []string{a, b}
	e := entryFor(t, m, "org/model")
	attempt := 0
	m.rename = func(from, to string) error {
		attempt++
		if attempt == 2 {
			return os.ErrPermission
		}
		return os.Rename(from, to)
	}
	_, err := m.Remove(context.Background(), e.ID, e.Version)
	require.ErrorIs(t, err, os.ErrPermission)
	for _, root := range []string{a, b} {
		_, err = os.Stat(filepath.Join(root, "models--org--model/blobs/weights"))
		require.NoError(t, err)
		names, err := os.ReadDir(root)
		require.NoError(t, err)
		require.Len(t, names, 1)
	}
}

func TestCancellationBetweenLocationsRestoresFilesBeforeAnyPurge(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	cacheFixture(t, a, "org/model", "first")
	cacheFixture(t, b, "org/model", "second")
	m := fixtureManager(a)
	m.options.HubRoots = []string{a, b}
	e := entryFor(t, m, "org/model")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	attempts, purges := 0, 0
	m.rename = func(from, to string) error {
		err := os.Rename(from, to)
		attempts++
		if attempts == 1 {
			cancel()
		}
		return err
	}
	m.purge = func(string) error { purges++; return nil }
	_, err := m.Remove(ctx, e.ID, e.Version)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, purges)
	for _, root := range []string{a, b} {
		_, err := os.Stat(filepath.Join(root, "models--org--model/blobs/weights"))
		require.NoError(t, err)
		names, err := os.ReadDir(root)
		require.NoError(t, err)
		require.Len(t, names, 1)
	}
}

func TestInterruptedCleanupRemainsVisibleAndCanBeRetriedAfterRestart(t *testing.T) {
	root := t.TempDir()
	cacheFixture(t, root, "org/model", "retained")
	m := fixtureManager(root)
	e := entryFor(t, m, "org/model")
	m.purge = func(string) error { return os.ErrPermission }
	_, err := m.Remove(context.Background(), e.ID, e.Version)
	require.Error(t, err)
	m = fixtureManager(root) // No reliance on process memory for retained files.
	e = entryFor(t, m, "org/model")
	require.Equal(t, "removal_pending", e.Status)
	require.EqualValues(t, 8, e.Bytes)
	_, err = m.Remove(context.Background(), e.ID, e.Version)
	require.NoError(t, err)
	inv, err := m.Inventory(context.Background(), false)
	require.NoError(t, err)
	require.Empty(t, inv.Entries)
}

func TestDirectModelRemovalPreservesRuntimeAndPackageCache(t *testing.T) {
	root := t.TempDir()
	for name, data := range map[string]string{"model.nemo": "weights", "model.nemo.tmp": "partial", "bin/python": "runtime", "uv/cache": "packages"} {
		path := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
		require.NoError(t, os.WriteFile(path, []byte(data), 0644))
	}
	m := New(Options{EnvironmentRoot: root, PackageRoot: filepath.Join(root, "uv"), Gate: NewGate(), Direct: []DirectFiles{{Model: Model{Source: "org/model", Name: "Model", Resources: []string{"recognizer"}}, Root: root, Paths: []string{"model.nemo", "model.nemo.tmp"}}}})
	inv, err := m.Inventory(context.Background(), false)
	require.NoError(t, err)
	require.EqualValues(t, 14, inv.ModelBytes)
	require.EqualValues(t, 7, inv.RuntimeBytes)
	require.EqualValues(t, 8, inv.PackageCacheBytes)
	e := inv.Entries[0]
	require.Equal(t, "partial", e.Status)
	_, err = m.Remove(context.Background(), e.ID, e.Version)
	require.NoError(t, err)
	for _, name := range []string{"bin/python", "uv/cache"} {
		_, err = os.Stat(filepath.Join(root, name))
		require.NoError(t, err)
	}
}

func TestAdmissionAndNewStageWaitForRemoval(t *testing.T) {
	root := t.TempDir()
	cacheFixture(t, root, "org/model", "weights")
	m := fixtureManager(root)
	e := entryFor(t, m, "org/model")
	entered, finish := make(chan struct{}), make(chan struct{})
	m.purge = func(path string) error { close(entered); <-finish; return os.RemoveAll(path) }
	removed := make(chan error, 1)
	go func() { _, err := m.Remove(context.Background(), e.ID, e.Version); removed <- err }()
	<-entered
	admitted, stage := make(chan struct{}), make(chan struct{})
	go func() { release := m.gate.Admission(); close(admitted); release() }()
	go func() {
		release := m.gate.BeginUse(Use{Resources: []string{"recognizer"}, State: "in_use"})
		close(stage)
		release()
	}()
	select {
	case <-admitted:
		t.Fatal("admission passed removal barrier")
	case <-time.After(25 * time.Millisecond):
	}
	select {
	case <-stage:
		t.Fatal("stage touched files during removal")
	case <-time.After(25 * time.Millisecond):
	}
	close(finish)
	require.NoError(t, <-removed)
	for _, done := range []chan struct{}{admitted, stage} {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("guard failed to release")
		}
	}
}

func TestAdmissionFinishesDurableWriteBeforeRemovalChecksActivity(t *testing.T) {
	root := t.TempDir()
	cacheFixture(t, root, "org/model", "weights")
	m := fixtureManager(root)
	e := entryFor(t, m, "org/model")
	var uses []Use
	m.options.Uses = func(context.Context) ([]Use, error) { return uses, nil }
	release := m.gate.Admission()
	checked := make(chan error, 1)
	go func() { _, err := m.Remove(context.Background(), e.ID, e.Version); checked <- err }()
	uses = []Use{{Resources: []string{"recognizer"}, State: "in_use"}}
	release()
	require.ErrorIs(t, <-checked, ErrBusy)
}
