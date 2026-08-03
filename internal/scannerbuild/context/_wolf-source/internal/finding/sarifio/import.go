// Package sarifio imports SARIF into Wolf's normalized finding model.
package sarifio

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/finding/identity"
	"github.com/alphabravocompany/thewolf/internal/models"
)

const (
	MaxImportBytes   = 10 << 20
	MaxImportResults = 10000
)

type ImportResult struct {
	Findings    []models.Finding `json:"findings"`
	ResultCount int              `json:"result_count"`
}

type logFile struct {
	Schema  string    `json:"$schema"`
	Version string    `json:"version"`
	Runs    []runFile `json:"runs"`
}

type runFile struct {
	Tool struct {
		Driver struct {
			Name  string     `json:"name"`
			Rules []ruleFile `json:"rules"`
		} `json:"driver"`
	} `json:"tool"`
	Results []resultFile `json:"results"`
}

type ruleFile struct {
	ID               string                 `json:"id"`
	ShortDescription messageFile            `json:"shortDescription"`
	Properties       map[string]interface{} `json:"properties"`
}

type resultFile struct {
	RuleID              string                 `json:"ruleId"`
	Level               string                 `json:"level"`
	Message             messageFile            `json:"message"`
	Locations           []locationFile         `json:"locations"`
	PartialFingerprints map[string]string      `json:"partialFingerprints"`
	Properties          map[string]interface{} `json:"properties"`
	Suppressions        []suppressionFile      `json:"suppressions"`
}

type messageFile struct {
	Text string `json:"text"`
}

type locationFile struct {
	PhysicalLocation struct {
		ArtifactLocation struct {
			URI string `json:"uri"`
		} `json:"artifactLocation"`
		Region struct {
			StartLine int `json:"startLine"`
			EndLine   int `json:"endLine"`
		} `json:"region"`
	} `json:"physicalLocation"`
}

type suppressionFile struct {
	Justification messageFile            `json:"justification"`
	Properties    map[string]interface{} `json:"properties"`
}

func Import(data []byte) (ImportResult, error) {
	if len(data) > MaxImportBytes {
		return ImportResult{}, fmt.Errorf("SARIF exceeds maximum size of %d bytes", MaxImportBytes)
	}
	var log logFile
	if err := json.Unmarshal(data, &log); err != nil {
		return ImportResult{}, fmt.Errorf("parse SARIF: %w", err)
	}
	if log.Version != "" && log.Version != "2.1.0" {
		return ImportResult{}, fmt.Errorf("unsupported SARIF version %q", log.Version)
	}
	if len(log.Runs) == 0 {
		return ImportResult{}, fmt.Errorf("SARIF contains no runs")
	}

	var out ImportResult
	for _, run := range log.Runs {
		tool := strings.TrimSpace(run.Tool.Driver.Name)
		if tool == "" {
			tool = "sarif"
		}
		rules := map[string]ruleFile{}
		for _, rule := range run.Tool.Driver.Rules {
			rules[rule.ID] = rule
		}
		for _, result := range run.Results {
			if out.ResultCount >= MaxImportResults {
				return ImportResult{}, fmt.Errorf("SARIF exceeds maximum result count of %d", MaxImportResults)
			}
			out.ResultCount++
			finding := findingFromResult(tool, rules[result.RuleID], result)
			identity.Apply(&finding)
			out.Findings = append(out.Findings, finding)
		}
	}
	return out, nil
}

func findingFromResult(tool string, rule ruleFile, result resultFile) models.Finding {
	title := rule.ShortDescription.Text
	if title == "" {
		title = result.RuleID
	}
	if title == "" {
		title = result.Message.Text
	}
	category := models.CategorySAST
	if value := stringProp(result.Properties, "category"); value != "" {
		category = models.Category(value)
	} else if value := stringProp(rule.Properties, "category"); value != "" {
		category = models.Category(value)
	}

	finding := models.Finding{
		ToolName:         tool,
		Category:         category,
		Severity:         severityFromSARIF(result),
		Title:            title,
		Description:      result.Message.Text,
		RuleID:           result.RuleID,
		CWEID:            stringProp(rule.Properties, "cweId"),
		Status:           models.StatusOpen,
		SARIFData:        findingSARIFData(result),
		FineCategory:     firstString(stringProp(result.Properties, "fineCategory"), stringProp(rule.Properties, "fineCategory")),
		FixStrategyID:    firstString(stringProp(result.Properties, "fixStrategyId"), stringProp(rule.Properties, "fixStrategyId")),
		Confidence:       stringProp(result.Properties, "confidence"),
		BaselineState:    stringProp(result.Properties, "baselineState"),
		SuppressionID:    stringProp(result.Properties, "suppressionId"),
		SuppressedReason: stringProp(result.Properties, "suppressedReason"),
		SourceKind:       stringProp(result.Properties, "sourceKind"),
		SourceRef:        stringProp(result.Properties, "sourceRef"),
	}
	if result.Description() != "" {
		finding.Description = result.Description()
	}
	if finding.RuleID == "" {
		finding.RuleID = rule.ID
	}
	applyLocation(&finding, result)
	applyFingerprints(&finding, result)
	applySuppression(&finding, result)
	return finding
}

func findingSARIFData(result resultFile) string {
	data, err := json.Marshal(result)
	if err != nil {
		return ""
	}
	return string(data)
}

func (r resultFile) Description() string {
	return strings.TrimSpace(r.Message.Text)
}

func applyLocation(f *models.Finding, result resultFile) {
	if len(result.Locations) == 0 {
		return
	}
	loc := result.Locations[0].PhysicalLocation
	f.FilePath = loc.ArtifactLocation.URI
	f.LineStart = loc.Region.StartLine
	f.LineEnd = loc.Region.EndLine
}

func applyFingerprints(f *models.Finding, result resultFile) {
	f.StableFingerprint = firstString(result.PartialFingerprints["wolfStableFingerprint"], stringProp(result.Properties, "stableFingerprint"))
	f.LocationFingerprint = firstString(result.PartialFingerprints["wolfLocationFingerprint"], stringProp(result.Properties, "locationFingerprint"))
	f.SemanticFingerprint = firstString(result.PartialFingerprints["wolfSemanticFingerprint"], stringProp(result.Properties, "semanticFingerprint"))
	f.EvidenceFingerprint = firstString(result.PartialFingerprints["wolfEvidenceFingerprint"], stringProp(result.Properties, "evidenceFingerprint"))
	f.Fingerprint = firstString(result.PartialFingerprints["wolfLegacyFingerprint"], stringProp(result.Properties, "wolfFingerprint"), f.StableFingerprint)
	if version := intProp(result.Properties, "identityVersion"); version > 0 {
		f.IdentityVersion = version
	}
}

func applySuppression(f *models.Finding, result resultFile) {
	if boolProp(result.Properties, "suppressed") || len(result.Suppressions) > 0 {
		f.Suppressed = true
	}
	if f.SuppressedReason == "" && len(result.Suppressions) > 0 {
		f.SuppressedReason = result.Suppressions[0].Justification.Text
	}
	if f.SuppressionID == "" && len(result.Suppressions) > 0 {
		f.SuppressionID = stringProp(result.Suppressions[0].Properties, "wolfSuppressionId")
	}
}

func severityFromSARIF(result resultFile) models.Severity {
	if value := stringProp(result.Properties, "severity"); value != "" {
		return models.Severity(value)
	}
	switch strings.ToLower(result.Level) {
	case "error":
		return models.SeverityHigh
	case "warning":
		return models.SeverityMedium
	case "note":
		return models.SeverityLow
	default:
		return models.SeverityInfo
	}
}

func stringProp(props map[string]interface{}, key string) string {
	if props == nil {
		return ""
	}
	value, _ := props[key].(string)
	return strings.TrimSpace(value)
}

func boolProp(props map[string]interface{}, key string) bool {
	if props == nil {
		return false
	}
	value, _ := props[key].(bool)
	return value
}

func intProp(props map[string]interface{}, key string) int {
	if props == nil {
		return 0
	}
	switch value := props[key].(type) {
	case float64:
		return int(value)
	case int:
		return value
	default:
		return 0
	}
}

func firstString(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
