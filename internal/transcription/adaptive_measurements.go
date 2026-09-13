package transcription

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"scriberr/internal/models"
)

type capacitySample struct {
	total, used, process   int64
	external, processKnown bool
	ownershipUnknown       bool
	pids                   map[int]bool
}

// /proc sampling is read-only and limited to this coordinator's process tree.
// Matching the stage's private output directory separates overlapping CPU work.
func stageProcesses(directory string) map[int]int64 {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	type process struct {
		parent  int
		rss     int64
		matches bool
	}
	all := map[int]process{}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "stat"))
		if err != nil {
			continue
		}
		end := strings.LastIndexByte(string(data), ')')
		if end < 0 {
			continue
		}
		fields := strings.Fields(string(data)[end+1:])
		if len(fields) < 22 {
			continue
		}
		parent, _ := strconv.Atoi(fields[1])
		rss, _ := strconv.ParseInt(fields[21], 10, 64)
		cmd, _ := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
		all[pid] = process{parent, rss * int64(os.Getpagesize()), directory != "" && strings.Contains(string(cmd), directory)}
	}
	result := map[int]int64{}
	for pid, p := range all {
		current := pid
		matched := p.matches
		ours := false
		for depth := 0; depth < 64; depth++ {
			if current == os.Getpid() {
				ours = true
				break
			}
			ancestor, ok := all[current]
			if !ok || ancestor.parent == current {
				break
			}
			matched = matched || ancestor.matches
			current = ancestor.parent
		}
		if ours && matched {
			result[pid] = policyMax(0, p.rss)
		}
	}
	return result
}

func hostCapacity() (total, available int64) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		value, _ := strconv.ParseInt(fields[1], 10, 64)
		switch fields[0] {
		case "MemTotal:":
			total = value * 1024
		case "MemAvailable:":
			available = value * 1024
		}
	}
	// cgroup limits bound the worker's actual allowance, not the host's RAM.
	for _, pair := range [][2]string{{"/sys/fs/cgroup/memory.max", "/sys/fs/cgroup/memory.current"}, {"/sys/fs/cgroup/memory/memory.limit_in_bytes", "/sys/fs/cgroup/memory/memory.usage_in_bytes"}} {
		limitData, e1 := os.ReadFile(pair[0])
		usedData, e2 := os.ReadFile(pair[1])
		if e1 != nil || e2 != nil {
			continue
		}
		limit, e1 := strconv.ParseInt(strings.TrimSpace(string(limitData)), 10, 64)
		used, e2 := strconv.ParseInt(strings.TrimSpace(string(usedData)), 10, 64)
		if e1 == nil && e2 == nil && limit > 0 && limit < total {
			total = limit
			available = policyMin(available, policyMax(0, limit-used))
			break
		}
	}
	return total, available
}

// classifyGPUProcesses keeps missing namespace attribution separate from a
// confirmed consumer that existed before this stage acquired its GPU slot.
func classifyGPUProcesses(observed map[int]int64, owned map[int]int64, baseline map[int]bool, queryComplete, preflight bool) capacitySample {
	sample := capacitySample{pids: map[int]bool{}, ownershipUnknown: !queryComplete}
	for pid, memory := range observed {
		sample.pids[pid] = true
		if _, ok := owned[pid]; ok {
			sample.process += memory
			sample.processKnown = true
		} else if preflight || baseline[pid] {
			sample.external = true
		} else {
			sample.ownershipUnknown = true
		}
	}
	return sample
}

func verifiedGPUProcesses(directory string) map[int]int64 {
	// NVML reports host PIDs. Container /proc generally exposes namespace PIDs;
	// coincident integers are not evidence that two processes are the same.
	for _, marker := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(marker); err == nil {
			return nil
		}
	}
	return stageProcesses(directory)
}

// Calls outside the running sampler are preflight: the stage has no worker yet.
func gpuCapacity(ctx context.Context, gpu gpuInventory, directory string) (capacitySample, bool) {
	return gpuCapacitySince(ctx, gpu, directory, nil, true)
}
func gpuCapacitySince(ctx context.Context, gpu gpuInventory, directory string, baseline map[int]bool, preflight bool) (capacitySample, bool) {
	if gpu.UUID == "" {
		return capacitySample{}, false
	}
	query, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	output, err := exec.CommandContext(query, "nvidia-smi", "-i", gpu.UUID, "--query-gpu=memory.total,memory.used", "--format=csv,noheader,nounits").Output()
	if err != nil {
		return capacitySample{}, false
	}
	fields := strings.Split(strings.TrimSpace(string(output)), ",")
	if len(fields) != 2 {
		return capacitySample{}, false
	}
	total, e1 := strconv.ParseInt(strings.TrimSpace(fields[0]), 10, 64)
	used, e2 := strconv.ParseInt(strings.TrimSpace(fields[1]), 10, 64)
	if e1 != nil || e2 != nil || total <= 0 || used < 0 {
		return capacitySample{}, false
	}
	output, err = exec.CommandContext(query, "nvidia-smi", "-i", gpu.UUID, "--query-compute-apps=pid,used_gpu_memory", "--format=csv,noheader,nounits").Output()
	complete := err == nil
	observed := map[int]int64{}
	if complete {
		for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
			if line == "" {
				continue
			}
			fields := strings.Split(line, ",")
			if len(fields) != 2 {
				complete = false
				continue
			}
			pid, pidErr := strconv.Atoi(strings.TrimSpace(fields[0]))
			memory, memoryErr := strconv.ParseInt(strings.TrimSpace(fields[1]), 10, 64)
			if pidErr != nil || memoryErr != nil || pid <= 0 || memory < 0 {
				complete = false
				continue
			}
			observed[pid] = memory * 1024 * 1024
		}
	}
	sample := classifyGPUProcesses(observed, verifiedGPUProcesses(directory), baseline, complete, preflight)
	sample.total, sample.used = total*1024*1024, used*1024*1024
	return sample, true
}

func startStageMeasurements(ctx context.Context, device string, gpu gpuInventory, directory string) func() models.StageMeasurements {
	started := time.Now()
	sampleCtx, cancel := context.WithCancel(ctx)
	var mu sync.Mutex
	measurement := models.StageMeasurements{Scope: "unavailable"}
	ptr := func(v int64) *int64 { return &v }
	var baseline map[int]bool
	preflight := true
	sample := func() {
		if device == "cuda" {
			initial := preflight
			preflight = false // Even an unavailable initial query must not classify a later worker as preexisting.
			value, ok := gpuCapacitySince(sampleCtx, gpu, directory, baseline, initial)
			if initial && ok {
				baseline = value.pids
			}
			if !ok {
				mu.Lock()
				measurement.OwnershipUnknown = true
				mu.Unlock()
				return
			}
			mu.Lock()
			defer mu.Unlock()
			measurement.Scope = "sampled-device-and-owned-process-vram"
			measurement.GPUTotalBytes = ptr(value.total)
			if measurement.DeviceUsedBeforeBytes == nil {
				measurement.DeviceUsedBeforeBytes = ptr(value.used)
			}
			if measurement.DevicePeakUsedBytes == nil || *measurement.DevicePeakUsedBytes < value.used {
				measurement.DevicePeakUsedBytes = ptr(value.used)
			}
			if value.processKnown && (measurement.ProcessPeakBytes == nil || *measurement.ProcessPeakBytes < value.process) {
				measurement.ProcessPeakBytes = ptr(value.process)
			}
			measurement.AvailableAfterBytes = ptr(policyMax(0, value.total-value.used))
			measurement.ExternalContention = measurement.ExternalContention || value.external
			measurement.OwnershipUnknown = measurement.OwnershipUnknown || value.ownershipUnknown
			measurement.Samples++
		} else {
			total, available := hostCapacity()
			if total <= 0 {
				return
			}
			processes := stageProcesses(directory)
			rss := int64(0)
			for _, v := range processes {
				rss += v
			}
			mu.Lock()
			defer mu.Unlock()
			measurement.Scope = "sampled-owned-process-rss-and-host-or-cgroup"
			measurement.HostTotalBytes = ptr(total)
			if measurement.HostAvailableBeforeBytes == nil {
				measurement.HostAvailableBeforeBytes = ptr(available)
			}
			if measurement.HostMinimumAvailableBytes == nil || available < *measurement.HostMinimumAvailableBytes {
				measurement.HostMinimumAvailableBytes = ptr(available)
			}
			if len(processes) > 0 && (measurement.ProcessPeakBytes == nil || rss > *measurement.ProcessPeakBytes) {
				measurement.ProcessPeakBytes = ptr(rss)
			}
			measurement.Samples++
		}
	}
	sample()
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-sampleCtx.Done():
				return
			case <-ticker.C:
				sample()
			}
		}
	}()
	return func() models.StageMeasurements {
		cancel()
		<-done
		mu.Lock()
		defer mu.Unlock()
		measurement.ElapsedSeconds = time.Since(started).Seconds()
		return measurement
	}
}

func workloadClass(inputDuration float64, channels int) string {
	for _, bound := range []int{30, 120, 600, 1800, 3600, 7200, 14400} {
		if inputDuration <= float64(bound) {
			return fmt.Sprintf("duration-%d-channels-%d", bound, policyMax(1, channels))
		}
	}
	return fmt.Sprintf("duration-over-14400-channels-%d", policyMax(1, channels))
}
