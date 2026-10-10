package modelstorage

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Manager retains only a metadata snapshot. Scans never read weight contents,
// import a model, install a runtime, or follow snapshot symlinks outside a repo.
type Manager struct {
	options       Options
	gate          *Gate
	scanMu        sync.Mutex
	cached        Inventory
	filesRevision uint64
	now           func() time.Time
	rename        func(string, string) error
	purge         func(string) error
}

type movedAsset struct{ original, moved string }

func New(options Options) *Manager {
	if options.Gate == nil {
		options.Gate = ProcessGate()
	}
	return &Manager{options: options, gate: options.Gate, now: time.Now, rename: os.Rename, purge: os.RemoveAll}
}

const ReconcileInterval = 72 * time.Hour

func (m *Manager) SubscribeChanges() (<-chan struct{}, func()) { return m.gate.SubscribeChanges() }

func opaqueID(source string) string {
	sum := sha256.Sum256([]byte(source))
	return hex.EncodeToString(sum[:16])
}

func (m *Manager) Inventory(ctx context.Context, refresh bool) (Inventory, error) {
	m.scanMu.Lock()
	revision, changed := m.gate.fileChangesSince(m.filesRevision)
	if m.cached.ScannedAt.IsZero() || m.now().Sub(m.cached.ScannedAt) >= ReconcileInterval || refresh {
		result, err := m.scan(ctx, true)
		if err != nil {
			m.scanMu.Unlock()
			return Inventory{}, err
		}
		m.cached = result
	} else if changed.All || len(changed.Resources) > 0 {
		// Preparation and inference may fetch files. Only inspect affected
		// models; installed runtimes and unrelated repositories stay cached.
		changed = includeSharedModels(m.cached.Entries, changed)
		result, err := m.scanSelected(ctx, false, &changed)
		if err != nil {
			m.scanMu.Unlock()
			return Inventory{}, err
		}
		m.cached = mergeInventory(m.cached, result, changed)
	}
	m.filesRevision = revision
	result := m.cached
	result.Entries = append([]Entry{}, result.Entries...)
	result.Warnings = append([]string{}, result.Warnings...)
	m.scanMu.Unlock()
	// Free space needs only statfs, not a recursive inventory scan.
	result.Volumes = currentVolumes(result.volumeRoots)
	uses := m.gate.Uses()
	activityVerified := true
	if m.options.Uses != nil {
		queued, err := m.options.Uses(ctx)
		if err != nil {
			activityVerified = false
			result.Warnings = append(result.Warnings, "Run activity could not be verified. Removal is temporarily unavailable.")
		} else {
			uses = append(uses, queued...)
		}
	}
	var details map[string]Details
	if m.options.Details != nil {
		var err error
		details, err = m.options.Details(ctx)
		if err != nil {
			activityVerified = false
			result.Warnings = append(result.Warnings, "Profile references could not be verified. Removal is temporarily unavailable.")
		}
	}
	for i := range result.Entries {
		entry := &result.Entries[i]
		entry.Uses = matchingUses(entry.resources, uses)
		if len(entry.Uses) > 0 && blocksRemoval(entry.Uses[0]) {
			entry.CanRemove = false
			entry.Status = entry.Uses[0].State
			entry.Reason = "These files are in use by an active run or download."
		} else if len(entry.Uses) > 0 && entry.CanRemove && entry.Status == "downloaded" {
			entry.Status = "queued"
		}
		if !activityVerified {
			entry.CanRemove = false
			entry.Reason = "Run activity could not be verified."
		}
		if d, ok := details[entry.Source]; ok {
			entry.LastUsed, entry.Profiles, entry.CachedOnlyProfiles = d.LastUsed, d.Profiles, d.CachedOnlyProfiles
		}
	}
	// One cache-layout problem may affect several repositories. Keep each
	// warning visible once without changing the removal guards above.
	sort.Strings(result.Warnings)
	result.Warnings = slices.Compact(result.Warnings)
	return result, nil
}

func matchingUses(keys []string, uses []Use) []Use {
	result := make([]Use, 0)
	seen := make(map[string]int)
	titles := make(map[string]string)
	for _, use := range uses {
		if use.RecordingTitle != "" {
			titles[use.RecordingID] = use.RecordingTitle
		}
	}
	for _, use := range uses {
		if !overlaps(keys, use) {
			continue
		}
		key := use.RecordingID + ":" + use.Stage + ":" + use.State
		if index, ok := seen[key]; ok {
			result[index].CachedOnly = result[index].CachedOnly || use.CachedOnly
			continue
		}
		seen[key] = len(result)
		if use.RecordingTitle == "" {
			use.RecordingTitle = titles[use.RecordingID]
		}
		result = append(result, use)
	}
	sort.SliceStable(result, func(i, j int) bool {
		return blocksRemoval(result[i]) && !blocksRemoval(result[j])
	})
	return result
}

func (m *Manager) Remove(ctx context.Context, id, version string) (Removal, error) {
	var result Removal
	err := m.gate.removing(ctx, func() error {
		m.scanMu.Lock()
		defer m.scanMu.Unlock()
		// Fresh metadata and durable queue readback are both inside the same
		// barrier used for new admissions and stage reservations.
		fresh, err := m.scan(ctx, false)
		if err != nil {
			return err
		}
		var selected *Entry
		for i := range fresh.Entries {
			if fresh.Entries[i].ID == id {
				selected = &fresh.Entries[i]
				break
			}
		}
		if selected == nil {
			for _, old := range m.cached.Entries {
				if old.ID == id {
					m.gate.FilesChanged(Use{Resources: append(append([]string(nil), old.resources...), old.Source)})
					break
				}
			}
			return ErrNotFound
		}
		// Even a rejected stale confirmation should refresh that model's entry.
		defer m.gate.FilesChanged(Use{Resources: append(append([]string(nil), selected.resources...), selected.Source)})
		if version == "" || selected.Version != version {
			return ErrChanged
		}
		if !selected.CanRemove {
			return ErrReadOnly
		}
		uses := m.gate.Uses()
		if m.options.Uses != nil {
			queued, err := m.options.Uses(ctx)
			if err != nil {
				return errors.Join(ErrUnverified, err)
			}
			uses = append(uses, queued...)
		}
		for _, use := range matchingUses(selected.resources, uses) {
			if blocksRemoval(use) {
				return ErrBusy
			}
		}
		if m.options.Details != nil {
			if _, err := m.options.Details(ctx); err != nil {
				return errors.Join(ErrUnverified, err)
			}
		}
		var unlocks []func()
		defer func() {
			for i := len(unlocks) - 1; i >= 0; i-- {
				unlocks[i]()
			}
		}()
		for _, a := range selected.assets {
			if err := safeAsset(a); err != nil {
				return ErrReadOnly
			}
			if a.kind == "hub" && !a.retained {
				lockDir := filepath.Join(a.root, ".locks", filepath.Base(a.path))
				locks, err := os.ReadDir(lockDir)
				if err != nil && !errors.Is(err, os.ErrNotExist) {
					return ErrReadOnly
				}
				for _, lock := range locks {
					if lock.IsDir() || !strings.HasSuffix(lock.Name(), ".lock") {
						continue
					}
					path := filepath.Join(lockDir, lock.Name())
					info, err := os.Lstat(path)
					if err != nil || !info.Mode().IsRegular() {
						return ErrReadOnly
					}
					unlock, err := lockDownload(path)
					if err != nil {
						return err
					}
					unlocks = append(unlocks, unlock)
				}
			}
		}
		// Rename every target before unlinking any. If one location is unwritable,
		// restore prior renames so a failed request does not strand partial files.
		var moved []movedAsset
		undo := func(cause error) error {
			return rollback(m.rename, moved, cause)
		}
		for _, a := range selected.assets {
			if err := ctx.Err(); err != nil {
				return undo(err)
			}
			var token [16]byte
			if _, err := rand.Read(token[:]); err != nil {
				return undo(err)
			}
			name, _ := originalName(filepath.Base(a.path))
			target := filepath.Join(filepath.Dir(a.path), "."+name+".jotist-removal-"+hex.EncodeToString(token[:]))
			if err := m.rename(a.path, target); err != nil {
				return undo(err)
			}
			moved = append(moved, movedAsset{a.path, target})
		}
		for _, a := range moved {
			if err := m.purge(a.moved); err != nil {
				return fmt.Errorf("some model files remain for cleanup: %w", err)
			}
		}
		result = Removal{ID: id, RemovedBytes: selected.RemovableBytes, Message: "Model files removed. Profiles and completed runs are unchanged."}
		return nil
	})
	return result, err
}

func rollback(rename func(string, string) error, moved []movedAsset, cause error) error {
	for i := len(moved) - 1; i >= 0; i-- {
		if err := rename(moved[i].moved, moved[i].original); err != nil {
			cause = errors.Join(cause, fmt.Errorf("could not restore a model location: %w", err))
		}
	}
	return cause
}

func (m *Manager) scan(ctx context.Context, otherStorage bool) (Inventory, error) {
	return m.scanSelected(ctx, otherStorage, nil)
}

func (m *Manager) scanSelected(ctx context.Context, otherStorage bool, selected *Use) (Inventory, error) {
	result := Inventory{ScannedAt: m.now().UTC(), Entries: []Entry{}, Volumes: []Volume{}, Warnings: []string{}}
	entries := make(map[string]*Entry)
	models := make(map[string]Model)
	directFiles := append([]DirectFiles(nil), m.options.Direct...)
	if m.options.AdditionalDirect != nil {
		extra, err := m.options.AdditionalDirect(ctx)
		if err != nil {
			return Inventory{}, err
		}
		directFiles = append(directFiles, extra...)
	}
	for _, model := range m.options.Models {
		models[model.Source] = model
	}
	for _, direct := range directFiles {
		models[direct.Model.Source] = direct.Model
	}
	add := func(model Model, a asset) error {
		entry := entries[model.Source]
		if entry == nil {
			entry = &Entry{ID: opaqueID(model.Source), Name: model.Name, Source: model.Source, Purpose: model.Purpose, Status: "downloaded", CanRemove: len(model.Resources) > 0, resources: model.Resources, files: make(map[string]fileSize), Uses: []Use{}}
			entries[model.Source] = entry
		}
		for _, old := range entry.assets {
			if old.path == a.path {
				return nil
			}
		}
		entry.assets = append(entry.assets, a)
		entry.Locations++
		if len(model.Resources) == 0 {
			entry.CanRemove = false
			entry.Reason = "This repository is not managed by a registered Jotist model."
		}
		if a.retained {
			entry.Status = "removal_pending"
		}
		if err := inspectAsset(ctx, a, entry); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			entry.CanRemove = false
			entry.Reason = "The cache layout could not be safely verified."
			result.Warnings = append(result.Warnings, "Some model files could not be fully inspected.")
		}
		return nil
	}
	roots := append([]string(nil), m.options.HubRoots...)
	if m.options.AdditionalHubRoots != nil {
		extra, err := m.options.AdditionalHubRoots(ctx)
		if err != nil {
			return Inventory{}, err
		}
		roots = append(roots, extra...)
	}
	rootSeen := make(map[string]bool)
	for _, configured := range roots {
		root, err := canonical(configured)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			result.Warnings = append(result.Warnings, "A model cache location could not be inspected.")
			continue
		}
		if rootSeen[root] {
			continue
		}
		rootSeen[root] = true
		dirs, err := os.ReadDir(root)
		if err != nil {
			result.Warnings = append(result.Warnings, "A model cache location could not be inspected.")
			continue
		}
		for _, dir := range dirs {
			name, retained := originalName(dir.Name())
			if !strings.HasPrefix(name, "models--") {
				continue
			}
			source := strings.ReplaceAll(strings.TrimPrefix(name, "models--"), "--", "/")
			model, known := models[source]
			if !known {
				model = Model{Source: source, Name: source, Purpose: "Other cached repository"}
			}
			if selected != nil && !selectedModel(model.Source, model.Resources, *selected) {
				continue
			}
			if err := add(model, asset{root: root, path: filepath.Join(root, dir.Name()), kind: "hub", retained: retained}); err != nil {
				return Inventory{}, err
			}
		}
	}
	for _, direct := range directFiles {
		if selected != nil && !selectedModel(direct.Model.Source, direct.Model.Resources, *selected) {
			continue
		}
		root, err := canonical(direct.Root)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			result.Warnings = append(result.Warnings, "A model location could not be inspected.")
			continue
		}
		rootSeen[root] = true
		for _, relative := range direct.Paths {
			if filepath.IsAbs(relative) || relative == "." || strings.HasPrefix(filepath.Clean(relative), "..") {
				return Inventory{}, ErrReadOnly
			}
			parent := filepath.Join(root, filepath.Dir(relative))
			children, err := os.ReadDir(parent)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				result.Warnings = append(result.Warnings, "A model location could not be inspected.")
				continue
			}
			for _, child := range children {
				name, retained := originalName(child.Name())
				if name != filepath.Base(relative) {
					continue
				}
				if err := add(direct.Model, asset{root: root, path: filepath.Join(parent, child.Name()), kind: "direct", retained: retained}); err != nil {
					return Inventory{}, err
				}
			}
		}
	}
	for _, entry := range entries {
		if entry.Revisions == 0 && len(entry.assets) > 0 && entry.assets[0].kind == "hub" && entry.Status == "downloaded" {
			entry.Status = "partial"
		}
		sort.Strings(entry.signatures)
		sum := sha256.Sum256([]byte(entry.Source + strings.Join(entry.signatures, "\n")))
		entry.Version = hex.EncodeToString(sum[:])
		result.Entries = append(result.Entries, *entry)
	}
	// A missing/unreadable model location leaves the inventory incomplete. Avoid
	// removing only one visible copy while another configured copy is unknown.
	if len(result.Warnings) > 0 {
		for i := range result.Entries {
			result.Entries[i].CanRemove = false
			result.Entries[i].Reason = "Model storage could not be fully inspected."
		}
	}
	all := countEntries(&result)
	if otherStorage {
		var excluded []string
		for _, entry := range result.Entries {
			for _, a := range entry.assets {
				excluded = append(excluded, a.path)
			}
		}
		runtimeRoots := append([]string{m.options.EnvironmentRoot}, m.options.OtherRuntimeRoots...)
		packageRoots := append([]string{m.options.PackageRoot}, m.options.OtherDownloadRoots...)
		runtimeExcluded := append([]string(nil), excluded...)
		for _, configured := range packageRoots {
			if path, err := canonical(configured); err == nil {
				runtimeExcluded = append(runtimeExcluded, path)
			}
		}
		result.RuntimeBytes = otherBytes(ctx, runtimeRoots, runtimeExcluded, all, &result.Warnings)
		result.PackageCacheBytes = otherBytes(ctx, packageRoots, excluded, all, &result.Warnings)
		if err := ctx.Err(); err != nil {
			return Inventory{}, err
		}
		for _, direct := range directFiles {
			if root, err := canonical(direct.Root); err == nil {
				rootSeen[root] = true
			}
		}
		for _, configured := range append(runtimeRoots, packageRoots...) {
			if root, err := canonical(configured); err == nil {
				rootSeen[root] = true
			}
		}
	}
	for root := range rootSeen {
		result.volumeRoots = append(result.volumeRoots, root)
	}
	sort.Strings(result.volumeRoots)
	result.Volumes = currentVolumes(result.volumeRoots)
	return result, nil
}

func currentVolumes(roots []string) []Volume {
	result := []Volume{}
	seen := make(map[string]bool)
	for _, root := range roots {
		if key, value, err := volume(root); err == nil && !seen[key] {
			seen[key] = true
			result = append(result, value)
		}
	}
	return result
}

func selectedModel(source string, resources []string, use Use) bool {
	return overlaps(resources, use) || overlaps([]string{source}, use)
}

func includeSharedModels(entries []Entry, use Use) Use {
	if use.All {
		return use
	}
	selected := make(map[string]bool)
	files := make(map[string]bool)
	for changed := true; changed; {
		changed = false
		for _, entry := range entries {
			if selected[entry.Source] {
				continue
			}
			matches := selectedModel(entry.Source, entry.resources, use)
			for key := range entry.files {
				matches = matches || files[key]
			}
			if matches {
				selected[entry.Source] = true
				use.Resources = append(use.Resources, entry.Source)
				for key := range entry.files {
					files[key] = true
				}
				changed = true
			}
		}
	}
	return use
}

// Retain the timestamp and runtime/package totals of the last full inventory.
// Per-model events are reconciled separately, including models first downloaded
// since that check. Shared inode accounting is recomputed without disk I/O.
func mergeInventory(cached, changed Inventory, use Use) Inventory {
	entries := make([]Entry, 0, len(cached.Entries)+len(changed.Entries))
	for _, entry := range cached.Entries {
		if !selectedModel(entry.Source, entry.resources, use) {
			entries = append(entries, entry)
		}
	}
	cached.Entries = append(entries, changed.Entries...)
	roots := make(map[string]bool)
	for _, root := range append(append([]string(nil), cached.volumeRoots...), changed.volumeRoots...) {
		roots[root] = true
	}
	cached.volumeRoots = nil
	for root := range roots {
		cached.volumeRoots = append(cached.volumeRoots, root)
	}
	sort.Strings(cached.volumeRoots)
	for _, warning := range changed.Warnings {
		found := false
		for _, old := range cached.Warnings {
			found = found || old == warning
		}
		if !found {
			cached.Warnings = append(cached.Warnings, warning)
		}
	}
	if len(changed.Warnings) > 0 {
		for i := range cached.Entries {
			cached.Entries[i].CanRemove = false
			cached.Entries[i].Reason = "Model storage could not be fully inspected."
		}
	}
	countEntries(&cached)
	return cached
}

func countEntries(result *Inventory) map[string]fileSize {
	all := make(map[string]fileSize)
	for _, entry := range result.Entries {
		for key, file := range entry.files {
			mergeFile(all, key, file)
		}
	}
	result.ModelBytes = 0
	for i := range result.Entries {
		entry := &result.Entries[i]
		entry.Bytes, entry.RemovableBytes, entry.SharedBytes = 0, 0, 0
		for key, file := range entry.files {
			entry.Bytes += file.size
			if len(all[key].paths) == len(file.paths) && uint64(len(file.paths)) >= file.links {
				entry.RemovableBytes += file.size
			} else {
				entry.SharedBytes += file.size
			}
		}
	}
	for _, file := range all {
		result.ModelBytes += file.size
	}
	sort.Slice(result.Entries, func(i, j int) bool {
		if result.Entries[i].Bytes == result.Entries[j].Bytes {
			return result.Entries[i].Name < result.Entries[j].Name
		}
		return result.Entries[i].Bytes > result.Entries[j].Bytes
	})
	return all
}

var retainedPattern = regexp.MustCompile(`^\.(.+)\.jotist-removal-[a-f0-9]{32}$`)

func originalName(name string) (string, bool) {
	retained := false
	for {
		match := retainedPattern.FindStringSubmatch(name)
		if match == nil {
			return name, retained
		}
		name, retained = match[1], true
	}
}

func canonical(path string) (string, error) {
	if path == "" {
		return "", os.ErrNotExist
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

func safeAsset(a asset) error {
	root, err := canonical(a.root)
	if err != nil || root != a.root {
		return ErrReadOnly
	}
	parent, err := canonical(filepath.Dir(a.path))
	if err != nil || parent != filepath.Dir(a.path) {
		return ErrReadOnly
	}
	rel, err := filepath.Rel(root, a.path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ErrReadOnly
	}
	info, err := os.Lstat(a.path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		return ErrReadOnly
	}
	if !info.Mode().IsRegular() && !info.IsDir() {
		return ErrReadOnly
	}
	return nil
}

func inspectAsset(ctx context.Context, a asset, entry *Entry) error {
	if err := safeAsset(a); err != nil {
		return err
	}
	return filepath.WalkDir(a.path, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		inode, _ := identity(path, info)
		entry.signatures = append(entry.signatures, fmt.Sprintf("%s:%s:%d:%d:%s", path, inode, info.Size(), info.ModTime().UnixNano(), info.Mode()))
		if a.kind == "hub" && path != a.path && filepath.Dir(path) == a.path {
			switch d.Name() {
			case "blobs", "snapshots", "refs", "trees", ".no_exist":
			default:
				return ErrReadOnly
			}
		}
		if d.Type()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil || filepath.IsAbs(link) {
				return ErrReadOnly
			}
			target, err := filepath.EvalSymlinks(path)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(a.path, target)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return ErrReadOnly
			}
			entry.signatures = append(entry.signatures, "link:"+path+":"+target)
			return nil
		}
		if d.IsDir() {
			if a.kind == "hub" && filepath.Base(filepath.Dir(path)) == "snapshots" {
				entry.Revisions++
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return ErrReadOnly
		}
		if strings.HasSuffix(path, ".incomplete") || strings.HasSuffix(path, ".tmp") {
			if entry.Status != "removal_pending" {
				entry.Status = "partial"
			}
		}
		key, links := identity(path, info)
		mergeFile(entry.files, key, fileSize{size: info.Size(), links: links, paths: map[string]bool{path: true}})
		return nil
	})
}

func mergeFile(files map[string]fileSize, key string, file fileSize) {
	old, exists := files[key]
	if !exists {
		file.paths = clonePaths(file.paths)
		files[key] = file
		return
	}
	for path := range file.paths {
		old.paths[path] = true
	}
	files[key] = old
}
func clonePaths(paths map[string]bool) map[string]bool {
	result := make(map[string]bool, len(paths))
	for path := range paths {
		result[path] = true
	}
	return result
}

func otherBytes(ctx context.Context, roots, excluded []string, seen map[string]fileSize, warnings *[]string) int64 {
	var total int64
	rootSeen := make(map[string]bool)
	for _, configured := range roots {
		root, err := canonical(configured)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			*warnings = append(*warnings, "Some runtime or download-cache files could not be inspected.")
			continue
		}
		if rootSeen[root] {
			continue
		}
		rootSeen[root] = true
		err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			for _, skip := range excluded {
				if path == skip || strings.HasPrefix(path, skip+string(filepath.Separator)) {
					if d.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
			}
			if d.IsDir() || d.Type()&os.ModeSymlink != 0 {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			key, links := identity(path, info)
			if _, exists := seen[key]; !exists {
				total += info.Size()
			}
			mergeFile(seen, key, fileSize{size: info.Size(), links: links, paths: map[string]bool{path: true}})
			return nil
		})
		if err != nil {
			*warnings = append(*warnings, "Some runtime or download-cache files could not be inspected.")
		}
	}
	return total
}
