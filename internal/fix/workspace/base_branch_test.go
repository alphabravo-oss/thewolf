package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestPrepareUsesSourceBranchAndPreservesUserCheckout(t *testing.T) {
	for _, source := range []models.SourceType{models.SourceTypeLocal, models.SourceTypeGitHub} {
		t.Run(string(source), func(t *testing.T) {
			ctx := t.Context()
			origin := initRepo(t)
			git := func(args ...string) string {
				t.Helper()
				out, err := runGit(ctx, origin, args...)
				if err != nil {
					t.Fatal(err)
				}
				return strings.TrimSpace(out)
			}
			git("checkout", "-b", "release")
			if err := os.WriteFile(filepath.Join(origin, "release.go"), []byte("package main\n"), 0600); err != nil {
				t.Fatal(err)
			}
			git("add", "release.go")
			git("-c", "user.name=test", "-c", "user.email=test@example.test", "commit", "-m", "release")
			wantBase := git("rev-parse", "HEAD")
			git("checkout", "main")
			userFile := filepath.Join(origin, "main.go")
			if err := os.WriteFile(userFile, []byte("uncommitted user edits\n"), 0600); err != nil {
				t.Fatal(err)
			}
			opts := Options{Repo: &models.Repo{SourceType: source, SourcePath: origin, DefaultBranch: "main"}, Branch: "wolf-fix/release", BaseBranch: "release", BaseDir: t.TempDir()}
			if source == models.SourceTypeGitHub {
				opts.Token, opts.CloneURL = "fixture-token", "file://"+origin
			}
			ws, err := Prepare(ctx, opts)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = ws.Cleanup(t.Context()) })
			if ws.verificationBase != wantBase {
				t.Fatalf("base = %s, want release %s", ws.verificationBase, wantBase)
			}
			if _, err := os.Stat(filepath.Join(ws.Path(), "release.go")); err != nil {
				t.Fatal(err)
			}
			if files, err := ws.ChangedFiles(ctx); err != nil || len(files) != 0 {
				t.Fatalf("workspace not clean: %v %v", files, err)
			}
			if got := git("branch", "--show-current"); got != "main" {
				t.Fatalf("user branch changed: %q", got)
			}
			if got, err := os.ReadFile(userFile); err != nil || string(got) != "uncommitted user edits\n" {
				t.Fatalf("user edits changed: %q %v", got, err)
			}
			if err := ws.Cleanup(ctx); err != nil {
				t.Fatal(err)
			}
			// An existing branch is not a substitute for lost workspace metadata.
			opts.Branch = "main"
			if extra, err := Prepare(ctx, opts); err == nil {
				_ = extra.Cleanup(ctx)
				t.Fatal("adopted an existing branch")
			}
			for _, missing := range []string{"missing", "release~1"} {
				opts.Branch, opts.BaseBranch = "wolf-fix/missing", missing
				if extra, err := Prepare(ctx, opts); err == nil {
					_ = extra.Cleanup(ctx)
					t.Fatalf("accepted non-branch base %q", missing)
				}
			}
		})
	}
}
