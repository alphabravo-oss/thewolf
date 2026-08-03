package routes

import (
	"testing"

	scannercontainer "github.com/alphabravocompany/thewolf/internal/plugin/container"
	"github.com/alphabravocompany/thewolf/internal/scan/report"
)

func TestApplyContainerImagesToScannerPlanRecordsCompatibilityRuntime(t *testing.T) {
	defaultRef := "registry.example/wolf-scanners@sha256:" + repeatHex("a")
	jvmRef := "registry.example/wolf-scanners-jvm@sha256:" + repeatHex("b")
	upstreamRef := "registry.example/trivy@sha256:" + repeatHex("c")
	plan := &report.ScannerPlan{
		Run: []report.ScannerPlanDecision{
			{Tool: "bandit"}, {Tool: "pmd"}, {Tool: "trivy"},
		},
		Skip: []report.ScannerPlanDecision{{Tool: "ruff"}},
	}
	config := &scannercontainer.Config{
		Image: defaultRef,
		ImageOverrides: map[string]string{
			"pmd": jvmRef,
		},
		UpstreamTools: map[string]scannercontainer.ToolImageSpec{
			"trivy": {Image: upstreamRef},
		},
	}

	applyContainerImagesToScannerPlan(plan, config)

	want := map[string]string{
		"bandit": defaultRef,
		"pmd":    jvmRef,
		"trivy":  upstreamRef,
		"ruff":   defaultRef,
	}
	for _, decision := range append(plan.Run, plan.Skip...) {
		if decision.Image != want[decision.Tool] {
			t.Fatalf("%s image = %q, want %q", decision.Tool, decision.Image, want[decision.Tool])
		}
	}
	if plan.ScannerReleaseID != "" || plan.ReleaseManifestDigest != "" {
		t.Fatalf("compatibility plan invented managed release identity: %#v", plan)
	}
}

func TestScannerRunRecordRetainsConfiguredImageDigest(t *testing.T) {
	digest := "sha256:" + repeatHex("d")
	plan := &report.ScannerPlan{Run: []report.ScannerPlanDecision{{
		Tool: "bandit", Image: "registry.example/wolf-scanners@" + digest,
	}}}
	record := scannerRunRecordQueued("scan-1", "bandit", plan)
	if record.Image != plan.Run[0].Image || record.ImageDigest != digest {
		t.Fatalf("scanner run provenance = image %q digest %q", record.Image, record.ImageDigest)
	}
}

func repeatHex(value string) string {
	result := ""
	for range 64 {
		result += value
	}
	return result
}
