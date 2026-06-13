package docs

import (
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/scannertools/manifest"
)

func TestMarkdown(t *testing.T) {
	m := &manifest.Manifest{Tools: map[string]manifest.Tool{
		"bandit": {
			DisplayName:     "Bandit",
			Category:        "sast",
			ResourceClass:   "medium",
			DefaultTimeout:  "10m",
			PluginPackage:   "plugins/python",
			IntegrationTier: manifest.TierDefault,
			PinnedVersion:   "1.7.10",
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
			PinnedVersion:   "1.92.0",
			VersionVariable: "SEMGREP_VERSION",
			Image:           manifest.Image{PinnedReference: "semgrep/semgrep:1.92.0"},
			UpdateSource:    manifest.UpdateSource{Type: "docker_registry", Repository: "semgrep/semgrep"},
		},
	}}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	got := string(Markdown(m))
	for _, want := range []string{
		"| Default Wolf image | 1 |",
		"| Upstream/native images | 1 |",
		"| `semgrep` | sast | upstream | `1.92.0` (`SEMGREP_VERSION`) | `semgrep/semgrep:1.92.0` | `linux/amd64`, `linux/arm64` | `docker:semgrep/semgrep` |",
		"| `bandit` | sast | default | `1.7.10` (`BANDIT_VERSION`) | `pip:bandit` | - | `pypi:bandit` |",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated markdown missing %q:\n%s", want, got)
		}
	}
}
