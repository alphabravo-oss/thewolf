// Package scannerquality defines deterministic compatibility and evidence
// gates for every scanner tool and every Wolf-owned scanner/fixer image.
package scannerquality

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannerbuild"
	"github.com/alphabravocompany/thewolf/internal/scannertools/manifest"
	git "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/object"
	"gopkg.in/yaml.v3"
)

const (
	PolicyPath = "scanners/quality/policy.yaml"
	CorpusPath = "scanners/quality/corpus.yaml"
	DBLockPath = "scanners/quality/trivy-db.lock.json"
	GoldenPath = "scanners/quality/goldens/family-findings.json"

	policySchema = "wolf.scanners/quality-policy/v1"
	corpusSchema = "wolf.scanners/quality-corpus/v1"
	dbLockSchema = "wolf.scanners/vulnerability-db-lock/v1"

	maxDefinitionBytes = 8 << 20
	maxFixtureBytes    = 4 << 20
)

var requiredParserCases = []string{
	"valid", "malformed", "partial", "empty", "large", "encoded",
	"non-utf8", "adversarial",
}

type Policy struct {
	SchemaVersion     string                      `yaml:"schemaVersion"`
	ExpectedToolCount int                         `yaml:"expectedToolCount"`
	Tools             map[string]ToolPolicy       `yaml:"tools"`
	ParserContract    ParserInvocationContract    `yaml:"parserContract"`
	ThresholdProfiles map[string]Threshold        `yaml:"thresholdProfiles"`
	ScannerVariants   map[string]VariantPolicy    `yaml:"scannerVariants"`
	FixerVariants     map[string]VariantPolicy    `yaml:"fixerVariants"`
	ToleratedDeltas   []ToleratedDelta            `yaml:"toleratedDeltas"`
	VulnerabilityDB   VulnerabilityDatabasePolicy `yaml:"vulnerabilityDatabase"`
}

type ParserInvocationContract struct {
	AdapterNamespace string   `yaml:"adapterNamespace"`
	Command          []string `yaml:"command"`
	ResultSchema     string   `yaml:"resultSchema"`
	RequiredFields   []string `yaml:"requiredFields"`
}

type ToolPolicy struct {
	Family             string `yaml:"family"`
	Strategy           string `yaml:"strategy"`
	ParserOwned        bool   `yaml:"parserOwned"`
	ParserFormat       string `yaml:"parserFormat"`
	ThresholdProfile   string `yaml:"thresholdProfile"`
	AllowEmptyFindings bool   `yaml:"allowEmptyFindings"`
	Rationale          string `yaml:"rationale"`
}

type Threshold struct {
	DurationMS      int64 `yaml:"durationMs"`
	OutputBytes     int64 `yaml:"outputBytes"`
	PeakMemoryBytes int64 `yaml:"peakMemoryBytes"`
	MaxParseErrors  int   `yaml:"maxParseErrors"`
	MaxFindings     int   `yaml:"maxFindings"`
}

type VariantPolicy struct {
	Dockerfile string   `yaml:"dockerfile"`
	Platforms  []string `yaml:"platforms"`
	Strategy   string   `yaml:"strategy"`
	Rationale  string   `yaml:"rationale"`
}

type ToleratedDelta struct {
	Tool      string    `yaml:"tool"`
	RuleID    string    `yaml:"ruleId"`
	Kind      string    `yaml:"kind"`
	Reason    string    `yaml:"reason"`
	ExpiresAt time.Time `yaml:"expiresAt"`
}

// GoldenExpectation is an operator-reviewed assertion about real scanner
// output on the content-addressed executable corpus. It deliberately does not
// invent tool rule IDs or fingerprints: those identities must come from the
// actual stable and candidate executions.
type GoldenExpectation struct {
	Tool            string `json:"tool"`
	Family          string `json:"family"`
	Mode            string `json:"mode"`
	MinimumFindings int    `json:"minimumFindings"`
	Rationale       string `json:"rationale,omitempty"`
}

type VulnerabilityDatabasePolicy struct {
	Provider    string `yaml:"provider"`
	Repository  string `yaml:"repository"`
	LockFile    string `yaml:"lockFile"`
	MaxAgeHours int64  `yaml:"maxAgeHours"`
}

type Corpus struct {
	SchemaVersion string                       `yaml:"schemaVersion"`
	Fixtures      map[string]Fixture           `yaml:"fixtures"`
	Families      map[string]FamilyFixture     `yaml:"families"`
	ParserCases   map[string]map[string]string `yaml:"parserCases"`
}

type Fixture struct {
	Kind        string `yaml:"kind"`
	Content     string `yaml:"content"`
	Base64      string `yaml:"base64"`
	Repeat      string `yaml:"repeat"`
	RepeatCount int    `yaml:"repeatCount"`
	SHA256      string `yaml:"sha256"`
}

type FamilyFixture struct {
	SourceFixtures []string `yaml:"sourceFixtures"`
	Categories     []string `yaml:"categories"`
}

type DBLock struct {
	SchemaVersion string    `json:"schemaVersion"`
	Provider      string    `json:"provider"`
	Repository    string    `json:"repository"`
	Digest        string    `json:"digest"`
	RecordedAt    time.Time `json:"recordedAt"`
	ExpiresAt     time.Time `json:"expiresAt"`
}

type Coverage struct {
	Tools                 int
	Families              int
	ScannerVariants       int
	FixerVariants         int
	ScannerPlatformTuples int
	FixerPlatformTuples   int
	ParserOwnedTools      int
	ParserAdapters        int
	HostileTestedAdapters int
	ValidTestedAdapters   int
	Fixtures              int
	GoldenExpectations    int
}

type parserAdapter struct {
	Tool          string
	Function      string
	SourceFile    string
	HostileTested bool
	ValidTested   bool
}

type buildPolicy struct {
	SchemaVersion string                        `yaml:"schemaVersion"`
	Variants      map[string]buildPolicyVariant `yaml:"variants"`
	FixerVariants map[string]buildPolicyVariant `yaml:"fixerVariants"`
}

type buildPolicyVariant struct {
	Dockerfile   string            `yaml:"dockerfile"`
	Context      string            `yaml:"context"`
	Image        string            `yaml:"image"`
	Platforms    []string          `yaml:"platforms"`
	DependsOn    []string          `yaml:"dependsOn"`
	BuildArgs    map[string]string `yaml:"buildArgs"`
	AuthMode     string            `yaml:"authMode"`
	SmokeCommand []string          `yaml:"smokeCommand"`
}

func ValidateRepository(ctx context.Context, root string, now time.Time) (Coverage, error) {
	if err := ctx.Err(); err != nil {
		return Coverage{}, err
	}
	toolManifest, err := manifest.LoadFile(filepath.Join(root, "scanners", "tools.yaml"))
	if err != nil {
		return Coverage{}, err
	}
	var policy Policy
	if err := readYAML(filepath.Join(root, PolicyPath), &policy); err != nil {
		return Coverage{}, err
	}
	var corpus Corpus
	if err := readYAML(filepath.Join(root, CorpusPath), &corpus); err != nil {
		return Coverage{}, err
	}
	var builds buildPolicy
	if err := readYAML(filepath.Join(root, "scanners/build-policy.yaml"), &builds); err != nil {
		return Coverage{}, err
	}
	var database DBLock
	if err := readJSON(filepath.Join(root, DBLockPath), &database); err != nil {
		return Coverage{}, err
	}
	coverage, err := validate(ctx, toolManifest, policy, corpus, builds, database, now.UTC())
	if err != nil {
		return Coverage{}, err
	}
	adapterCoverage, err := validateParserAdapters(ctx, root, policy)
	if err != nil {
		return Coverage{}, err
	}
	coverage.ParserAdapters = adapterCoverage.ParserAdapters
	coverage.HostileTestedAdapters = adapterCoverage.HostileTestedAdapters
	coverage.ValidTestedAdapters = adapterCoverage.ValidTestedAdapters
	goldenCount, err := validateGoldenFile(filepath.Join(root, GoldenPath), policy, corpus)
	if err != nil {
		return Coverage{}, err
	}
	coverage.GoldenExpectations = goldenCount
	return coverage, nil
}

func ValidateEvidenceFile(
	ctx context.Context,
	root, evidencePath string,
	now time.Time,
) (Coverage, error) {
	coverage, err := ValidateRepository(ctx, root, now)
	if err != nil {
		return Coverage{}, err
	}
	var policy Policy
	if err := readYAML(filepath.Join(root, PolicyPath), &policy); err != nil {
		return Coverage{}, err
	}
	var database DBLock
	if err := readJSON(filepath.Join(root, DBLockPath), &database); err != nil {
		return Coverage{}, err
	}
	evidence, err := LoadEvidence(evidencePath)
	if err != nil {
		return Coverage{}, err
	}
	if err := EvaluateEvidence(ctx, policy, database, evidence, now.UTC()); err != nil {
		return Coverage{}, err
	}
	goldens, goldenDigest, err := LoadGoldenExpectations(root)
	if err != nil {
		return Coverage{}, err
	}
	if err := EvaluateGoldenEvidence(policy, goldens, goldenDigest, evidence); err != nil {
		return Coverage{}, err
	}
	return coverage, nil
}

// LoadGoldenExpectations returns validated family-level assertions and their
// content digest. Runtime evidence binds this exact policy while every rule ID,
// fingerprint, and finding remains derived from actual tool output.
func LoadGoldenExpectations(root string) ([]GoldenExpectation, string, error) {
	var policy Policy
	if err := readYAML(filepath.Join(root, PolicyPath), &policy); err != nil {
		return nil, "", err
	}
	var corpus Corpus
	if err := readYAML(filepath.Join(root, CorpusPath), &corpus); err != nil {
		return nil, "", err
	}
	path := filepath.Join(root, GoldenPath)
	if _, err := validateGoldenFile(path, policy, corpus); err != nil {
		return nil, "", err
	}
	data, err := readBounded(path)
	if err != nil {
		return nil, "", err
	}
	var expectations []GoldenExpectation
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&expectations); err != nil {
		return nil, "", err
	}
	canonical, err := CanonicalGoldenExpectations(expectations)
	if err != nil {
		return nil, "", err
	}
	return expectations, digest(canonical), nil
}

// LoadExecutionInputs returns the exact validated definitions used by a
// managed stable/candidate comparison. Repository validation happens first so
// callers cannot execute a corpus whose parser, build, lock, or golden
// coverage is incomplete.
func LoadExecutionInputs(
	ctx context.Context, root string, now time.Time,
) (Policy, Corpus, DBLock, error) {
	if _, err := ValidateRepository(ctx, root, now); err != nil {
		return Policy{}, Corpus{}, DBLock{}, err
	}
	var policy Policy
	if err := readYAML(filepath.Join(root, PolicyPath), &policy); err != nil {
		return Policy{}, Corpus{}, DBLock{}, err
	}
	var corpus Corpus
	if err := readYAML(filepath.Join(root, CorpusPath), &corpus); err != nil {
		return Policy{}, Corpus{}, DBLock{}, err
	}
	var database DBLock
	if err := readJSON(filepath.Join(root, DBLockPath), &database); err != nil {
		return Policy{}, Corpus{}, DBLock{}, err
	}
	return policy, corpus, database, nil
}

// MaterializeExecutableCorpus writes the canonical source fixtures to one
// deterministic polyglot repository. Parser-output fixtures remain inputs to
// the trusted parser conformance gate and are deliberately not executable
// source files.
func MaterializeExecutableCorpus(destination string, corpus Corpus) error {
	info, err := os.Lstat(destination)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("quality corpus destination must be a real directory")
	}
	paths := map[string]string{
		"src-python": "src/main.py", "src-ruby": "lib/main.rb",
		"src-rust": "src/main.rs", "src-go": "main.go",
		"src-javascript": "src/main.js", "src-jvm": "src/Main.java",
		"src-cpp": "src/main.cpp", "src-php": "src/main.php",
		"src-swift": "Sources/main.swift", "src-shell": "fixture.sh",
		"src-docs": "README.md", "src-sql": "fixture.sql",
		"src-terraform": "main.tf", "src-kubernetes": "deployment.yaml",
		"src-container": "Dockerfile", "src-dependency": "package-lock.json",
		"src-secret": "fixture.env", "src-sbom": "sbom.spdx.json",
		"src-openapi": "openapi.yaml", "src-polyglot": "fixture.yaml",
		"src-codeql": "src/main.c", "src-supply-chain": "repository.json",
	}
	keys := make([]string, 0, len(paths))
	for key := range paths {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fixture, ok := corpus.Fixtures[key]
		if !ok {
			return fmt.Errorf("quality executable corpus is missing fixture %q", key)
		}
		value, err := expandFixture(fixture)
		if err != nil || digest(value) != fixture.SHA256 {
			return fmt.Errorf("quality executable fixture %q is invalid", key)
		}
		target := filepath.Join(destination, filepath.FromSlash(paths[key]))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		if _, err := file.Write(value); err != nil {
			file.Close()
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
	}
	if err := materializeDockleArchive(filepath.Join(destination, "dockle-image.tar")); err != nil {
		return fmt.Errorf("materialize Dockle quality archive: %w", err)
	}
	if err := materializeQualityGitRepository(destination); err != nil {
		return fmt.Errorf("materialize Scorecard quality repository: %w", err)
	}
	return nil
}

// materializeDockleArchive creates a deterministic Docker-save archive with
// intentionally poor image metadata and a fake credential filename. Dockle
// can inspect this through --input without a container-engine socket.
func materializeDockleArchive(path string) error {
	var layer bytes.Buffer
	layerWriter := tar.NewWriter(&layer)
	if err := writeCorpusTarFile(
		layerWriter, "app/credentials.json", []byte("{\"token\":\"wolf_fake_not_a_credential\"}\n"), 0o600,
	); err != nil {
		return err
	}
	if err := layerWriter.Close(); err != nil {
		return err
	}
	layerSum := sha256.Sum256(layer.Bytes())
	configuration := map[string]any{
		"architecture": "amd64",
		"os":           "linux",
		"config": map[string]any{
			"Env":  []string{"MYSQL_PASSWORD=wolf_fake_not_a_credential"},
			"User": "",
		},
		"rootfs": map[string]any{
			"type":     "layers",
			"diff_ids": []string{"sha256:" + hex.EncodeToString(layerSum[:])},
		},
		"history": []map[string]any{{"created_by": "ADD credentials.json /app/credentials.json"}},
	}
	configBytes, err := json.Marshal(configuration)
	if err != nil {
		return err
	}
	configSum := sha256.Sum256(configBytes)
	configName := hex.EncodeToString(configSum[:]) + ".json"
	manifestBytes, err := json.Marshal([]map[string]any{{
		"Config": configName, "RepoTags": []string{"wolf-quality/dockle:fixture"},
		"Layers": []string{"layer.tar"},
	}})
	if err != nil {
		return err
	}
	var archive bytes.Buffer
	archiveWriter := tar.NewWriter(&archive)
	for _, entry := range []struct {
		name  string
		value []byte
	}{
		{configName, configBytes},
		{"layer.tar", layer.Bytes()},
		{"manifest.json", manifestBytes},
	} {
		if err := writeCorpusTarFile(archiveWriter, entry.name, entry.value, 0o600); err != nil {
			return err
		}
	}
	if err := archiveWriter.Close(); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(archive.Bytes()); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

func writeCorpusTarFile(writer *tar.Writer, name string, value []byte, mode int64) error {
	header := &tar.Header{
		Name: name, Mode: mode, Size: int64(len(value)), Typeflag: tar.TypeReg,
		ModTime: time.Unix(0, 0).UTC(), Uid: 65532, Gid: 65532,
	}
	if err := writer.WriteHeader(header); err != nil {
		return err
	}
	_, err := writer.Write(value)
	return err
}

func materializeQualityGitRepository(destination string) error {
	var paths []string
	if err := filepath.WalkDir(destination, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == destination || entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(destination, path)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(relative))
		return nil
	}); err != nil {
		return err
	}
	sort.Strings(paths)
	repository, err := git.PlainInit(destination, false)
	if err != nil {
		return err
	}
	if _, err := repository.CreateRemote(&gitconfig.RemoteConfig{
		Name: "origin", URLs: []string{"https://quality-fixture.invalid/wolf/repository.git"},
	}); err != nil {
		return err
	}
	worktree, err := repository.Worktree()
	if err != nil {
		return err
	}
	for _, path := range paths {
		if _, err := worktree.Add(path); err != nil {
			return err
		}
	}
	signature := &object.Signature{
		Name: "Wolf Quality Fixture", Email: "quality-fixture@invalid.example",
		When: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	if _, err := worktree.Commit("deterministic scanner quality corpus", &git.CommitOptions{
		Author: signature, Committer: signature,
	}); err != nil {
		return err
	}
	// Git's index records host filesystem timestamps. It is not needed for a
	// read-only local Scorecard scan, so omit it from the content-addressed
	// quality volume.
	if err := os.Remove(filepath.Join(destination, ".git", "index")); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func validate(
	ctx context.Context,
	toolManifest *manifest.Manifest,
	policy Policy,
	corpus Corpus,
	builds buildPolicy,
	database DBLock,
	now time.Time,
) (Coverage, error) {
	if policy.SchemaVersion != policySchema {
		return Coverage{}, fmt.Errorf("scanner quality policy schema %q is unsupported", policy.SchemaVersion)
	}
	if corpus.SchemaVersion != corpusSchema {
		return Coverage{}, fmt.Errorf("scanner quality corpus schema %q is unsupported", corpus.SchemaVersion)
	}
	if len(toolManifest.Tools) != policy.ExpectedToolCount ||
		len(policy.Tools) != policy.ExpectedToolCount {
		return Coverage{}, fmt.Errorf(
			"scanner quality tool coverage is %d manifest/%d policy, expected %d",
			len(toolManifest.Tools), len(policy.Tools), policy.ExpectedToolCount,
		)
	}
	if policy.ParserContract.AdapterNamespace != "wolf/plugin" ||
		len(policy.ParserContract.Command) == 0 ||
		policy.ParserContract.ResultSchema != "wolf.scan-result/v1" ||
		!equalStrings(
			policy.ParserContract.RequiredFields,
			[]string{"fingerprint", "line", "message", "path", "ruleId", "severity", "tool"},
		) {
		return Coverage{}, errors.New("scanner parser invocation contract is incomplete")
	}
	coverage := Coverage{
		Tools: len(policy.Tools), Families: len(corpus.Families),
		ScannerVariants: len(policy.ScannerVariants),
		FixerVariants:   len(policy.FixerVariants), Fixtures: len(corpus.Fixtures),
	}
	for name := range toolManifest.Tools {
		if err := ctx.Err(); err != nil {
			return Coverage{}, err
		}
		tool, ok := policy.Tools[name]
		if !ok {
			return Coverage{}, fmt.Errorf("scanner tool %q has no quality test strategy", name)
		}
		if _, ok := corpus.Families[tool.Family]; !ok {
			return Coverage{}, fmt.Errorf("scanner tool %q references unknown fixture family %q", name, tool.Family)
		}
		if _, ok := policy.ThresholdProfiles[tool.ThresholdProfile]; !ok {
			return Coverage{}, fmt.Errorf("scanner tool %q references unknown threshold profile %q", name, tool.ThresholdProfile)
		}
		switch tool.Strategy {
		case "executable-fixture":
		case "gated-executable", "structural":
			if strings.TrimSpace(tool.Rationale) == "" {
				return Coverage{}, fmt.Errorf("scanner tool %q strategy %q requires a policy rationale", name, tool.Strategy)
			}
		default:
			return Coverage{}, fmt.Errorf("scanner tool %q has invalid strategy %q", name, tool.Strategy)
		}
		if tool.ParserOwned {
			coverage.ParserOwnedTools++
			cases, ok := corpus.ParserCases[tool.ParserFormat]
			if !ok {
				return Coverage{}, fmt.Errorf("scanner tool %q parser format %q has no corpus", name, tool.ParserFormat)
			}
			for _, caseName := range requiredParserCases {
				fixtureID, ok := cases[caseName]
				if !ok {
					return Coverage{}, fmt.Errorf("scanner tool %q parser is missing %s coverage", name, caseName)
				}
				if _, ok := corpus.Fixtures[fixtureID]; !ok {
					return Coverage{}, fmt.Errorf("scanner tool %q parser references unknown fixture %q", name, fixtureID)
				}
			}
		}
		if tool.AllowEmptyFindings && strings.TrimSpace(tool.Rationale) == "" {
			return Coverage{}, fmt.Errorf("scanner tool %q allows empty findings without a rationale", name)
		}
	}
	for name := range policy.Tools {
		if _, ok := toolManifest.Tools[name]; !ok {
			return Coverage{}, fmt.Errorf("quality policy contains unregistered scanner tool %q", name)
		}
	}
	for name, threshold := range policy.ThresholdProfiles {
		if threshold.DurationMS <= 0 || threshold.OutputBytes <= 0 ||
			threshold.PeakMemoryBytes <= 0 || threshold.MaxParseErrors < 0 ||
			threshold.MaxFindings <= 0 {
			return Coverage{}, fmt.Errorf("threshold profile %q is incomplete", name)
		}
	}
	for family, declaration := range corpus.Families {
		if len(declaration.SourceFixtures) == 0 || len(declaration.Categories) == 0 {
			return Coverage{}, fmt.Errorf("fixture family %q is incomplete", family)
		}
		for _, fixtureID := range declaration.SourceFixtures {
			if _, ok := corpus.Fixtures[fixtureID]; !ok {
				return Coverage{}, fmt.Errorf("fixture family %q references unknown fixture %q", family, fixtureID)
			}
		}
	}
	for id, fixture := range corpus.Fixtures {
		expanded, err := expandFixture(fixture)
		if err != nil {
			return Coverage{}, fmt.Errorf("fixture %q: %w", id, err)
		}
		if digest(expanded) != fixture.SHA256 {
			return Coverage{}, fmt.Errorf("fixture %q source digest mismatch", id)
		}
	}
	scannerTuples, err := validateScannerVariants(policy.ScannerVariants, builds)
	if err != nil {
		return Coverage{}, err
	}
	coverage.ScannerPlatformTuples = scannerTuples
	fixerTuples, err := validateFixerVariants(policy.FixerVariants, builds)
	if err != nil {
		return Coverage{}, err
	}
	coverage.FixerPlatformTuples = fixerTuples
	if err := validateDatabaseLock(policy.VulnerabilityDB, database, now); err != nil {
		return Coverage{}, err
	}
	for _, delta := range policy.ToleratedDeltas {
		if delta.Tool == "" || delta.RuleID == "" || delta.Kind == "" ||
			delta.Reason == "" || delta.ExpiresAt.IsZero() {
			return Coverage{}, errors.New("tolerated delta must include tool, rule, kind, reason, and expiry")
		}
		if !delta.ExpiresAt.After(now) {
			return Coverage{}, fmt.Errorf("tolerated delta for %s/%s expired", delta.Tool, delta.RuleID)
		}
	}
	return coverage, nil
}

// validateParserAdapters binds policy entries to the actual parser functions
// and tests in plugins/. This keeps declarative format coverage from being
// mistaken for proof that a Wolf adapter exists or is exercised.
func validateParserAdapters(ctx context.Context, root string, policy Policy) (Coverage, error) {
	adapters, err := discoverParserAdapters(ctx, filepath.Join(root, "plugins"))
	if err != nil {
		return Coverage{}, err
	}
	byTool := make(map[string]parserAdapter, len(adapters))
	coverage := Coverage{ParserAdapters: len(adapters)}
	for _, adapter := range adapters {
		tool, ok := policy.Tools[adapter.Tool]
		if !ok || !tool.ParserOwned {
			return Coverage{}, fmt.Errorf(
				"parser adapter %s in %s maps to undeclared/non-owned tool %q",
				adapter.Function, adapter.SourceFile, adapter.Tool,
			)
		}
		if prior, duplicate := byTool[adapter.Tool]; duplicate {
			return Coverage{}, fmt.Errorf(
				"scanner tool %q has multiple parser adapters: %s and %s",
				adapter.Tool, prior.Function, adapter.Function,
			)
		}
		if !adapter.HostileTested {
			return Coverage{}, fmt.Errorf(
				"parser adapter %s for %q has no hostile conformance coverage",
				adapter.Function, adapter.Tool,
			)
		}
		if !adapter.ValidTested {
			return Coverage{}, fmt.Errorf(
				"parser adapter %s for %q has no valid finding fixture coverage",
				adapter.Function, adapter.Tool,
			)
		}
		coverage.HostileTestedAdapters++
		coverage.ValidTestedAdapters++
		byTool[adapter.Tool] = adapter
	}
	for name, tool := range policy.Tools {
		if tool.ParserOwned {
			if _, ok := byTool[name]; !ok {
				return Coverage{}, fmt.Errorf("scanner tool %q has no parser adapter", name)
			}
		}
	}
	return coverage, nil
}

func discoverParserAdapters(ctx context.Context, pluginsRoot string) ([]parserAdapter, error) {
	type testCoverage struct {
		hostile bool
		valid   bool
	}
	tests := make(map[string]testCoverage)
	var adapters []parserAdapter
	fset := token.NewFileSet()
	err := filepath.WalkDir(pluginsRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return fmt.Errorf("parse plugin source %s: %w", path, err)
		}
		directory := filepath.Dir(path)
		if strings.HasSuffix(path, "_test.go") {
			ast.Inspect(file, func(node ast.Node) bool {
				identifier, ok := node.(*ast.Ident)
				if !ok || !isParserFunction(identifier.Name) {
					return true
				}
				key := directory + "\x00" + identifier.Name
				value := tests[key]
				if filepath.Base(path) == "parser_conformance_test.go" {
					value.hostile = true
				} else {
					value.valid = true
				}
				tests[key] = value
				return true
			})
			return nil
		}

		toolNames := pluginNames(file)
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv != nil || !isParserFunction(function.Name.Name) {
				continue
			}
			if len(toolNames) != 1 {
				return fmt.Errorf(
					"parser adapter %s in %s requires exactly one literal plugin Name, found %d",
					function.Name.Name, path, len(toolNames),
				)
			}
			adapters = append(adapters, parserAdapter{
				Tool: toolNames[0], Function: function.Name.Name,
				SourceFile: filepath.ToSlash(path),
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for index := range adapters {
		key := filepath.FromSlash(filepath.Dir(adapters[index].SourceFile)) + "\x00" + adapters[index].Function
		// SourceFile may be absolute or relative, but filepath.Dir normalizes
		// it to the same directory form used during the walk.
		coverage := tests[key]
		adapters[index].HostileTested = coverage.hostile
		adapters[index].ValidTested = coverage.valid
	}
	sort.Slice(adapters, func(i, j int) bool {
		if adapters[i].Tool == adapters[j].Tool {
			return adapters[i].Function < adapters[j].Function
		}
		return adapters[i].Tool < adapters[j].Tool
	})
	return adapters, nil
}

func isParserFunction(name string) bool {
	return strings.HasPrefix(name, "parse") && strings.HasSuffix(name, "Output") && len(name) > len("parseOutput")
}

func pluginNames(file *ast.File) []string {
	var names []string
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Recv == nil || function.Name.Name != "Name" || function.Body == nil {
			continue
		}
		for _, statement := range function.Body.List {
			returned, ok := statement.(*ast.ReturnStmt)
			if !ok || len(returned.Results) != 1 {
				continue
			}
			literal, ok := returned.Results[0].(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				continue
			}
			var value string
			if err := json.Unmarshal([]byte(literal.Value), &value); err == nil && value != "" {
				names = append(names, value)
			}
		}
	}
	return names
}

func validateScannerVariants(
	declared map[string]VariantPolicy,
	builds buildPolicy,
) (int, error) {
	if len(declared) != len(builds.Variants) {
		return 0, fmt.Errorf("scanner variant coverage is %d, build policy declares %d", len(declared), len(builds.Variants))
	}
	tuples := 0
	for name, expected := range builds.Variants {
		actual, ok := declared[name]
		if !ok {
			return 0, fmt.Errorf("scanner variant %q has no quality strategy", name)
		}
		if actual.Dockerfile != expected.Dockerfile ||
			!equalStrings(actual.Platforms, expected.Platforms) {
			return 0, fmt.Errorf("scanner variant %q quality matrix differs from build policy", name)
		}
		if err := validateVariantStrategy("scanner", name, actual); err != nil {
			return 0, err
		}
		tuples += len(actual.Platforms)
	}
	return tuples, nil
}

func validateFixerVariants(
	declared map[string]VariantPolicy,
	builds buildPolicy,
) (int, error) {
	if len(declared) != len(builds.FixerVariants) ||
		len(builds.FixerVariants) != len(scannerbuild.FixerVariants) {
		return 0, fmt.Errorf(
			"fixer variant coverage is %d, build policy declares %d, build table declares %d",
			len(declared), len(builds.FixerVariants), len(scannerbuild.FixerVariants),
		)
	}
	tuples := 0
	for _, expected := range scannerbuild.FixerVariants {
		actual, ok := declared[expected.Name]
		if !ok {
			return 0, fmt.Errorf("fixer variant %q has no quality strategy", expected.Name)
		}
		build, ok := builds.FixerVariants[expected.Name]
		if !ok {
			return 0, fmt.Errorf("fixer variant %q has no canonical build policy", expected.Name)
		}
		dockerfile := filepath.ToSlash(filepath.Join(expected.ContextSubdir, expected.Dockerfile))
		if actual.Dockerfile != dockerfile ||
			build.Dockerfile != dockerfile ||
			!equalStrings(actual.Platforms, build.Platforms) {
			return 0, fmt.Errorf("fixer variant %q quality matrix differs from build policy", expected.Name)
		}
		if err := validateVariantStrategy("fixer", expected.Name, actual); err != nil {
			return 0, err
		}
		tuples += len(actual.Platforms)
	}
	return tuples, nil
}

func validateVariantStrategy(kind, name string, policy VariantPolicy) error {
	if len(policy.Platforms) == 0 {
		return fmt.Errorf("%s variant %q has no declared platforms", kind, name)
	}
	if policy.Strategy != "executable" && policy.Strategy != "structural" {
		return fmt.Errorf("%s variant %q has invalid quality strategy %q", kind, name, policy.Strategy)
	}
	if policy.Strategy == "structural" && strings.TrimSpace(policy.Rationale) == "" {
		return fmt.Errorf("%s variant %q structural strategy requires a rationale", kind, name)
	}
	seen := make(map[string]bool, len(policy.Platforms))
	for _, platform := range policy.Platforms {
		if seen[platform] || (platform != "linux/amd64" && platform != "linux/arm64") {
			return fmt.Errorf("%s variant %q has invalid/duplicate platform %q", kind, name, platform)
		}
		seen[platform] = true
	}
	return nil
}

func validateDatabaseLock(policy VulnerabilityDatabasePolicy, lock DBLock, now time.Time) error {
	if lock.SchemaVersion != dbLockSchema || lock.Provider != policy.Provider ||
		lock.Repository != policy.Repository || !validDigest(lock.Digest) {
		return errors.New("vulnerability database identity is missing or mismatched")
	}
	if policy.LockFile != DBLockPath || policy.MaxAgeHours <= 0 {
		return errors.New("vulnerability database policy is incomplete")
	}
	if lock.RecordedAt.IsZero() || lock.ExpiresAt.IsZero() ||
		!lock.ExpiresAt.After(lock.RecordedAt) ||
		lock.ExpiresAt.Sub(lock.RecordedAt) > time.Duration(policy.MaxAgeHours)*time.Hour {
		return errors.New("vulnerability database identity has invalid freshness bounds")
	}
	if now.Before(lock.RecordedAt.Add(-5*time.Minute)) || !now.Before(lock.ExpiresAt) {
		return errors.New("vulnerability database identity is stale")
	}
	return nil
}

func validateGoldenFile(path string, policy Policy, corpus Corpus) (int, error) {
	data, err := readBounded(path)
	if err != nil {
		return 0, err
	}
	var expectations []GoldenExpectation
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&expectations); err != nil {
		return 0, fmt.Errorf("decode quality expectations %s: %w", path, err)
	}
	if len(expectations) == 0 || len(expectations) > maxEvidenceItems {
		return 0, errors.New("quality expectation count is invalid")
	}
	families := make(map[string]bool, len(corpus.Families))
	seen := make(map[string]bool, len(expectations))
	for _, expectation := range expectations {
		tool, ok := policy.Tools[expectation.Tool]
		if !ok || expectation.Family == "" || expectation.Family != tool.Family ||
			expectation.MinimumFindings < 0 {
			return 0, fmt.Errorf("quality expectation is malformed for %q", expectation.Tool)
		}
		if seen[expectation.Tool] {
			return 0, fmt.Errorf("quality expectations duplicate scanner tool %q", expectation.Tool)
		}
		seen[expectation.Tool] = true
		switch expectation.Mode {
		case "real-output":
			if tool.Strategy == "structural" {
				return 0, fmt.Errorf("structural scanner tool %q claims real output", expectation.Tool)
			}
			if expectation.MinimumFindings == 0 && strings.TrimSpace(expectation.Rationale) == "" {
				return 0, fmt.Errorf("quality expectation for %q permits a clean corpus without rationale", expectation.Tool)
			}
		case "structural":
			if tool.Strategy != "structural" || expectation.MinimumFindings != 0 ||
				strings.TrimSpace(expectation.Rationale) == "" {
				return 0, fmt.Errorf("structural quality expectation for %q is invalid", expectation.Tool)
			}
		default:
			return 0, fmt.Errorf("quality expectation for %q has unsupported mode %q", expectation.Tool, expectation.Mode)
		}
		families[tool.Family] = true
	}
	if len(families) != len(corpus.Families) {
		return 0, fmt.Errorf(
			"quality expectations cover %d fixture families, expected %d",
			len(families), len(corpus.Families),
		)
	}
	if _, err := CanonicalGoldenExpectations(expectations); err != nil {
		return 0, err
	}
	return len(expectations), nil
}

func expandFixture(fixture Fixture) ([]byte, error) {
	modes := 0
	if fixture.Content != "" || (fixture.Content == "" && fixture.Base64 == "" && fixture.Repeat == "" && fixture.RepeatCount == 0) {
		modes++
	}
	if fixture.Base64 != "" {
		modes++
	}
	if fixture.Repeat != "" || fixture.RepeatCount != 0 {
		modes++
	}
	if modes != 1 {
		return nil, errors.New("fixture must use exactly one content encoding")
	}
	var value []byte
	var err error
	switch {
	case fixture.Base64 != "":
		value, err = base64.StdEncoding.DecodeString(fixture.Base64)
	case fixture.Repeat != "" || fixture.RepeatCount != 0:
		if fixture.Repeat == "" || fixture.RepeatCount < 1 || fixture.RepeatCount > 1_000_000 {
			return nil, errors.New("fixture repeat bounds are invalid")
		}
		if int64(len(fixture.Repeat))*int64(fixture.RepeatCount) > maxFixtureBytes {
			return nil, errors.New("fixture exceeds expansion bound")
		}
		value = bytes.Repeat([]byte(fixture.Repeat), fixture.RepeatCount)
	default:
		value = []byte(fixture.Content)
	}
	if err != nil {
		return nil, fmt.Errorf("decode base64: %w", err)
	}
	if len(value) > maxFixtureBytes {
		return nil, errors.New("fixture exceeds expansion bound")
	}
	return value, nil
}

func readYAML(path string, target any) error {
	data, err := readBounded(path)
	if err != nil {
		return err
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

func readJSON(path string, target any) error {
	data, err := readBounded(path)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

func readBounded(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxDefinitionBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxDefinitionBytes {
		return nil, fmt.Errorf("%s exceeds %d bytes", path, maxDefinitionBytes)
	}
	return data, nil
}

func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func validDigest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != 71 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	left = append([]string(nil), left...)
	right = append([]string(nil), right...)
	sort.Strings(left)
	sort.Strings(right)
	return strings.Join(left, "\x00") == strings.Join(right, "\x00")
}
