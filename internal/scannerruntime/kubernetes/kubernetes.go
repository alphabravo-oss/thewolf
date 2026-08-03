package kubernetes

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	scannercontainer "github.com/alphabravocompany/thewolf/internal/plugin/container"
	"github.com/alphabravocompany/thewolf/internal/scannerruntime"
)

type Config struct {
	APIServer       string
	Namespace       string
	Token           string
	CAFile          string
	WorkspacePVC    string
	WorkspaceRoot   string
	WolfImage       string
	ServiceAccount  string
	PollInterval    time.Duration
	JobTTLSeconds   int
	DefaultTimeout  time.Duration
	MaxOutputBytes  int64
	NetworkClass    string
	ImagePullPolicy string
}

const DefaultScannerOutputMaxBytes int64 = 16 << 20

func ConfigFromEnv() (Config, error) {
	cfg := Config{
		APIServer:       envOr("WOLF_K8S_API_SERVER", "https://kubernetes.default.svc"),
		Namespace:       strings.TrimSpace(os.Getenv("WOLF_K8S_NAMESPACE")),
		CAFile:          envOr("WOLF_K8S_CA_FILE", "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"),
		WorkspacePVC:    strings.TrimSpace(os.Getenv("WOLF_K8S_WORKSPACE_PVC")),
		WorkspaceRoot:   envOr("WOLF_WORKSPACE_ROOT", "/workspace"),
		WolfImage:       strings.TrimSpace(os.Getenv("WOLF_K8S_WOLF_IMAGE")),
		ServiceAccount:  envOr("WOLF_K8S_SCANNER_SERVICE_ACCOUNT", "wolf-scanner"),
		PollInterval:    time.Second,
		JobTTLSeconds:   envInt("WOLF_K8S_JOB_TTL_SECONDS", 300),
		DefaultTimeout:  envDuration("WOLF_K8S_SCANNER_TIMEOUT", 30*time.Minute),
		MaxOutputBytes:  int64(envInt("WOLF_K8S_SCANNER_OUTPUT_MAX_BYTES", int(DefaultScannerOutputMaxBytes))),
		NetworkClass:    envOr("WOLF_K8S_NETWORK_CLASS", "offline"),
		ImagePullPolicy: envOr("WOLF_K8S_IMAGE_PULL_POLICY", "IfNotPresent"),
	}
	if cfg.Namespace == "" {
		data, _ := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
		cfg.Namespace = strings.TrimSpace(string(data))
	}
	if cfg.Namespace == "" {
		cfg.Namespace = "default"
	}
	cfg.Token = strings.TrimSpace(os.Getenv("WOLF_K8S_BEARER_TOKEN"))
	if cfg.Token == "" {
		data, _ := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/token")
		cfg.Token = strings.TrimSpace(string(data))
	}
	if cfg.WorkspacePVC == "" {
		return Config{}, fmt.Errorf("WOLF_K8S_WORKSPACE_PVC is required")
	}
	if cfg.WolfImage == "" {
		return Config{}, fmt.Errorf("WOLF_K8S_WOLF_IMAGE is required")
	}
	if cfg.Token == "" {
		return Config{}, fmt.Errorf("Kubernetes service-account token is unavailable")
	}
	return cfg, nil
}

// CommandContext preserves plugins' exec.Cmd interface by delegating one
// invocation to the hidden scanner-job-exec command.
func CommandContext(ctx context.Context, cfg *scannercontainer.Config, opts scannercontainer.Options, tool string, args ...string) *exec.Cmd {
	invocation, err := invocationFromContainer(ctx, cfg, opts, tool, args...)
	if err != nil {
		return exec.CommandContext(ctx, "sh", "-c", "printf '%s\n' \"$1\" >&2; exit 127", "wolf-k8s-error", err.Error())
	}
	data, _ := json.Marshal(invocation)
	encoded := base64.RawURLEncoding.EncodeToString(data)
	executable, err := os.Executable()
	if err != nil {
		executable = "wolf"
	}
	cmd := exec.CommandContext(ctx, executable, "scanner-job-exec", "--invocation", encoded)
	if opts.Stdin != "" {
		cmd.Stdin = strings.NewReader(opts.Stdin)
	}
	return cmd
}

func invocationFromContainer(ctx context.Context, cfg *scannercontainer.Config, opts scannercontainer.Options, tool string, args ...string) (scannerruntime.Invocation, error) {
	if cfg == nil {
		return scannerruntime.Invocation{}, fmt.Errorf("scanner configuration is unavailable")
	}
	image := cfg.ImageFor(tool)
	if image == "" {
		return scannerruntime.Invocation{}, fmt.Errorf("scanner image is empty for %s", tool)
	}
	command := []string{"/usr/local/bin/wolf-tool-entry"}
	finalArgs := append([]string{tool}, args...)
	if spec, ok := cfg.UpstreamSpec(tool); ok {
		image = spec.Image
		target := spec.Entrypoint
		if target == "" {
			target = tool
		}
		command = []string{target}
		finalArgs = append([]string(nil), args...)
	}
	if opts.EntrypointOverride != "" {
		command = []string{opts.EntrypointOverride}
		finalArgs = append([]string(nil), args...)
	}
	environment := make(map[string]string, len(cfg.ExtraEnv)+len(opts.ExtraEnv))
	for key, value := range cfg.ExtraEnv {
		environment[key] = value
	}
	for key, value := range opts.ExtraEnv {
		environment[key] = value
	}
	mounts := []scannerruntime.Mount{}
	if !opts.NoRepoMount {
		if opts.RepoDir == "" {
			return scannerruntime.Invocation{}, fmt.Errorf("scanner repo path is empty")
		}
		mounts = append(mounts, scannerruntime.Mount{
			Source: opts.RepoDir, Target: scannercontainer.ScanMountPoint, ReadOnly: !opts.ReadWrite,
		})
	}
	workdir := opts.WorkDir
	if workdir == "" {
		if opts.NoRepoMount {
			workdir = "/tmp"
		} else {
			workdir = scannercontainer.ScanMountPoint
		}
	}
	identity := scannerruntime.IdentityFromContext(ctx)
	memory := cfg.Memory
	if opts.MemoryOverride != "" {
		memory = opts.MemoryOverride
	}
	invocationNetworkClass := identity.NetworkClass
	if invocationNetworkClass == "" {
		invocationNetworkClass = networkClass(cfg.Network)
	}
	return scannerruntime.Invocation{
		Image: image, Command: command, Args: finalArgs, WorkingDir: workdir,
		Environment: environment, Mounts: mounts, Stdin: opts.Stdin,
		Memory: memory, CPUs: cfg.CPUs, NetworkClass: invocationNetworkClass,
		ScanID: identity.ScanID, UserID: identity.UserID,
		LeaseToken: identity.LeaseToken, Attempt: identity.Attempt, ToolName: tool,
	}, nil
}

type Client struct {
	cfg  Config
	http *http.Client
}

func NewClient(cfg Config) (*Client, error) {
	pool, err := x509.SystemCertPool()
	if err != nil {
		pool = x509.NewCertPool()
	}
	if data, err := os.ReadFile(cfg.CAFile); err == nil {
		pool.AppendCertsFromPEM(data)
	}
	return &Client{
		cfg: cfg,
		http: &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12, RootCAs: pool,
		}}, Timeout: 30 * time.Second},
	}, nil
}

// ReconcileAbandonedJobs removes scanner Jobs whose scan/lease labels no
// longer identify an active queue claim. A database lookup error is treated
// conservatively: that Job is retained and the error is returned.
func ReconcileAbandonedJobs(
	ctx context.Context,
	cfg Config,
	isCurrent func(context.Context, string, string) (bool, error),
) (int, error) {
	if isCurrent == nil {
		return 0, fmt.Errorf("Kubernetes Job reconciliation requires an ownership callback")
	}
	client, err := NewClient(cfg)
	if err != nil {
		return 0, err
	}
	var list struct {
		Items []struct {
			Metadata struct {
				Name   string            `json:"name"`
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
		} `json:"items"`
	}
	selector := url.QueryEscape("app.kubernetes.io/name=wolf-scanner")
	if err := client.request(ctx, http.MethodGet,
		"/apis/batch/v1/namespaces/"+url.PathEscape(cfg.Namespace)+"/jobs?labelSelector="+selector,
		nil, &list); err != nil {
		return 0, err
	}
	deleted := 0
	var firstErr error
	for _, job := range list.Items {
		scanID := job.Metadata.Labels["wolf.dev/scan"]
		leaseToken := job.Metadata.Labels["wolf.dev/lease"]
		current, ownershipErr := isCurrent(ctx, scanID, leaseToken)
		if ownershipErr != nil {
			if firstErr == nil {
				firstErr = ownershipErr
			}
			continue
		}
		if current {
			continue
		}
		if err := client.deleteJobChecked(ctx, job.Metadata.Name); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		deleted++
	}
	return deleted, firstErr
}

// Execute creates one native Kubernetes Job, waits for terminal state, mirrors
// the wrapper's separate stdout/stderr files, and deletes the Job.
func Execute(ctx context.Context, cfg Config, invocation scannerruntime.Invocation, stdout, stderr io.Writer) int {
	client, err := NewClient(cfg)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	jobName, resultDir, err := client.createJob(ctx, invocation)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer client.deleteJob(context.Background(), jobName)
	defer func() { _ = os.RemoveAll(resultDir) }()
	if err := client.waitJob(ctx, jobName, invocation.Timeout); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	outputLimit := cfg.MaxOutputBytes
	if outputLimit <= 0 {
		outputLimit = DefaultScannerOutputMaxBytes
	}
	stdoutData, err := readBoundedResult(filepath.Join(resultDir, "stdout"), outputLimit)
	if err != nil {
		fmt.Fprintln(stderr, "read scanner Job stdout:", err)
		return 1
	}
	stderrData, err := readBoundedResult(filepath.Join(resultDir, "stderr"), outputLimit)
	if err != nil {
		fmt.Fprintln(stderr, "read scanner Job stderr:", err)
		return 1
	}
	exitData, err := readBoundedResult(filepath.Join(resultDir, "exit-code"), 16)
	if err != nil {
		fmt.Fprintln(stderr, "read scanner Job exit code:", err)
		return 1
	}
	_, _ = stdout.Write(stdoutData)
	_, _ = stderr.Write(stderrData)
	code, err := strconv.Atoi(strings.TrimSpace(string(exitData)))
	if err != nil || code < 0 || code > 255 {
		return 1
	}
	return code
}

func readBoundedResult(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("result exceeds %d-byte limit", limit)
	}
	return data, nil
}

func (c *Client) createJob(ctx context.Context, invocation scannerruntime.Invocation) (string, string, error) {
	workspaceRoot, err := filepath.Abs(c.cfg.WorkspaceRoot)
	if err != nil {
		return "", "", err
	}
	var sourceSubPath string
	for _, mount := range invocation.Mounts {
		if mount.Target != scannercontainer.ScanMountPoint {
			continue
		}
		source, err := filepath.Abs(mount.Source)
		if err != nil {
			return "", "", err
		}
		rel, err := filepath.Rel(workspaceRoot, source)
		if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return "", "", fmt.Errorf("scanner source %s must be inside WOLF_WORKSPACE_ROOT %s", source, workspaceRoot)
		}
		sourceSubPath = filepath.ToSlash(rel)
	}
	scanLabel := dnsLabel(defaultValue(invocation.ScanID, "scan"))
	toolLabel := dnsLabel(invocation.ToolName)
	userLabel := dnsLabel(defaultValue(invocation.UserID, "unknown"))
	leaseLabel := dnsLabel(defaultValue(invocation.LeaseToken, "inline"))
	attemptLabel := strconv.Itoa(max(invocation.Attempt, 1))
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	jobName := truncateLabel(
		"wolf-"+shortLabel(scanLabel, 12)+"-"+shortLabel(toolLabel, 24)+"-"+suffix,
		63,
	)
	resultRel := filepath.ToSlash(filepath.Join(scanLabel, toolLabel+"-"+suffix))
	resultDir := filepath.Join(workspaceRoot, ".wolf-results", filepath.FromSlash(resultRel))
	if err := os.MkdirAll(resultDir, 0o770); err != nil {
		return "", "", fmt.Errorf("create scanner result directory: %w", err)
	}
	command := append([]string(nil), invocation.Command...)
	wrapperArgs := []string{
		"scanner-tool-wrapper",
		"--stdout", "/results/stdout",
		"--stderr", "/results/stderr",
		"--exit-code", "/results/exit-code",
		"--max-output-bytes", strconv.FormatInt(defaultOutputLimit(c.cfg.MaxOutputBytes), 10),
	}
	if invocation.Stdin != "" {
		if err := os.WriteFile(filepath.Join(resultDir, "stdin"), []byte(invocation.Stdin), 0o660); err != nil {
			_ = os.RemoveAll(resultDir)
			return "", "", fmt.Errorf("write scanner stdin: %w", err)
		}
		wrapperArgs = append(wrapperArgs, "--stdin", "/results/stdin")
	}
	wrapperArgs = append(wrapperArgs, "--")
	wrapperArgs = append(wrapperArgs, command...)
	wrapperArgs = append(wrapperArgs, invocation.Args...)
	env := make([]map[string]interface{}, 0, len(invocation.Environment))
	for key, value := range invocation.Environment {
		env = append(env, map[string]interface{}{"name": key, "value": value})
	}
	volumeMounts := []map[string]interface{}{
		{"name": "wolf-bin", "mountPath": "/wolf/bin", "readOnly": true},
		{
			"name": "workspace", "mountPath": "/results",
			"subPath": filepath.ToSlash(filepath.Join(".wolf-results", resultRel)),
		},
	}
	if sourceSubPath != "" {
		volumeMounts = append(volumeMounts, map[string]interface{}{
			"name": "workspace", "mountPath": scannercontainer.ScanMountPoint,
			"subPath": sourceSubPath, "readOnly": true,
		})
	}
	timeout := invocation.Timeout
	if timeout <= 0 {
		timeout = c.cfg.DefaultTimeout
	}
	resources := resourceRequirements(invocation)
	job := map[string]interface{}{
		"apiVersion": "batch/v1", "kind": "Job",
		"metadata": map[string]interface{}{
			"name": jobName,
			"labels": map[string]string{
				"app.kubernetes.io/name": "wolf-scanner", "wolf.dev/scan": scanLabel,
				"wolf.dev/tool": toolLabel, "wolf.dev/user": userLabel, "wolf.dev/lease": leaseLabel,
				"wolf.dev/attempt": attemptLabel,
				"wolf.dev/network": dnsLabel(defaultValue(invocation.NetworkClass, c.cfg.NetworkClass)),
			},
		},
		"spec": map[string]interface{}{
			"backoffLimit": 0, "ttlSecondsAfterFinished": c.cfg.JobTTLSeconds,
			"activeDeadlineSeconds": int64(timeout.Seconds()),
			"template": map[string]interface{}{
				"metadata": map[string]interface{}{"labels": map[string]string{
					"app.kubernetes.io/name": "wolf-scanner", "job-name": jobName,
					"wolf.dev/scan": scanLabel, "wolf.dev/tool": toolLabel,
					"wolf.dev/user": userLabel, "wolf.dev/lease": leaseLabel,
					"wolf.dev/attempt": attemptLabel,
					"wolf.dev/network": dnsLabel(defaultValue(invocation.NetworkClass, c.cfg.NetworkClass)),
				}},
				"spec": map[string]interface{}{
					"restartPolicy": "Never", "automountServiceAccountToken": false,
					"serviceAccountName": c.cfg.ServiceAccount,
					"securityContext": map[string]interface{}{
						"runAsNonRoot": true, "runAsUser": 1000, "runAsGroup": 1000,
						"fsGroup": 1000, "fsGroupChangePolicy": "OnRootMismatch",
						"seccompProfile": map[string]string{"type": "RuntimeDefault"},
					},
					"initContainers": []map[string]interface{}{{
						"name": "wolf-helper", "image": c.cfg.WolfImage, "imagePullPolicy": c.cfg.ImagePullPolicy,
						"command": []string{"/bin/sh", "-c"}, "args": []string{"cp /usr/local/bin/wolf /wolf/bin/wolf"},
						"volumeMounts":    []map[string]interface{}{{"name": "wolf-bin", "mountPath": "/wolf/bin"}},
						"securityContext": hardenedSecurityContext(false),
					}},
					"containers": []map[string]interface{}{{
						"name": "scanner", "image": invocation.Image, "imagePullPolicy": c.cfg.ImagePullPolicy,
						"command": []string{"/wolf/bin/wolf"}, "args": wrapperArgs,
						"workingDir": invocation.WorkingDir, "env": env, "volumeMounts": volumeMounts,
						"resources": resources, "securityContext": hardenedSecurityContext(true),
					}},
					"volumes": []map[string]interface{}{
						{"name": "wolf-bin", "emptyDir": map[string]interface{}{
							"medium": "Memory", "sizeLimit": "128Mi",
						}},
						{"name": "workspace", "persistentVolumeClaim": map[string]string{"claimName": c.cfg.WorkspacePVC}},
					},
				},
			},
		},
	}
	var response map[string]interface{}
	if err := c.request(ctx, http.MethodPost,
		"/apis/batch/v1/namespaces/"+url.PathEscape(c.cfg.Namespace)+"/jobs", job, &response); err != nil {
		_ = os.RemoveAll(resultDir)
		return "", "", err
	}
	return jobName, resultDir, nil
}

func defaultOutputLimit(configured int64) int64 {
	if configured <= 0 {
		return DefaultScannerOutputMaxBytes
	}
	return configured
}

// RuntimeRef identifies every native Job belonging to a scanner run. Some
// plugins intentionally launch more than one command, so a label selector is
// a more accurate runtime reference than a single Job name.
func RuntimeRef(scanID, toolName string, attempt int, leaseToken string) string {
	return fmt.Sprintf(
		"labels:wolf.dev/scan=%s,wolf.dev/tool=%s,wolf.dev/attempt=%d,wolf.dev/lease=%s",
		dnsLabel(defaultValue(scanID, "scan")),
		dnsLabel(defaultValue(toolName, "tool")),
		max(attempt, 1),
		dnsLabel(defaultValue(leaseToken, "inline")),
	)
}

func (c *Client) waitJob(ctx context.Context, name string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = c.cfg.DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout+30*time.Second)
	defer cancel()
	ticker := time.NewTicker(c.cfg.PollInterval)
	defer ticker.Stop()
	for {
		var job struct {
			Status struct {
				Succeeded int `json:"succeeded"`
				Failed    int `json:"failed"`
			} `json:"status"`
		}
		err := c.request(ctx, http.MethodGet,
			"/apis/batch/v1/namespaces/"+url.PathEscape(c.cfg.Namespace)+"/jobs/"+url.PathEscape(name),
			nil, &job)
		if err != nil {
			return err
		}
		if job.Status.Succeeded > 0 || job.Status.Failed > 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (c *Client) deleteJob(ctx context.Context, name string) {
	_ = c.deleteJobChecked(ctx, name)
}

func (c *Client) deleteJobChecked(ctx context.Context, name string) error {
	body := map[string]interface{}{
		"apiVersion": "v1", "kind": "DeleteOptions", "propagationPolicy": "Foreground",
	}
	return c.request(ctx, http.MethodDelete,
		"/apis/batch/v1/namespaces/"+url.PathEscape(c.cfg.Namespace)+"/jobs/"+url.PathEscape(name),
		body, nil)
}

func (c *Client) request(ctx context.Context, method, path string, body interface{}, dest interface{}) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimSuffix(c.cfg.APIServer, "/")+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Kubernetes API %s %s returned %d: %s", method, path, response.StatusCode, strings.TrimSpace(string(data)))
	}
	if dest != nil && len(data) > 0 {
		return json.Unmarshal(data, dest)
	}
	return nil
}

func hardenedSecurityContext(readOnlyRoot bool) map[string]interface{} {
	return map[string]interface{}{
		"runAsNonRoot": true, "runAsUser": 1000, "runAsGroup": 1000,
		"allowPrivilegeEscalation": false, "readOnlyRootFilesystem": readOnlyRoot,
		"capabilities": map[string]interface{}{"drop": []string{"ALL"}},
	}
}

func resourceRequirements(invocation scannerruntime.Invocation) map[string]interface{} {
	limits := map[string]string{}
	if invocation.Memory != "" {
		limits["memory"] = invocation.Memory
	}
	if invocation.CPUs != "" {
		limits["cpu"] = invocation.CPUs
	}
	if len(limits) == 0 {
		return map[string]interface{}{}
	}
	return map[string]interface{}{"limits": limits, "requests": limits}
}

func networkClass(dockerNetwork string) string {
	if dockerNetwork == "none" {
		return "offline"
	}
	return "network-required"
}

func dnsLabel(value string) string {
	value = strings.ToLower(value)
	var builder strings.Builder
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-' {
			builder.WriteRune(char)
		} else {
			builder.WriteByte('-')
		}
	}
	return strings.Trim(builder.String(), "-")
}

func truncateLabel(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return strings.TrimRight(value[:max], "-")
}

func shortLabel(value string, max int) string {
	if len(value) > max {
		return strings.TrimRight(value[:max], "-")
	}
	return value
}

func defaultValue(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envInt(name string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func envDuration(name string, fallback time.Duration) time.Duration {
	value, err := time.ParseDuration(strings.TrimSpace(os.Getenv(name)))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}
