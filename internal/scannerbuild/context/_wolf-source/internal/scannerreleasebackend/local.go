package scannerreleasebackend

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannerpipeline"
)

type Command struct {
	Path        string
	Args        []string
	Directory   string
	Environment []string
	Stdin       []byte
}

type CommandOutput struct {
	Stdout []byte
	Stderr []byte
}

type CommandRuntime interface {
	Run(context.Context, Command) (CommandOutput, error)
}

// ExecRuntime is intentionally capability-neutral. It is suitable for
// buildx client commands whose actual workload runs in a resource-limited
// Kubernetes BuildKit pod, but it is not by itself a local step sandbox.
type ExecRuntime struct {
	MaxOutputBytes int
}

func (r ExecRuntime) Run(ctx context.Context, command Command) (CommandOutput, error) {
	if strings.TrimSpace(command.Path) == "" {
		return CommandOutput{}, errors.New("backend executable is required")
	}
	limit := r.MaxOutputBytes
	if limit <= 0 {
		limit = maxBackendLogBytes
	}
	child := exec.CommandContext(ctx, command.Path, command.Args...) // #nosec G204 -- executable is administrator configuration and argv is rendered only from allowlisted actions.
	child.Dir = command.Directory
	child.Env = append([]string(nil), command.Environment...)
	child.Stdin = bytes.NewReader(command.Stdin)
	var stdout, stderr boundedWriter
	stdout.maximum, stderr.maximum = limit, limit
	child.Stdout, child.Stderr = &stdout, &stderr
	err := child.Run()
	output := CommandOutput{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	if err != nil {
		return output, fmt.Errorf("%w: %s", err, redact(string(output.Stderr), 4096))
	}
	return output, nil
}

type boundedWriter struct {
	buffer  bytes.Buffer
	maximum int
}

func (w *boundedWriter) Write(value []byte) (int, error) {
	original := len(value)
	if w.buffer.Len() >= w.maximum {
		return original, nil
	}
	remaining := w.maximum - w.buffer.Len()
	if len(value) > remaining {
		value = value[:remaining]
	}
	_, err := w.buffer.Write(value)
	return original, err
}

func (w *boundedWriter) Bytes() []byte {
	return append([]byte(nil), w.buffer.Bytes()...)
}

// LocalSandbox runs one fixed-protocol step program inside a rootless
// Docker/Podman-compatible container. The container engine enforces CPU,
// memory, writable-layer size, process timeout/cancellation, and the outer
// Executor enforces concurrency. The program receives Invocation on stdin;
// candidate data is never placed in argv.
type LocalSandbox struct {
	Runtime     CommandRuntime
	EnginePath  string
	Image       string
	Program     string
	Environment []string
	Platforms   []string
	// HostWorkspaceRoot maps the worker's ContainerWorkspaceRoot to the path
	// understood by a remote rootless container engine (for example Compose).
	// Both are empty for an engine running in the worker's own mount namespace.
	HostWorkspaceRoot       string
	ContainerWorkspaceRoot  string
	SignerProfileHostFile   string
	SignerCredentialHostDir string
	SignerAdapterPath       string
	SignerNetwork           string
}

func (b LocalSandbox) Name() string { return "local-offline" }

func (b LocalSandbox) Capabilities(context.Context) (Capabilities, error) {
	if b.Runtime == nil || b.EnginePath == "" || !immutableImage(b.Image) {
		return Capabilities{}, errors.New("local backend requires runtime, container engine, and digest-pinned image")
	}
	if (b.HostWorkspaceRoot == "") != (b.ContainerWorkspaceRoot == "") ||
		(b.HostWorkspaceRoot != "" &&
			(!filepath.IsAbs(b.HostWorkspaceRoot) ||
				!filepath.IsAbs(b.ContainerWorkspaceRoot))) {
		return Capabilities{}, errors.New(
			"local backend workspace mapping roots must both be absolute or both be empty",
		)
	}
	if b.Program == "" {
		b.Program = "/usr/local/bin/wolf"
	}
	gib := int64(1 << 30)
	actions := offlineActionPatterns()
	signingEnabled := b.SignerProfileHostFile != "" && b.SignerAdapterPath != ""
	if signingEnabled {
		if !filepath.IsAbs(b.SignerProfileHostFile) ||
			!filepath.IsAbs(b.SignerAdapterPath) ||
			(b.SignerCredentialHostDir != "" &&
				!filepath.IsAbs(b.SignerCredentialHostDir)) {
			return Capabilities{}, errors.New(
				"local signer profile, credential directory, and adapter paths must be absolute",
			)
		}
		actions = append(actions, "signature/*", "release-manifest-signature")
	}
	return Capabilities{
		Name: b.Name(), Actions: actions,
		Kinds: allKinds(), Platforms: append([]string(nil), b.Platforms...),
		MaxCPU: 64000, MaxMemory: 256 * gib, MaxDisk: 1024 * gib,
		MaxTimeout: 24 * time.Hour, MaxConcurrency: 64,
		EnforcesCPU: true, EnforcesMemory: true, EnforcesDisk: true,
		EnforcesTimeout: true, EnforcesCancellation: true, Idempotent: true,
		ExternalIdempotency: signingEnabled,
	}, nil
}

func (b LocalSandbox) Execute(
	ctx context.Context,
	invocation Invocation,
) (BackendResult, error) {
	if _, err := b.Capabilities(ctx); err != nil {
		return BackendResult{}, err
	}
	mountSource, err := b.mountSource(invocation.Request.Workspace)
	if err != nil {
		return BackendResult{}, err
	}
	sandboxInvocation := invocation
	sandboxInvocation.Request.Workspace = "/workspace"
	payload, err := json.Marshal(sandboxInvocation)
	if err != nil {
		return BackendResult{}, err
	}
	if b.Program == "" {
		b.Program = "/usr/local/bin/wolf"
	}
	name := "wolf-release-" + strings.TrimPrefix(invocation.OperationID, "sha256:")[:24]
	network := "none"
	if b.SignerNetwork != "" {
		network = b.SignerNetwork
	}
	args := []string{
		"run", "--rm", "--name", name,
		"--network", network,
		"--cpus", formatCPU(invocation.Resources.CPUMilli),
		"--memory", strconv.FormatInt(invocation.Resources.MemoryBytes, 10),
		"--memory-swap", strconv.FormatInt(invocation.Resources.MemoryBytes, 10),
		"--storage-opt", "size=" + strconv.FormatInt(invocation.Resources.DiskBytes, 10),
		"--pids-limit", "512",
		"--read-only",
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"--tmpfs", "/tmp:rw,noexec,nosuid,nodev,size=268435456",
		"--volume", mountSource + ":/workspace:rw",
		"--workdir", "/workspace",
	}
	if b.SignerProfileHostFile != "" && b.SignerAdapterPath != "" {
		args = append(args,
			"--volume", b.SignerProfileHostFile+":/run/wolf/signing/profile.json:ro",
			"--env", "WOLF_SCANNER_SIGNER_PROFILE_FILE=/run/wolf/signing/profile.json",
			"--env", "WOLF_SCANNER_SIGNER_ADAPTER="+b.SignerAdapterPath,
			"--env", "WOLF_SCANNER_SIGNER_JOURNAL=/workspace/.wolf-signing/journal",
		)
		if b.SignerCredentialHostDir != "" {
			args = append(args,
				"--volume", b.SignerCredentialHostDir+":/run/wolf/signing/credentials:ro",
			)
		}
		for _, name := range signerEnvironmentNames() {
			args = append(args, "--env", name)
		}
	}
	args = append(args, b.Image, b.Program, "scanner-release-step")
	output, err := b.Runtime.Run(ctx, Command{
		Path: b.EnginePath, Args: args, Environment: b.Environment, Stdin: payload,
	})
	if err != nil {
		return BackendResult{}, err
	}
	if len(output.Stdout) > maxBackendResultBytes {
		return BackendResult{}, errors.New("local backend result exceeds size limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(output.Stdout))
	decoder.DisallowUnknownFields()
	var result BackendResult
	if err := decoder.Decode(&result); err != nil {
		return BackendResult{}, fmt.Errorf("decode local backend result: %w", err)
	}
	if err := ensureEOF(decoder); err != nil {
		return BackendResult{}, err
	}
	result.Log = string(output.Stderr)
	return result, nil
}

func signerEnvironmentNames() []string {
	return []string{
		"AWS_REGION", "AWS_DEFAULT_REGION", "AWS_ROLE_ARN",
		"AWS_WEB_IDENTITY_TOKEN_FILE", "GOOGLE_APPLICATION_CREDENTIALS",
		"GOOGLE_CLOUD_PROJECT", "AZURE_CLIENT_ID", "AZURE_TENANT_ID",
		"AZURE_FEDERATED_TOKEN_FILE", "PKCS11_MODULE_PATH", "PKCS11_CONFIG",
		"SIGSTORE_ID_TOKEN_FILE", "SIGSTORE_FULCIO_URL", "SIGSTORE_REKOR_URL",
	}
}

func (b LocalSandbox) mountSource(workspace string) (string, error) {
	if b.HostWorkspaceRoot == "" && b.ContainerWorkspaceRoot == "" {
		return workspace, nil
	}
	if !filepath.IsAbs(b.HostWorkspaceRoot) ||
		!filepath.IsAbs(b.ContainerWorkspaceRoot) {
		return "", errors.New("local backend workspace mapping roots must both be absolute")
	}
	relative, err := filepath.Rel(b.ContainerWorkspaceRoot, workspace)
	if err != nil || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("local backend workspace is outside its mapped root")
	}
	return filepath.Join(b.HostWorkspaceRoot, relative), nil
}

func immutableImage(value string) bool {
	parts := strings.Split(value, "@sha256:")
	return len(parts) == 2 && parts[0] != "" && len(parts[1]) == 64
}

func formatCPU(milli int64) string {
	return strconv.FormatFloat(float64(milli)/1000, 'f', 3, 64)
}

func allKinds() []scannerpipeline.StepKind {
	return []scannerpipeline.StepKind{
		scannerpipeline.StepCheckout, scannerpipeline.StepValidation,
		scannerpipeline.StepBuild, scannerpipeline.StepTest,
		scannerpipeline.StepSecurity, scannerpipeline.StepEvidence,
		scannerpipeline.StepPublish, scannerpipeline.StepIntegration,
		scannerpipeline.StepPolicy,
	}
}

func allActionPatterns() []string {
	return []string{
		"checkout", "manifest-validate", "generated-parity",
		"update-source-recheck", "lock-reproducibility", "license-metadata",
		"build/*", "image-manifest/*", "strict-version-smoke/*",
		"invocation-smoke/*", "fixer-auth-contract/*", "parser-fixtures/*", "normalized-golden/*",
		"candidate-stable-comparison/*", "recorded-resource-gate/*",
		"vulnerability-db-identity/*",
		"vulnerability-scan/*", "secret-scan/*", "license-scan/*",
		"sbom/*", "oci-annotations/*", "provenance/*",
		"candidate-publish/*", "signature/*", "published-verify/*",
		"finding-regression", "aggregate-sbom", "mirror-copy-verify",
		"fixer-integration",
		"compose-integration", "kubernetes-integration", "release-manifest",
		"compose-scanner-integration", "kind-scanner-integration",
		"release-manifest-signature", "policy-evaluation",
		"policy-decision-artifact", "candidate-evidence-summary",
	}
}

func offlineActionPatterns() []string {
	return []string{
		"checkout", "manifest-validate", "generated-parity",
		"lock-reproducibility", "license-metadata",
		"image-manifest/*", "strict-version-smoke/*", "invocation-smoke/*",
		"fixer-auth-contract/*", "parser-fixtures/*", "normalized-golden/*", "vulnerability-scan/*",
		"candidate-stable-comparison/*", "recorded-resource-gate/*",
		"vulnerability-db-identity/*",
		"secret-scan/*", "license-scan/*", "sbom/*", "oci-annotations/*",
		"provenance/*", "published-verify/*",
		"finding-regression", "aggregate-sbom", "compose-integration",
		"fixer-integration",
		"compose-scanner-integration",
		"release-manifest",
		"policy-evaluation", "policy-decision-artifact", "candidate-evidence-summary",
	}
}

var _ io.Writer = (*boundedWriter)(nil)
