package scannerrollout

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannerdiscovery"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

const (
	fixtureCorpusSchema    = "wolf.scanner-fixture-corpus/v1"
	defaultCorpusJSON      = `{"schema_version":"wolf.scanner-fixture-corpus/v1","corpus_id":"wolf-core-synthetic-2026.1","fixtures":[{"id":"clean-minimal","family":"general","source_digest":"sha256:5a3b2f1d45d315da84f97a7c3f234857d8c9a39cb398cab7d843baaa6bd672b6","expected_findings":[]},{"id":"known-vulnerable","family":"security","source_digest":"sha256:96f62ba9e6133daee6d8554601199ea17fb25b5af72f8c9d4d770331b5472714","expected_findings":["wolf.synthetic.command-injection","wolf.synthetic.hardcoded-secret"]},{"id":"parser-edge","family":"parser","source_digest":"sha256:392ddf50bca3a4f9ec5bcf30c477f4f6ed272e6e61cc6138a8e32043ba56f29c","expected_findings":["wolf.synthetic.unicode-path"]}]}`
	defaultCorpusPublicKey = "MCowBQYDK2VwAyEAFiVxhngjd/q57OgXjlWKeuNDJRudN6SdhsId8CMCxsA="
	defaultCorpusSignature = "z0Wd7iIt0+zbPmf96H/U1lhHb/oyCYg0sX6DUuhptJuejJ+JprkzgjEdkuTELnwbz45yUop1y5wUwXmbS3/kBA=="
)

type FixtureDefinition struct {
	ID               string   `json:"id"`
	Family           string   `json:"family"`
	SourceDigest     string   `json:"source_digest"`
	ExpectedFindings []string `json:"expected_findings"`
}

type FixtureCorpus struct {
	SchemaVersion string              `json:"schema_version"`
	CorpusID      string              `json:"corpus_id"`
	Fixtures      []FixtureDefinition `json:"fixtures"`
	Digest        string              `json:"-"`
}

func DefaultFixtureCorpus() (FixtureCorpus, error) {
	raw := []byte(defaultCorpusJSON)
	publicDER, err := base64.StdEncoding.DecodeString(defaultCorpusPublicKey)
	if err != nil {
		return FixtureCorpus{}, err
	}
	publicValue, err := x509.ParsePKIXPublicKey(publicDER)
	if err != nil {
		return FixtureCorpus{}, fmt.Errorf("parse fixture corpus public key: %w", err)
	}
	publicKey, ok := publicValue.(ed25519.PublicKey)
	if !ok {
		return FixtureCorpus{}, errors.New("fixture corpus public key is not Ed25519")
	}
	signature, err := base64.StdEncoding.DecodeString(defaultCorpusSignature)
	if err != nil {
		return FixtureCorpus{}, err
	}
	if !ed25519.Verify(publicKey, raw, signature) {
		return FixtureCorpus{}, errors.New("fixture corpus signature is invalid")
	}
	var corpus FixtureCorpus
	if err := json.Unmarshal(raw, &corpus); err != nil {
		return FixtureCorpus{}, err
	}
	corpus.Digest = digestSynthetic(raw)
	if err := corpus.Validate(); err != nil {
		return FixtureCorpus{}, err
	}
	return corpus, nil
}

func (c FixtureCorpus) Validate() error {
	if c.SchemaVersion != fixtureCorpusSchema || c.CorpusID == "" ||
		!validSyntheticDigest(c.Digest) || len(c.Fixtures) == 0 {
		return errors.New("synthetic fixture corpus identity is invalid")
	}
	seen := make(map[string]struct{}, len(c.Fixtures))
	for _, fixture := range c.Fixtures {
		if fixture.ID == "" || fixture.Family == "" ||
			!validSyntheticDigest(fixture.SourceDigest) {
			return fmt.Errorf("synthetic fixture %q is invalid", fixture.ID)
		}
		if _, duplicate := seen[fixture.ID]; duplicate {
			return fmt.Errorf("duplicate synthetic fixture %q", fixture.ID)
		}
		seen[fixture.ID] = struct{}{}
	}
	return nil
}

type SyntheticExecutionRequest struct {
	RolloutID             string              `json:"rollout_id"`
	CohortID              string              `json:"cohort_id"`
	CohortName            string              `json:"cohort_name"`
	AssignmentOperationID string              `json:"assignment_operation_id"`
	ReleaseID             string              `json:"release_id"`
	ReleaseManifestDigest string              `json:"release_manifest_digest"`
	ImageDigests          map[string]string   `json:"image_digests"`
	ImageReferences       map[string]string   `json:"image_references"`
	CorpusID              string              `json:"corpus_id"`
	CorpusDigest          string              `json:"corpus_digest"`
	Fixtures              []FixtureDefinition `json:"fixtures"`
}

type SyntheticFixtureResult struct {
	FixtureID  string   `json:"fixture_id"`
	ParserOK   bool     `json:"parser_ok"`
	FindingIDs []string `json:"finding_ids"`
	DurationMS int64    `json:"duration_ms"`
	CrashLoop  bool     `json:"crash_loop,omitempty"`
}

type SyntheticExecutionResult struct {
	RolloutID              string                   `json:"rollout_id"`
	CohortID               string                   `json:"cohort_id"`
	AssignmentOperationID  string                   `json:"assignment_operation_id"`
	ReleaseID              string                   `json:"release_id"`
	ReleaseManifestDigest  string                   `json:"release_manifest_digest"`
	CorpusDigest           string                   `json:"corpus_digest"`
	ImageDigests           map[string]string        `json:"image_digests"`
	PullVerified           bool                     `json:"pull_verified"`
	SignatureVerified      bool                     `json:"signature_verified"`
	ManifestVerified       bool                     `json:"manifest_verified"`
	InfrastructureFailures int                      `json:"infrastructure_failures,omitempty"`
	Fixtures               []SyntheticFixtureResult `json:"fixtures"`
}

type SyntheticExecutor interface {
	ExecuteSynthetic(context.Context, SyntheticExecutionRequest) (SyntheticExecutionResult, error)
}

type CommandSyntheticExecutor struct {
	Path           string
	Args           []string
	Environment    []string
	Timeout        time.Duration
	MaxOutputBytes int64
}

func (e CommandSyntheticExecutor) ExecuteSynthetic(
	ctx context.Context,
	request SyntheticExecutionRequest,
) (SyntheticExecutionResult, error) {
	if strings.TrimSpace(e.Path) == "" {
		return SyntheticExecutionResult{}, errors.New("synthetic verification adapter path is required")
	}
	timeout := e.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Minute
	}
	limit := e.MaxOutputBytes
	if limit <= 0 {
		limit = 4 << 20
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return SyntheticExecutionResult{}, err
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(runCtx, e.Path, e.Args...)
	command.Env = append([]string(nil), e.Environment...)
	command.Stdin = bytes.NewReader(raw)
	var stderr bytes.Buffer
	command.Stderr = &limitedSyntheticWriter{writer: &stderr, remaining: limit}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return SyntheticExecutionResult{}, err
	}
	if err := command.Start(); err != nil {
		return SyntheticExecutionResult{}, err
	}
	response, readErr := io.ReadAll(io.LimitReader(stdout, limit+1))
	waitErr := command.Wait()
	if runCtx.Err() != nil {
		return SyntheticExecutionResult{}, runCtx.Err()
	}
	if readErr != nil {
		return SyntheticExecutionResult{}, readErr
	}
	if int64(len(response)) > limit {
		return SyntheticExecutionResult{}, errors.New("synthetic verification adapter response exceeds limit")
	}
	if waitErr != nil {
		return SyntheticExecutionResult{}, fmt.Errorf(
			"synthetic verification adapter failed: %w: %s",
			waitErr, scannerdiscovery.RedactText(strings.TrimSpace(stderr.String())),
		)
	}
	var result SyntheticExecutionResult
	decoder := json.NewDecoder(bytes.NewReader(response))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return SyntheticExecutionResult{}, fmt.Errorf("decode synthetic verification result: %w", err)
	}
	if err := ensureSyntheticEOF(decoder); err != nil {
		return SyntheticExecutionResult{}, err
	}
	return result, nil
}

type limitedSyntheticWriter struct {
	writer    io.Writer
	remaining int64
	overflow  bool
}

func (w *limitedSyntheticWriter) Write(value []byte) (int, error) {
	original := len(value)
	if w.remaining <= 0 {
		w.overflow = w.overflow || original > 0
		return original, nil
	}
	if int64(len(value)) > w.remaining {
		w.overflow = true
		value = value[:w.remaining]
	}
	_, _ = w.writer.Write(value)
	w.remaining -= int64(len(value))
	return original, nil
}

func (w *limitedSyntheticWriter) Overflowed() bool {
	return w.overflow
}

func ensureSyntheticEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("synthetic verification adapter returned multiple JSON values")
		}
		return fmt.Errorf("decode synthetic verification trailing data: %w", err)
	}
	return nil
}

type SyntheticStore interface {
	scannerrelease.ReleaseRepository
	scannerrelease.WorkerStatusRepository
}

// DurableSyntheticRuntime adds a fixed, signed fixture-corpus pass to the
// canary verification stage. Evidence is stored under a synthetic cohort so it
// survives controller restart without being counted as a deployment worker.
type DurableSyntheticRuntime struct {
	Base     Runtime
	Store    SyntheticStore
	Executor SyntheticExecutor
	Corpus   FixtureCorpus
	Now      func() time.Time
}

func (r DurableSyntheticRuntime) Assign(
	ctx context.Context,
	request AssignmentRequest,
) error {
	if err := r.validate(); err != nil {
		return err
	}
	if err := r.Base.Assign(ctx, request); err != nil {
		return err
	}
	statuses, err := r.syntheticStatuses(ctx, request.RolloutID, request.CohortID)
	if err != nil {
		return err
	}
	for _, status := range statuses {
		if status.AssignmentOperationID == request.OperationID &&
			status.DesiredReleaseID == request.DesiredReleaseID {
			return nil
		}
	}
	now := r.now()
	return r.Store.UpsertWorkerReleaseStatus(ctx, &scannerrelease.WorkerReleaseStatus{
		WorkerID:          syntheticWorkerID(request.RolloutID, request.CohortID),
		Cohort:            syntheticCohort(request.RolloutID, request.CohortID),
		DesiredReleaseID:  request.DesiredReleaseID,
		CachedDigestsJSON: "[]", VerificationState: "pending",
		CapabilitiesJSON: "{}", AssignmentOperationID: request.OperationID,
		AssignedAt: &now, LastHeartbeat: now,
	})
}

func (r DurableSyntheticRuntime) Health(
	ctx context.Context,
	request HealthRequest,
) (HealthSnapshot, error) {
	if err := r.validate(); err != nil {
		return HealthSnapshot{}, err
	}
	snapshot, err := r.Base.Health(ctx, request)
	if err != nil || !request.SyntheticVerification {
		return snapshot, err
	}
	metrics, err := r.ensureEvidence(ctx, request)
	if err != nil {
		return HealthSnapshot{}, err
	}
	addWorkerMetricsValue(&snapshot.Canary, metrics, false)
	summary, err := r.syntheticHealthEvidence(ctx, request)
	if err != nil {
		return HealthSnapshot{}, err
	}
	snapshot.Synthetic = &summary
	return snapshot, snapshot.Validate()
}

func (r DurableSyntheticRuntime) Pause(
	ctx context.Context,
	request AssignmentRequest,
) error {
	return forwardSyntheticLifecycle(ctx, r.Base, "pause", request)
}

func (r DurableSyntheticRuntime) Resume(
	ctx context.Context,
	request AssignmentRequest,
) error {
	return forwardSyntheticLifecycle(ctx, r.Base, "resume", request)
}

func (r DurableSyntheticRuntime) Cancel(
	ctx context.Context,
	request AssignmentRequest,
) error {
	return forwardSyntheticLifecycle(ctx, r.Base, "cancel", request)
}

func forwardSyntheticLifecycle(
	ctx context.Context,
	base Runtime,
	action string,
	request AssignmentRequest,
) error {
	lifecycle, ok := base.(LifecycleRuntime)
	if !ok {
		return nil
	}
	switch action {
	case "pause":
		return lifecycle.Pause(ctx, request)
	case "resume":
		return lifecycle.Resume(ctx, request)
	case "cancel":
		return lifecycle.Cancel(ctx, request)
	default:
		return errors.New("unknown synthetic runtime lifecycle action")
	}
}

func (r DurableSyntheticRuntime) ensureEvidence(
	ctx context.Context,
	request HealthRequest,
) (workerMetrics, error) {
	inventory, err := r.Store.GetReleaseInventory(ctx, request.DesiredReleaseID)
	if err != nil {
		return workerMetrics{}, fmt.Errorf("load synthetic verification release: %w", err)
	}
	execution, err := syntheticRequest(request, inventory, r.Corpus)
	if err != nil {
		return workerMetrics{}, r.persistSyntheticFailure(ctx, request, workerMetrics{
			Samples: 1, ManifestFailures: 1,
		}, err)
	}
	statuses, err := r.syntheticStatuses(ctx, request.RolloutID, request.CohortID)
	if err != nil {
		return workerMetrics{}, err
	}
	for _, status := range statuses {
		if status.AssignmentOperationID != request.OperationID ||
			status.DesiredReleaseID != request.DesiredReleaseID ||
			status.AssignedAt == nil || status.EvidenceObservedAt == nil ||
			status.EvidenceObservedAt.Before(status.AssignedAt.UTC()) {
			continue
		}
		var evidence syntheticEvidence
		if json.Unmarshal([]byte(status.CapabilitiesJSON), &evidence) == nil &&
			evidence.Binding.matches(request, execution) &&
			reflect.DeepEqual(evidence.ImageDigests, execution.ImageDigests) {
			return evidence.Metrics, nil
		}
	}
	result, err := r.Executor.ExecuteSynthetic(ctx, execution)
	if err != nil {
		if ctx.Err() != nil {
			return workerMetrics{}, ctx.Err()
		}
		return workerMetrics{}, r.persistSyntheticFailure(ctx, request, workerMetrics{
			Samples: 1, InfrastructureFailures: 1,
		}, err)
	}
	metrics, verifyErr := verifySyntheticResult(execution, result)
	if persistErr := r.persistSyntheticEvidence(ctx, request, execution, result, metrics, verifyErr); persistErr != nil {
		return workerMetrics{}, persistErr
	}
	return metrics, nil
}

type syntheticBinding struct {
	RolloutID             string `json:"rollout_id"`
	CohortID              string `json:"cohort_id"`
	AssignmentOperationID string `json:"assignment_operation_id"`
	ReleaseID             string `json:"release_id"`
	ManifestDigest        string `json:"manifest_digest"`
	CorpusID              string `json:"corpus_id"`
	CorpusDigest          string `json:"corpus_digest"`
}

func (b syntheticBinding) matches(
	request HealthRequest,
	execution SyntheticExecutionRequest,
) bool {
	return b.RolloutID == request.RolloutID && b.CohortID == request.CohortID &&
		b.AssignmentOperationID == request.OperationID &&
		b.ReleaseID == request.DesiredReleaseID &&
		b.CorpusID == execution.CorpusID &&
		b.CorpusDigest == execution.CorpusDigest &&
		b.ManifestDigest == execution.ReleaseManifestDigest
}

type syntheticEvidence struct {
	Binding      syntheticBinding         `json:"binding"`
	Metrics      workerMetrics            `json:"metrics"`
	ImageDigests map[string]string        `json:"image_digests"`
	Fixtures     []SyntheticFixtureResult `json:"fixtures"`
}

func syntheticRequest(
	request HealthRequest,
	inventory *scannerrelease.ReleaseInventory,
	corpus FixtureCorpus,
) (SyntheticExecutionRequest, error) {
	if inventory == nil || inventory.Release.ID != request.DesiredReleaseID ||
		!validSyntheticDigest(inventory.Release.ManifestDigest) {
		return SyntheticExecutionRequest{}, errors.New("synthetic release manifest identity is invalid")
	}
	digests := make(map[string]string, len(inventory.Images))
	references := make(map[string]string, len(inventory.Images))
	for _, image := range inventory.Images {
		if !scannerrelease.IsRuntimeScannerImage(image) {
			continue
		}
		reference, referenceErr := immutableImageReference(
			image.Repository, image.Digest,
		)
		if image.ImageKey == "" || referenceErr != nil ||
			!strings.HasPrefix(strings.ToLower(image.SignatureStatus), "verified") ||
			!validSyntheticDigest(image.ProvenanceDigest) ||
			!validSyntheticDigest(image.SBOMDigest) {
			return SyntheticExecutionRequest{}, fmt.Errorf(
				"synthetic release image %q has incomplete immutable evidence",
				image.ImageKey,
			)
		}
		digests[image.ImageKey] = image.Digest
		references[image.ImageKey] = reference
	}
	if len(digests) == 0 {
		return SyntheticExecutionRequest{}, errors.New("synthetic release has no images")
	}
	return SyntheticExecutionRequest{
		RolloutID: request.RolloutID, CohortID: request.CohortID,
		CohortName: request.CohortName, AssignmentOperationID: request.OperationID,
		ReleaseID:             request.DesiredReleaseID,
		ReleaseManifestDigest: inventory.Release.ManifestDigest,
		ImageDigests:          digests, ImageReferences: references,
		CorpusID: corpus.CorpusID, CorpusDigest: corpus.Digest,
		Fixtures: append([]FixtureDefinition(nil), corpus.Fixtures...),
	}, nil
}

func verifySyntheticResult(
	request SyntheticExecutionRequest,
	result SyntheticExecutionResult,
) (workerMetrics, error) {
	metrics := workerMetrics{
		Samples:                len(request.Fixtures),
		InfrastructureFailures: result.InfrastructureFailures,
	}
	if result.RolloutID != request.RolloutID || result.CohortID != request.CohortID ||
		result.AssignmentOperationID != request.AssignmentOperationID ||
		result.ReleaseID != request.ReleaseID ||
		result.ReleaseManifestDigest != request.ReleaseManifestDigest ||
		result.CorpusDigest != request.CorpusDigest ||
		!reflect.DeepEqual(result.ImageDigests, request.ImageDigests) {
		metrics.ManifestFailures++
		return metrics, errors.New("synthetic verification result identity does not match its assignment")
	}
	if !result.PullVerified {
		metrics.PullFailures++
	}
	if !result.SignatureVerified {
		metrics.SignatureFailures++
	}
	if !result.ManifestVerified {
		metrics.ManifestFailures++
	}
	expected := make(map[string]FixtureDefinition, len(request.Fixtures))
	for _, fixture := range request.Fixtures {
		expected[fixture.ID] = fixture
	}
	seen := make(map[string]struct{}, len(result.Fixtures))
	durations := make([]int64, 0, len(result.Fixtures))
	for _, fixture := range result.Fixtures {
		definition, exists := expected[fixture.FixtureID]
		if !exists {
			metrics.ParserFailures++
			continue
		}
		if _, duplicate := seen[fixture.FixtureID]; duplicate {
			metrics.ParserFailures++
			continue
		}
		seen[fixture.FixtureID] = struct{}{}
		if !fixture.ParserOK || fixture.DurationMS < 0 {
			metrics.ParserFailures++
		}
		if fixture.CrashLoop {
			metrics.CrashLoops++
		}
		actual := make(map[string]struct{}, len(fixture.FindingIDs))
		for _, finding := range fixture.FindingIDs {
			actual[finding] = struct{}{}
		}
		for _, finding := range definition.ExpectedFindings {
			if _, exists := actual[finding]; !exists {
				metrics.ExpectedFindingLosses++
			}
		}
		durations = append(durations, fixture.DurationMS)
	}
	if len(seen) != len(expected) {
		metrics.ParserFailures += len(expected) - len(seen)
	}
	if len(durations) > 0 {
		sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
		index := (95*len(durations)+99)/100 - 1
		metrics.P95DurationMS = durations[index]
	}
	if metrics.ParserFailures+metrics.PullFailures+metrics.SignatureFailures+
		metrics.ManifestFailures+metrics.ExpectedFindingLosses+metrics.CrashLoops+
		metrics.InfrastructureFailures > 0 {
		return metrics, errors.New("synthetic verification fixture or supply-chain checks failed")
	}
	return metrics, nil
}

func (r DurableSyntheticRuntime) persistSyntheticFailure(
	ctx context.Context,
	request HealthRequest,
	metrics workerMetrics,
	cause error,
) error {
	if err := r.persistSyntheticEvidence(
		ctx, request,
		SyntheticExecutionRequest{
			RolloutID: request.RolloutID, CohortID: request.CohortID,
			AssignmentOperationID: request.OperationID,
			ReleaseID:             request.DesiredReleaseID,
			CorpusID:              r.Corpus.CorpusID,
			CorpusDigest:          r.Corpus.Digest,
		},
		SyntheticExecutionResult{}, metrics, cause,
	); err != nil {
		return err
	}
	return nil
}

func (r DurableSyntheticRuntime) persistSyntheticEvidence(
	ctx context.Context,
	request HealthRequest,
	execution SyntheticExecutionRequest,
	result SyntheticExecutionResult,
	metrics workerMetrics,
	verifyErr error,
) error {
	now := r.now()
	assignedAt := now
	statuses, err := r.syntheticStatuses(ctx, request.RolloutID, request.CohortID)
	if err != nil {
		return err
	}
	for _, status := range statuses {
		if status.AssignmentOperationID == request.OperationID &&
			status.AssignedAt != nil {
			assignedAt = status.AssignedAt.UTC()
			break
		}
	}
	state, detail := "verified", ""
	if verifyErr != nil {
		state, detail = "failed", syntheticFailureDetail(metrics, verifyErr)
	}
	evidence := syntheticEvidence{
		Binding: syntheticBinding{
			RolloutID: request.RolloutID, CohortID: request.CohortID,
			AssignmentOperationID: request.OperationID,
			ReleaseID:             request.DesiredReleaseID,
			ManifestDigest:        execution.ReleaseManifestDigest,
			CorpusID:              r.Corpus.CorpusID,
			CorpusDigest:          r.Corpus.Digest,
		},
		Metrics: metrics, ImageDigests: execution.ImageDigests,
		Fixtures: result.Fixtures,
	}
	raw, err := json.Marshal(evidence)
	if err != nil {
		return err
	}
	digests, _ := json.Marshal(execution.ImageDigests)
	return r.Store.UpsertWorkerReleaseStatus(ctx, &scannerrelease.WorkerReleaseStatus{
		WorkerID:          syntheticWorkerID(request.RolloutID, request.CohortID),
		Cohort:            syntheticCohort(request.RolloutID, request.CohortID),
		DesiredReleaseID:  request.DesiredReleaseID,
		ObservedReleaseID: request.DesiredReleaseID,
		CachedDigestsJSON: string(digests), VerificationState: state,
		VerificationError: detail, CapabilitiesJSON: string(raw),
		AssignmentOperationID: request.OperationID, AssignedAt: &assignedAt,
		EvidenceObservedAt: &now, LastHeartbeat: now,
	})
}

func syntheticFailureDetail(metrics workerMetrics, _ error) string {
	switch {
	case metrics.SignatureFailures > 0:
		return "synthetic signature verification failed"
	case metrics.ManifestFailures > 0:
		return "synthetic manifest digest verification failed"
	case metrics.PullFailures > 0:
		return "synthetic image pull verification failed"
	case metrics.ParserFailures > 0:
		return "synthetic parser verification failed"
	case metrics.ExpectedFindingLosses > 0:
		return "synthetic expected finding verification failed"
	case metrics.CrashLoops > 0:
		return "synthetic crash-loop verification failed"
	default:
		return "synthetic infrastructure verification failed"
	}
}

func (r DurableSyntheticRuntime) syntheticHealthEvidence(
	ctx context.Context,
	request HealthRequest,
) (SyntheticHealthEvidence, error) {
	statuses, err := r.syntheticStatuses(ctx, request.RolloutID, request.CohortID)
	if err != nil {
		return SyntheticHealthEvidence{}, err
	}
	summary := SyntheticHealthEvidence{
		CorpusID: r.Corpus.CorpusID, CorpusDigest: r.Corpus.Digest,
		State: "pending", FixtureTotal: len(r.Corpus.Fixtures),
		ObservedAt: r.now(),
	}
	var selected *scannerrelease.WorkerReleaseStatus
	for index := range statuses {
		status := &statuses[index]
		if status.AssignmentOperationID != request.OperationID ||
			status.DesiredReleaseID != request.DesiredReleaseID {
			continue
		}
		if selected == nil || status.LastHeartbeat.After(selected.LastHeartbeat) {
			selected = status
		}
	}
	if selected == nil {
		return summary, nil
	}
	if selected.EvidenceObservedAt != nil {
		summary.ObservedAt = selected.EvidenceObservedAt.UTC()
	} else if selected.AssignedAt != nil {
		summary.ObservedAt = selected.AssignedAt.UTC()
	} else if !selected.LastHeartbeat.IsZero() {
		summary.ObservedAt = selected.LastHeartbeat.UTC()
	}
	switch {
	case verificationReady(selected.VerificationState):
		summary.State = "passed"
	case verificationFailed(selected.VerificationState) ||
		selected.VerificationError != "":
		summary.State = "failed"
	}
	var evidence syntheticEvidence
	if json.Unmarshal([]byte(selected.CapabilitiesJSON), &evidence) == nil {
		summary.Current = evidence.Binding.RolloutID == request.RolloutID &&
			evidence.Binding.CohortID == request.CohortID &&
			evidence.Binding.AssignmentOperationID == request.OperationID &&
			evidence.Binding.ReleaseID == request.DesiredReleaseID &&
			evidence.Binding.CorpusID == r.Corpus.CorpusID &&
			evidence.Binding.CorpusDigest == r.Corpus.Digest &&
			selected.AssignedAt != nil && selected.EvidenceObservedAt != nil &&
			!selected.EvidenceObservedAt.Before(selected.AssignedAt.UTC())
		summary.FixturePassed = countPassedSyntheticFixtures(
			r.Corpus.Fixtures, evidence.Fixtures,
		)
		if summary.State != "pending" {
			summary.FixtureFailed = summary.FixtureTotal - summary.FixturePassed
		}
		summary.FailureClass = syntheticFailureClass(
			evidence.Metrics, selected.VerificationError,
		)
	}
	if summary.State == "failed" && summary.FailureClass == "" {
		summary.FailureClass = "infrastructure"
	}
	return summary, nil
}

func countPassedSyntheticFixtures(
	definitions []FixtureDefinition,
	results []SyntheticFixtureResult,
) int {
	expected := make(map[string]FixtureDefinition, len(definitions))
	for _, definition := range definitions {
		expected[definition.ID] = definition
	}
	passed := make(map[string]struct{}, len(results))
	for _, result := range results {
		definition, ok := expected[result.FixtureID]
		if !ok || !result.ParserOK || result.CrashLoop || result.DurationMS < 0 {
			continue
		}
		findings := make(map[string]struct{}, len(result.FindingIDs))
		for _, finding := range result.FindingIDs {
			findings[finding] = struct{}{}
		}
		complete := true
		for _, finding := range definition.ExpectedFindings {
			if _, ok := findings[finding]; !ok {
				complete = false
				break
			}
		}
		if complete {
			passed[result.FixtureID] = struct{}{}
		}
	}
	return len(passed)
}

func syntheticFailureClass(metrics workerMetrics, detail string) string {
	switch {
	case metrics.SignatureFailures > 0 || strings.Contains(detail, "signature"):
		return "signature"
	case metrics.ManifestFailures > 0 ||
		strings.Contains(detail, "manifest") || strings.Contains(detail, "digest"):
		return "manifest"
	case metrics.PullFailures > 0 || strings.Contains(detail, "pull"):
		return "pull"
	case metrics.ParserFailures > 0 || strings.Contains(detail, "parser"):
		return "parser"
	case metrics.ExpectedFindingLosses > 0 || strings.Contains(detail, "finding"):
		return "finding_loss"
	case metrics.CrashLoops > 0 || strings.Contains(detail, "crash"):
		return "crash_loop"
	case metrics.InfrastructureFailures > 0 || strings.Contains(detail, "infrastructure"):
		return "infrastructure"
	default:
		return ""
	}
}

func (r DurableSyntheticRuntime) syntheticStatuses(
	ctx context.Context,
	rolloutID, cohortID string,
) ([]scannerrelease.WorkerReleaseStatus, error) {
	return r.Store.ListWorkerReleaseStatuses(
		ctx, syntheticCohort(rolloutID, cohortID), time.Unix(0, 0).UTC(),
	)
}

func (r DurableSyntheticRuntime) validate() error {
	if r.Base == nil || r.Store == nil || r.Executor == nil {
		return errors.New("durable synthetic runtime requires base, store, and executor")
	}
	return r.Corpus.Validate()
}

func (r DurableSyntheticRuntime) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

func syntheticCohort(rolloutID, cohortID string) string {
	return "__synthetic__/" + rolloutID + "/" + cohortID
}

func syntheticWorkerID(rolloutID, cohortID string) string {
	return "synthetic:" + rolloutID + ":" + cohortID
}

func addWorkerMetricsValue(health *CanaryHealth, metrics workerMetrics, stable bool) {
	raw, _ := json.Marshal(metrics)
	addWorkerMetrics(health, string(raw), stable)
}

func validSyntheticDigest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func digestSynthetic(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}
