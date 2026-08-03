package scannerreleasebackend

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/alphabravocompany/thewolf/internal/scannerpipeline"
	"github.com/alphabravocompany/thewolf/internal/scannerreleaseworker"
)

const (
	maxBackendResultBytes = 4 << 20
	maxBackendLogBytes    = 64 << 10
)

var (
	fullCommitPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)
	digestPattern     = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,255}$`)
	secretPattern     = regexp.MustCompile(`(?i)(token|password|secret|api[_-]?key|authorization)\s*[:=]\s*[^\s,;]+`)
	bearerPattern     = regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]+`)
)

// Executor adds immutable binding, exhaustive policy mapping, resource
// capability checks, cancellation, concurrency, bounded redaction, and
// workspace-scoped idempotency to any concrete Backend.
type Executor struct {
	Backend Backend
	Policy  ResourcePolicy

	mu         sync.Mutex
	semaphores map[string]chan struct{}
	operations map[string]*keyedLock
}

type keyedLock struct {
	mutex sync.Mutex
	refs  int
}

var _ scannerreleaseworker.Executor = (*Executor)(nil)

func NewExecutor(backend Backend, policy ResourcePolicy) (*Executor, error) {
	if backend == nil {
		return nil, errors.New("scanner release backend is required")
	}
	if len(policy.ByKind) == 0 {
		policy = DefaultResourcePolicy()
	}
	return &Executor{
		Backend: backend, Policy: policy,
		semaphores: make(map[string]chan struct{}),
		operations: make(map[string]*keyedLock),
	}, nil
}

func (e *Executor) Execute(
	ctx context.Context,
	request scannerreleaseworker.StepRequest,
) (scannerreleaseworker.StepResult, error) {
	if err := validateRequest(request); err != nil {
		return scannerreleaseworker.StepResult{}, err
	}
	action, resources, err := e.Policy.Resolve(request.Step)
	if err != nil {
		return scannerreleaseworker.StepResult{}, err
	}
	capabilities, err := e.Backend.Capabilities(ctx)
	if err != nil {
		return scannerreleaseworker.StepResult{}, fmt.Errorf("load %s backend capabilities: %w", e.Backend.Name(), err)
	}
	if err := authorizeCapability(capabilities, action, resources); err != nil {
		return scannerreleaseworker.StepResult{}, err
	}
	binding := Binding{
		DefinitionCommit: request.DefinitionCommit,
		LockDigest:       request.LockDigest, PolicyID: request.PolicyID,
		PolicyRevision: request.PolicyRevision,
	}
	operationID := operationID(request, action)
	invocation := Invocation{
		OperationID: operationID, Request: request, Action: action,
		Resources: resources, Binding: binding,
	}

	releaseOperation := e.lockOperation(operationID)
	defer releaseOperation()
	if result, found, err := loadCachedResult(request.Workspace, invocation); err != nil {
		return scannerreleaseworker.StepResult{}, err
	} else if found {
		return result, nil
	}

	release, err := e.acquire(ctx, capabilities.Name+":"+string(action.Kind), min(resources.MaxConcurrency, capabilities.MaxConcurrency))
	if err != nil {
		return scannerreleaseworker.StepResult{}, err
	}
	defer release()
	executionContext, cancel := context.WithTimeout(ctx, resources.Timeout)
	defer cancel()
	backendResult, err := e.Backend.Execute(executionContext, invocation)
	if err != nil {
		if executionContext.Err() != nil {
			return scannerreleaseworker.StepResult{}, executionContext.Err()
		}
		if errors.Is(err, ErrAmbiguousResult) {
			return scannerreleaseworker.StepResult{}, fmt.Errorf(
				"%w: %s backend step %q: %s",
				scannerreleaseworker.ErrReconciliationRequired,
				e.Backend.Name(), request.Step.Key, redact(err.Error(), 4096),
			)
		}
		return scannerreleaseworker.StepResult{}, fmt.Errorf(
			"%s backend step %q failed: %s",
			e.Backend.Name(), request.Step.Key, redact(err.Error(), 4096),
		)
	}
	if backendResult.Binding != binding {
		return scannerreleaseworker.StepResult{}, fmt.Errorf("%w for step %q", ErrBinding, request.Step.Key)
	}
	if externalSideEffect(action.Name) &&
		backendResult.ExternalOperationID != operationID {
		return scannerreleaseworker.StepResult{}, fmt.Errorf(
			"%w: backend did not acknowledge external operation ID for step %q",
			ErrBinding, request.Step.Key,
		)
	}
	result := backendResult.Result
	result.Verification.DefinitionCommit = binding.DefinitionCommit
	result.Verification.LockDigest = binding.LockDigest
	result.Verification.PolicyID = binding.PolicyID
	result.Verification.PolicyRevision = binding.PolicyRevision
	if result.Summary == nil {
		result.Summary = make(map[string]any)
	}
	result.Summary["backend"] = capabilities.Name
	result.Summary["operation_id"] = operationID
	result.Summary["executed_attempt"] = request.StepAttempt
	result.Summary["resources"] = resources
	if backendResult.Log != "" {
		result.Summary["backend_log"] = redact(backendResult.Log, maxBackendLogBytes)
	}
	result, err = redactStepResult(result)
	if err != nil {
		return scannerreleaseworker.StepResult{}, err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return scannerreleaseworker.StepResult{}, fmt.Errorf("encode backend result: %w", err)
	}
	if len(encoded) > maxBackendResultBytes {
		return scannerreleaseworker.StepResult{}, fmt.Errorf(
			"backend result exceeds %d bytes", maxBackendResultBytes,
		)
	}
	if err := validateResult(result); err != nil {
		return scannerreleaseworker.StepResult{}, err
	}
	if err := storeCachedResult(request.Workspace, invocation, result); err != nil {
		return scannerreleaseworker.StepResult{}, err
	}
	return result, nil
}

func validateRequest(request scannerreleaseworker.StepRequest) error {
	switch {
	case !identifierPattern.MatchString(request.BuildRunID),
		!identifierPattern.MatchString(request.CandidateID):
		return errors.New("scanner release backend request IDs are invalid")
	case request.BuildAttempt <= 0 || request.StepAttempt <= 0:
		return errors.New("scanner release backend attempts must be positive")
	case !digestPattern.MatchString(request.LogicalOperationID) ||
		request.LogicalOperationID != scannerreleaseworker.DeriveLogicalOperationID(request):
		return errors.New("scanner release backend logical operation ID is invalid")
	case !fullCommitPattern.MatchString(request.DefinitionCommit):
		return errors.New("scanner release backend requires a full lowercase Git SHA-1")
	case !digestPattern.MatchString(request.LockDigest):
		return errors.New("scanner release backend lock digest is invalid")
	case !identifierPattern.MatchString(request.PolicyID) || request.PolicyRevision <= 0:
		return errors.New("scanner release backend policy binding is invalid")
	case !filepath.IsAbs(request.Workspace):
		return errors.New("scanner release backend workspace must be absolute")
	}
	info, err := os.Lstat(request.Workspace)
	if err != nil {
		return fmt.Errorf("inspect scanner release backend workspace: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("scanner release backend workspace must be a real directory")
	}
	return nil
}

func authorizeCapability(capability Capabilities, action Action, resources Resources) error {
	if capability.Name == "" || capability.MaxCPU <= 0 ||
		capability.MaxMemory <= 0 || capability.MaxDisk <= 0 ||
		capability.MaxTimeout <= 0 || capability.MaxConcurrency <= 0 {
		return fmt.Errorf("%w: backend capability advertisement is incomplete", ErrResourcePolicy)
	}
	if !capability.EnforcesCPU || !capability.EnforcesMemory ||
		!capability.EnforcesDisk || !capability.EnforcesTimeout ||
		!capability.EnforcesCancellation || !capability.Idempotent {
		return fmt.Errorf("%w: backend %q does not enforce every mandatory control", ErrResourcePolicy, capability.Name)
	}
	if !supportsAction(capability.Actions, action.Name) ||
		!containsKind(capability.Kinds, action.Kind) {
		return fmt.Errorf("%w: backend %q action %q", ErrUnsupportedStep, capability.Name, action.Name)
	}
	if externalSideEffect(action.Name) && !capability.ExternalIdempotency {
		return fmt.Errorf(
			"%w: backend %q does not provide external idempotency for action %q",
			ErrResourcePolicy, capability.Name, action.Name,
		)
	}
	if action.Platform != "" && len(capability.Platforms) != 0 &&
		!contains(capability.Platforms, action.Platform) {
		return fmt.Errorf("%w: backend %q platform %q", ErrUnsupportedStep, capability.Name, action.Platform)
	}
	if resources.CPUMilli > capability.MaxCPU ||
		resources.MemoryBytes > capability.MaxMemory ||
		resources.DiskBytes > capability.MaxDisk ||
		resources.Timeout > capability.MaxTimeout {
		return fmt.Errorf("%w: step %q exceeds backend %q capacity", ErrResourcePolicy, action.Name, capability.Name)
	}
	return nil
}

func supportsAction(supported []string, action string) bool {
	for _, candidate := range supported {
		if candidate == action {
			return true
		}
		if strings.HasSuffix(candidate, "/*") &&
			strings.HasPrefix(action, strings.TrimSuffix(candidate, "*")) {
			return true
		}
	}
	return false
}

func containsKind(values []scannerpipeline.StepKind, target scannerpipeline.StepKind) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func operationID(request scannerreleaseworker.StepRequest, _ Action) string {
	return request.LogicalOperationID
}

func externalSideEffect(action string) bool {
	return strings.HasPrefix(action, "build/") ||
		strings.HasPrefix(action, "image-manifest/") ||
		strings.HasPrefix(action, "candidate-publish/") ||
		strings.HasPrefix(action, "signature/") ||
		action == "release-manifest-signature" ||
		action == "mirror-copy-verify" ||
		action == "mirror-release-closure-verify"
}

// RequiresExternalIdempotency reports whether an action can leave an
// externally visible registry, signature, or mirror side effect.
func RequiresExternalIdempotency(action string) bool {
	return externalSideEffect(action)
}

// ValidateInvocation revalidates the complete immutable payload inside a
// sandbox before a step executable is allowed to run.
func ValidateInvocation(invocation Invocation) error {
	if err := validateRequest(invocation.Request); err != nil {
		return err
	}
	action, resources, err := DefaultResourcePolicy().Resolve(invocation.Request.Step)
	if err != nil {
		return err
	}
	expectedBinding := Binding{
		DefinitionCommit: invocation.Request.DefinitionCommit,
		LockDigest:       invocation.Request.LockDigest,
		PolicyID:         invocation.Request.PolicyID,
		PolicyRevision:   invocation.Request.PolicyRevision,
	}
	switch {
	case invocation.OperationID != operationID(invocation.Request, action):
		return fmt.Errorf("%w: operation ID", ErrBinding)
	case invocation.Action != action:
		return fmt.Errorf("%w: action", ErrBinding)
	case invocation.Resources != resources:
		return fmt.Errorf("%w: resources", ErrBinding)
	case invocation.Binding != expectedBinding:
		return fmt.Errorf("%w: request binding", ErrBinding)
	}
	return nil
}

// PrepareInvocation creates the canonical payload used by a backend or a
// protocol-compatible step image.
func PrepareInvocation(request scannerreleaseworker.StepRequest) (Invocation, error) {
	if err := validateRequest(request); err != nil {
		return Invocation{}, err
	}
	action, resources, err := DefaultResourcePolicy().Resolve(request.Step)
	if err != nil {
		return Invocation{}, err
	}
	binding := Binding{
		DefinitionCommit: request.DefinitionCommit,
		LockDigest:       request.LockDigest,
		PolicyID:         request.PolicyID,
		PolicyRevision:   request.PolicyRevision,
	}
	return Invocation{
		OperationID: operationID(request, action),
		Request:     request,
		Action:      action,
		Resources:   resources,
		Binding:     binding,
	}, nil
}

func (e *Executor) lockOperation(key string) func() {
	e.mu.Lock()
	lock := e.operations[key]
	if lock == nil {
		lock = &keyedLock{}
		e.operations[key] = lock
	}
	lock.refs++
	e.mu.Unlock()
	lock.mutex.Lock()
	return func() {
		lock.mutex.Unlock()
		e.mu.Lock()
		lock.refs--
		if lock.refs == 0 {
			delete(e.operations, key)
		}
		e.mu.Unlock()
	}
}

func (e *Executor) acquire(
	ctx context.Context,
	key string,
	maximum int,
) (func(), error) {
	e.mu.Lock()
	semaphore := e.semaphores[key]
	if semaphore == nil {
		semaphore = make(chan struct{}, maximum)
		e.semaphores[key] = semaphore
	}
	e.mu.Unlock()
	select {
	case semaphore <- struct{}{}:
		return func() { <-semaphore }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type cacheEnvelope struct {
	InvocationDigest string                          `json:"invocation_digest"`
	Result           scannerreleaseworker.StepResult `json:"result"`
}

func cachePath(workspace, operationID string) string {
	return filepath.Join(
		workspace, ".wolf-release-backend-results",
		strings.TrimPrefix(operationID, "sha256:")+".json",
	)
}

func invocationDigest(invocation Invocation) string {
	// StepAttempt is audit metadata, not part of the durable external
	// invocation identity. A replacement worker may reconcile the same Job or
	// cached sink result through a later diagnostic attempt.
	invocation.Request.StepAttempt = 0
	value, _ := json.Marshal(invocation)
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func loadCachedResult(
	workspace string,
	invocation Invocation,
) (scannerreleaseworker.StepResult, bool, error) {
	value, err := os.ReadFile(cachePath(workspace, invocation.OperationID))
	if os.IsNotExist(err) {
		return scannerreleaseworker.StepResult{}, false, nil
	}
	if err != nil {
		return scannerreleaseworker.StepResult{}, false, err
	}
	if len(value) > maxBackendResultBytes {
		return scannerreleaseworker.StepResult{}, false, errors.New("cached backend result exceeds size limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	var envelope cacheEnvelope
	if err := decoder.Decode(&envelope); err != nil {
		return scannerreleaseworker.StepResult{}, false, fmt.Errorf("decode cached backend result: %w", err)
	}
	if err := ensureEOF(decoder); err != nil {
		return scannerreleaseworker.StepResult{}, false, err
	}
	if envelope.InvocationDigest != invocationDigest(invocation) {
		return scannerreleaseworker.StepResult{}, false, errors.New("cached backend result binding mismatch")
	}
	if err := validateResult(envelope.Result); err != nil {
		return scannerreleaseworker.StepResult{}, false, err
	}
	return envelope.Result, true, nil
}

func storeCachedResult(
	workspace string,
	invocation Invocation,
	result scannerreleaseworker.StepResult,
) error {
	directory := filepath.Dir(cachePath(workspace, invocation.OperationID))
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	value, err := json.Marshal(cacheEnvelope{
		InvocationDigest: invocationDigest(invocation), Result: result,
	})
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".result-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(value); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, cachePath(workspace, invocation.OperationID))
}

func validateResult(result scannerreleaseworker.StepResult) error {
	if result.OutputDigest != "" && !digestPattern.MatchString(result.OutputDigest) {
		return errors.New("scanner release backend returned an invalid output digest")
	}
	if result.OutputURI != "" {
		parsed, err := url.Parse(result.OutputURI)
		if err != nil || parsed.Scheme == "" || parsed.User != nil ||
			parsed.RawQuery != "" || parsed.Fragment != "" {
			return errors.New("scanner release backend returned an unsafe output URI")
		}
	}
	return nil
}

func redactStepResult(
	result scannerreleaseworker.StepResult,
) (scannerreleaseworker.StepResult, error) {
	value, err := json.Marshal(result)
	if err != nil {
		return scannerreleaseworker.StepResult{}, fmt.Errorf("encode backend result for redaction: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	var generic any
	if err := decoder.Decode(&generic); err != nil {
		return scannerreleaseworker.StepResult{}, err
	}
	value, err = json.Marshal(redactResultValue(generic))
	if err != nil {
		return scannerreleaseworker.StepResult{}, err
	}
	var redacted scannerreleaseworker.StepResult
	if err := json.Unmarshal(value, &redacted); err != nil {
		return scannerreleaseworker.StepResult{}, fmt.Errorf("decode redacted backend result: %w", err)
	}
	return redacted, nil
}

func redactResultValue(value any) any {
	switch typed := value.(type) {
	case string:
		return redact(typed, maxBackendResultBytes)
	case []any:
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = redactResultValue(item)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = redactResultValue(item)
		}
		return out
	default:
		return typed
	}
}

func redact(value string, maximum int) string {
	value = secretPattern.ReplaceAllString(value, "$1=[REDACTED]")
	value = bearerPattern.ReplaceAllString(value, "$1[REDACTED]")
	if len(value) > maximum {
		return value[:maximum] + "…"
	}
	return value
}

func ensureEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("backend result contained multiple JSON values")
	}
	return err
}

func sortedKinds(values []scannerpipeline.StepKind) []scannerpipeline.StepKind {
	out := append([]scannerpipeline.StepKind(nil), values...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
