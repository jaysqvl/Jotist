package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// Control requests use the same user login as uploads, so saved account
// defaults (including the HF token) are resolved by the server at admission.
func controlRequest(ctx context.Context, cfg *Config, method, path string, body []byte) ([]byte, error) {
	if cfg.ServerURL == "" || cfg.Token == "" {
		return nil, fmt.Errorf("run 'scriberr login --server URL' first")
	}
	base, err := url.Parse(cfg.ServerURL)
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") || base.User != nil {
		return nil, fmt.Errorf("configured server URL is invalid")
	}
	rel, err := url.Parse(path)
	if err != nil || rel.IsAbs() || rel.Host != "" || !strings.HasPrefix(rel.Path, "/api/v1/") || rel.Fragment != "" {
		return nil, fmt.Errorf("API path must begin with /api/v1/ on the configured server")
	}
	base.Path = strings.TrimRight(base.Path, "/") + rel.Path
	base.RawPath, base.RawQuery, base.Fragment = "", rel.RawQuery, ""
	req, err := http.NewRequestWithContext(ctx, method, base.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	setCLIAuth(req, cfg)
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	const limit = 32 << 20 // Transcripts and recovery views can exceed upload responses.
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, fmt.Errorf("server response exceeds 32 MiB")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("server returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return data, nil
}

func controlBody(cmd *cobra.Command, path string) ([]byte, error) {
	if path == "" {
		return nil, nil
	}
	var reader io.Reader = cmd.InOrStdin()
	if path != "-" {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		reader = f
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxCLIResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxCLIResponseBytes || !json.Valid(data) {
		return nil, fmt.Errorf("request body must be valid JSON of at most 1 MiB")
	}
	return data, nil
}

func printControlResponse(cmd *cobra.Command, method, path string, body []byte) error {
	data, err := controlRequest(cmd.Context(), GetConfig(), method, path, body)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}
	var pretty bytes.Buffer
	if json.Indent(&pretty, data, "", "  ") == nil {
		data = pretty.Bytes()
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), string(data))
	return err
}

func endpointCommand(use, short, method, path string, count int, acceptsBody bool) *cobra.Command {
	var bodyFile string
	cmd := &cobra.Command{Use: use, Short: short, Args: cobra.ExactArgs(count)}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		endpoint := path
		for _, arg := range args {
			// IDs occupy exactly one path component; do not let an argument
			// turn a read command into another endpoint through path traversal.
			if arg == "" || arg == "." || arg == ".." || strings.ContainsAny(arg, "/\\?#%") {
				return fmt.Errorf("invalid resource ID")
			}
			endpoint = strings.Replace(endpoint, "{}", arg, 1)
		}
		body, err := controlBody(cmd, bodyFile)
		if err != nil {
			return err
		}
		return printControlResponse(cmd, method, "/api/v1"+endpoint, body)
	}
	if acceptsBody {
		cmd.Flags().StringVar(&bodyFile, "body", "", "JSON request file; use - to read stdin")
	}
	return cmd
}

func controlCommands() []*cobra.Command {
	models := endpointCommand("models", "List model capabilities, benchmark evidence and memory estimates", "GET", "/transcription/models", 0, false)
	jobs := &cobra.Command{Use: "jobs", Short: "Inspect recordings and cancel active work"}
	jobs.AddCommand(endpointCommand("list", "List recordings", "GET", "/transcription/list", 0, false),
		endpointCommand("status JOB", "Show recording status", "GET", "/transcription/{}/status", 1, false),
		endpointCommand("cancel JOB", "Stop an active transcription", "POST", "/transcription/{}/kill", 1, false))
	runs := &cobra.Command{Use: "runs", Short: "Inspect execution history, checkpoints, errors and resume controls"}
	runs.AddCommand(endpointCommand("list JOB", "List recording executions", "GET", "/transcription/{}/runs", 1, false))
	for _, resource := range []string{"recovery", "logs", "transcript"} {
		runs.AddCommand(endpointCommand(resource+" JOB RUN", "Read execution "+resource, "GET", "/transcription/{}/runs/{}/"+resource, 2, false))
	}
	runs.AddCommand(endpointCommand("resume JOB RUN", "Resume an eligible failed execution from compatible checkpoints", "POST", "/transcription/{}/runs/{}/resume", 2, false))
	queue := &cobra.Command{Use: "queue", Short: "Queue saved profiles or explicit model and recovery parameters"}
	queue.AddCommand(endpointCommand("list JOB", "List queued runs and terminal history", "GET", "/transcription/{}/queue?include_terminal=true", 1, false),
		endpointCommand("add JOB", "Queue a run; --body accepts profile_id and/or parameters", "POST", "/transcription/{}/queue", 1, true),
		endpointCommand("cancel JOB ITEM", "Cancel one queued run", "DELETE", "/transcription/{}/queue/{}", 2, false),
		endpointCommand("order JOB", "Reorder waiting runs with an ordered_ids JSON body", "PUT", "/transcription/{}/queue/order", 1, true),
		endpointCommand("clear JOB", "Clear waiting runs", "DELETE", "/transcription/{}/queue", 1, false))
	profiles := &cobra.Command{Use: "profiles", Short: "Manage profiles, adaptive policies and saved revisions"}
	profiles.AddCommand(endpointCommand("list", "List profiles", "GET", "/profiles/", 0, false),
		endpointCommand("get PROFILE", "Read a profile", "GET", "/profiles/{}", 1, false),
		endpointCommand("create", "Create a profile from --body JSON", "POST", "/profiles/", 0, true),
		endpointCommand("update PROFILE", "Update a profile from --body JSON", "PUT", "/profiles/{}", 1, true),
		endpointCommand("delete PROFILE", "Delete a profile", "DELETE", "/profiles/{}", 1, false),
		endpointCommand("default PROFILE", "Select the default profile", "POST", "/profiles/{}/set-default", 1, false),
		endpointCommand("policy PROFILE", "Read adaptive policy, observations and revisions", "GET", "/profiles/{}/adaptive-policy", 1, false))
	for _, action := range []string{"reset-adaptive", "freeze-adaptive", "restore-revision"} {
		profiles.AddCommand(endpointCommand(action+" PROFILE", "Apply "+action+" using --body JSON", "POST", "/profiles/{}/"+action, 1, true))
	}
	settings := &cobra.Command{Use: "settings", Short: "Read and update account defaults, including the Hugging Face token"}
	settings.AddCommand(endpointCommand("get", "Read settings (tokens remain hidden)", "GET", "/user/settings", 0, false),
		endpointCommand("update", "Update defaults from --body JSON; use stdin to keep tokens out of arguments", "PUT", "/user/settings", 0, true))
	var bodyFile string
	api := &cobra.Command{Use: "api METHOD PATH", Short: "Call any API endpoint on the configured server", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		method := strings.ToUpper(args[0])
		switch method {
		case "GET", "POST", "PUT", "PATCH", "DELETE":
		default:
			return fmt.Errorf("unsupported HTTP method")
		}
		body, err := controlBody(cmd, bodyFile)
		if err != nil {
			return err
		}
		return printControlResponse(cmd, method, args[1], body)
	}}
	api.Flags().StringVar(&bodyFile, "body", "", "JSON request file; use - to read stdin")
	return []*cobra.Command{models, jobs, runs, queue, profiles, settings, api}
}

func init() { rootCmd.AddCommand(controlCommands()...) }
