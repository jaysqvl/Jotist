package transcription

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/jaysqvl/Jotist/internal/models"
)

func executionMeasurementDevice(params models.WhisperXParams) string {
	localASR := params.ModelFamily != FamilyOpenAI
	if localASR && params.Device != "cpu" {
		return "auto"
	}
	if !params.Diarize || params.DiarizeModel == "native" || params.DiarizationDevice == "cpu" || (localASR && params.DiarizationDevice == "same") {
		return "cpu"
	}
	if params.DiarizationDevice == "" && localASR && params.ModelFamily != FamilyNvidiaCanary && params.ModelFamily != FamilyNvidiaCanaryQwen && params.ModelFamily != FamilyNvidiaParakeet && params.ModelFamily != FamilyMistralVoxtral {
		return "cpu"
	}
	return "auto"
}

type processReading struct {
	parent     int
	rss        int64
	cpuTicks   uint64
	startTicks uint64
	matches    bool
}

func parseProcessStat(stat string, pageSize int64) (processReading, bool) {
	end := strings.LastIndexByte(stat, ')')
	if end < 0 {
		return processReading{}, false
	}
	f := strings.Fields(stat[end+1:])
	if len(f) < 22 {
		return processReading{}, false
	}
	parent, e1 := strconv.Atoi(f[1])
	utime, e2 := strconv.ParseUint(f[11], 10, 64)
	stime, e3 := strconv.ParseUint(f[12], 10, 64)
	started, e4 := strconv.ParseUint(f[19], 10, 64)
	rss, e5 := strconv.ParseInt(f[21], 10, 64)
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil || e5 != nil {
		return processReading{}, false
	}
	return processReading{parent: parent, rss: policyMax(0, rss) * pageSize, cpuTicks: utime + stime, startTicks: started}, true
}

func ownedProcessReadings(all map[int]processReading, coordinator int) map[int]processReading {
	result := map[int]processReading{}
	for pid, p := range all {
		if pid == coordinator {
			continue // Coordinator work is shared by every recording.
		}
		current, matched := pid, p.matches
		for depth := 0; depth < 64; depth++ {
			ancestor, ok := all[current]
			if !ok || ancestor.parent == current {
				break
			}
			matched = matched || ancestor.matches
			if ancestor.parent == coordinator {
				if matched {
					result[pid] = p
				}
				break
			}
			current = ancestor.parent
		}
	}
	return result
}

func stageProcessReadings(directory string) map[int]processReading {
	entries, err := os.ReadDir("/proc")
	if err != nil || directory == "" {
		return nil
	}
	all := map[int]processReading{}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "stat"))
		if err != nil {
			continue
		}
		reading, ok := parseProcessStat(string(data), int64(os.Getpagesize()))
		if !ok {
			continue
		}
		command, _ := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
		reading.matches = strings.Contains(string(command), directory)
		all[pid] = reading
	}
	return ownedProcessReadings(all, os.Getpid())
}

// AT_CLKTCK supplies the kernel's units, including on non-x86 Linux. No
// guessed 100 Hz value or extra process is needed in the sampling loop.
func processClockTicks() float64 {
	data, err := os.ReadFile("/proc/self/auxv")
	if err != nil {
		return 0
	}
	width := strconv.IntSize / 8
	for offset := 0; offset+2*width <= len(data); offset += 2 * width {
		var key, value uint64
		if width == 8 {
			key, value = binary.NativeEndian.Uint64(data[offset:]), binary.NativeEndian.Uint64(data[offset+width:])
		} else {
			key, value = uint64(binary.NativeEndian.Uint32(data[offset:])), uint64(binary.NativeEndian.Uint32(data[offset+width:]))
		}
		if key == 17 && value > 0 { // AT_CLKTCK
			return float64(value)
		}
	}
	return 0
}

func permittedCPUCount() float64 {
	count := float64(runtime.NumCPU())
	if data, err := os.ReadFile("/sys/fs/cgroup/cpu.max"); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) == 2 {
			count = boundedCPUQuota(count, fields[0], fields[1])
		}
	} else {
		quota, e1 := os.ReadFile("/sys/fs/cgroup/cpu/cpu.cfs_quota_us")
		period, e2 := os.ReadFile("/sys/fs/cgroup/cpu/cpu.cfs_period_us")
		if e1 == nil && e2 == nil {
			count = boundedCPUQuota(count, strings.TrimSpace(string(quota)), strings.TrimSpace(string(period)))
		}
	}
	return count
}

func boundedCPUQuota(cores float64, quota, period string) float64 {
	q, e1 := strconv.ParseFloat(quota, 64)
	p, e2 := strconv.ParseFloat(period, 64)
	if e1 == nil && e2 == nil && q > 0 && p > 0 && q/p < cores {
		return q / p
	}
	return cores
}

// Integrate adjacent valid readings. Missing intervals break continuity;
// arithmetic sample means would bias irregular or delayed observations.
type sampledAverage struct {
	lastTime time.Time
	last     float64
	valid    bool
	weighted float64
	seconds  float64
}

func (a *sampledAverage) observe(at time.Time, value *float64) {
	if value != nil && a.valid {
		seconds := at.Sub(a.lastTime).Seconds()
		if seconds > 0 {
			a.weighted += (a.last + *value) / 2 * seconds
			a.seconds += seconds
		}
	}
	a.lastTime, a.valid = at, value != nil
	if value != nil {
		a.last = *value
	}
}

func (a *sampledAverage) average() *float64 {
	if a.seconds <= 0 {
		return nil
	}
	value := a.weighted / a.seconds
	return &value
}

type processCPUAccumulator struct {
	previous map[int]processReading
	lastTime time.Time
	seconds  float64
	used     float64
	seen     bool
	matched  bool
}

func (a *processCPUAccumulator) observe(at time.Time, processes map[int]processReading, clock float64) {
	if len(processes) > 0 {
		a.seen = true
	}
	seconds := at.Sub(a.lastTime).Seconds()
	if processes != nil && a.previous != nil && seconds > 0 && clock > 0 {
		var ticks uint64
		for pid, reading := range processes {
			previous, found := a.previous[pid]
			if found && previous.startTicks == reading.startTicks && reading.cpuTicks >= previous.cpuTicks {
				a.matched = true
				ticks += reading.cpuTicks - previous.cpuTicks
			}
		}
		a.used += float64(ticks) / clock
		a.seconds += seconds
	}
	a.previous, a.lastTime = processes, at
}

func (a *processCPUAccumulator) average() *float64 {
	if !a.seen || !a.matched || a.seconds <= 0 {
		return nil
	}
	value := a.used / a.seconds * 100 // 100% is one logical CPU; can exceed 100%.
	return &value
}
