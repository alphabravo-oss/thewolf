package remediate

import "testing"

// LoadConfig's defaults are the whole point of the fail-closed design: an
// unset environment must leave remediation off and yolo mode unavailable.
func TestLoadConfigDefaultsAreFailClosed(t *testing.T) {
	t.Setenv("WOLF_REMEDIATE_ENABLED", "")
	t.Setenv("WOLF_REMEDIATE_ALLOW_YOLO", "")

	cfg := LoadConfig()
	if cfg.Enabled {
		t.Error("Enabled defaults to true, want false")
	}
	if cfg.AllowYolo {
		t.Error("AllowYolo defaults to true, want false")
	}
}

func TestClampTurnsBoundsToCeiling(t *testing.T) {
	cfg := Config{MaxTurns: 20, MaxTurnsCeiling: 100}

	if got := cfg.ClampTurns(150); got != 100 {
		t.Errorf("ClampTurns(150) = %d, want 100 (the admin ceiling)", got)
	}
	if got := cfg.ClampTurns(0); got != 20 {
		t.Errorf("ClampTurns(0) = %d, want 20 (the default, for an unspecified request)", got)
	}
}
