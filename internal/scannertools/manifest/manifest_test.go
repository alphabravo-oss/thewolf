package manifest_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/scannertools/manifest"
	"github.com/alphabravocompany/thewolf/internal/scannertools/validate"
	_ "github.com/alphabravocompany/thewolf/plugins"
)

func TestDefaultManifestLoads(t *testing.T) {
	m := loadManifest(t)
	if got := len(m.Tools); got != 49 {
		t.Fatalf("manifest tool count = %d, want 49", got)
	}
	counts := m.TierCounts()
	assertEqual(t, "default tool count", counts[manifest.TierDefault], 22)
	assertEqual(t, "bucket tool count", counts[manifest.TierBucket], 5)
	assertEqual(t, "upstream tool count", counts[manifest.TierUpstream], 22)
}

func TestDefaultManifestValidation(t *testing.T) {
	result, err := validate.Run(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "tool count", result.ToolCount, 49)
	assertEqual(t, "default count", result.DefaultCount, 22)
	assertEqual(t, "bucket count", result.BucketCount, 5)
	assertEqual(t, "upstream count", result.UpstreamCount, 22)
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
