package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/UseTrace-SI/trace-cli/internal/manifest"
	"github.com/UseTrace-SI/trace-cli/internal/redact"
	"github.com/UseTrace-SI/trace-cli/internal/syncclient"
	"github.com/spf13/cobra"
)

var cloudIDPattern = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
var cloudSecretPart = regexp.MustCompile(`(?i)^(?:\.git|\.trace(?:-brain|-graph)?|\.ssh|\.aws|\.codex|\.agents|node_modules|vendor|\.env(?:\..*)?|credentials(?:\..*)?|secrets?(?:\..*)?|id_(?:rsa|ed25519)(?:\.pub)?)$`)
var cloudSecretSuffix = regexp.MustCompile(`(?i)\.(?:pem|key|p12|pfx|keystore)$`)

// Reject symbolic links in every component and verify each opened identity
// against Lstat before reading. Root resolves links itself on some platforms,
// so O_NOFOLLOW alone is insufficient to enforce this upload policy.
func openCloudFile(anchor *os.Root, rel string) (*os.File, error) {
	parts := strings.Split(rel, "/")
	current := anchor
	defer func() {
		if current != anchor {
			current.Close()
		}
	}()
	for _, part := range parts[:len(parts)-1] {
		info, err := current.Lstat(part)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("upload directory must not be a symlink")
		}
		next, err := current.OpenRoot(part)
		if err != nil {
			return nil, err
		}
		nextInfo, err := next.Stat(".")
		if err != nil || !os.SameFile(info, nextInfo) {
			next.Close()
			return nil, fmt.Errorf("upload directory changed while opening")
		}
		if current != anchor {
			current.Close()
		}
		current = next
	}
	leaf := parts[len(parts)-1]
	expected, err := current.Lstat(leaf)
	if err != nil || !expected.Mode().IsRegular() {
		return nil, fmt.Errorf("upload must be a regular file without symlinks")
	}
	file, err := current.OpenFile(leaf, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	actual, err := file.Stat()
	if err != nil || !os.SameFile(expected, actual) {
		file.Close()
		return nil, fmt.Errorf("upload file changed while opening")
	}
	return file, nil
}

type cloudFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}
type cloudInput struct {
	ProjectID string      `json:"project_id"`
	RequestID string      `json:"request_id"`
	Command   []string    `json:"command"`
	Files     []cloudFile `json:"files"`
	Timeout   int         `json:"timeout_seconds"`
}

func cloudFiles(root string, paths []string) ([]cloudFile, int, error) {
	if len(paths) > 128 {
		return nil, 0, fmt.Errorf("at most 128 files can be uploaded")
	}
	anchor, err := os.OpenRoot(root)
	if err != nil {
		return nil, 0, err
	}
	defer anchor.Close()
	files := []cloudFile{}
	seen := map[string]bool{}
	total := 0
	for _, path := range paths {
		if filepath.IsAbs(path) {
			return nil, 0, fmt.Errorf("include paths must be repository-relative")
		}
		rel := filepath.ToSlash(filepath.Clean(path))
		if !utf8.ValidString(rel) || len(rel) > 512 || rel == "." || rel == ".." || strings.HasPrefix(rel, "../") || strings.ContainsAny(rel, "\\:") || strings.ContainsFunc(rel, func(r rune) bool { return r < 32 || r == 127 }) {
			return nil, 0, fmt.Errorf("unsafe include path")
		}
		if cloudSecretSuffix.MatchString(rel) {
			return nil, 0, fmt.Errorf("file is excluded from cloud jobs")
		}
		for _, part := range strings.Split(rel, "/") {
			if cloudSecretPart.MatchString(part) {
				return nil, 0, fmt.Errorf("file is excluded from cloud jobs")
			}
		}
		if seen[rel] {
			return nil, 0, fmt.Errorf("duplicate include path")
		}
		seen[rel] = true
		excluded, err := manifest.CaptureExcluded(root, rel)
		if err != nil {
			return nil, 0, err
		}
		if excluded {
			return nil, 0, fmt.Errorf("file is excluded by capture policy: %s", rel)
		}
		files = append(files, cloudFile{Path: rel})
	}
	if err := checkCloudIgnored(root, files); err != nil {
		return nil, 0, err
	}
	for index, item := range files {
		rel := item.Path
		resolved, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return nil, 0, err
		}
		if resolved != filepath.Join(root, filepath.FromSlash(rel)) {
			return nil, 0, fmt.Errorf("symlink paths cannot be uploaded")
		}
		// Pin parent identities and compare the regular leaf before reading.
		file, err := openCloudFile(anchor, rel)
		if err != nil {
			return nil, 0, err
		}
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			file.Close()
			return nil, 0, fmt.Errorf("include must name a regular file")
		}
		data, err := io.ReadAll(io.LimitReader(file, 128*1024+1))
		file.Close()
		if err != nil {
			return nil, 0, err
		}
		if len(data) > 128*1024 {
			return nil, 0, fmt.Errorf("included file exceeds 128 KiB")
		}
		total += len(data)
		if total > 512*1024 {
			return nil, 0, fmt.Errorf("included files exceed 512 KiB")
		}
		files[index].Content = base64.StdEncoding.EncodeToString(data)
	}
	return files, total, nil
}

func checkCloudIgnored(root string, files []cloudFile) error {
	if len(files) > 0 {
		var input strings.Builder
		for _, file := range files {
			input.WriteString(file.Path)
			input.WriteByte(0)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		process := exec.CommandContext(ctx, "git", "-C", root, "check-ignore", "--no-index", "-z", "--stdin")
		process.Stdin = strings.NewReader(input.String())
		var out boundedOutput
		process.Stdout = &out
		process.Stderr = io.Discard
		err := process.Run()
		var exit *exec.ExitError
		if err != nil && (!errors.As(err, &exit) || exit.ExitCode() != 1) {
			return fmt.Errorf("checking ignored upload paths: %w", err)
		}
		if out.truncated || out.data.Len() > 0 {
			return fmt.Errorf("gitignored files cannot be uploaded")
		}
	}
	return nil
}

func validateCloudConfig(cfg syncclient.Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	u, _ := url.Parse(cfg.Endpoint)
	if cfg.Mode != "personal" || (u.Path != "" && u.Path != "/") {
		return fmt.Errorf("cloud execution requires a personal connection to a plain HTTPS origin")
	}
	return nil
}
func cloudRequest(ctx context.Context, cfg syncclient.Config, token, method, path string, body any, client *http.Client) (json.RawMessage, error) {
	if err := validateCloudConfig(cfg); err != nil {
		return nil, err
	}
	if !regexp.MustCompile(`^trace_[A-Za-z0-9_-]{43}$`).MatchString(token) {
		return nil, fmt.Errorf("cloud execution requires a personal connection credential")
	}
	if !strings.HasPrefix(path, "/api/runner/jobs") {
		return nil, fmt.Errorf("invalid executor API path")
	}
	var payload []byte
	var err error
	if body != nil {
		payload, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(cfg.Endpoint, "/")+path, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	safe := *client
	safe.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := safe.Do(request)
	if err != nil {
		return nil, fmt.Errorf("executor request failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if response.StatusCode == http.StatusForbidden {
			return nil, fmt.Errorf("execution permission denied; approve this device for isolated NUC jobs in the dashboard")
		}
		return nil, fmt.Errorf("executor returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 1<<20 || !json.Valid(data) {
		return nil, fmt.Errorf("invalid or oversized executor response")
	}
	return json.RawMessage(data), nil
}

func cloudRunCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	var project string
	var requestID string
	var includes []string
	var timeout int
	var dryRun bool
	cmd := &cobra.Command{Use: "cloud-run --project ID [--include FILE] -- COMMAND [ARGS...]", Short: "Submit explicitly selected files to an approved isolated NUC job", SilenceUsage: true, Args: cobra.MinimumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if !cloudIDPattern.MatchString(project) {
			return fmt.Errorf("--project requires the dashboard project ID")
		}
		if requestID != "" && !cloudIDPattern.MatchString(requestID) {
			return fmt.Errorf("--request-id must be a UUID")
		}
		if timeout < 1 || timeout > 600 {
			return fmt.Errorf("timeout must be 1–600 seconds")
		}
		if len(args) > 32 {
			return fmt.Errorf("at most 32 command arguments are supported")
		}
		length := 0
		for _, arg := range args {
			if len(arg) > 4096 || strings.ContainsRune(arg, 0) {
				return fmt.Errorf("invalid command argument")
			}
			if (redact.Redactor{}).Text(arg) != arg {
				return fmt.Errorf("credentials cannot be included in job command arguments")
			}
			length += len(arg)
		}
		if length > 8192 || args[0] == "" {
			return fmt.Errorf("command exceeds the argument budget")
		}
		root, err := rootFromFlag(*repoPath)
		if err != nil {
			return err
		}
		root, err = filepath.EvalSymlinks(root)
		if err != nil {
			return err
		}
		files, total, err := cloudFiles(root, includes)
		if err != nil {
			return err
		}
		if dryRun {
			paths := []string{}
			for _, file := range files {
				paths = append(paths, file.Path)
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"project_id": project, "command": args, "files": paths, "bytes": total, "timeout_seconds": timeout, "submitted": false})
		}
		cfg, err := readSyncConfig(root)
		if err != nil {
			return err
		}
		if err := validateCloudConfig(cfg); err != nil {
			return err
		}
		provider, err := syncTokenProvider(cfg)
		if err != nil {
			return err
		}
		token, err := provider(cmd.Context())
		if err != nil {
			return err
		}
		if requestID == "" {
			var raw [16]byte
			if _, err := rand.Read(raw[:]); err != nil {
				return err
			}
			raw[6] = (raw[6] & 15) | 64
			raw[8] = (raw[8] & 63) | 128
			requestID = fmt.Sprintf("%x-%x-%x-%x-%x", raw[:4], raw[4:6], raw[6:8], raw[8:10], raw[10:])
		}
		body, err := cloudRequest(cmd.Context(), cfg, token, http.MethodPost, "/api/runner/jobs", cloudInput{ProjectID: project, RequestID: requestID, Command: args, Files: files, Timeout: timeout}, nil)
		if err != nil {
			return fmt.Errorf("job request %s: %w; retry identical inputs with --request-id %s to avoid a duplicate job", requestID, err, requestID)
		}
		return printCloudResponse(cmd, *jsonOutput, body)
	}}
	cmd.Flags().StringVar(&project, "project", "", "dashboard project ID (required)")
	cmd.Flags().StringVar(&requestID, "request-id", "", "reuse a previous submission UUID when retrying identical inputs")
	cmd.Flags().StringArrayVar(&includes, "include", nil, "explicit regular file to upload (repeatable; no implicit project upload)")
	cmd.Flags().IntVar(&timeout, "timeout", 120, "job time budget in seconds (1–600)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show selected files and byte totals without accessing credentials or the network")
	for _, action := range []string{"status", "cancel", "forget", "list"} {
		action := action
		child := &cobra.Command{Use: action + " <job-id>", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			if action == "list" && !cloudIDPattern.MatchString(project) {
				return fmt.Errorf("--project requires the dashboard project ID")
			}
			if action != "list" && !cloudIDPattern.MatchString(args[0]) {
				return fmt.Errorf("invalid job ID")
			}
			root, err := rootFromFlag(*repoPath)
			if err != nil {
				return err
			}
			cfg, err := readSyncConfig(root)
			if err != nil {
				return err
			}
			if err := validateCloudConfig(cfg); err != nil {
				return err
			}
			provider, err := syncTokenProvider(cfg)
			if err != nil {
				return err
			}
			token, err := provider(cmd.Context())
			if err != nil {
				return err
			}
			method := http.MethodGet
			path := "/api/runner/jobs"
			if action == "list" {
				path += "?project=" + url.QueryEscape(project)
			} else {
				path += "/" + args[0]
			}
			if action == "cancel" || action == "forget" {
				method = http.MethodDelete
			}
			if action == "forget" {
				path += "?purge=true"
			}
			body, err := cloudRequest(cmd.Context(), cfg, token, method, path, nil, nil)
			if err != nil {
				return err
			}
			return printCloudResponse(cmd, *jsonOutput, body)
		}}
		if action == "list" {
			child.Use = "list --project ID"
			child.Args = cobra.NoArgs
			child.Flags().StringVar(&project, "project", "", "dashboard project ID (required)")
		}
		if action == "forget" {
			child.Short = "Delete the hosted job inputs/results and revoke an active lease"
		}
		cmd.AddCommand(child)
	}
	return cmd
}

func printCloudResponse(cmd *cobra.Command, jsonOutput bool, body json.RawMessage) error {
	if jsonOutput {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(body)
	}
	text := (redact.Redactor{}).Text(string(body))
	text = strings.Map(func(r rune) rune {
		if (unicode.IsControl(r) && r != '\n' && r != '\t') || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, text)
	_, err := fmt.Fprintln(cmd.OutOrStdout(), text)
	return err
}
