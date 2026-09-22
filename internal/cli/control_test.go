package cli

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestControlCommandsUseAuthenticatedRecoveryAndQueueRoutes(t *testing.T) {
	for _, tc := range []struct {
		args               []string
		method, path, body string
	}{
		{[]string{"runs", "recovery", "job", "run"}, "GET", "/api/v1/transcription/job/runs/run/recovery", ""},
		{[]string{"runs", "resume", "job", "run"}, "POST", "/api/v1/transcription/job/runs/run/resume", ""},
		{[]string{"queue", "add", "job", "--body", "-"}, "POST", "/api/v1/transcription/job/queue", `{"profile_id":"saved","parameters":{"device":"cpu","recovery_mode":"fixed","hf_token_source":"default"}}`},
		{[]string{"profiles", "freeze-adaptive", "profile", "--body", "-"}, "POST", "/api/v1/profiles/profile/freeze-adaptive", `{"plan_id":"plan","expected_revision":2,"expected_generation":1}`},
		{[]string{"settings", "update", "--body", "-"}, "PUT", "/api/v1/user/settings", `{"hf_token":"private-token"}`},
	} {
		t.Run(tc.path+tc.method, func(t *testing.T) {
			called := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				require.Equal(t, tc.method, r.Method)
				require.Equal(t, tc.path, r.URL.Path)
				require.Equal(t, "Bearer test-login", r.Header.Get("Authorization"))
				body, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				require.Equal(t, tc.body, string(body))
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer server.Close()
			viper.Set("server_url", server.URL)
			viper.Set("token", "test-login")
			defer viper.Reset()
			root := &cobra.Command{Use: "test", SilenceUsage: true, SilenceErrors: true}
			root.AddCommand(controlCommands()...)
			var output bytes.Buffer
			root.SetOut(&output)
			root.SetIn(bytes.NewBufferString(tc.body))
			root.SetArgs(tc.args)
			require.NoError(t, root.Execute())
			require.True(t, called)
			require.NotContains(t, output.String(), "private-token")
		})
	}
}

func TestControlRequestRejectsExternalURLAndRedirect(t *testing.T) {
	foreignCalls := 0
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { foreignCalls++ }))
	defer foreign.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, foreign.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	cfg := &Config{ServerURL: server.URL, Token: "private-login"}
	for _, path := range []string{foreign.URL, "//example.org/api/v1/models", "/outside"} {
		_, err := controlRequest(context.Background(), cfg, "GET", path, nil)
		require.Error(t, err)
	}
	_, err := controlRequest(context.Background(), cfg, "GET", "/api/v1/transcription/models", nil)
	require.ErrorContains(t, err, "HTTP 307")
	require.Zero(t, foreignCalls)
}
