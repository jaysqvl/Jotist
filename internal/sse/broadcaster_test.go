package sse

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBroadcaster(t *testing.T) {
	b := NewBroadcaster()
	t.Cleanup(b.Shutdown)

	handlerDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(handlerDone)
		b.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	const jobID = "test-job-1"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/events?job_id="+jobID, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })
	if response.StatusCode != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", response.StatusCode)
	}
	for header, want := range map[string]string{
		"Content-Type":  "text/event-stream",
		"Cache-Control": "no-cache",
		"Connection":    "keep-alive",
	} {
		if got := response.Header.Get(header); got != want {
			t.Errorf("Expected %s %q, got %q", header, want, got)
		}
	}

	type streamEvent struct {
		Type    string            `json:"type"`
		JobID   string            `json:"job_id"`
		Payload map[string]string `json:"payload"`
	}
	reader := bufio.NewReader(response.Body)
	readEvent := func() streamEvent {
		t.Helper()
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("Read event: %v", err)
		}
		if !strings.HasPrefix(line, "data: ") {
			t.Fatalf("Expected SSE data field, got %q", line)
		}
		separator, err := reader.ReadString('\n')
		if err != nil || separator != "\n" {
			t.Fatalf("Expected complete SSE frame, got separator %q, error %v", separator, err)
		}
		var event streamEvent
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
			t.Fatalf("Decode event: %v", err)
		}
		return event
	}
	if connected := readEvent(); connected.Type != "connected" || connected.JobID != jobID {
		t.Fatalf("Expected connected event for %s, got %+v", jobID, connected)
	}

	// Delivery is nonblocking, so the initial flush does not guarantee that the
	// handler is already receiving. Publish until delivery or the request deadline.
	publishCtx, stopPublishing := context.WithCancel(ctx)
	publisherDone := make(chan struct{})
	go func() {
		defer close(publisherDone)
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			b.Broadcast(jobID, "status_update", map[string]string{"status": "completed"})
			select {
			case <-publishCtx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	t.Cleanup(func() {
		stopPublishing()
		<-publisherDone
	})

	if event := readEvent(); event.Type != "status_update" || event.Payload["status"] != "completed" {
		t.Errorf("Expected completed status update, got %+v", event)
	}
	stopPublishing()
	<-publisherDone
	cancel()
	response.Body.Close()
	select {
	case <-handlerDone:
	case <-time.After(5 * time.Second):
		t.Fatal("SSE handler did not exit after client disconnected")
	}
}
