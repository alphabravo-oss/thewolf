package manifest_test

import (
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/scannertools/manifest"
	"github.com/alphabravocompany/thewolf/internal/scannertools/validate"
	_ "github.com/alphabravocompany/thewolf/plugins"
	"github.com/alphabravocompany/thewolf/scanners"
)

// TestEmbeddedManifestIsValid guards the packaging fix: scanners/tools.yaml is
// embedded in the binary so /scanners/tools works in the container (where no
// repo checkout exists). If the embed breaks or the YAML goes invalid, this
// fails at test time instead of 500-ing at runtime.
func TestEmbeddedManifestIsValid(t *testing.T) {
	if len(scanners.ToolsYAML) == 0 {
		t.Fatal("embedded tools.yaml is empty")
	}
	m, err := manifest.LoadBytes(scanners.ToolsYAML, "embedded")
	if err != nil {
		t.Fatalf("embedded manifest failed to load: %v", err)
	}
	if len(m.Tools) == 0 {
		t.Fatal("embedded manifest has no tools")
	}
}

func TestDefaultManifestLoads(t *testing.T) {
	m := loadManifest(t)
	if got := len(m.Tools); got != 49 {
		t.Fatalf("manifest tool count = %d, want 49", got)
	}
	counts := m.TierCounts()
	assertEqual(t, "default tool count", counts[manifest.TierDefault], 21)
	assertEqual(t, "bucket tool count", counts[manifest.TierBucket], 5)
	assertEqual(t, "upstream tool count", counts[manifest.TierUpstream], 23)
}

func TestDefaultManifestValidation(t *testing.T) {
	result, err := validate.Run(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "tool count", result.ToolCount, 49)
	assertEqual(t, "default count", result.DefaultCount, 21)
	assertEqual(t, "bucket count", result.BucketCount, 5)
	assertEqual(t, "upstream count", result.UpstreamCount, 23)
}

func TestParseVersionsEnv(t *testing.T) {
	got, err := manifest.ParseVersionsEnv([]byte(`
# comment
FOO=1.2.3
BAR="4.5.6"
`))
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "FOO", got["FOO"], "1.2.3")
	assertEqual(t, "BAR", got["BAR"], "4.5.6")
}

func TestManifestRequiresResourceMetadata(t *testing.T) {
	m := &manifest.Manifest{Tools: map[string]manifest.Tool{
		"demo": {
			DisplayName:     "Demo",
			Category:        "sast",
			PluginPackage:   "plugins/demo",
			IntegrationTier: manifest.TierDefault,
			Install:         manifest.Install{Manager: "go"},
			UpdateSource:    manifest.UpdateSource{Type: "go_module"},
		},
	}}

	err := m.Validate()
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "resource_class") || !strings.Contains(err.Error(), "default_timeout") {
		t.Fatalf("expected resource metadata errors, got %v", err)
	}
}

func TestDefaultManifestUsesEverySupportedUpdateSource(t *testing.T) {
	m := loadManifest(t)
	declared := map[string]struct{}{}
	for _, tool := range m.Tools {
		declared[tool.UpdateSource.Type] = struct{}{}
	}
	var got []string
	for sourceType := range declared {
		got = append(got, sourceType)
	}
	sort.Strings(got)
	want := manifest.SupportedUpdateSourceTypes()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("declared update source types = %v, supported = %v", got, want)
	}
}

func TestManifestRejectsIncompleteKnownUpdateSource(t *testing.T) {
	m := validTestManifest()
	tool := m.Tools["demo"]
	tool.UpdateSource = manifest.UpdateSource{Type: "github_releases", Owner: "acme"}
	m.Tools["demo"] = tool
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), "update_source.repo is required") {
		t.Fatalf("Validate() = %v", err)
	}
}

func TestManifestAllowsUnsupportedSourceOnlyWithCompleteManualException(t *testing.T) {
	m := validTestManifest()
	tool := m.Tools["demo"]
	tool.UpdateSource = manifest.UpdateSource{Type: "vendor_portal"}
	m.Tools["demo"] = tool
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("Validate unsupported source = %v", err)
	}
	tool.ManualUpdate = manifest.ManualUpdate{
		Owner: "scanner-platform", Reason: "vendor portal requires an enterprise login",
		ReviewAfter: "2027-07-30",
	}
	m.Tools["demo"] = tool
	if err := m.Validate(); err != nil {
		t.Fatalf("Validate manual exception = %v", err)
	}
}

func TestManifestValidatesSupplyChainMetadata(t *testing.T) {
	m := validTestManifest()
	tool := m.Tools["demo"]
	tool.SourceIntegrity = manifest.SourceIntegrity{
		SHA256:         "not-a-digest",
		SHA256Variable: "not-a-variable",
		SignatureURL:   "https://example.invalid/demo.sig",
	}
	tool.ParserContract = manifest.ParserContract{Fixtures: []string{"../escape.json"}}
	tool.Risk = manifest.RiskPolicy{AutoCandidate: true}
	m.Tools["demo"] = tool
	err := m.Validate()
	if err == nil {
		t.Fatal("expected supply-chain metadata validation errors")
	}
	for _, want := range []string{"source_integrity.sha256", "sha256_variable", "signature_identity", "parser_contract.fixtures", "risk.classification"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("validation error missing %q: %v", want, err)
		}
	}
}

func validTestManifest() *manifest.Manifest {
	return &manifest.Manifest{Tools: map[string]manifest.Tool{
		"demo": {
			DisplayName: "Demo", Category: "sast", ResourceClass: "medium",
			DefaultTimeout: "10m", PluginPackage: "plugins/demo",
			IntegrationTier: manifest.TierDefault,
			Install:         manifest.Install{Manager: "pip", Package: "demo"},
			UpdateSource:    manifest.UpdateSource{Type: "pypi", Package: "demo"},
		},
	}}
}

func loadManifest(t *testing.T) *manifest.Manifest {
	t.Helper()
	m, err := manifest.LoadFile(filepath.Join(repoRoot(t), "scanners", "tools.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := manifest.FindRepoRoot("")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func assertEqual[T comparable](t *testing.T, label string, got, want T) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %v, want %v", label, got, want)
	}
}
