// Package serverlock enforces the single-coordinator deployment supported by
// the in-process GPU scheduler. It does not assert that orphan workers exited.
package serverlock

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
)

var ErrAlreadyRunning = errors.New("another Scriberr server owns this database")
var priorSafety atomic.Int32    // 0 unmanaged (tests/embedders), 1 proven, 2 unknown
func PriorWorkersStopped() bool { return priorSafety.Load() != 2 }

type Lease struct {
	file                  *os.File
	PreviousShutdownClean bool
	workersStoppedProof   bool
}

func Acquire(databasePath string) (*Lease, error) {
	path, err := filepath.Abs(databasePath)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	if resolved, resolveErr := filepath.EvalSymlinks(path); resolveErr == nil {
		path = resolved
	} else if parent, parentErr := filepath.EvalSymlinks(filepath.Dir(path)); parentErr == nil {
		path = filepath.Join(parent, filepath.Base(path))
	}
	file, err := os.OpenFile(path+".server.lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := lockFile(file); err != nil {
		file.Close()
		return nil, err
	}
	var previous struct {
		Clean    bool   `json:"clean"`
		Boundary string `json:"boundary"`
	}
	data, err := os.ReadFile(path + ".server.lock")
	if err != nil {
		file.Close()
		return nil, err
	}
	clean := len(data) == 0
	if json.Unmarshal(data, &previous) == nil {
		clean = previous.Clean
	}
	boundary := processBoundary()
	if clean || (boundary != "" && previous.Boundary != "" && boundary != previous.Boundary) {
		priorSafety.Store(1)
	} else {
		priorSafety.Store(2)
	}
	lease := &Lease{file: file, PreviousShutdownClean: clean, workersStoppedProof: PriorWorkersStopped()}
	if err := lease.writeState(false); err != nil {
		file.Close()
		return nil, err
	}
	return lease, nil
}

func (l *Lease) writeState(clean bool) error {
	data, err := json.Marshal(struct {
		Clean    bool   `json:"clean"`
		PID      int    `json:"pid"`
		Boundary string `json:"boundary"`
	}{clean, os.Getpid(), processBoundary()})
	if err != nil {
		return err
	}
	if _, err := l.file.WriteAt(data, 0); err != nil {
		return err
	}
	// Never truncate a prior marker before its replacement is written: a
	// crash in that window would make the next owner mistake it for first use.
	if err := l.file.Truncate(int64(len(data))); err != nil {
		return err
	}
	return l.file.Sync()
}

// MarkWorkersStopped requires the caller to have drained every owned worker.
// An earlier unproven orphan cannot be cleared by opening and closing the UI.
func (l *Lease) MarkWorkersStopped() error {
	if l == nil || l.file == nil || !l.workersStoppedProof {
		return nil
	}
	return l.writeState(true)
}

func (l *Lease) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	closeErr := l.file.Close()
	l.file = nil
	return closeErr
}
