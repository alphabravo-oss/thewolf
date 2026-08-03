package scannerreleasebackend

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

type KubernetesBackend struct {
	APIServer                       string
	Namespace                       string
	Instance                        string
	ExecutionLane                   string
	Token                           string
	HTTPClient                      *http.Client
	WorkspacePVC                    string
	WorkspaceRoot                   string
	Image                           string
	Program                         string
	ServiceAccount                  string
	SignerProfileSecret             string
	SignerCredentialSecret          string
	SignerAdapterPath               string
	SignerWorkloadIdentity          bool
	AdapterRegistryCredentialSecret string
	AdapterRegistryCredentialMount  string
	AdapterEngineCredentialSecret   string
	AdapterEngineCredentialMount    string
	AdapterWorkloadIdentity         bool
	AdapterPath                     string
	PollInterval                    time.Duration
	JobTTLSeconds                   int
	Actions                         []string
	Platforms                       []string
}

const (
	KubernetesExecutionLaneLabel       = "wolf.security/lane"
	KubernetesExecutionLaneOrdinary    = "ordinary"
	KubernetesExecutionLaneFixed       = "fixed"
	KubernetesExecutionLaneQuality     = "quality"
	KubernetesExecutionLaneIntegration = "integration"
	KubernetesExecutionLaneSigner      = "signer"
)

func (b KubernetesBackend) Name() string { return "kubernetes-job" }

func (b KubernetesBackend) Capabilities(context.Context) (Capabilities, error) {
	if err := b.validate(); err != nil {
		return Capabilities{}, err
	}
	actions := b.Actions
	if len(actions) == 0 {
		// The shipped wolf scanner-release-step command deliberately supports
		// the offline/evidence action set plus its verified signer protocol.
		// A custom step program must opt into every additional action so the
		// coordinator never advertises work the immutable image cannot execute.
		actions = offlineActionPatterns()
		if b.SignerProfileSecret != "" && b.SignerAdapterPath != "" {
			actions = append(actions, "signature/*", "release-manifest-signature")
		}
	}
	if b.SignerProfileSecret == "" || b.SignerAdapterPath == "" {
		filtered := make([]string, 0, len(actions))
		for _, action := range actions {
			if !RequiresSigning(strings.TrimSuffix(action, "*")) {
				filtered = append(filtered, action)
			}
		}
		actions = filtered
	}
	gib := int64(1 << 30)
	return Capabilities{
		Name: b.Name(), Actions: append([]string(nil), actions...),
		Kinds: allKinds(), Platforms: append([]string(nil), b.Platforms...),
		MaxCPU: 64000, MaxMemory: 256 * gib, MaxDisk: 1024 * gib,
		MaxTimeout: 24 * time.Hour, MaxConcurrency: 1000,
		EnforcesCPU: true, EnforcesMemory: true, EnforcesDisk: true,
		EnforcesTimeout: true, EnforcesCancellation: true, Idempotent: true,
		ExternalIdempotency: b.SignerProfileSecret != "" && b.SignerAdapterPath != "",
	}, nil
}

func (b KubernetesBackend) validate() error {
	parsed, err := url.Parse(b.APIServer)
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return errors.New("Kubernetes backend API server is invalid")
	}
	if parsed.Scheme != "https" &&
		!(parsed.Scheme == "http" && isLoopback(parsed.Hostname())) {
		return errors.New("Kubernetes backend API server must use HTTPS")
	}
	if !componentPattern.MatchString(b.Namespace) ||
		!componentPattern.MatchString(b.WorkspacePVC) ||
		!filepath.IsAbs(b.WorkspaceRoot) ||
		!immutableImage(b.Image) {
		return errors.New("Kubernetes backend namespace, PVC, workspace root, or image is invalid")
	}
	if b.Instance != "" && !componentPattern.MatchString(b.Instance) {
		return errors.New("Kubernetes backend instance label is invalid")
	}
	if b.ExecutionLane != "" && !contains([]string{
		KubernetesExecutionLaneOrdinary,
		KubernetesExecutionLaneFixed,
		KubernetesExecutionLaneQuality,
		KubernetesExecutionLaneIntegration,
		KubernetesExecutionLaneSigner,
	}, b.ExecutionLane) {
		return errors.New("Kubernetes backend execution lane is invalid")
	}
	if b.HTTPClient == nil {
		return errors.New("Kubernetes backend HTTP client is required")
	}
	if b.SignerProfileSecret != "" {
		if !componentPattern.MatchString(b.SignerProfileSecret) ||
			(b.SignerCredentialSecret != "" &&
				!componentPattern.MatchString(b.SignerCredentialSecret)) ||
			!filepath.IsAbs(b.SignerAdapterPath) {
			return errors.New("Kubernetes signer secret names or adapter path are invalid")
		}
	}
	if b.AdapterRegistryCredentialSecret != "" &&
		(!componentPattern.MatchString(b.AdapterRegistryCredentialSecret) ||
			!filepath.IsAbs(b.AdapterRegistryCredentialMount)) {
		return errors.New("Kubernetes adapter registry credential secret or mount path is invalid")
	}
	if b.AdapterEngineCredentialSecret != "" &&
		(!componentPattern.MatchString(b.AdapterEngineCredentialSecret) ||
			!filepath.IsAbs(b.AdapterEngineCredentialMount)) {
		return errors.New("Kubernetes adapter engine credential secret or mount path is invalid")
	}
	if b.AdapterRegistryCredentialSecret != "" &&
		b.AdapterRegistryCredentialSecret == b.AdapterEngineCredentialSecret {
		return errors.New("Kubernetes adapter registry and engine credentials must use distinct Secrets")
	}
	if b.AdapterPath != "" && !filepath.IsAbs(b.AdapterPath) {
		return errors.New("Kubernetes release adapter path must be absolute")
	}
	return nil
}

func (b KubernetesBackend) Execute(
	ctx context.Context,
	invocation Invocation,
) (BackendResult, error) {
	if _, err := b.Capabilities(ctx); err != nil {
		return BackendResult{}, err
	}
	sandboxInvocation, workspaceSubPath, err := b.sandboxInvocation(invocation)
	if err != nil {
		return BackendResult{}, err
	}
	requestPath, resultPath, err := b.operationPaths(invocation)
	if err != nil {
		return BackendResult{}, err
	}
	journal, err := openKubernetesOperationJournal(
		invocation.Request.Workspace, invocation.OperationID,
	)
	if err != nil {
		return BackendResult{}, err
	}
	defer journal.Close()
	if result, found, err := readBackendResultAt(journal); err != nil {
		return BackendResult{}, err
	} else if found {
		return result, nil
	}
	expectedInvocation := invocationDigest(sandboxInvocation)
	startedValue, started, err := readKubernetesJournalFile(journal, "started", 256)
	if err != nil {
		return BackendResult{}, err
	}
	if started && string(startedValue) != expectedInvocation+"\n" {
		return BackendResult{}, fmt.Errorf("%w: Kubernetes start marker", ErrBinding)
	}
	request, err := json.Marshal(sandboxInvocation)
	if err != nil {
		return BackendResult{}, err
	}
	if !started {
		if err := writeKubernetesJournalFile(journal, "request.json", request); err != nil {
			return BackendResult{}, err
		}
	}
	sandboxRequestPath, err := sandboxPath(invocation.Request.Workspace, requestPath)
	if err != nil {
		return BackendResult{}, err
	}
	sandboxResultPath, err := sandboxPath(invocation.Request.Workspace, resultPath)
	if err != nil {
		return BackendResult{}, err
	}
	job := b.renderJob(sandboxInvocation, sandboxRequestPath, sandboxResultPath, workspaceSubPath)
	jobName := nestedString(job, "metadata", "name")
	var status int
	var response []byte
	recreated := false
	if started {
		status, response, err = b.request(
			ctx, http.MethodGet, b.jobsPath()+"/"+url.PathEscape(jobName), nil,
		)
		if err != nil {
			return BackendResult{}, err
		}
		if status == http.StatusNotFound {
			if err := b.createJob(ctx, job); err != nil {
				return BackendResult{}, fmt.Errorf("recreate Kubernetes recovery Job: %w", err)
			}
			recreated = true
		}
		if status != http.StatusOK && status != http.StatusNotFound {
			return BackendResult{}, fmt.Errorf(
				"Kubernetes resume Job returned %d: %s", status, redact(string(response), 4096),
			)
		}
		if status == http.StatusOK {
			if err := verifyJobInvocation(response, expectedInvocation); err != nil {
				return BackendResult{}, err
			}
		}
	} else {
		if err := b.createJob(ctx, job); err != nil {
			return BackendResult{}, err
		}
		if err := writeKubernetesJournalFile(
			journal, "started", []byte(expectedInvocation+"\n"),
		); err != nil {
			return BackendResult{}, err
		}
	}
	defer b.deleteJob(context.WithoutCancel(ctx), jobName)
	poll := b.PollInterval
	if poll <= 0 {
		poll = time.Second
	}
	for {
		// A completed adapter writes its durable result before exiting. Check it
		// before the API so recovery also succeeds when TTL cleanup removes the
		// replacement Job immediately after completion.
		if result, found, readErr := readBackendResultAt(journal); readErr != nil {
			return BackendResult{}, readErr
		} else if found {
			_ = unix.Unlinkat(int(journal.Fd()), "request.json", 0)
			return result, nil
		}
		status, response, err = b.request(
			ctx, http.MethodGet, b.jobsPath()+"/"+url.PathEscape(jobName), nil,
		)
		if err != nil {
			return BackendResult{}, err
		}
		if status == http.StatusNotFound {
			if recreated {
				return BackendResult{}, fmt.Errorf(
					"%w: recovery Job for operation %s disappeared without a durable result",
					ErrAmbiguousResult, invocation.OperationID,
				)
			}
			if err := b.createJob(ctx, job); err != nil {
				return BackendResult{}, fmt.Errorf("recreate missing Kubernetes Job: %w", err)
			}
			recreated = true
			continue
		}
		if status != http.StatusOK {
			return BackendResult{}, fmt.Errorf(
				"Kubernetes read Job returned %d: %s", status, redact(string(response), 4096),
			)
		}
		if err := verifyJobInvocation(response, expectedInvocation); err != nil {
			return BackendResult{}, err
		}
		var observed struct {
			Status struct {
				Succeeded int `json:"succeeded"`
				Failed    int `json:"failed"`
			} `json:"status"`
		}
		if err := json.Unmarshal(response, &observed); err != nil {
			return BackendResult{}, fmt.Errorf("decode Kubernetes Job status: %w", err)
		}
		if observed.Status.Succeeded > 0 {
			break
		}
		if observed.Status.Failed > 0 {
			return BackendResult{}, errors.New("Kubernetes release Job failed")
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return BackendResult{}, ctx.Err()
		case <-timer.C:
		}
	}
	result, found, err := readBackendResultAt(journal)
	if err != nil {
		return BackendResult{}, err
	}
	if !found {
		return BackendResult{}, fmt.Errorf(
			"%w: Kubernetes Job succeeded without a durable result for %s",
			ErrAmbiguousResult, invocation.OperationID,
		)
	}
	_ = unix.Unlinkat(int(journal.Fd()), "request.json", 0)
	return result, nil
}

func (b KubernetesBackend) createJob(ctx context.Context, job map[string]any) error {
	status, response, err := b.request(ctx, http.MethodPost, b.jobsPath(), job)
	if err != nil {
		return err
	}
	if status != http.StatusCreated && status != http.StatusConflict {
		return fmt.Errorf(
			"Kubernetes create Job returned %d: %s", status, redact(string(response), 4096),
		)
	}
	return nil
}

func (b KubernetesBackend) renderJob(
	invocation Invocation,
	requestPath, resultPath, workspaceSubPath string,
) map[string]any {
	signingAction := RequiresSigning(invocation.Action.Name)
	program := b.Program
	if program == "" {
		program = "/usr/local/bin/wolf"
	}
	ttl := b.JobTTLSeconds
	if ttl <= 0 {
		ttl = 300
	}
	deadline := int64((invocation.Resources.Timeout + time.Second - 1) / time.Second)
	name := "wolf-release-" + strings.TrimPrefix(invocation.OperationID, "sha256:")[:24]
	labels := map[string]string{
		"app.kubernetes.io/name":      "wolf-scanner-release",
		"app.kubernetes.io/component": "step",
		KubernetesExecutionLaneLabel:  b.executionLane(signingAction),
		"wolf.dev/operation":          strings.TrimPrefix(invocation.OperationID, "sha256:")[:32],
		"wolf.dev/action":             labelValue(invocation.Action.Name),
	}
	if b.Instance != "" {
		labels["app.kubernetes.io/instance"] = b.Instance
	}
	annotations := map[string]string{
		"wolf.dev/invocation-digest": invocationDigest(invocation),
	}
	container := map[string]any{
		"name":    "step",
		"image":   b.Image,
		"command": []string{program},
		"args": []string{
			"scanner-release-step",
			"--request", requestPath,
			"--result", resultPath,
		},
		"workingDir": invocation.Request.Workspace,
		"resources": map[string]any{
			"requests": map[string]string{
				"cpu":               strconv.FormatInt(invocation.Resources.CPUMilli, 10) + "m",
				"memory":            strconv.FormatInt(invocation.Resources.MemoryBytes, 10),
				"ephemeral-storage": strconv.FormatInt(invocation.Resources.DiskBytes, 10),
			},
			"limits": map[string]string{
				"cpu":               strconv.FormatInt(invocation.Resources.CPUMilli, 10) + "m",
				"memory":            strconv.FormatInt(invocation.Resources.MemoryBytes, 10),
				"ephemeral-storage": strconv.FormatInt(invocation.Resources.DiskBytes, 10),
			},
		},
		"securityContext": map[string]any{
			"allowPrivilegeEscalation": false,
			"readOnlyRootFilesystem":   true,
			"runAsNonRoot":             true,
			"runAsUser":                1000,
			"capabilities":             map[string]any{"drop": []string{"ALL"}},
			"seccompProfile":           map[string]string{"type": "RuntimeDefault"},
		},
		"volumeMounts": []map[string]any{
			{
				"name": "workspace", "mountPath": "/workspace",
				"subPath": workspaceSubPath, "readOnly": true,
			},
			{
				"name": "workspace", "mountPath": operationSandboxDirectory(invocation.OperationID),
				"subPath": operationPVCSubPath(workspaceSubPath, invocation.OperationID),
			},
			{"name": "tmp", "mountPath": "/tmp"},
			{"name": "scratch", "mountPath": "/work"},
		},
	}
	environment, _ := container["env"].([]map[string]any)
	environment = append(environment, map[string]any{
		"name": "WOLF_SCANNER_RELEASE_SCRATCH_DIR", "value": "/work",
	})
	container["env"] = environment
	if b.AdapterPath != "" && !signingAction {
		container["args"] = append(container["args"].([]string), "--adapter", b.AdapterPath)
	}
	if signingAction && b.SignerProfileSecret != "" {
		environment, _ := container["env"].([]map[string]any)
		environment = append(environment,
			map[string]any{"name": "WOLF_SCANNER_SIGNER_PROFILE_FILE", "value": "/run/wolf/signing/profile/profile.json"},
			map[string]any{"name": "WOLF_SCANNER_SIGNER_ADAPTER", "value": b.SignerAdapterPath},
			map[string]any{"name": "WOLF_SCANNER_SIGNER_JOURNAL", "value": filepath.Join(invocation.Request.Workspace, ".wolf-signing", "journal")},
		)
		container["env"] = environment
		mounts := container["volumeMounts"].([]map[string]any)
		mounts = append(mounts, map[string]any{
			"name": "signer-profile", "mountPath": "/run/wolf/signing/profile",
			"readOnly": true,
		}, map[string]any{
			"name": "workspace", "mountPath": "/workspace/.wolf-signing/journal",
			"subPath": operationPVCSubPath(workspaceSubPath, invocation.OperationID),
		})
		if b.SignerCredentialSecret != "" {
			mounts = append(mounts, map[string]any{
				"name": "signer-credentials", "mountPath": "/run/wolf/signing/credentials",
				"readOnly": true,
			})
		}
		container["volumeMounts"] = mounts
	}
	if b.AdapterRegistryCredentialSecret != "" {
		environment, _ := container["env"].([]map[string]any)
		environment = append(environment, map[string]any{
			"name": "WOLF_SCANNER_RELEASE_REGISTRY_CREDENTIAL_DIR", "value": b.AdapterRegistryCredentialMount,
		})
		container["env"] = environment
		mounts := container["volumeMounts"].([]map[string]any)
		mounts = append(mounts, map[string]any{
			"name": "adapter-registry-credentials", "mountPath": b.AdapterRegistryCredentialMount,
			"readOnly": true,
		})
		container["volumeMounts"] = mounts
	}
	if b.AdapterEngineCredentialSecret != "" {
		environment, _ := container["env"].([]map[string]any)
		environment = append(environment, map[string]any{
			"name": "WOLF_SCANNER_RELEASE_ENGINE_CREDENTIAL_DIR", "value": b.AdapterEngineCredentialMount,
		})
		container["env"] = environment
		mounts := container["volumeMounts"].([]map[string]any)
		mounts = append(mounts, map[string]any{
			"name": "adapter-engine-credentials", "mountPath": b.AdapterEngineCredentialMount,
			"readOnly": true,
		})
		container["volumeMounts"] = mounts
	}
	podSpec := map[string]any{
		"restartPolicy": "Never",
		"automountServiceAccountToken": (signingAction && b.SignerProfileSecret != "" && b.SignerWorkloadIdentity) ||
			b.AdapterWorkloadIdentity,
		"securityContext": map[string]any{
			"runAsNonRoot": true, "runAsUser": 1000, "runAsGroup": 1000, "fsGroup": 1000,
		},
		"containers": []map[string]any{container},
		"volumes": []map[string]any{
			{
				"name":                  "workspace",
				"persistentVolumeClaim": map[string]string{"claimName": b.WorkspacePVC},
			},
			{
				"name": "tmp",
				"emptyDir": map[string]any{
					"sizeLimit": strconv.FormatInt(
						min(invocation.Resources.DiskBytes, int64(1<<30)), 10,
					),
				},
			},
			{
				"name": "scratch",
				"emptyDir": map[string]any{
					"sizeLimit": strconv.FormatInt(invocation.Resources.DiskBytes, 10),
				},
			},
		},
	}
	if signingAction && b.SignerProfileSecret != "" {
		volumes := podSpec["volumes"].([]map[string]any)
		volumes = append(volumes, map[string]any{
			"name": "signer-profile",
			"secret": map[string]any{
				"secretName": b.SignerProfileSecret,
				"items": []map[string]string{{
					"key": "profile.json", "path": "profile.json",
				}},
			},
		})
		if b.SignerCredentialSecret != "" {
			volumes = append(volumes, map[string]any{
				"name": "signer-credentials",
				"secret": map[string]any{
					"secretName": b.SignerCredentialSecret,
				},
			})
		}
		podSpec["volumes"] = volumes
	}
	if b.AdapterRegistryCredentialSecret != "" {
		volumes := podSpec["volumes"].([]map[string]any)
		volumes = append(volumes, map[string]any{
			"name":   "adapter-registry-credentials",
			"secret": map[string]any{"secretName": b.AdapterRegistryCredentialSecret},
		})
		podSpec["volumes"] = volumes
	}
	if b.AdapterEngineCredentialSecret != "" {
		volumes := podSpec["volumes"].([]map[string]any)
		volumes = append(volumes, map[string]any{
			"name":   "adapter-engine-credentials",
			"secret": map[string]any{"secretName": b.AdapterEngineCredentialSecret},
		})
		podSpec["volumes"] = volumes
	}
	if b.ServiceAccount != "" {
		podSpec["serviceAccountName"] = b.ServiceAccount
	}
	return map[string]any{
		"apiVersion": "batch/v1", "kind": "Job",
		"metadata": map[string]any{
			"name": name, "namespace": b.Namespace, "labels": labels,
			"annotations": annotations,
		},
		"spec": map[string]any{
			"backoffLimit": 0, "ttlSecondsAfterFinished": ttl,
			"activeDeadlineSeconds": deadline,
			"template": map[string]any{
				"metadata": map[string]any{
					"labels": labels, "annotations": annotations,
				},
				"spec": podSpec,
			},
		},
	}
}

func (b KubernetesBackend) executionLane(signingAction bool) string {
	if signingAction {
		return KubernetesExecutionLaneSigner
	}
	if b.ExecutionLane != "" {
		return b.ExecutionLane
	}
	return KubernetesExecutionLaneOrdinary
}

func (b KubernetesBackend) operationPaths(
	invocation Invocation,
) (string, string, error) {
	workspace, err := filepath.Abs(invocation.Request.Workspace)
	if err != nil {
		return "", "", err
	}
	root, err := filepath.Abs(b.WorkspaceRoot)
	if err != nil {
		return "", "", err
	}
	relative, err := filepath.Rel(root, workspace)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", "", errors.New("Kubernetes backend workspace is outside the mounted PVC root")
	}
	base := filepath.Join(
		workspace, ".wolf-release-backend-journal",
		strings.TrimPrefix(invocation.OperationID, "sha256:"),
	)
	return filepath.Join(base, "request.json"), filepath.Join(base, "result.json"), nil
}

func (b KubernetesBackend) sandboxInvocation(
	invocation Invocation,
) (Invocation, string, error) {
	workspace, err := filepath.EvalSymlinks(invocation.Request.Workspace)
	if err != nil {
		return Invocation{}, "", fmt.Errorf("resolve Kubernetes workspace: %w", err)
	}
	root, err := filepath.EvalSymlinks(b.WorkspaceRoot)
	if err != nil {
		return Invocation{}, "", fmt.Errorf("resolve Kubernetes PVC root: %w", err)
	}
	relative, err := filepath.Rel(root, workspace)
	if err != nil || relative == "." || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) ||
		filepath.IsAbs(relative) || filepath.Clean(relative) != relative {
		return Invocation{}, "", errors.New("Kubernetes workspace is not an isolated PVC subdirectory")
	}
	for _, component := range strings.Split(filepath.ToSlash(relative), "/") {
		if !componentPattern.MatchString(component) {
			return Invocation{}, "", errors.New("Kubernetes workspace subPath contains an invalid component")
		}
	}
	sandbox := invocation
	sandbox.Request.Workspace = "/workspace"
	return sandbox, filepath.ToSlash(relative), nil
}

func sandboxPath(workspace, hostPath string) (string, error) {
	relative, err := filepath.Rel(workspace, hostPath)
	if err != nil || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("Kubernetes journal path escapes the claimed workspace")
	}
	return filepath.ToSlash(filepath.Join("/workspace", relative)), nil
}

func operationSandboxDirectory(operationID string) string {
	return "/workspace/.wolf-release-backend-journal/" +
		strings.TrimPrefix(operationID, "sha256:")
}

func operationPVCSubPath(workspaceSubPath, operationID string) string {
	return filepath.ToSlash(filepath.Join(
		workspaceSubPath, ".wolf-release-backend-journal",
		strings.TrimPrefix(operationID, "sha256:"),
	))
}

// openKubernetesOperationJournal anchors all host-side journal access to an
// open directory descriptor. Jobs receive only this directory as a writable
// overlay; source, context, and rehydrated evidence remain read-only.
func openKubernetesOperationJournal(workspace, operationID string) (*os.File, error) {
	component := strings.TrimPrefix(operationID, "sha256:")
	if !digestPattern.MatchString(operationID) || !componentPattern.MatchString(component) {
		return nil, errors.New("Kubernetes operation journal identity is invalid")
	}
	workspaceFD, err := unix.Open(
		workspace, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0,
	)
	if err != nil {
		return nil, fmt.Errorf("open Kubernetes workspace without following links: %w", err)
	}
	defer unix.Close(workspaceFD)
	journalFD, err := openOrCreateJournalDirectory(workspaceFD, ".wolf-release-backend-journal")
	if err != nil {
		return nil, err
	}
	defer unix.Close(journalFD)
	operationFD, err := openOrCreateJournalDirectory(journalFD, component)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(operationFD), "kubernetes-operation-journal"), nil
}

func openOrCreateJournalDirectory(parent int, name string) (int, error) {
	if err := unix.Mkdirat(parent, name, 0o700); err != nil && !errors.Is(err, unix.EEXIST) {
		return -1, err
	}
	fd, err := unix.Openat(
		parent, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0,
	)
	if err != nil {
		return -1, fmt.Errorf("open Kubernetes journal directory %q without following links: %w", name, err)
	}
	return fd, nil
}

func writeKubernetesJournalFile(directory *os.File, name string, value []byte) error {
	if directory == nil || (name != "request.json" && name != "started") {
		return errors.New("Kubernetes journal write target is invalid")
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return err
	}
	temporary := fmt.Sprintf(".%s-%x.tmp", name, random[:])
	fd, err := unix.Openat(
		int(directory.Fd()), temporary,
		unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW,
		0o600,
	)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), temporary)
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = unix.Unlinkat(int(directory.Fd()), temporary, 0)
		}
	}()
	if _, err := file.Write(value); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := unix.Renameat(int(directory.Fd()), temporary, int(directory.Fd()), name); err != nil {
		return err
	}
	removeTemporary = false
	return nil
}

func readKubernetesJournalFile(
	directory *os.File, name string, maximum int64,
) ([]byte, bool, error) {
	if directory == nil || !componentPattern.MatchString(name) || maximum <= 0 {
		return nil, false, errors.New("Kubernetes journal read target is invalid")
	}
	fd, err := unix.Openat(
		int(directory.Fd()), name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0,
	)
	if errors.Is(err, unix.ENOENT) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("open Kubernetes journal file %q without following links: %w", name, err)
	}
	file := os.NewFile(uintptr(fd), name)
	defer file.Close()
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return nil, false, err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Nlink != 1 || stat.Size > maximum {
		return nil, false, fmt.Errorf("Kubernetes journal file %q is not a bounded unlinked regular file", name)
	}
	value, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(value)) > maximum {
		return nil, false, fmt.Errorf("Kubernetes journal file %q exceeds size limit", name)
	}
	return value, true, nil
}

func readBackendResultAt(directory *os.File) (BackendResult, bool, error) {
	value, found, err := readKubernetesJournalFile(directory, "result.json", maxBackendResultBytes)
	if err != nil || !found {
		return BackendResult{}, found, err
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	var result BackendResult
	if err := decoder.Decode(&result); err != nil {
		return BackendResult{}, false, fmt.Errorf("decode Kubernetes backend result: %w", err)
	}
	if err := ensureEOF(decoder); err != nil {
		return BackendResult{}, false, err
	}
	return result, true, nil
}

func verifyJobInvocation(value []byte, expected string) error {
	var observed struct {
		Metadata struct {
			Annotations map[string]string `json:"annotations"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(value, &observed); err != nil {
		return fmt.Errorf("decode Kubernetes Job identity: %w", err)
	}
	if observed.Metadata.Annotations["wolf.dev/invocation-digest"] != expected {
		return fmt.Errorf("%w: Kubernetes Job invocation annotation", ErrBinding)
	}
	return nil
}

func (b KubernetesBackend) jobsPath() string {
	return strings.TrimSuffix(b.APIServer, "/") +
		"/apis/batch/v1/namespaces/" + url.PathEscape(b.Namespace) + "/jobs"
}

func (b KubernetesBackend) request(
	ctx context.Context,
	method, endpoint string,
	body any,
) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		value, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(value)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if b.Token != "" {
		request.Header.Set("Authorization", "Bearer "+b.Token)
	}
	response, err := b.HTTPClient.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	value, err := io.ReadAll(io.LimitReader(response.Body, maxBackendResultBytes+1))
	if err != nil {
		return 0, nil, err
	}
	if len(value) > maxBackendResultBytes {
		return 0, nil, errors.New("Kubernetes API response exceeds size limit")
	}
	return response.StatusCode, value, nil
}

func (b KubernetesBackend) deleteJob(ctx context.Context, name string) {
	body := map[string]any{
		"propagationPolicy":  "Background",
		"gracePeriodSeconds": 0,
	}
	_, _, _ = b.request(
		ctx, http.MethodDelete, b.jobsPath()+"/"+url.PathEscape(name), body,
	)
}

func nestedString(value map[string]any, keys ...string) string {
	var current any = value
	for _, key := range keys {
		object, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = object[key]
	}
	result, _ := current.(string)
	return result
}

func labelValue(value string) string {
	value = strings.ReplaceAll(value, "/", "-")
	if len(value) > 63 {
		value = value[:63]
	}
	return strings.Trim(value, "-_.")
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
