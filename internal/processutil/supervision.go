package processutil

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrWorkerStalled is distinct from cancellation and a wall-clock deadline.
// Completed stages remain available for an explicit resume.
var ErrWorkerStalled = errors.New("model worker stopped after sustained inactivity; completed checkpoints retained")

type SupervisionPolicy struct {
	IdleTimeout time.Duration
	Interval    time.Duration
	Observe     func(SupervisionEvent)
}

type SupervisionEvent struct {
	State       string
	IdleSeconds int64
}

type supervisionKey struct{}

// WithSupervision supplies the same lifecycle policy to every subprocess in a
// transcription, including installation, recognition, alignment and speakers.
// Other callers of processutil retain their own existing context policy.
func WithSupervision(ctx context.Context, policy SupervisionPolicy) context.Context {
	return context.WithValue(ctx, supervisionKey{}, policy)
}

func DefaultSupervisionPolicy() SupervisionPolicy {
	minutes := 30
	if value, err := strconv.Atoi(os.Getenv("TRANSCRIPTION_STALL_MINUTES")); err == nil && value >= 0 && value <= 24*60 {
		minutes = value
	}
	return SupervisionPolicy{IdleTimeout: time.Duration(minutes) * time.Minute, Interval: 5 * time.Second}
}

// A monotonic counter is progress. Repeated heartbeats and arbitrary log lines
// are not. The parser is bounded and never retains or publishes user text.
type progressWriter struct {
	destination io.Writer
	state       *workerProgress
	partial     []byte
	dropping    bool
	mu          sync.Mutex
}

type workerProgress struct {
	mu       sync.Mutex
	units    uint64
	advanced time.Time
}

func (w *progressWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, value := range data {
		if value == '\n' {
			if !w.dropping {
				w.state.accept(w.partial, time.Now())
			}
			w.partial, w.dropping = w.partial[:0], false
		} else if !w.dropping {
			if len(w.partial) >= 2048 {
				w.partial, w.dropping = w.partial[:0], true
			} else {
				w.partial = append(w.partial, value)
			}
		}
	}
	return w.destination.Write(data)
}

func (p *workerProgress) accept(line []byte, at time.Time) {
	const prefix = "JOTIST_PROGRESS="
	if !strings.HasPrefix(string(line), prefix) {
		return
	}
	var event struct {
		CompletedUnits uint64 `json:"completed_units"`
	}
	if json.Unmarshal(line[len(prefix):], &event) != nil || event.CompletedUnits > 1_000_000_000 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if event.CompletedUnits > p.units {
		p.units, p.advanced = event.CompletedUnits, at
	}
}

func (p *workerProgress) since(at time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.advanced.After(at)
}

type activityReading struct {
	known, busy, starved, gpu bool
	counters                  map[int]activityCounters
}

type activityCounters struct {
	start, cpu, io, delay uint64
}

func countersAdvanced(previous, next map[int]activityCounters) (active, starved bool) {
	for pid, current := range next {
		prior, ok := previous[pid]
		if !ok || prior.start != current.start {
			return true, false // A new descendant is active setup/work.
		}
		if current.cpu > prior.cpu || current.io > prior.io {
			active = true
		}
		if current.delay > prior.delay {
			starved = true
		}
	}
	return
}

// Run supervises the entire worker process group. CPU and I/O counters are
// coarse activity evidence, not proof of semantic progress or model accuracy.
// Missing observations, runnable/IO-blocked workers and active/unknown GPU work
// defer termination: uncertainty must not turn a slow CPU job into a failure.
func Run(ctx context.Context, cmd *exec.Cmd) error {
	policy, enabled := ctx.Value(supervisionKey{}).(SupervisionPolicy)
	if !enabled || policy.IdleTimeout <= 0 {
		return cmd.Run()
	}
	if policy.Interval <= 0 {
		policy.Interval = 5 * time.Second
	}
	progress := &workerProgress{}
	sharedOutput := cmd.Stdout != nil && reflect.TypeOf(cmd.Stdout).Comparable() && cmd.Stdout == cmd.Stderr
	for _, destination := range []*io.Writer{&cmd.Stdout, &cmd.Stderr} {
		writer := *destination
		if writer == nil {
			writer = io.Discard
		}
		*destination = &progressWriter{destination: writer, state: progress}
	}
	if sharedOutput {
		cmd.Stderr = cmd.Stdout // Preserve exec's single copier for a shared buffer.
	}
	if cmd.WaitDelay == 0 {
		cmd.WaitDelay = 5 * time.Second
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	result := make(chan bool, 1)
	go func() {
		result <- superviseWorker(ctx, cmd, policy, progress, done)
	}()
	err := cmd.Wait()
	close(done)
	stalled := <-result
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if stalled {
		return ErrWorkerStalled
	}
	return err
}

func CombinedOutput(ctx context.Context, cmd *exec.Cmd) ([]byte, error) {
	if cmd.Stdout != nil || cmd.Stderr != nil {
		return nil, errors.New("exec: output already set")
	}
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	err := Run(ctx, cmd)
	return output.Bytes(), err
}

func superviseWorker(ctx context.Context, cmd *exec.Cmd, policy SupervisionPolicy, progress *workerProgress, done <-chan struct{}) bool {
	ticker := time.NewTicker(policy.Interval)
	defer ticker.Stop()
	lastActivity := time.Now()
	previous := readWorkerActivity(cmd.Process.Pid)
	lastState := ""
	observe := func(state string, at time.Time) {
		if state != lastState && policy.Observe != nil {
			policy.Observe(SupervisionEvent{State: state, IdleSeconds: int64(at.Sub(lastActivity).Seconds())})
		}
		lastState = state
	}
	for {
		select {
		case <-ctx.Done():
			return false
		case <-done:
			return false
		case at := <-ticker.C:
			current := readWorkerActivity(cmd.Process.Pid)
			advanced, starved := countersAdvanced(previous.counters, current.counters)
			if !current.known || !previous.known || current.busy || current.starved || starved || advanced || progress.since(lastActivity) {
				lastActivity = at
				observe("active_or_unverified", at)
			}
			previous = current
			if at.Sub(lastActivity) < policy.IdleTimeout {
				continue
			}
			// A GPU can compute while its CPU thread sleeps. Global GPU idleness
			// is sufficient to rule out that case; activity from another app or
			// unavailable GPU sensors cannot prove this worker is stalled.
			if current.gpu && !gpuConfirmedIdle(ctx) {
				observe("idle_gpu_unverified", at)
				continue
			}
			// Recheck completion/cancellation before signalling the group.
			select {
			case <-ctx.Done():
				return false
			case <-done:
				return false
			default:
			}
			if cmd.Cancel == nil || cmd.Cancel() != nil {
				observe("idle_termination_unverified", at)
				continue
			}
			observe("stalled", at)
			return true
		}
	}
}

func gpuConfirmedIdle(ctx context.Context) bool {
	probe, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	output, err := exec.CommandContext(probe, "nvidia-smi", "--query-gpu=utilization.gpu", "--format=csv,noheader,nounits").Output()
	if err != nil {
		return false
	}
	rows := strings.Fields(string(output))
	if len(rows) == 0 {
		return false
	}
	for _, row := range rows {
		value, err := strconv.Atoi(row)
		if err != nil || value != 0 {
			return false
		}
	}
	return true
}
