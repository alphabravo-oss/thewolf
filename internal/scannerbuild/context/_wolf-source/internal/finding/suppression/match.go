// Package suppression matches durable server-side suppressions against
// normalized findings.
package suppression

import (
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
	scansuppress "github.com/alphabravocompany/thewolf/internal/scan/suppress"
)

type Match struct {
	Suppression models.FindingSuppression
	Reason      string
}

func FirstMatch(suppressions []models.FindingSuppression, f models.Finding, branch string, now time.Time) (Match, bool) {
	for _, s := range suppressions {
		if Matches(s, f, branch, now) {
			return Match{
				Suppression: s,
				Reason:      "suppression:" + s.ID + ":" + s.Reason,
			}, true
		}
	}
	return Match{}, false
}

func Matches(s models.FindingSuppression, f models.Finding, branch string, now time.Time) bool {
	if s.Status != models.SuppressionStatusActive {
		return false
	}
	if s.ExpiresAt != nil && !s.ExpiresAt.After(now) {
		return false
	}
	if s.Branch != "" && branch != "" && s.Branch != branch {
		return false
	}

	switch s.ScopeType {
	case models.SuppressionScopeFingerprint:
		return s.ScopeValue == f.Fingerprint || s.ScopeValue == f.StableFingerprint
	case models.SuppressionScopeStableFingerprint:
		return s.ScopeValue == f.StableFingerprint
	case models.SuppressionScopeRule:
		return s.ScopeValue == f.RuleID
	case models.SuppressionScopeFineCategory:
		return s.ScopeValue == f.FineCategory
	case models.SuppressionScopePathGlob:
		rs := scansuppress.RuleSet{Rules: []scansuppress.Rule{{PathGlob: s.ScopeValue}}}
		_, ok := rs.Match(f)
		return ok
	default:
		return false
	}
}

func Apply(findings []models.Finding, suppressions []models.FindingSuppression, branches map[string]string, now time.Time) ([]models.Finding, int) {
	count := 0
	for i := range findings {
		if findings[i].Suppressed {
			continue
		}
		branch := ""
		if branches != nil {
			branch = branches[findings[i].ScanID]
		}
		if match, ok := FirstMatch(suppressions, findings[i], branch, now); ok {
			findings[i].Suppressed = true
			findings[i].SuppressionID = match.Suppression.ID
			findings[i].SuppressedReason = match.Reason
			count++
		}
	}
	return findings, count
}
