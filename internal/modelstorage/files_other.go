//go:build !linux && !darwin

package modelstorage

import "os"

func identity(path string, _ os.FileInfo) (string, uint64) { return path, 1 }
func volume(string) (string, Volume, error)                { return "", Volume{}, ErrReadOnly }
func lockDownload(string) (func(), error)                  { return nil, ErrReadOnly }
