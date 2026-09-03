package routes

import (
	"testing"
)

func TestValidateScanRequestSelectorsAcceptsFastProfiles(t *testing.T) {
	h := &Handler{}
	for _, profile := range []string{"", "standard", "full", "targeted", "fast", "pr", "release", "FAST", " Pr "} {
		req := createScanRequest{Profile: profile}
		if profile == "targeted" {
			req.Tools = []string{}
			req.IncludePaths = []string{"src/**"}
		}
		if err := validateScanRequestSelectors(h, &req); err != nil {
			t.Fatalf("profile %q: %v", profile, err)
		}
	}
	if err := validateScanRequestSelectors(h, &createScanRequest{Profile: "deep"}); err == nil {
		t.Fatal("expected rejection of profile deep")
	} else if err.Error() != "profile must be standard, full, targeted, fast, pr, or release" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestApplyScanProfileDisablesHeavyToolsForFast(t *testing.T) {
	req := &createScanRequest{Profile: "fast"}
	applyScanProfile(&Handler{}, req, "")
	if !containsStr(req.DisabledTools, "bearer") {
		t.Fatalf("fast profile DisabledTools = %#v, want bearer", req.DisabledTools)
	}

	explicit := &createScanRequest{Profile: "fast", Tools: []string{"semgrep"}}
	applyScanProfile(&Handler{}, explicit, "")
	if len(explicit.DisabledTools) != 0 {
		t.Fatalf("explicit tools DisabledTools = %#v, want none", explicit.DisabledTools)
	}
}
