package scannerrollout

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

func TestCohortDeploymentRuntimeUsesExactDigestsAndRestoresRollbackRelease(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 30, 18, 0, 0, 0, time.UTC)
	store := deploymentTestInventoryStore()
	cache := &deploymentTestCache{now: now}
	control := &deploymentTestControl{observed: make(map[string]DeploymentObservation)}
	status := &deploymentTestStatus{now: now}
	runtime := CohortDeploymentRuntime{
		Name: "test", Store: store, Cache: cache, Control: control,
		Status: status, Now: func() time.Time { return now },
	}
	activeStableScan := struct {
		ReleaseID string
		Digest    string
	}{ReleaseID: "old", Digest: store.inventory["old"].Images[0].Digest}
	for _, request := range []AssignmentRequest{
		{
			OperationID: "rollout/r1/cohort/canary/release/new",
			RolloutID:   "r1", CohortID: "canary", CohortName: "canary",
			DesiredReleaseID: "new", PreviousReleaseID: "old",
		},
		{
			OperationID: "rollout/r1/cohort/canary/release/old",
			RolloutID:   "r1", CohortID: "canary", CohortName: "canary",
			DesiredReleaseID: "old", PreviousReleaseID: "old", Rollback: true,
		},
	} {
		if err := runtime.Assign(context.Background(), request); err != nil {
			t.Fatal(err)
		}
		snapshot, err := runtime.Health(context.Background(), HealthRequest{
			OperationID: request.OperationID, RolloutID: request.RolloutID,
			CohortID: request.CohortID, CohortName: request.CohortName,
			DesiredReleaseID: request.DesiredReleaseID,
		})
		if err != nil || snapshot.ObservedReleaseID != request.DesiredReleaseID {
			t.Fatalf("health = %#v, err=%v", snapshot, err)
		}
	}
	applied := control.assignmentSnapshot()
	if len(applied) != 2 || !applied[1].Rollback ||
		applied[0].ReleaseID != "new" || applied[1].ReleaseID != "old" ||
		applied[1].ManifestDigest != store.inventory["old"].Release.ManifestDigest {
		t.Fatalf("assignments = %#v", applied)
	}
	if len(cache.plans) != 2 ||
		len(cache.plans[0].ImageReferences) != 1 ||
		cache.plans[0].ImageReferences["default"] !=
			"registry.example/wolf/scanners@"+store.inventory["new"].Images[0].Digest {
		t.Fatalf("cache plans = %#v", cache.plans)
	}
	if activeStableScan.ReleaseID != "old" ||
		activeStableScan.Digest != store.inventory["old"].Images[0].Digest {
		t.Fatalf("overlapping active scan changed during rollout: %#v", activeStableScan)
	}
}

func TestCohortDeploymentRuntimeFailsClosedOnCacheOrObservedDigestMismatch(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 30, 18, 0, 0, 0, time.UTC)
	store := deploymentTestInventoryStore()
	request := AssignmentRequest{
		OperationID: "operation", RolloutID: "r1", CohortID: "c1",
		CohortName: "canary", DesiredReleaseID: "new",
	}
	t.Run("cache", func(t *testing.T) {
		cache := &deploymentTestCache{now: now, wrong: true}
		runtime := CohortDeploymentRuntime{
			Name: "test", Store: store, Cache: cache,
			Control: &deploymentTestControl{observed: make(map[string]DeploymentObservation)},
			Status:  &deploymentTestStatus{now: now},
		}
		if err := runtime.Assign(context.Background(), request); err == nil ||
			!strings.Contains(err.Error(), "cache verification") {
			t.Fatalf("cache mismatch error = %v", err)
		}
	})
	t.Run("readback", func(t *testing.T) {
		control := &deploymentTestControl{
			observed: make(map[string]DeploymentObservation), wrong: true,
		}
		runtime := CohortDeploymentRuntime{
			Name: "test", Store: store,
			Cache: &deploymentTestCache{now: now}, Control: control,
			Status: &deploymentTestStatus{now: now},
		}
		if err := runtime.Assign(context.Background(), request); err == nil ||
			!strings.Contains(err.Error(), "readback") {
			t.Fatalf("readback mismatch error = %v", err)
		}
	})
}

func TestResolveDeploymentPlanRejectsMalformedOCIRepositories(t *testing.T) {
	t.Parallel()
	digest := "sha256:" + strings.Repeat("a", 64)
	for _, repository := range []string{
		" registry.example/wolf/scanners",
		"registry.example/wolf/scanners ",
		"registry.example/wolf/\nscanners",
		"https://registry.example/wolf/scanners",
		"user:password@registry.example/wolf/scanners",
		"registry.example:70000/wolf/scanners",
		"registry..example/wolf/scanners",
		"Registry.example/wolf/scanners",
		"registry.example/wolf/../scanners",
		"registry.example/wolf/scanners:latest",
	} {
		repository := repository
		t.Run(repository, func(t *testing.T) {
			store := &deploymentInventoryStore{
				inventory: map[string]scannerrelease.ReleaseInventory{
					"new": {
						Release: scannerrelease.Release{
							ID: "new", ManifestDigest: digest,
						},
						Images: []scannerrelease.ReleaseImage{{
							ImageKey: "default", Repository: repository, Digest: digest,
						}},
					},
				},
			}
			if _, err := ResolveDeploymentPlan(
				context.Background(), store, "new",
			); err == nil {
				t.Fatalf("malformed repository %q was accepted", repository)
			}
		})
	}
	for _, repository := range []string{
		"docker.io/library/alpine",
		"registry.example:5000/wolf/scanners",
		"ghcr.io/alpha-bravo/wolf_scanners",
	} {
		reference, err := immutableImageReference(repository, digest)
		if err != nil || reference != repository+"@"+digest {
			t.Fatalf("valid reference %q = %q, %v", repository, reference, err)
		}
	}
}

func TestComposeControlPersistsReadbackLifecycleAndRollbackAcrossRestart(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 30, 18, 0, 0, 0, time.UTC)
	runner := &composeTestRunner{}
	root := t.TempDir()
	control := ComposeControl{StateRoot: root, Runner: runner}
	newAssignment := deploymentTestAssignment("new", "operation-new", now)
	if err := control.Apply(context.Background(), newAssignment); err != nil {
		t.Fatal(err)
	}
	if observation, err := control.Observe(context.Background(), newAssignment); err != nil ||
		!observationMatches(newAssignment, observation) {
		t.Fatalf("new observation = %#v, err=%v", observation, err)
	}
	// A fresh control instance proves restart/readback and idempotency use the
	// persisted observed file rather than process memory.
	restarted := ComposeControl{StateRoot: root, Runner: runner}
	if err := restarted.Apply(context.Background(), newAssignment); err != nil {
		t.Fatal(err)
	}
	if runner.applyCalls != 1 {
		t.Fatalf("idempotent apply calls = %d", runner.applyCalls)
	}
	if err := restarted.Pause(context.Background(), newAssignment); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.Observe(context.Background(), newAssignment); err == nil {
		t.Fatal("paused Compose cohort reported ready")
	}
	if err := restarted.Resume(context.Background(), newAssignment); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.Observe(context.Background(), newAssignment); err != nil {
		t.Fatal(err)
	}
	if err := restarted.Cancel(context.Background(), newAssignment); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.Observe(context.Background(), newAssignment); err == nil {
		t.Fatal("cancelled Compose cohort reported ready")
	}

	oldAssignment := deploymentTestAssignment("old", "operation-old", now.Add(time.Minute))
	oldAssignment.Rollback = true
	oldAssignment.PreviousReleaseID = "new"
	if err := restarted.Apply(context.Background(), oldAssignment); err != nil {
		t.Fatal(err)
	}
	observation, err := restarted.Observe(context.Background(), oldAssignment)
	if err != nil || observation.ReleaseID != "old" ||
		observation.ManifestDigest != oldAssignment.ManifestDigest {
		t.Fatalf("rollback observation = %#v, err=%v", observation, err)
	}
}

func TestComposeControlRestoresDesiredStateWhenReloadFails(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	runner := &composeTestRunner{}
	control := ComposeControl{StateRoot: root, Runner: runner}
	first := deploymentTestAssignment("old", "old-operation", time.Now().UTC())
	if err := control.Apply(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	desiredPath, _, _, err := control.paths(first.CohortID)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(desiredPath)
	if err != nil {
		t.Fatal(err)
	}
	runner.fail = true
	second := deploymentTestAssignment("new", "new-operation", time.Now().UTC())
	if err := control.Apply(context.Background(), second); err == nil {
		t.Fatal("failed Compose reload unexpectedly succeeded")
	}
	after, err := os.ReadFile(desiredPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("failed reload did not restore the prior desired state")
	}
}

type deploymentInventoryStore struct {
	inventory map[string]scannerrelease.ReleaseInventory
}

func deploymentTestInventoryStore() *deploymentInventoryStore {
	release := func(id, manifestCharacter, imageCharacter string) scannerrelease.ReleaseInventory {
		return scannerrelease.ReleaseInventory{
			Release: scannerrelease.Release{
				ID: id, ManifestDigest: "sha256:" + strings.Repeat(manifestCharacter, 64),
			},
			Images: []scannerrelease.ReleaseImage{{
				ImageKey: "default", Repository: "registry.example/wolf/scanners",
				Digest: "sha256:" + strings.Repeat(imageCharacter, 64),
			}, {
				ImageKey: "fixer-codex", ImageKind: scannerrelease.ReleaseImageFixer,
				Repository: "registry.example/wolf/fixer-codex",
				Digest:     "sha256:" + strings.Repeat("e", 64),
			}},
		}
	}
	return &deploymentInventoryStore{inventory: map[string]scannerrelease.ReleaseInventory{
		"old": release("old", "a", "b"),
		"new": release("new", "c", "d"),
	}}
}

func (s *deploymentInventoryStore) GetReleaseInventory(
	_ context.Context,
	id string,
) (*scannerrelease.ReleaseInventory, error) {
	inventory, exists := s.inventory[id]
	if !exists {
		return nil, errors.New("release not found")
	}
	copy := inventory
	copy.Images = append([]scannerrelease.ReleaseImage(nil), inventory.Images...)
	return &copy, nil
}

type deploymentTestCache struct {
	now   time.Time
	wrong bool
	plans []DeploymentPlan
}

func (c *deploymentTestCache) Prepare(
	_ context.Context,
	_ string,
	plan DeploymentPlan,
) (CacheVerification, error) {
	c.plans = append(c.plans, plan)
	digests := cloneStrings(plan.ImageDigests)
	if c.wrong {
		digests["default"] = "sha256:" + strings.Repeat("f", 64)
	}
	return CacheVerification{Digests: digests, VerifiedAt: c.now}, nil
}

type deploymentTestControl struct {
	mu          sync.Mutex
	observed    map[string]DeploymentObservation
	assignments []DeploymentAssignment
	wrong       bool
}

func (c *deploymentTestControl) Apply(
	_ context.Context,
	assignment DeploymentAssignment,
) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.assignments = append(c.assignments, assignment)
	observation := DeploymentObservation{
		OperationID: assignment.OperationID, ReleaseID: assignment.ReleaseID,
		ManifestDigest: assignment.ManifestDigest,
		ImageDigests:   cloneStrings(assignment.ImageDigests),
		Ready:          true, ObservedAt: assignment.AppliedAt,
	}
	if c.wrong {
		observation.ManifestDigest = "sha256:" + strings.Repeat("f", 64)
	}
	c.observed[assignment.CohortID] = observation
	return nil
}

func (c *deploymentTestControl) Observe(
	_ context.Context,
	assignment DeploymentAssignment,
) (DeploymentObservation, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	observation, exists := c.observed[assignment.CohortID]
	if !exists {
		return DeploymentObservation{}, errors.New("not assigned")
	}
	return observation, nil
}

func (c *deploymentTestControl) Pause(context.Context, DeploymentAssignment) error {
	return nil
}
func (c *deploymentTestControl) Resume(context.Context, DeploymentAssignment) error {
	return nil
}
func (c *deploymentTestControl) Cancel(context.Context, DeploymentAssignment) error {
	return nil
}

func (c *deploymentTestControl) assignmentSnapshot() []DeploymentAssignment {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]DeploymentAssignment(nil), c.assignments...)
}

type deploymentTestStatus struct {
	mu      sync.Mutex
	now     time.Time
	desired map[string]string
}

func (s *deploymentTestStatus) Assign(
	_ context.Context,
	request AssignmentRequest,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.desired == nil {
		s.desired = make(map[string]string)
	}
	s.desired[request.CohortID] = request.DesiredReleaseID
	return nil
}

func (s *deploymentTestStatus) Health(
	_ context.Context,
	request HealthRequest,
) (HealthSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return healthySnapshot(s.desired[request.CohortID], s.now), nil
}

type composeTestRunner struct {
	applyCalls int
	fail       bool
}

func (r *composeTestRunner) RunComposeAction(
	_ context.Context,
	action string,
	assignment DeploymentAssignment,
) (DeploymentObservation, error) {
	if r.fail {
		return DeploymentObservation{}, errors.New("injected reload failure")
	}
	if action != "apply" {
		return DeploymentObservation{}, nil
	}
	r.applyCalls++
	return DeploymentObservation{
		OperationID: assignment.OperationID, ReleaseID: assignment.ReleaseID,
		ManifestDigest: assignment.ManifestDigest,
		ImageDigests:   cloneStrings(assignment.ImageDigests),
		Ready:          true, ObservedAt: assignment.AppliedAt,
	}, nil
}

func deploymentTestAssignment(
	releaseID, operationID string,
	at time.Time,
) DeploymentAssignment {
	character := "a"
	if releaseID == "new" {
		character = "b"
	}
	digest := "sha256:" + strings.Repeat(character, 64)
	return DeploymentAssignment{
		OperationID: operationID, RolloutID: "r1", Target: "production",
		CohortID: "cohort-1", CohortName: "canary", ReleaseID: releaseID,
		ManifestDigest: digest,
		ImageDigests:   map[string]string{"default": digest},
		ImageReferences: map[string]string{
			"default": "registry.example/wolf/scanners@" + digest,
		},
		CachedDigests: map[string]string{"default": digest}, AppliedAt: at,
	}
}

func readDesiredAssignment(t *testing.T, path string) DeploymentAssignment {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	var assignment DeploymentAssignment
	if err := json.Unmarshal(raw, &assignment); err != nil {
		t.Fatal(err)
	}
	return assignment
}
