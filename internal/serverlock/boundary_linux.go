//go:build linux

package serverlock

import (
	"os"
	"strings"
)

func processBoundary() string {
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ""
	}
	namespace, err := os.Readlink("/proc/1/ns/pid")
	if err != nil {
		return ""
	}
	stat, err := os.ReadFile("/proc/1/stat")
	if err != nil {
		return ""
	}
	index := strings.LastIndex(string(stat), ") ")
	if index < 0 {
		return ""
	}
	fields := strings.Fields(string(stat)[index+2:])
	if len(fields) <= 19 {
		return ""
	}
	return strings.TrimSpace(string(boot)) + "/" + namespace + "/" + fields[19]
}
