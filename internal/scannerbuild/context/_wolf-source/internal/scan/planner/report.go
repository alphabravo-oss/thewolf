package planner

import "github.com/alphabravocompany/thewolf/internal/scan/report"

// ToReportPlan converts a planner result into the stable artifact schema used
// by manifest.json.
func ToReportPlan(result Result) *report.ScannerPlan {
	return &report.ScannerPlan{
		Run:  toReportDecisions(result.Run),
		Skip: toReportDecisions(result.Skip),
		Summary: report.ScannerPlanSummary{
			RunCount:      result.Summary.RunCount,
			SkipCount:     result.Summary.SkipCount,
			LanguageCount: result.Summary.LanguageCount,
			ExplicitTools: append([]string(nil), result.Summary.ExplicitTools...),
			DisabledTools: append([]string(nil), result.Summary.DisabledTools...),
			AllScanners:   result.Summary.AllScanners,
		},
	}
}

func toReportDecisions(values []Decision) []report.ScannerPlanDecision {
	if len(values) == 0 {
		return nil
	}
	out := make([]report.ScannerPlanDecision, 0, len(values))
	for _, value := range values {
		out = append(out, report.ScannerPlanDecision{
			Tool:            value.Tool,
			DisplayName:     value.DisplayName,
			Category:        value.Category,
			Languages:       append([]string(nil), value.Languages...),
			Selected:        value.Selected,
			Available:       value.Available,
			ReasonCode:      value.ReasonCode,
			Reason:          value.Reason,
			IntegrationTier: value.IntegrationTier,
			Bucket:          value.Bucket,
			Image:           value.Image,
			ResourceClass:   value.ResourceClass,
			DefaultTimeout:  value.DefaultTimeout,
			NetworkRequired: value.NetworkRequired,
			Exclusive:       value.Exclusive,
			PathScope:       value.PathScope,
		})
	}
	return out
}
