package scannerrollout

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

type ReleaseInventoryStore interface {
	GetReleaseInventory(context.Context, string) (*scannerrelease.ReleaseInventory, error)
}

type DeploymentPlan struct {
	ReleaseID       string            `json:"release_id"`
	ManifestDigest  string            `json:"manifest_digest"`
	ImageDigests    map[string]string `json:"image_digests"`
	ImageReferences map[string]string `json:"image_references"`
}

func ResolveDeploymentPlan(
	ctx context.Context,
	store ReleaseInventoryStore,
	releaseID string,
) (DeploymentPlan, error) {
	if store == nil || strings.TrimSpace(releaseID) == "" {
		return DeploymentPlan{}, errors.New("deployment release inventory store and release ID are required")
	}
	inventory, err := store.GetReleaseInventory(ctx, releaseID)
	if err != nil {
		return DeploymentPlan{}, err
	}
	if inventory == nil || inventory.Release.ID != releaseID ||
		!validSyntheticDigest(inventory.Release.ManifestDigest) {
		return DeploymentPlan{}, errors.New("deployment release manifest identity is invalid")
	}
	plan := DeploymentPlan{
		ReleaseID: releaseID, ManifestDigest: inventory.Release.ManifestDigest,
		ImageDigests:    make(map[string]string, len(inventory.Images)),
		ImageReferences: make(map[string]string, len(inventory.Images)),
	}
	for _, image := range inventory.Images {
		if !scannerrelease.IsRuntimeScannerImage(image) {
			continue
		}
		if image.ImageKey == "" {
			return DeploymentPlan{}, errors.New("deployment image key is required")
		}
		reference, referenceErr := immutableImageReference(
			image.Repository, image.Digest,
		)
		if referenceErr != nil {
			return DeploymentPlan{}, fmt.Errorf(
				"deployment image %q does not have a valid immutable reference: %w",
				image.ImageKey, referenceErr,
			)
		}
		if _, duplicate := plan.ImageDigests[image.ImageKey]; duplicate {
			return DeploymentPlan{}, fmt.Errorf("duplicate deployment image key %q", image.ImageKey)
		}
		plan.ImageDigests[image.ImageKey] = image.Digest
		plan.ImageReferences[image.ImageKey] = reference
	}
	if len(plan.ImageDigests) == 0 {
		return DeploymentPlan{}, errors.New("deployment release has no images")
	}
	return plan, nil
}

var (
	ociPathComponentPattern = regexp.MustCompile(
		`^[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*$`,
	)
	ociRegistryPattern = regexp.MustCompile(
		`^(?:localhost|[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)(?:\.(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?))*(?::[1-9][0-9]{0,4})?$`,
	)
)

func immutableImageReference(repository, digest string) (string, error) {
	if repository == "" || strings.TrimSpace(repository) != repository ||
		len(repository) > 255 ||
		strings.ContainsAny(repository, "@?#\\") ||
		strings.Contains(repository, "://") ||
		strings.HasPrefix(repository, "/") ||
		strings.HasSuffix(repository, "/") ||
		strings.Contains(repository, "//") ||
		strings.IndexFunc(repository, func(value rune) bool {
			return unicode.IsControl(value) || unicode.IsSpace(value)
		}) >= 0 {
		return "", errors.New("OCI repository contains invalid syntax")
	}
	parts := strings.Split(repository, "/")
	pathStart := 0
	if len(parts) > 1 &&
		(strings.ContainsAny(parts[0], ".:") || parts[0] == "localhost") {
		if !ociRegistryPattern.MatchString(parts[0]) {
			return "", errors.New("OCI registry authority is invalid")
		}
		if separator := strings.LastIndexByte(parts[0], ':'); separator >= 0 {
			port, portErr := strconv.Atoi(parts[0][separator+1:])
			if portErr != nil || port > 65535 {
				return "", errors.New("OCI registry port is invalid")
			}
		}
		pathStart = 1
	}
	if pathStart == len(parts) {
		return "", errors.New("OCI repository path is missing")
	}
	for _, component := range parts[pathStart:] {
		if !ociPathComponentPattern.MatchString(component) {
			return "", errors.New("OCI repository path component is invalid")
		}
	}
	if !validSyntheticDigest(digest) {
		return "", errors.New("OCI image digest is invalid")
	}
	return repository + "@" + digest, nil
}

type CacheVerification struct {
	Digests    map[string]string `json:"digests"`
	VerifiedAt time.Time         `json:"verified_at"`
}

type ImageCache interface {
	Prepare(context.Context, string, DeploymentPlan) (CacheVerification, error)
}

type DeploymentAssignment struct {
	OperationID       string            `json:"operation_id"`
	RolloutID         string            `json:"rollout_id"`
	Target            string            `json:"target"`
	CohortID          string            `json:"cohort_id"`
	CohortName        string            `json:"cohort_name"`
	ReleaseID         string            `json:"release_id"`
	PreviousReleaseID string            `json:"previous_release_id,omitempty"`
	ManifestDigest    string            `json:"manifest_digest"`
	ImageDigests      map[string]string `json:"image_digests"`
	ImageReferences   map[string]string `json:"image_references"`
	CachedDigests     map[string]string `json:"cached_digests"`
	Rollback          bool              `json:"rollback"`
	AppliedAt         time.Time         `json:"applied_at"`
}

type DeploymentObservation struct {
	OperationID    string            `json:"operation_id"`
	ReleaseID      string            `json:"release_id"`
	ManifestDigest string            `json:"manifest_digest"`
	ImageDigests   map[string]string `json:"image_digests"`
	Ready          bool              `json:"ready"`
	ObservedAt     time.Time         `json:"observed_at"`
}

type DeploymentControl interface {
	Apply(context.Context, DeploymentAssignment) error
	Observe(context.Context, DeploymentAssignment) (DeploymentObservation, error)
	Pause(context.Context, DeploymentAssignment) error
	Resume(context.Context, DeploymentAssignment) error
	Cancel(context.Context, DeploymentAssignment) error
}

// CohortDeploymentRuntime provides the exact-digest assignment sequence used
// by both Compose and Kubernetes controls. It intentionally does not modify
// active scan records; only future worker assignments observe the new desired
// release, preserving overlapping stable scans on their original snapshot.
type CohortDeploymentRuntime struct {
	Name    string
	Store   ReleaseInventoryStore
	Status  Runtime
	Cache   ImageCache
	Control DeploymentControl
	Now     func() time.Time
}

func (r CohortDeploymentRuntime) Assign(
	ctx context.Context,
	request AssignmentRequest,
) error {
	if err := r.validate(); err != nil {
		return err
	}
	plan, err := ResolveDeploymentPlan(ctx, r.Store, request.DesiredReleaseID)
	if err != nil {
		return fmt.Errorf("%s resolve release: %w", r.Name, err)
	}
	cache, err := r.Cache.Prepare(ctx, request.OperationID, plan)
	if err != nil {
		return fmt.Errorf("%s pre-pull release: %w", r.Name, err)
	}
	if err := verifyCache(plan, cache); err != nil {
		return fmt.Errorf("%s cache verification: %w", r.Name, err)
	}
	assignment := deploymentAssignment(request, plan, cache, r.now())
	if observed, observeErr := r.Control.Observe(ctx, assignment); observeErr == nil &&
		observationMatches(assignment, observed) {
		return r.Status.Assign(ctx, request)
	}
	if err := r.Control.Apply(ctx, assignment); err != nil {
		return fmt.Errorf("%s apply cohort: %w", r.Name, err)
	}
	observed, err := r.Control.Observe(ctx, assignment)
	if err != nil {
		return fmt.Errorf("%s assignment readback: %w", r.Name, err)
	}
	if !observationMatches(assignment, observed) {
		return fmt.Errorf("%s assignment readback does not match exact release digests", r.Name)
	}
	if err := r.Status.Assign(ctx, request); err != nil {
		return fmt.Errorf("%s persist worker assignment: %w", r.Name, err)
	}
	return nil
}

func (r CohortDeploymentRuntime) Health(
	ctx context.Context,
	request HealthRequest,
) (HealthSnapshot, error) {
	if err := r.validate(); err != nil {
		return HealthSnapshot{}, err
	}
	plan, err := ResolveDeploymentPlan(ctx, r.Store, request.DesiredReleaseID)
	if err != nil {
		return HealthSnapshot{}, err
	}
	assignment := deploymentAssignment(
		AssignmentRequest{
			OperationID: request.OperationID, RolloutID: request.RolloutID,
			Target: request.Target, CohortID: request.CohortID,
			CohortName: request.CohortName, DesiredReleaseID: request.DesiredReleaseID,
		},
		plan, CacheVerification{Digests: plan.ImageDigests}, time.Time{},
	)
	observation, observationErr := r.Control.Observe(ctx, assignment)
	snapshot, err := r.Status.Health(ctx, request)
	if err != nil {
		return HealthSnapshot{}, err
	}
	if observationErr != nil || !observationMatches(assignment, observation) {
		snapshot.FailedWorkers++
		snapshot.Canary.ManifestFailures++
		snapshot.ObservedReleaseID = ""
		return snapshot, snapshot.Validate()
	}
	if observation.ObservedAt.After(snapshot.ObservedAt) {
		snapshot.ObservedAt = observation.ObservedAt.UTC()
	}
	return snapshot, snapshot.Validate()
}

func (r CohortDeploymentRuntime) Pause(
	ctx context.Context,
	request AssignmentRequest,
) error {
	assignment, err := r.resolveAssignment(ctx, request)
	if err != nil {
		return err
	}
	return r.Control.Pause(ctx, assignment)
}

func (r CohortDeploymentRuntime) Resume(
	ctx context.Context,
	request AssignmentRequest,
) error {
	assignment, err := r.resolveAssignment(ctx, request)
	if err != nil {
		return err
	}
	return r.Control.Resume(ctx, assignment)
}

func (r CohortDeploymentRuntime) Cancel(
	ctx context.Context,
	request AssignmentRequest,
) error {
	assignment, err := r.resolveAssignment(ctx, request)
	if err != nil {
		return err
	}
	return r.Control.Cancel(ctx, assignment)
}

func (r CohortDeploymentRuntime) resolveAssignment(
	ctx context.Context,
	request AssignmentRequest,
) (DeploymentAssignment, error) {
	plan, err := ResolveDeploymentPlan(ctx, r.Store, request.DesiredReleaseID)
	if err != nil {
		return DeploymentAssignment{}, err
	}
	return deploymentAssignment(
		request, plan, CacheVerification{Digests: plan.ImageDigests}, r.now(),
	), nil
}

func (r CohortDeploymentRuntime) validate() error {
	if strings.TrimSpace(r.Name) == "" || r.Store == nil || r.Status == nil ||
		r.Cache == nil || r.Control == nil {
		return errors.New("cohort deployment runtime requires name, store, status, cache, and control")
	}
	return nil
}

func (r CohortDeploymentRuntime) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

func deploymentAssignment(
	request AssignmentRequest,
	plan DeploymentPlan,
	cache CacheVerification,
	at time.Time,
) DeploymentAssignment {
	return DeploymentAssignment{
		OperationID: request.OperationID, RolloutID: request.RolloutID,
		Target: request.Target, CohortID: request.CohortID,
		CohortName: request.CohortName, ReleaseID: plan.ReleaseID,
		PreviousReleaseID: request.PreviousReleaseID,
		ManifestDigest:    plan.ManifestDigest,
		ImageDigests:      cloneStrings(plan.ImageDigests),
		ImageReferences:   cloneStrings(plan.ImageReferences),
		CachedDigests:     cloneStrings(cache.Digests),
		Rollback:          request.Rollback, AppliedAt: at,
	}
}

func verifyCache(plan DeploymentPlan, cache CacheVerification) error {
	if cache.VerifiedAt.IsZero() {
		return errors.New("cache verification time is missing")
	}
	if !reflect.DeepEqual(plan.ImageDigests, cache.Digests) {
		return errors.New("cached digests differ from the immutable release")
	}
	return nil
}

func observationMatches(
	assignment DeploymentAssignment,
	observation DeploymentObservation,
) bool {
	return observation.Ready && !observation.ObservedAt.IsZero() &&
		observation.OperationID == assignment.OperationID &&
		observation.ReleaseID == assignment.ReleaseID &&
		observation.ManifestDigest == assignment.ManifestDigest &&
		reflect.DeepEqual(observation.ImageDigests, assignment.ImageDigests)
}

func cloneStrings(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func sortedDeploymentKeys(values map[string]string) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func encodeDeploymentAssignment(value DeploymentAssignment) ([]byte, error) {
	return json.Marshal(value)
}
