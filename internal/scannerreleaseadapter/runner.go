// Package scannerreleaseadapter implements the three first-party managed
// scanner-release adapter executables. The coordinator still owns policy,
// routing, and immutable bindings; adapters only execute their compiled lane
// catalog and persist bounded, content-addressed evidence.
package scannerreleaseadapter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/scannerreleasebackend"
	"github.com/alphabravocompany/thewolf/internal/scannerreleaseworker"
	"github.com/alphabravocompany/thewolf/internal/scannersigning"
)

const (
	inputLimit                 = 4 << 20
	evidencePayloadLimit       = 256 << 20
	actionJournalMetadataLimit = 8 << 20
	artifactMediaType          = "application/vnd.wolf.scanner-release-adapter-evidence.v1+json"
)

// ActionResult is the trusted, bounded hand-off from a compiled action
// implementation to the durable artifact publisher. Payload is persisted as
// an OCI blob; Summary contains only allowlisted domain fields.
type ActionResult struct {
	Payload       []byte
	MediaType     string
	Summary       map[string]any
	PolicyInput   *scannerreleaseworker.PolicyInput
	Runtime       string
	ImageDigests  map[string]string
	SubjectURI    string
	SubjectDigest string
	// ImageManifestPayload is the exact registry response used only by the
	// OCI annotation gate. Runner carries it separately from the gate report
	// so the trusted backend can re-hash and strictly decode the subject bytes.
	ImageManifestPayload   []byte
	ImageManifestMediaType string
	// StorageIdentity is reserved for artifacts whose OCI manifest is the
	// domain subject itself (notably the aggregate release manifest).
	StorageIdentity bool
}

// ActionExecutor implements the compiled lane catalog. Implementations must
// not interpret deployment-provided commands or arguments.
type ActionExecutor interface {
	Execute(context.Context, scannerreleasebackend.AdapterLane, scannerreleasebackend.Invocation, string) (ActionResult, error)
}

// PublishRequest binds an artifact to the immutable external operation. A
// non-empty SubjectDigest requires an OCI referrer attached to SubjectURI.
type PublishRequest struct {
	Workspace     string
	OperationID   string
	Action        string
	CommandID     string
	Payload       []byte
	MediaType     string
	SubjectURI    string
	SubjectDigest string
}

// PublishedArtifact distinguishes the OCI manifest/referrer identity from
// its exact payload identity. They are both read back before success.
type PublishedArtifact struct {
	URI              string
	Digest           string
	PayloadDigest    string
	MediaType        string
	SizeBytes        int64
	StorageMediaType string
	StorageSizeBytes int64
	ReadBackVerified bool
}

type Publisher interface {
	Publish(context.Context, PublishRequest) (PublishedArtifact, error)
}

type Runner struct {
	Lane      scannerreleasebackend.AdapterLane
	Actions   ActionExecutor
	Publisher Publisher
}

// Run consumes exactly one full backend invocation and emits exactly one
// BackendResult. Unknown JSON fields, trailing input, lane drift, and stale
// bindings fail before any action or registry mutation.
func (r Runner) Run(ctx context.Context, input io.Reader, output io.Writer) error {
	if r.Actions == nil || r.Publisher == nil {
		return errors.New("scanner release adapter actions and publisher are required")
	}
	if _, err := scannerreleasebackend.AdapterActionPatterns(r.Lane); err != nil {
		return err
	}
	invocation, err := decodeInvocation(input)
	if err != nil {
		return err
	}
	if err := scannerreleasebackend.ValidateInvocation(invocation); err != nil {
		return err
	}
	allowed, _ := scannerreleasebackend.AdapterActionPatterns(r.Lane)
	if !matchesAction(allowed, invocation.Action.Name) {
		return fmt.Errorf("lane %q cannot execute action %q", r.Lane, invocation.Action.Name)
	}
	commandID, err := scannerreleasebackend.AdapterCommandID(invocation.Action.Name)
	if err != nil {
		return err
	}
	action, found, err := loadActionJournal(r.Lane, invocation, commandID)
	if err != nil {
		return err
	}
	if !found {
		action, err = r.Actions.Execute(ctx, r.Lane, invocation, commandID)
		if err != nil {
			return fmt.Errorf("execute %s: %w", commandID, err)
		}
		if err := persistActionJournal(r.Lane, invocation, commandID, action); err != nil {
			return err
		}
		// Always consume the committed representation, including on the first
		// attempt. JSON normalization then makes the action handed to publishing
		// byte-for-byte equivalent to every recovery attempt.
		action, found, err = loadActionJournal(r.Lane, invocation, commandID)
		if err != nil || !found {
			return errors.New("committed adapter action journal could not be recovered")
		}
	}
	if len(action.Payload) == 0 {
		action.Payload, err = canonicalActionPayload(r.Lane, invocation, commandID, action.Summary)
		if err != nil {
			return err
		}
	}
	if len(action.Payload) > evidencePayloadLimit {
		return errors.New("scanner release adapter evidence payload exceeds size limit")
	}
	if strings.TrimSpace(action.MediaType) == "" {
		action.MediaType = artifactMediaType
	}
	artifact, err := r.Publisher.Publish(ctx, PublishRequest{
		Workspace:   invocation.Request.Workspace,
		OperationID: invocation.OperationID, Action: invocation.Action.Name,
		CommandID: commandID, Payload: action.Payload, MediaType: action.MediaType,
		SubjectURI: action.SubjectURI, SubjectDigest: action.SubjectDigest,
	})
	if err != nil {
		return fmt.Errorf("publish %s evidence: %w", commandID, err)
	}
	if err := validatePublishedArtifact(artifact, action.Payload); err != nil {
		return err
	}
	if action.StorageIdentity {
		if invocation.Action.Name != "release-manifest" {
			return errors.New("OCI storage identity is only supported for the release manifest")
		}
		if err := scannerreleasebackend.WriteSigningDescriptor(
			invocation.Request.Workspace, "release-manifest-signature",
			scannersigning.Artifact{
				URI: artifact.URI, Digest: artifact.Digest,
				MediaType: artifact.StorageMediaType,
			},
		); err != nil {
			return fmt.Errorf("write release manifest signing descriptor: %w", err)
		}
	}
	summary := cloneSummary(action.Summary)
	evidence := map[string]any{
		"schema_version": scannerreleasebackend.AdapterEvidenceSchema,
		"lane":           r.Lane, "action": invocation.Action.Name,
		"operation_id": invocation.OperationID, "command_id": commandID,
		"artifact": map[string]any{
			"uri": artifact.URI, "digest": artifact.Digest,
			"payload_digest": artifact.PayloadDigest,
			"media_type":     artifact.MediaType, "size_bytes": artifact.SizeBytes,
			"storage_media_type": artifact.StorageMediaType,
			"storage_size_bytes": artifact.StorageSizeBytes,
			"read_back_verified": artifact.ReadBackVerified,
		},
	}
	outputDigest := artifact.PayloadDigest
	outputIdentity := "payload"
	if action.StorageIdentity {
		outputDigest = artifact.Digest
		outputIdentity = "storage"
	}
	evidence["output_identity"] = outputIdentity
	if action.Runtime != "" {
		evidence["runtime"] = action.Runtime
	}
	if len(action.ImageDigests) != 0 {
		evidence["image_digests"] = action.ImageDigests
	}
	if action.SubjectDigest != "" {
		evidence["subject_digest"] = action.SubjectDigest
		evidence["referrer_digest"] = artifact.Digest
	}
	if len(action.ImageManifestPayload) != 0 {
		if !strings.HasPrefix(invocation.Action.Name, "oci-annotations/") ||
			action.SubjectDigest == "" ||
			sha256Digest(action.ImageManifestPayload) != action.SubjectDigest ||
			strings.TrimSpace(action.ImageManifestMediaType) == "" {
			return errors.New("OCI annotation action did not return an exact subject manifest")
		}
		evidence["image_manifest"] = map[string]any{
			"digest": action.SubjectDigest, "media_type": action.ImageManifestMediaType,
			"payload_base64": base64.StdEncoding.EncodeToString(action.ImageManifestPayload),
		}
	}
	summary["adapter_evidence"] = evidence
	result := scannerreleasebackend.BackendResult{
		Binding: invocation.Binding, ExternalOperationID: invocation.OperationID,
		Result: scannerreleaseworker.StepResult{
			// Most adapter outputs use their domain payload identity. The release
			// manifest is itself an OCI subject and uses its storage-manifest digest.
			OutputURI: artifact.URI, OutputDigest: outputDigest,
			Summary: summary, PolicyInput: action.PolicyInput,
			Verification: scannerreleaseworker.Verification{
				DefinitionCommit: invocation.Binding.DefinitionCommit,
				LockDigest:       invocation.Binding.LockDigest,
				PolicyID:         invocation.Binding.PolicyID,
				PolicyRevision:   invocation.Binding.PolicyRevision,
			},
		},
	}
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(result)
}

const actionJournalSchema = "wolf.scanner-release-adapter-action-journal/v1"

type actionJournal struct {
	SchemaVersion    string                            `json:"schema_version"`
	InvocationDigest string                            `json:"invocation_digest"`
	Lane             scannerreleasebackend.AdapterLane `json:"lane"`
	Action           string                            `json:"action"`
	CommandID        string                            `json:"command_id"`
	PayloadDigest    string                            `json:"payload_digest"`
	PayloadSize      int64                             `json:"payload_size_bytes"`
	Result           ActionResult                      `json:"result"`
}

func adapterActionJournalPath(invocation scannerreleasebackend.Invocation) (string, error) {
	workspace, err := filepath.Abs(invocation.Request.Workspace)
	if err != nil || workspace != filepath.Clean(invocation.Request.Workspace) ||
		!digest(invocation.OperationID) {
		return "", errors.New("adapter action journal workspace or operation is invalid")
	}
	// The platform temporary directory on macOS commonly traverses /var ->
	// /private/var. Resolve the trusted workspace itself, then reject symlinks
	// for the journal directories that this process owns beneath it.
	workspace, err = filepath.EvalSymlinks(workspace)
	if err != nil {
		return "", errors.New("adapter action journal workspace cannot be resolved")
	}
	info, err := os.Stat(workspace)
	if err != nil || !info.IsDir() {
		return "", errors.New("adapter action journal workspace is invalid")
	}
	root := filepath.Join(workspace, ".wolf-release-backend-journal")
	if err := createOwnedDirectory(root); err != nil {
		return "", err
	}
	directory := filepath.Join(root, strings.TrimPrefix(invocation.OperationID, "sha256:"))
	if err := createOwnedDirectory(directory); err != nil {
		return "", err
	}
	return filepath.Join(directory, "adapter-action-committed"), nil
}

func createOwnedDirectory(path string) error {
	if err := os.Mkdir(path, 0o700); err != nil && !os.IsExist(err) {
		return fmt.Errorf("create adapter action journal: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("adapter action journal directory is invalid")
	}
	return nil
}

func actionInvocationDigest(invocation scannerreleasebackend.Invocation) (string, error) {
	value, err := json.Marshal(invocation)
	if err != nil {
		return "", err
	}
	return sha256Digest(value), nil
}

func expectedActionJournal(
	lane scannerreleasebackend.AdapterLane,
	invocation scannerreleasebackend.Invocation,
	commandID string,
) (actionJournal, error) {
	digest, err := actionInvocationDigest(invocation)
	if err != nil {
		return actionJournal{}, err
	}
	return actionJournal{
		SchemaVersion: actionJournalSchema, InvocationDigest: digest,
		Lane: lane, Action: invocation.Action.Name, CommandID: commandID,
	}, nil
}

func loadActionJournal(
	lane scannerreleasebackend.AdapterLane,
	invocation scannerreleasebackend.Invocation,
	commandID string,
) (ActionResult, bool, error) {
	directory, err := adapterActionJournalPath(invocation)
	if err != nil {
		return ActionResult{}, false, err
	}
	info, err := os.Lstat(directory)
	if os.IsNotExist(err) {
		return ActionResult{}, false, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ActionResult{}, false, errors.New("adapter action journal committed directory is invalid")
	}
	value, err := readActionJournalFile(directory, "result.json", actionJournalMetadataLimit)
	if err != nil || len(value) == 0 {
		return ActionResult{}, false, errors.New("adapter action journal is unreadable or exceeds its bound")
	}
	var journal actionJournal
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&journal); err != nil {
		return ActionResult{}, false, errors.New("adapter action journal is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ActionResult{}, false, errors.New("adapter action journal has trailing JSON")
	}
	expected, err := expectedActionJournal(lane, invocation, commandID)
	if err != nil {
		return ActionResult{}, false, err
	}
	if journal.SchemaVersion != expected.SchemaVersion ||
		journal.InvocationDigest != expected.InvocationDigest || journal.Lane != expected.Lane ||
		journal.Action != expected.Action || journal.CommandID != expected.CommandID {
		return ActionResult{}, false, errors.New("adapter action journal immutable binding mismatch")
	}
	payload, err := readActionJournalFile(directory, "payload", evidencePayloadLimit)
	if err != nil || int64(len(payload)) != journal.PayloadSize ||
		journal.PayloadDigest != sha256Digest(payload) {
		return ActionResult{}, false, errors.New("adapter action journal payload identity mismatch")
	}
	journal.Result.Payload = payload
	return journal.Result, true, nil
}

func persistActionJournal(
	lane scannerreleasebackend.AdapterLane,
	invocation scannerreleasebackend.Invocation,
	commandID string,
	result ActionResult,
) error {
	directory, err := adapterActionJournalPath(invocation)
	if err != nil {
		return err
	}
	journal, err := expectedActionJournal(lane, invocation, commandID)
	if err != nil {
		return err
	}
	if len(result.Payload) > evidencePayloadLimit {
		return errors.New("adapter action journal result exceeds its bound")
	}
	journal.PayloadDigest = sha256Digest(result.Payload)
	journal.PayloadSize = int64(len(result.Payload))
	journal.Result = result
	journal.Result.Payload = nil
	value, err := json.Marshal(journal)
	if err != nil || len(value) > actionJournalMetadataLimit {
		return errors.New("adapter action journal result exceeds its bound")
	}
	staging, err := os.MkdirTemp(filepath.Dir(directory), ".adapter-action-stage-")
	if err != nil {
		return err
	}
	removeStaging := true
	defer func() {
		if removeStaging {
			_ = os.RemoveAll(staging)
		}
	}()
	if err := writeSyncedActionJournalFile(filepath.Join(staging, "payload"), result.Payload); err != nil {
		return err
	}
	if err := writeSyncedActionJournalFile(filepath.Join(staging, "result.json"), value); err != nil {
		return err
	}
	stagingDirectory, err := os.Open(staging)
	if err != nil {
		return err
	}
	syncErr := stagingDirectory.Sync()
	closeErr := stagingDirectory.Close()
	if syncErr != nil || closeErr != nil {
		return errors.New("sync adapter action journal transaction")
	}
	if err := os.Rename(staging, directory); err != nil {
		existing, found, loadErr := loadActionJournal(lane, invocation, commandID)
		if loadErr != nil || !found {
			return fmt.Errorf("commit adapter action journal transaction: %w", err)
		}
		existingValue, _ := json.Marshal(existing)
		resultValue, _ := json.Marshal(result)
		if !bytes.Equal(existingValue, resultValue) {
			return errors.New("same adapter operation produced conflicting action results")
		}
		return nil
	}
	removeStaging = false
	return nil
}

func readActionJournalFile(directory, name string, maximum int) ([]byte, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > int64(maximum) {
		return nil, errors.New("adapter action journal file is invalid")
	}
	value, err := io.ReadAll(io.LimitReader(file, int64(maximum)+1))
	if err != nil || int64(len(value)) != info.Size() {
		return nil, errors.New("adapter action journal file read mismatch")
	}
	return value, nil
}

func writeSyncedActionJournalFile(path string, value []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(value); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

func decodeInvocation(input io.Reader) (scannerreleasebackend.Invocation, error) {
	value, err := io.ReadAll(io.LimitReader(input, inputLimit+1))
	if err != nil {
		return scannerreleasebackend.Invocation{}, err
	}
	if len(value) > inputLimit {
		return scannerreleasebackend.Invocation{}, errors.New("scanner release adapter invocation exceeds size limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	var invocation scannerreleasebackend.Invocation
	if err := decoder.Decode(&invocation); err != nil {
		return invocation, fmt.Errorf("decode scanner release adapter invocation: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return invocation, errors.New("scanner release adapter invocation has trailing JSON")
	}
	return invocation, nil
}

func canonicalActionPayload(
	lane scannerreleasebackend.AdapterLane,
	invocation scannerreleasebackend.Invocation,
	commandID string,
	summary map[string]any,
) ([]byte, error) {
	document := struct {
		SchemaVersion string                            `json:"schema_version"`
		Lane          scannerreleasebackend.AdapterLane `json:"lane"`
		Action        string                            `json:"action"`
		CommandID     string                            `json:"command_id"`
		OperationID   string                            `json:"operation_id"`
		Binding       scannerreleasebackend.Binding     `json:"binding"`
		Summary       map[string]any                    `json:"summary,omitempty"`
	}{
		SchemaVersion: scannerreleasebackend.AdapterEvidenceSchema,
		Lane:          lane, Action: invocation.Action.Name, CommandID: commandID,
		OperationID: invocation.OperationID, Binding: invocation.Binding,
		Summary: summary,
	}
	return json.Marshal(document)
}

func validatePublishedArtifact(artifact PublishedArtifact, payload []byte) error {
	if artifact.URI == "" || !digest(artifact.Digest) || !digest(artifact.PayloadDigest) ||
		artifact.MediaType == "" || artifact.SizeBytes < 0 || artifact.StorageMediaType == "" ||
		artifact.StorageSizeBytes <= 0 || !artifact.ReadBackVerified ||
		!strings.Contains(artifact.URI, artifact.Digest) {
		return errors.New("scanner release adapter artifact is not immutable and read-back verified")
	}
	if artifact.PayloadDigest != sha256Digest(payload) {
		return errors.New("scanner release adapter payload digest does not match published artifact")
	}
	return nil
}

func matchesAction(patterns []string, action string) bool {
	for _, pattern := range patterns {
		if pattern == action || (strings.HasSuffix(pattern, "/*") &&
			strings.HasPrefix(action, strings.TrimSuffix(pattern, "*"))) {
			return true
		}
	}
	return false
}

func cloneSummary(input map[string]any) map[string]any {
	result := make(map[string]any, len(input)+1)
	for key, value := range input {
		result[key] = value
	}
	return result
}

func sha256Digest(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func digest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil && value == strings.ToLower(value)
}

// Main is shared by the three tiny lane-specific command packages.
func Main(lane scannerreleasebackend.AdapterLane) {
	runner, err := ProductionRunner(lane)
	if err == nil {
		err = runner.Run(context.Background(), os.Stdin, os.Stdout)
	}
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, redact(err.Error()))
		os.Exit(1)
	}
}

func redact(value string) string {
	// Adapter errors never include child output. This final bound prevents an
	// unexpected operating-system error from becoming unbounded Job output.
	const maximum = 4096
	if len(value) > maximum {
		return value[:maximum] + "…"
	}
	return value
}
