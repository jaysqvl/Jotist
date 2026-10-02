package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func resetTestConfig(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, name := range []string{"JOTIST_SERVER_URL", "JOTIST_TOKEN", "JOTIST_WATCH_FOLDER", "SCRIBERR_SERVER_URL", "SCRIBERR_TOKEN", "SCRIBERR_WATCH_FOLDER"} {
		t.Setenv(name, "")
	}
	previousFile := cfgFile
	cfgFile = ""
	viper.Reset()
	t.Cleanup(func() {
		cfgFile = previousFile
		viper.Reset()
	})
	return home
}

func TestConfigUsesJotistOrExplicitFile(t *testing.T) {
	for _, filename := range []string{".jotist.yaml", "custom.yaml"} {
		t.Run(filename, func(t *testing.T) {
			home := resetTestConfig(t)
			path := filepath.Join(home, filename)
			require.NoError(t, os.WriteFile(path, []byte("server_url: https://jotist.example\ntoken: current-login\nwatch_folder: /recordings\n"), 0600))
			if filename == "custom.yaml" {
				cfgFile = path
			}
			InitConfig()
			require.Equal(t, &Config{ServerURL: "https://jotist.example", Token: "current-login", WatchFolder: "/recordings"}, GetConfig())

			saved, err := SaveConfig("https://updated.example", "updated-login", "")
			require.NoError(t, err)
			require.Equal(t, path, saved)
			stored := viper.New()
			stored.SetConfigFile(path)
			require.NoError(t, stored.ReadInConfig())
			require.Equal(t, "https://updated.example", stored.GetString("server_url"))
			require.Equal(t, "updated-login", stored.GetString("token"))
			require.Equal(t, "/recordings", stored.GetString("watch_folder"))
			info, err := os.Stat(path)
			require.NoError(t, err)
			require.Equal(t, os.FileMode(0600), info.Mode().Perm())
		})
	}
}

func TestConfigDoesNotUseLegacyFileOrEnvironment(t *testing.T) {
	home := resetTestConfig(t)
	legacyPath := filepath.Join(home, ".scriberr.yaml")
	legacy := []byte("server_url: https://legacy.example\ntoken: legacy-login\nwatch_folder: /legacy-recordings\n")
	require.NoError(t, os.WriteFile(legacyPath, legacy, 0600))
	t.Setenv("SCRIBERR_SERVER_URL", "https://legacy-env.example")
	t.Setenv("SCRIBERR_TOKEN", "legacy-env-login")
	t.Setenv("SCRIBERR_WATCH_FOLDER", "/legacy-env-recordings")
	InitConfig()
	require.Equal(t, &Config{}, GetConfig())
	saved, err := SaveConfig("https://jotist.example", "jotist-login", "/recordings")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(home, ".jotist.yaml"), saved)
	unchanged, err := os.ReadFile(legacyPath)
	require.NoError(t, err)
	require.Equal(t, legacy, unchanged)
}

func TestJotistEnvironmentOverridesSavedConfig(t *testing.T) {
	home := resetTestConfig(t)
	require.NoError(t, os.WriteFile(filepath.Join(home, ".jotist.yaml"), []byte("server_url: https://saved.example\ntoken: saved-login\nwatch_folder: /saved-recordings\n"), 0600))
	t.Setenv("JOTIST_SERVER_URL", "https://jotist.example")
	t.Setenv("JOTIST_TOKEN", "jotist-login")
	t.Setenv("JOTIST_WATCH_FOLDER", "/jotist-recordings")
	InitConfig()
	require.Equal(t, &Config{ServerURL: "https://jotist.example", Token: "jotist-login", WatchFolder: "/jotist-recordings"}, GetConfig())
}

func TestJotistWatcherIdentityAndSelectedConfig(t *testing.T) {
	config := getServiceConfig("/private/config.yaml")
	require.Equal(t, "jotist-watcher", config.Name)
	require.Equal(t, "Jotist Watcher Service", config.DisplayName)
	require.Equal(t, []string{"service-run", "--config", "/private/config.yaml"}, config.Arguments)
	require.Equal(t, filepath.Join(os.TempDir(), "jotist-service.log"), getLogFilePath())
}
