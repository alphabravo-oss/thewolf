package verify

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

type branchScannerFunc func(context.Context, string, []string) ([]models.Finding, error)

func (f branchScannerFunc) Rescan(ctx context.Context, path string, tools []string) ([]models.Finding, error) {
	return f(ctx, path, tools)
}

func TestGateBranchChecksAllToolsAndFindingCounts(t *testing.T) {
	stubBuild(t, false)
	fixed := goFinding()
	known := models.Finding{ToolName: "semgrep", RuleID: "known", FilePath: "other.go", LineStart: 10}
	shifted := known
	shifted.LineStart = 25
	for _, tc := range []struct {
		name string
		post []models.Finding
		err  error
		want string
	}{
		{"unchanged-existing", []models.Finding{shifted}, nil, ""},
		{"reintroduced", []models.Finding{fixed}, nil, "previously accepted"},
		{"additional-occurrence", []models.Finding{known, shifted}, nil, "introduced a finding"},
		{"extra-file", []models.Finding{{ToolName: "clean-tool", RuleID: "new", FilePath: "elsewhere.py"}}, nil, "introduced a finding"},
		{"incomplete", nil, errors.New("scanner unavailable"), "scanner unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := &fakeWorkspace{path: t.TempDir(), changed: []string{"main.go"}}
			tools := []string{"gosec", "semgrep", "clean-tool"}
			scanner := branchScannerFunc(func(_ context.Context, path string, got []string) ([]models.Finding, error) {
				if path != ws.Path() || !slices.Equal(got, tools) {
					t.Fatalf("wrong branch scope: %s %v", path, got)
				}
				return tc.post, tc.err
			})
			stages, err := GateBranch(t.Context(), ws, []models.Finding{fixed, known}, []models.Finding{fixed}, tools, scanner)
			if tc.want == "" {
				if err != nil || !stages[len(stages)-1].Passed {
					t.Fatalf("valid branch rejected: %v %v", stages, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("unsafe branch accepted: %v %v", stages, err)
			}
		})
	}
}

func TestGateBranchBuildsUntargetedLanguages(t *testing.T) {
	stubBuild(t, false)
	ws := &fakeWorkspace{path: t.TempDir(), changed: []string{"main.go", "extra.py"}}
	for _, file := range ws.changed {
		if err := os.WriteFile(filepath.Join(ws.Path(), file), []byte("source"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var commands []string
	runCommand = func(_ context.Context, _, name string, _ ...string) (string, error) {
		commands = append(commands, name)
		if name == "python3" {
			return "invalid syntax", errors.New("exit 1")
		}
		return "", nil
	}
	scanner := branchScannerFunc(func(context.Context, string, []string) ([]models.Finding, error) {
		t.Fatal("scanned after a failed build")
		return nil, nil
	})
	_, err := GateBranch(t.Context(), ws, []models.Finding{goFinding()}, nil, []string{"gosec"}, scanner)
	if err == nil || !strings.Contains(err.Error(), "final build check failed") || !slices.Equal(commands, []string{"go", "python3"}) {
		t.Fatalf("untargeted language escaped build verification: %v %v", commands, err)
	}
}

func TestBranchBuildChecksLeaveNoGeneratedFiles(t *testing.T) {
	for _, language := range []string{"go", "python3"} {
		t.Run(language, func(t *testing.T) {
			if !commandAvailable(language) {
				t.Skip("toolchain unavailable")
			}
			dir := t.TempDir()
			file, source := "main.py", "x = 1\n"
			if language == "go" {
				t.Setenv("GOWORK", "off")
				t.Setenv("GOPROXY", "off")
				file, source = "main.go", "package main\nfunc main() {}\n"
				if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module fixture\n\ngo 1.26\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(dir, file), []byte(source), 0o600); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadDir(dir)
			stage := buildStage(t.Context(), dir, models.Finding{FilePath: file}, []string{file})
			after, _ := os.ReadDir(dir)
			if !stage.Passed || stage.Skipped || len(before) != len(after) {
				t.Fatalf("build generated patch content: %+v before=%v after=%v", stage, before, after)
			}
		})
	}
}
