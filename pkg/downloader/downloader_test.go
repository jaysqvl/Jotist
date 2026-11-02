package downloader

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWaitingAdapterReusesManualDownloadInsteadOfOverwriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "weights.nemo")
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte("unnecessary network download"))
	}))
	defer server.Close()
	manual := make(chan struct{})
	finish := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- WithDestination(context.Background(), path, func() error {
			close(manual)
			<-finish
			return os.WriteFile(path, []byte("complete verified manual weights"), 0644)
		})
	}()
	<-manual
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := DownloadFileIfMissing(context.Background(), server.URL, path, 4); err != nil {
			t.Error(err)
		}
	}()
	close(finish)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if requests.Load() != 0 {
		t.Fatalf("overwrote manual download with %d requests", requests.Load())
	}
	data, _ := os.ReadFile(path)
	if string(data) != "complete verified manual weights" {
		t.Fatal(string(data))
	}
}

func TestDestinationWaitIsCancelableAndDoesNotUnlockAnotherDownload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "weights")
	start := make(chan struct{})
	finish := make(chan struct{})
	go func() {
		_ = WithDestination(context.Background(), path, func() error { close(start); <-finish; return nil })
	}()
	<-start
	defer close(finish)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := WithDestination(ctx, path, func() error { t.Error("canceled waiter entered critical section"); return nil }); err != context.DeadlineExceeded {
		t.Fatal(err)
	}
}
