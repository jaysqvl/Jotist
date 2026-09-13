package adapters

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/pelletier/go-toml/v2"

	"scriberr/internal/processutil"
)

var pythonPreparationLocks sync.Map

// lockPythonPreparation serializes the entire preparation of shared runtime
// directories. uv locks its own writes, but cannot protect our script/project
// refreshes from another adapter preparing the same environment.
func lockPythonPreparation(ctx context.Context, envPath string) (func(), error) {
	key, err := filepath.Abs(envPath)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	value, _ := pythonPreparationLocks.LoadOrStore(key, make(chan struct{}, 1))
	semaphore := value.(chan struct{})
	select {
	case semaphore <- struct{}{}:
		var once sync.Once
		return func() { once.Do(func() { <-semaphore }) }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// checkPythonEnvironmentReady is called under the preparation lock and keeps
// package synchronization/import probes cancellable with their parent task.
func checkPythonEnvironmentReady(ctx context.Context, envPath, importStatement string) bool {
	if ctx.Err() != nil {
		return false
	}
	key := fmt.Sprintf("%s:%s", envPath, importStatement)
	envCacheMutex.RLock()
	ready := envCache[key]
	envCacheMutex.RUnlock()
	if ready {
		return true
	}
	cmd := processutil.CommandContext(ctx, "uv", "run", "--system-certs", "--project", envPath, "python", "-c", importStatement)
	if err := cmd.Run(); err != nil {
		return false
	}
	envCacheMutex.Lock()
	envCache[key] = true
	envCacheMutex.Unlock()
	return true
}

// writePythonProject atomically replaces a complete project specification, so
// readers outside the preparation lock never observe a truncated TOML file.
func writePythonProject(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".pyproject-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(0644); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

// refreshPythonProject makes installed environments use the embedded dependency
// versions on their next uv invocation, including after an application upgrade.
func refreshPythonProject(files fs.FS, embeddedPath, envPath string) error {
	data, err := fs.ReadFile(files, embeddedPath)
	if err != nil {
		return err
	}
	data = []byte(strings.Replace(string(data), "https://download.pytorch.org/whl/cu126", GetPyTorchWheelURL(), 1))
	if err := os.MkdirAll(envPath, 0755); err != nil {
		return err
	}
	path := filepath.Join(envPath, "pyproject.toml")
	previous, err := os.ReadFile(path)
	changed := false
	defer func() {
		if changed {
			// A cached import check is stale after either dependency or
			// interpreter changes, including partially completed upgrades.
			envCacheMutex.Lock()
			for key := range envCache {
				if strings.HasPrefix(key, envPath+":") {
					delete(envCache, key)
				}
			}
			envCacheMutex.Unlock()
		}
	}()
	if err != nil || string(previous) != string(data) {
		if err := writePythonProject(path, data); err != nil {
			return err
		}
		changed = true
	}
	// Old uv-created runtimes can retain a Python 3.10 pin even after their
	// project moves to >=3.11. Reconcile this on every refresh: a prior startup
	// may already have written the new TOML before failing at uv sync.
	pinChanged, err := reconcilePythonVersion(envPath, data)
	changed = changed || pinChanged
	return err
}

func reconcilePythonVersion(envPath string, project []byte) (bool, error) {
	path := filepath.Join(envPath, ".python-version")
	previous, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil // Let uv choose an installed compatible interpreter.
	}
	if err != nil {
		return false, err
	}
	var spec struct {
		Project struct {
			RequiresPython string `toml:"requires-python"`
		} `toml:"project"`
	}
	if err := toml.Unmarshal(project, &spec); err != nil {
		return false, fmt.Errorf("read embedded Python requirement: %w", err)
	}
	// These are the ranges used by our built-in projects. Do not guess at
	// custom PEP 440 constraints or overwrite custom interpreter/path requests.
	minimum := 0
	switch strings.ReplaceAll(spec.Project.RequiresPython, " ", "") {
	case ">=3.11,<3.13":
		minimum = 11
	case ">=3.10,<3.13":
		minimum = 10
	default:
		return false, nil
	}
	parts := strings.Split(strings.TrimSpace(string(previous)), ".")
	if len(parts) < 2 || len(parts) > 3 {
		return false, nil
	}
	version := make([]int, len(parts))
	for index, part := range parts {
		if part == "" || strings.Trim(part, "0123456789") != "" {
			return false, nil
		}
		value, err := strconv.Atoi(part)
		if err != nil {
			return false, nil
		}
		version[index] = value
	}
	if version[0] == 3 && version[1] >= minimum && version[1] < 13 {
		return false, nil
	}
	// uv performs the actual environment synchronization; do not delete the
	// existing virtualenv, cached weights, or any user files during migration.
	if err := writePythonProject(path, []byte("3.12\n")); err != nil {
		return false, fmt.Errorf("update incompatible Python pin: %w", err)
	}
	return true, nil
}
