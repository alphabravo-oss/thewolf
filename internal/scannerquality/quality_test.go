package scannerquality

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannertools/manifest"
)

var qualityTestNow = time.Date(2026, 7, 30, 19, 0, 0, 0, time.UTC)

func TestRepositoryCoverageIsComplete(t *testing.T) {
	t.Parallel()
	coverage, err := ValidateRepository(context.Background(), repositoryRoot(t), qualityTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if coverage.Tools != 49 || coverage.ParserOwnedTools != 49 ||
		coverage.ParserAdapters != 49 || coverage.HostileTestedAdapters != 49 ||
		coverage.ValidTestedAdapters != 49 ||
		coverage.ScannerVariants != 4 || coverage.ScannerPlatformTuples != 7 ||
		coverage.FixerVariants != 5 || coverage.FixerPlatformTuples != 10 ||
		coverage.Families != 23 || coverage.Fixtures != 54 ||
		coverage.GoldenExpectations != 23 {
		t.Fatalf("unexpected coverage: %#v", coverage)
	}
}

func TestParserAdapterDiscoveryFailsClosedOnMissingTestClass(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	pluginDir := filepath.Join(root, "plugins", "demo")
	if err := os.MkdirAll(pluginDir, 0o750); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(pluginDir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("demo.go", `package demo
type Plugin struct{}
func (*Plugin) Name() string { return "demo" }
func parseDemoOutput([]byte) {}
`)
	write("parser_conformance_test.go", "package demo\nvar _ = parseDemoOutput\n")
	policy := Policy{Tools: map[string]ToolPolicy{"demo": {ParserOwned: true}}}
	if _, err := validateParserAdapters(context.Background(), root, policy); err == nil || !strings.Contains(err.Error(), "valid finding fixture") {
		t.Fatalf("missing valid fixture error = %v", err)
	}
	write("demo_test.go", "package demo\nvar _ = parseDemoOutput\n")
	coverage, err := validateParserAdapters(context.Background(), root, policy)
	if err != nil {
		t.Fatal(err)
	}
	if coverage.ParserAdapters != 1 || coverage.HostileTestedAdapters != 1 || coverage.ValidTestedAdapters != 1 {
		t.Fatalf("adapter coverage = %#v", coverage)
	}
}

func TestCoverageFailsClosedOnMissingToolVariantFixtureAndDatabaseIdentity(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	toolManifest, policy, corpus, builds, database := loadDefinitions(t, root)
	tests := []struct {
		name   string
		mutate func(*Policy, *Corpus, *buildPolicy, *DBLock)
		want   string
	}{
		{
			name: "missing tool",
			mutate: func(policy *Policy, _ *Corpus, _ *buildPolicy, _ *DBLock) {
				delete(policy.Tools, "semgrep")
			},
			want: "tool coverage",
		},
		{
			name: "missing scanner variant",
			mutate: func(policy *Policy, _ *Corpus, _ *buildPolicy, _ *DBLock) {
				delete(policy.ScannerVariants, "rust")
			},
			want: "scanner variant coverage",
		},
		{
			name: "missing fixer variant",
			mutate: func(policy *Policy, _ *Corpus, _ *buildPolicy, _ *DBLock) {
				delete(policy.FixerVariants, "api")
			},
			want: "fixer variant coverage",
		},
		{
			name: "missing malformed parser case",
			mutate: func(_ *Policy, corpus *Corpus, _ *buildPolicy, _ *DBLock) {
				delete(corpus.ParserCases["json"], "malformed")
			},
			want: "missing malformed coverage",
		},
		{
			name: "fixture digest drift",
			mutate: func(_ *Policy, corpus *Corpus, _ *buildPolicy, _ *DBLock) {
				fixture := corpus.Fixtures["src-go"]
				fixture.SHA256 = "sha256:" + strings.Repeat("f", 64)
				corpus.Fixtures["src-go"] = fixture
			},
			want: "source digest mismatch",
		},
		{
			name: "database mismatch",
			mutate: func(_ *Policy, _ *Corpus, _ *buildPolicy, database *DBLock) {
				database.Provider = "other"
			},
			want: "identity is missing or mismatched",
		},
		{
			name: "database stale",
			mutate: func(_ *Policy, _ *Corpus, _ *buildPolicy, database *DBLock) {
				database.RecordedAt = qualityTestNow.Add(-2 * time.Hour)
				database.ExpiresAt = qualityTestNow.Add(-time.Minute)
			},
			want: "stale",
		},
	}
	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			policyCopy := policy
			policyCopy.Tools = cloneMap(policy.Tools)
			policyCopy.ScannerVariants = cloneMap(policy.ScannerVariants)
			policyCopy.FixerVariants = cloneMap(policy.FixerVariants)
			corpusCopy := corpus
			corpusCopy.Fixtures = cloneMap(corpus.Fixtures)
			corpusCopy.ParserCases = cloneNestedMap(corpus.ParserCases)
			buildCopy, databaseCopy := builds, database
			testCase.mutate(&policyCopy, &corpusCopy, &buildCopy, &databaseCopy)
			_, err := validate(
				context.Background(), toolManifest, policyCopy, corpusCopy,
				buildCopy, databaseCopy, qualityTestNow,
			)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("validate() error = %v, want substring %q", err, testCase.want)
			}
		})
	}
}

func TestEvidenceGoldenComparisonThresholdsAndDBIdentity(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	_, policy, _, _, database := loadDefinitions(t, root)
	evidence := completeEvidence(policy, database)
	finding := Finding{
		Tool: "semgrep", RuleID: "WOLF-FIXTURE", Severity: "high",
		Path: "src/main.py", Line: 1, Message: "fixture",
		Fingerprint: "fixture-fingerprint",
	}
	setFindings(&evidence.Stable, "semgrep", []Finding{finding})
	setFindings(&evidence.Candidate, "semgrep", []Finding{finding})
	if err := EvaluateEvidence(context.Background(), policy, database, evidence, qualityTestNow); err != nil {
		t.Fatal(err)
	}

	loss := evidence
	loss.Candidate = cloneToolEvidence(evidence.Candidate)
	replacement := finding
	replacement.RuleID = "WOLF-OTHER"
	replacement.Fingerprint = "other-fingerprint"
	setFindings(&loss.Candidate, "semgrep", []Finding{replacement})
	if err := EvaluateEvidence(context.Background(), policy, database, loss, qualityTestNow); err == nil ||
		!strings.Contains(err.Error(), "finding loss") {
		t.Fatalf("finding loss error = %v", err)
	}

	drift := evidence
	drift.Candidate = cloneToolEvidence(evidence.Candidate)
	changed := finding
	changed.Severity = "low"
	setFindings(&drift.Candidate, "semgrep", []Finding{changed})
	if err := EvaluateEvidence(context.Background(), policy, database, drift, qualityTestNow); err == nil ||
		!strings.Contains(err.Error(), "severity drift") {
		t.Fatalf("severity drift error = %v", err)
	}

	threshold := evidence
	threshold.Candidate = cloneToolEvidence(evidence.Candidate)
	for index := range threshold.Candidate {
		if threshold.Candidate[index].Tool == "semgrep" {
			threshold.Candidate[index].PeakMemoryBytes =
				policy.ThresholdProfiles["heavy"].PeakMemoryBytes + 1
		}
	}
	if err := EvaluateEvidence(context.Background(), policy, database, threshold, qualityTestNow); err == nil ||
		!strings.Contains(err.Error(), "resource threshold") {
		t.Fatalf("threshold error = %v", err)
	}

	parseError := evidence
	parseError.Candidate = cloneToolEvidence(evidence.Candidate)
	for index := range parseError.Candidate {
		if parseError.Candidate[index].Tool == "semgrep" {
			parseError.Candidate[index].ParseErrors = 1
		}
	}
	if err := EvaluateEvidence(context.Background(), policy, database, parseError, qualityTestNow); err == nil ||
		!strings.Contains(err.Error(), "resource threshold") {
		t.Fatalf("parse error gate = %v", err)
	}

	dbMismatch := evidence
	dbMismatch.VulnerabilityDatabase.Digest = "sha256:" + strings.Repeat("f", 64)
	if err := EvaluateEvidence(context.Background(), policy, database, dbMismatch, qualityTestNow); err == nil ||
		!strings.Contains(err.Error(), "database identity") {
		t.Fatalf("database identity error = %v", err)
	}
}

func TestRuntimeEvidenceIsBoundToRealOutputExpectations(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	_, policy, _, _, database := loadDefinitions(t, root)
	goldens, goldenDigest, err := LoadGoldenExpectations(root)
	if err != nil {
		t.Fatal(err)
	}
	evidence := completeEvidence(policy, database)
	evidence.Scope = []string{"bandit"}
	evidence.GoldenDigest = goldenDigest
	evidence.Stable = []ToolEvidence{toolEvidenceFor(evidence.Stable, "bandit")}
	evidence.Candidate = []ToolEvidence{toolEvidenceFor(evidence.Candidate, "bandit")}
	if err := EvaluateGoldenEvidence(policy, goldens, goldenDigest, evidence); err != nil {
		t.Fatal(err)
	}
	mutated := evidence
	mutated.Candidate = cloneToolEvidence(evidence.Candidate)
	mutated.Candidate[0].Findings = nil
	mutated.Candidate[0].RawOutputDigest = "sha256:" + strings.Repeat("c", 64)
	mutated.Candidate[0].RawOutputBytes = 1
	if err := EvaluateGoldenEvidence(policy, goldens, goldenDigest, mutated); err == nil {
		t.Fatal("candidate that lost the required real fixture finding was accepted")
	}

	codeqlGoldens := expectationsForTool(goldens, "codeql")
	structural := completeEvidence(policy, database)
	structural.Scope = []string{"codeql"}
	structural.GoldenDigest = goldenDigest
	structural.Stable = []ToolEvidence{toolEvidenceFor(structural.Stable, "codeql")}
	structural.Candidate = []ToolEvidence{toolEvidenceFor(structural.Candidate, "codeql")}
	canonical, err := CanonicalGoldenExpectations(codeqlGoldens)
	if err != nil {
		t.Fatal(err)
	}
	structural.Stable[0].OutputDigest = sha256Bytes(canonical)
	structural.Candidate[0].OutputDigest = sha256Bytes(canonical)
	if err := EvaluateGoldenEvidence(policy, goldens, goldenDigest, structural); err != nil {
		t.Fatal(err)
	}
	structural.Candidate[0].OutputDigest = "sha256:" + strings.Repeat("f", 64)
	if err := EvaluateGoldenEvidence(policy, goldens, goldenDigest, structural); err == nil {
		t.Fatal("structural evidence that was not bound to its parser golden was accepted")
	}
}

func TestEvidenceNetworkIdentityIsExact(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	_, policy, _, _, database := loadDefinitions(t, root)
	evidence := completeEvidence(policy, database)
	digest := "sha256:" + strings.Repeat("b", 64)
	evidence.Network = NetworkEvidence{
		Mode: "controlled-internal", Name: "wolf-quality-fixtures",
		ID: strings.Repeat("c", 64), PolicyDigest: digest,
	}
	if err := EvaluateEvidence(context.Background(), policy, database, evidence, qualityTestNow); err != nil {
		t.Fatal(err)
	}

	tests := []NetworkEvidence{
		{},
		{Mode: "none", Name: "wolf-quality-fixtures"},
		{Mode: "controlled-internal", Name: "bridge", ID: strings.Repeat("c", 64), PolicyDigest: digest},
		{Mode: "controlled-internal", Name: "wolf-quality-fixtures", ID: "not-a-network-id", PolicyDigest: digest},
		{Mode: "controlled-internal", Name: "wolf-quality-fixtures", ID: strings.Repeat("c", 64), PolicyDigest: "mutable"},
	}
	for _, invalid := range tests {
		copy := evidence
		copy.Network = invalid
		if err := EvaluateEvidence(context.Background(), policy, database, copy, qualityTestNow); err == nil {
			t.Fatalf("invalid network evidence was accepted: %+v", invalid)
		}
	}
}

func TestCanonicalFindingsStableSortAndBounds(t *testing.T) {
	t.Parallel()
	left := Finding{Tool: "z", RuleID: "b", Path: "z.go", Line: 2, Fingerprint: "2"}
	right := Finding{Tool: "a", RuleID: "a", Path: "a.go", Line: 1, Fingerprint: "1"}
	first, err := CanonicalFindings([]Finding{left, right})
	if err != nil {
		t.Fatal(err)
	}
	second, err := CanonicalFindings([]Finding{right, left})
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) ||
		strings.Index(string(first), `"tool": "a"`) > strings.Index(string(first), `"tool": "z"`) {
		t.Fatalf("canonical output is unstable:\n%s", first)
	}
	if _, err := CanonicalFindings(make([]Finding, maxEvidenceItems+1)); err == nil {
		t.Fatal("oversized finding set was accepted")
	}
}

func TestEvidenceScopeIsExactAndStillEnforcesMeasuredIdentity(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	_, policy, _, _, database := loadDefinitions(t, root)
	evidence := completeEvidence(policy, database)
	evidence.Scope = []string{"bandit"}
	for index := range evidence.Stable {
		if evidence.Stable[index].Tool == "bandit" {
			evidence.Stable = []ToolEvidence{evidence.Stable[index]}
			break
		}
	}
	for index := range evidence.Candidate {
		if evidence.Candidate[index].Tool == "bandit" {
			evidence.Candidate = []ToolEvidence{evidence.Candidate[index]}
			break
		}
	}
	if err := EvaluateEvidence(context.Background(), policy, database, evidence, qualityTestNow); err != nil {
		t.Fatal(err)
	}
	evidence.Scope = []string{"bandit", "unknown"}
	if err := EvaluateEvidence(context.Background(), policy, database, evidence, qualityTestNow); err == nil {
		t.Fatal("unknown quality evidence scope was accepted")
	}
}

func TestMaterializeExecutableCorpusWritesOnlyCanonicalSourcePaths(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	_, _, corpus, _, _ := loadDefinitions(t, root)
	destination := t.TempDir()
	if err := MaterializeExecutableCorpus(destination, corpus); err != nil {
		t.Fatal(err)
	}
	for fixture, relative := range map[string]string{
		"src-python": "src/main.py", "src-go": "main.go",
		"src-container": "Dockerfile", "src-openapi": "openapi.yaml",
	} {
		value, err := os.ReadFile(filepath.Join(destination, relative))
		if err != nil {
			t.Fatal(err)
		}
		if digest(value) != corpus.Fixtures[fixture].SHA256 {
			t.Fatalf("materialized fixture %s digest changed", fixture)
		}
	}
	if _, err := os.Stat(filepath.Join(destination, "json-adversarial")); !os.IsNotExist(err) {
		t.Fatal("parser-only hostile fixture was materialized as executable source")
	}
	archive, err := os.ReadFile(filepath.Join(destination, "dockle-image.tar"))
	if err != nil || len(archive) == 0 {
		t.Fatalf("Dockle archive was not materialized: %v", err)
	}
	reader := tar.NewReader(bytes.NewReader(archive))
	archiveEntries := map[string]bool{}
	for {
		header, readErr := reader.Next()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			t.Fatal(readErr)
		}
		archiveEntries[header.Name] = true
		if header.ModTime.Unix() != 0 {
			t.Fatalf("non-deterministic Dockle archive header: %#v", header)
		}
	}
	if !archiveEntries["manifest.json"] || !archiveEntries["layer.tar"] {
		t.Fatalf("Dockle archive entries = %v", archiveEntries)
	}
	if _, err := os.Stat(filepath.Join(destination, ".git", "HEAD")); err != nil {
		t.Fatalf("Scorecard Git fixture was not materialized: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destination, ".git", "index")); !os.IsNotExist(err) {
		t.Fatalf("host-dependent Git index was retained: %v", err)
	}
	second := t.TempDir()
	if err := MaterializeExecutableCorpus(second, corpus); err != nil {
		t.Fatal(err)
	}
	secondArchive, err := os.ReadFile(filepath.Join(second, "dockle-image.tar"))
	if err != nil || !bytes.Equal(archive, secondArchive) {
		t.Fatal("Dockle quality archive is not deterministic")
	}
}

func TestValidationHonorsCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ValidateRepository(ctx, repositoryRoot(t), qualityTestNow); err == nil {
		t.Fatal("cancelled validation succeeded")
	}
}

func TestLoadEvidenceRejectsMalformedAndOversizedInput(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "evidence.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadEvidence(path); err == nil {
		t.Fatal("malformed evidence was accepted")
	}
	if testing.Short() {
		return
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxEvidenceBytes + 1); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadEvidence(path); err == nil ||
		!strings.Contains(err.Error(), "size bound") {
		t.Fatalf("oversized evidence error = %v", err)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := manifest.FindRepoRoot("")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func loadDefinitions(
	t *testing.T,
	root string,
) (*manifest.Manifest, Policy, Corpus, buildPolicy, DBLock) {
	t.Helper()
	toolManifest, err := manifest.LoadFile(filepath.Join(root, "scanners/tools.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var policy Policy
	if err := readYAML(filepath.Join(root, PolicyPath), &policy); err != nil {
		t.Fatal(err)
	}
	var corpus Corpus
	if err := readYAML(filepath.Join(root, CorpusPath), &corpus); err != nil {
		t.Fatal(err)
	}
	var builds buildPolicy
	if err := readYAML(filepath.Join(root, "scanners/build-policy.yaml"), &builds); err != nil {
		t.Fatal(err)
	}
	var database DBLock
	if err := readJSON(filepath.Join(root, DBLockPath), &database); err != nil {
		t.Fatal(err)
	}
	return toolManifest, policy, corpus, builds, database
}

func completeEvidence(policy Policy, database DBLock) Evidence {
	evidence := Evidence{
		SchemaVersion: EvidenceSchema,
		GoldenDigest:  "sha256:" + strings.Repeat("b", 64),
		VulnerabilityDatabase: DBEvidence{
			Provider: database.Provider, Repository: database.Repository,
			Digest: database.Digest, RecordedAt: database.RecordedAt,
		},
		Network: NetworkEvidence{Mode: "none"},
	}
	for tool := range policy.Tools {
		digest := "sha256:" + strings.Repeat("a", 64)
		value := ToolEvidence{
			Tool: tool, ExecutionMode: "executed", OutputKind: "normalized-findings",
			ImageReference: "registry.example/wolf/scanners@" + digest,
			ImageDigest:    digest, OutputDigest: digest,
			DurationMS: 1, OutputBytes: 1, PeakMemoryBytes: 1,
			Findings: []Finding{{
				Tool: tool, RuleID: "WOLF-QUALITY-EXECUTED", Severity: "low",
				Path: "fixture", Line: 1, Message: "fixture", Fingerprint: tool + "-fixture",
			}},
		}
		if policy.Tools[tool].Strategy == "structural" {
			value.ExecutionMode = "structural"
			value.OutputKind = "structural-manifest"
			value.DurationMS = 0
			value.PeakMemoryBytes = 0
			value.Findings = nil
		}
		if policy.Tools[tool].AllowEmptyFindings {
			value.Findings = nil
			value.RawOutputDigest = digest
			value.RawOutputBytes = 1
		}
		evidence.Stable = append(evidence.Stable, value)
		evidence.Candidate = append(evidence.Candidate, value)
	}
	return evidence
}

func setFindings(values *[]ToolEvidence, tool string, findings []Finding) {
	for index := range *values {
		if (*values)[index].Tool == tool {
			(*values)[index].Findings = findings
			return
		}
	}
}

func cloneToolEvidence(values []ToolEvidence) []ToolEvidence {
	data, _ := json.Marshal(values)
	var clone []ToolEvidence
	_ = json.Unmarshal(data, &clone)
	return clone
}

func expectationsForTool(values []GoldenExpectation, tool string) []GoldenExpectation {
	var selected []GoldenExpectation
	for _, value := range values {
		if value.Tool == tool {
			selected = append(selected, value)
		}
	}
	return selected
}

func toolEvidenceFor(values []ToolEvidence, tool string) ToolEvidence {
	for _, value := range values {
		if value.Tool == tool {
			return value
		}
	}
	return ToolEvidence{}
}

func cloneMap[K comparable, V any](input map[K]V) map[K]V {
	output := make(map[K]V, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func cloneNestedMap(input map[string]map[string]string) map[string]map[string]string {
	output := make(map[string]map[string]string, len(input))
	for key, value := range input {
		output[key] = cloneMap(value)
	}
	return output
}
