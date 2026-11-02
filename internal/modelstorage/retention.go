package modelstorage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

const DefaultRetentionDays = 14
const MaxRetentionDays = 365
const CleanupInterval = 24 * time.Hour

var ErrNotExpired = errors.New("model is retained by the cache policy")

type CacheRetentionPolicy struct {
	Enabled           bool       `json:"enabled"`
	RetentionDays     int        `json:"retention_days"`
	LastCheckedAt     *time.Time `json:"last_checked_at,omitempty"`
	NextCheckAt       *time.Time `json:"next_check_at,omitempty"`
	LastRemovedModels int        `json:"last_removed_models"`
	LastRemovedBytes  int64      `json:"last_removed_bytes"`
	LastSkippedModels int        `json:"last_skipped_models"`
	LastFailedModels  int        `json:"last_failed_models"`
}

func ValidateRetentionDays(days int) error {
	if days < 1 || days > MaxRetentionDays {
		return fmt.Errorf("retention must be between 1 and %d days", MaxRetentionDays)
	}
	return nil
}

type CacheRecord struct {
	Source       string
	CacheVersion string
	CachedSince  time.Time
	LastUsed     *time.Time
}

type RetentionStore interface {
	LoadPolicy(context.Context) (CacheRetentionPolicy, error)
	SavePolicy(context.Context, bool, int) error
	Observe(context.Context, []Entry, time.Time) (map[string]CacheRecord, error)
	ReadRecord(context.Context, string) (CacheRecord, error)
	SaveCleanup(context.Context, CleanupResult) error
}

type CleanupTarget struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Bytes   int64  `json:"bytes"`
}

type CleanupPlan struct {
	Mode               string          `json:"mode"`
	Version            string          `json:"version"`
	Models             []CleanupTarget `json:"models"`
	RemovableBytes     int64           `json:"removable_bytes"`
	SkippedModels      int             `json:"skipped_models"`
	CachedOnlyProfiles bool            `json:"cached_only_profiles"`
}

type CleanupResult struct {
	CheckedAt     time.Time `json:"checked_at"`
	RemovedModels int       `json:"removed_models"`
	RemovedBytes  int64     `json:"removed_bytes"`
	SkippedModels int       `json:"skipped_models"`
	FailedModels  int       `json:"failed_models"`
	Message       string    `json:"message"`
}

type Retainer struct {
	manager *Manager
	store   RetentionStore
	now     func() time.Time
	mu      sync.Mutex // One cleanup batch; ordinary runs still use the file gate.
	wake    chan struct{}
}

func NewRetainer(manager *Manager, store RetentionStore) *Retainer {
	return &Retainer{manager: manager, store: store, now: time.Now, wake: make(chan struct{}, 1)}
}

func (r *Retainer) Policy(ctx context.Context) (CacheRetentionPolicy, error) {
	p, err := r.store.LoadPolicy(ctx)
	if err != nil {
		return p, err
	}
	if err := ValidateRetentionDays(p.RetentionDays); err != nil {
		return p, err
	}
	if p.Enabled {
		next := r.now().UTC()
		if p.LastCheckedAt != nil {
			next = p.LastCheckedAt.Add(CleanupInterval)
		}
		p.NextCheckAt = &next
	}
	return p, nil
}

func (r *Retainer) SavePolicy(ctx context.Context, enabled bool, days int) (CacheRetentionPolicy, error) {
	if err := ValidateRetentionDays(days); err != nil {
		return CacheRetentionPolicy{}, err
	}
	if err := r.store.SavePolicy(ctx, enabled, days); err != nil {
		return CacheRetentionPolicy{}, err
	}
	select {
	case r.wake <- struct{}{}:
	default:
	}
	r.manager.gate.ActivityChanged()
	return r.Policy(ctx)
}

// Inventory only writes small metadata records for newly observed/changed
// caches or newer run timestamps. It never reads model weight contents.
func (r *Retainer) Inventory(ctx context.Context, refresh bool) (Inventory, error) {
	inv, err := r.manager.Inventory(ctx, refresh)
	if err != nil {
		return inv, err
	}
	p, err := r.Policy(ctx)
	if err != nil {
		return inv, err
	}
	records, err := r.store.Observe(ctx, inv.Entries, r.now().UTC())
	if err != nil {
		return inv, err
	}
	for i := range inv.Entries {
		e := &inv.Entries[i]
		record, ok := records[e.ID]
		if !ok || record.Source != e.Source {
			return inv, ErrUnverified
		}
		e.CachedSince = &record.CachedSince
		e.LastUsed = later(e.LastUsed, record.LastUsed)
		if !e.CanRemove {
			e.RetentionReason = e.Reason
		} else if e.CachedOnlyProfiles > 0 || cachedOnlyQueued(e.Uses) {
			e.RetentionReason = "Kept for a cached-only profile or queued run."
		} else if p.Enabled {
			deadline := cacheDeadline(record, *e, p.RetentionDays)
			e.EvictAfter = &deadline
		}
	}
	inv.Retention = &p
	return inv, nil
}

func later(a, b *time.Time) *time.Time {
	if a == nil || (b != nil && b.After(*a)) {
		return b
	}
	return a
}

func cacheDeadline(record CacheRecord, entry Entry, days int) time.Time {
	stamp := record.CachedSince
	if used := later(entry.LastUsed, record.LastUsed); used != nil && used.After(stamp) {
		stamp = *used
	}
	return stamp.Add(time.Duration(days) * 24 * time.Hour)
}

func cachedOnlyQueued(uses []Use) bool {
	for _, use := range uses {
		if use.State == "queued" && use.CachedOnly {
			return true
		}
	}
	return false
}

func (r *Retainer) Preview(ctx context.Context, mode string, refresh bool) (CleanupPlan, error) {
	plan := CleanupPlan{Mode: mode, Models: []CleanupTarget{}}
	if mode != "all" && mode != "expired" {
		return plan, errors.New("invalid cleanup mode")
	}
	inv, err := r.Inventory(ctx, refresh)
	if err != nil {
		return plan, err
	}
	for _, e := range inv.Entries {
		eligible := e.CanRemove && !cachedOnlyQueued(e.Uses)
		if mode == "expired" {
			eligible = eligible && inv.Retention.Enabled && e.CachedOnlyProfiles == 0 &&
				((e.EvictAfter != nil && !e.EvictAfter.After(r.now())) || e.Status == "removal_pending")
		}
		if !eligible {
			plan.SkippedModels++
			continue
		}
		plan.Models = append(plan.Models, CleanupTarget{ID: e.ID, Name: e.Name, Version: e.Version, Bytes: e.RemovableBytes})
		plan.RemovableBytes += e.RemovableBytes
		plan.CachedOnlyProfiles = plan.CachedOnlyProfiles || e.CachedOnlyProfiles > 0
	}
	data, err := json.Marshal(struct {
		Mode               string
		Days               int
		Models             []CleanupTarget
		CachedOnlyProfiles bool
	}{mode, inv.Retention.RetentionDays, plan.Models, plan.CachedOnlyProfiles})
	if err != nil {
		return plan, err
	}
	sum := sha256.Sum256(data)
	plan.Version = hex.EncodeToString(sum[:])
	return plan, nil
}

// Collect rechecks each target inside the removal barrier. A failed/uncertain
// response must be reconciled by inventory, never treated as removal success.
func (r *Retainer) Collect(ctx context.Context, mode, confirmedVersion string) (CleanupResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := CleanupResult{CheckedAt: r.now().UTC()}
	plan, err := r.Preview(ctx, mode, false)
	if err != nil {
		return result, err
	}
	if (mode == "all" && confirmedVersion == "") || (confirmedVersion != "" && confirmedVersion != plan.Version) {
		return result, ErrChanged
	}
	result.SkippedModels = plan.SkippedModels
	for _, target := range plan.Models {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		removed, err := r.manager.remove(ctx, target.ID, target.Version, func(entry Entry) error {
			if cachedOnlyQueued(entry.Uses) {
				return ErrBusy
			}
			if mode == "all" {
				return nil
			}
			policy, err := r.Policy(ctx)
			if err != nil {
				return ErrUnverified
			}
			if !policy.Enabled || entry.CachedOnlyProfiles > 0 {
				return ErrNotExpired
			}
			record, err := r.store.ReadRecord(ctx, entry.ID)
			if err != nil || record.Source != entry.Source || record.CacheVersion != entry.Version {
				return ErrUnverified
			}
			if entry.Status != "removal_pending" && cacheDeadline(record, entry, policy.RetentionDays).After(r.now()) {
				return ErrNotExpired
			}
			return nil
		})
		switch {
		case err == nil:
			result.RemovedModels++
			result.RemovedBytes += removed.RemovedBytes
		case errors.Is(err, ErrBusy), errors.Is(err, ErrChanged), errors.Is(err, ErrNotFound), errors.Is(err, ErrNotExpired):
			result.SkippedModels++
		default:
			result.FailedModels++
		}
	}
	result.Message = fmt.Sprintf("Removed %d cached models. Profiles and completed runs are unchanged.", result.RemovedModels)
	if result.SkippedModels > 0 {
		result.Message += fmt.Sprintf(" %d models were kept.", result.SkippedModels)
	}
	if result.FailedModels > 0 {
		result.Message += fmt.Sprintf(" %d removals could not finish. Refresh to review remaining files.", result.FailedModels)
	}
	if err := r.store.SaveCleanup(ctx, result); err != nil {
		return result, err
	}
	r.manager.gate.ActivityChanged()
	return result, nil
}

// Run responds to actual model activity and one daily timer; there is no
// periodic filesystem polling. Inventory keeps its existing 72-hour full check.
func (r *Retainer) Run(ctx context.Context, report func(CleanupResult, error)) {
	changes, unsubscribe := r.manager.SubscribeChanges()
	defer unsubscribe()
	timer := time.NewTimer(0)
	defer timer.Stop()
	force := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-changes:
			_, _ = r.Inventory(ctx, false)
		case <-r.wake:
			force = true
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(0)
		case <-timer.C:
			policy, err := r.Policy(ctx)
			if err == nil && policy.Enabled && !force && policy.NextCheckAt != nil && policy.NextCheckAt.After(r.now()) {
				_, _ = r.Inventory(ctx, false)
				timer.Reset(policy.NextCheckAt.Sub(r.now()))
				continue
			}
			force = false
			if err == nil && policy.Enabled {
				var result CleanupResult
				result, err = r.Collect(ctx, "expired", "")
				if report != nil {
					report(result, err)
				}
			} else {
				_, observeErr := r.Inventory(ctx, false)
				if err == nil {
					err = observeErr
				}
				if err != nil && report != nil {
					report(CleanupResult{}, err)
				}
			}
			timer.Reset(CleanupInterval)
		}
	}
}
