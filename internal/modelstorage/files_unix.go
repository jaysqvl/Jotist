//go:build linux || darwin

package modelstorage

import (
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func identity(path string, info os.FileInfo) (string, uint64) {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return fmt.Sprintf("%d:%d", stat.Dev, stat.Ino), uint64(stat.Nlink)
	}
	return path, 1
}

func volume(path string) (string, Volume, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return "", Volume{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", Volume{}, err
	}
	device := path
	if fileStat, ok := info.Sys().(*syscall.Stat_t); ok {
		device = fmt.Sprint(fileStat.Dev)
	}
	return device, Volume{TotalBytes: stat.Blocks * uint64(stat.Bsize), AvailableBytes: stat.Bavail * uint64(stat.Bsize)}, nil
}

// FileLock uses flock on Unix too. Keep existing HF locks held throughout
// removal; never delete lock files or confuse a retained lock file with a lock.
func lockDownload(path string) (func(), error) {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, ErrBusy
	}
	return func() { _ = unix.Flock(int(file.Fd()), unix.LOCK_UN); _ = file.Close() }, nil
}
