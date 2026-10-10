package modelstorage

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound   = errors.New("model files are no longer cached")
	ErrBusy       = errors.New("model files are in use by an active run or download")
	ErrChanged    = errors.New("model storage changed; refresh before removing files")
	ErrReadOnly   = errors.New("these files cannot be safely removed by Jotist")
	ErrUnverified = errors.New("run activity or profile references could not be verified")
)

// Model describes identities owned by Jotist's registered runtimes. Unknown
// repositories in a shared Hugging Face cache are shown for auditing only.
type Model struct {
	Source    string
	Name      string
	Purpose   string
	Resources []string
}

type DirectFiles struct {
	Model Model
	Root  string
	Paths []string // Relative, model-owned paths only; never a runtime directory.
}

type Options struct {
	EnvironmentRoot    string
	HubRoots           []string
	PackageRoot        string
	OtherRuntimeRoots  []string
	OtherDownloadRoots []string
	AdditionalHubRoots func(context.Context) ([]string, error)
	AdditionalDirect   func(context.Context) ([]DirectFiles, error)
	Models             []Model
	Direct             []DirectFiles
	Gate               *Gate
	// Uses supplies current activity and immutable queued request dependencies.
	Uses    func(context.Context) ([]Use, error)
	Details func(context.Context) (map[string]Details, error)
}

type Details struct {
	LastUsed           *time.Time `json:"last_used,omitempty"`
	Profiles           int        `json:"profiles"`
	CachedOnlyProfiles int        `json:"cached_only_profiles"`
}

type Entry struct {
	ID                 string     `json:"id"`
	Name               string     `json:"name"`
	Source             string     `json:"source"`
	Purpose            string     `json:"purpose"`
	Bytes              int64      `json:"bytes"`
	RemovableBytes     int64      `json:"removable_bytes"`
	SharedBytes        int64      `json:"shared_bytes"`
	Revisions          int        `json:"revisions"`
	Locations          int        `json:"locations"`
	Status             string     `json:"status"`
	CanRemove          bool       `json:"can_remove"`
	Reason             string     `json:"reason,omitempty"`
	Version            string     `json:"version"`
	LastUsed           *time.Time `json:"last_used,omitempty"`
	Profiles           int        `json:"profiles"`
	CachedOnlyProfiles int        `json:"cached_only_profiles"`
	Uses               []Use      `json:"uses"`
	assets             []asset
	resources          []string
	files              map[string]fileSize
	signatures         []string
}

type Volume struct {
	TotalBytes     uint64 `json:"total_bytes"`
	AvailableBytes uint64 `json:"available_bytes"`
}

type Inventory struct {
	ScannedAt         time.Time `json:"scanned_at"`
	ModelBytes        int64     `json:"model_bytes"`
	RuntimeBytes      int64     `json:"runtime_bytes"`
	PackageCacheBytes int64     `json:"package_cache_bytes"`
	Volumes           []Volume  `json:"volumes"`
	Entries           []Entry   `json:"entries"`
	Warnings          []string  `json:"warnings"`
	volumeRoots       []string
}

type Removal struct {
	ID           string `json:"id"`
	RemovedBytes int64  `json:"removed_bytes"`
	Message      string `json:"message"`
}

type asset struct {
	root, path, kind string
	retained         bool
}
type fileSize struct {
	size  int64
	links uint64
	paths map[string]bool
}
