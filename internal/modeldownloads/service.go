package modeldownloads

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/jaysqvl/Jotist/internal/modelstorage"
)

type Options struct {
	Store          Store
	Fetcher        Fetcher
	Gate           *modelstorage.Gate
	Token          func(context.Context, uint) (string, error)
	Keep           func(context.Context, []string) error
	WorkersStopped func() bool
}

// Service has one event-driven worker. It never polls files or the recording
// queue. The process server lease prevents a second server from owning this DB.
type Service struct {
	options           Options
	models            map[string]Model
	mu                sync.Mutex
	started, stopping bool
	ctx               context.Context
	stop              context.CancelFunc
	done              chan struct{}
	wake              chan struct{}
	activeID          string
	activeCancel      context.CancelFunc
	subscribers       map[chan Event]struct{}
}

func New(options Options) *Service {
	if options.Gate == nil {
		options.Gate = modelstorage.ProcessGate()
	}
	s := &Service{options: options, models: make(map[string]Model), done: make(chan struct{}), wake: make(chan struct{}, 1), subscribers: make(map[chan Event]struct{})}
	if options.Fetcher != nil {
		for _, m := range options.Fetcher.Catalog() {
			s.models[m.ID] = m
		}
	}
	return s
}

func (s *Service) Catalog() []Model                        { return s.options.Fetcher.Catalog() }
func (s *Service) List(ctx context.Context) ([]Job, error) { return s.options.Store.List(ctx) }

func (s *Service) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return nil
	}
	if s.options.WorkersStopped != nil && !s.options.WorkersStopped() {
		return ErrUnavailable
	}
	if s.stopping || s.options.Store == nil || s.options.Fetcher == nil {
		return ErrUnavailable
	}
	if err := s.options.Store.Recover(context.Background()); err != nil {
		return err
	}
	s.ctx, s.stop = context.WithCancel(context.Background())
	s.started = true
	go s.run()
	s.signal()
	return nil
}

func (s *Service) Close() {
	s.mu.Lock()
	s.stopping = true
	if !s.started {
		s.mu.Unlock()
		return
	}
	s.stop()
	done := s.done
	s.mu.Unlock()
	<-done
}

func (s *Service) Enqueue(ctx context.Context, owner uint, id string, keep bool) (Job, bool, error) {
	model, ok := s.models[id]
	if !ok {
		return Job{}, false, ErrUnknown
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started || s.stopping {
		return Job{}, false, ErrUnavailable
	}
	job, created, err := s.options.Store.Enqueue(ctx, owner, model, keep)
	if err == nil {
		s.publishLocked(Event{Type: "model_download_changed", Job: &job})
		s.signal()
	}
	return job, created, err
}

func (s *Service) Cancel(ctx context.Context, owner uint, id string) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started || s.stopping {
		return Job{}, ErrUnavailable
	}
	job, err := s.options.Store.Cancel(ctx, id, owner)
	if err != nil {
		return Job{}, err
	}
	if s.activeID == id && s.activeCancel != nil {
		s.activeCancel()
	}
	s.publishLocked(Event{Type: "model_download_changed", Job: &job})
	return job, nil
}

func (s *Service) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Service) run() {
	defer close(s.done)
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.wake:
		}
		for s.ctx.Err() == nil {
			// Coordinate claim/cancel publication. A cancellation cannot land in
			// the gap between a durable claim and registering its cancel function.
			s.mu.Lock()
			job, err := s.options.Store.Next(s.ctx)
			if err != nil || job == nil {
				s.mu.Unlock()
				if err != nil && s.ctx.Err() == nil {
					// Retry a transient store failure while work is intended. Normal
					// idle operation blocks on wake and performs no recurring reads.
					timer := time.NewTimer(10 * time.Second)
					select {
					case <-s.ctx.Done():
						timer.Stop()
						return
					case <-timer.C:
						s.signal()
					}
				}
				break
			}
			ctx, cancel := context.WithCancel(s.ctx)
			s.activeID, s.activeCancel = job.ID, cancel
			s.publishLocked(Event{Type: "model_download_changed", Job: job})
			s.mu.Unlock()
			s.execute(ctx, *job)
			cancel()
			s.mu.Lock()
			s.activeID, s.activeCancel = "", nil
			s.mu.Unlock()
		}
	}
}

func (s *Service) execute(ctx context.Context, job Job) {
	model, ok := s.models[job.ModelID]
	if !ok || model.Source != job.Source {
		s.finish(job.ID, "failed", "catalog_changed", "This model changed after the download was queued. Choose it again.")
		return
	}
	sources := append([]string{model.Source}, model.Dependencies...)
	release := s.options.Gate.BeginUse(modelstorage.Use{Resources: sources, Stage: "model download", State: "downloading"})
	defer release() // The lease outlives the child process and keep-preference save.
	var token string
	var err error
	if s.options.Token != nil {
		token, err = s.options.Token(ctx, job.OwnerID)
	}
	if err != nil {
		s.finish(job.ID, "failed", "credentials_unavailable", "Could not read the saved Hugging Face access token. Check Settings and try again.")
		return
	}
	lastUpdate := time.Time{}
	progress := func(p Progress) {
		if !validProgress(p) || (time.Since(lastUpdate) < time.Second && p.Phase == "downloading" && p.CompletedFiles < p.TotalFiles) {
			return
		}
		lastUpdate = time.Now()
		if updated, err := s.options.Store.Progress(ctx, job.ID, p); err == nil {
			s.publish(Event{Type: "model_download_changed", Job: &updated})
		}
	}
	err = s.options.Fetcher.Download(ctx, model, token, progress)
	if ctx.Err() != nil {
		if s.ctx.Err() != nil {
			return
		} // Shutdown leaves it durable for resume.
		s.finish(job.ID, "canceled", "", "Download canceled. Completed files can be reused.")
		return
	}
	if err != nil {
		code, message := publicFailure(err)
		s.finish(job.ID, "failed", code, message)
		return
	}
	s.options.Gate.FilesChanged(modelstorage.Use{Resources: sources})
	// Reconcile a repeated admission's stronger keep request while the worker
	// was running. An omitted/false request never clears an existing model pin.
	jobs, err := s.options.Store.List(ctx)
	if err == nil {
		for _, current := range jobs {
			if current.ID == job.ID {
				job = current
				break
			}
		}
	}
	if err != nil {
		s.finish(job.ID, "failed", "settings_unavailable", "Files downloaded, but the keep preference could not be verified. Try again to finish.")
		return
	}
	if job.KeepDownloaded {
		if s.options.Keep == nil || s.options.Keep(ctx, sources) != nil {
			s.finish(job.ID, "failed", "keep_failed", "Files downloaded, but Keep downloaded could not be saved. Try again or set it in the model list.")
			return
		}
	}
	s.finish(job.ID, "completed", "", "Model files downloaded. No run was started.")
}

func validProgress(p Progress) bool {
	return (p.Phase == "preparing" || p.Phase == "checking" || p.Phase == "downloading" || p.Phase == "verifying") &&
		p.DownloadedBytes >= 0 && p.TotalBytes >= 0 && p.DownloadedBytes <= p.TotalBytes && p.CompletedFiles >= 0 && p.TotalFiles >= p.CompletedFiles
}

func publicFailure(err error) (string, string) {
	var e DownloadError
	if errors.As(err, &e) {
		switch e.Code {
		case "access_denied":
			return e.Code, "Access was denied. Add a Hugging Face token in Settings and accept the model's access terms, then try again."
		case "not_found":
			return e.Code, "The model files or pinned revision are unavailable from their publisher."
		case "disk_full":
			return e.Code, "There is not enough storage for this download. Free space and try again."
		case "storage_low":
			return e.Code, "There is not enough space to download this model while keeping 2 GiB free for recordings. Free space and try again."
		case "network":
			return e.Code, "The download could not reach the model host. Try again; completed files can be reused."
		case "runtime_setup":
			return e.Code, "The download helper could not be prepared. Check the server's Python and uv installation."
		}
	}
	return "download_failed", "The download did not finish. Try again; completed files can be reused."
}

func (s *Service) finish(id, state, code, message string) {
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		job, err := s.options.Store.Finish(ctx, id, state, code, message)
		cancel()
		if err == nil {
			s.publish(Event{Type: "model_download_changed", Job: &job})
			return
		}
		// Never strand an active durable job after a transient database error.
		// Shutdown leaves it for the next proven worker boundary to reconcile.
		timer := time.NewTimer(time.Second)
		select {
		case <-s.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (s *Service) Available() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.started && !s.stopping }

func (s *Service) publish(event Event) { s.mu.Lock(); defer s.mu.Unlock(); s.publishLocked(event) }
func (s *Service) publishLocked(event Event) {
	for ch := range s.subscribers {
		select {
		case ch <- event:
		default:
			// A slow client reconciles the bounded durable job list; never block
			// a download or send a stale progress value after a terminal event.
			for len(ch) > 0 {
				select {
				case <-ch:
				default:
				}
			}
			ch <- Event{Type: "model_downloads_changed"}
		}
	}
}
func (s *Service) Subscribe() (<-chan Event, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := make(chan Event, 8)
	s.subscribers[ch] = struct{}{}
	var once sync.Once
	return ch, func() { once.Do(func() { s.mu.Lock(); delete(s.subscribers, ch); s.mu.Unlock() }) }
}
