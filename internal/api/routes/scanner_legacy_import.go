package routes

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	scannercontainer "github.com/alphabravocompany/thewolf/internal/plugin/container"
	"github.com/alphabravocompany/thewolf/internal/scannercontrol"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

type legacyReleaseImportRequest struct {
	Reason          string            `json:"reason"`
	ResolvedDigests map[string]string `json:"resolved_digests,omitempty"`
}

type legacyConfiguredImage struct {
	Key        string `json:"key"`
	Reference  string `json:"reference"`
	Digest     string `json:"digest"`
	Kind       string `json:"kind"`
	Tool       string `json:"tool,omitempty"`
	Entrypoint string `json:"entrypoint,omitempty"`
}

type legacyImportSnapshot struct {
	SchemaVersion         int                     `json:"schema_version"`
	Images                []legacyConfiguredImage `json:"images"`
	ProvenanceLimitations []string                `json:"provenance_limitations"`
}

// ScannerSupplyChainImportLegacyConfig snapshots the process's configured
// scanner references. It is opt-in, does not pull or retag images, and does not
// change desired release or any queued/running scan assignment.
func ScannerSupplyChainImportLegacyConfig(w http.ResponseWriter, r *http.Request) {
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	key, ok := scannerIdempotencyKey(w, r)
	if !ok {
		return
	}
	var request legacyReleaseImportRequest
	if !scannerDecode(w, r, &request) {
		return
	}
	request.Reason = strings.TrimSpace(request.Reason)
	if request.Reason == "" {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "reason is required")
		return
	}
	cfg := scannercontainer.Default()
	if cfg == nil || strings.TrimSpace(cfg.Image) == "" {
		response.WriteError(w, http.StatusConflict, "legacy_config_unavailable", "configured scanner images are unavailable")
		return
	}
	snapshot, err := configuredLegacySnapshot(cfg, request.ResolvedDigests)
	if err != nil {
		response.WriteError(w, http.StatusBadRequest, "legacy_config_invalid", err.Error())
		return
	}
	digest, err := digestJSON(snapshot)
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	releaseID := deterministicImportID("legacy-release-", digest, 32)
	if existing, getErr := store.GetReleaseInventory(r.Context(), releaseID); getErr == nil {
		response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]any{
			"release": existing.Release, "images": existing.Images,
			"created": false, "provenance_limitations": snapshot.ProvenanceLimitations,
			"runtime_assignments_changed": false,
		}})
		return
	} else if !errors.Is(getErr, sql.ErrNoRows) {
		scannerWriteError(w, getErr)
		return
	}

	actor := scannerActor(r)
	policy, err := (scannercontrol.Service{Store: store}).EnsureDefaultPolicy(r.Context(), actor)
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	registryIDs := make(map[string]string)
	for _, image := range snapshot.Images {
		host := legacyRegistryHost(image.Reference)
		if _, exists := registryIDs[host]; exists {
			continue
		}
		registryIDs[host], err = ensureImportedRegistry(r.Context(), store, host, actor)
		if err != nil {
			scannerWriteError(w, err)
			return
		}
	}
	candidateID := deterministicImportID("legacy-candidate-", digest, 32)
	candidate := scannerrelease.Candidate{
		ID: candidateID, DefinitionCommit: "legacy-config-import",
		SelectionJSON: string(mustJSON(snapshot)), LockDigest: digest,
		LockURI:         "legacy://configured-images/" + strings.TrimPrefix(digest, "sha256:"),
		RiskSummaryJSON: `{"source":"legacy_runtime_configuration","provenance":"unverified","runnable":false}`,
		State:           scannerrelease.CandidatePublished, RequiredGatesJSON: `[]`,
		PolicyDecision: "legacy_unverified", PolicyID: policy.ID, PolicyRevision: policy.Revision,
		Actor: actor, IdempotencyKey: "legacy-import:" + key,
	}
	command := scannerrelease.TransitionCommand{
		Actor: actor, Reason: request.Reason, PolicyRevision: policy.Revision,
		IdempotencyKey: key + "/legacy-import",
		PayloadJSON:    fmt.Sprintf(`{"configuration_digest":%q,"runtime_assignments_changed":false}`, digest),
	}
	if existing, getErr := store.GetCandidate(r.Context(), candidateID); getErr == nil {
		if existing.LockDigest != digest || existing.DefinitionCommit != candidate.DefinitionCommit {
			scannerWriteError(w, scannerrelease.ErrIdempotencyConflict)
			return
		}
		candidate = *existing
	} else if errors.Is(getErr, sql.ErrNoRows) {
		if err := store.CreateCandidate(r.Context(), &candidate, command); err != nil {
			scannerWriteError(w, err)
			return
		}
	} else {
		scannerWriteError(w, getErr)
		return
	}

	now := time.Now().UTC()
	inventory := scannerrelease.ReleaseInventory{Release: scannerrelease.Release{
		ID: releaseID, Name: "legacy-config-" + strings.TrimPrefix(digest, "sha256:")[:12],
		CandidateID: candidate.ID, LockDigest: digest, ManifestDigest: digest,
		ManifestURI: "legacy://configured-images/" + strings.TrimPrefix(digest, "sha256:"),
		State:       scannerrelease.ReleasePublished, SignerIdentity: "legacy-unverified",
		PolicyID: policy.ID, PolicyRevision: policy.Revision,
		DefinitionCommit: "legacy-config-import", Imported: true, Legacy: true,
		Protected: true, RollbackEligible: false, RetentionClass: "legacy",
		PublishedAt: now,
	}}
	for _, configured := range snapshot.Images {
		inventory.Images = append(inventory.Images, scannerrelease.ReleaseImage{
			ID: uuid.NewString(), ImageKey: configured.Key,
			RegistryTargetID: registryIDs[legacyRegistryHost(configured.Reference)],
			Repository:       configured.Reference, Digest: configured.Digest,
			PlatformDigests: "{}", SignatureStatus: "legacy_unverified",
		})
		if configured.Tool != "" {
			metadata, _ := json.Marshal(map[string]string{
				"image_key": configured.Key, "kind": configured.Kind,
				"entrypoint": configured.Entrypoint, "original_reference": configured.Reference,
				"provenance": "legacy_unverified",
			})
			inventory.Tools = append(inventory.Tools, scannerrelease.ReleaseTool{
				ID: uuid.NewString(), ToolKey: configured.Tool, Version: "unknown",
				SourceReference: configured.Reference, SourceDigest: configured.Digest,
				ParserCompatibility: "legacy-unverified", MetadataJSON: string(metadata),
			})
		}
	}
	if err := store.CreateRelease(r.Context(), &inventory, command); err != nil {
		if existing, getErr := store.GetReleaseInventory(r.Context(), releaseID); getErr == nil &&
			existing.Release.ManifestDigest == digest {
			inventory = *existing
		} else {
			scannerWriteError(w, err)
			return
		}
	}
	response.WriteJSON(w, http.StatusCreated, response.SuccessResponse{Data: map[string]any{
		"release": inventory.Release, "images": inventory.Images, "created": true,
		"provenance_limitations":      snapshot.ProvenanceLimitations,
		"runtime_assignments_changed": false,
	}})
}

func configuredLegacySnapshot(cfg *scannercontainer.Config, supplied map[string]string) (legacyImportSnapshot, error) {
	snapshot := legacyImportSnapshot{
		SchemaVersion: 1,
		ProvenanceLimitations: []string{
			"image signatures were not verified by the managed release pipeline",
			"SBOM and build provenance are unavailable",
			"the snapshot is historical evidence and is not rollout eligible",
		},
	}
	unusedDigests := make(map[string]string, len(supplied))
	for key, value := range supplied {
		unusedDigests[key] = value
	}
	add := func(image legacyConfiguredImage) error {
		image.Reference = strings.TrimSpace(image.Reference)
		if image.Reference == "" {
			return nil
		}
		image.Digest = digestFromLegacyReference(image.Reference)
		if image.Digest == "" {
			image.Digest = strings.TrimSpace(supplied[image.Key])
		}
		delete(unusedDigests, image.Key)
		if !validLegacyDigest(image.Digest) {
			return fmt.Errorf("resolved_digests[%q] must provide the immutable sha256 digest for tagged reference %q", image.Key, image.Reference)
		}
		snapshot.Images = append(snapshot.Images, image)
		return nil
	}
	if err := add(legacyConfiguredImage{Key: "default", Reference: cfg.Image, Kind: "wolf"}); err != nil {
		return snapshot, err
	}
	overrideTools := make([]string, 0, len(cfg.ImageOverrides))
	for tool := range cfg.ImageOverrides {
		overrideTools = append(overrideTools, tool)
	}
	sort.Strings(overrideTools)
	for _, tool := range overrideTools {
		if err := add(legacyConfiguredImage{
			Key: "wolf-" + tool, Reference: cfg.ImageOverrides[tool], Kind: "wolf", Tool: tool,
		}); err != nil {
			return snapshot, err
		}
	}
	upstreamTools := make([]string, 0, len(cfg.UpstreamTools))
	for tool := range cfg.UpstreamTools {
		upstreamTools = append(upstreamTools, tool)
	}
	sort.Strings(upstreamTools)
	for _, tool := range upstreamTools {
		spec := cfg.UpstreamTools[tool]
		if err := add(legacyConfiguredImage{
			Key: "upstream-" + tool, Reference: spec.Image, Kind: "upstream",
			Tool: tool, Entrypoint: spec.Entrypoint,
		}); err != nil {
			return snapshot, err
		}
	}
	sort.Slice(snapshot.Images, func(i, j int) bool { return snapshot.Images[i].Key < snapshot.Images[j].Key })
	if len(unusedDigests) != 0 {
		keys := make([]string, 0, len(unusedDigests))
		for key := range unusedDigests {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		return snapshot, fmt.Errorf("resolved_digests contains unknown configured image keys: %s", strings.Join(keys, ", "))
	}
	return snapshot, nil
}

func digestFromLegacyReference(reference string) string {
	index := strings.LastIndex(reference, "@")
	if index < 0 {
		return ""
	}
	return strings.TrimSpace(reference[index+1:])
}

func validLegacyDigest(value string) bool {
	if len(value) != 71 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, char := range value[7:] {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func legacyRegistryHost(reference string) string {
	name := strings.TrimSpace(reference)
	if at := strings.LastIndexByte(name, '@'); at >= 0 {
		name = name[:at]
	}
	if !strings.Contains(name, "/") {
		return "docker.io"
	}
	first := strings.SplitN(name, "/", 2)[0]
	if strings.Contains(first, ".") || strings.Contains(first, ":") || first == "localhost" {
		return first
	}
	return "docker.io"
}

func mustJSON(value any) []byte {
	raw, _ := json.Marshal(value)
	return raw
}
