// Package scannerreleaseworkspace owns the bounded, build-local hand-off
// between independently executed scanner release steps.
//
// Durable build state remains authoritative.  This ledger exists because the
// release manifest and final publication receipt need transitive evidence,
// while StepRequest intentionally exposes only direct DAG dependencies.
package scannerreleaseworkspace

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/scannerpipeline"
)

const (
	EvidenceSchema    = "wolf.scanner-release-workspace-evidence/v1"
	ContextSchema     = "wolf.scanner-release-execution-context/v1"
	maximumFileBytes  = 4 << 20
	maximumFiles      = 256
	maximumTotalBytes = 64 << 20
)

// Binding repeats every immutable build input needed to reject evidence from
// another candidate, build attempt, or policy snapshot.
type Binding struct {
	BuildRunID       string `json:"build_run_id"`
	CandidateID      string `json:"candidate_id"`
	BuildAttempt     int    `json:"build_attempt"`
	DefinitionCommit string `json:"definition_commit"`
	LockDigest       string `json:"lock_digest"`
	PolicyID         string `json:"policy_id"`
	PolicyRevision   int64  `json:"policy_revision"`
}

func NewBinding(
	buildRunID, candidateID string,
	buildAttempt int,
	definitionCommit, lockDigest, policyID string,
	policyRevision int64,
) Binding {
	return Binding{
		BuildRunID: buildRunID, CandidateID: candidateID,
		BuildAttempt: buildAttempt, DefinitionCommit: definitionCommit,
		LockDigest: lockDigest, PolicyID: policyID, PolicyRevision: policyRevision,
	}
}

type Evidence struct {
	SchemaVersion string               `json:"schema_version"`
	Step          scannerpipeline.Step `json:"step"`
	StepAttempt   int                  `json:"step_attempt"`
	Binding       Binding              `json:"binding"`
	Result        json.RawMessage      `json:"result"`
}

// RegistryTarget is a non-secret snapshot. CredentialReference remains an
// opaque deployment reference and is deliberately never written here.
type RegistryTarget struct {
	ID         string `json:"id"`
	Version    int64  `json:"version"`
	Host       string `json:"host"`
	Namespace  string `json:"namespace"`
	Repository string `json:"repository_prefix"`
}

// ExecutionContext is provisioned once per ephemeral build workspace by the
// managed coordinator.  It contains only public coordinates; raw Git,
// registry, and signing credentials stay in backend-owned providers/mounts.
type ExecutionContext struct {
	SchemaVersion string         `json:"schema_version"`
	SourceURL     string         `json:"source_url"`
	Primary       RegistryTarget `json:"primary"`
	Mirror        RegistryTarget `json:"mirror"`
	Stable        *StableRelease `json:"stable,omitempty"`
}

// StableRelease is the credential-free, immutable comparison baseline copied
// from the latest durable stable release. Quality adapters never infer a
// floating tag or registry default.
type StableRelease struct {
	ID               string        `json:"id"`
	LockDigest       string        `json:"lock_digest"`
	ManifestDigest   string        `json:"manifest_digest"`
	DefinitionCommit string        `json:"definition_commit"`
	Images           []StableImage `json:"images"`
	Tools            []StableTool  `json:"tools"`
}

type StableImage struct {
	Key             string `json:"key"`
	Repository      string `json:"repository"`
	Digest          string `json:"digest"`
	PlatformDigests string `json:"platform_digests"`
}

type StableTool struct {
	Key                 string `json:"key"`
	ImageKey            string `json:"image_key"`
	Kind                string `json:"kind"`
	SourceReference     string `json:"source_reference"`
	SourceDigest        string `json:"source_digest,omitempty"`
	ParserCompatibility string `json:"parser_compatibility"`
}

func WriteEvidence(
	workspace string,
	step scannerpipeline.Step,
	stepAttempt int,
	binding Binding,
	result any,
) error {
	if err := validateWorkspace(workspace); err != nil {
		return err
	}
	resultValue, err := json.Marshal(result)
	if err != nil {
		return err
	}
	document := Evidence{
		SchemaVersion: EvidenceSchema, Step: step,
		StepAttempt: stepAttempt, Binding: binding, Result: resultValue,
	}
	value, err := json.Marshal(document)
	if err != nil {
		return err
	}
	if len(value) > maximumFileBytes {
		return errors.New("scanner release workspace evidence exceeds size limit")
	}
	return atomicWrite(evidencePath(workspace, step.Key), value)
}

func (e Evidence) DecodeResult(target any) error {
	decoder := json.NewDecoder(bytes.NewReader(e.Result))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("scanner release workspace result has trailing JSON")
	}
	return nil
}

func ReadEvidence(workspace, stepKey string, binding Binding) (Evidence, error) {
	var document Evidence
	if err := readStrictFile(evidencePath(workspace, stepKey), &document); err != nil {
		return Evidence{}, err
	}
	if document.SchemaVersion != EvidenceSchema || document.Step.Key != stepKey ||
		document.Binding != binding || document.StepAttempt <= 0 {
		return Evidence{}, errors.New("scanner release workspace evidence binding is invalid")
	}
	return document, nil
}

func ReadAllEvidence(workspace string, binding Binding) (map[string]Evidence, error) {
	if err := validateWorkspace(workspace); err != nil {
		return nil, err
	}
	directory := filepath.Join(workspace, ".wolf-release-evidence")
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	if len(entries) > maximumFiles {
		return nil, errors.New("scanner release workspace evidence exceeds file-count limit")
	}
	result := make(map[string]Evidence, len(entries))
	var totalBytes int64
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			return nil, errors.New("scanner release workspace evidence directory contains an unexpected entry")
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		totalBytes += info.Size()
		if totalBytes > maximumTotalBytes {
			return nil, errors.New("scanner release workspace evidence exceeds aggregate size limit")
		}
		var document Evidence
		if err := readStrictFile(filepath.Join(directory, entry.Name()), &document); err != nil {
			return nil, err
		}
		if document.SchemaVersion != EvidenceSchema || document.Binding != binding ||
			document.Step.Key == "" || document.StepAttempt <= 0 ||
			evidenceFilename(document.Step.Key) != entry.Name() {
			return nil, errors.New("scanner release workspace evidence binding is invalid")
		}
		if _, duplicate := result[document.Step.Key]; duplicate {
			return nil, fmt.Errorf("scanner release workspace evidence repeats step %q", document.Step.Key)
		}
		result[document.Step.Key] = document
	}
	return result, nil
}

// ReadPlanEvidence requires exactly the completed transitive plan prefix
// supplied by the caller. Final assemblers use this instead of accepting
// arbitrary workspace files.
func ReadPlanEvidence(
	workspace string,
	binding Binding,
	expected []scannerpipeline.Step,
) (map[string]Evidence, error) {
	result, err := ReadAllEvidence(workspace, binding)
	if err != nil {
		return nil, err
	}
	wanted := make(map[string]struct{}, len(expected))
	for _, step := range expected {
		if step.Key == "" {
			return nil, errors.New("scanner release expected evidence contains an empty step")
		}
		wanted[step.Key] = struct{}{}
		if _, exists := result[step.Key]; !exists {
			return nil, fmt.Errorf("scanner release workspace is missing evidence %q", step.Key)
		}
	}
	if len(result) != len(wanted) {
		for key := range result {
			if _, exists := wanted[key]; !exists {
				return nil, fmt.Errorf("scanner release workspace contains unexpected evidence %q", key)
			}
		}
		return nil, errors.New("scanner release workspace evidence count is invalid")
	}
	return result, nil
}

func WriteContext(workspace string, context ExecutionContext) error {
	if err := validateWorkspace(workspace); err != nil {
		return err
	}
	context.SchemaVersion = ContextSchema
	value, err := json.Marshal(context)
	if err != nil {
		return err
	}
	if len(value) > maximumFileBytes {
		return errors.New("scanner release execution context exceeds size limit")
	}
	path := filepath.Join(workspace, ".wolf-release-context.json")
	if existing, readErr := os.ReadFile(path); readErr == nil {
		if bytes.Equal(existing, value) {
			return nil
		}
		return errors.New("scanner release execution context is already bound to different targets")
	} else if !os.IsNotExist(readErr) {
		return readErr
	}
	return atomicWrite(path, value)
}

func ReadContext(workspace string) (ExecutionContext, error) {
	var context ExecutionContext
	if err := readStrictFile(filepath.Join(workspace, ".wolf-release-context.json"), &context); err != nil {
		return ExecutionContext{}, err
	}
	if context.SchemaVersion != ContextSchema || context.SourceURL == "" ||
		context.Primary.ID == "" || context.Primary.Host == "" ||
		context.Primary.Namespace == "" || context.Primary.Repository == "" ||
		context.Mirror.ID == "" || context.Mirror.Host == "" ||
		context.Mirror.Namespace == "" || context.Mirror.Repository == "" {
		return ExecutionContext{}, errors.New("scanner release execution context is incomplete")
	}
	if context.Stable != nil {
		if err := validateStableRelease(*context.Stable); err != nil {
			return ExecutionContext{}, err
		}
	}
	return context, nil
}

func validateStableRelease(stable StableRelease) error {
	if strings.TrimSpace(stable.ID) == "" || !digestValue(stable.LockDigest) ||
		!digestValue(stable.ManifestDigest) || len(stable.DefinitionCommit) != 40 ||
		len(stable.Images) == 0 || len(stable.Tools) == 0 {
		return errors.New("scanner release stable comparison context is incomplete")
	}
	images := make(map[string]bool, len(stable.Images))
	for _, image := range stable.Images {
		if image.Key == "" || images[image.Key] || image.Repository == "" ||
			strings.ContainsAny(image.Repository, " \t\r\n") || strings.Contains(image.Repository, "@") ||
			!digestValue(image.Digest) || strings.TrimSpace(image.PlatformDigests) == "" {
			return errors.New("scanner release stable image context is invalid")
		}
		images[image.Key] = true
	}
	tools := make(map[string]bool, len(stable.Tools))
	for _, tool := range stable.Tools {
		if tool.Key == "" || tools[tool.Key] || !images[tool.ImageKey] ||
			(tool.Kind != "wolf" && tool.Kind != "upstream") ||
			tool.SourceReference == "" || tool.ParserCompatibility == "" ||
			(tool.Kind == "upstream" && (!digestValue(tool.SourceDigest) ||
				!strings.Contains(tool.SourceReference, "@"+tool.SourceDigest))) {
			return errors.New("scanner release stable tool context is invalid")
		}
		tools[tool.Key] = true
	}
	return nil
}

func digestValue(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil && strings.ToLower(value) == value
}

func evidencePath(workspace, stepKey string) string {
	return filepath.Join(workspace, ".wolf-release-evidence", evidenceFilename(stepKey))
}

func evidenceFilename(stepKey string) string {
	sum := sha256.Sum256([]byte(stepKey))
	return hex.EncodeToString(sum[:]) + ".json"
}

func validateWorkspace(workspace string) error {
	if !filepath.IsAbs(workspace) {
		return errors.New("scanner release workspace must be absolute")
	}
	info, err := os.Lstat(workspace)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("scanner release workspace must be a real directory")
	}
	return nil
}

func readStrictFile(path string, target any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > maximumFileBytes {
		return errors.New("scanner release workspace document is not a bounded regular file")
	}
	decoder := json.NewDecoder(io.LimitReader(file, maximumFileBytes+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("scanner release workspace document has trailing JSON")
	}
	return nil
}

func atomicWrite(path string, value []byte) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".wolf-release-*.tmp")
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
	return os.Rename(temporaryPath, path)
}
