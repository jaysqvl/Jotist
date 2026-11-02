package repository

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/jaysqvl/Jotist/internal/modeldownloads"
	"github.com/jaysqvl/Jotist/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func downloadRepository(t *testing.T) (*ModelDownloadRepository, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "downloads.db")+"?_pragma=busy_timeout(5000)"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.ModelDownload{}); err != nil {
		t.Fatal(err)
	}
	return NewModelDownloadRepository(db), db
}

func TestDownloadAdmissionIsIdempotentAndPreservesKeep(t *testing.T) {
	r, db := downloadRepository(t)
	ctx := context.Background()
	m := modeldownloads.Model{ID: strings.Repeat("a", 32), Source: "publisher/model", Name: "Model"}
	var ids sync.Map
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(keep bool) {
			defer wg.Done()
			job, _, err := r.Enqueue(ctx, 1, m, keep)
			if err != nil {
				t.Error(err)
				return
			}
			ids.Store(job.ID, true)
		}(i == 3)
	}
	wg.Wait()
	n := 0
	ids.Range(func(_, _ any) bool { n++; return true })
	if n != 1 {
		t.Fatalf("created %d jobs for one source", n)
	}
	jobs, err := r.List(ctx)
	if err != nil || len(jobs) != 1 || !jobs[0].KeepDownloaded {
		t.Fatalf("keep intent was lost: %+v %v", jobs, err)
	}
	var rows int64
	db.Model(&models.ModelDownload{}).Count(&rows)
	if rows != 1 {
		t.Fatal(rows)
	}
	if _, err := r.Cancel(ctx, jobs[0].ID, 2); !errors.Is(err, modeldownloads.ErrNotFound) {
		t.Fatalf("other owner could cancel: %v", err)
	}
	if _, err := r.Cancel(ctx, jobs[0].ID, 1); err != nil {
		t.Fatal(err)
	}
	next, created, err := r.Enqueue(ctx, 1, m, false)
	if err != nil || !created || next.ID == jobs[0].ID {
		t.Fatalf("explicit retry should create a new job: %+v %v", next, err)
	}
}

func TestDownloadRestartRecoversOnlyItsOwnInterruptedJobs(t *testing.T) {
	r, db := downloadRepository(t)
	ctx := context.Background()
	if err := db.Exec("CREATE TABLE unrelated_runs (id TEXT, state TEXT)").Error; err != nil {
		t.Fatal(err)
	}
	db.Exec("INSERT INTO unrelated_runs VALUES ('existing-run','processing')")
	first, _, _ := r.Enqueue(ctx, 1, modeldownloads.Model{ID: "first", Source: "a/first", Name: "First"}, false)
	claimed, err := r.Next(ctx)
	if err != nil || claimed.ID != first.ID || claimed.State != "downloading" {
		t.Fatalf("bad claim %+v %v", claimed, err)
	}
	second, _, _ := r.Enqueue(ctx, 1, modeldownloads.Model{ID: "second", Source: "a/second", Name: "Second"}, false)
	third, _, _ := r.Enqueue(ctx, 1, modeldownloads.Model{ID: "third", Source: "a/third", Name: "Third"}, false)
	if _, err := r.Cancel(ctx, first.ID, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Finish(ctx, third.ID, "completed", "", "ok"); err != nil {
		t.Fatal(err)
	}
	if err := r.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	jobs, _ := r.List(ctx)
	states := map[string]string{}
	for _, job := range jobs {
		states[job.ID] = job.State
	}
	if states[first.ID] != "canceled" || states[second.ID] != "queued" || states[third.ID] != "completed" {
		t.Fatal(states)
	}
	var state string
	db.Raw("SELECT state FROM unrelated_runs WHERE id='existing-run'").Scan(&state)
	if state != "processing" {
		t.Fatalf("modified a recording run: %s", state)
	}
	// A second interruption resumes an ordinary active download.
	if _, err := r.Next(ctx); err != nil {
		t.Fatal(err)
	}
	if err := r.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	job, _ := r.get(ctx, second.ID)
	if job.State != "queued" {
		t.Fatal(job.State)
	}
}

func TestDownloadCancelWinsCompletionAndDoesNotSerializeOwner(t *testing.T) {
	r, _ := downloadRepository(t)
	ctx := context.Background()
	job, _, _ := r.Enqueue(ctx, 42, modeldownloads.Model{ID: "m", Source: "a/model", Name: "Model"}, false)
	_, _ = r.Next(ctx)
	_, _ = r.Cancel(ctx, job.ID, 42)
	job, err := r.Finish(ctx, job.ID, "completed", "", "downloaded")
	if err != nil || job.State != "canceled" {
		t.Fatalf("%+v %v", job, err)
	}
	data, _ := json.Marshal(job)
	if strings.Contains(string(data), "owner") || strings.Contains(string(data), "token") {
		t.Fatalf("private metadata serialized: %s", data)
	}
}
