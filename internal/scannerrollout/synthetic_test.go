package scannerrollout

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

func TestDefaultFixtureCorpusHasValidPinnedSignature(t *testing.T) {
	t.Parallel()
	corpus, err := DefaultFixtureCorpus()
	if err != nil {
		t.Fatal(err)
	}
	if corpus.SchemaVersion != fixtureCorpusSchema || len(corpus.Fixtures) != 3 ||
		corpus.Digest != "sha256:01768ba8e8650ffbb92e6abd0ed952c4246d4e9568c7be069dc107c5e38e18aa" {
		t.Fatalf("corpus = %#v", corpus)
	}
}

func TestCommandSyntheticExecutorRedactsCredentialBearingStderr(t *testing.T) {
	t.Parallel()
	executor := CommandSyntheticExecutor{
		Path: "/bin/sh",
		Args: []string{
			"-c",
			`printf '%s\n' 'Authorization: Bearer synthetic-super-secret' >&2; exit 1`,
		},
		Environment: []string{"PATH=/usr/bin:/bin"},
	}
	_, err := executor.ExecuteSynthetic(
		context.Background(), SyntheticExecutionRequest{},
	)
	if err == nil {
		t.Fatal("credential-bearing adapter failure unexpectedly succeeded")
	}
	if strings.Contains(err.Error(), "synthetic-super-secret") ||
		!strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("synthetic adapter error was not redacted: %v", err)
	}
}

func TestDurableSyntheticRuntimePersistsGenericInfrastructureFailure(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 30, 18, 0, 0, 0, time.UTC)
	corpus, err := DefaultFixtureCorpus()
	if err != nil {
		t.Fatal(err)
	}
	store := newSyntheticTestStore(now)
	runtime := DurableSyntheticRuntime{
		Base: &syntheticBaseRuntime{now: now}, Store: store,
		Executor: syntheticErrorExecutor{
			err: errors.New("token=synthetic-super-secret"),
		},
		Corpus: corpus, Now: func() time.Time { return now },
	}
	assignment := AssignmentRequest{
		OperationID: "rollout/r1/cohort/c1/release/new",
		RolloutID:   "r1", CohortID: "c1", CohortName: "canary",
		DesiredReleaseID: "new",
	}
	if err := runtime.Assign(context.Background(), assignment); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Health(context.Background(), HealthRequest{
		OperationID: assignment.OperationID, RolloutID: assignment.RolloutID,
		CohortID: assignment.CohortID, CohortName: assignment.CohortName,
		DesiredReleaseID:      assignment.DesiredReleaseID,
		SyntheticVerification: true,
	}); err != nil {
		t.Fatal(err)
	}
	status := store.workerSnapshot(t, syntheticWorkerID("r1", "c1"))
	if status.VerificationError != "synthetic infrastructure verification failed" ||
		strings.Contains(status.CapabilitiesJSON, "synthetic-super-secret") {
		t.Fatalf("durable synthetic failure leaked adapter detail: %#v", status)
	}
}

func TestDurableSyntheticRuntimeRejectsStaleEvidenceAndReusesCurrentEvidence(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 30, 18, 0, 0, 0, time.UTC)
	corpus, err := DefaultFixtureCorpus()
	if err != nil {
		t.Fatal(err)
	}
	store := newSyntheticTestStore(now)
	executor := &syntheticTestExecutor{}
	base := &syntheticBaseRuntime{now: now}
	runtime := DurableSyntheticRuntime{
		Base: base, Store: store, Executor: executor, Corpus: corpus,
		Now: func() time.Time { return now },
	}
	assignment := AssignmentRequest{
		OperationID: "rollout/r1/cohort/c1/release/new",
		RolloutID:   "r1", CohortID: "c1", CohortName: "canary",
		DesiredReleaseID: "new",
	}
	if err := runtime.Assign(context.Background(), assignment); err != nil {
		t.Fatal(err)
	}
	request := HealthRequest{
		OperationID: assignment.OperationID, RolloutID: assignment.RolloutID,
		CohortID: assignment.CohortID, CohortName: assignment.CohortName,
		DesiredReleaseID:      assignment.DesiredReleaseID,
		SyntheticVerification: true,
	}
	snapshot, err := runtime.Health(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if executor.callCount() != 1 || snapshot.Canary.Samples != 5 ||
		snapshot.Canary.ParserFailures != 0 ||
		snapshot.Canary.CandidateP95Duration != time.Second {
		t.Fatalf("first synthetic snapshot = %#v calls=%d", snapshot, executor.callCount())
	}
	if snapshot.Synthetic == nil ||
		snapshot.Synthetic.CorpusID != corpus.CorpusID ||
		snapshot.Synthetic.CorpusDigest != corpus.Digest ||
		!snapshot.Synthetic.Current ||
		snapshot.Synthetic.State != "passed" ||
		snapshot.Synthetic.FixtureTotal != len(corpus.Fixtures) ||
		snapshot.Synthetic.FixturePassed != len(corpus.Fixtures) ||
		snapshot.Synthetic.FixtureFailed != 0 {
		t.Fatalf("synthetic public projection = %#v", snapshot.Synthetic)
	}
	if _, err := runtime.Health(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if executor.callCount() != 1 {
		t.Fatal("current durable synthetic evidence was executed again")
	}

	store.inventory.Release.ManifestDigest = "sha256:" + strings.Repeat("d", 64)
	if _, err := runtime.Health(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if executor.callCount() != 2 {
		t.Fatal("evidence for a different current manifest digest was accepted")
	}

	status := store.workerSnapshot(t, syntheticWorkerID("r1", "c1"))
	stale := status.AssignedAt.Add(-time.Second)
	status.EvidenceObservedAt = &stale
	if err := store.UpsertWorkerReleaseStatus(context.Background(), &status); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Health(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if executor.callCount() != 3 {
		t.Fatal("evidence older than its assignment was accepted")
	}
}

func TestDurableSyntheticRuntimeFeedsIdentityAndFindingFailuresIntoCanary(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 30, 18, 0, 0, 0, time.UTC)
	corpus, err := DefaultFixtureCorpus()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*SyntheticExecutionResult)
		check  func(CanaryHealth) bool
	}{
		{
			name: "identity",
			mutate: func(result *SyntheticExecutionResult) {
				result.AssignmentOperationID = "stale-operation"
			},
			check: func(health CanaryHealth) bool { return health.ManifestFailures == 1 },
		},
		{
			name: "finding-loss",
			mutate: func(result *SyntheticExecutionResult) {
				result.Fixtures[1].FindingIDs = nil
			},
			check: func(health CanaryHealth) bool { return health.ExpectedFindingLosses == 2 },
		},
		{
			name: "signature",
			mutate: func(result *SyntheticExecutionResult) {
				result.SignatureVerified = false
			},
			check: func(health CanaryHealth) bool { return health.SignatureFailures == 1 },
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := newSyntheticTestStore(now)
			executor := &syntheticTestExecutor{mutate: tt.mutate}
			runtime := DurableSyntheticRuntime{
				Base: &syntheticBaseRuntime{now: now}, Store: store,
				Executor: executor, Corpus: corpus, Now: func() time.Time { return now },
			}
			assignment := AssignmentRequest{
				OperationID: "rollout/r1/cohort/c1/release/new",
				RolloutID:   "r1", CohortID: "c1", CohortName: "canary",
				DesiredReleaseID: "new",
			}
			if err := runtime.Assign(context.Background(), assignment); err != nil {
				t.Fatal(err)
			}
			snapshot, err := runtime.Health(context.Background(), HealthRequest{
				OperationID: assignment.OperationID, RolloutID: assignment.RolloutID,
				CohortID: assignment.CohortID, CohortName: assignment.CohortName,
				DesiredReleaseID:      assignment.DesiredReleaseID,
				SyntheticVerification: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			if !tt.check(snapshot.Canary) {
				t.Fatalf("health = %#v", snapshot.Canary)
			}
			status := store.workerSnapshot(t, syntheticWorkerID("r1", "c1"))
			if status.VerificationState != "failed" ||
				status.AssignmentOperationID != assignment.OperationID ||
				status.EvidenceObservedAt == nil {
				t.Fatalf("durable failure status = %#v", status)
			}
		})
	}
}

func TestDurableSyntheticRuntimeDoesNotPersistCancelledExecution(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 30, 18, 0, 0, 0, time.UTC)
	corpus, err := DefaultFixtureCorpus()
	if err != nil {
		t.Fatal(err)
	}
	store := newSyntheticTestStore(now)
	executor := &syntheticTestExecutor{block: true, started: make(chan struct{})}
	runtime := DurableSyntheticRuntime{
		Base: &syntheticBaseRuntime{now: now}, Store: store,
		Executor: executor, Corpus: corpus, Now: func() time.Time { return now },
	}
	assignment := AssignmentRequest{
		OperationID: "rollout/r1/cohort/c1/release/new",
		RolloutID:   "r1", CohortID: "c1", CohortName: "canary",
		DesiredReleaseID: "new",
	}
	if err := runtime.Assign(context.Background(), assignment); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := runtime.Health(ctx, HealthRequest{
			OperationID: assignment.OperationID, RolloutID: assignment.RolloutID,
			CohortID: assignment.CohortID, CohortName: assignment.CohortName,
			DesiredReleaseID:      assignment.DesiredReleaseID,
			SyntheticVerification: true,
		})
		result <- err
	}()
	<-executor.started
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled health error = %v", err)
	}
	status := store.workerSnapshot(t, syntheticWorkerID("r1", "c1"))
	if status.VerificationState != "pending" || status.EvidenceObservedAt != nil {
		t.Fatalf("cancelled execution persisted evidence: %#v", status)
	}
}

type syntheticBaseRuntime struct {
	now time.Time
}

func (r *syntheticBaseRuntime) Assign(context.Context, AssignmentRequest) error {
	return nil
}

func (r *syntheticBaseRuntime) Health(
	_ context.Context,
	request HealthRequest,
) (HealthSnapshot, error) {
	return healthySnapshot(request.DesiredReleaseID, r.now), nil
}

type syntheticTestExecutor struct {
	mu      sync.Mutex
	calls   int
	mutate  func(*SyntheticExecutionResult)
	block   bool
	started chan struct{}
}

type syntheticErrorExecutor struct {
	err error
}

func (e syntheticErrorExecutor) ExecuteSynthetic(
	context.Context,
	SyntheticExecutionRequest,
) (SyntheticExecutionResult, error) {
	return SyntheticExecutionResult{}, e.err
}

func (e *syntheticTestExecutor) ExecuteSynthetic(
	ctx context.Context,
	request SyntheticExecutionRequest,
) (SyntheticExecutionResult, error) {
	e.mu.Lock()
	e.calls++
	e.mu.Unlock()
	if e.block {
		close(e.started)
		<-ctx.Done()
		return SyntheticExecutionResult{}, ctx.Err()
	}
	fixtures := make([]SyntheticFixtureResult, 0, len(request.Fixtures))
	for index, fixture := range request.Fixtures {
		fixtures = append(fixtures, SyntheticFixtureResult{
			FixtureID: fixture.ID, ParserOK: true,
			FindingIDs: append([]string(nil), fixture.ExpectedFindings...),
			DurationMS: int64((index + 1) * 100),
		})
	}
	result := SyntheticExecutionResult{
		RolloutID: request.RolloutID, CohortID: request.CohortID,
		AssignmentOperationID: request.AssignmentOperationID,
		ReleaseID:             request.ReleaseID,
		ReleaseManifestDigest: request.ReleaseManifestDigest,
		CorpusDigest:          request.CorpusDigest, ImageDigests: request.ImageDigests,
		PullVerified: true, SignatureVerified: true, ManifestVerified: true,
		Fixtures: fixtures,
	}
	if e.mutate != nil {
		e.mutate(&result)
	}
	return result, nil
}

func (e *syntheticTestExecutor) callCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

type syntheticTestStore struct {
	scannerrelease.ReleaseRepository
	*fakeWorkerStatusStore
	inventory scannerrelease.ReleaseInventory
}

func newSyntheticTestStore(now time.Time) *syntheticTestStore {
	digest := "sha256:" + strings.Repeat("a", 64)
	return &syntheticTestStore{
		fakeWorkerStatusStore: &fakeWorkerStatusStore{},
		inventory: scannerrelease.ReleaseInventory{
			Release: scannerrelease.Release{
				ID: "new", ManifestDigest: digest,
			},
			Images: []scannerrelease.ReleaseImage{{
				ImageKey: "default", Repository: "registry.example/wolf/scanners",
				Digest: digest, SignatureStatus: "verified-cosign",
				ProvenanceDigest: "sha256:" + strings.Repeat("b", 64),
				SBOMDigest:       "sha256:" + strings.Repeat("c", 64),
				CreatedAt:        now,
			}},
		},
	}
}

func (s *syntheticTestStore) GetReleaseInventory(
	_ context.Context,
	id string,
) (*scannerrelease.ReleaseInventory, error) {
	if id != s.inventory.Release.ID {
		return nil, errors.New("release not found")
	}
	copy := s.inventory
	copy.Images = append([]scannerrelease.ReleaseImage(nil), s.inventory.Images...)
	return &copy, nil
}
