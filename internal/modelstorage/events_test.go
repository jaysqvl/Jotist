package modelstorage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func findEntry(t *testing.T, inventory Inventory, source string) Entry {
	t.Helper()
	for _, entry := range inventory.Entries {
		if entry.Source == source {
			return entry
		}
	}
	t.Fatalf("missing model %s", source)
	return Entry{}
}

func TestInventoryStaysCachedUntil72HoursOrManualRefresh(t *testing.T) {
	root := t.TempDir()
	repo := cacheFixture(t, root, "org/model", "old")
	runtime := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(runtime, "runtime"), []byte("old"), 0600))
	m := fixtureManager(root)
	m.options.EnvironmentRoot = runtime
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	scans := 0
	m.options.AdditionalHubRoots = func(context.Context) ([]string, error) { scans++; return nil, nil }
	initial, err := m.Inventory(context.Background(), false)
	require.NoError(t, err)
	require.Equal(t, 1, scans)
	require.NoError(t, os.WriteFile(filepath.Join(repo, "blobs", "weights"), []byte("external change"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(runtime, "runtime"), []byte("updated runtime"), 0600))
	now = now.Add(72*time.Hour - time.Second)
	for i := 0; i < 20; i++ {
		cached, err := m.Inventory(context.Background(), false)
		require.NoError(t, err)
		require.Equal(t, initial.ModelBytes, cached.ModelBytes)
		require.Equal(t, initial.RuntimeBytes, cached.RuntimeBytes)
		require.Equal(t, initial.ScannedAt, cached.ScannedAt)
	}
	require.Equal(t, 1, scans, "idle inventory reads must not traverse files")
	now = now.Add(time.Second)
	reconciled, err := m.Inventory(context.Background(), false)
	require.NoError(t, err)
	require.Equal(t, 2, scans)
	require.EqualValues(t, len("external change"), reconciled.ModelBytes)
	require.EqualValues(t, len("updated runtime"), reconciled.RuntimeBytes)
	require.Equal(t, now, reconciled.ScannedAt)
	// Explicit Refresh is honored even immediately after another check.
	require.NoError(t, os.WriteFile(filepath.Join(repo, "blobs", "weights"), []byte("manual refresh"), 0600))
	manual, err := m.Inventory(context.Background(), true)
	require.NoError(t, err)
	require.Equal(t, 3, scans)
	require.EqualValues(t, len("manual refresh"), manual.ModelBytes)
}

func TestActivityOnlyEventsDoNotScanFilesAndProtectionIsFresh(t *testing.T) {
	root := t.TempDir()
	cacheFixture(t, root, "org/model", "weight")
	m := fixtureManager(root)
	scans := 0
	m.options.AdditionalHubRoots = func(context.Context) ([]string, error) { scans++; return nil, nil }
	initial, err := m.Inventory(context.Background(), false)
	require.NoError(t, err)
	ch, unsubscribe := m.SubscribeChanges()
	defer unsubscribe()
	release := m.gate.BeginUse(Use{Resources: []string{"recognizer"}, State: "downloading"})
	select {
	case <-ch:
	default:
		t.Fatal("missing activity event")
	}
	busy, err := m.Inventory(context.Background(), false)
	require.NoError(t, err)
	require.Equal(t, 1, scans)
	require.False(t, busy.Entries[0].CanRemove)
	require.Equal(t, "downloading", busy.Entries[0].Status)
	_, err = m.Remove(context.Background(), initial.Entries[0].ID, initial.Entries[0].Version)
	require.ErrorIs(t, err, ErrBusy)
	release()
	available, err := m.Inventory(context.Background(), false)
	require.NoError(t, err)
	require.True(t, available.Entries[0].CanRemove)
	checked := scans
	m.gate.ActivityChanged()
	_, err = m.Inventory(context.Background(), false)
	require.NoError(t, err)
	require.Equal(t, checked, scans, "queue/profile activity must not scan model files")
}

func TestStageEventsUpdateOnlyAffectedModelsAndKeepRuntimeSnapshot(t *testing.T) {
	root := t.TempDir()
	model := cacheFixture(t, root, "org/model", "old model")
	other := cacheFixture(t, root, "org/other", "old other")
	runtime := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(runtime, "package"), []byte("old runtime"), 0600))
	m := fixtureManager(root)
	m.options.EnvironmentRoot = runtime
	initial, err := m.Inventory(context.Background(), false)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(model, "blobs", "weights"), []byte("new model download"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(other, "blobs", "weights"), []byte("unrelated external change"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(runtime, "package"), []byte("unrelated package change"), 0600))
	m.gate.FilesChanged(Use{Resources: []string{"recognizer"}})
	updated, err := m.Inventory(context.Background(), false)
	require.NoError(t, err)
	require.EqualValues(t, len("new model download"), findEntry(t, updated, "org/model").Bytes)
	require.Equal(t, findEntry(t, initial, "org/other").Version, findEntry(t, updated, "org/other").Version)
	require.Equal(t, initial.RuntimeBytes, updated.RuntimeBytes)
	require.Equal(t, initial.ScannedAt, updated.ScannedAt)
	manual, err := m.Inventory(context.Background(), true)
	require.NoError(t, err)
	require.EqualValues(t, len("unrelated external change"), findEntry(t, manual, "org/other").Bytes)
	require.EqualValues(t, len("unrelated package change"), manual.RuntimeBytes)
}

func TestFirstDownloadRemovalAndFailedPartialDownloadPublishUpdates(t *testing.T) {
	root := t.TempDir()
	m := fixtureManager(root)
	initial, err := m.Inventory(context.Background(), false)
	require.NoError(t, err)
	require.Empty(t, initial.Entries)
	release := m.gate.BeginUse(Use{Resources: []string{"recognizer"}, State: "in_use"})
	cacheFixture(t, root, "org/model", "downloaded")
	m.gate.FilesChanged(Use{Resources: []string{"recognizer"}})
	during, err := m.Inventory(context.Background(), false)
	require.NoError(t, err)
	require.False(t, during.Entries[0].CanRemove)
	release()
	after, err := m.Inventory(context.Background(), false)
	require.NoError(t, err)
	require.True(t, after.Entries[0].CanRemove)
	_, err = m.Remove(context.Background(), after.Entries[0].ID, after.Entries[0].Version)
	require.NoError(t, err)
	removed, err := m.Inventory(context.Background(), false)
	require.NoError(t, err)
	require.Empty(t, removed.Entries)
	require.Equal(t, initial.ScannedAt, removed.ScannedAt, "removal must not force a runtime/package scan")
	// A failed subprocess leaves a partial download, which still must appear.
	release = m.gate.BeginUse(Use{Resources: []string{"recognizer"}, State: "in_use"})
	partial := filepath.Join(root, "models--org--model", "blobs", "weights.incomplete")
	require.NoError(t, os.MkdirAll(filepath.Dir(partial), 0700))
	require.NoError(t, os.WriteFile(partial, []byte("partial"), 0600))
	release()
	failed, err := m.Inventory(context.Background(), false)
	require.NoError(t, err)
	require.Equal(t, "partial", failed.Entries[0].Status)
	require.EqualValues(t, len("partial"), failed.ModelBytes)
}

func TestCoalescedEventsRetainAllFileChangesAndUnsubscribe(t *testing.T) {
	gate := NewGate()
	ch, unsubscribe := gate.SubscribeChanges()
	gate.FilesChanged(Use{Resources: []string{"a"}})
	gate.FilesChanged(Use{Resources: []string{"b"}})
	gate.ActivityChanged()
	require.Len(t, ch, 1)
	revision, changed := gate.fileChangesSince(0)
	require.ElementsMatch(t, []string{"a", "b"}, changed.Resources)
	_, changed = gate.fileChangesSince(revision)
	require.Empty(t, changed.Resources)
	unsubscribe()
	unsubscribe()
	<-ch
	gate.ActivityChanged()
	require.Empty(t, ch)
}

func TestTargetedUpdatesReconcileModelsSharingHardLinkedFiles(t *testing.T) {
	root := t.TempDir()
	a := cacheFixture(t, root, "org/model", "shared")
	b := cacheFixture(t, root, "org/other", "replace")
	require.NoError(t, os.Remove(filepath.Join(b, "blobs", "weights")))
	require.NoError(t, os.Link(filepath.Join(a, "blobs", "weights"), filepath.Join(b, "blobs", "weights")))
	m := fixtureManager(root)
	initial, err := m.Inventory(context.Background(), false)
	require.NoError(t, err)
	require.EqualValues(t, len("shared"), initial.ModelBytes)
	_, err = m.Remove(context.Background(), findEntry(t, initial, "org/model").ID, findEntry(t, initial, "org/model").Version)
	require.NoError(t, err)
	after, err := m.Inventory(context.Background(), false)
	require.NoError(t, err)
	require.Len(t, after.Entries, 1)
	require.EqualValues(t, len("shared"), after.Entries[0].RemovableBytes)
	require.Zero(t, after.Entries[0].SharedBytes)
}

func TestRemovalOfExternallyDeletedModelInvalidatesItsCachedEntry(t *testing.T) {
	root := t.TempDir()
	repo := cacheFixture(t, root, "org/model", "weights")
	m := fixtureManager(root)
	initial, err := m.Inventory(context.Background(), false)
	require.NoError(t, err)
	require.NoError(t, os.RemoveAll(repo))
	_, err = m.Remove(context.Background(), initial.Entries[0].ID, initial.Entries[0].Version)
	require.ErrorIs(t, err, ErrNotFound)
	after, err := m.Inventory(context.Background(), false)
	require.NoError(t, err)
	require.Empty(t, after.Entries)
}
