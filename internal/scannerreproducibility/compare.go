// Package scannerreproducibility compares the deterministic properties of
// scanner releases produced by independent managed and customer factories.
package scannerreproducibility

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannerquality"
	scannerlock "github.com/alphabravocompany/thewolf/internal/scannertools/lock"
)

const (
	EvidenceSchema = "wolf.scanners.factory-reproducibility-evidence/v1"
	ReportSchema   = "wolf.scanners.factory-reproducibility-report/v1"
	maxInputBytes  = 128 << 20
)

var (
	digestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	commitPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)
	idPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:@/-]{0,127}$`)
)

var expectedImages = map[string]imageExpectation{
	"default":      {Kind: "scanner", Platforms: []string{"linux/amd64"}},
	"jvm":          {Kind: "scanner", Platforms: []string{"linux/amd64"}},
	"rust":         {Kind: "scanner", Platforms: []string{"linux/amd64"}},
	"codeql":       {Kind: "scanner", Platforms: []string{"linux/amd64"}},
	"fixer-base":   {Kind: "fixer", Platforms: []string{"linux/amd64"}},
	"fixer-api":    {Kind: "fixer", Platforms: []string{"linux/amd64"}},
	"fixer-claude": {Kind: "fixer", Platforms: []string{"linux/amd64"}},
	"fixer-codex":  {Kind: "fixer", Platforms: []string{"linux/amd64"}},
}

type imageExpectation struct {
	Kind      string
	Platforms []string
}

// Evidence is a signed/exported factory receipt. Provenance and SBOM values
// are normalized at collection time so the comparator does not need registry
// credentials and cannot silently infer missing evidence.
type Evidence struct {
	SchemaVersion string           `json:"schemaVersion"`
	Factory       FactoryIdentity  `json:"factory"`
	Release       ReleaseManifest  `json:"release"`
	ScannerLock   scannerlock.Lock `json:"scannerLock"`
	Policy        PolicyEvidence   `json:"policy"`
	Images        []ImageEvidence  `json:"images"`
	Quality       QualityEvidence  `json:"quality"`
}

type FactoryIdentity struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
}

type ReleaseManifest struct {
	SchemaVersion         string         `json:"schemaVersion"`
	ReleaseID             string         `json:"releaseId"`
	DefinitionCommit      string         `json:"definitionCommit"`
	DefinitionDigest      string         `json:"definitionDigest"`
	LockDigest            string         `json:"lockDigest"`
	ApprovalReceiptDigest *string        `json:"approvalReceiptDigest"`
	GeneratedAt           string         `json:"generatedAt"`
	Operation             string         `json:"operation"`
	AggregateSBOM         AggregateSBOM  `json:"aggregateSbom"`
	Images                []ReleaseImage `json:"images"`
}

type AggregateSBOM struct {
	MediaType string `json:"mediaType"`
	SHA256    string `json:"sha256"`
}

type ReleaseImage struct {
	Variant               string          `json:"variant"`
	ImageKind             string          `json:"imageKind"`
	Image                 string          `json:"image"`
	ReleaseID             string          `json:"releaseId"`
	LockDigest            string          `json:"lockDigest"`
	DefinitionDigest      string          `json:"definitionDigest"`
	ApprovalReceiptDigest *string         `json:"approvalReceiptDigest"`
	Digest                string          `json:"digest"`
	Platforms             []string        `json:"platforms"`
	BaseReference         string          `json:"baseReference,omitempty"`
	Primary               RegistryRecord  `json:"primary"`
	Mirror                *RegistryRecord `json:"mirror"`
	SBOMSHA256            string          `json:"sbom_sha256"`
	Evidence              ImageReceipt    `json:"evidence"`
	SignatureVerified     bool            `json:"signatureVerified"`
	ProvenanceVerified    bool            `json:"provenanceVerified"`
	SBOMVerified          bool            `json:"sbomVerified"`
}

type RegistryRecord struct {
	Repository         string `json:"repository"`
	Verified           bool   `json:"verified"`
	ReferrersSHA256    string `json:"referrersSha256,omitempty"`
	SignatureVerified  bool   `json:"signatureVerified,omitempty"`
	ProvenanceVerified bool   `json:"provenanceVerified,omitempty"`
	SBOMVerified       bool   `json:"sbomVerified,omitempty"`
}

type ImageReceipt struct {
	SignatureVerificationSHA256  string `json:"signatureVerificationSha256"`
	ProvenanceVerificationSHA256 string `json:"provenanceVerificationSha256"`
	SBOMVerificationSHA256       string `json:"sbomVerificationSha256"`
	ReferrersSHA256              string `json:"referrersSha256"`
}

type PolicyEvidence struct {
	Digest                 string `json:"digest"`
	DeclaredEvidenceDigest string `json:"declaredEvidenceDigest,omitempty"`
}

type ImageEvidence struct {
	Variant     string            `json:"variant"`
	Annotations map[string]string `json:"annotations"`
	Provenance  Provenance        `json:"provenance"`
	SBOM        SPDXDocument      `json:"sbom"`
}

type Provenance struct {
	BuildType    string     `json:"buildType"`
	BuilderID    string     `json:"builderId"`
	InvocationID string     `json:"invocationId,omitempty"`
	StartedAt    string     `json:"startedAt,omitempty"`
	FinishedAt   string     `json:"finishedAt,omitempty"`
	Materials    []Material `json:"materials"`
	Subjects     []Subject  `json:"subjects"`
}

type Material struct {
	URI    string            `json:"uri"`
	Digest map[string]string `json:"digest"`
}

type Subject struct {
	Name   string            `json:"name"`
	Digest map[string]string `json:"digest"`
}

type SPDXDocument struct {
	SPDXVersion       string        `json:"spdxVersion"`
	DataLicense       string        `json:"dataLicense"`
	Name              string        `json:"name"`
	DocumentNamespace string        `json:"documentNamespace"`
	CreationInfo      CreationInfo  `json:"creationInfo"`
	Packages          []SPDXPackage `json:"packages"`
}

type CreationInfo struct {
	Created  string   `json:"created"`
	Creators []string `json:"creators"`
}

type SPDXPackage struct {
	SPDXID           string            `json:"SPDXID"`
	Name             string            `json:"name"`
	VersionInfo      string            `json:"versionInfo,omitempty"`
	Supplier         string            `json:"supplier,omitempty"`
	DownloadLocation string            `json:"downloadLocation,omitempty"`
	FilesAnalyzed    *bool             `json:"filesAnalyzed,omitempty"`
	LicenseConcluded string            `json:"licenseConcluded,omitempty"`
	LicenseDeclared  string            `json:"licenseDeclared,omitempty"`
	Checksums        []SPDXChecksum    `json:"checksums,omitempty"`
	ExternalRefs     []SPDXExternalRef `json:"externalRefs,omitempty"`
}

type SPDXChecksum struct {
	Algorithm     string `json:"algorithm"`
	ChecksumValue string `json:"checksumValue"`
}

type SPDXExternalRef struct {
	ReferenceCategory string `json:"referenceCategory"`
	ReferenceType     string `json:"referenceType"`
	ReferenceLocator  string `json:"referenceLocator"`
}

type QualityEvidence struct {
	CorpusDigest string                    `json:"corpusDigest"`
	PolicyDigest string                    `json:"policyDigest"`
	Runs         []scannerquality.Evidence `json:"runs"`
}

type Report struct {
	SchemaVersion          string                  `json:"schemaVersion"`
	Equivalent             bool                    `json:"equivalent"`
	ManagedFactory         FactoryIdentity         `json:"managedFactory"`
	CustomerFactory        FactoryIdentity         `json:"customerFactory"`
	Comparisons            []Comparison            `json:"comparisons"`
	Mismatches             []string                `json:"mismatches,omitempty"`
	NondeterministicFields []NondeterministicField `json:"nondeterministicFields"`
}

type Comparison struct {
	Property       string `json:"property"`
	Equivalent     bool   `json:"equivalent"`
	ManagedDigest  string `json:"managedDigest"`
	CustomerDigest string `json:"customerDigest"`
}

type NondeterministicField struct {
	Path     string `json:"path"`
	Reason   string `json:"reason"`
	Managed  any    `json:"managed,omitempty"`
	Customer any    `json:"customer,omitempty"`
	Equal    bool   `json:"equal"`
}

func LoadFile(path string) (Evidence, error) {
	file, err := os.Open(path)
	if err != nil {
		return Evidence{}, err
	}
	defer file.Close()
	limited := &io.LimitedReader{R: file, N: maxInputBytes + 1}
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()
	var evidence Evidence
	if err := decoder.Decode(&evidence); err != nil {
		if limited.N == 0 {
			return Evidence{}, fmt.Errorf("factory evidence exceeds the %d-byte limit", maxInputBytes)
		}
		return Evidence{}, fmt.Errorf("decode factory evidence: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Evidence{}, errors.New("factory evidence contains trailing JSON or exceeds the size limit")
	}
	if limited.N == 0 {
		return Evidence{}, fmt.Errorf("factory evidence exceeds the %d-byte limit", maxInputBytes)
	}
	if err := Validate(evidence); err != nil {
		return Evidence{}, err
	}
	return evidence, nil
}

func Validate(e Evidence) error {
	if e.SchemaVersion != EvidenceSchema {
		return fmt.Errorf("unsupported factory evidence schema %q", e.SchemaVersion)
	}
	if !idPattern.MatchString(e.Factory.ID) || (e.Factory.Kind != "managed" && e.Factory.Kind != "customer") {
		return errors.New("factory requires a bounded ID and kind managed or customer")
	}
	if !digestPattern.MatchString(e.Release.LockDigest) || !digestPattern.MatchString(e.Release.DefinitionDigest) ||
		!commitPattern.MatchString(e.Release.DefinitionCommit) {
		return errors.New("release lock, definition digest, or definition commit is invalid")
	}
	if e.ScannerLock.LockDigest != e.Release.LockDigest || e.ScannerLock.Definition.Digest != e.Release.DefinitionDigest {
		return errors.New("embedded scanner lock does not match release identity")
	}
	if err := e.ScannerLock.Validate(); err != nil {
		return fmt.Errorf("embedded scanner lock is invalid: %w", err)
	}
	if err := validateLockImageMatrix(e.ScannerLock); err != nil {
		return err
	}
	if !digestPattern.MatchString(e.Policy.Digest) || !digestPattern.MatchString(e.Quality.CorpusDigest) ||
		!digestPattern.MatchString(e.Quality.PolicyDigest) || e.Policy.Digest != e.Quality.PolicyDigest {
		return errors.New("policy and quality corpus require matching exact digests")
	}
	if e.Policy.DeclaredEvidenceDigest != "" && !digestPattern.MatchString(e.Policy.DeclaredEvidenceDigest) {
		return errors.New("declared policy evidence digest is invalid")
	}
	if len(e.ScannerLock.Tools) == 0 {
		return errors.New("scanner lock has no tool inventory")
	}
	releaseImages, err := validateReleaseImages(e.Release)
	if err != nil {
		return err
	}
	if err := validateImageEvidence(e, releaseImages); err != nil {
		return err
	}
	if err := validateQuality(e); err != nil {
		return err
	}
	return nil
}

func validateLockImageMatrix(lock scannerlock.Lock) error {
	if len(lock.ReleaseInputs.Variants) != 4 || len(lock.ReleaseInputs.FixerVariants) != 4 {
		return errors.New("embedded scanner lock does not declare the canonical eight-image matrix")
	}
	for variant, expected := range expectedImages {
		var platforms []string
		if expected.Kind == "scanner" {
			definition, ok := lock.ReleaseInputs.Variants[variant]
			if !ok {
				return fmt.Errorf("embedded scanner lock is missing release variant %q", variant)
			}
			platforms = definition.Platforms
		} else {
			lockVariant := strings.TrimPrefix(variant, "fixer-")
			definition, ok := lock.ReleaseInputs.FixerVariants[lockVariant]
			if !ok {
				return fmt.Errorf("embedded scanner lock is missing fixer variant %q", lockVariant)
			}
			platforms = definition.Platforms
		}
		if !equalStrings(platforms, expected.Platforms) {
			return fmt.Errorf("embedded scanner lock variant %q has a non-canonical platform matrix", variant)
		}
	}
	return nil
}

func validateReleaseImages(release ReleaseManifest) (map[string]ReleaseImage, error) {
	if release.SchemaVersion != "wolf.scanners.release/v1" || len(release.Images) != len(expectedImages) ||
		!idPattern.MatchString(release.ReleaseID) || !validTimestamp(release.GeneratedAt) ||
		strings.TrimSpace(release.Operation) == "" ||
		release.AggregateSBOM.MediaType != "application/spdx+json" || !digestPattern.MatchString(release.AggregateSBOM.SHA256) {
		return nil, errors.New("release must contain the canonical eight-image manifest")
	}
	if release.ApprovalReceiptDigest != nil && !digestPattern.MatchString(*release.ApprovalReceiptDigest) {
		return nil, errors.New("release approval receipt digest is invalid")
	}
	indexed := make(map[string]ReleaseImage, len(release.Images))
	for _, image := range release.Images {
		expected, ok := expectedImages[image.Variant]
		if !ok || indexed[image.Variant].Variant != "" {
			return nil, fmt.Errorf("release image variant %q is unknown or duplicated", image.Variant)
		}
		if image.ImageKind != expected.Kind || !equalStrings(image.Platforms, expected.Platforms) ||
			strings.TrimSpace(image.Image) == "" ||
			image.ReleaseID != release.ReleaseID || image.LockDigest != release.LockDigest ||
			image.DefinitionDigest != release.DefinitionDigest ||
			!reflect.DeepEqual(image.ApprovalReceiptDigest, release.ApprovalReceiptDigest) ||
			!digestPattern.MatchString(image.Digest) ||
			strings.TrimSpace(image.Primary.Repository) == "" ||
			!image.Primary.Verified || !image.SignatureVerified || !image.ProvenanceVerified || !image.SBOMVerified ||
			!digestPattern.MatchString(image.SBOMSHA256) ||
			!digestPattern.MatchString(image.Evidence.SignatureVerificationSHA256) ||
			!digestPattern.MatchString(image.Evidence.ProvenanceVerificationSHA256) ||
			!digestPattern.MatchString(image.Evidence.SBOMVerificationSHA256) ||
			!digestPattern.MatchString(image.Evidence.ReferrersSHA256) {
			return nil, fmt.Errorf("release image %q has invalid identity, platform, or verification evidence", image.Variant)
		}
		indexed[image.Variant] = image
	}
	for variant := range expectedImages {
		if _, ok := indexed[variant]; !ok {
			return nil, fmt.Errorf("release image %q is missing", variant)
		}
	}
	return indexed, nil
}

func validateImageEvidence(e Evidence, releaseImages map[string]ReleaseImage) error {
	if len(e.Images) != len(expectedImages) {
		return errors.New("factory evidence must contain provenance, annotations, and SBOM for every image")
	}
	seen := make(map[string]bool, len(e.Images))
	for _, image := range e.Images {
		expected, ok := expectedImages[image.Variant]
		if !ok || seen[image.Variant] {
			return fmt.Errorf("image evidence variant %q is unknown or duplicated", image.Variant)
		}
		seen[image.Variant] = true
		requiredAnnotations := map[string]string{
			"org.opencontainers.image.revision":  e.Release.DefinitionCommit,
			"dev.wolf.release.variant":           image.Variant,
			"dev.wolf.release.image-kind":        expected.Kind,
			"dev.wolf.release.platforms":         strings.Join(expected.Platforms, ","),
			"dev.wolf.release.lock-digest":       e.Release.LockDigest,
			"dev.wolf.release.definition-digest": e.Release.DefinitionDigest,
		}
		for key, value := range requiredAnnotations {
			if image.Annotations[key] != value {
				return fmt.Errorf("image %q annotation %q does not match release identity", image.Variant, key)
			}
		}
		if strings.TrimSpace(image.Annotations["org.opencontainers.image.source"]) == "" {
			return fmt.Errorf("image %q has no source annotation", image.Variant)
		}
		if strings.TrimSpace(image.Provenance.BuildType) == "" || strings.TrimSpace(image.Provenance.BuilderID) == "" ||
			(image.Provenance.StartedAt != "" && !validTimestamp(image.Provenance.StartedAt)) ||
			(image.Provenance.FinishedAt != "" && !validTimestamp(image.Provenance.FinishedAt)) ||
			len(image.Provenance.Materials) == 0 ||
			len(image.Provenance.Subjects) == 0 {
			return fmt.Errorf("image %q has incomplete normalized provenance", image.Variant)
		}
		materialBindsDefinition := false
		for _, material := range image.Provenance.Materials {
			if strings.TrimSpace(material.URI) == "" || len(material.Digest) == 0 {
				return fmt.Errorf("image %q has an invalid provenance material", image.Variant)
			}
			if strings.Contains(material.URI, e.Release.DefinitionCommit) ||
				material.Digest["sha1"] == e.Release.DefinitionCommit ||
				material.Digest["gitCommit"] == e.Release.DefinitionCommit {
				materialBindsDefinition = true
			}
		}
		if !materialBindsDefinition {
			return fmt.Errorf("image %q provenance is not bound to the definition commit", image.Variant)
		}
		for _, subject := range image.Provenance.Subjects {
			if strings.TrimSpace(subject.Name) == "" || len(subject.Digest) == 0 {
				return fmt.Errorf("image %q has an invalid provenance subject", image.Variant)
			}
		}
		if image.SBOM.SPDXVersion != "SPDX-2.3" || image.SBOM.DataLicense != "CC0-1.0" ||
			strings.TrimSpace(image.SBOM.Name) == "" || strings.TrimSpace(image.SBOM.DocumentNamespace) == "" ||
			!validTimestamp(image.SBOM.CreationInfo.Created) || len(image.SBOM.CreationInfo.Creators) == 0 ||
			len(image.SBOM.Packages) == 0 {
			return fmt.Errorf("image %q has an invalid or empty SPDX 2.3 inventory", image.Variant)
		}
		spdxIDs := make(map[string]bool, len(image.SBOM.Packages))
		for _, pkg := range image.SBOM.Packages {
			if strings.TrimSpace(pkg.SPDXID) == "" || strings.TrimSpace(pkg.Name) == "" || spdxIDs[pkg.SPDXID] {
				return fmt.Errorf("image %q has an invalid or duplicate SPDX package", image.Variant)
			}
			spdxIDs[pkg.SPDXID] = true
		}
		if releaseImages[image.Variant].Variant == "" {
			return fmt.Errorf("image evidence %q has no release image", image.Variant)
		}
	}
	return nil
}

func validTimestamp(value string) bool {
	_, err := time.Parse(time.RFC3339Nano, value)
	return err == nil
}

func validateQuality(e Evidence) error {
	if len(e.Quality.Runs) == 0 {
		return errors.New("factory evidence has no measured quality runs")
	}
	stable, candidate := make(map[string]bool), make(map[string]bool)
	for _, run := range e.Quality.Runs {
		if run.SchemaVersion != scannerquality.EvidenceSchema || !digestPattern.MatchString(run.GoldenDigest) ||
			strings.TrimSpace(run.VulnerabilityDatabase.Provider) == "" ||
			strings.TrimSpace(run.VulnerabilityDatabase.Repository) == "" ||
			!digestPattern.MatchString(run.VulnerabilityDatabase.Digest) ||
			run.VulnerabilityDatabase.RecordedAt.IsZero() {
			return errors.New("quality run has invalid schema, expectation, or database identity")
		}
		if err := validateQualityNetwork(run.Network); err != nil {
			return err
		}
		runStable, runCandidate := make(map[string]bool), make(map[string]bool)
		for _, tool := range run.Stable {
			if stable[tool.Tool] || validateQualityTool(e.ScannerLock.Tools, tool) != nil {
				return fmt.Errorf("stable quality evidence for tool %q is duplicated or invalid", tool.Tool)
			}
			stable[tool.Tool] = true
			runStable[tool.Tool] = true
		}
		for _, tool := range run.Candidate {
			if candidate[tool.Tool] || validateQualityTool(e.ScannerLock.Tools, tool) != nil {
				return fmt.Errorf("candidate quality evidence for tool %q is duplicated or invalid", tool.Tool)
			}
			candidate[tool.Tool] = true
			runCandidate[tool.Tool] = true
		}
		expectedScope := make(map[string]bool, len(e.ScannerLock.Tools))
		if len(run.Scope) == 0 {
			for tool := range e.ScannerLock.Tools {
				expectedScope[tool] = true
			}
		} else {
			for _, tool := range run.Scope {
				if _, known := e.ScannerLock.Tools[tool]; !known || expectedScope[tool] {
					return fmt.Errorf("quality run has an unknown or duplicate scoped tool %q", tool)
				}
				expectedScope[tool] = true
			}
		}
		if !reflect.DeepEqual(runStable, expectedScope) || !reflect.DeepEqual(runCandidate, expectedScope) {
			return errors.New("quality run stable/candidate coverage does not match its declared scope")
		}
	}
	if len(stable) != len(e.ScannerLock.Tools) || len(candidate) != len(e.ScannerLock.Tools) {
		return fmt.Errorf("quality evidence covers %d stable/%d candidate tools; scanner lock contains %d", len(stable), len(candidate), len(e.ScannerLock.Tools))
	}
	for tool := range e.ScannerLock.Tools {
		if !stable[tool] || !candidate[tool] {
			return fmt.Errorf("quality evidence is missing scanner tool %q", tool)
		}
	}
	return nil
}

func validateQualityNetwork(network scannerquality.NetworkEvidence) error {
	switch network.Mode {
	case "none":
		if network.Name != "" || network.ID != "" || network.PolicyDigest != "" {
			return errors.New("network-disabled quality evidence contains a controlled-network identity")
		}
	case "controlled-internal":
		if !strings.HasPrefix(network.Name, "wolf-quality-") ||
			!boundedHexIdentity(network.ID) || !digestPattern.MatchString(network.PolicyDigest) {
			return errors.New("controlled quality network evidence is incomplete or invalid")
		}
	default:
		return fmt.Errorf("quality evidence network mode %q is unsupported", network.Mode)
	}
	return nil
}

func validateQualityTool(inventory map[string]scannerlock.Tool, tool scannerquality.ToolEvidence) error {
	if _, known := inventory[tool.Tool]; !known || tool.ParseErrors != 0 ||
		tool.DurationMS < 0 || tool.OutputBytes < 0 || tool.RawOutputBytes < 0 ||
		tool.PeakMemoryBytes < 0 || !digestPattern.MatchString(tool.ImageDigest) ||
		!strings.Contains(tool.ImageReference, "@"+tool.ImageDigest) ||
		strings.ContainsAny(tool.ImageReference, " \t\r\n") ||
		!digestPattern.MatchString(tool.OutputDigest) ||
		((tool.RawOutputDigest == "") != (tool.RawOutputBytes == 0)) ||
		(tool.RawOutputDigest != "" && !digestPattern.MatchString(tool.RawOutputDigest)) {
		return errors.New("incomplete quality tool identity or measurements")
	}
	switch tool.ExecutionMode {
	case "executed":
		if tool.OutputKind != "normalized-findings" || tool.DurationMS <= 0 ||
			tool.OutputBytes <= 0 || tool.PeakMemoryBytes <= 0 {
			return errors.New("incomplete executed quality evidence")
		}
	case "structural":
		if tool.OutputKind != "structural-manifest" || tool.DurationMS != 0 ||
			tool.PeakMemoryBytes != 0 || len(tool.Findings) != 0 || tool.RawOutputDigest != "" {
			return errors.New("structural quality evidence claims execution")
		}
	default:
		return errors.New("invalid quality execution mode")
	}
	for _, finding := range tool.Findings {
		if (finding.Tool != "" && finding.Tool != tool.Tool) || finding.RuleID == "" ||
			finding.Fingerprint == "" || finding.Line < 0 {
			return errors.New("malformed normalized finding")
		}
	}
	return nil
}

func boundedHexIdentity(value string) bool {
	if len(value) < 12 || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

// Compare returns a deterministic report. A non-equivalent report is not a
// parsing error; callers can persist it before failing a release gate.
func Compare(managed, customer Evidence) (Report, error) {
	if err := Validate(managed); err != nil {
		return Report{}, fmt.Errorf("managed evidence: %w", err)
	}
	if err := Validate(customer); err != nil {
		return Report{}, fmt.Errorf("customer evidence: %w", err)
	}
	if managed.Factory.Kind != "managed" || customer.Factory.Kind != "customer" {
		return Report{}, errors.New("comparison requires managed evidence followed by customer evidence")
	}
	report := Report{
		SchemaVersion: ReportSchema, Equivalent: true,
		ManagedFactory: managed.Factory, CustomerFactory: customer.Factory,
	}
	add := func(property string, left, right any) {
		leftDigest, leftCanonical := canonicalDigest(left)
		rightDigest, rightCanonical := canonicalDigest(right)
		equal := bytes.Equal(leftCanonical, rightCanonical)
		report.Comparisons = append(report.Comparisons, Comparison{
			Property: property, Equivalent: equal, ManagedDigest: leftDigest, CustomerDigest: rightDigest,
		})
		if !equal {
			report.Equivalent = false
			report.Mismatches = append(report.Mismatches, property)
		}
	}
	add("definition_identity", definitionProjection(managed), definitionProjection(customer))
	add("image_platform_matrix", imageMatrix(managed.Release), imageMatrix(customer.Release))
	add("tool_inventory", managed.ScannerLock.Tools, customer.ScannerLock.Tools)
	add("build_policy_inventory", managed.ScannerLock.ReleaseInputs, customer.ScannerLock.ReleaseInputs)
	add("declared_policy", managed.Policy, customer.Policy)
	add("quality_corpus", qualityIdentity(managed), qualityIdentity(customer))
	add("quality_results", qualityProjection(managed.Quality.Runs), qualityProjection(customer.Quality.Runs))
	managedImages, customerImages := imageEvidenceIndex(managed.Images), imageEvidenceIndex(customer.Images)
	for _, variant := range sortedImageVariants() {
		add("annotations/"+variant, deterministicAnnotations(managedImages[variant].Annotations), deterministicAnnotations(customerImages[variant].Annotations))
		add("provenance_materials/"+variant, provenanceProjection(managedImages[variant].Provenance), provenanceProjection(customerImages[variant].Provenance))
		add("sbom_package_inventory/"+variant, packageProjection(managedImages[variant].SBOM.Packages), packageProjection(customerImages[variant].SBOM.Packages))
	}
	report.NondeterministicFields = nondeterministicFields(managed, customer)
	sort.Slice(report.Comparisons, func(i, j int) bool { return report.Comparisons[i].Property < report.Comparisons[j].Property })
	sort.Strings(report.Mismatches)
	sort.Slice(report.NondeterministicFields, func(i, j int) bool {
		return report.NondeterministicFields[i].Path < report.NondeterministicFields[j].Path
	})
	return report, nil
}

func definitionProjection(e Evidence) any {
	return struct {
		Commit, Definition, Lock string
	}{e.Release.DefinitionCommit, e.Release.DefinitionDigest, e.Release.LockDigest}
}

func imageMatrix(release ReleaseManifest) map[string]imageExpectation {
	result := make(map[string]imageExpectation, len(release.Images))
	for _, image := range release.Images {
		platforms := sortedStrings(image.Platforms)
		result[image.Variant] = imageExpectation{Kind: image.ImageKind, Platforms: platforms}
	}
	return result
}

func qualityIdentity(e Evidence) any {
	goldens, databases, networks := []string{}, []string{}, []string{}
	for _, run := range e.Quality.Runs {
		goldens = append(goldens, run.GoldenDigest)
		databases = append(databases, run.VulnerabilityDatabase.Provider+"|"+run.VulnerabilityDatabase.Repository+"|"+run.VulnerabilityDatabase.Digest)
		networks = append(networks, run.Network.Mode+"|"+run.Network.PolicyDigest)
	}
	return struct {
		Corpus, Policy               string
		Goldens, Databases, Networks []string
	}{e.Quality.CorpusDigest, e.Quality.PolicyDigest, sortedUniqueStrings(goldens), sortedUniqueStrings(databases), sortedUniqueStrings(networks)}
}

type deterministicToolEvidence struct {
	Tool, ExecutionMode, OutputKind, OutputDigest      string
	GoldenDigest, DatabaseProvider, DatabaseRepository string
	DatabaseDigest, NetworkMode, NetworkPolicyDigest   string
	ParseErrors                                        int
	Findings                                           []scannerquality.Finding
}

func qualityProjection(runs []scannerquality.Evidence) map[string]deterministicToolEvidence {
	result := make(map[string]deterministicToolEvidence)
	for _, run := range runs {
		for _, side := range []struct {
			name  string
			tools []scannerquality.ToolEvidence
		}{{"stable", run.Stable}, {"candidate", run.Candidate}} {
			for _, tool := range side.tools {
				findings := append([]scannerquality.Finding(nil), tool.Findings...)
				for index := range findings {
					if findings[index].Tool == "" {
						findings[index].Tool = tool.Tool
					}
				}
				sort.Slice(findings, func(i, j int) bool {
					left, _ := json.Marshal(findings[i])
					right, _ := json.Marshal(findings[j])
					return bytes.Compare(left, right) < 0
				})
				result[side.name+"/"+tool.Tool] = deterministicToolEvidence{
					Tool: tool.Tool, ExecutionMode: tool.ExecutionMode, OutputKind: tool.OutputKind,
					OutputDigest: tool.OutputDigest, GoldenDigest: run.GoldenDigest,
					DatabaseProvider:   run.VulnerabilityDatabase.Provider,
					DatabaseRepository: run.VulnerabilityDatabase.Repository,
					DatabaseDigest:     run.VulnerabilityDatabase.Digest,
					NetworkMode:        run.Network.Mode, NetworkPolicyDigest: run.Network.PolicyDigest,
					ParseErrors: tool.ParseErrors, Findings: findings,
				}
			}
		}
	}
	return result
}

func deterministicAnnotations(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for key, value := range values {
		if containsString(nondeterministicAnnotationKeys, key) {
			continue
		}
		result[key] = value
	}
	return result
}

func provenanceProjection(value Provenance) any {
	materials := append([]Material(nil), value.Materials...)
	sort.Slice(materials, func(i, j int) bool {
		left, _ := json.Marshal(materials[i])
		right, _ := json.Marshal(materials[j])
		return bytes.Compare(left, right) < 0
	})
	return struct {
		BuildType string
		Materials []Material
	}{value.BuildType, materials}
}

type packageIdentity struct {
	Name, VersionInfo, Supplier, DownloadLocation, LicenseConcluded, LicenseDeclared string
	FilesAnalyzed                                                                    *bool
	Checksums                                                                        []SPDXChecksum
	ExternalRefs                                                                     []SPDXExternalRef
}

func packageProjection(packages []SPDXPackage) []packageIdentity {
	result := make([]packageIdentity, 0, len(packages))
	for _, pkg := range packages {
		checksums := append([]SPDXChecksum(nil), pkg.Checksums...)
		sort.Slice(checksums, func(i, j int) bool {
			return checksums[i].Algorithm+checksums[i].ChecksumValue < checksums[j].Algorithm+checksums[j].ChecksumValue
		})
		refs := append([]SPDXExternalRef(nil), pkg.ExternalRefs...)
		sort.Slice(refs, func(i, j int) bool {
			return refs[i].ReferenceCategory+refs[i].ReferenceType+refs[i].ReferenceLocator < refs[j].ReferenceCategory+refs[j].ReferenceType+refs[j].ReferenceLocator
		})
		result = append(result, packageIdentity{
			Name: pkg.Name, VersionInfo: pkg.VersionInfo, Supplier: pkg.Supplier,
			DownloadLocation: pkg.DownloadLocation, FilesAnalyzed: pkg.FilesAnalyzed,
			LicenseConcluded: pkg.LicenseConcluded, LicenseDeclared: pkg.LicenseDeclared,
			Checksums: checksums, ExternalRefs: refs,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		left, _ := json.Marshal(result[i])
		right, _ := json.Marshal(result[j])
		return bytes.Compare(left, right) < 0
	})
	return result
}

func nondeterministicFields(managed, customer Evidence) []NondeterministicField {
	var result []NondeterministicField
	add := func(path, reason string, left, right any) {
		result = append(result, NondeterministicField{Path: path, Reason: reason, Managed: left, Customer: right, Equal: reflect.DeepEqual(left, right)})
	}
	add("factory.id", "factory-local identity", managed.Factory.ID, customer.Factory.ID)
	add("release.releaseId", "factory-local immutable release identity", managed.Release.ReleaseID, customer.Release.ReleaseID)
	add("release.generatedAt", "factory execution timestamp", managed.Release.GeneratedAt, customer.Release.GeneratedAt)
	add("release.operation", "factory workflow lane", managed.Release.Operation, customer.Release.Operation)
	add("release.approvalReceiptDigest", "factory-local authorization receipt", managed.Release.ApprovalReceiptDigest, customer.Release.ApprovalReceiptDigest)
	add("release.aggregateSbom.sha256", "SPDX document metadata changes the document digest; package inventory is compared separately", managed.Release.AggregateSBOM.SHA256, customer.Release.AggregateSBOM.SHA256)
	managedRelease, customerRelease := releaseImageIndex(managed.Release.Images), releaseImageIndex(customer.Release.Images)
	managedImages, customerImages := imageEvidenceIndex(managed.Images), imageEvidenceIndex(customer.Images)
	for _, variant := range sortedImageVariants() {
		prefix := "images[" + variant + "]"
		left, right := managedRelease[variant], customerRelease[variant]
		add(prefix+".image", "factory-local image name or reference", left.Image, right.Image)
		add(prefix+".releaseId", "factory-local immutable release identity", left.ReleaseID, right.ReleaseID)
		add(prefix+".approvalReceiptDigest", "factory-local authorization receipt", left.ApprovalReceiptDigest, right.ApprovalReceiptDigest)
		add(prefix+".digest", "factory build output digest; deterministic inputs are compared independently", left.Digest, right.Digest)
		add(prefix+".primary.repository", "factory-owned registry namespace", left.Primary.Repository, right.Primary.Repository)
		add(prefix+".primary.referrersSha256", "factory-local registry referrer-set receipt", left.Primary.ReferrersSHA256, right.Primary.ReferrersSHA256)
		add(prefix+".primary.signatureVerified", "factory-local registry verification result", left.Primary.SignatureVerified, right.Primary.SignatureVerified)
		add(prefix+".primary.provenanceVerified", "factory-local registry verification result", left.Primary.ProvenanceVerified, right.Primary.ProvenanceVerified)
		add(prefix+".primary.sbomVerified", "factory-local registry verification result", left.Primary.SBOMVerified, right.Primary.SBOMVerified)
		add(prefix+".mirror", "factory-owned mirror namespace and referrer receipts", left.Mirror, right.Mirror)
		add(prefix+".baseReference", "factory-owned fixer base repository and digest", left.BaseReference, right.BaseReference)
		add(prefix+".sbom_sha256", "SPDX document metadata changes the document digest", left.SBOMSHA256, right.SBOMSHA256)
		add(prefix+".evidence", "factory-local signature, attestation, and referrer receipts", left.Evidence, right.Evidence)
		lp, rp := managedImages[variant].Provenance, customerImages[variant].Provenance
		add(prefix+".provenance.builderId", "factory builder identity", lp.BuilderID, rp.BuilderID)
		add(prefix+".provenance.invocationId", "factory invocation identity", lp.InvocationID, rp.InvocationID)
		add(prefix+".provenance.startedAt", "factory execution timestamp", lp.StartedAt, rp.StartedAt)
		add(prefix+".provenance.finishedAt", "factory execution timestamp", lp.FinishedAt, rp.FinishedAt)
		add(prefix+".provenance.subjects", "factory build output subjects", lp.Subjects, rp.Subjects)
		ls, rs := managedImages[variant].SBOM, customerImages[variant].SBOM
		add(prefix+".sbom.name", "factory/SPDX document identity", ls.Name, rs.Name)
		add(prefix+".sbom.documentNamespace", "factory-generated SPDX namespace", ls.DocumentNamespace, rs.DocumentNamespace)
		add(prefix+".sbom.creationInfo", "SPDX generator and creation timestamp", ls.CreationInfo, rs.CreationInfo)
		add(prefix+".sbom.packageSpdxIds", "SPDX document-local package identifiers", packageSPDXIDs(ls.Packages), packageSPDXIDs(rs.Packages))
		for _, key := range nondeterministicAnnotationKeys {
			add(prefix+".annotations["+key+"]", "factory-local OCI annotation", managedImages[variant].Annotations[key], customerImages[variant].Annotations[key])
		}
	}
	leftQuality, rightQuality := qualityToolEvidenceIndex(managed.Quality.Runs), qualityToolEvidenceIndex(customer.Quality.Runs)
	qualityKeys := make([]string, 0, len(leftQuality))
	for key := range leftQuality {
		qualityKeys = append(qualityKeys, key)
	}
	sort.Strings(qualityKeys)
	for _, key := range qualityKeys {
		left, right := leftQuality[key], rightQuality[key]
		prefix := "quality[" + key + "]"
		add(prefix+".imageReference", "factory-local exact image reference", left.Tool.ImageReference, right.Tool.ImageReference)
		add(prefix+".imageDigest", "factory-local image digest", left.Tool.ImageDigest, right.Tool.ImageDigest)
		add(prefix+".rawOutputDigest", "factory-local native output identity", left.Tool.RawOutputDigest, right.Tool.RawOutputDigest)
		add(prefix+".rawOutputBytes", "runtime measurement", left.Tool.RawOutputBytes, right.Tool.RawOutputBytes)
		add(prefix+".durationMs", "runtime measurement", left.Tool.DurationMS, right.Tool.DurationMS)
		add(prefix+".outputBytes", "runtime measurement", left.Tool.OutputBytes, right.Tool.OutputBytes)
		add(prefix+".peakMemoryBytes", "runtime measurement", left.Tool.PeakMemoryBytes, right.Tool.PeakMemoryBytes)
		add(prefix+".database.recordedAt", "factory evidence collection timestamp", left.DatabaseRecordedAt, right.DatabaseRecordedAt)
		add(prefix+".network.name", "factory-local controlled network name", left.NetworkName, right.NetworkName)
		add(prefix+".network.id", "factory-local controlled network identity", left.NetworkID, right.NetworkID)
	}
	return result
}

var nondeterministicAnnotationKeys = []string{
	"org.opencontainers.image.created",
	"org.opencontainers.image.version",
	"dev.wolf.release.id",
	"dev.wolf.release.approval-receipt",
	"dev.wolf.release.factory",
	"dev.wolf.fixer.base",
}

func packageSPDXIDs(packages []SPDXPackage) []string {
	values := make([]string, 0, len(packages))
	for _, pkg := range packages {
		values = append(values, pkg.SPDXID)
	}
	return sortedStrings(values)
}

type qualityToolContext struct {
	Tool               scannerquality.ToolEvidence
	DatabaseRecordedAt time.Time
	NetworkName        string
	NetworkID          string
}

func qualityToolEvidenceIndex(runs []scannerquality.Evidence) map[string]qualityToolContext {
	result := make(map[string]qualityToolContext)
	for _, run := range runs {
		for _, tool := range run.Stable {
			result["stable/"+tool.Tool] = qualityToolContext{
				Tool: tool, DatabaseRecordedAt: run.VulnerabilityDatabase.RecordedAt,
				NetworkName: run.Network.Name, NetworkID: run.Network.ID,
			}
		}
		for _, tool := range run.Candidate {
			result["candidate/"+tool.Tool] = qualityToolContext{
				Tool: tool, DatabaseRecordedAt: run.VulnerabilityDatabase.RecordedAt,
				NetworkName: run.Network.Name, NetworkID: run.Network.ID,
			}
		}
	}
	return result
}

func canonicalDigest(value any) (string, []byte) {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), encoded
}

func imageEvidenceIndex(images []ImageEvidence) map[string]ImageEvidence {
	result := make(map[string]ImageEvidence, len(images))
	for _, image := range images {
		result[image.Variant] = image
	}
	return result
}

func releaseImageIndex(images []ReleaseImage) map[string]ReleaseImage {
	result := make(map[string]ReleaseImage, len(images))
	for _, image := range images {
		result[image.Variant] = image
	}
	return result
}

func sortedImageVariants() []string {
	result := make([]string, 0, len(expectedImages))
	for variant := range expectedImages {
		result = append(result, variant)
	}
	sort.Strings(result)
	return result
}

func sortedStrings(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}

func sortedUniqueStrings(values []string) []string {
	values = sortedStrings(values)
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return result
}

func equalStrings(left, right []string) bool {
	return reflect.DeepEqual(sortedStrings(left), sortedStrings(right))
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
