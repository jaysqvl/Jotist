package adapters

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"
)

func TestWhisperPreparationChecksRealModulesAfterSync(t *testing.T) {
	for _, failImport := range []bool{false, true} {
		t.Run(map[bool]string{false: "valid_runtime", true: "broken_alignment_import"}[failImport], func(t *testing.T) {
			directory := t.TempDir()
			bin := filepath.Join(directory, "bin")
			if err := os.MkdirAll(bin, 0755); err != nil {
				t.Fatal(err)
			}
			stub := `#!/bin/sh
if [ "$1" = "sync" ]; then
  touch "$CHECK_ROOT/synced"
  exit 0
fi
test -f "$CHECK_ROOT/synced" || exit 21
case "$*" in
  *"python -I -c"*"whisperx.alignment"*"whisperx.asr"*"whisperx.transcribe"*) ;;
  *) exit 22 ;;
esac
touch "$CHECK_ROOT/imported"
if [ "$FAIL_IMPORT" = "yes" ]; then
  echo 'RuntimeError: operator torchvision::nms does not exist' >&2
  exit 23
fi
`
			if err := os.WriteFile(filepath.Join(bin, "uv"), []byte(stub), 0755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("CHECK_ROOT", directory)
			t.Setenv("FAIL_IMPORT", map[bool]string{false: "no", true: "yes"}[failImport])
			adapter := NewWhisperXAdapter(filepath.Join(directory, "environment"))
			err := adapter.PrepareEnvironment(context.Background())
			if failImport {
				if err == nil || !strings.Contains(err.Error(), "torchvision::nms") || adapter.initialized {
					t.Fatalf("broken alignment import must fail preparation: %v, initialized=%v", err, adapter.initialized)
				}
			} else if err != nil || !adapter.initialized {
				t.Fatalf("valid runtime was not prepared: %v", err)
			}
			if _, err := os.Stat(filepath.Join(directory, "imported")); err != nil {
				t.Fatal("preparation never checked the actual alignment and transcription modules")
			}
		})
	}
}

func TestWhisperPreparationReconcilesOldPythonPinBeforeSync(t *testing.T) {
	directory := t.TempDir()
	environment := filepath.Join(directory, "WhisperX")
	bin := filepath.Join(directory, "bin")
	for _, path := range []string{environment, bin} {
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	// Represent the real failed-upgrade state: current project was installed,
	// but the old .python-version caused uv to reject the first sync.
	if err := refreshPythonProject(whisperxScripts, "py/whisperx/pyproject.toml", environment); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(environment, ".python-version"), []byte("3.10.20\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(environment, "uv.lock"), []byte("old interpreter dependency graph"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(environment, "cached-model.bin"), []byte("retained model cache"), 0600); err != nil {
		t.Fatal(err)
	}
	stub := `#!/bin/sh
test "$(cat "$CHECK_ROOT/WhisperX/.python-version")" = "3.12" || exit 31
test ! -e "$CHECK_ROOT/WhisperX/uv.lock" || exit 34
if [ "$1" = "sync" ]; then
  touch "$CHECK_ROOT/synced"
  exit 0
fi
test -f "$CHECK_ROOT/synced" || exit 32
case "$*" in
  *"--no-sync"*"python -I -c"*"whisperx.alignment"*) exit 0 ;;
  *) exit 33 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "uv"), []byte(stub), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CHECK_ROOT", directory)
	key := environment + ":old import check"
	envCacheMutex.Lock()
	envCache[key] = true
	envCacheMutex.Unlock()
	t.Cleanup(func() { envCacheMutex.Lock(); delete(envCache, key); envCacheMutex.Unlock() })
	adapter := NewWhisperXAdapter(directory)
	if err := adapter.PrepareEnvironment(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !adapter.initialized {
		t.Fatal("upgraded runtime was not marked initialized")
	}
	envCacheMutex.RLock()
	stale := envCache[key]
	envCacheMutex.RUnlock()
	if stale {
		t.Fatal("interpreter migration kept a cached import check")
	}
	data, err := os.ReadFile(filepath.Join(environment, "cached-model.bin"))
	if err != nil || string(data) != "retained model cache" {
		t.Fatal("interpreter migration disturbed cached model files")
	}
}

func TestPythonPinMigrationPreservesSupportedAndCustomInterpreters(t *testing.T) {
	for _, tc := range []struct{ name, requirement, pin, want string }{
		{"old_3_10", ">=3.11,<3.13", "3.10.20\n", "3.12\n"},
		{"diarizen_old_3_11", ">=3.12,<3.13", "3.11.16\n", "3.12\n"},
		{"diarizen_supported_3_12", ">=3.12,<3.13", "3.12.14\n", "3.12.14\n"},
		{"newer_unsupported", ">=3.11,<3.13", "3.13.2\n", "3.12\n"},
		{"supported_3_11", ">=3.11,<3.13", "3.11.9\n", "3.11.9\n"},
		{"supported_3_12", ">=3.11,<3.13", "3.12.11\n", "3.12.11\n"},
		{"pyannote_3_10", ">=3.10,<3.13", "3.10.20\n", "3.10.20\n"},
		{"custom_interpreter", ">=3.11,<3.13", "/custom/python3.12\n", "/custom/python3.12\n"},
		{"custom_implementation", ">=3.11,<3.13", "pypy@3.11\n", "pypy@3.11\n"},
		{"custom_requirement", ">=3.10,<3.11", "3.10.20\n", "3.10.20\n"},
		{"missing_pin", ">=3.11,<3.13", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			environment := t.TempDir()
			path := filepath.Join(environment, ".python-version")
			if tc.pin != "" {
				if err := os.WriteFile(path, []byte(tc.pin), 0644); err != nil {
					t.Fatal(err)
				}
			}
			files := fstest.MapFS{"pyproject.toml": {Data: []byte("[project]\nname = 'fixture'\nrequires-python = '" + tc.requirement + "'\n")}}
			if err := refreshPythonProject(files, "pyproject.toml", environment); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if tc.want == "" && os.IsNotExist(err) {
				return
			}
			if err != nil || string(data) != tc.want {
				t.Fatalf("Python pin = %q (%v), want %q", data, err, tc.want)
			}
		})
	}
}

func TestSharedNvidiaPreparationDoesNotOverlapUV(t *testing.T) {
	directory := t.TempDir()
	environment := filepath.Join(directory, "nvidia")
	bin := filepath.Join(directory, "bin")
	if err := os.MkdirAll(environment, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(bin, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"parakeet-tdt-0.6b-v3.nemo", "canary-1b-v2.nemo", "diar_streaming_sortformer_4spk-v2.1.nemo"} {
		path := filepath.Join(environment, name)
		if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Truncate(path, 2*1024*1024); err != nil {
			t.Fatal(err)
		}
	}
	stub := `#!/bin/sh
if ! mkdir "$CHECK_ROOT/uv-active" 2>/dev/null; then
  touch "$CHECK_ROOT/overlap"
  exit 21
fi
trap 'rmdir "$CHECK_ROOT/uv-active"' EXIT
test -s "$CHECK_ROOT/nvidia/pyproject.toml" || exit 22
sleep 0.04
exit 0
`
	if err := os.WriteFile(filepath.Join(bin, "uv"), []byte(stub), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CHECK_ROOT", directory)
	canary := NewCanaryAdapter(environment)
	// This fixture tests preparation serialization, not archive integrity (which
	// has a separate rejection test). Represent an already verified cached file.
	info, err := os.Stat(filepath.Join(environment, "canary-1b-v2.nemo"))
	if err != nil {
		t.Fatal(err)
	}
	canary.verifiedModelSize, canary.verifiedModelTime = info.Size(), info.ModTime()
	adapters := []interface{ PrepareEnvironment(context.Context) error }{NewParakeetAdapter(environment), canary, NewSortformerAdapter(environment)}
	start := make(chan struct{})
	errors := make(chan error, len(adapters))
	var group sync.WaitGroup
	for _, adapter := range adapters {
		group.Add(1)
		go func(adapter interface{ PrepareEnvironment(context.Context) error }) {
			defer group.Done()
			<-start
			errors <- adapter.PrepareEnvironment(context.Background())
		}(adapter)
	}
	close(start)
	group.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(directory, "overlap")); !os.IsNotExist(err) {
		t.Fatal("shared NVIDIA environment ran overlapping preparation commands")
	}
}

func TestPythonPreparationLockCanCancelWaitingCaller(t *testing.T) {
	directory := t.TempDir()
	release, err := lockPythonPreparation(context.Background(), directory)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		nextRelease, err := lockPythonPreparation(ctx, filepath.Join(directory, "."))
		if nextRelease != nil {
			nextRelease()
		}
		result <- err
	}()
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("waiting preparation returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled preparation remained blocked behind another adapter")
	}
	otherRelease, err := lockPythonPreparation(context.Background(), filepath.Join(directory, "other"))
	if err != nil {
		t.Fatal(err)
	}
	otherRelease()
}

func TestPythonProjectReplacementNeverExposesPartialFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pyproject.toml")
	first := []byte("[project]\nname = 'first'\n#" + strings.Repeat("a", 32768))
	second := []byte("[project]\nname = 'second'\n#" + strings.Repeat("b", 65536))
	if err := writePythonProject(path, first); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	readerResult := make(chan error, 1)
	go func() {
		for {
			data, err := os.ReadFile(path)
			if err != nil {
				readerResult <- err
				return
			}
			if !bytes.Equal(data, first) && !bytes.Equal(data, second) {
				readerResult <- errors.New("reader saw partial pyproject contents")
				return
			}
			select {
			case <-stop:
				readerResult <- nil
				return
			default:
			}
		}
	}()
	for index := 0; index < 64; index++ {
		data := first
		if index%2 == 0 {
			data = second
		}
		if err := writePythonProject(path, data); err != nil {
			close(stop)
			<-readerResult
			t.Fatal(err)
		}
	}
	close(stop)
	if err := <-readerResult; err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeUpgradeRefreshesRetainedLockWithoutTouchingModels(t *testing.T) {
	environment := t.TempDir()
	project := []byte("[project]\nname = 'fixture'\nversion = '1'\nrequires-python = '>=3.11,<3.13'\n")
	files := fstest.MapFS{"pyproject.toml": {Data: project}}
	lockPath := filepath.Join(environment, "uv.lock")
	modelPath := filepath.Join(environment, "cached-model.bin")
	// Reproduce RC2's partially updated state: new TOML, old allowed lock,
	// and a cached successful import do not imply a current dependency graph.
	for name, contents := range map[string][]byte{
		"pyproject.toml": project, "uv.lock": []byte("old allowed dependency versions"),
		"cached-model.bin": []byte("preserve model weights"),
	} {
		if err := os.WriteFile(filepath.Join(environment, name), contents, 0600); err != nil {
			t.Fatal(err)
		}
	}
	key := environment + ":old import"
	envCacheMutex.Lock()
	envCache[key] = true
	envCacheMutex.Unlock()
	t.Cleanup(func() { envCacheMutex.Lock(); delete(envCache, key); envCacheMutex.Unlock() })
	if err := refreshPythonProject(files, "pyproject.toml", environment); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("legacy lock survived migration: %v", err)
	}
	envCacheMutex.RLock()
	stale := envCache[key]
	envCacheMutex.RUnlock()
	if stale {
		t.Fatal("legacy import readiness survived dependency migration")
	}
	// Once uv has resolved the migrated project, normal repeated preparation
	// retains its lock rather than upgrading dependencies on every invocation.
	if err := os.WriteFile(lockPath, []byte("new resolved dependency graph"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := refreshPythonProject(files, "pyproject.toml", environment); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(lockPath); err != nil || string(data) != "new resolved dependency graph" {
		t.Fatal("unchanged runtime did not retain its resolved lock")
	}
	files["pyproject.toml"].Data = append(project, []byte("dependencies = ['fixed-package==2']\n")...)
	if err := refreshPythonProject(files, "pyproject.toml", environment); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatal("changed project did not re-resolve its dependency graph")
	}
	if data, err := os.ReadFile(modelPath); err != nil || string(data) != "preserve model weights" {
		t.Fatal("dependency migration changed cached model data")
	}
}

func TestRuntimeUpgradeRejectsUnsafeLockAndRetries(t *testing.T) {
	environment := t.TempDir()
	files := fstest.MapFS{"pyproject.toml": {Data: []byte("[project]\nname = 'fixture'\nrequires-python = '>=3.11,<3.13'\n")}}
	lockPath := filepath.Join(environment, "uv.lock")
	if err := os.Mkdir(lockPath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := refreshPythonProject(files, "pyproject.toml", environment); err == nil {
		t.Fatal("non-regular dependency lock must fail closed")
	}
	if err := os.Remove(lockPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, []byte("retained old graph"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := refreshPythonProject(files, "pyproject.toml", environment); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatal("retry after interrupted migration retained stale lock")
	}
}

func TestReadinessRequiresExactEnvironmentSync(t *testing.T) {
	root := t.TempDir()
	stub := `#!/bin/sh
test "$1" = run && test "$2" = --exact || exit 41
rm "$CHECK_ROOT/orphan-package"
`
	if err := os.WriteFile(filepath.Join(root, "uv"), []byte(stub), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "orphan-package"), []byte("obsolete distribution"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CHECK_ROOT", root)
	t.Cleanup(func() {
		envCacheMutex.Lock()
		delete(envCache, root+":import current_runtime")
		envCacheMutex.Unlock()
	})
	if !checkPythonEnvironmentReady(context.Background(), root, "import current_runtime") {
		t.Fatal("readiness did not request removal of obsolete distributions")
	}
	if _, err := os.Stat(filepath.Join(root, "orphan-package")); !os.IsNotExist(err) {
		t.Fatal("readiness accepted an environment with an orphan package")
	}
}
