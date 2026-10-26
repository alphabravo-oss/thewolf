package suppression

import (
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestMatchesScopesAndExpiration(t *testing.T) {
	now := time.Now().UTC()
	finding := models.Finding{
		Fingerprint:       "legacy-fp",
		StableFingerprint: "stable-fp",
		RuleID:            "G201",
		FineCategory:      "sql-injection",
		FilePath:          "app/db.go",
	}

	cases := []struct {
		name string
		s    models.FindingSuppression
		want bool
	}{
		{
			name: "stable fingerprint",
			s:    suppression(models.SuppressionScopeStableFingerprint, "stable-fp"),
			want: true,
		},
		{
			name: "rule",
			s:    suppression(models.SuppressionScopeRule, "G201"),
			want: true,
		},
		{
			name: "fine category",
			s:    suppression(models.SuppressionScopeFineCategory, "sql-injection"),
			want: true,
		},
		{
			name: "path glob",
			s:    suppression(models.SuppressionScopePathGlob, "**/db.go"),
			want: true,
		},
		{
			name: "expired",
			s: func() models.FindingSuppression {
				expired := now.Add(-time.Hour)
				s := suppression(models.SuppressionScopeRule, "G201")
				s.ExpiresAt = &expired
				return s
			}(),
			want: false,
		},
		{
			name: "revoked",
			s: func() models.FindingSuppression {
				s := suppression(models.SuppressionScopeRule, "G201")
				s.Status = models.SuppressionStatusRevoked
				return s
			}(),
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Matches(tc.s, finding, "", now); got != tc.want {
				t.Fatalf("Matches() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestApplySetsSuppressionFields(t *testing.T) {
	findings := []models.Finding{{ID: "f1", StableFingerprint: "stable-fp"}}
	suppressions := []models.FindingSuppression{
		{
			ID:         "sup-1",
			Status:     models.SuppressionStatusActive,
			ScopeType:  models.SuppressionScopeStableFingerprint,
			ScopeValue: "stable-fp",
			Reason:     "accepted risk",
		},
	}

	out, count := Apply(findings, suppressions, nil, time.Now().UTC())
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
	if !out[0].Suppressed || out[0].SuppressionID != "sup-1" {
		t.Fatalf("finding not marked suppressed: %+v", out[0])
	}
}

func suppression(scope models.SuppressionScopeType, value string) models.FindingSuppression {
	return models.FindingSuppression{
		ID:         "sup",
		Status:     models.SuppressionStatusActive,
		ScopeType:  scope,
		ScopeValue: value,
		Reason:     "test",
	}
}

func TestSuppressionScopeAndReevaluation(t *testing.T) {
	now := time.Now().UTC()
	rule := suppression(models.SuppressionScopeRule, "rule")
	rule.RepoID, rule.Branch = "repo", "main"
	f := models.Finding{RepoID: "repo", ScanID: "scan", RuleID: "rule"}
	if Matches(rule, f, "", now) {
		t.Fatal("unknown branch matched a branch-scoped suppression")
	}
	other := f
	other.RepoID = "other"
	if Matches(rule, other, "main", now) {
		t.Fatal("cross-repository suppression matched")
	}
	f.Suppressed, f.SuppressionID, f.SuppressedReason = true, rule.ID, "old"
	rule.Status = models.SuppressionStatusRevoked
	out, _ := Apply([]models.Finding{f}, []models.FindingSuppression{rule}, map[string]string{"scan": "main"}, now)
	if out[0].Suppressed || out[0].SuppressionID != "" {
		t.Fatal("revoked suppression stayed attached")
	}
}
