package platform

import (
	"runtime"
	"testing"
)

func TestInstallationDefaultsAreScopedToLinuxARM64(t *testing.T) {
	for _, tc := range []struct{ os, arch, device string }{
		{"linux", "arm64", "cpu"}, {"linux", "amd64", "cuda"},
		{"darwin", "arm64", "cuda"}, {"darwin", "amd64", "cuda"},
		{"windows", "amd64", "cuda"},
	} {
		got := For(tc.os, tc.arch)
		if got.DefaultDevice != tc.device || got.OS != tc.os || got.Architecture != tc.arch {
			t.Fatalf("unexpected platform defaults for %s/%s", tc.os, tc.arch)
		}
	}
	if Host() != For(runtime.GOOS, runtime.GOARCH) {
		t.Fatal("defaults must describe the server, not a client hint")
	}
}
