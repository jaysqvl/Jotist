//go:build linux

package processutil

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSupervisionStopsIdleDescendantGroup(t *testing.T) {
	ctx := WithSupervision(context.Background(), SupervisionPolicy{IdleTimeout: 400 * time.Millisecond, Interval: 50 * time.Millisecond})
	cmd := CommandContext(ctx, "sh", "-c", "sleep 60 & wait")
	started := time.Now()
	require.ErrorIs(t, Run(ctx, cmd), ErrWorkerStalled)
	require.Less(t, time.Since(started), 5*time.Second)
}

func TestSupervisionOutputStopsIdleMetadataGroup(t *testing.T) {
	deadline, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx := WithSupervision(deadline, SupervisionPolicy{IdleTimeout: 400 * time.Millisecond, Interval: 50 * time.Millisecond})
	cmd := CommandContext(ctx, "sh", "-c", "printf 16.02; printf diagnostic >&2; sleep 60 & wait")
	output, err := Output(ctx, cmd)
	require.ErrorIs(t, err, ErrWorkerStalled)
	require.Equal(t, "16.02", string(output))
}

func TestSupervisionKeepsBusyCPUAndMonotonicProgress(t *testing.T) {
	for _, script := range []string{
		"while :; do :; done",
		"i=0; while :; do i=$((i+1)); printf 'JOTIST_PROGRESS={\"completed_units\":%s}\\n' \"$i\"; sleep 0.1; done",
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		supervised := WithSupervision(ctx, SupervisionPolicy{IdleTimeout: 400 * time.Millisecond, Interval: 50 * time.Millisecond})
		err := Run(supervised, CommandContext(supervised, "sh", "-c", script))
		cancel()
		require.ErrorIs(t, err, context.DeadlineExceeded, "active worker was terminated as idle")
	}
}

func TestWorkerActivityObservesCPUChild(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := CommandContext(ctx, "sh", "-c", "while :; do :; done & wait")
	require.NoError(t, cmd.Start())
	defer func() { cancel(); _ = cmd.Wait() }()
	var reading activityReading
	require.Eventually(t, func() bool {
		reading = readWorkerActivity(cmd.Process.Pid)
		return reading.known && len(reading.counters) >= 2
	}, time.Second, 10*time.Millisecond)
	require.Contains(t, reading.counters, cmd.Process.Pid)
	for pid := range reading.counters {
		require.Positive(t, pid)
	}
}
