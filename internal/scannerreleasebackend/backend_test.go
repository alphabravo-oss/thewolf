package scannerreleasebackend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannerpipeline"
	"github.com/alphabravocompany/thewolf/internal/scannerregistry"
	"github.com/alphabravocompany/thewolf/internal/scannerreleaseworker"
	"github.com/alphabravocompany/thewolf/internal/scannerreleaseworkspace"
	"github.com/alphabravocompany/thewolf/internal/scannersigning"
	scannerlock "github.com/alphabravocompany/thewolf/internal/scannertools/lock"
	"github.com/alphabravocompany/thewolf/internal/scannertools/manifest"
)

const (
	testCommit = "1111111111111111111111111111111111111111"
	testLock   = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testDigest = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func TestSigningClosureEvidenceRequiresDurableArtifactBinding(t *testing.T) {
	t.Parallel()
	artifactDigest := "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	certificateDigest := "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	result := scannerreleaseworker.StepResult{
		OutputDigest: artifactDigest,
		Summary: map[string]any{"signing_evidence": scannersigning.Evidence{
			SchemaVersion: "wolf.scanner-signing-evidence/v1",
			Verified:      true, SignatureDigest: testDigest,
			SignatureArtifactDigest: artifactDigest,
			ArtifactSubjectDigest:   testLock,
			CertificateDigest:       certificateDigest,
		}},
	}
	evidence, err := signingClosureEvidence(result, "release_manifest")
	if err != nil {
		t.Fatal(err)
	}
	if evidence["release_manifest_signature"] != testDigest ||
		evidence["release_manifest_signature_artifact"] != artifactDigest ||
		evidence["release_manifest_certificate"] != certificateDigest {
		t.Fatalf("unexpected signing closure evidence: %#v", evidence)
	}

	result.OutputDigest = testLock
	if _, err := signingClosureEvidence(result, "release_manifest"); err == nil {
		t.Fatal("expected top-level signature digest mismatch to fail closed")
	}
	result.OutputDigest = artifactDigest
	evidenceValue := result.Summary["signing_evidence"].(scannersigning.Evidence)
	evidenceValue.CertificateDigest = "mutable-certificate"
	result.Summary["signing_evidence"] = evidenceValue
	if _, err := signingClosureEvidence(result, "release_manifest"); err == nil {
		t.Fatal("expected mutable certificate reference to fail closed")
	}
}

type fakeBackend struct {
	capability Capabilities
	calls      atomic.Int64
	invocation Invocation
	result     BackendResult
	executeErr error
	wait       bool
}

func (b *fakeBackend) Name() string { return "fake" }
func (b *fakeBackend) Capabilities(context.Context) (Capabilities, error) {
	return b.capability, nil
}
func (b *fakeBackend) Execute(ctx context.Context, invocation Invocation) (BackendResult, error) {
	b.calls.Add(1)
	b.invocation = invocation
	if b.wait {
		<-ctx.Done()
		return BackendResult{}, ctx.Err()
	}
	if b.executeErr != nil {
		return BackendResult{}, b.executeErr
	}
	result := b.result
	result.Binding = invocation.Binding
	return result, nil
}

func TestDefaultPolicyCoversEveryPipelineStepAndRejectsUnknown(t *testing.T) {
	t.Parallel()
	plan, err := scannerpipeline.Default(scannerpipeline.Inputs{
		Images: []scannerpipeline.Image{
			{Key: "default", Platforms: []string{"linux/amd64", "linux/arm64"}},
			{Key: "fixer-base", Kind: scannerpipeline.ImageKindFixer, Platforms: []string{"linux/amd64", "linux/arm64"}},
			{
				Key: "fixer-codex", Kind: scannerpipeline.ImageKindFixer,
				Platforms: []string{"linux/amd64", "linux/arm64"}, DependsOn: []string{"fixer-base"},
			},
		},
		RequireCompose: true, RequireKubernetes: true, RequireMirror: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	policy := DefaultResourcePolicy()
	for _, step := range plan.Steps {
		action, resources, err := policy.Resolve(step)
		if err != nil {
			t.Fatalf("step %q is not mapped: %v", step.Key, err)
		}
		if action.Name != step.Key || resources.CPUMilli <= 0 ||
			resources.MemoryBytes <= 0 || resources.DiskBytes <= 0 ||
			resources.Timeout <= 0 || resources.MaxConcurrency <= 0 {
			t.Fatalf("incomplete mapping for %q: %#v %#v", step.Key, action, resources)
		}
	}
	if _, _, err := policy.Resolve(scannerpipeline.Step{
		Key: "custom-shell", Kind: scannerpipeline.StepBuild, Timeout: time.Minute,
	}); !errors.Is(err, ErrUnsupportedStep) {
		t.Fatalf("unknown step error = %v", err)
	}
}

func TestSecureExecutorBindsResourcesRedactsAndCachesDuplicate(t *testing.T) {
	t.Parallel()
	backend := &fakeBackend{
		capability: completeCapabilities("fake", allActionPatterns(), allKinds()),
		result: BackendResult{
			Result: scannerreleaseworker.StepResult{
				OutputURI:    "oci://registry.example/evidence@" + testDigest,
				OutputDigest: testDigest,
				Summary:      map[string]any{"status": "passed"},
			},
			Log: "token=super-secret Bearer raw-token",
		},
	}
	executor, err := NewExecutor(backend, DefaultResourcePolicy())
	if err != nil {
		t.Fatal(err)
	}
	request := testRequest(t, scannerpipeline.Step{
		Key: "manifest-validate", Kind: scannerpipeline.StepValidation,
		Timeout: time.Minute, Required: true,
	})
	first, err := executor.Execute(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := executor.Execute(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	retry := request
	retry.StepAttempt = 2
	if retry.LogicalOperationID != scannerreleaseworker.DeriveLogicalOperationID(retry) {
		t.Fatal("diagnostic attempt changed the logical operation ID")
	}
	third, err := executor.Execute(context.Background(), retry)
	if err != nil {
		t.Fatal(err)
	}
	if backend.calls.Load() != 1 {
		t.Fatalf("duplicate operation executed %d times", backend.calls.Load())
	}
	if first.OutputDigest != second.OutputDigest || second.OutputDigest != third.OutputDigest ||
		first.Verification.DefinitionCommit != testCommit ||
		first.Verification.LockDigest != testLock ||
		first.Verification.PolicyID != "policy-1" ||
		first.Verification.PolicyRevision != 7 {
		t.Fatalf("bound result = %#v", first)
	}
	summary, _ := json.Marshal(first.Summary)
	if strings.Contains(string(summary), "super-secret") ||
		strings.Contains(string(summary), "raw-token") ||
		!strings.Contains(string(summary), "[REDACTED]") {
		t.Fatalf("backend log was not redacted: %s", summary)
	}
	if backend.invocation.Resources.CPUMilli != 2000 ||
		backend.invocation.Resources.Timeout != time.Minute ||
		!strings.HasPrefix(backend.invocation.OperationID, "sha256:") {
		t.Fatalf("invocation policy = %#v", backend.invocation)
	}
}

func TestSecureExecutorRequiresCanonicalLogicalOperationID(t *testing.T) {
	t.Parallel()
	backend := &fakeBackend{capability: completeCapabilities(
		"fake", []string{"manifest-validate"},
		[]scannerpipeline.StepKind{scannerpipeline.StepValidation},
	)}
	executor, err := NewExecutor(backend, DefaultResourcePolicy())
	if err != nil {
		t.Fatal(err)
	}
	request := testRequest(t, scannerpipeline.Step{
		Key: "manifest-validate", Kind: scannerpipeline.StepValidation,
		Timeout: time.Minute, Required: true,
	})
	request.LogicalOperationID = ""
	if _, err := executor.Execute(context.Background(), request); err == nil ||
		!strings.Contains(err.Error(), "logical operation ID") {
		t.Fatalf("missing logical operation ID error = %v", err)
	}
	request.LogicalOperationID = testDigest
	if _, err := executor.Execute(context.Background(), request); err == nil ||
		!strings.Contains(err.Error(), "logical operation ID") {
		t.Fatalf("noncanonical logical operation ID error = %v", err)
	}
	if backend.calls.Load() != 0 {
		t.Fatalf("backend ran %d times for invalid logical identity", backend.calls.Load())
	}
}

func TestSecureExecutorClassifiesAmbiguousSinkResultForReconciliation(t *testing.T) {
	t.Parallel()
	backend := &fakeBackend{
		capability: completeCapabilities(
			"fake", []string{"candidate-publish/*"},
			[]scannerpipeline.StepKind{scannerpipeline.StepPublish},
		),
		executeErr: fmt.Errorf("registry acknowledgement lost: %w", ErrAmbiguousResult),
	}
	executor, err := NewExecutor(backend, DefaultResourcePolicy())
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.Execute(context.Background(), testRequest(t, scannerpipeline.Step{
		Key: "candidate-publish/default", Kind: scannerpipeline.StepPublish,
		Timeout: time.Minute, Retryable: true, Required: true,
	}))
	if !errors.Is(err, scannerreleaseworker.ErrReconciliationRequired) {
		t.Fatalf("ambiguous sink error = %v", err)
	}
	if backend.calls.Load() != 1 {
		t.Fatalf("ambiguous sink backend calls = %d", backend.calls.Load())
	}
}

func TestSecureExecutorFailsClosedOnCapabilityAndCancellation(t *testing.T) {
	t.Parallel()
	backend := &fakeBackend{
		capability: completeCapabilities(
			"fake", []string{"manifest-validate"},
			[]scannerpipeline.StepKind{scannerpipeline.StepValidation},
		),
	}
	backend.capability.EnforcesDisk = false
	executor, _ := NewExecutor(backend, DefaultResourcePolicy())
	if _, err := executor.Execute(context.Background(), testRequest(t, scannerpipeline.Step{
		Key: "manifest-validate", Kind: scannerpipeline.StepValidation,
		Timeout: time.Minute, Required: true,
	})); !errors.Is(err, ErrResourcePolicy) {
		t.Fatalf("missing enforcement error = %v", err)
	}
	if backend.calls.Load() != 0 {
		t.Fatal("backend ran without mandatory enforcement")
	}

	backend.capability.EnforcesDisk = true
	backend.wait = true
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := executor.Execute(ctx, testRequest(t, scannerpipeline.Step{
		Key: "manifest-validate", Kind: scannerpipeline.StepValidation,
		Timeout: time.Minute, Required: true,
	})); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled backend error = %v", err)
	}
}

func TestSecureExecutorCleansOperationLocksAfterHighCardinalityChurn(t *testing.T) {
	t.Parallel()
	backend := &fakeBackend{
		capability: completeCapabilities(
			"fake", []string{"manifest-validate"},
			[]scannerpipeline.StepKind{scannerpipeline.StepValidation},
		),
		result: BackendResult{Result: scannerreleaseworker.StepResult{
			OutputDigest: testDigest,
		}},
	}
	executor, err := NewExecutor(backend, DefaultResourcePolicy())
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	for attempt := 1; attempt <= 250; attempt++ {
		request := testRequestAt(workspace, scannerpipeline.Step{
			Key: "manifest-validate", Kind: scannerpipeline.StepValidation,
			Timeout: time.Minute, Required: true,
		})
		request.StepAttempt = attempt
		if _, err := executor.Execute(context.Background(), request); err != nil {
			t.Fatal(err)
		}
	}
	executor.mu.Lock()
	operationCount := len(executor.operations)
	executor.mu.Unlock()
	if operationCount != 0 {
		t.Fatalf("operation lock map retained %d completed keys", operationCount)
	}
}

type concurrencyBackend struct {
	active atomic.Int64
	peak   atomic.Int64
}

func (b *concurrencyBackend) Name() string { return "concurrency-test" }

func (b *concurrencyBackend) Capabilities(context.Context) (Capabilities, error) {
	return completeCapabilities(
		b.Name(), []string{"manifest-validate"},
		[]scannerpipeline.StepKind{scannerpipeline.StepValidation},
	), nil
}

func (b *concurrencyBackend) Execute(
	ctx context.Context,
	invocation Invocation,
) (BackendResult, error) {
	active := b.active.Add(1)
	defer b.active.Add(-1)
	for {
		peak := b.peak.Load()
		if active <= peak || b.peak.CompareAndSwap(peak, active) {
			break
		}
	}
	select {
	case <-ctx.Done():
		return BackendResult{}, ctx.Err()
	case <-time.After(10 * time.Millisecond):
	}
	return BackendResult{
		Binding: invocation.Binding,
		Result:  scannerreleaseworker.StepResult{OutputDigest: testDigest},
	}, nil
}

func TestSecureExecutorEnforcesPolicyConcurrency(t *testing.T) {
	t.Parallel()
	backend := &concurrencyBackend{}
	policy := DefaultResourcePolicy()
	resources := policy.ByKind[scannerpipeline.StepValidation]
	resources.MaxConcurrency = 1
	policy.ByKind[scannerpipeline.StepValidation] = resources
	executor, err := NewExecutor(backend, policy)
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	failures := make(chan error, 16)
	for index := 0; index < 16; index++ {
		group.Add(1)
		go func(attempt int) {
			defer group.Done()
			request := testRequest(t, scannerpipeline.Step{
				Key: "manifest-validate", Kind: scannerpipeline.StepValidation,
				Timeout: time.Minute, Required: true,
			})
			request.StepAttempt = attempt + 1
			_, err := executor.Execute(context.Background(), request)
			failures <- err
		}(index)
	}
	group.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	if peak := backend.peak.Load(); peak != 1 {
		t.Fatalf("backend peak concurrency = %d, want 1", peak)
	}
}

func TestExternalSideEffectsRequireSinkOperationAcknowledgement(t *testing.T) {
	t.Parallel()
	backend := &fakeBackend{
		capability: completeCapabilities(
			"fake", []string{"candidate-publish/*"},
			[]scannerpipeline.StepKind{scannerpipeline.StepPublish},
		),
		result: BackendResult{Result: scannerreleaseworker.StepResult{
			OutputDigest: testDigest,
		}},
	}
	executor, err := NewExecutor(backend, DefaultResourcePolicy())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Execute(context.Background(), testRequest(t, scannerpipeline.Step{
		Key: "candidate-publish/default", Kind: scannerpipeline.StepPublish,
		Timeout: time.Minute, Required: true,
	})); !errors.Is(err, ErrBinding) {
		t.Fatalf("missing external operation acknowledgement error = %v", err)
	}
}

func TestMirrorReleaseClosureUsesStableExternalOperationIdentity(t *testing.T) {
	t.Parallel()
	if !RequiresExternalIdempotency("mirror-release-closure-verify") {
		t.Fatal("mirror release closure is not classified as an external side effect")
	}
	request := testRequest(t, scannerpipeline.Step{
		Key: "mirror-release-closure-verify", Kind: scannerpipeline.StepPublish,
		Timeout: time.Minute, Retryable: true, Required: true,
	})
	first, err := PrepareInvocation(request)
	if err != nil {
		t.Fatal(err)
	}
	request.StepAttempt = 2
	second, err := PrepareInvocation(request)
	if err != nil {
		t.Fatal(err)
	}
	if first.OperationID != second.OperationID || invocationDigest(first) != invocationDigest(second) {
		t.Fatalf(
			"mirror closure identity changed across diagnostic attempt: first=%s second=%s",
			first.OperationID, second.OperationID,
		)
	}
}

func TestLocalBackendRejectsAmbiguousPublishSignAndMirrorActions(t *testing.T) {
	t.Parallel()
	backend := LocalSandbox{
		Runtime: &recordingRuntime{}, EnginePath: "/usr/bin/podman",
		Image: "registry.example/wolf-release-step@sha256:" + strings.Repeat("c", 64),
	}
	capability, err := backend.Capabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{
		"candidate-publish/default", "signature/default",
		"release-manifest-signature", "mirror-copy-verify", "mirror-release-closure-verify",
		"build/default/linux-amd64",
	} {
		if supportsAction(capability.Actions, action) {
			t.Fatalf("local backend ambiguously advertises %q", action)
		}
	}
	if capability.ExternalIdempotency {
		t.Fatal("local backend must not advertise external idempotency")
	}
}

func TestKubernetesBuiltinStepDoesNotAdvertiseUnimplementedExternalActions(t *testing.T) {
	t.Parallel()
	backend := KubernetesBackend{
		APIServer: "https://kubernetes.default.svc", Namespace: "release-builds",
		HTTPClient: &http.Client{}, WorkspacePVC: "release-workspace",
		WorkspaceRoot: "/workspace",
		Image:         "registry.example/wolf-step@sha256:" + strings.Repeat("d", 64),
	}
	capability, err := backend.Capabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{
		"build/default/linux-amd64", "candidate-publish/default",
		"signature/default", "release-manifest-signature", "mirror-copy-verify",
		"mirror-release-closure-verify",
	} {
		if supportsAction(capability.Actions, action) {
			t.Fatalf("built-in Kubernetes step ambiguously advertises %q", action)
		}
	}
	if capability.ExternalIdempotency {
		t.Fatal("unsigned built-in Kubernetes step must not advertise external idempotency")
	}
}

func TestKubernetesBackendRejectsSharedRegistryAndEngineSecret(t *testing.T) {
	t.Parallel()
	backend := KubernetesBackend{
		APIServer: "https://kubernetes.default.svc", Namespace: "release-builds",
		HTTPClient: &http.Client{}, WorkspacePVC: "release-workspace", WorkspaceRoot: "/workspace",
		Image:                           "registry.example/wolf-step@sha256:" + strings.Repeat("d", 64),
		AdapterRegistryCredentialSecret: "shared-credentials",
		AdapterRegistryCredentialMount:  "/run/wolf/adapter-registry",
		AdapterEngineCredentialSecret:   "shared-credentials",
		AdapterEngineCredentialMount:    "/run/wolf/adapter-engine",
	}
	if _, err := backend.Capabilities(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "distinct Secrets") {
		t.Fatalf("shared registry/engine secret error = %v", err)
	}
}

type recordingRuntime struct {
	mu       sync.Mutex
	commands []Command
	run      func(Command) (CommandOutput, error)
}

func (r *recordingRuntime) Run(_ context.Context, command Command) (CommandOutput, error) {
	r.mu.Lock()
	r.commands = append(r.commands, command)
	r.mu.Unlock()
	if r.run != nil {
		return r.run(command)
	}
	return CommandOutput{}, nil
}

func TestLocalSandboxUsesFixedArgvAndAllResourceControls(t *testing.T) {
	t.Parallel()
	var binding Binding
	runtime := &recordingRuntime{}
	runtime.run = func(command Command) (CommandOutput, error) {
		var invocation Invocation
		if err := json.Unmarshal(command.Stdin, &invocation); err != nil {
			return CommandOutput{}, err
		}
		binding = invocation.Binding
		value, _ := json.Marshal(BackendResult{
			Binding: binding,
			Result: scannerreleaseworker.StepResult{
				OutputDigest: testDigest,
			},
		})
		return CommandOutput{Stdout: value}, nil
	}
	backend := LocalSandbox{
		Runtime: runtime, EnginePath: "/usr/bin/podman",
		Image: "registry.example/wolf-release-step@sha256:" + strings.Repeat("c", 64),
	}
	invocation := testInvocation(t, "manifest-validate", scannerpipeline.StepValidation)
	result, err := backend.Execute(context.Background(), invocation)
	if err != nil {
		t.Fatal(err)
	}
	if result.Binding != invocation.Binding {
		t.Fatalf("local binding = %#v", result.Binding)
	}
	command := runtime.commands[0]
	joined := strings.Join(command.Args, " ")
	for _, expected := range []string{
		"--network none", "--cpus 2.000", "--memory 2147483648",
		"--memory-swap 2147483648", "--storage-opt size=10737418240",
		"--pids-limit 512", "--read-only", "--cap-drop ALL",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("local argv missing %q: %s", expected, joined)
		}
	}
	if strings.Contains(joined, invocation.Request.CandidateID) ||
		strings.Contains(joined, invocation.Binding.LockDigest) {
		t.Fatalf("candidate data leaked into argv: %s", joined)
	}
}

func TestBuildxBackendRendersKubernetesDriverLimitsAndImmutableResult(t *testing.T) {
	t.Parallel()
	root, lock := copyLockFixture(t)
	request := testRequestAt(root, scannerpipeline.Step{
		Key: "build/default/linux-amd64", Kind: scannerpipeline.StepBuild,
		Timeout: 90 * time.Minute, Required: true,
	})
	request.LockDigest = lock.LockDigest
	invocation := testInvocationFromRequest(request)
	runtime := &recordingRuntime{}
	runtime.run = func(command Command) (CommandOutput, error) {
		for index, value := range command.Args {
			if value == "--metadata-file" && index+1 < len(command.Args) {
				metadata := []byte(`{"containerimage.digest":"` + testDigest + `"}`)
				if err := os.WriteFile(command.Args[index+1], metadata, 0o600); err != nil {
					return CommandOutput{}, err
				}
			}
		}
		return CommandOutput{}, nil
	}
	backend := BuildxBackend{
		Runtime: runtime, BuildxPath: "/usr/bin/docker",
		Registry:  "registry.example/wolf/scanners",
		Platforms: []string{"linux/amd64"}, Push: true,
	}
	result, err := backend.Execute(context.Background(), invocation)
	if err != nil {
		t.Fatal(err)
	}
	if result.Result.OutputDigest != testDigest ||
		!strings.HasSuffix(result.Result.OutputURI, "@"+testDigest) {
		t.Fatalf("buildx result = %#v", result)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	joined := commandArgs(runtime.commands)
	for _, expected := range []string{
		"create", "--driver kubernetes",
		"requests.cpu=4000m", "limits.memory=8589934592",
		"limits.ephemeral-storage=53687091200",
		"--platform linux/amd64", "--provenance=mode=max", "--sbom=true", "--push",
		"--build-arg WOLF_VERSION=candidate-1",
		filepath.Join(resolvedRoot, "scanners"),
		"rm --force",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("buildx commands missing %q:\n%s", expected, joined)
		}
	}
}

func TestBuildxBackendBuildsFixerEngineFromExactBaseDependency(t *testing.T) {
	t.Parallel()
	root, lock := copyLockFixture(t)
	request := testRequestAt(root, scannerpipeline.Step{
		Key: "build/fixer-codex/linux-amd64", Kind: scannerpipeline.StepBuild,
		DependsOn: []string{"image-manifest/fixer-base"},
		Timeout:   90 * time.Minute, Required: true,
	})
	request.LockDigest = lock.LockDigest
	request.Dependencies = map[string]scannerreleaseworker.DependencyEvidence{
		"image-manifest/fixer-base": {OutputDigest: testDigest},
	}
	invocation := testInvocationFromRequest(request)
	runtime := &recordingRuntime{}
	runtime.run = func(command Command) (CommandOutput, error) {
		for index, value := range command.Args {
			if value == "--metadata-file" && index+1 < len(command.Args) {
				if err := os.WriteFile(
					command.Args[index+1],
					[]byte(`{"containerimage.digest":"`+testDigest+`"}`), 0o600,
				); err != nil {
					return CommandOutput{}, err
				}
			}
		}
		return CommandOutput{}, nil
	}
	backend := BuildxBackend{
		Runtime: runtime, BuildxPath: "/usr/bin/docker",
		Registry:  "registry.example/wolf/scanners",
		Platforms: []string{"linux/amd64"}, Push: true,
	}
	result, err := backend.Execute(context.Background(), invocation)
	if err != nil {
		t.Fatal(err)
	}
	expectedReference := "oci://registry.example/wolf/scanners/wolf-fixer-codex@" + testDigest
	if result.Result.OutputURI != expectedReference {
		t.Fatalf("fixer output URI = %q, want %q", result.Result.OutputURI, expectedReference)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	joined := commandArgs(runtime.commands)
	for _, expected := range []string{
		"--file " + filepath.Join(resolvedRoot, "fixer", "Dockerfile.codex"),
		"--build-arg WOLF_FIXER_BASE_REF=registry.example/wolf/scanners/wolf-fixer@" + testDigest,
		"--tag registry.example/wolf/scanners/wolf-fixer-codex:operation-",
		resolvedRoot,
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("fixer buildx command missing %q:\n%s", expected, joined)
		}
	}
}

func TestManagedBuildxRequiresPushAndUsesBoundPrimaryRegistry(t *testing.T) {
	t.Parallel()
	backend := BuildxBackend{
		Runtime: ExecRuntime{}, BuildxPath: "/usr/bin/docker",
		Platforms: []string{"linux/amd64", "linux/arm64"},
		Push:      false, RequirePush: true, UseWorkspaceRegistry: true,
		KubernetesNamespace: "wolf-release-builds", BuildKitServiceAccount: "wolf-buildkit",
		RequireKubernetesIdentity: true,
		DockerConfigDirectory:     "/run/wolf/buildx-docker", RequireRegistryAuth: true,
	}
	if _, err := backend.Capabilities(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "requires registry push") {
		t.Fatalf("push-disabled managed capabilities error = %v", err)
	}
	backend.Push = true
	if _, err := backend.Capabilities(context.Background()); err != nil {
		t.Fatalf("push-capable managed capabilities: %v", err)
	}
	workspace := t.TempDir()
	if err := scannerreleaseworkspace.WriteContext(workspace, scannerreleaseworkspace.ExecutionContext{
		SourceURL: "https://git.example/wolf.git",
		Primary: scannerreleaseworkspace.RegistryTarget{
			ID: "primary", Version: 3, Host: "registry.bound.example",
			Namespace: "wolf", Repository: "managed/scanners",
		},
		Mirror: scannerreleaseworkspace.RegistryTarget{
			ID: "mirror", Version: 2, Host: "mirror.example",
			Namespace: "wolf", Repository: "managed/scanners",
		},
	}); err != nil {
		t.Fatal(err)
	}
	backend.Registry = "registry.unbound.example/escape"
	resolved, err := backend.registryBase(Invocation{Request: scannerreleaseworker.StepRequest{Workspace: workspace}})
	if err != nil {
		t.Fatal(err)
	}
	if resolved != "registry.bound.example/managed/scanners" {
		t.Fatalf("managed buildx registry = %q", resolved)
	}
}

func TestManagedBuildxDependentFixerUsesBoundWorkspaceRegistry(t *testing.T) {
	t.Parallel()
	root, lock := copyLockFixture(t)
	if err := scannerreleaseworkspace.WriteContext(root, scannerreleaseworkspace.ExecutionContext{
		SourceURL: "https://git.example/wolf.git",
		Primary: scannerreleaseworkspace.RegistryTarget{
			ID: "primary", Version: 3, Host: "registry.bound.example",
			Namespace: "wolf", Repository: "managed/scanners",
		},
		Mirror: scannerreleaseworkspace.RegistryTarget{
			ID: "mirror", Version: 2, Host: "mirror.example",
			Namespace: "wolf", Repository: "managed/scanners",
		},
	}); err != nil {
		t.Fatal(err)
	}
	request := testRequestAt(root, scannerpipeline.Step{
		Key: "build/fixer-codex/linux-amd64", Kind: scannerpipeline.StepBuild,
		DependsOn: []string{"image-manifest/fixer-base"},
		Timeout:   90 * time.Minute, Required: true,
	})
	request.LockDigest = lock.LockDigest
	request.Dependencies = map[string]scannerreleaseworker.DependencyEvidence{
		"image-manifest/fixer-base": {OutputDigest: testDigest},
	}
	runtime := &recordingRuntime{}
	runtime.run = func(command Command) (CommandOutput, error) {
		for index, value := range command.Args {
			if value == "--metadata-file" && index+1 < len(command.Args) {
				if err := os.WriteFile(
					command.Args[index+1],
					[]byte(`{"containerimage.digest":"`+testDigest+`"}`), 0o600,
				); err != nil {
					return CommandOutput{}, err
				}
			}
		}
		return CommandOutput{}, nil
	}
	dockerConfig := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(dockerConfig, "config.json"),
		[]byte(`{"auths":{"registry.bound.example":{"auth":"dGVzdDp0ZXN0"}}}`), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	backend := BuildxBackend{
		Runtime: runtime, BuildxPath: "/usr/bin/docker",
		Registry:  "registry.unbound.example/escape",
		Platforms: []string{"linux/amd64"}, Push: true,
		RequirePush: true, UseWorkspaceRegistry: true,
		KubernetesNamespace: "wolf-release-builds", BuildKitServiceAccount: "wolf-buildkit",
		RequireKubernetesIdentity: true,
		DockerConfigDirectory:     dockerConfig, RequireRegistryAuth: true,
	}
	result, err := backend.Execute(context.Background(), testInvocationFromRequest(request))
	if err != nil {
		t.Fatal(err)
	}
	if result.Result.OutputURI != "oci://registry.bound.example/managed/scanners/wolf-fixer-codex@"+testDigest {
		t.Fatalf("managed fixer output URI = %q", result.Result.OutputURI)
	}
	joined := commandArgs(runtime.commands)
	if !strings.Contains(
		joined,
		"--build-arg WOLF_FIXER_BASE_REF=registry.bound.example/managed/scanners/wolf-fixer@"+testDigest,
	) || strings.Contains(joined, "registry.unbound.example") {
		t.Fatalf("managed fixer escaped bound registry:\n%s", joined)
	}
	if !strings.Contains(joined, "namespace=wolf-release-builds") ||
		!strings.Contains(joined, "serviceaccount=wolf-buildkit") {
		t.Fatalf("managed BuildKit identity is missing:\n%s", joined)
	}
	for _, binding := range []string{
		"--provenance=mode=max,version=v1,builder-id=" + wolfBuildxBuilderID,
		"--build-arg WOLF_DEFINITION_COMMIT=" + testCommit,
		"--build-arg WOLF_LOCK_DIGEST=" + lock.LockDigest,
		"--build-arg WOLF_IMAGE_VARIANT=fixer-codex",
		wolfImageSource + ".git#" + testCommit,
	} {
		if !strings.Contains(joined, binding) {
			t.Fatalf("managed Buildx trust binding %q is missing:\n%s", binding, joined)
		}
	}
	for _, command := range runtime.commands {
		if !contains(command.Environment, "DOCKER_CONFIG="+dockerConfig) {
			t.Fatalf("Buildx command does not use target-bound Docker config: %#v", command.Environment)
		}
	}
}

func TestBuildxAttestationRequiresExactRunnableSubjectAndPredicates(t *testing.T) {
	t.Parallel()
	runnable := "sha256:" + strings.Repeat("a", 64)
	expectation := provenanceExpectation{
		imageTrustBinding: imageTrustBinding{
			Source: wolfImageSource, DefinitionCommit: testCommit, LockDigest: testLock,
			CandidateID: "candidate-1", ImageKind: "scanner", Variant: "default",
		},
		Platform: "linux/amd64",
	}
	statement := func(predicateType string, predicate any) []byte {
		value, err := json.Marshal(map[string]any{
			"_type": "https://in-toto.io/Statement/v1",
			"subject": []map[string]any{{
				"name": "wolf-scanners", "digest": map[string]string{
					"sha256": strings.TrimPrefix(runnable, "sha256:"),
				},
			}},
			"predicateType": predicateType, "predicate": predicate,
		})
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	provenancePredicate := map[string]any{
		"buildDefinition": map[string]any{
			"buildType": "https://github.com/moby/buildkit/blob/master/docs/attestations/slsa-definitions.md",
			"externalParameters": map[string]any{"request": map[string]any{
				"args": map[string]string{
					"build-arg:WOLF_DEFINITION_COMMIT": testCommit,
					"build-arg:WOLF_LOCK_DIGEST":       testLock,
					"build-arg:WOLF_CANDIDATE_ID":      "candidate-1",
					"build-arg:WOLF_IMAGE_KIND":        "scanner",
					"build-arg:WOLF_IMAGE_VARIANT":     "default",
					"build-arg:WOLF_BUILD_PLATFORM":    "linux/amd64",
				},
			}},
			"resolvedDependencies": []map[string]any{
				{"uri": wolfImageSource, "digest": map[string]string{"gitCommit": testCommit}},
				{"uri": "file:scanners/scanner-lock.yaml", "digest": map[string]string{
					"sha256": strings.TrimPrefix(testLock, "sha256:"),
				}},
			},
		},
		"runDetails": map[string]any{"builder": map[string]string{"id": wolfBuildxBuilderID}},
	}
	sbom := statement("https://spdx.dev/Document", map[string]any{"spdxVersion": "SPDX-2.3"})
	provenance := statement("https://slsa.dev/provenance/v1", provenancePredicate)
	blobs := map[string][]byte{
		testSHA256Digest(sbom): sbom, testSHA256Digest(provenance): provenance,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for digest, payload := range blobs {
			if strings.HasSuffix(r.URL.Path, "/"+digest) {
				_, _ = w.Write(payload)
				return
			}
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	registryHost := strings.TrimPrefix(server.URL, "http://")
	client := scannerregistry.Client{
		HTTP:      server.Client(),
		Endpoints: map[string]scannerregistry.Endpoint{registryHost: {BaseURL: server.URL}},
	}
	root := scannerregistry.Reference{Registry: registryHost, Repository: "wolf/scanners"}
	descriptor := scannerregistry.Descriptor{
		Digest:   "sha256:" + strings.Repeat("b", 64),
		Platform: scannerregistry.Platform{OS: "unknown", Architecture: "unknown"},
		Annotations: map[string]string{
			"vnd.docker.reference.type":   "attestation-manifest",
			"vnd.docker.reference.digest": runnable,
		},
	}
	content, err := json.Marshal(map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.manifest.v1+json",
		"subject": map[string]any{
			"mediaType": "application/vnd.oci.image.manifest.v1+json",
			"digest":    runnable, "size": 42,
		},
		"layers": []map[string]any{
			{
				"mediaType": "application/vnd.in-toto+json",
				"digest":    testSHA256Digest(sbom), "size": len(sbom),
				"annotations": map[string]string{"in-toto.io/predicate-type": "https://spdx.dev/Document"},
			},
			{
				"mediaType": "application/vnd.in-toto+json",
				"digest":    testSHA256Digest(provenance), "size": len(provenance),
				"annotations": map[string]string{"in-toto.io/predicate-type": "https://slsa.dev/provenance/v1"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := inspectBuildxAttestation(
		context.Background(), client, root, descriptor, content, runnable, expectation,
	)
	if err != nil {
		t.Fatal(err)
	}
	if evidence["sbom"].PayloadDigest != testSHA256Digest(sbom) ||
		evidence["provenance"].PayloadDigest != testSHA256Digest(provenance) ||
		evidence["provenance"].ManifestDigest != descriptor.Digest ||
		evidence["provenance"].BuilderID != wolfBuildxBuilderID ||
		!buildxAttestationDescriptor(descriptor) {
		t.Fatalf("Buildx attestation evidence = %#v", evidence)
	}
	descriptor.Annotations["vnd.docker.reference.digest"] = "sha256:" + strings.Repeat("e", 64)
	if _, err := inspectBuildxAttestation(
		context.Background(), client, root, descriptor, content, runnable, expectation,
	); err == nil {
		t.Fatal("Buildx attestation with a mismatched subject was accepted")
	}
}

func TestBuildxProvenanceRejectsHostilePayloadBindings(t *testing.T) {
	t.Parallel()
	runnable := "sha256:" + strings.Repeat("a", 64)
	expected := provenanceExpectation{imageTrustBinding: imageTrustBinding{
		Source: wolfImageSource, DefinitionCommit: testCommit, LockDigest: testLock,
		CandidateID: "candidate-1", ImageKind: "scanner", Variant: "default",
	}, Platform: "linux/amd64"}
	baseParameters := map[string]string{
		"WOLF_DEFINITION_COMMIT": testCommit, "WOLF_LOCK_DIGEST": testLock,
		"WOLF_CANDIDATE_ID": "candidate-1", "WOLF_IMAGE_KIND": "scanner",
		"WOLF_IMAGE_VARIANT": "default", "WOLF_BUILD_PLATFORM": "linux/amd64",
	}
	baseMaterials := []slsaMaterial{
		{URI: wolfImageSource, Digest: map[string]string{"gitCommit": testCommit}},
		{URI: "file:scanners/scanner-lock.yaml", Digest: map[string]string{
			"sha256": strings.TrimPrefix(testLock, "sha256:"),
		}},
	}
	for _, test := range []struct {
		name       string
		builder    string
		parameters map[string]string
		materials  []slsaMaterial
	}{
		{name: "wrong builder", builder: "https://attacker.example/builder", parameters: baseParameters, materials: baseMaterials},
		{name: "wrong lock invocation", builder: wolfBuildxBuilderID, parameters: func() map[string]string {
			value := cloneStrings(baseParameters)
			value["WOLF_LOCK_DIGEST"] = testDigest
			return value
		}(), materials: baseMaterials},
		{name: "missing source material", builder: wolfBuildxBuilderID, parameters: baseParameters, materials: baseMaterials[1:]},
	} {
		t.Run(test.name, func(t *testing.T) {
			predicate, _ := json.Marshal(map[string]any{
				"buildDefinition": map[string]any{
					"buildType":            "https://github.com/moby/buildkit/blob/master/docs/attestations/slsa-definitions.md",
					"externalParameters":   map[string]any{"request": map[string]any{"buildArgs": test.parameters}},
					"resolvedDependencies": test.materials,
				},
				"runDetails": map[string]any{"builder": map[string]string{"id": test.builder}},
			})
			statement, _ := json.Marshal(map[string]any{
				"_type": "https://in-toto.io/Statement/v1",
				"subject": []map[string]any{{"name": "image", "digest": map[string]string{
					"sha256": strings.TrimPrefix(runnable, "sha256:"),
				}}},
				"predicateType": "https://slsa.dev/provenance/v1",
				"predicate":     json.RawMessage(predicate),
			})
			if _, _, err := inspectInTotoPayload(statement, runnable, expected); err == nil {
				t.Fatal("hostile provenance payload was accepted")
			}
		})
	}
}

func TestBuildxBackendRejectsFixerEngineWithoutVerifiedBaseDependency(t *testing.T) {
	t.Parallel()
	root, lock := copyLockFixture(t)
	request := testRequestAt(root, scannerpipeline.Step{
		Key: "build/fixer-api/linux-amd64", Kind: scannerpipeline.StepBuild,
		DependsOn: []string{"image-manifest/fixer-base"},
		Timeout:   90 * time.Minute, Required: true,
	})
	request.LockDigest = lock.LockDigest
	backend := BuildxBackend{
		Runtime: &recordingRuntime{}, BuildxPath: "/usr/bin/docker",
		Registry:  "registry.example/wolf/scanners",
		Platforms: []string{"linux/amd64"}, Push: true,
	}
	if _, err := backend.Execute(
		context.Background(), testInvocationFromRequest(request),
	); !errors.Is(err, ErrBinding) {
		t.Fatalf("missing fixer dependency error = %v", err)
	}
}

func TestKubernetesBackendManifestAndExecutionAreBoundedAndHardened(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	workspace := filepath.Join(root, "candidate")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	invocation := testInvocationFromRequest(testRequestAt(workspace, scannerpipeline.Step{
		Key: "vulnerability-scan/default", Kind: scannerpipeline.StepSecurity,
		Timeout: 30 * time.Minute, Required: true,
	}))
	sandboxInvocation := invocation
	sandboxInvocation.Request.Workspace = "/workspace"
	var (
		captured map[string]any
		deleted  atomic.Bool
		posts    atomic.Int64
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		switch r.Method {
		case http.MethodPost:
			posts.Add(1)
			if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
				t.Fatal(err)
			}
			args := nestedSliceStrings(t, captured, "spec", "template", "spec", "containers", "0", "args")
			resultPath := argumentAfter(args, "--result")
			resultPath = filepath.Join(workspace, strings.TrimPrefix(resultPath, "/workspace/"))
			if err := os.MkdirAll(filepath.Dir(resultPath), 0o700); err != nil {
				t.Fatal(err)
			}
			value, _ := json.Marshal(BackendResult{
				Binding: invocation.Binding,
				Result:  scannerreleaseworker.StepResult{OutputDigest: testDigest},
			})
			if err := os.WriteFile(resultPath, value, 0o600); err != nil {
				t.Fatal(err)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{}`))
		case http.MethodGet:
			_, _ = w.Write([]byte(
				`{"metadata":{"annotations":{"wolf.dev/invocation-digest":"` +
					invocationDigest(sandboxInvocation) +
					`"}},"status":{"succeeded":1}}`,
			))
		case http.MethodDelete:
			deleted.Store(true)
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer server.Close()
	backend := KubernetesBackend{
		APIServer: server.URL, Namespace: "release-builds", Token: "test-token",
		Instance: "wolf-enterprise", ExecutionLane: KubernetesExecutionLaneQuality,
		HTTPClient: server.Client(), WorkspacePVC: "release-workspace",
		WorkspaceRoot: root,
		Image:         "registry.example/wolf-step@sha256:" + strings.Repeat("d", 64),
		PollInterval:  time.Millisecond, JobTTLSeconds: 60,
	}
	result, err := backend.Execute(context.Background(), invocation)
	if err != nil {
		t.Fatal(err)
	}
	if result.Result.OutputDigest != testDigest || !deleted.Load() {
		t.Fatalf("Kubernetes result=%#v deleted=%t", result, deleted.Load())
	}
	retryInvocation := invocation
	retryInvocation.Request.StepAttempt = 2
	if retryInvocation.OperationID != scannerreleaseworker.DeriveLogicalOperationID(retryInvocation.Request) {
		t.Fatal("diagnostic retry changed Kubernetes logical operation identity")
	}
	replayed, err := backend.Execute(context.Background(), retryInvocation)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Result.OutputDigest != testDigest || posts.Load() != 1 {
		t.Fatalf("durable replay=%#v create requests=%d", replayed, posts.Load())
	}
	spec := nestedMapAny(t, captured, "spec")
	if spec["activeDeadlineSeconds"] != float64(1800) && spec["activeDeadlineSeconds"] != int64(1800) {
		t.Fatalf("active deadline = %#v", spec["activeDeadlineSeconds"])
	}
	pod := nestedMapAny(t, captured, "spec", "template", "spec")
	if pod["automountServiceAccountToken"] != false || pod["restartPolicy"] != "Never" {
		t.Fatalf("pod security = %#v", pod)
	}
	podLabels := nestedMapAny(t, captured, "spec", "template", "metadata", "labels")
	if podLabels["app.kubernetes.io/instance"] != "wolf-enterprise" ||
		podLabels[KubernetesExecutionLaneLabel] != KubernetesExecutionLaneQuality {
		t.Fatalf("pod execution labels = %#v", podLabels)
	}
	container := nestedArrayMap(t, pod, "containers")[0]
	mounts := nestedArrayMap(t, container, "volumeMounts")
	workspaceMount := mounts[0]
	if workspaceMount["mountPath"] != "/workspace" || workspaceMount["subPath"] != "candidate" ||
		workspaceMount["readOnly"] != true {
		t.Fatalf("workspace mount exposes PVC root: %#v", workspaceMount)
	}
	operationMount := mounts[1]
	expectedOperationPath := "candidate/.wolf-release-backend-journal/" +
		strings.TrimPrefix(invocation.OperationID, "sha256:")
	if operationMount["mountPath"] != operationSandboxDirectory(invocation.OperationID) ||
		operationMount["subPath"] != expectedOperationPath || operationMount["readOnly"] == true {
		t.Fatalf("operation mount is not an isolated writable overlay: %#v", operationMount)
	}
	if mounts[2]["mountPath"] != "/tmp" || mounts[3]["mountPath"] != "/work" {
		t.Fatalf("isolated scratch mounts = %#v", mounts)
	}
	security := container["securityContext"].(map[string]any)
	if security["allowPrivilegeEscalation"] != false ||
		security["readOnlyRootFilesystem"] != true {
		t.Fatalf("container security = %#v", security)
	}
	resources := container["resources"].(map[string]any)
	limits := resources["limits"].(map[string]any)
	if limits["cpu"] != "2000m" || limits["memory"] != "4294967296" ||
		limits["ephemeral-storage"] != "21474836480" {
		t.Fatalf("Kubernetes limits = %#v", limits)
	}
	args := stringSlice(container["args"])
	joined := strings.Join(args, " ")
	if strings.Contains(joined, invocation.Request.CandidateID) ||
		strings.Contains(joined, invocation.Binding.LockDigest) {
		t.Fatalf("candidate data leaked into Job argv: %s", joined)
	}
}

func TestKubernetesBackendRecreatesMissingStartedJobAndRecoversDurableResult(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	workspace := filepath.Join(root, "candidate")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	invocation := testInvocationFromRequest(testRequestAt(workspace, scannerpipeline.Step{
		Key: "candidate-publish/default", Kind: scannerpipeline.StepPublish,
		Timeout: 30 * time.Minute, Required: true,
	}))
	var (
		posts      atomic.Int64
		resultPath string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			posts.Add(1)
			value, _ := json.Marshal(BackendResult{
				Binding: invocation.Binding,
				Result:  scannerreleaseworker.StepResult{OutputDigest: testDigest},
			})
			if err := os.WriteFile(resultPath, value, 0o600); err != nil {
				t.Error(err)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{}`))
		case http.MethodGet:
			http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
		case http.MethodDelete:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer server.Close()
	backend := KubernetesBackend{
		APIServer: server.URL, Namespace: "release-builds", Token: "test-token",
		HTTPClient: server.Client(), WorkspacePVC: "release-workspace",
		WorkspaceRoot: root,
		Image:         "registry.example/wolf-step@sha256:" + strings.Repeat("d", 64),
		PollInterval:  time.Millisecond, JobTTLSeconds: 60,
	}
	requestPath, _, err := backend.operationPaths(invocation)
	if err != nil {
		t.Fatal(err)
	}
	resultPath = filepath.Join(filepath.Dir(requestPath), "result.json")
	if err := os.MkdirAll(filepath.Dir(requestPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(filepath.Dir(requestPath), "started"),
		func() []byte {
			sandbox := invocation
			sandbox.Request.Workspace = "/workspace"
			return []byte(invocationDigest(sandbox) + "\n")
		}(), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	result, err := backend.Execute(context.Background(), invocation)
	if err != nil {
		t.Fatalf("missing Job recovery failed: %v", err)
	}
	if result.Result.OutputDigest != testDigest || posts.Load() != 1 {
		t.Fatalf("recovery result=%#v recreate posts=%d", result, posts.Load())
	}
}

func TestKubernetesBackendRejectsWholePVCAndTraversalWorkspaces(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	backend := KubernetesBackend{WorkspaceRoot: root}
	rootInvocation := testInvocationFromRequest(testRequestAt(root, scannerpipeline.Step{
		Key: "manifest-validate", Kind: scannerpipeline.StepValidation,
		Timeout: time.Minute, Required: true,
	}))
	if _, _, err := backend.sandboxInvocation(rootInvocation); err == nil ||
		!strings.Contains(err.Error(), "isolated PVC subdirectory") {
		t.Fatalf("whole-PVC workspace error = %v", err)
	}
	outside := t.TempDir()
	escape := filepath.Join(root, "escape")
	if err := os.Symlink(outside, escape); err != nil {
		t.Fatal(err)
	}
	escapeInvocation := testInvocationFromRequest(testRequestAt(escape, scannerpipeline.Step{
		Key: "manifest-validate", Kind: scannerpipeline.StepValidation,
		Timeout: time.Minute, Required: true,
	}))
	if _, _, err := backend.sandboxInvocation(escapeInvocation); err == nil {
		t.Fatal("symlinked cross-PVC workspace was accepted")
	}
}

func TestKubernetesJournalRejectsSymlinkedDirectoriesAndFiles(t *testing.T) {
	t.Parallel()
	operationID := "sha256:" + strings.Repeat("a", 64)
	for _, test := range []struct {
		name  string
		setup func(t *testing.T, workspace, outside string)
	}{
		{
			name: "journal-directory",
			setup: func(t *testing.T, workspace, outside string) {
				t.Helper()
				if err := os.Symlink(outside, filepath.Join(workspace, ".wolf-release-backend-journal")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "operation-directory",
			setup: func(t *testing.T, workspace, outside string) {
				t.Helper()
				journal := filepath.Join(workspace, ".wolf-release-backend-journal")
				if err := os.Mkdir(journal, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(journal, strings.TrimPrefix(operationID, "sha256:"))); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			workspace, outside := t.TempDir(), t.TempDir()
			test.setup(t, workspace, outside)
			if journal, err := openKubernetesOperationJournal(workspace, operationID); err == nil {
				_ = journal.Close()
				t.Fatal("symlinked Kubernetes journal directory was accepted")
			}
		})
	}

	workspace, outside := t.TempDir(), t.TempDir()
	journal, err := openKubernetesOperationJournal(workspace, operationID)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	target := filepath.Join(outside, "target")
	if err := os.WriteFile(target, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	operationPath := filepath.Join(
		workspace, ".wolf-release-backend-journal", strings.TrimPrefix(operationID, "sha256:"),
	)
	for _, name := range []string{"result.json", "started"} {
		if err := os.Symlink(target, filepath.Join(operationPath, name)); err != nil {
			t.Fatal(err)
		}
		if _, _, err := readKubernetesJournalFile(journal, name, maxBackendResultBytes); err == nil {
			t.Fatalf("symlinked Kubernetes journal file %q was accepted", name)
		}
		if err := os.Remove(filepath.Join(operationPath, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(target, filepath.Join(operationPath, "request.json")); err != nil {
		t.Fatal(err)
	}
	if err := writeKubernetesJournalFile(journal, "request.json", []byte("safe-request")); err != nil {
		t.Fatal(err)
	}
	if value, err := os.ReadFile(target); err != nil || string(value) != "untouched" {
		t.Fatalf("request symlink target was modified: %q, %v", value, err)
	}
	if value, err := os.ReadFile(filepath.Join(operationPath, "request.json")); err != nil ||
		string(value) != "safe-request" {
		t.Fatalf("safe request replacement = %q, %v", value, err)
	}
}

func TestKubernetesAdapterTokenMountIsExplicitWorkloadIdentityOnly(t *testing.T) {
	t.Parallel()
	invocation := testInvocationFromRequest(testRequestAt(t.TempDir(), scannerpipeline.Step{
		Key: "manifest-validate", Kind: scannerpipeline.StepValidation,
		Timeout: time.Minute, Required: true,
	}))
	base := KubernetesBackend{
		WorkspaceRoot: "/workspace", AdapterWorkloadIdentity: true,
		ServiceAccount: "wolf-fixed-adapter",
	}
	job := base.renderJob(
		invocation, "/workspace/.journal/request.json",
		"/workspace/.journal/result.json", "build-one",
	)
	pod := nestedMapAny(t, job, "spec", "template", "spec")
	if pod["automountServiceAccountToken"] != true ||
		pod["serviceAccountName"] != "wolf-fixed-adapter" {
		t.Fatalf("secretless workload identity pod = %#v", pod)
	}

	base.AdapterWorkloadIdentity = false
	base.AdapterRegistryCredentialSecret = "fixed-registry-credentials"
	base.AdapterRegistryCredentialMount = "/run/wolf/adapter-registry"
	base.AdapterEngineCredentialSecret = "fixed-engine-credentials"
	base.AdapterEngineCredentialMount = "/run/wolf/adapter-engine"
	job = base.renderJob(
		invocation, "/workspace/.journal/request.json",
		"/workspace/.journal/result.json", "build-two",
	)
	pod = nestedMapAny(t, job, "spec", "template", "spec")
	if pod["automountServiceAccountToken"] != false {
		t.Fatalf("secret-only adapter received service-account token: %#v", pod)
	}
	container := nestedArrayMap(t, pod, "containers")[0]
	environment := nestedArrayMap(t, container, "env")
	envValues := map[string]string{}
	for _, item := range environment {
		name, _ := item["name"].(string)
		value, _ := item["value"].(string)
		envValues[name] = value
	}
	if envValues["WOLF_SCANNER_RELEASE_REGISTRY_CREDENTIAL_DIR"] != "/run/wolf/adapter-registry" ||
		envValues["WOLF_SCANNER_RELEASE_ENGINE_CREDENTIAL_DIR"] != "/run/wolf/adapter-engine" {
		t.Fatalf("split adapter credential environments = %#v", envValues)
	}
	volumes := nestedArrayMap(t, pod, "volumes")
	secretNames := map[string]string{}
	for _, volume := range volumes {
		secret, _ := volume["secret"].(map[string]any)
		if secret != nil {
			secretNames[volume["name"].(string)], _ = secret["secretName"].(string)
		}
	}
	if secretNames["adapter-registry-credentials"] != "fixed-registry-credentials" ||
		secretNames["adapter-engine-credentials"] != "fixed-engine-credentials" {
		t.Fatalf("split adapter credential volumes = %#v", secretNames)
	}
}

func TestKubernetesSigningJobAlwaysUsesSignerExecutionLane(t *testing.T) {
	t.Parallel()
	invocation := testInvocationFromRequest(testRequestAt(t.TempDir(), scannerpipeline.Step{
		Key: "release-manifest-signature", Kind: scannerpipeline.StepEvidence,
		Timeout: time.Minute, Required: true,
	}))
	backend := KubernetesBackend{
		WorkspaceRoot: "/workspace", ExecutionLane: KubernetesExecutionLaneFixed,
		SignerProfileSecret: "signer-profile", SignerAdapterPath: "/usr/local/bin/signer",
	}
	job := backend.renderJob(
		invocation, "/workspace/.journal/request.json",
		"/workspace/.journal/result.json", "build-one",
	)
	spec := job["spec"].(map[string]any)
	template := spec["template"].(map[string]any)
	metadata := template["metadata"].(map[string]any)
	labels := metadata["labels"].(map[string]string)
	if labels[KubernetesExecutionLaneLabel] != KubernetesExecutionLaneSigner {
		t.Fatalf("signing pod lane labels = %#v", labels)
	}
}

func completeCapabilities(
	name string,
	actions []string,
	kinds []scannerpipeline.StepKind,
) Capabilities {
	gib := int64(1 << 30)
	return Capabilities{
		Name: name, Actions: actions, Kinds: kinds,
		MaxCPU: 64000, MaxMemory: 256 * gib, MaxDisk: 1024 * gib,
		MaxTimeout: 24 * time.Hour, MaxConcurrency: 64,
		EnforcesCPU: true, EnforcesMemory: true, EnforcesDisk: true,
		EnforcesTimeout: true, EnforcesCancellation: true, Idempotent: true,
		ExternalIdempotency: true,
	}
}

func testRequest(t *testing.T, step scannerpipeline.Step) scannerreleaseworker.StepRequest {
	t.Helper()
	return testRequestAt(t.TempDir(), step)
}

func testRequestAt(workspace string, step scannerpipeline.Step) scannerreleaseworker.StepRequest {
	request := scannerreleaseworker.StepRequest{
		BuildRunID: "build-1", CandidateID: "candidate-1",
		BuildAttempt: 1, Step: step, StepAttempt: 1,
		Workspace: workspace, DefinitionCommit: testCommit,
		LockDigest: testLock, PolicyID: "policy-1", PolicyRevision: 7,
		PlatformsJSON: `[{"key":"default","platforms":["linux/amd64"]}]`,
	}
	request.LogicalOperationID = scannerreleaseworker.DeriveLogicalOperationID(request)
	return request
}

func testInvocation(
	t *testing.T,
	key string,
	kind scannerpipeline.StepKind,
) Invocation {
	t.Helper()
	return testInvocationFromRequest(testRequest(t, scannerpipeline.Step{
		Key: key, Kind: kind, Timeout: 90 * time.Minute, Required: true,
	}))
}

func testInvocationFromRequest(request scannerreleaseworker.StepRequest) Invocation {
	action, resources, err := DefaultResourcePolicy().Resolve(request.Step)
	if err != nil {
		panic(err)
	}
	binding := Binding{
		DefinitionCommit: request.DefinitionCommit, LockDigest: request.LockDigest,
		PolicyID: request.PolicyID, PolicyRevision: request.PolicyRevision,
	}
	return Invocation{
		OperationID: operationID(request, action), Request: request,
		Action: action, Resources: resources, Binding: binding,
	}
}

func copyLockFixture(t *testing.T) (string, *scannerlock.Lock) {
	t.Helper()
	source, err := manifest.FindRepoRoot("")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for _, relative := range []string{
		scannerlock.DefaultLockPath,
		"scanners/Dockerfile",
		"scanners/Dockerfile.min",
		"scanners/Dockerfile.jvm",
		"scanners/Dockerfile.rust",
		"scanners/Dockerfile.codeql",
		"fixer/Dockerfile.base",
		"fixer/Dockerfile.api",
		"fixer/Dockerfile.claude",
		"fixer/Dockerfile.codex",
	} {
		value, err := os.ReadFile(filepath.Join(source, relative))
		if err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(root, relative)
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, value, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	lock, err := scannerlock.LoadFile(filepath.Join(root, scannerlock.DefaultLockPath))
	if err != nil {
		t.Fatal(err)
	}
	return root, lock
}

func commandArgs(commands []Command) string {
	lines := make([]string, len(commands))
	for index, command := range commands {
		lines[index] = command.Path + " " + strings.Join(command.Args, " ")
	}
	return strings.Join(lines, "\n")
}

func testSHA256Digest(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func cloneStrings(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func nestedMapAny(t *testing.T, root map[string]any, keys ...string) map[string]any {
	t.Helper()
	var current any = root
	for _, key := range keys {
		object, ok := current.(map[string]any)
		if !ok {
			t.Fatalf("%q is not an object in %#v", key, current)
		}
		current = object[key]
	}
	result, ok := current.(map[string]any)
	if !ok {
		t.Fatalf("value is not an object: %#v", current)
	}
	return result
}

func nestedArrayMap(t *testing.T, root map[string]any, key string) []map[string]any {
	t.Helper()
	switch value := root[key].(type) {
	case []map[string]any:
		return value
	case []any:
		out := make([]map[string]any, len(value))
		for index, item := range value {
			var ok bool
			out[index], ok = item.(map[string]any)
			if !ok {
				t.Fatalf("%s[%d] is not map: %#v", key, index, item)
			}
		}
		return out
	default:
		t.Fatalf("%s is not []map: %#v", key, root[key])
		return nil
	}
}

func nestedSliceStrings(
	t *testing.T,
	root map[string]any,
	keys ...string,
) []string {
	t.Helper()
	var current any = root
	for _, key := range keys {
		if index := parseIndex(key); index >= 0 {
			array := current.([]any)
			current = array[index]
			continue
		}
		current = current.(map[string]any)[key]
	}
	return stringSlice(current)
}

func parseIndex(value string) int {
	if value == "0" {
		return 0
	}
	return -1
}

func stringSlice(value any) []string {
	switch values := value.(type) {
	case []string:
		return values
	case []any:
		out := make([]string, len(values))
		for index, value := range values {
			out[index], _ = value.(string)
		}
		return out
	default:
		return nil
	}
}

func argumentAfter(args []string, name string) string {
	for index, value := range args {
		if value == name && index+1 < len(args) {
			return args[index+1]
		}
	}
	return ""
}
