package scorer

import (
	"math"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

// almostEqual checks whether two float64 values are within a small epsilon.
func almostEqual(a, b, epsilon float64) bool {
	return math.Abs(a-b) < epsilon
}

const eps = 1e-9

// ---------- SeverityScore ----------

func TestSeverityScore_AllLevels(t *testing.T) {
	tests := []struct {
		severity models.Severity
		want     float64
	}{
		{models.SeverityCritical, 10},
		{models.SeverityHigh, 8},
		{models.SeverityMedium, 5},
		{models.SeverityLow, 2},
		{models.SeverityInfo, 1},
	}

	for _, tt := range tests {
		t.Run(string(tt.severity), func(t *testing.T) {
			got := SeverityScore(tt.severity)
			if got != tt.want {
				t.Errorf("SeverityScore(%q) = %v, want %v", tt.severity, got, tt.want)
			}
		})
	}
}

func TestSeverityScore_Unknown(t *testing.T) {
	got := SeverityScore(models.Severity("unknown"))
	if got != 0 {
		t.Errorf("SeverityScore(unknown) = %v, want 0", got)
	}
}

// ---------- LocationWeight ----------

func TestLocationWeight_HighPatterns(t *testing.T) {
	paths := []string{
		"src/auth/login.go",
		"internal/payment/stripe.go",
		"pkg/billing/invoice.go",
		"api/routes/v1.go",
		"server/middleware/cors.go",
		"lib/security/tls.go",
		"internal/crypto/hash.go",
	}
	for _, p := range paths {
		t.Run(p, func(t *testing.T) {
			got := LocationWeight(p)
			if got != 3.0 {
				t.Errorf("LocationWeight(%q) = %v, want 3.0", p, got)
			}
		})
	}
}

func TestLocationWeight_MediumPatterns(t *testing.T) {
	paths := []string{
		"app/controller/user.go",
		"internal/service/order.go",
		"pkg/handler/http.go",
		"db/model/account.go",
		"lib/core/engine.go",
		"src/domain/entity.go",
	}
	for _, p := range paths {
		t.Run(p, func(t *testing.T) {
			got := LocationWeight(p)
			if got != 2.0 {
				t.Errorf("LocationWeight(%q) = %v, want 2.0", p, got)
			}
		})
	}
}

func TestLocationWeight_LowPatterns(t *testing.T) {
	paths := []string{
		"vendor/github.com/lib/pq/conn.go",
		"node_modules/express/index.js",
		"pkg/generated/proto.go",
		"test/mock/client.go",
		"test/fixture/data.json",
		"testdata/sample.yaml",
	}
	for _, p := range paths {
		t.Run(p, func(t *testing.T) {
			got := LocationWeight(p)
			if got != 0.5 {
				t.Errorf("LocationWeight(%q) = %v, want 0.5", p, got)
			}
		})
	}
}

func TestLocationWeight_Default(t *testing.T) {
	paths := []string{
		"src/utils/helpers.go",
		"lib/config/config.go",
		"main.go",
		"README.md",
	}
	for _, p := range paths {
		t.Run(p, func(t *testing.T) {
			got := LocationWeight(p)
			if got != 1.0 {
				t.Errorf("LocationWeight(%q) = %v, want 1.0", p, got)
			}
		})
	}
}

func TestLocationWeight_CaseInsensitive(t *testing.T) {
	got := LocationWeight("src/Auth/Login.go")
	if got != 3.0 {
		t.Errorf("LocationWeight(mixed case auth) = %v, want 3.0", got)
	}
}

func TestLocationWeight_HighPrecedenceOverMedium(t *testing.T) {
	// "auth" (3x) should win over "service" (2x) when both appear.
	got := LocationWeight("internal/auth/service/token.go")
	if got != 3.0 {
		t.Errorf("LocationWeight(auth+service) = %v, want 3.0 (high takes precedence)", got)
	}
}

// ---------- CompositeScore ----------

func TestCompositeScore_Maximum(t *testing.T) {
	// 10 * 3 * 10 = 300 -> 100
	got := CompositeScore(10, 3.0, 10)
	if !almostEqual(got, 100.0, eps) {
		t.Errorf("CompositeScore(10, 3, 10) = %v, want 100", got)
	}
}

func TestCompositeScore_Minimum(t *testing.T) {
	// 1 * 0.5 * 0 = 0 -> 0
	got := CompositeScore(1, 0.5, 0)
	if !almostEqual(got, 0.0, eps) {
		t.Errorf("CompositeScore(1, 0.5, 0) = %v, want 0", got)
	}
}

func TestCompositeScore_MidRange(t *testing.T) {
	// 5 * 1 * 5 = 25 -> 25/300*100 = 8.333...
	got := CompositeScore(5, 1.0, 5)
	want := 25.0 / 300.0 * 100.0
	if !almostEqual(got, want, eps) {
		t.Errorf("CompositeScore(5, 1, 5) = %v, want %v", got, want)
	}
}

func TestCompositeScore_ClampNegative(t *testing.T) {
	got := CompositeScore(-1, 1, 5)
	if got != 0 {
		t.Errorf("CompositeScore(-1, 1, 5) = %v, want 0 (clamped)", got)
	}
}

func TestCompositeScore_ClampAbove100(t *testing.T) {
	// Force an impossible raw value above 300.
	got := CompositeScore(20, 3, 10)
	if got != 100 {
		t.Errorf("CompositeScore(20, 3, 10) = %v, want 100 (clamped)", got)
	}
}

// ---------- ScoreFinding ----------

func TestScoreFinding_FillsAllFields(t *testing.T) {
	f := &models.Finding{
		Severity: models.SeverityCritical,
		FilePath: "src/auth/login.go",
	}

	result := ScoreFinding(f)

	if result != f {
		t.Error("ScoreFinding should return the same pointer")
	}
	if f.ToolSeverityScore != 10 {
		t.Errorf("ToolSeverityScore = %v, want 10", f.ToolSeverityScore)
	}
	if f.LocationWeight != 3.0 {
		t.Errorf("LocationWeight = %v, want 3.0", f.LocationWeight)
	}
	if f.AIContextScore != 5.0 {
		t.Errorf("AIContextScore = %v, want 5.0 (default)", f.AIContextScore)
	}
	// 10 * 3 * 5 = 150 -> 150/300*100 = 50
	if !almostEqual(f.CompositeScore, 50.0, eps) {
		t.Errorf("CompositeScore = %v, want 50", f.CompositeScore)
	}
}

func TestScoreFinding_PreservesExistingAIContextScore(t *testing.T) {
	f := &models.Finding{
		Severity:       models.SeverityHigh,
		FilePath:       "vendor/lib/thing.go",
		AIContextScore: 8,
	}

	ScoreFinding(f)

	if f.AIContextScore != 8 {
		t.Errorf("AIContextScore = %v, want 8 (should not override)", f.AIContextScore)
	}
	// 8 * 0.5 * 8 = 32 -> 32/300*100 = 10.666...
	want := 32.0 / 300.0 * 100.0
	if !almostEqual(f.CompositeScore, want, eps) {
		t.Errorf("CompositeScore = %v, want %v", f.CompositeScore, want)
	}
}

func TestScoreFinding_DefaultPath(t *testing.T) {
	f := &models.Finding{
		Severity:       models.SeverityLow,
		FilePath:       "src/utils/helpers.go",
		AIContextScore: 3,
	}

	ScoreFinding(f)

	if f.LocationWeight != 1.0 {
		t.Errorf("LocationWeight = %v, want 1.0", f.LocationWeight)
	}
	// 2 * 1 * 3 = 6 -> 6/300*100 = 2
	if !almostEqual(f.CompositeScore, 2.0, eps) {
		t.Errorf("CompositeScore = %v, want 2.0", f.CompositeScore)
	}
}

// ---------- ScoreFindings ----------

func TestScoreFindings_ScoresAll(t *testing.T) {
	findings := []models.Finding{
		{Severity: models.SeverityCritical, FilePath: "src/auth/login.go"},
		{Severity: models.SeverityInfo, FilePath: "testdata/sample.yaml"},
		{Severity: models.SeverityMedium, FilePath: "internal/service/order.go", AIContextScore: 7},
	}

	scored := ScoreFindings(findings)

	if len(scored) != 3 {
		t.Fatalf("len(scored) = %d, want 3", len(scored))
	}

	// First: critical + auth (3x) + default AI (5) -> 10*3*5=150 -> 50
	if !almostEqual(scored[0].CompositeScore, 50.0, eps) {
		t.Errorf("[0] CompositeScore = %v, want 50", scored[0].CompositeScore)
	}

	// Second: info + testdata (0.5x) + default AI (5) -> 1*0.5*5=2.5 -> 2.5/300*100 = 0.833...
	want1 := 2.5 / 300.0 * 100.0
	if !almostEqual(scored[1].CompositeScore, want1, eps) {
		t.Errorf("[1] CompositeScore = %v, want %v", scored[1].CompositeScore, want1)
	}

	// Third: medium + service (2x) + AI 7 -> 5*2*7=70 -> 70/300*100 = 23.333...
	want2 := 70.0 / 300.0 * 100.0
	if !almostEqual(scored[2].CompositeScore, want2, eps) {
		t.Errorf("[2] CompositeScore = %v, want %v", scored[2].CompositeScore, want2)
	}
}
