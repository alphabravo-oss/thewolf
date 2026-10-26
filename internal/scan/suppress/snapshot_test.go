package suppress

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestSnapshotRetainsScanRulesWithoutSource(t *testing.T) {
	t.Setenv("WOLF_DISABLE_DEFAULT_SUPPRESSIONS", "")
	root := initRepo(t, "*.log\n!keep.log\n")
	if err := os.WriteFile(filepath.Join(root, ".wolfignore"), []byte("app.go rule=R1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	findings := []models.Finding{
		{FilePath: "app.go", RuleID: "R1"},
		{FilePath: "app.go", RuleID: "R2"},
		{FilePath: "private.log"},
		{FilePath: "keep.log"},
		{FilePath: "vendor/dep.go"},
		{FilePath: "Cargo.lock", Category: models.CategorySCA},
		{FilePath: "Cargo.lock", Category: models.CategorySAST},
	}
	snapshot, err := Capture(t.Context(), findings, root)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".wolfignore"), []byte("keep.log\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("app.go\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WOLF_DISABLE_DEFAULT_SUPPRESSIONS", "1")
	fresh, err := Capture(t.Context(), findings, root)
	if err != nil {
		t.Fatal(err)
	}
	newRows, _, err := fresh.Apply(slices.Clone(findings))
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []bool{true, true, false, true, false, false, false} {
		if newRows[i].Suppressed != want {
			t.Fatalf("new scan ignored updated rules at %d: %+v", i, newRows[i])
		}
	}
	// Serialization models storage/reload; later environment/source edits must
	// not be consulted, including when a source checkout no longer exists.
	t.Setenv("WOLF_DISABLE_DEFAULT_SUPPRESSIONS", "1")
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	scan := &models.Scan{Status: models.ScanStatusCompleted, IgnorePolicyJSON: string(encoded)}
	got, count, err := ApplyScan(t.Context(), slices.Clone(findings), scan, root)
	if err != nil || count != 4 {
		t.Fatalf("snapshot projection: %d %v", count, err)
	}
	for i, want := range []bool{true, false, true, false, true, false, true} {
		if got[i].Suppressed != want {
			t.Fatalf("row %d: %+v", i, got[i])
		}
	}
	// Current durable/import decisions remain authoritative. Removing one
	// later does not remove a scan-time source rule for the same path.
	findings[0].Suppressed, findings[0].SuppressedReason = true, "suppression:current"
	got, _, err = ApplyScan(t.Context(), slices.Clone(findings), scan, root)
	if err != nil || got[0].SuppressedReason != "suppression:current" {
		t.Fatalf("durable decision overwritten: %+v %v", got[0], err)
	}
	findings[0].Suppressed, findings[0].SuppressedReason = false, ""
	got, _, err = ApplyScan(t.Context(), findings, scan, root)
	if err != nil || !got[0].Suppressed || got[0].SuppressedReason == "suppression:current" {
		t.Fatalf("source decision lost after revoke: %+v %v", got[0], err)
	}
}

func TestSnapshotRejectsUnknownPolicyAndPathsAtomically(t *testing.T) {
	findings := []models.Finding{{FilePath: "known.go"}, {FilePath: "unknown.go"}}
	for _, policy := range []string{
		`{`, `null`, `{"version":2,"git_ignored_paths":{}}`,
		`{"version":1,"rules":{"Rules":[{"Reason":"hidden"}]},"git_ignored_paths":{"known.go":true}}`,
	} {
		rows := slices.Clone(findings)
		_, n, err := ApplyScan(t.Context(), rows, &models.Scan{IgnorePolicyJSON: policy}, "")
		if err == nil || n != 0 || !reflect.DeepEqual(rows, findings) {
			t.Fatalf("partial/unknown policy applied: %s %+v %v", policy, rows, err)
		}
	}
	// No policy is evidence of an old scan, not permission to invent its rules.
	rows := slices.Clone(findings)
	_, _, err := ApplyScan(t.Context(), rows, &models.Scan{Status: models.ScanStatusCompleted}, t.TempDir())
	if err != nil || !reflect.DeepEqual(rows, findings) {
		t.Fatalf("legacy policy fabricated: %+v %v", rows, err)
	}
}
