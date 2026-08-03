package scannerreproducibility

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannerquality"
	scannerlock "github.com/alphabravocompany/thewolf/internal/scannertools/lock"
)

func TestCompareAcceptsIndependentFactoryNondeterminism(t *testing.T) {
	managed := testEvidence(t, "managed", "managed-factory", "a")
	customer := testEvidence(t, "customer", "customer-factory", "b")

	report, err := Compare(managed, customer)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Equivalent || len(report.Mismatches) != 0 {
		t.Fatalf("report = %#v", report)
	}
	if len(report.NondeterministicFields) == 0 {
		t.Fatal("expected explicit nondeterministic field report")
	}
	byPath := make(map[string]NondeterministicField)
	for _, field := range report.NondeterministicFields {
		byPath[field.Path] = field
	}
	for _, path := range []string{
		"factory.id", "release.releaseId", "release.generatedAt", "images[default].image",
		"images[default].digest",
		"images[default].provenance.builderId", "images[default].sbom.documentNamespace",
		"images[default].annotations[dev.wolf.release.id]", "quality[stable/bandit].imageDigest",
		"quality[stable/bandit].database.recordedAt",
	} {
		if field, ok := byPath[path]; !ok || field.Equal {
			t.Fatalf("nondeterministic field %q = %#v", path, field)
		}
	}
}

func TestCompareRejectsDeterministicDifferences(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(*Evidence)
		expected string
	}{
		{
			name: "definition", expected: "definition_identity",
			mutate: func(e *Evidence) {
				e.Release.DefinitionCommit = strings.Repeat("c", 40)
				for index := range e.Images {
					e.Images[index].Annotations["org.opencontainers.image.revision"] = e.Release.DefinitionCommit
					e.Images[index].Provenance.Materials[0].URI = "git+https://example.invalid/wolf@" + e.Release.DefinitionCommit
					e.Images[index].Provenance.Materials[0].Digest["sha1"] = e.Release.DefinitionCommit
				}
			},
		},
		{
			name: "policy", expected: "declared_policy",
			mutate: func(e *Evidence) {
				e.Policy.DeclaredEvidenceDigest = testDigest("d")
			},
		},
		{
			name: "provenance material", expected: "provenance_materials/default",
			mutate: func(e *Evidence) {
				e.Images[imageIndex(e.Images, "default")].Provenance.Materials[0].URI = "git+https://example.invalid/other"
			},
		},
		{
			name: "SBOM package", expected: "sbom_package_inventory/default",
			mutate: func(e *Evidence) {
				e.Images[imageIndex(e.Images, "default")].SBOM.Packages[0].VersionInfo = "9.9.9"
			},
		},
		{
			name: "quality output", expected: "quality_results",
			mutate: func(e *Evidence) {
				e.Quality.Runs[0].Candidate[0].OutputDigest = testDigest("e")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			managed := testEvidence(t, "managed", "managed-factory", "a")
			customer := testEvidence(t, "customer", "customer-factory", "b")
			tt.mutate(&customer)
			report, err := Compare(managed, customer)
			if err != nil {
				t.Fatal(err)
			}
			if report.Equivalent || !contains(report.Mismatches, tt.expected) {
				t.Fatalf("mismatches = %#v, want %q", report.Mismatches, tt.expected)
			}
		})
	}
}

func TestCompareBindsQualityContextToEachTool(t *testing.T) {
	managed := testEvidence(t, "managed", "managed-factory", "a")
	customer := testEvidence(t, "customer", "customer-factory", "b")
	splitQualityContexts(&managed, testDigest("d"), testDigest("e"))
	splitQualityContexts(&customer, testDigest("e"), testDigest("d"))

	report, err := Compare(managed, customer)
	if err != nil {
		t.Fatal(err)
	}
	if report.Equivalent || !contains(report.Mismatches, "quality_results") {
		t.Fatalf("mismatches = %#v, want per-tool quality context mismatch", report.Mismatches)
	}
	if contains(report.Mismatches, "quality_corpus") {
		t.Fatalf("quality identity set should match despite different per-tool binding: %#v", report.Mismatches)
	}
}

func TestCompareNormalizesEvidenceOrdering(t *testing.T) {
	managed := testEvidence(t, "managed", "managed-factory", "a")
	customer := testEvidence(t, "customer", "customer-factory", "b")
	reverse(customer.Release.Images)
	reverse(customer.Images)
	reverse(customer.Quality.Runs[0].Stable)
	reverse(customer.Quality.Runs[0].Candidate)

	report, err := Compare(managed, customer)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Equivalent {
		t.Fatalf("reordered canonical evidence mismatches = %#v", report.Mismatches)
	}
}

func TestValidateRequiresExactImageAndQualityCoverage(t *testing.T) {
	evidence := testEvidence(t, "managed", "managed-factory", "a")
	evidence.Release.Images = evidence.Release.Images[:7]
	if err := Validate(evidence); err == nil || !strings.Contains(err.Error(), "nine-image") {
		t.Fatalf("missing image error = %v", err)
	}

	evidence = testEvidence(t, "managed", "managed-factory", "a")
	evidence.Release.Images[imageReleaseIndex(evidence.Release.Images, "codeql")].Platforms = []string{"linux/arm64"}
	if err := Validate(evidence); err == nil || !strings.Contains(err.Error(), "platform") {
		t.Fatalf("incorrect platform error = %v", err)
	}

	evidence = testEvidence(t, "managed", "managed-factory", "a")
	evidence.Quality.Runs[0].Candidate = evidence.Quality.Runs[0].Candidate[:1]
	if err := Validate(evidence); err == nil || !strings.Contains(err.Error(), "coverage") {
		t.Fatalf("missing quality error = %v", err)
	}
}

func TestLoadFileRejectsUnknownFields(t *testing.T) {
	evidence := testEvidence(t, "managed", "managed-factory", "a")
	encoded, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	encoded = append(encoded[:len(encoded)-1], []byte(`,"unexpected":true}`)...)
	path := filepath.Join(t.TempDir(), "evidence.json")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(path); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field error = %v", err)
	}
}

func testEvidence(t *testing.T, kind, id, nondeterministic string) Evidence {
	t.Helper()
	loadedLock, err := scannerlock.LoadFile(filepath.Join("..", "..", scannerlock.DefaultLockPath))
	if err != nil {
		t.Fatal(err)
	}
	lockDigest := loadedLock.LockDigest
	definitionDigest := loadedLock.Definition.Digest
	policyDigest := testDigest("3")
	releaseID := kind + "-release-" + nondeterministic
	commit := strings.Repeat("4", 40)
	factoryHour := map[string]int{"managed": 1, "customer": 2}[kind]
	factoryTimestamp := time.Date(2026, 7, 31, factoryHour, 0, 0, 0, time.UTC)
	releaseImages := make([]ReleaseImage, 0, len(expectedImages))
	imageEvidence := make([]ImageEvidence, 0, len(expectedImages))
	for _, variant := range sortedImageVariants() {
		expected := expectedImages[variant]
		imageDigest := testDigest(nondeterministic)
		releaseImages = append(releaseImages, ReleaseImage{
			Variant: variant, ImageKind: expected.Kind, Image: kind + "-wolf-" + variant,
			ReleaseID: releaseID, LockDigest: lockDigest, DefinitionDigest: definitionDigest,
			Digest: imageDigest, Platforms: append([]string(nil), expected.Platforms...),
			BaseReference: "registry." + kind + "/base@" + imageDigest,
			Primary:       RegistryRecord{Repository: "registry." + kind + "/" + variant, Verified: true},
			SBOMSHA256:    testDigest(nondeterministic), SignatureVerified: true,
			ProvenanceVerified: true, SBOMVerified: true,
			Evidence: ImageReceipt{
				SignatureVerificationSHA256:  testDigest(nondeterministic),
				ProvenanceVerificationSHA256: testDigest(nondeterministic),
				SBOMVerificationSHA256:       testDigest(nondeterministic),
				ReferrersSHA256:              testDigest(nondeterministic),
			},
		})
		filesAnalyzed := false
		imageEvidence = append(imageEvidence, ImageEvidence{
			Variant: variant,
			Annotations: map[string]string{
				"org.opencontainers.image.source":    "https://example.invalid/wolf",
				"org.opencontainers.image.revision":  commit,
				"org.opencontainers.image.version":   releaseID,
				"dev.wolf.release.id":                releaseID,
				"dev.wolf.release.variant":           variant,
				"dev.wolf.release.image-kind":        expected.Kind,
				"dev.wolf.release.platforms":         strings.Join(expected.Platforms, ","),
				"dev.wolf.release.lock-digest":       lockDigest,
				"dev.wolf.release.definition-digest": definitionDigest,
			},
			Provenance: Provenance{
				BuildType: "https://mobyproject.org/buildkit@v1", BuilderID: kind + "-builder",
				InvocationID: kind + "-run-" + nondeterministic,
				StartedAt:    factoryTimestamp.Format(time.RFC3339),
				FinishedAt:   factoryTimestamp.Add(time.Minute).Format(time.RFC3339),
				Materials:    []Material{{URI: "git+https://example.invalid/wolf@" + commit, Digest: map[string]string{"sha1": commit}}},
				Subjects:     []Subject{{Name: "registry." + kind + "/" + variant, Digest: map[string]string{"sha256": strings.Repeat(nondeterministic, 64)}}},
			},
			SBOM: SPDXDocument{
				SPDXVersion: "SPDX-2.3", DataLicense: "CC0-1.0",
				Name: kind + "-" + variant, DocumentNamespace: "https://spdx.invalid/" + kind + "/" + variant,
				CreationInfo: CreationInfo{Created: "2026-07-31T00:00:00Z", Creators: []string{"Tool: " + kind}},
				Packages: []SPDXPackage{{
					SPDXID: "SPDXRef-" + kind, Name: "shared-package", VersionInfo: "1.2.3",
					Supplier: "Organization: Wolf", DownloadLocation: "NOASSERTION", FilesAnalyzed: &filesAnalyzed,
					LicenseConcluded: "MIT", LicenseDeclared: "MIT",
					Checksums: []SPDXChecksum{{Algorithm: "SHA256", ChecksumValue: strings.Repeat("5", 64)}},
				}},
			},
		})
	}
	toolEvidence := func(tool string) scannerquality.ToolEvidence {
		return scannerquality.ToolEvidence{
			Tool: tool, ExecutionMode: "executed", ImageReference: "registry." + kind + "/scanner@" + testDigest(nondeterministic),
			ImageDigest: testDigest(nondeterministic), OutputKind: "normalized-findings", OutputDigest: testDigest("6"),
			RawOutputDigest: testDigest(nondeterministic), RawOutputBytes: 20,
			DurationMS: 100, OutputBytes: 10, PeakMemoryBytes: 1000,
			Findings: []scannerquality.Finding{{Tool: tool, RuleID: "fixture", Severity: "medium", Path: "src/main", Line: 1, Message: "fixture", Fingerprint: "stable"}},
		}
	}
	toolNames := make([]string, 0, len(loadedLock.Tools))
	for tool := range loadedLock.Tools {
		toolNames = append(toolNames, tool)
	}
	sort.Strings(toolNames)
	stable := make([]scannerquality.ToolEvidence, 0, len(toolNames))
	candidate := make([]scannerquality.ToolEvidence, 0, len(toolNames))
	for _, tool := range toolNames {
		stable = append(stable, toolEvidence(tool))
		candidate = append(candidate, toolEvidence(tool))
	}
	return Evidence{
		SchemaVersion: EvidenceSchema, Factory: FactoryIdentity{ID: id, Kind: kind},
		Release: ReleaseManifest{
			SchemaVersion: "wolf.scanners.release/v1", ReleaseID: releaseID,
			DefinitionCommit: commit, DefinitionDigest: definitionDigest, LockDigest: lockDigest,
			GeneratedAt: factoryTimestamp.Format(time.RFC3339), Operation: kind,
			AggregateSBOM: AggregateSBOM{MediaType: "application/spdx+json", SHA256: testDigest(nondeterministic)},
			Images:        releaseImages,
		},
		ScannerLock: *loadedLock,
		Policy:      PolicyEvidence{Digest: policyDigest, DeclaredEvidenceDigest: testDigest("8")},
		Images:      imageEvidence,
		Quality: QualityEvidence{
			CorpusDigest: testDigest("9"), PolicyDigest: policyDigest,
			Runs: []scannerquality.Evidence{{
				SchemaVersion: scannerquality.EvidenceSchema, GoldenDigest: testDigest("0"),
				VulnerabilityDatabase: scannerquality.DBEvidence{
					Provider: "trivy", Repository: "registry.invalid/db", Digest: testDigest("f"),
					RecordedAt: factoryTimestamp,
				},
				Network:   scannerquality.NetworkEvidence{Mode: "none"},
				Stable:    stable,
				Candidate: candidate,
			}},
		},
	}
}

func testDigest(value string) string { return "sha256:" + strings.Repeat(value, 64) }

func imageIndex(images []ImageEvidence, variant string) int {
	for index := range images {
		if images[index].Variant == variant {
			return index
		}
	}
	return -1
}

func imageReleaseIndex(images []ReleaseImage, variant string) int {
	for index := range images {
		if images[index].Variant == variant {
			return index
		}
	}
	return -1
}

func splitQualityContexts(evidence *Evidence, firstDigest, remainingDigest string) {
	original := evidence.Quality.Runs[0]
	first := original
	first.Scope = []string{original.Stable[0].Tool}
	first.Stable = append([]scannerquality.ToolEvidence(nil), original.Stable[:1]...)
	first.Candidate = append([]scannerquality.ToolEvidence(nil), original.Candidate[:1]...)
	first.VulnerabilityDatabase.Digest = firstDigest

	remaining := original
	remaining.Scope = make([]string, 0, len(original.Stable)-1)
	for _, tool := range original.Stable[1:] {
		remaining.Scope = append(remaining.Scope, tool.Tool)
	}
	remaining.Stable = append([]scannerquality.ToolEvidence(nil), original.Stable[1:]...)
	remaining.Candidate = append([]scannerquality.ToolEvidence(nil), original.Candidate[1:]...)
	remaining.VulnerabilityDatabase.Digest = remainingDigest
	evidence.Quality.Runs = []scannerquality.Evidence{first, remaining}
}

func reverse[T any](values []T) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
