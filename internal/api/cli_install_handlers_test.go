package api

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInstallScriptInstallsOnlyJotist(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("installer requires a Unix shell")
	}
	bash, err := exec.LookPath("bash")
	require.NoError(t, err)
	for _, tc := range []struct {
		name, argument, jotistURL, expectedURL string
	}{
		{"argument", "https://argument.example", "https://jotist.example", "https://argument.example"},
		{"Jotist environment", "", "https://jotist.example", "https://jotist.example"},
		{"legacy environment ignored", "", "", "http://localhost:8080"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			toolsDir := filepath.Join(dir, "tools")
			installDir := filepath.Join(dir, "install")
			tmpDir := filepath.Join(dir, "tmp")
			for _, path := range []string{toolsDir, installDir, tmpDir} {
				require.NoError(t, os.Mkdir(path, 0700))
			}
			fixture := filepath.Join(dir, "downloaded-cli")
			require.NoError(t, os.WriteFile(fixture, []byte("#!/bin/sh\nprintf 'Jotist CLI\\n'\n"), 0700))
			require.NoError(t, os.WriteFile(filepath.Join(toolsDir, "uname"), []byte("#!/bin/sh\ncase \"$1\" in -s) echo Linux ;; -m) echo x86_64 ;; esac\n"), 0700))
			require.NoError(t, os.WriteFile(filepath.Join(toolsDir, "curl"), []byte(`#!/bin/sh
while [ "$#" -gt 0 ]; do
    case "$1" in
        --output) shift; target="$1" ;;
        --) shift; printf '%s' "$1" > "$CLI_TEST_DOWNLOAD_URL" ;;
    esac
    shift
done
cp "$CLI_TEST_BINARY" "$target"
`), 0700))
			downloadURL := filepath.Join(dir, "download-url")
			args := []string{"-s", "--"}
			if tc.argument != "" {
				args = append(args, tc.argument)
			}
			cmd := exec.Command(bash, args...)
			cmd.Stdin = strings.NewReader(installScript)
			cmd.Env = append(os.Environ(),
				"PATH="+toolsDir+string(os.PathListSeparator)+os.Getenv("PATH"),
				"JOTIST_INSTALL_DIR="+installDir,
				"JOTIST_SERVER_URL="+tc.jotistURL,
				"SCRIBERR_SERVER_URL=https://legacy.example",
				"CLI_TEST_BINARY="+fixture,
				"CLI_TEST_DOWNLOAD_URL="+downloadURL,
				"TMPDIR="+tmpDir,
			)
			output, err := cmd.CombinedOutput()
			require.NoError(t, err, string(output))
			require.Contains(t, string(output), "jotist login --server "+tc.expectedURL)
			url, err := os.ReadFile(downloadURL)
			require.NoError(t, err)
			require.Equal(t, tc.expectedURL+"/api/v1/cli/download?os=linux&arch=amd64", string(url))
			files, err := os.ReadDir(installDir)
			require.NoError(t, err)
			require.Len(t, files, 1)
			require.Equal(t, "jotist", files[0].Name())
			output, err = exec.Command(filepath.Join(installDir, "jotist"), "--help").CombinedOutput()
			require.NoError(t, err)
			require.Equal(t, "Jotist CLI\n", string(output))
			files, err = os.ReadDir(tmpDir)
			require.NoError(t, err)
			require.Empty(t, files)
		})
	}
}
