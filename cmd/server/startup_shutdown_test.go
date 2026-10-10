package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if os.Getenv("JOTIST_TEST_CHILD_SERVER") == "1" {
		main()
		return
	}
	os.Exit(m.Run())
}

// Readiness can be observed before Docker's first healthcheck. A replacement
// at that point must drain workers and record a clean shutdown, rather than
// exiting through SIGTERM's default handler and leaving an unclean owner marker.
func TestGracefulShutdownAtFirstReadiness(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("container shutdown uses POSIX signals")
	}
	root := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable)
	cmd.Dir = root // Never load a developer's .env or application data.
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"),
		"JOTIST_TEST_CHILD_SERVER=1", "APP_ENV=production", "LOG_LEVEL=error",
		"HOST=127.0.0.1", "PORT=" + strconv.Itoa(port),
		"DATABASE_PATH=" + filepath.Join(root, "jotist.db"),
		"JWT_SECRET_FILE=" + filepath.Join(root, "jwt-secret"),
		"UPLOAD_DIR=" + filepath.Join(root, "uploads"),
		"TRANSCRIPTS_DIR=" + filepath.Join(root, "transcripts"),
		"TEMP_DIR=" + filepath.Join(root, "temp"),
		"WHISPERX_ENV=" + filepath.Join(root, "runtime"),
	}
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var waitErr error
	go func() {
		waitErr = cmd.Wait()
		close(done)
	}()
	defer func() {
		_ = cmd.Process.Kill()
		<-done
	}()
	client := &http.Client{Timeout: 200 * time.Millisecond}
	deadline := time.Now().Add(20 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		select {
		case <-done:
			t.Fatalf("server exited before readiness: %v", waitErr)
		default:
		}
		response, err := client.Get("http://" + address + "/ready")
		if err == nil {
			var data struct{ Status string }
			decodeErr := json.NewDecoder(response.Body).Decode(&data)
			_ = response.Body.Close()
			ready = response.StatusCode == http.StatusOK && decodeErr == nil && data.Status == "ready"
			if ready {
				break
			}
		}
		time.Sleep(time.Millisecond)
	}
	if !ready {
		t.Fatal("server did not become ready")
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
		if waitErr != nil {
			t.Fatalf("ready server did not shut down gracefully: %v", waitErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ready server did not finish shutdown")
	}
	data, err := os.ReadFile(filepath.Join(root, "jotist.db.server.lock"))
	var state struct{ Clean bool }
	if err != nil || json.Unmarshal(data, &state) != nil || !state.Clean {
		t.Fatal("graceful shutdown did not retain clean worker ownership")
	}
}
