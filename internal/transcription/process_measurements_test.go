package transcription

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"scriberr/internal/models"
)

func TestProcessMeasurementsSelectDevicesForLocalAndCloudStages(t *testing.T) {
	for _, test := range []struct {
		params models.WhisperXParams
		want   string
	}{
		{models.WhisperXParams{Device: "cpu"}, "cpu"},
		{models.WhisperXParams{Device: "cuda"}, "auto"},
		{models.WhisperXParams{Device: "cpu", Diarize: true, DiarizationDevice: "cuda"}, "auto"},
		{models.WhisperXParams{Device: "cpu", Diarize: true, DiarizeModel: "native", DiarizationDevice: "cuda"}, "cpu"},
		{models.WhisperXParams{ModelFamily: FamilyOpenAI}, "cpu"},
		{models.WhisperXParams{ModelFamily: FamilyOpenAI, Diarize: true, DiarizationDevice: "cpu"}, "cpu"},
		{models.WhisperXParams{ModelFamily: FamilyOpenAI, Diarize: true, DiarizationDevice: "cuda"}, "auto"},
	} {
		require.Equal(t, test.want, executionMeasurementDevice(test.params))
	}
}

func TestProcessMeasurementsUseOwnedDescendantsAndRejectPIDReuse(t *testing.T) {
	all := map[int]processReading{1: {parent: 0}, 2: {parent: 1, matches: true, rss: 20}, 3: {parent: 2, rss: 30}, 4: {parent: 1, rss: 80}, 5: {parent: 7, matches: true, rss: 90}}
	owned := ownedProcessReadings(all, 1)
	require.Len(t, owned, 2)
	require.Equal(t, int64(30), owned[3].rss)
	require.NotContains(t, owned, 1)
	require.NotContains(t, owned, 4)
	require.NotContains(t, owned, 5)
	var cpu processCPUAccumulator
	t0 := time.Unix(0, 0)
	cpu.observe(t0, map[int]processReading{2: {startTicks: 10, cpuTicks: 100}}, 100)
	cpu.observe(t0.Add(time.Second), map[int]processReading{2: {startTicks: 10, cpuTicks: 300}}, 100)
	require.InDelta(t, 200, *cpu.average(), 0.001)
	cpu.observe(t0.Add(2*time.Second), map[int]processReading{2: {startTicks: 11, cpuTicks: 10000}}, 100)
	require.InDelta(t, 100, *cpu.average(), 0.001, "reused PID must not contribute its unrelated counter")
}

func TestProcessMeasurementsParseKernelUnitsAndCPUQuota(t *testing.T) {
	fields := strings.Fields("S 1 2 3 4 5 6 7 8 9 10 120 80 0 0 0 0 1 0 42 99999 7")
	reading, ok := parseProcessStat("23 (worker name (gpu)) "+strings.Join(fields, " "), 4096)
	require.True(t, ok)
	require.Equal(t, uint64(200), reading.cpuTicks)
	require.Equal(t, uint64(42), reading.startTicks)
	require.Equal(t, int64(7*4096), reading.rss)
	require.Equal(t, 1.5, boundedCPUQuota(10, "150000", "100000"))
	require.Equal(t, 10., boundedCPUQuota(10, "max", "100000"))
	_, ok = parseProcessStat("malformed", 4096)
	require.False(t, ok)
}

func TestProcessMeasurementsTimeWeightAveragesAndKeepMissingIntervalsUnknown(t *testing.T) {
	var average sampledAverage
	t0 := time.Unix(0, 0)
	value := func(v float64) *float64 { return &v }
	average.observe(t0, value(100))
	require.Nil(t, average.average())
	average.observe(t0.Add(time.Second), value(300))
	average.observe(t0.Add(4*time.Second), value(300))
	require.InDelta(t, 275, *average.average(), 0.0001)
	average.observe(t0.Add(5*time.Second), nil)
	average.observe(t0.Add(8*time.Second), value(500))
	average.observe(t0.Add(9*time.Second), value(500))
	require.Equal(t, 5., average.seconds)
	require.InDelta(t, 320, *average.average(), 0.0001)
	require.False(t, math.IsNaN(*average.average()))
	var cpu processCPUAccumulator
	cpu.observe(t0, nil, 0)
	cpu.observe(t0.Add(time.Second), nil, 0)
	require.Nil(t, cpu.average())
}
