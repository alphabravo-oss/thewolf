package routes

import (
	"strings"
	"testing"
)

func TestFilterUnifiedDiff_OneFile(t *testing.T) {
	diff := "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-old\n+new\n" +
		"diff --git a/b.go b/b.go\n--- a/b.go\n+++ b/b.go\n@@ -1 +1 @@\n-x\n+y\n"
	got := filterUnifiedDiff(diff, []string{"a.go"})
	if !strings.Contains(got, "a.go") || !strings.Contains(got, "+new") || strings.Contains(got, "b.go") {
		t.Fatalf("got %q", got)
	}
}
