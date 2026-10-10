// Package modelstorage audits model files without importing model runtimes.
package modelstorage

import (
	"context"
	"sync"
)

// Use describes files needed by a stage or queued request. Only active stages
// and downloads reserve files; queued requests may download missing files later.
// Resources contain model identities and shared dependency groups, never paths.
type Use struct {
	Resources      []string `json:"-"`
	All            bool     `json:"-"`
	RecordingID    string   `json:"recording_id,omitempty"`
	RecordingTitle string   `json:"recording_title,omitempty"`
	Stage          string   `json:"stage,omitempty"`
	State          string   `json:"state"`
	CachedOnly     bool     `json:"cached_only,omitempty"`
}

// Gate orders removal, stage reservations and durable queue admission. A stage
// registers before touching files and releases only after its child has exited.
// Removal holds the barrier until filesystem mutations finish. Existing stages
// using other files keep running; newly starting stages wait for removal.
type Gate struct {
	barrier         sync.RWMutex
	mu              sync.Mutex
	next            uint64
	uses            map[uint64]Use
	revision        uint64
	filesChanged    map[string]uint64
	allFilesChanged uint64
	subscribers     map[chan struct{}]struct{}
}

var processGate = NewGate()

func ProcessGate() *Gate { return processGate }
func NewGate() *Gate {
	return &Gate{uses: make(map[uint64]Use), filesChanged: make(map[string]uint64), subscribers: make(map[chan struct{}]struct{})}
}

func (g *Gate) BeginUse(use Use) func() {
	g.barrier.RLock()
	g.mu.Lock()
	g.next++
	id := g.next
	use.Resources = append([]string(nil), use.Resources...)
	g.uses[id] = use
	g.changedLocked(Use{}, false)
	g.mu.Unlock()
	g.barrier.RUnlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			delete(g.uses, id)
			// A subprocess may download weights during preparation or inference,
			// including a partial download on failure. Reconcile only its resources.
			g.changedLocked(use, true)
			g.mu.Unlock()
		})
	}
}

// Admission must be acquired before queue ownership locks. The deletion path
// reads durable queue rows without taking queue ownership locks.
func (g *Gate) Admission() func() {
	g.barrier.RLock()
	return func() { g.barrier.RUnlock(); g.ActivityChanged() }
}

// ActivityChanged publishes an inexpensive invalidation; it does not scan files.
func (g *Gate) ActivityChanged() {
	g.mu.Lock()
	g.changedLocked(Use{}, false)
	g.mu.Unlock()
}

// FilesChanged is called at model preparation/removal boundaries. Consumers
// retain the affected resource identities until their next inventory request.
func (g *Gate) FilesChanged(use Use) {
	g.mu.Lock()
	g.changedLocked(use, true)
	g.mu.Unlock()
}

func (g *Gate) changedLocked(use Use, files bool) {
	g.revision++
	if files {
		if use.All {
			g.allFilesChanged = g.revision
		}
		for _, resource := range use.Resources {
			g.filesChanged[resource] = g.revision
		}
	}
	for subscriber := range g.subscribers {
		select {
		case subscriber <- struct{}{}:
		default:
		}
	}
}

func (g *Gate) fileChangesSince(revision uint64) (uint64, Use) {
	g.mu.Lock()
	defer g.mu.Unlock()
	use := Use{All: g.allFilesChanged > revision}
	for resource, changed := range g.filesChanged {
		if changed > revision {
			use.Resources = append(use.Resources, resource)
		}
	}
	return g.revision, use
}

// SubscribeChanges coalesces notifications without blocking runs. File changes
// are retained independently, so a slow/disconnected UI cannot lose inventory
// invalidations. No background filesystem watcher is involved.
func (g *Gate) SubscribeChanges() (<-chan struct{}, func()) {
	g.mu.Lock()
	ch := make(chan struct{}, 1)
	g.subscribers[ch] = struct{}{}
	g.mu.Unlock()
	var once sync.Once
	return ch, func() { once.Do(func() { g.mu.Lock(); delete(g.subscribers, ch); g.mu.Unlock() }) }
}

func (g *Gate) Uses() []Use {
	g.mu.Lock()
	defer g.mu.Unlock()
	result := make([]Use, 0, len(g.uses))
	for _, use := range g.uses {
		use.Resources = append([]string(nil), use.Resources...)
		result = append(result, use)
	}
	return result
}

func (g *Gate) removing(ctx context.Context, fn func() error) error {
	g.barrier.Lock()
	defer g.barrier.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	return fn()
}

func overlaps(keys []string, use Use) bool {
	if use.All {
		return true
	}
	for _, key := range keys {
		for _, resource := range use.Resources {
			if key == resource {
				return true
			}
		}
	}
	return false
}

func blocksRemoval(use Use) bool {
	// Unknown states fail closed. A verified waiting request does not touch files.
	return use.State != "queued"
}
