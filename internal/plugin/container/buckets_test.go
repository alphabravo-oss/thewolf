package container

import "testing"

func TestDefaultBucketImages(t *testing.T) {
	m := DefaultBucketImages("ghcr.io/x/wolf-scanners", "2.0")

	cases := map[string]string{
		"infer":  "ghcr.io/x/wolf-scanners-jvm:2.0",
		"pmd":    "ghcr.io/x/wolf-scanners-jvm:2.0",
		"clippy": "ghcr.io/x/wolf-scanners-rust:2.0",
		"codeql": "ghcr.io/x/wolf-scanners-codeql:2.0",
	}
	for tool, want := range cases {
		got, ok := m[tool]
		if !ok {
			t.Errorf("missing %q in bucket map", tool)
		} else if got != want {
			t.Errorf("%s: got %q, want %q", tool, got, want)
		}
	}

	// Tools NOT in the map (most of them) — should not be present.
	for _, tool := range []string{"bandit", "gosec", "semgrep", "trivy", "eslint"} {
		if _, ok := m[tool]; ok {
			t.Errorf("%q should fall through to default Image, not be in bucket map", tool)
		}
	}
}

func TestDefaultBucketImages_Empty(t *testing.T) {
	if m := DefaultBucketImages("", "1.0"); m != nil {
		t.Errorf("expected nil for empty base, got %v", m)
	}
	if m := DefaultBucketImages("base", ""); m != nil {
		t.Errorf("expected nil for empty version, got %v", m)
	}
}

func TestConfig_ImageFor_FallsThroughToDefault(t *testing.T) {
	c := &Config{
		Image: "wolf-scanners:1.0",
		ImageOverrides: map[string]string{
			"infer": "wolf-scanners-jvm:1.0",
		},
	}
	if got := c.ImageFor("infer"); got != "wolf-scanners-jvm:1.0" {
		t.Errorf("infer should hit override, got %q", got)
	}
	if got := c.ImageFor("bandit"); got != "wolf-scanners:1.0" {
		t.Errorf("bandit should fall through to default, got %q", got)
	}
}

func TestConfig_AllImages(t *testing.T) {
	c := &Config{
		Image: "x:1",
		ImageOverrides: map[string]string{
			"a": "y:1",
			"b": "y:1", // duplicate
			"c": "z:1",
		},
	}
	got := c.AllImages()
	if len(got) != 3 {
		t.Errorf("expected 3 distinct images, got %d (%v)", len(got), got)
	}
	want := map[string]bool{"x:1": true, "y:1": true, "z:1": true}
	for _, g := range got {
		if !want[g] {
			t.Errorf("unexpected image %q", g)
		}
	}
}
