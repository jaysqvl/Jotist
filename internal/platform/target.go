// Package platform owns installation defaults, independent of browser hardware.
package platform

import "runtime"

type Target struct {
	OS            string `json:"os"`
	Architecture  string `json:"architecture"`
	DefaultDevice string `json:"default_device"`
}

// For preserves existing defaults except for the Linux ARM64 CPU target.
// It does not rewrite explicit operator settings or saved run parameters.
func For(goos, architecture string) Target {
	device := "cuda"
	if goos == "linux" && architecture == "arm64" {
		device = "cpu"
	}
	return Target{OS: goos, Architecture: architecture, DefaultDevice: device}
}

func Host() Target { return For(runtime.GOOS, runtime.GOARCH) }
