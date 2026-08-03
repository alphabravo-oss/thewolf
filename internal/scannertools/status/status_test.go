package status

import (
	"testing"

	"github.com/alphabravocompany/thewolf/internal/plugin/container"
	"github.com/alphabravocompany/thewolf/internal/scannertools/manifest"
)

func TestBuildMergesManifestAndContainerConfig(t *testing.T) {
	m := &manifest.Manifest{Tools: map[string]manifest.Tool{
		"bandit": {
			DisplayName:     "Bandit",
			Category:        "sast",
			ResourceClass:   "medium",
			DefaultTimeout:  "10m",
			PluginPackage:   "plugins/python",
			IntegrationTier: manifest.TierDefault,
			PinnedVersion:   "1.0.0",
			VersionVariable: "BANDIT_VERSION",
			Install:         manifest.Install{Manager: "pip", Package: "bandit"},
			UpdateSource:    manifest.UpdateSource{Type: "pypi", Package: "bandit"},
		},
		"semgrep": {
			DisplayName:     "Semgrep",
			Category:        "sast",
			ResourceClass:   "heavy",
			DefaultTimeout:  "15m",
			PluginPackage:   "plugins/general",
			IntegrationTier: manifest.TierUpstream,
			PinnedVersion:   "1.2.3",
			VersionVariable: "SEMGREP_VERSION",
			Image:           manifest.Image{PinnedReference: "semgrep/semgrep:1.2.3", Entrypoint: "semgrep"},
			UpdateSource:    manifest.UpdateSource{Type: "docker_registry", Repository: "semgrep/semgrep"},
		},
	}}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}

	rows := Build(m, &container.Config{
		Image: "wolf-scanners:2.0.0",
		UpstreamTools: map[string]container.ToolImageSpec{
			"semgrep": {Image: "semgrep/semgrep:1.2.3", Entrypoint: "semgrep"},
		},
	})
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	bandit, ok := Find(rows, "bandit")
	if !ok {
		t.Fatal("bandit missing")
	}
	if bandit.ConfiguredImage != "wolf-scanners:2.0.0" {
		t.Fatalf("bandit configured image = %q", bandit.ConfiguredImage)
	}
	semgrep, ok := Find(rows, "semgrep")
	if !ok {
		t.Fatal("semgrep missing")
	}
	if semgrep.CanonicalImage != "semgrep/semgrep:1.2.3" || semgrep.ConfiguredImage != "semgrep/semgrep:1.2.3" {
		t.Fatalf("semgrep image mismatch: %#v", semgrep)
	}
	if semgrep.Overridden {
		t.Fatalf("semgrep should not be overridden: %#v", semgrep)
	}
}

func TestBuildMergesImagePresence(t *testing.T) {
	m := &manifest.Manifest{Tools: map[string]manifest.Tool{
		"bandit": {
			DisplayName:     "Bandit",
			Category:        "sast",
			ResourceClass:   "medium",
			DefaultTimeout:  "10m",
			PluginPackage:   "plugins/python",
			IntegrationTier: manifest.TierDefault,
			PinnedVersion:   "1.0.0",
			VersionVariable: "BANDIT_VERSION",
			Install:         manifest.Install{Manager: "pip", Package: "bandit"},
			UpdateSource:    manifest.UpdateSource{Type: "pypi", Package: "bandit"},
		},
	}}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	rows := BuildWithChecksAndImages(m, &container.Config{Image: "wolf-scanners:2.0.0"}, nil, map[string]bool{
		"wolf-scanners:2.0.0": true,
	})
	bandit, ok := Find(rows, "bandit")
	if !ok {
		t.Fatal("bandit missing")
	}
	if bandit.ImagePresent == nil || !*bandit.ImagePresent {
		t.Fatalf("image presence not merged: %#v", bandit)
	}
}

func TestBuildDetectsOverridesAndLatest(t *testing.T) {
	m := &manifest.Manifest{Tools: map[string]manifest.Tool{
		"semgrep": {
			DisplayName:     "Semgrep",
			Category:        "sast",
			ResourceClass:   "heavy",
			DefaultTimeout:  "15m",
			PluginPackage:   "plugins/general",
			IntegrationTier: manifest.TierUpstream,
			PinnedVersion:   "1.2.3",
			VersionVariable: "SEMGREP_VERSION",
			Image:           manifest.Image{PinnedReference: "semgrep/semgrep:1.2.3"},
			UpdateSource:    manifest.UpdateSource{Type: "docker_registry", Repository: "semgrep/semgrep"},
		},
	}}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}

	rows := Build(m, &container.Config{
		Image: "wolf-scanners:2.0.0",
		UpstreamTools: map[string]container.ToolImageSpec{
			"semgrep": {Image: "registry.internal/semgrep:latest"},
		},
	})
	semgrep, ok := Find(rows, "semgrep")
	if !ok {
		t.Fatal("semgrep missing")
	}
	if !semgrep.Overridden {
		t.Fatalf("semgrep override not detected: %#v", semgrep)
	}
	if !semgrep.UsesLatestTag {
		t.Fatalf("semgrep latest tag not detected: %#v", semgrep)
	}
}

func TestUsesLatestTag(t *testing.T) {
	tests := []struct {
		ref  string
		want bool
	}{
		{"wolf-scanners:latest", true},
		{"registry:5000/wolf-scanners:latest", true},
		{"wolf-scanners:2.0.0", false},
		{"wolf-scanners@sha256:abc", false},
		{"registry:5000/wolf-scanners", false},
	}
	for _, tt := range tests {
		if got := UsesLatestTag(tt.ref); got != tt.want {
			t.Fatalf("UsesLatestTag(%q) = %v, want %v", tt.ref, got, tt.want)
		}
	}
}
