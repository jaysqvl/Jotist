//go:build linux

package processutil

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Limit samples to descendants of this exact worker. PID start times prevent
// reuse from being mistaken for continued work. No command lines are collected.
func readWorkerActivity(root int) activityReading {
	result := activityReading{counters: map[int]activityCounters{}}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return result
	}
	type row struct {
		parent int
		state  string
		data   activityCounters
	}
	all := map[int]row{}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "stat"))
		if err != nil {
			continue
		}
		value := string(data)
		end := strings.LastIndexByte(value, ')')
		if end < 0 {
			continue
		}
		fields := strings.Fields(value[end+1:])
		if len(fields) < 20 {
			continue
		}
		parent, e1 := strconv.Atoi(fields[1])
		user, e2 := strconv.ParseUint(fields[11], 10, 64)
		system, e3 := strconv.ParseUint(fields[12], 10, 64)
		start, e4 := strconv.ParseUint(fields[19], 10, 64)
		if e1 != nil || e2 != nil || e3 != nil || e4 != nil {
			continue
		}
		all[pid] = row{parent: parent, state: fields[0], data: activityCounters{start: start, cpu: user + system}}
	}
	if _, found := all[root]; !found {
		return result
	}
	result.known = true
	for pid, process := range all {
		owned := pid == root
		for ancestor, depth := pid, 0; !owned && depth < 64; depth++ {
			value, found := all[ancestor]
			if !found || value.parent == ancestor || value.parent <= 1 {
				break
			}
			ancestor = value.parent
			owned = ancestor == root
		}
		if !owned {
			continue
		}
		result.busy = result.busy || process.state == "R"
		result.starved = result.starved || process.state == "D"
		base := filepath.Join("/proc", strconv.Itoa(pid))
		if data, err := os.ReadFile(filepath.Join(base, "io")); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				fields := strings.Fields(line)
				if len(fields) == 2 && (fields[0] == "read_bytes:" || fields[0] == "write_bytes:") {
					value, _ := strconv.ParseUint(fields[1], 10, 64)
					process.data.io += value
				}
			}
		} else if _, err := os.Stat(base); err == nil {
			result.known = false
		}
		if data, err := os.ReadFile(filepath.Join(base, "schedstat")); err == nil {
			fields := strings.Fields(string(data))
			if len(fields) >= 2 {
				process.data.delay, _ = strconv.ParseUint(fields[1], 10, 64)
			}
		}
		fds, err := os.ReadDir(filepath.Join(base, "fd"))
		if err != nil && process.state != "Z" {
			result.known = false
		}
		for _, fd := range fds {
			target, _ := os.Readlink(filepath.Join(base, "fd", fd.Name()))
			result.gpu = result.gpu || strings.HasPrefix(target, "/dev/nvidia") || strings.HasPrefix(target, "/dev/dri/")
		}
		result.counters[pid] = process.data
	}
	return result
}
