package scannerrelease

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"
)

const PublicationReceiptSchema = "wolf.scanner-publication-receipt/v1"

// PublicationReceipt is the immutable hand-off between the release build and
// the control plane. It is emitted by the final evidence step only after the
// complete DAG has succeeded. API callers may select this receipt by digest,
// but they cannot supply or override its authoritative inventory.
type PublicationReceipt struct {
	SchemaVersion        string            `json:"schema_version"`
	CandidateID          string            `json:"candidate_id"`
	BuildRunID           string            `json:"build_run_id"`
	DefinitionCommit     string            `json:"definition_commit"`
	LockDigest           string            `json:"lock_digest"`
	PolicyID             string            `json:"policy_id"`
	PolicyRevision       int64             `json:"policy_revision"`
	PolicyDecisionDigest string            `json:"policy_decision_digest"`
	ManifestDigest       string            `json:"manifest_digest"`
	ManifestURI          string            `json:"manifest_uri"`
	SignerIdentity       string            `json:"signer_identity"`
	Tools                []ReleaseTool     `json:"tools"`
	Images               []ReleaseImage    `json:"images"`
	Artifacts            []ReleaseArtifact `json:"artifacts,omitempty"`
}

// CanonicalPublicationReceipt returns a copy with deterministic inventory
// ordering and database-owned fields removed. This makes a receipt digest
// stable across replicas and retries while retaining every security-relevant
// identity and evidence field.
func CanonicalPublicationReceipt(receipt PublicationReceipt) PublicationReceipt {
	canonical := receipt
	canonical.Tools = append([]ReleaseTool(nil), receipt.Tools...)
	canonical.Images = append([]ReleaseImage(nil), receipt.Images...)
	canonical.Artifacts = append([]ReleaseArtifact(nil), receipt.Artifacts...)
	for index := range canonical.Tools {
		canonical.Tools[index].ID = ""
		canonical.Tools[index].ReleaseID = ""
		canonical.Tools[index].CreatedAt = time.Time{}
	}
	for index := range canonical.Images {
		canonical.Images[index].ID = ""
		canonical.Images[index].ReleaseID = ""
		canonical.Images[index].CreatedAt = time.Time{}
	}
	for index := range canonical.Artifacts {
		canonical.Artifacts[index].ID = ""
		canonical.Artifacts[index].ReleaseID = ""
		canonical.Artifacts[index].CreatedAt = time.Time{}
	}
	sort.Slice(canonical.Tools, func(i, j int) bool {
		return canonical.Tools[i].ToolKey < canonical.Tools[j].ToolKey
	})
	sort.Slice(canonical.Images, func(i, j int) bool {
		left := canonical.Images[i].RegistryTargetID + "\x00" + canonical.Images[i].ImageKey
		right := canonical.Images[j].RegistryTargetID + "\x00" + canonical.Images[j].ImageKey
		return left < right
	})
	sort.Slice(canonical.Artifacts, func(i, j int) bool {
		left := canonical.Artifacts[i].ArtifactType + "\x00" + canonical.Artifacts[i].URI
		right := canonical.Artifacts[j].ArtifactType + "\x00" + canonical.Artifacts[j].URI
		return left < right
	})
	return canonical
}

// PublicationReceiptDigest returns the canonical SHA-256 identity used by
// approvals and publication commands.
func PublicationReceiptDigest(receipt PublicationReceipt) (string, error) {
	value, err := json.Marshal(CanonicalPublicationReceipt(receipt))
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
