package processutil

import (
	"bytes"
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestProgressIsMonotonicAndBounded(t *testing.T) {
	var output bytes.Buffer
	progress := &workerProgress{}
	w := &progressWriter{destination: &output, state: progress}
	before := time.Now().Add(-time.Second)
	_, err := w.Write([]byte("heartbeat\nJOTIST_PROGRESS={\"completed_"))
	require.NoError(t, err)
	require.False(t, progress.since(before))
	_, err = w.Write([]byte("units\":1}\n"))
	require.NoError(t, err)
	require.True(t, progress.since(before))
	first := progress.advanced
	_, err = w.Write([]byte("JOTIST_PROGRESS={\"completed_units\":1}\nJOTIST_PROGRESS={\"completed_units\":1000000001}\n"))
	require.NoError(t, err)
	require.Equal(t, first, progress.advanced, "repeated heartbeats cannot hide a stalled worker")
	_, err = w.Write(append(bytes.Repeat([]byte("x"), 4096), '\n'))
	require.NoError(t, err)
	require.LessOrEqual(t, cap(w.partial), 4096)
	require.Contains(t, output.String(), "completed_units")
}

func TestActivityCountersSeparateContentionAndPIDReuse(t *testing.T) {
	prior := map[int]activityCounters{7: {start: 42, cpu: 8, io: 16, delay: 1}}
	active, starved := countersAdvanced(prior, map[int]activityCounters{7: {start: 42, cpu: 8, io: 16, delay: 20}})
	require.False(t, active)
	require.True(t, starved)
	active, _ = countersAdvanced(prior, map[int]activityCounters{7: {start: 43, cpu: 0}})
	require.True(t, active, "new worker identity is not a stalled old process")
	active, starved = countersAdvanced(prior, prior)
	require.False(t, active)
	require.False(t, starved)
}

func TestTranscriptionInactivityDefaultAndDisable(t *testing.T) {
	t.Setenv("TRANSCRIPTION_STALL_MINUTES", "")
	require.Equal(t, 30*time.Minute, DefaultSupervisionPolicy().IdleTimeout)
	t.Setenv("TRANSCRIPTION_STALL_MINUTES", "0")
	require.Zero(t, DefaultSupervisionPolicy().IdleTimeout)
	t.Setenv("TRANSCRIPTION_STALL_MINUTES", "-1")
	require.Equal(t, 30*time.Minute, DefaultSupervisionPolicy().IdleTimeout)
}

func TestSharedOutputKeepsSingleCopierAndCancellation(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("requires POSIX shell")
	}
	ctx := WithSupervision(context.Background(), SupervisionPolicy{IdleTimeout: time.Second, Interval: 10 * time.Millisecond})
	cmd := CommandContext(ctx, "sh", "-c", "printf output; printf diagnostic >&2")
	output, err := CombinedOutput(ctx, cmd)
	require.NoError(t, err)
	require.Equal(t, "outputdiagnostic", string(output))
	cancelCtx, cancel := context.WithCancel(ctx)
	cmd = CommandContext(cancelCtx, "sh", "-c", "sleep 60 & wait")
	time.AfterFunc(100*time.Millisecond, cancel)
	require.ErrorIs(t, Run(cancelCtx, cmd), context.Canceled)
}
