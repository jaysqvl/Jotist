package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jaysqvl/Jotist/internal/database"
	"github.com/jaysqvl/Jotist/internal/modeldownloads"
	"github.com/jaysqvl/Jotist/internal/models"
	"github.com/jaysqvl/Jotist/internal/modelstorage"
	"github.com/jaysqvl/Jotist/internal/repository"
)

type testModelFetcher struct {
	model   modeldownloads.Model
	start   chan struct{}
	exit    chan struct{}
	release chan struct{}
	err     error
}

func (f *testModelFetcher) Catalog() []modeldownloads.Model { return []modeldownloads.Model{f.model} }
func (f *testModelFetcher) Download(ctx context.Context, _ modeldownloads.Model, token string, progress func(modeldownloads.Progress)) error {
	if token != "private-test-token" {
		return errors.New("unexpected token")
	}
	close(f.start)
	progress(modeldownloads.Progress{Phase: "downloading", DownloadedBytes: 10, TotalBytes: 20, TotalFiles: 1})
	select {
	case <-ctx.Done():
		<-f.release
		close(f.exit)
		return ctx.Err()
	case <-f.release:
		close(f.exit)
		return f.err
	}
}

func downloadTestHandler(t *testing.T) (*Handler, *testModelFetcher, *modelstorage.Gate, uint) {
	t.Helper()
	h, db, user, _ := modelStorageTestHandler(t)
	if err := db.AutoMigrate(&models.ModelDownload{}); err != nil {
		t.Fatal(err)
	}
	f := &testModelFetcher{model: modeldownloads.Model{ID: strings.Repeat("a", 32), Source: storageTestSource, Name: "Cohere", Dependencies: []string{"shared/embedding"}}, start: make(chan struct{}), exit: make(chan struct{}), release: make(chan struct{})}
	gate := modelstorage.NewGate()
	h.modelDownloads = modeldownloads.New(modeldownloads.Options{Store: repository.NewModelDownloadRepository(db), Fetcher: f, Gate: gate, Token: func(context.Context, uint) (string, error) { return "private-test-token", nil }})
	if err := h.modelDownloads.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		select {
		case <-f.release:
		default:
			close(f.release)
		}
		h.modelDownloads.Close()
	})
	return h, f, gate, user.ID
}

func TestManualDownloadRejectsCallerRecipesAndUnknownModels(t *testing.T) {
	h, _, _, owner := downloadTestHandler(t)
	for _, body := range []string{`{"model_id":"` + strings.Repeat("b", 32) + `"}`, `{"model_id":"../../other"}`, `{"model_id":"` + strings.Repeat("a", 32) + `","url":"https://example.com/private"}`, `{"model_id":"` + strings.Repeat("a", 32) + `"} {}`} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("user_id", owner)
		c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		h.DownloadModel(c)
		if w.Code != 400 {
			t.Fatalf("invalid admission got %d: %s", w.Code, w.Body.String())
		}
	}
	jobs, _ := h.modelDownloads.List(context.Background())
	if len(jobs) != 0 {
		t.Fatalf("invalid recipes wrote jobs: %+v", jobs)
	}
}

func TestManualDownloadKeepsLeaseUntilCanceledChildExits(t *testing.T) {
	h, f, gate, owner := downloadTestHandler(t)
	job, created, err := h.modelDownloads.Enqueue(context.Background(), owner, f.model.ID, false)
	if err != nil || !created {
		t.Fatal(err)
	}
	select {
	case <-f.start:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not start")
	}
	uses := gate.Uses()
	if len(uses) != 1 || uses[0].State != "downloading" || len(uses[0].Resources) != 2 {
		t.Fatalf("missing dependency lease: %+v", uses)
	}
	if _, err := h.modelDownloads.Cancel(context.Background(), owner, job.ID); err != nil {
		t.Fatal(err)
	}
	if len(gate.Uses()) != 1 {
		t.Fatal("released files before child exited")
	}
	close(f.release)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		jobs, _ := h.modelDownloads.List(context.Background())
		if jobs[0].State == "canceled" && len(gate.Uses()) == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("canceled download did not release after child exit")
}

func TestManualDownloadFailuresAreRedactedAndDoNotChangeRuns(t *testing.T) {
	h, f, _, owner := downloadTestHandler(t)
	f.err = errors.New("private-test-token and /server/private/path and private transcript")
	_, _, err := h.modelDownloads.Enqueue(context.Background(), owner, f.model.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	<-f.start
	close(f.release)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		jobs, _ := h.modelDownloads.List(context.Background())
		if len(jobs) > 0 && jobs[0].State == "failed" {
			data, _ := json.Marshal(jobs)
			for _, secret := range []string{"private-test-token", "/server/private", "private transcript"} {
				if strings.Contains(string(data), secret) {
					t.Fatalf("leaked %s", secret)
				}
			}
			if jobs[0].ErrorCode != "download_failed" {
				t.Fatal(jobs[0].ErrorCode)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("failed download did not become terminal")
}

func TestManualDownloadRoutesRejectAPIKeysAndUnsignedRequests(t *testing.T) {
	h, db, _, _ := modelStorageTestHandler(t)
	previous := database.DB
	database.DB = db
	t.Cleanup(func() { database.DB = previous })
	router := SetupRoutes(h, h.authService)
	for _, endpoint := range []struct{ method, path string }{{"GET", "/catalog"}, {"GET", "/downloads"}, {"POST", "/downloads"}, {"POST", "/downloads/unknown/cancel"}} {
		req := httptest.NewRequest(endpoint.method, "/api/v1/settings/models"+endpoint.path, strings.NewReader(`{}`))
		req.Header.Set("X-API-Key", "upload-key")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s accepted an upload key: %d", endpoint.method, endpoint.path, w.Code)
		}
	}
}

func TestUnprovenPreviousWorkersDoNotResumeDownloads(t *testing.T) {
	h, db, _, _ := modelStorageTestHandler(t)
	if err := db.AutoMigrate(&models.ModelDownload{}); err != nil {
		t.Fatal(err)
	}
	f := &testModelFetcher{model: modeldownloads.Model{ID: strings.Repeat("a", 32), Source: storageTestSource}}
	store := repository.NewModelDownloadRepository(db)
	job, _, err := store.Enqueue(context.Background(), 1, f.model, false)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = store.Next(context.Background())
	h.modelDownloads = modeldownloads.New(modeldownloads.Options{Store: store, Fetcher: f, WorkersStopped: func() bool { return false }})
	if !errors.Is(h.StartModelDownloads(), modeldownloads.ErrUnavailable) {
		t.Fatal("unverified worker boundary was accepted")
	}
	jobs, _ := store.List(context.Background())
	if jobs[0].ID != job.ID || jobs[0].State != "downloading" {
		t.Fatal("changed interrupted job without worker proof")
	}
	uses, err := repository.ReadModelDownloadUses(context.Background(), db, f.Catalog())
	if err != nil || len(uses) != 1 || uses[0].State != "downloading" {
		t.Fatalf("lost durable protection: %+v %v", uses, err)
	}
}

func TestDownloadKeepIsSavedForAllDependenciesBeforeReleasingFiles(t *testing.T) {
	h, db, user, _ := modelStorageTestHandler(t)
	f := &testModelFetcher{model: modeldownloads.Model{ID: strings.Repeat("a", 32), Source: storageTestSource, Dependencies: []string{"shared/embedding"}}, start: make(chan struct{}), exit: make(chan struct{}), release: make(chan struct{})}
	gate := modelstorage.NewGate()
	kept := map[string]bool{"previous/kept-model": true}
	h.modelDownloads = modeldownloads.New(modeldownloads.Options{Store: repository.NewModelDownloadRepository(db), Fetcher: f, Gate: gate,
		Token: func(context.Context, uint) (string, error) { return "private-test-token", nil },
		Keep: func(_ context.Context, sources []string) error {
			if len(gate.Uses()) == 0 {
				t.Error("keep save raced file removal")
			}
			for _, source := range sources {
				kept[source] = true
			}
			return nil
		},
	})
	if err := h.StartModelDownloads(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.StopModelDownloads)
	job, _, err := h.modelDownloads.Enqueue(context.Background(), user.ID, f.model.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	<-f.start
	// An uncertain retry with false is idempotent and cannot downgrade keep.
	again, created, err := h.modelDownloads.Enqueue(context.Background(), user.ID, f.model.ID, false)
	if err != nil || created || again.ID != job.ID {
		t.Fatalf("duplicate admission: %+v %v", again, err)
	}
	close(f.release)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		jobs, _ := h.modelDownloads.List(context.Background())
		if jobs[0].State == "completed" {
			h.StopModelDownloads()
			if !kept[storageTestSource] || !kept["shared/embedding"] || !kept["previous/kept-model"] {
				t.Fatal(kept)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("download/keep did not finish")
}
