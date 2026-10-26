package suppress

import (
	"errors"
	"github.com/alphabravocompany/thewolf/internal/models"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestWolfIgnoreRejectsInvalidRules(t *testing.T) {
	for _, line := range []string{
		"** categroy=sql-injection", "** category=", "** rule=,", "** unexpected", "category=xss", "[invalid rule=R1", "** rule=R1,,R2", "** reason=", "!keep.go",
	} {
		t.Run(line, func(t *testing.T) {
			rs, err := ParseWolfIgnore(strings.NewReader("safe.go rule=R1\n"+line+"\n"), ".wolfignore")
			if err == nil || !strings.Contains(err.Error(), ".wolfignore:2:") || len(rs.Rules) != 0 {
				t.Fatalf("invalid file returned rules=%v error=%v", rs.Rules, err)
			}
		})
	}
}

func TestWolfIgnoreReadFailureDiscardsPartialRules(t *testing.T) {
	expected := errors.New("read interrupted")
	rs, err := ParseWolfIgnore(io.MultiReader(strings.NewReader("** rule=R1\n"), failingIgnoreReader{expected}), ".wolfignore")
	if !errors.Is(err, expected) || len(rs.Rules) != 0 {
		t.Fatalf("partial rules: %+v %v", rs, err)
	}
	rs, err = ParseWolfIgnore(strings.NewReader("** rule=R1\n"+strings.Repeat("x", 65536)), ".wolfignore")
	if err == nil || len(rs.Rules) != 0 {
		t.Fatalf("oversize line: %+v %v", rs, err)
	}
}

type failingIgnoreReader struct{ err error }

func (r failingIgnoreReader) Read([]byte) (int, error) { return 0, r.err }

func TestWolfIgnoreValidCommentsAndPredicates(t *testing.T) {
	literal, err := ParseWolfIgnore(strings.NewReader("name=value.go rule=R1"), ".wolfignore")
	if err != nil || len(literal.Rules) != 1 || literal.Rules[0].PathGlob != "name=value.go" {
		t.Fatalf("literal equals: %+v %v", literal, err)
	}

	rs, err := ParseWolfIgnore(strings.NewReader("** category=xss,sql-injection rule=R1,R2 reason=accepted # descriptive comment\n"), ".wolfignore")
	if err != nil {
		t.Fatal(err)
	}
	f := models.Finding{FilePath: "app.go", FineCategory: "xss", RuleID: "R1"}
	if r, ok := rs.Match(f); !ok || r.Reason != "accepted" {
		t.Fatalf("valid rule: %+v %v", r, ok)
	}
	f.RuleID = "R3"
	if _, ok := rs.Match(f); ok {
		t.Fatal("filter broadened")
	}
}

func TestApplyRepoRejectsUnreadableOrInvalidRulesWithoutMutation(t *testing.T) {
	for _, mode := range []string{"invalid", "directory", "outside-symlink"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			name := filepath.Join(root, ".wolfignore")
			switch mode {
			case "invalid":
				if err := os.WriteFile(name, []byte("** categroy=xss\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(name, 0700); err != nil {
					t.Fatal(err)
				}
			case "outside-symlink":
				outside := filepath.Join(t.TempDir(), "rules")
				if err := os.WriteFile(outside, []byte("**\n"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, name); err != nil {
					t.Fatal(err)
				}
			}
			findings := []models.Finding{{FilePath: "vendor/app.go"}, {FilePath: "app.go", Suppressed: true, SuppressedReason: "imported"}}
			before := append([]models.Finding(nil), findings...)
			_, n, err := ApplyRepo(t.Context(), findings, root)
			if err == nil || n != 0 || !reflect.DeepEqual(findings, before) {
				t.Fatalf("failed rules changed decisions: %+v %d %v", findings, n, err)
			}
		})
	}
}
