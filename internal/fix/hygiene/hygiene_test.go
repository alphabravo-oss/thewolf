package hygiene

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestClassify(t *testing.T) {
	if Classify("yamllint") != KindLint || Classify("trivy") != KindBump ||
		Classify("scorecard") != KindPolicy || Classify("bearer") != KindCode ||
		Classify("shellcheck") != KindLint {
		t.Fatal(Classify("yamllint"), Classify("trivy"), Classify("scorecard"), Classify("bearer"), Classify("shellcheck"))
	}
}

func TestLintPassWritesYamllint(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "x.yaml"), []byte("foo: bar   \n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "clean.yaml"), []byte("ok: true\n"), 0o644)
	res, err := LintPass(context.Background(), dir, "yamllint", []models.Finding{
		{ID: "a", FilePath: "x.yaml"},
		{ID: "b", FilePath: "clean.yaml"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".yamllint.yaml")); err != nil {
		t.Fatal("expected .yamllint.yaml")
	}
	if res.Kept["a"] == "" {
		t.Fatal("changed file should be kept/fixed, not muted")
	}
	if res.Muted["a"] != "" || res.Muted["b"] != "" {
		t.Fatalf("lint leftovers must not be muted: %#v", res.Muted)
	}
	if res.Kept["b"] != "" {
		t.Fatalf("untouched file must stay open: %#v", res.Kept)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "x.yaml"))
	if strings.Contains(string(got), "bar   ") {
		t.Fatalf("trailing space not trimmed: %q", got)
	}
}

func TestRestoreProtectedHelmTarball(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	run("init")
	_ = os.MkdirAll(filepath.Join(dir, "chart", "charts"), 0o755)
	tgz := filepath.Join(dir, "chart", "charts", "argo-cd-9.5.21.tgz")
	_ = os.WriteFile(tgz, []byte("chart"), 0o644)
	_ = os.MkdirAll(filepath.Join(dir, "docs"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "docs", "openapi.yaml"), []byte("openapi: 3.0.0\n"), 0o644)
	run("add", ".")
	run("commit", "-m", "base")
	_ = os.Remove(tgz)
	_ = os.WriteFile(filepath.Join(dir, "docs", "openapi.yaml"), []byte("rewritten\n"), 0o644)
	got := RestoreProtected(context.Background(), dir)
	if len(got) < 2 {
		t.Fatalf("restored = %v", got)
	}
	if _, err := os.Stat(tgz); err != nil {
		t.Fatal("helm tarball must be restored")
	}
	body, _ := os.ReadFile(filepath.Join(dir, "docs", "openapi.yaml"))
	if strings.Contains(string(body), "rewritten") {
		t.Fatalf("openapi rewritten: %s", body)
	}
}

func TestProtectedRel(t *testing.T) {
	for _, p := range []string{
		"deploy/chart/Chart.yaml",
		"deploy/chart/Chart.lock",
		"deploy/chart/charts/argo-cd-9.5.21.tgz",
		"docs/openapi.yaml",
		"internal/handler/assets/openapi.yaml",
		"specs/openapi/foo.yaml",
	} {
		if !ProtectedRel(p) {
			t.Fatalf("expected protected: %s", p)
		}
	}
	for _, p := range []string{"main.go", "frontend/src/app.tsx", ".github/workflows/ci.yml"} {
		if ProtectedRel(p) {
			t.Fatalf("expected unprotected: %s", p)
		}
	}
}

func TestKindRankLintBeforeCode(t *testing.T) {
	if KindRank("yamllint") >= KindRank("bearer") || KindRank("trivy") >= KindRank("bearer") {
		t.Fatal(KindRank("yamllint"), KindRank("trivy"), KindRank("bearer"))
	}
	if KindRank("yamllint") >= KindRank("trivy") || KindRank("trivy") >= KindRank("scorecard") {
		t.Fatal("lint < bump < policy")
	}
}

func TestParseAdvisory(t *testing.T) {
	adv := parseAdvisory(models.Finding{Title: "CVE-2026-33815 in github.com/jackc/pgx/v5@v5.8.0", FilePath: "go.mod"})
	if adv.kind != "go" || adv.name != "github.com/jackc/pgx/v5" {
		t.Fatalf("go parse = %+v", adv)
	}
	adv = parseAdvisory(models.Finding{Title: "GO-2025-4009: pem in encoding/pem", FilePath: "go.mod"})
	if adv.kind != "stdlib" {
		t.Fatalf("stdlib parse = %+v", adv)
	}
	adv = parseAdvisory(models.Finding{Title: "nanoid: custom generators", FilePath: "frontend/package.json"})
	if adv.kind != "npm" || adv.name != "nanoid" {
		t.Fatalf("npm parse = %+v", adv)
	}
	adv = parseAdvisory(models.Finding{
		Title: "CVE-1", Description: "Vulnerable package: lodash (range: <4.17.21)", FilePath: "package.json",
	})
	if adv.kind != "npm" || adv.name != "lodash" {
		t.Fatalf("npm desc parse = %+v", adv)
	}
	adv = parseAdvisory(models.Finding{Title: "CVE-1 in alpine", FilePath: "docker.io/library/alpine:3.18"})
	if adv.kind != "image" {
		t.Fatalf("image parse = %+v", adv)
	}
}

func TestParseRenovateFinding(t *testing.T) {
	u, ok := parseRenovateFinding(models.Finding{
		ID: "a", ToolName: "renovate", FilePath: "go.mod",
		Title: "gomod: github.com/jackc/pgx/v5 v5.8.0 → v5.8.1 (patch)",
	})
	if !ok || u.name != "github.com/jackc/pgx/v5" || u.updateType != "patch" || u.next != "v5.8.1" {
		t.Fatalf("patch parse = %+v ok=%v", u, ok)
	}
	u, ok = parseRenovateFinding(models.Finding{
		ToolName: "renovate", Title: "npm: @types/node ^20.12.7 → ^26.0.0 (major)",
		FilePath: "frontend/package.json",
	})
	if !ok || u.name != "@types/node" || u.updateType != "major" {
		t.Fatalf("scoped npm parse = %+v ok=%v", u, ok)
	}
}

func TestPickConservativePrefersPatchOverMinor(t *testing.T) {
	apply, skip, ok := pickConservative([]renovateUpdate{
		{id: "maj", name: "go", current: "1.25.1", next: "1.26.1", updateType: "minor", vuln: false},
		{id: "pat", name: "go", current: "1.25.1", next: "1.25.2", updateType: "patch"},
	})
	if !ok || apply.id != "pat" {
		t.Fatalf("apply=%+v ok=%v", apply, ok)
	}
	if len(skip) != 1 || skip[0].id != "maj" {
		t.Fatalf("skip=%+v", skip)
	}
}

func TestPickConservativeMinorOnlyWhenVuln(t *testing.T) {
	_, _, ok := pickConservative([]renovateUpdate{
		{id: "m", name: "x", current: "1.25.1", next: "1.26.1", updateType: "minor", vuln: false},
	})
	if ok {
		t.Fatal("non-vuln minor must not auto-apply")
	}
	apply, _, ok := pickConservative([]renovateUpdate{
		{id: "m", name: "x", current: "1.25.1", next: "1.26.1", updateType: "minor", vuln: true},
	})
	if !ok || apply.id != "m" {
		t.Fatalf("vuln minor should apply, got %+v ok=%v", apply, ok)
	}
}

func TestBumpPassNeverMutesRenovate(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n\ngo 1.25.1\n"), 0o644)
	runInRepo = func(context.Context, string, string, ...string) error { return nil }
	t.Cleanup(func() {
		runInRepo = func(ctx context.Context, dir, name string, args ...string) error { return nil }
	})
	res, err := BumpPass(context.Background(), dir, []models.Finding{
		{
			ID: "maj", ToolName: "renovate", FilePath: "go.mod",
			Title: "gomod: github.com/foo/bar v1.0.0 → v2.0.0 (major)",
		},
		{
			ID: "img", ToolName: "renovate", FilePath: "frontend/Dockerfile",
			Title:       "dockerfile: node 22-alpine → 26-alpine (major)",
			Description: "Renovate detected an available major update for node in frontend/Dockerfile: 22-alpine → 26-alpine.",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Muted) != 0 {
		t.Fatalf("renovate must not be muted: %#v", res.Muted)
	}
}

func TestBumpPassAppliesRenovatePatchNotMinor(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n\ngo 1.25.1\n"), 0o644)
	var got [][]string
	runInRepo = func(_ context.Context, _, name string, args ...string) error {
		got = append(got, append([]string{name}, args...))
		return nil
	}
	t.Cleanup(func() {
		runInRepo = func(ctx context.Context, dir, name string, args ...string) error { return nil }
	})
	res, err := BumpPass(context.Background(), dir, []models.Finding{
		{ID: "p", ToolName: "renovate", FilePath: "go.mod", Title: "gomod: github.com/foo/bar v1.25.1 → v1.25.2 (patch)"},
		{ID: "m", ToolName: "renovate", FilePath: "go.mod", Title: "gomod: github.com/foo/bar v1.25.1 → v1.26.1 (minor)"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Kept["p"] == "" || !strings.Contains(res.Kept["p"], "v1.25.2") {
		t.Fatalf("patch kept = %#v", res.Kept)
	}
	if !strings.Contains(res.Kept["m"], "v1.25.2") || strings.Contains(res.Kept["m"], "applied v1.26") {
		t.Fatalf("minor should be recorded as skipped for smaller bump: %#v", res.Kept)
	}
	joined := fmt.Sprint(got)
	if !strings.Contains(joined, "github.com/foo/bar@v1.25.2") {
		t.Fatalf("go get args = %v", got)
	}
	if strings.Contains(joined, "@latest") || strings.Contains(joined, "v1.26.1") {
		t.Fatalf("must not jump minor or latest: %v", got)
	}
}

func TestBumpSpecIsPatchNotLatest(t *testing.T) {
	if bumpSpec("") != "@patch" {
		t.Fatalf("empty pin = %q", bumpSpec(""))
	}
}

func TestRewriteGoDirective(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "go.mod")
	_ = os.WriteFile(path, []byte("module x\n\ngo 1.25.1\n"), 0o644)
	if err := rewriteGoDirective(dir, "1.25.2"); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), "go 1.25.2") || strings.Contains(string(body), "go 1.25.1") {
		t.Fatalf("go.mod = %s", body)
	}
	if err := rewriteGoDirective(dir, "1.26.6"); err == nil {
		t.Fatal("must refuse a Go minor jump")
	}
}

func TestRenovateHandsOffHelmAndGoJump(t *testing.T) {
	if !renovateHandsOff(renovateUpdate{manager: "helmv3", name: "argo-cd", current: "9.5.21", next: "9.7.1", updateType: "minor"}) {
		t.Fatal("helm bumps are hands-off")
	}
	if !renovateHandsOff(renovateUpdate{manager: "gomod", name: "go", current: "1.25.1", next: "1.26.6", updateType: "minor"}) {
		t.Fatal("go 1.25 → 1.26 is hands-off")
	}
	if renovateHandsOff(renovateUpdate{manager: "gomod", name: "go", current: "1.25.1", next: "1.25.2", updateType: "patch"}) {
		t.Fatal("go patch should still be allowed")
	}
	if !renovateHandsOff(renovateUpdate{manager: "regex", name: "postgres", current: "16", next: "18", updateType: "major", file: "docker-compose.yml"}) {
		t.Fatal("postgres major is hands-off")
	}
	if renovateAuto(renovateUpdate{manager: "helmv3", name: "argo-cd", current: "9.5.21", next: "9.7.1", updateType: "minor"}) {
		t.Fatal("helm must not auto-apply")
	}
}

func TestBumpPassStdlibMuted(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n\ngo 1.22\n"), 0o644)
	runInRepo = func(context.Context, string, string, ...string) error {
		t.Fatal("must not go get stdlib")
		return nil
	}
	t.Cleanup(func() {
		runInRepo = func(ctx context.Context, dir, name string, args ...string) error { return nil }
	})
	res, err := BumpPass(context.Background(), dir, []models.Finding{
		{ID: "s", Title: "GO-2025-4009 in encoding/pem", FilePath: "go.mod"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Muted["s"] == "" || !strings.Contains(res.Muted["s"], "stdlib") {
		t.Fatalf("muted = %#v", res.Muted)
	}
}

func TestMuteWritesCommentAndSuppression(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte("package main\n\nfunc f() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var got *models.FindingSuppression
	files, err := Mute(dir, &models.FixJob{RepoID: "r", UserID: "u"}, models.Finding{
		ID: "f1", ToolName: "gosec", RuleID: "G101", FilePath: "main.go", LineStart: 3,
		Fingerprint: "fp-1",
	}, "false positive", suppressionCatch{fn: func(s *models.FindingSuppression) error {
		got = s
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("files = %v", files)
	}
	body, _ := os.ReadFile(src)
	if !strings.Contains(string(body), "//nosec G101") {
		t.Fatalf("body = %s", body)
	}
	if got == nil || got.ScopeValue != "fp-1" || got.RepoID != "r" {
		t.Fatalf("suppression = %+v", got)
	}
}

func TestMuteRule(t *testing.T) {
	var got *models.FindingSuppression
	if err := MuteRule(&models.FixJob{RepoID: "r", UserID: "u"}, "yamllint", "line-length", "style noise", suppressionCatch{fn: func(s *models.FindingSuppression) error {
		got = s
		return nil
	}}); err != nil {
		t.Fatal(err)
	}
	if got == nil || got.ScopeType != models.SuppressionScopeRule || got.ScopeValue != "line-length" {
		t.Fatalf("rule suppression = %+v", got)
	}
}

func TestNoiseReason(t *testing.T) {
	if !NoiseReason("clear false positive") || NoiseReason("too large to fix now") {
		t.Fatal("noise classifier")
	}
}

func TestPolicyWritesDependabot(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := PolicyPass(dir, []models.Finding{
		{ID: "d", RuleID: "Dependency-Update-Tool", Title: "Dependency-Update-Tool"},
		{ID: "p", RuleID: "Token-Permissions"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".github", "dependabot.yml")); err != nil {
		t.Fatal(err)
	}
	if res.Kept["d"] == "" || res.Muted["p"] == "" {
		t.Fatalf("%+v", res)
	}
}

type suppressionCatch struct {
	fn func(*models.FindingSuppression) error
}

func (s suppressionCatch) CreateFindingSuppression(sup *models.FindingSuppression) error {
	return s.fn(sup)
}
