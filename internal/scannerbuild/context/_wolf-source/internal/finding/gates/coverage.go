package gates

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/alphabravocompany/thewolf/internal/scan/coverage"
	"strings"
)

type CoverageRequirement struct {
	Metric     string  `json:"metric"`
	Minimum    float64 `json:"minimum"`
	Scope      string  `json:"scope,omitempty"`
	PathPrefix string  `json:"path_prefix,omitempty"`
}

func (c *CoverageRequirement) UnmarshalJSON(data []byte) error {
	var wire struct {
		Metric     string   `json:"metric"`
		Minimum    *float64 `json:"minimum"`
		Scope      string   `json:"scope,omitempty"`
		PathPrefix string   `json:"path_prefix,omitempty"`
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&wire); err != nil {
		return err
	}
	if wire.Minimum == nil {
		return fmt.Errorf("coverage minimum is required")
	}
	*c = CoverageRequirement{Metric: wire.Metric, Minimum: *wire.Minimum, Scope: wire.Scope, PathPrefix: wire.PathPrefix}
	return nil
}

type CoverageCheck struct {
	RuleID     string   `json:"rule_id"`
	Metric     string   `json:"metric"`
	Minimum    float64  `json:"minimum"`
	Actual     *float64 `json:"actual,omitempty"`
	Status     string   `json:"status"`
	Reason     string   `json:"reason"`
	Scope      string   `json:"scope"`
	PathPrefix string   `json:"path_prefix,omitempty"`
}

// ApplyCoverage uses measured evidence only. Missing metrics and partially
// instrumented bundles cannot satisfy a coverage rule. Scanner incompleteness
// still takes precedence over an otherwise passing coverage check.
func ApplyCoverage(eval *Evaluation, policy Policy, report *coverage.MeasuredReport, commit string) {
	for _, rule := range policy.Rules {
		c := rule.TestCoverage
		if c == nil {
			continue
		}
		check := CoverageCheck{RuleID: rule.ID, Metric: c.Metric, Minimum: c.Minimum, Status: StatusIncomplete}
		check.Scope, check.PathPrefix = c.Scope, c.PathPrefix
		if check.Scope == "" {
			check.Scope = "overall"
		}
		metric, count, measured := coverage.Metric{}, 0, 0
		if report != nil {
			files := report.Files
			if c.Scope == "changed" {
				files = nil
				if report.Changed != nil {
					files = report.Changed.Files
				}
			}
			for _, file := range files {
				if c.PathPrefix != "" && file.Path != c.PathPrefix && !strings.HasPrefix(file.Path, strings.TrimSuffix(c.PathPrefix, "/")+"/") {
					continue
				}
				count++
				if m, ok := file.Metrics[c.Metric]; ok {
					metric.Covered += m.Covered
					metric.Total += m.Total
					measured++
				}
			}
		}
		switch {
		case report == nil:
			check.Reason = "Measured test coverage has not been imported."
		case report.CommitSHA != commit || commit == "":
			check.Reason = "Coverage does not match the scanned commit."
		case c.Scope == "changed" && report.Changed == nil:
			check.Reason = "A diff-cover report for the tested commit has not been imported."
		case measured != count || (count == 0 && c.Scope != "changed"):
			check.Reason = "This metric is not measured for every file in the imported bundle."
		default:
			if metric.Total == 0 {
				check.Status = "not_applicable"
				check.Reason = "No coverable items for this metric in the reported scope."
				break
			}
			actual := metric.Percent()
			check.Actual = &actual
			check.Status = StatusPass
			check.Reason = fmt.Sprintf("%.2f%% %s coverage; %.2f%% required.", actual, c.Metric, c.Minimum)
			if actual < c.Minimum {
				check.Status = rule.Action
			}
		}
		eval.Coverage = append(eval.Coverage, check)
		if check.Status == StatusFail || check.Status == StatusIncomplete || check.Status == StatusWarn {
			if eval.Status == StatusPending || eval.Status == StatusIncomplete {
				continue
			}
			if check.Status != StatusWarn || eval.Status == StatusPass {
				eval.Status, eval.Summary.Status, eval.Reason = check.Status, check.Status, check.Reason
			}
		}
	}
}
