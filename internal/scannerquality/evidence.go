package scannerquality

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	EvidenceSchema   = "wolf.scanners/quality-evidence/v2"
	maxEvidenceBytes = 64 << 20
	maxEvidenceItems = 100_000
)

type Evidence struct {
	SchemaVersion         string          `json:"schemaVersion"`
	GoldenDigest          string          `json:"goldenDigest"`
	VulnerabilityDatabase DBEvidence      `json:"vulnerabilityDatabase"`
	Network               NetworkEvidence `json:"network"`
	Scope                 []string        `json:"scope,omitempty"`
	Stable                []ToolEvidence  `json:"stable"`
	Candidate             []ToolEvidence  `json:"candidate"`
}

type DBEvidence struct {
	Provider   string    `json:"provider"`
	Repository string    `json:"repository"`
	Digest     string    `json:"digest"`
	RecordedAt time.Time `json:"recordedAt"`
}

// NetworkEvidence binds a measured comparison to either a network-disabled
// execution or an inspected, engine-local fixture network. A controlled
// fixture network is required to be Docker-internal and labelled with the
// exact operator-reviewed policy digest before any scanner container runs.
type NetworkEvidence struct {
	Mode         string `json:"mode"`
	Name         string `json:"name,omitempty"`
	ID           string `json:"id,omitempty"`
	PolicyDigest string `json:"policyDigest,omitempty"`
}

type ToolEvidence struct {
	Tool            string    `json:"tool"`
	ExecutionMode   string    `json:"executionMode"`
	ImageReference  string    `json:"imageReference"`
	ImageDigest     string    `json:"imageDigest"`
	OutputKind      string    `json:"outputKind"`
	OutputDigest    string    `json:"outputDigest"`
	RawOutputDigest string    `json:"rawOutputDigest,omitempty"`
	RawOutputBytes  int64     `json:"rawOutputBytes,omitempty"`
	DurationMS      int64     `json:"durationMs"`
	OutputBytes     int64     `json:"outputBytes"`
	PeakMemoryBytes int64     `json:"peakMemoryBytes"`
	ParseErrors     int       `json:"parseErrors"`
	Findings        []Finding `json:"findings"`
}

type Finding struct {
	Tool        string `json:"tool"`
	RuleID      string `json:"ruleId"`
	Severity    string `json:"severity"`
	Path        string `json:"path"`
	Line        int    `json:"line"`
	Message     string `json:"message"`
	Fingerprint string `json:"fingerprint"`
}

func EvaluateEvidence(
	ctx context.Context,
	policy Policy,
	database DBLock,
	evidence Evidence,
	now time.Time,
) error {
	if evidence.SchemaVersion != EvidenceSchema {
		return fmt.Errorf("quality evidence schema %q is unsupported", evidence.SchemaVersion)
	}
	if !digestString(evidence.GoldenDigest) {
		return errors.New("quality evidence has no exact golden corpus identity")
	}
	if evidence.VulnerabilityDatabase.Provider != database.Provider ||
		evidence.VulnerabilityDatabase.Repository != database.Repository ||
		evidence.VulnerabilityDatabase.Digest != database.Digest ||
		evidence.VulnerabilityDatabase.RecordedAt.IsZero() ||
		evidence.VulnerabilityDatabase.RecordedAt.Before(database.RecordedAt) {
		return errors.New("quality evidence vulnerability database identity is missing, stale, or mismatched")
	}
	if err := validateDatabaseLock(policy.VulnerabilityDB, database, now.UTC()); err != nil {
		return err
	}
	if err := validateNetworkEvidence(evidence.Network); err != nil {
		return err
	}
	evaluationPolicy, err := scopedPolicy(policy, evidence.Scope)
	if err != nil {
		return err
	}
	stable, err := indexToolEvidence(evaluationPolicy, evidence.Stable)
	if err != nil {
		return fmt.Errorf("stable evidence: %w", err)
	}
	candidate, err := indexToolEvidence(evaluationPolicy, evidence.Candidate)
	if err != nil {
		return fmt.Errorf("candidate evidence: %w", err)
	}
	for tool, toolPolicy := range evaluationPolicy.Tools {
		if err := ctx.Err(); err != nil {
			return err
		}
		baseline, ok := stable[tool]
		if !ok {
			return fmt.Errorf("stable evidence is missing scanner tool %q", tool)
		}
		proposed, ok := candidate[tool]
		if !ok {
			return fmt.Errorf("candidate evidence is missing scanner tool %q", tool)
		}
		threshold := evaluationPolicy.ThresholdProfiles[toolPolicy.ThresholdProfile]
		if proposed.DurationMS > threshold.DurationMS ||
			proposed.OutputBytes > threshold.OutputBytes ||
			proposed.PeakMemoryBytes > threshold.PeakMemoryBytes ||
			proposed.ParseErrors > threshold.MaxParseErrors ||
			len(proposed.Findings) > threshold.MaxFindings {
			return fmt.Errorf("scanner tool %q exceeds recorded resource threshold", tool)
		}
		if proposed.ParseErrors > baseline.ParseErrors {
			return fmt.Errorf("scanner tool %q introduced parse errors", tool)
		}
		if err := compareFindings(evaluationPolicy, baseline.Findings, proposed.Findings, now); err != nil {
			return fmt.Errorf("scanner tool %q: %w", tool, err)
		}
	}
	return nil
}

// EvaluateGoldenEvidence proves that both immutable images executed the real
// family fixture contract. Finding identities come only from scanner output;
// the expectation file declares whether a family must be non-empty or may be
// clean, and structural tools bind the canonical expectation digest.
func EvaluateGoldenEvidence(
	policy Policy, goldens []GoldenExpectation, goldenDigest string, evidence Evidence,
) error {
	if !digestString(goldenDigest) || evidence.GoldenDigest != goldenDigest {
		return errors.New("quality evidence golden corpus identity is missing or mismatched")
	}
	evaluationPolicy, err := scopedPolicy(policy, evidence.Scope)
	if err != nil {
		return err
	}
	stable, err := indexToolEvidence(evaluationPolicy, evidence.Stable)
	if err != nil {
		return fmt.Errorf("stable expectation evidence: %w", err)
	}
	candidate, err := indexToolEvidence(evaluationPolicy, evidence.Candidate)
	if err != nil {
		return fmt.Errorf("candidate expectation evidence: %w", err)
	}
	byTool := make(map[string]GoldenExpectation)
	for _, golden := range goldens {
		if _, selected := evaluationPolicy.Tools[golden.Tool]; selected {
			byTool[golden.Tool] = golden
		}
	}
	for tool, expected := range byTool {
		toolPolicy := evaluationPolicy.Tools[tool]
		baseline, baselineExists := stable[tool]
		proposed, proposedExists := candidate[tool]
		if !baselineExists || !proposedExists {
			return fmt.Errorf("expectation evidence is missing scanner tool %q", tool)
		}
		if toolPolicy.Strategy == "structural" {
			canonical, canonicalErr := CanonicalGoldenExpectations([]GoldenExpectation{expected})
			if canonicalErr != nil {
				return canonicalErr
			}
			expectedDigest := sha256Bytes(canonical)
			if baseline.OutputDigest != expectedDigest || proposed.OutputDigest != expectedDigest {
				return fmt.Errorf("structural scanner tool %q is not bound to its reviewed expectation", tool)
			}
			continue
		}
		if baseline.ExecutionMode != "executed" || proposed.ExecutionMode != "executed" ||
			baseline.ParseErrors != 0 || proposed.ParseErrors != 0 {
			return fmt.Errorf("scanner tool %q did not produce parser-clean real execution evidence", tool)
		}
		if len(baseline.Findings) < expected.MinimumFindings ||
			len(proposed.Findings) < expected.MinimumFindings {
			return fmt.Errorf(
				"scanner tool %q produced fewer than %d required real fixture findings",
				tool, expected.MinimumFindings,
			)
		}
		if expected.MinimumFindings == 0 &&
			(baseline.OutputBytes == 0 || proposed.OutputBytes == 0 ||
				!digestString(baseline.OutputDigest) || !digestString(proposed.OutputDigest)) {
			return fmt.Errorf("scanner tool %q has no normalized output identity proving its reviewed clean fixture result", tool)
		}
	}
	return nil
}

func sha256Bytes(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func validateNetworkEvidence(evidence NetworkEvidence) error {
	switch evidence.Mode {
	case "none":
		if evidence.Name != "" || evidence.ID != "" || evidence.PolicyDigest != "" {
			return errors.New("network-disabled quality evidence contains a controlled-network identity")
		}
		return nil
	case "controlled-internal":
		if !strings.HasPrefix(evidence.Name, "wolf-quality-") ||
			!hexIdentity(evidence.ID) || !digestString(evidence.PolicyDigest) {
			return errors.New("controlled quality network evidence is incomplete or invalid")
		}
		return nil
	default:
		return fmt.Errorf("quality evidence network mode %q is unsupported", evidence.Mode)
	}
}

func hexIdentity(value string) bool {
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

func scopedPolicy(policy Policy, scope []string) (Policy, error) {
	if len(scope) == 0 {
		return policy, nil
	}
	selected := make(map[string]ToolPolicy, len(scope))
	for _, tool := range scope {
		definition, exists := policy.Tools[tool]
		if !exists || tool == "" {
			return Policy{}, fmt.Errorf("quality evidence scope contains unknown scanner tool %q", tool)
		}
		if _, duplicate := selected[tool]; duplicate {
			return Policy{}, fmt.Errorf("quality evidence scope duplicates scanner tool %q", tool)
		}
		selected[tool] = definition
	}
	policy.Tools = selected
	policy.ExpectedToolCount = len(selected)
	return policy, nil
}

func LoadEvidence(path string) (Evidence, error) {
	file, err := os.Open(path)
	if err != nil {
		return Evidence{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxEvidenceBytes+1))
	if err != nil {
		return Evidence{}, err
	}
	if len(data) > maxEvidenceBytes {
		return Evidence{}, errors.New("quality evidence exceeds size bound")
	}
	var evidence Evidence
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&evidence); err != nil {
		return Evidence{}, err
	}
	return evidence, nil
}

func CanonicalFindings(findings []Finding) ([]byte, error) {
	if len(findings) > maxEvidenceItems {
		return nil, errors.New("finding set exceeds item bound")
	}
	findings = append([]Finding(nil), findings...)
	sort.Slice(findings, func(i, j int) bool {
		left, right := findings[i], findings[j]
		return findingSortKey(left) < findingSortKey(right)
	})
	return json.MarshalIndent(findings, "", "  ")
}

func CanonicalGoldenExpectations(expectations []GoldenExpectation) ([]byte, error) {
	if len(expectations) > maxEvidenceItems {
		return nil, errors.New("quality expectation set exceeds item bound")
	}
	expectations = append([]GoldenExpectation(nil), expectations...)
	sort.Slice(expectations, func(i, j int) bool {
		return expectations[i].Tool < expectations[j].Tool
	})
	return json.MarshalIndent(expectations, "", "  ")
}

func indexToolEvidence(policy Policy, values []ToolEvidence) (map[string]ToolEvidence, error) {
	if len(values) > len(policy.Tools) {
		return nil, errors.New("tool evidence exceeds registered tool count")
	}
	indexed := make(map[string]ToolEvidence, len(values))
	totalFindings := 0
	for _, value := range values {
		toolPolicy, ok := policy.Tools[value.Tool]
		if !ok {
			return nil, fmt.Errorf("unknown scanner tool %q", value.Tool)
		}
		if _, duplicate := indexed[value.Tool]; duplicate {
			return nil, fmt.Errorf("duplicate scanner tool %q", value.Tool)
		}
		if value.DurationMS < 0 || value.OutputBytes < 0 || value.RawOutputBytes < 0 ||
			value.PeakMemoryBytes < 0 || value.ParseErrors < 0 {
			return nil, fmt.Errorf("scanner tool %q has negative evidence values", value.Tool)
		}
		if !exactImageEvidence(value.ImageReference, value.ImageDigest) ||
			!digestString(value.OutputDigest) {
			return nil, fmt.Errorf("scanner tool %q has no exact image/output identity", value.Tool)
		}
		if (value.RawOutputDigest == "") != (value.RawOutputBytes == 0) ||
			(value.RawOutputDigest != "" && !digestString(value.RawOutputDigest)) {
			return nil, fmt.Errorf("scanner tool %q has incomplete raw-output identity", value.Tool)
		}
		switch value.ExecutionMode {
		case "executed":
			if toolPolicy.Strategy == "structural" || value.OutputKind != "normalized-findings" ||
				value.DurationMS <= 0 || value.OutputBytes <= 0 || value.PeakMemoryBytes <= 0 {
				return nil, fmt.Errorf("scanner tool %q has incomplete execution measurements", value.Tool)
			}
			if len(value.Findings) == 0 && toolPolicy.AllowEmptyFindings && value.RawOutputDigest == "" {
				return nil, fmt.Errorf("scanner tool %q has no raw artifact proving its artifact-only result", value.Tool)
			}
		case "structural":
			if toolPolicy.Strategy != "structural" || value.OutputKind != "structural-manifest" ||
				value.DurationMS != 0 || value.PeakMemoryBytes != 0 || len(value.Findings) != 0 ||
				value.RawOutputDigest != "" {
				return nil, fmt.Errorf("scanner tool %q structural evidence claims execution", value.Tool)
			}
		default:
			return nil, fmt.Errorf("scanner tool %q has invalid execution mode", value.Tool)
		}
		totalFindings += len(value.Findings)
		if totalFindings > maxEvidenceItems {
			return nil, errors.New("finding evidence exceeds item bound")
		}
		for index := range value.Findings {
			if value.Findings[index].Tool == "" {
				value.Findings[index].Tool = value.Tool
			}
			if value.Findings[index].Tool != value.Tool ||
				value.Findings[index].RuleID == "" ||
				value.Findings[index].Fingerprint == "" ||
				value.Findings[index].Line < 0 {
				return nil, fmt.Errorf("scanner tool %q has malformed normalized finding", value.Tool)
			}
		}
		indexed[value.Tool] = value
	}
	return indexed, nil
}

func exactImageEvidence(reference, imageDigest string) bool {
	return digestString(imageDigest) && strings.Contains(reference, "@"+imageDigest) &&
		!strings.ContainsAny(reference, " \t\r\n")
}

func digestString(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, char := range strings.TrimPrefix(value, "sha256:") {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func compareFindings(
	policy Policy,
	stable, candidate []Finding,
	now time.Time,
) error {
	candidateByIdentity := make(map[string]Finding, len(candidate))
	for _, finding := range candidate {
		candidateByIdentity[findingIdentity(finding)] = finding
	}
	for _, expected := range stable {
		actual, ok := candidateByIdentity[findingIdentity(expected)]
		if !ok {
			if tolerated(policy, expected, "finding-loss", now) {
				continue
			}
			return fmt.Errorf("unacceptable finding loss for %s", findingIdentity(expected))
		}
		if actual.Severity != expected.Severity &&
			!tolerated(policy, expected, "severity-drift", now) {
			return fmt.Errorf("severity drift for %s", findingIdentity(expected))
		}
		if (actual.Path != expected.Path || actual.Line != expected.Line) &&
			!tolerated(policy, expected, "location-drift", now) {
			return fmt.Errorf("location drift for %s", findingIdentity(expected))
		}
	}
	return nil
}

func tolerated(policy Policy, finding Finding, kind string, now time.Time) bool {
	for _, delta := range policy.ToleratedDeltas {
		if delta.Tool == finding.Tool && delta.RuleID == finding.RuleID &&
			delta.Kind == kind && delta.ExpiresAt.After(now) &&
			strings.TrimSpace(delta.Reason) != "" {
			return true
		}
	}
	return false
}

func findingIdentity(finding Finding) string {
	return strings.Join([]string{finding.Tool, finding.RuleID, finding.Fingerprint}, "\x00")
}

func findingSortKey(finding Finding) string {
	return strings.Join([]string{
		finding.Tool, finding.RuleID, finding.Path,
		fmt.Sprintf("%012d", finding.Line), finding.Severity,
		finding.Fingerprint, finding.Message,
	}, "\x00")
}
