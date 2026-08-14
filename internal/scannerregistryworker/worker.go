// Package scannerregistryworker executes durable registry reconciliation,
// repair, and quarantine cleanup jobs.
package scannerregistryworker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannerobservability"
	"github.com/alphabravocompany/thewolf/internal/scannerregistry"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
	"github.com/alphabravocompany/thewolf/internal/scannertrace"
)

type Store interface {
	scannerrelease.RegistryRepository
	scannerrelease.ReleaseRepository
}

// ClientFactory builds one client that is explicitly configured for both
// origins. Implementations resolve opaque secret references without returning
// credential material in errors.
type ClientFactory interface {
	Pair(
		context.Context,
		*scannerrelease.RegistryTarget,
		*scannerrelease.RegistryTarget,
	) (scannerregistry.Client, string, string, error)
	Single(
		context.Context,
		*scannerrelease.RegistryTarget,
	) (scannerregistry.Client, string, error)
}

// Resigner is intentionally injected. Registry repair never guesses signing
// authority. A required re-sign policy fails closed when no authorized signer
// adapter is configured.
type Resigner interface {
	ReSign(
		context.Context,
		scannerrelease.RegistryJob,
		scannerregistry.Reference,
		scannerregistry.Reference,
	) (signatureDigest string, err error)
}

type Config struct {
	Store             Store
	Clients           ClientFactory
	Resigner          Resigner
	WorkerID          string
	PollInterval      time.Duration
	HeartbeatInterval time.Duration
	LeaseDuration     time.Duration
	OperationTimeout  time.Duration
	DrainTimeout      time.Duration
	BaseBackoff       time.Duration
	MaxBackoff        time.Duration
	Once              bool
	Observer          scannerobservability.Observer
	Now               func() time.Time
}

type Worker struct {
	config Config
}

func New(config Config) (*Worker, error) {
	if config.Store == nil || config.Clients == nil || strings.TrimSpace(config.WorkerID) == "" {
		return nil, errors.New("registry worker requires store, client factory, and worker ID")
	}
	if config.PollInterval <= 0 {
		config.PollInterval = 2 * time.Second
	}
	if config.HeartbeatInterval <= 0 {
		config.HeartbeatInterval = 10 * time.Second
	}
	if config.LeaseDuration <= config.HeartbeatInterval {
		config.LeaseDuration = 45 * time.Second
	}
	if config.OperationTimeout <= 0 {
		config.OperationTimeout = 30 * time.Minute
	}
	if config.DrainTimeout <= 0 {
		config.DrainTimeout = 2 * time.Minute
	}
	if config.BaseBackoff <= 0 {
		config.BaseBackoff = 15 * time.Second
	}
	if config.MaxBackoff <= 0 {
		config.MaxBackoff = 30 * time.Minute
	}
	if config.Now == nil {
		config.Now = func() time.Time { return time.Now().UTC() }
	}
	if config.Observer == nil {
		config.Observer = scannerobservability.Default
	}
	return &Worker{config: config}, nil
}

func (w *Worker) Run(ctx context.Context) error {
	w.config.Observer.SetState(scannerobservability.ComponentRegistry, "active")
	for {
		started := w.config.Now()
		reclaimed, err := w.config.Store.ReclaimStaleRegistryJobs(ctx, started)
		if err != nil {
			w.config.Observer.ObserveRun(scannerobservability.ComponentRegistry, "failure", time.Since(started))
			if w.config.Once {
				return err
			}
		} else {
			w.config.Observer.SetStuckWork(
				scannerobservability.ComponentRegistry, "dead_letter", reclaimed.DeadLettered,
			)
			err = w.runOne(ctx)
			if err != nil && !errors.Is(err, context.Canceled) {
				w.config.Observer.ObserveRun(scannerobservability.ComponentRegistry, "failure", time.Since(started))
				if w.config.Once {
					return err
				}
			} else {
				w.config.Observer.ObserveRun(scannerobservability.ComponentRegistry, "success", time.Since(started))
			}
		}
		if w.config.Once {
			return nil
		}
		select {
		case <-ctx.Done():
			w.config.Observer.SetState(scannerobservability.ComponentRegistry, "stopped")
			return ctx.Err()
		case <-time.After(w.config.PollInterval):
		}
	}
}

func (w *Worker) runOne(ctx context.Context) error {
	now := w.config.Now()
	job, err := w.config.Store.ClaimNextRegistryJob(
		ctx, w.config.WorkerID, now, now.Add(w.config.LeaseDuration),
	)
	if err != nil {
		w.config.Observer.ObserveClaim(scannerobservability.ComponentRegistry, "error")
		return err
	}
	if job == nil {
		w.config.Observer.ObserveClaim(scannerobservability.ComponentRegistry, "empty")
		return nil
	}
	w.config.Observer.ObserveClaim(scannerobservability.ComponentRegistry, "acquired")
	traceContext, _, traceErr := scannertrace.Resume(
		ctx, w.config.Store, "registry_job", job.ID, "registry-worker",
	)
	if traceErr != nil {
		return fmt.Errorf("resume registry operation correlation: %w", traceErr)
	}
	scannertrace.Logger(traceContext).Info().
		Str("aggregate_type", "registry_job").
		Str("aggregate_id", job.ID).
		Str("state", string(job.State)).
		Msg("scanner release work claimed")
	operationContext, cancel := context.WithTimeout(traceContext, w.config.OperationTimeout)
	defer cancel()
	heartbeatErrors := make(chan error, 1)
	stopHeartbeat := make(chan struct{})
	go w.heartbeat(operationContext, *job, stopHeartbeat, heartbeatErrors)
	summary, runErr := w.execute(operationContext, *job)
	close(stopHeartbeat)
	select {
	case heartbeatErr := <-heartbeatErrors:
		if heartbeatErr != nil && runErr == nil {
			runErr = heartbeatErr
		}
	default:
	}
	summaryJSON, _ := json.Marshal(summary)
	finished := w.config.Now()
	target := scannerrelease.RegistryJobCompleted
	availableAt := finished
	errorClass, errorDetail := "", ""
	if runErr != nil {
		errorClass = classify(runErr)
		errorDetail = runErr.Error()
		if job.Attempt >= job.MaxAttempts {
			target = scannerrelease.RegistryJobDeadLetter
		} else {
			target = scannerrelease.RegistryJobRetry
			availableAt = finished.Add(w.backoff(job.Attempt))
			w.config.Observer.ObserveRetry(scannerobservability.ComponentRegistry, errorClass)
		}
	}
	_, finalizeErr := w.config.Store.FinalizeRegistryJob(
		ctx, job.ID, w.config.WorkerID, job.LeaseToken, target, availableAt,
		string(summaryJSON), errorClass, errorDetail, finished,
	)
	if finalizeErr != nil {
		return finalizeErr
	}
	w.config.Observer.ObserveResult(scannerobservability.ComponentRegistry, string(target))
	logEvent := scannertrace.Logger(traceContext).Info()
	if runErr != nil {
		logEvent = scannertrace.Logger(traceContext).Warn()
	}
	logEvent.
		Str("aggregate_type", "registry_job").
		Str("aggregate_id", job.ID).
		Str("state", string(target)).
		Str("error_class", errorClass).
		Msg("scanner release work finalized")
	return nil
}

func (w *Worker) heartbeat(
	ctx context.Context,
	job scannerrelease.RegistryJob,
	stop <-chan struct{},
	result chan<- error,
) {
	ticker := time.NewTicker(w.config.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			result <- nil
			return
		case <-ctx.Done():
			result <- ctx.Err()
			return
		case <-ticker.C:
			now := w.config.Now()
			status, err := w.config.Store.HeartbeatRegistryJob(
				ctx, job.ID, w.config.WorkerID, job.LeaseToken,
				now, now.Add(w.config.LeaseDuration),
			)
			if err != nil {
				result <- err
				return
			}
			if !status.Current {
				result <- scannerrelease.ErrLeaseNotOwned
				return
			}
			w.config.Observer.ObserveLease(scannerobservability.ComponentRegistry, "heartbeat")
		}
	}
}

type jobSummary struct {
	Kind       scannerrelease.RegistryJobKind `json:"kind"`
	Checked    int                            `json:"checked"`
	Matched    int                            `json:"matched"`
	Drifted    int                            `json:"drifted"`
	Repaired   int                            `json:"repaired"`
	Deleted    int                            `json:"deleted"`
	Retained   int                            `json:"retained"`
	Failed     int                            `json:"failed"`
	FinishedAt time.Time                      `json:"finished_at"`
}

func (w *Worker) execute(
	ctx context.Context,
	job scannerrelease.RegistryJob,
) (jobSummary, error) {
	summary := jobSummary{Kind: job.Kind}
	var err error
	switch job.Kind {
	case scannerrelease.RegistryJobReconcile, scannerrelease.RegistryJobRepair:
		err = w.reconcile(ctx, job, &summary)
	case scannerrelease.RegistryJobCleanup:
		err = w.cleanup(ctx, job, &summary)
	default:
		err = errors.New("unsupported registry job kind")
	}
	summary.FinishedAt = w.config.Now()
	return summary, err
}

func (w *Worker) reconcile(
	ctx context.Context,
	job scannerrelease.RegistryJob,
	summary *jobSummary,
) error {
	destinationTarget, err := w.config.Store.GetRegistryTarget(ctx, job.RegistryTargetID)
	if err != nil {
		return err
	}
	if !destinationTarget.Enabled {
		return errors.New("destination registry target is disabled")
	}
	var (
		client          scannerregistry.Client
		sourceHost      string
		destinationHost string
		sourceTarget    *scannerrelease.RegistryTarget
	)
	if job.SourceRegistryTargetID != "" {
		sourceTarget, err = w.config.Store.GetRegistryTarget(ctx, job.SourceRegistryTargetID)
		if err != nil {
			return err
		}
		if !sourceTarget.Enabled {
			return errors.New("source registry target is disabled")
		}
		client, sourceHost, destinationHost, err = w.config.Clients.Pair(
			ctx, sourceTarget, destinationTarget,
		)
	} else {
		client, destinationHost, err = w.config.Clients.Single(ctx, destinationTarget)
	}
	if err != nil {
		return err
	}
	inventory, err := w.config.Store.GetReleaseInventory(ctx, job.ReleaseID)
	if err != nil {
		return err
	}
	images := releaseImagesByKey(inventory.Images)
	var failures []error
	for _, key := range sortedKeys(images) {
		versions := images[key]
		destinationImage := imageForTarget(versions, job.RegistryTargetID)
		sourceImage := imageForTarget(versions, job.SourceRegistryTargetID)
		expected := destinationImage
		if expected == nil {
			expected = sourceImage
		}
		if expected == nil {
			expected = &versions[0]
		}
		observation := scannerrelease.RegistryImageObservation{
			JobID: job.ID, ImageKey: key, ExpectedDigest: expected.Digest,
			ExpectedProvenanceDigest: expected.ProvenanceDigest,
			ExpectedSBOMDigest:       expected.SBOMDigest, State: "pending",
			CheckedAt: w.config.Now(), DetailJSON: "{}",
		}
		observation.ExpectedSignatureDigest = signatureDigest(inventory.Artifacts, key)
		destinationRepository := targetRepository(destinationTarget, expected.Repository)
		destinationReference := scannerregistry.Reference{
			Registry: destinationHost, Repository: destinationRepository, Digest: expected.Digest,
		}
		observation.DestinationReference = destinationReference.String()
		if strings.EqualFold(expected.SignatureStatus, "verified") &&
			observation.ExpectedSignatureDigest == "" {
			failures = append(failures, fmt.Errorf(
				"%s: release inventory marks the signature verified but has no immutable signature artifact digest",
				key,
			))
			observation.State = "failed"
			observation.DetailJSON = `{"error":"signature_digest_inventory_missing"}`
			summary.Failed++
			_ = w.config.Store.UpsertRegistryImageObservation(ctx, &observation)
			continue
		}
		var sourceReference scannerregistry.Reference
		if sourceTarget != nil {
			if sourceImage == nil {
				failures = append(failures, fmt.Errorf("%s: release has no source registry image", key))
				observation.State = "failed"
				observation.DetailJSON = `{"error":"source_inventory_missing"}`
				summary.Failed++
				_ = w.config.Store.UpsertRegistryImageObservation(ctx, &observation)
				continue
			}
			sourceReference = scannerregistry.Reference{
				Registry:   sourceHost,
				Repository: targetRepository(sourceTarget, sourceImage.Repository),
				Digest:     sourceImage.Digest,
			}
			observation.SourceReference = sourceReference.String()
			sourceManifest, sourceErr := client.FetchManifest(ctx, sourceReference)
			if sourceErr != nil {
				failures = append(failures, fmt.Errorf("%s source readback: %w", key, sourceErr))
				observation.State = "failed"
				observation.DetailJSON = `{"error":"source_readback_failed"}`
				summary.Failed++
				_ = w.config.Store.UpsertRegistryImageObservation(ctx, &observation)
				continue
			}
			observation.SourceDigest = sourceManifest.Digest
			if sourceManifest.Digest != expected.Digest {
				failures = append(failures, fmt.Errorf("%s source digest differs from release inventory", key))
				observation.State = "failed"
				observation.DetailJSON = `{"error":"source_digest_mismatch"}`
				summary.Failed++
				_ = w.config.Store.UpsertRegistryImageObservation(ctx, &observation)
				continue
			}
			sourceEvidence, sourceEvidenceErr := client.ReadEvidence(
				ctx, sourceReference, expectedEvidence(observation),
			)
			if sourceEvidenceErr != nil || !allFound(sourceEvidence) {
				evidenceFailure := sourceEvidenceErr
				if evidenceFailure == nil {
					evidenceFailure = errors.New("one or more release evidence digests are not attached to the source subject")
				}
				failures = append(
					failures,
					fmt.Errorf("%s source trust evidence readback failed: %w", key, evidenceFailure),
				)
				observation.State = "failed"
				observation.DetailJSON = `{"error":"source_evidence_mismatch"}`
				summary.Failed++
				_ = w.config.Store.UpsertRegistryImageObservation(ctx, &observation)
				continue
			}
			observation.DetailJSON = `{"source_evidence_verified":true}`
		}
		summary.Checked++
		drifted := false
		destinationManifest, destinationErr := client.FetchManifest(ctx, destinationReference)
		if destinationErr != nil {
			drifted = true
		} else {
			observation.DestinationDigest = destinationManifest.Digest
			drifted = destinationManifest.Digest != expected.Digest
		}
		evidence := expectedEvidence(observation)
		evidenceStatus, evidenceErr := client.ReadEvidence(ctx, destinationReference, evidence)
		if evidenceErr != nil {
			drifted = true
		} else {
			applyEvidenceReadback(&observation, evidence, evidenceStatus)
			for _, found := range evidenceStatus {
				if !found {
					drifted = true
				}
			}
		}
		if !drifted {
			observation.State = "matched"
			summary.Matched++
			if err := w.config.Store.UpsertRegistryImageObservation(ctx, &observation); err != nil {
				failures = append(failures, err)
			}
			continue
		}
		summary.Drifted++
		observation.State = "drifted"
		if job.Kind != scannerrelease.RegistryJobRepair {
			if err := w.config.Store.UpsertRegistryImageObservation(ctx, &observation); err != nil {
				failures = append(failures, err)
			}
			continue
		}
		if sourceTarget == nil {
			failures = append(failures, fmt.Errorf("%s: repair requires source registry", key))
			observation.State = "failed"
			summary.Failed++
			_ = w.config.Store.UpsertRegistryImageObservation(ctx, &observation)
			continue
		}
		observation.State = "copying"
		_ = w.config.Store.UpsertRegistryImageObservation(ctx, &observation)
		if err := client.CopyManifestGraph(ctx, sourceReference, destinationReference); err != nil {
			failures = append(failures, fmt.Errorf("%s copy: %w", key, err))
			w.recordPartialPublish(
				ctx, job, inventory.Release.CandidateID,
				destinationReference, "manifest", err,
			)
			observation.State = "failed"
			summary.Failed++
			_ = w.config.Store.UpsertRegistryImageObservation(ctx, &observation)
			continue
		}
		signatureExpected := observation.ExpectedSignatureDigest
		_ = signatureExpected
		if job.ReSignPolicy == scannerrelease.RegistryReSignRequired {
			if w.config.Resigner == nil {
				failures = append(failures, fmt.Errorf("%s: required re-sign adapter is unavailable", key))
				observation.State = "failed"
				summary.Failed++
				_ = w.config.Store.UpsertRegistryImageObservation(ctx, &observation)
				continue
			}
			signatureExpected, err = w.config.Resigner.ReSign(ctx, job, sourceReference, destinationReference)
			if err != nil || signatureExpected == "" {
				failures = append(failures, fmt.Errorf("%s re-sign: %w", key, err))
				observation.State = "failed"
				summary.Failed++
				_ = w.config.Store.UpsertRegistryImageObservation(ctx, &observation)
				continue
			}
			observation.ExpectedSignatureDigest = signatureExpected
		} else {
			for kind, digest := range evidence {
				if digest == "" {
					continue
				}
				evidenceSource := sourceReference
				evidenceSource.Digest = digest
				evidenceDestination := destinationReference
				evidenceDestination.Digest = digest
				if err := client.CopyManifestGraph(ctx, evidenceSource, evidenceDestination); err != nil {
					failures = append(failures, fmt.Errorf("%s %s copy: %w", key, kind, err))
					objectKind := kind
					if objectKind == "signature" || objectKind == "provenance" || objectKind == "sbom" {
						w.recordPartialPublish(
							ctx, job, inventory.Release.CandidateID,
							evidenceDestination, objectKind, err,
						)
					}
					observation.State = "failed"
					summary.Failed++
					break
				}
			}
			if observation.State == "failed" {
				_ = w.config.Store.UpsertRegistryImageObservation(ctx, &observation)
				continue
			}
		}
		readback, readbackErr := client.FetchManifest(ctx, destinationReference)
		status, statusErr := client.ReadEvidence(ctx, destinationReference, expectedEvidence(observation))
		if readbackErr != nil || statusErr != nil || readback.Digest != expected.Digest || !allFound(status) {
			failures = append(failures, fmt.Errorf("%s destination readback did not verify", key))
			observation.State = "failed"
			summary.Failed++
		} else {
			observation.DestinationDigest = readback.Digest
			applyEvidenceReadback(&observation, expectedEvidence(observation), status)
			observation.State = "repaired"
			summary.Repaired++
		}
		if err := w.config.Store.UpsertRegistryImageObservation(ctx, &observation); err != nil {
			failures = append(failures, err)
		}
	}
	parity := "matched"
	health := "healthy"
	if summary.Drifted > summary.Repaired || summary.Failed > 0 {
		parity, health = "mismatched", "degraded"
	}
	detail, _ := json.Marshal(summary)
	_ = w.config.Store.UpdateRegistryObservation(ctx, job.RegistryTargetID, scannerrelease.RegistryObservation{
		HealthStatus: health, CheckedAt: w.config.Now(), DigestParityStatus: parity,
		DetailJSON: string(detail),
	})
	return errors.Join(failures...)
}

func (w *Worker) cleanup(
	ctx context.Context,
	job scannerrelease.RegistryJob,
	summary *jobSummary,
) error {
	target, err := w.config.Store.GetRegistryTarget(ctx, job.RegistryTargetID)
	if err != nil {
		return err
	}
	client, host, err := w.config.Clients.Single(ctx, target)
	if err != nil {
		return err
	}
	var failures []error
	for _, state := range []string{"orphaned", "quarantined", "delete_failed"} {
		objects, listErr := w.config.Store.ListRegistryQuarantineObjects(
			ctx, target.ID, state, 500,
		)
		if listErr != nil {
			failures = append(failures, listErr)
			continue
		}
		for _, object := range objects {
			now := w.config.Now()
			authorized, decision, authorizeErr := w.config.Store.AuthorizeRegistryQuarantineDeletion(
				ctx, object.ID, w.config.WorkerID, now, now.Add(w.config.LeaseDuration),
			)
			if authorizeErr != nil {
				failures = append(failures, authorizeErr)
				continue
			}
			if !decision.Eligible {
				summary.Retained++
				continue
			}
			if object.ObjectKind == "blob" {
				_ = w.config.Store.CompleteRegistryQuarantineDeletion(
					ctx, authorized.ID, w.config.WorkerID, authorized.DeletionLeaseToken,
					false, "blob deletion is not supported by OCI Distribution", w.config.Now(),
				)
				summary.Retained++
				continue
			}
			reference := scannerregistry.Reference{
				Registry: host, Repository: object.Repository, Digest: object.Digest,
			}
			deleted, deleteErr := client.DeleteManifest(ctx, reference)
			errorDetail := ""
			if deleteErr != nil {
				errorDetail = deleteErr.Error()
				failures = append(failures, deleteErr)
			}
			if completeErr := w.config.Store.CompleteRegistryQuarantineDeletion(
				ctx, authorized.ID, w.config.WorkerID, authorized.DeletionLeaseToken,
				deleted && deleteErr == nil, errorDetail, w.config.Now(),
			); completeErr != nil {
				failures = append(failures, completeErr)
			} else if deleted && deleteErr == nil {
				summary.Deleted++
			} else {
				summary.Failed++
			}
		}
	}
	return errors.Join(failures...)
}

func (w *Worker) recordPartialPublish(
	ctx context.Context,
	job scannerrelease.RegistryJob,
	candidateID string,
	reference scannerregistry.Reference,
	objectKind string,
	cause error,
) {
	now := w.config.Now()
	detail, _ := json.Marshal(map[string]any{
		"registry_job_id": job.ID,
		"error_class":     classify(cause),
	})
	retainUntil := now.Add(24 * time.Hour)
	_ = w.config.Store.UpsertRegistryQuarantineObject(
		ctx, &scannerrelease.RegistryQuarantineObject{
			RegistryTargetID: job.RegistryTargetID, CandidateID: candidateID,
			Repository: reference.Repository, Digest: reference.Digest,
			ObjectKind: objectKind, State: "quarantined",
			RetentionClass: "partial_publish", RetainUntil: &retainUntil,
			DiscoveredAt: now, MetadataJSON: string(detail),
		},
	)
}

func releaseImagesByKey(images []scannerrelease.ReleaseImage) map[string][]scannerrelease.ReleaseImage {
	result := make(map[string][]scannerrelease.ReleaseImage)
	for _, image := range images {
		result[image.ImageKey] = append(result[image.ImageKey], image)
	}
	return result
}

func sortedKeys(values map[string][]scannerrelease.ReleaseImage) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	for i := 1; i < len(result); i++ {
		for j := i; j > 0 && result[j] < result[j-1]; j-- {
			result[j], result[j-1] = result[j-1], result[j]
		}
	}
	return result
}

func imageForTarget(
	images []scannerrelease.ReleaseImage,
	targetID string,
) *scannerrelease.ReleaseImage {
	if targetID == "" {
		return nil
	}
	for index := range images {
		if images[index].RegistryTargetID == targetID {
			return &images[index]
		}
	}
	return nil
}

func targetRepository(target *scannerrelease.RegistryTarget, repository string) string {
	repository = strings.Trim(repository, "/")
	namespace := strings.Trim(target.Namespace, "/")
	if namespace != "" && !strings.HasPrefix(repository, namespace+"/") {
		return namespace + "/" + repository
	}
	return repository
}

func signatureDigest(artifacts []scannerrelease.ReleaseArtifact, imageKey string) string {
	for _, artifact := range artifacts {
		kind := strings.ToLower(artifact.ArtifactType)
		if strings.Contains(kind, "signature") &&
			(imageKey == "" || !strings.Contains(kind, ":") ||
				strings.HasSuffix(kind, ":"+imageKey)) {
			return artifact.Digest
		}
	}
	return ""
}

func expectedEvidence(observation scannerrelease.RegistryImageObservation) map[string]string {
	return map[string]string{
		"signature":  observation.ExpectedSignatureDigest,
		"provenance": observation.ExpectedProvenanceDigest,
		"sbom":       observation.ExpectedSBOMDigest,
	}
}

func applyEvidenceReadback(
	observation *scannerrelease.RegistryImageObservation,
	expected map[string]string,
	found map[string]bool,
) {
	if found["signature"] {
		observation.DestinationSignatureDigest = expected["signature"]
	}
	if found["provenance"] {
		observation.DestinationProvenanceDigest = expected["provenance"]
	}
	if found["sbom"] {
		observation.DestinationSBOMDigest = expected["sbom"]
	}
}

func allFound(values map[string]bool) bool {
	for _, found := range values {
		if !found {
			return false
		}
	}
	return true
}

func classify(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "operation_timeout"
	case errors.Is(err, scannerrelease.ErrLeaseNotOwned):
		return "lease_lost"
	default:
		return "registry_operation_failed"
	}
}

func (w *Worker) backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := w.config.BaseBackoff
	for index := 1; index < attempt && delay < w.config.MaxBackoff; index++ {
		delay *= 2
	}
	if delay > w.config.MaxBackoff {
		return w.config.MaxBackoff
	}
	return delay
}
