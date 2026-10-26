package plugin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestKubernetesApplicability(t *testing.T) {
	for _, tc := range []struct {
		name, content string
		want          bool
	}{
		{"package.json", `{"name":"example"}`, false},
		{"nested/config.yaml", "setting: value", false},
		{"nested/deploy.yaml", "setting: value\n---\napiVersion: apps/v1\nkind: Deployment", true},
		{"deploy.json", `{"apiVersion":"v1","kind":"Pod"}`, true},
		{"chart/Chart.yaml", "name: example", true},
		{"node_modules/deploy.yaml", "apiVersion: v1\nkind: Pod", false},
		{"broken.yaml", "apiVersion: [", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, tc.name)
			if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(tc.content), 0600); err != nil {
				t.Fatal(err)
			}
			got, err := HasKubernetesInputs(dir)
			if err != nil || got != tc.want {
				t.Fatalf("got %v %v", got, err)
			}
		})
	}
	if _, err := HasKubernetesInputs(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatal("missing source reported as not applicable")
	}
}
