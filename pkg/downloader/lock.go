package downloader

import (
	"context"
	"path/filepath"
	"sync"
)

var destinationLocks sync.Map

// WithDestination coordinates normal adapter setup and manual weight downloads
// in this server. The callback must finish its child before releasing the lock.
func WithDestination(ctx context.Context, path string, fn func() error) error {
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	value, _ := destinationLocks.LoadOrStore(path, make(chan struct{}, 1))
	lock := value.(chan struct{})
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case lock <- struct{}{}:
		defer func() { <-lock }()
		return fn()
	case <-ctx.Done():
		return ctx.Err()
	}
}
