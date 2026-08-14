package hygiene

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
)

var (
	inPkgAt   = regexp.MustCompile(`(?i)\bin\s+([@a-zA-Z0-9._\-/]+)@([vV]?\d[^\s,)]*)`)
	inMod     = regexp.MustCompile(`(?i)\bin\s+([@a-zA-Z0-9._\-/]+)`)
	pkgLine   = regexp.MustCompile(`(?i)(?:Vulnerable package|Package):\s+([@a-zA-Z0-9._\-/]+)(?:@([^\s,(]+))?`)
	goModAt   = regexp.MustCompile(`([a-z0-9.-]+\.[a-z0-9.-]+(?:/[a-zA-Z0-9.\-_]+){1,})@v[0-9][^\s,)]*`)
	npmName   = regexp.MustCompile(`^([@a-zA-Z0-9._\-/]+):`)
	fixVer    = regexp.MustCompile(`(?i)fix(?: available)?:\s*([vV]?\d[^\s,)]*)`)
	runInRepo = func(ctx context.Context, dir, name string, args ...string) error {
		cmd := exec.CommandContext(ctx, name, args...) // #nosec G204
		cmd.Dir = dir
		return cmd.Run()
	}
)

var stdlibPrefix = []string{
	"encoding/", "os", "net", "crypto/", "internal/", "runtime", "reflect",
	"sync", "syscall", "time", "fmt", "io", "strconv", "strings", "bytes",
	"context", "errors", "path", "sort", "unicode", "math", "hash", "bufio",
	"cmp", "slices", "maps", "log", "flag", "testing", "embed", "unique",
}

type advisory struct {
	name string
	kind string // go | npm | stdlib | image
	pin  string
}

type bumpTarget struct {
	ids []string
	pin string
}

func addTarget(dst map[string]*bumpTarget, adv advisory, id string) {
	tgt := dst[adv.name]
	if tgt == nil {
		tgt = &bumpTarget{}
		dst[adv.name] = tgt
	}
	tgt.ids = append(tgt.ids, id)
	if tgt.pin == "" && adv.pin != "" {
		tgt.pin = adv.pin
	}
}

func bumpSpec(pin string) string {
	// Never @latest — that jumps minors/majors. An empty pin stays on the
	// current minor line (1.25.1 → 1.25.x).
	if strings.TrimSpace(pin) == "" {
		return "@patch"
	}
	if strings.HasPrefix(pin, "@") {
		return pin
	}
	return "@" + pin
}

// BumpPass upgrades Go / npm modules named by CVE findings and applies
// conservative Renovate updates (patch first; vuln-fixing minor only).
// Renovate findings are never muted. Stdlib and container-image advisories
// from CVE scanners are muted. Unparseable leftovers stay open so the
// code agent can still try.
func BumpPass(ctx context.Context, repoPath string, findings []models.Finding) (Result, error) {
	res := emptyResult()
	if repoPath == "" {
		return res, nil
	}
	hasGo := fileExists(filepath.Join(repoPath, "go.mod"))
	hasNPM := npmPrefix(repoPath) != "" && fileExists(filepath.Join(npmPrefix(repoPath), "package.json"))

	var rest []models.Finding
	reno := map[string][]renovateUpdate{}
	for _, f := range findings {
		if isRenovate(f) {
			if u, ok := parseRenovateFinding(f); ok {
				key := u.file + "|" + u.name
				reno[key] = append(reno[key], u)
				continue
			}
			// Never mute Renovate — leave it for the agent.
			continue
		}
		rest = append(rest, f)
	}
	if len(reno) > 0 {
		applyRenovateBumps(ctx, repoPath, reno, &res)
	}

	goMods := map[string]*bumpTarget{}
	npmPkgs := map[string]*bumpTarget{}
	for _, f := range rest {
		adv := parseAdvisory(f)
		switch adv.kind {
		case "image":
			res.Muted[f.ID] = "container / OS image advisory — not a repo dependency"
		case "stdlib":
			res.Muted[f.ID] = "stdlib / runtime advisory — needs a Go toolchain upgrade, not go get"
		case "go":
			if !hasGo {
				res.Muted[f.ID] = "Go advisory but this tree has no go.mod"
				continue
			}
			addTarget(goMods, adv, f.ID)
		case "npm":
			if !hasNPM {
				res.Muted[f.ID] = "npm advisory but this tree has no package.json"
				continue
			}
			addTarget(npmPkgs, adv, f.ID)
		default:
			if !hasGo && !hasNPM {
				res.Muted[f.ID] = "could not identify a repo dependency to bump"
			}
			// else leave open for the code agent
		}
	}

	if hasGo && len(goMods) > 0 {
		for mod, tgt := range goMods {
			spec := mod + bumpSpec(tgt.pin)
			bctx, cancel := context.WithTimeout(ctx, 90*time.Second)
			err := runInRepo(bctx, repoPath, "go", "get", spec)
			cancel()
			if err != nil {
				// leave open — agent can retry or pin a version
				continue
			}
			for _, id := range tgt.ids {
				res.Kept[id] = "bumped " + spec
			}
		}
		tctx, cancel := context.WithTimeout(ctx, 90*time.Second)
		_ = runInRepo(tctx, repoPath, "go", "mod", "tidy")
		cancel()
		res.Files = append(res.Files, "go.mod", "go.sum")
		res.Message = "bumped Go modules from CVE findings"
	}

	if hasNPM && len(npmPkgs) > 0 {
		prefix := npmPrefix(repoPath)
		for pkg, tgt := range npmPkgs {
			spec := pkg + bumpSpec(tgt.pin)
			args := []string{"install", spec, "--no-fund", "--no-audit"}
			if prefix != "" && prefix != repoPath {
				args = append(args, "--prefix", prefix)
			}
			bctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
			err := runInRepo(bctx, repoPath, "npm", args...)
			cancel()
			if err != nil {
				continue
			}
			for _, id := range tgt.ids {
				res.Kept[id] = "bumped " + spec
			}
		}
		res.Message = strings.TrimSpace(res.Message + " bumped npm packages")
	}
	return res, nil
}

func parseAdvisory(f models.Finding) advisory {
	fp := strings.ToLower(f.FilePath)
	if isImageTarget(fp) {
		return advisory{kind: "image"}
	}
	blob := strings.TrimSpace(f.Title + " " + f.Description + " " + f.RuleID)
	eco := ecosystem(fp, blob)

	if n := strings.TrimSpace(f.ModuleName); n != "" {
		return classifyMod(n, eco, fp, blob)
	}
	if m := inPkgAt.FindStringSubmatch(f.Title); len(m) >= 2 {
		return classifyMod(m[1], eco, fp, blob)
	}
	if m := pkgLine.FindStringSubmatch(f.Description); len(m) >= 2 {
		return classifyMod(m[1], eco, fp, blob)
	}
	if m := inMod.FindStringSubmatch(f.Title + " " + f.Description); len(m) >= 2 {
		return classifyMod(m[1], eco, fp, blob)
	}
	if m := goModAt.FindStringSubmatch(blob); len(m) == 2 {
		return classifyMod(m[1], "go", fp, blob)
	}
	if eco == "npm" {
		if m := npmName.FindStringSubmatch(f.Title); len(m) == 2 {
			return classifyMod(m[1], "npm", fp, blob)
		}
	}
	return advisory{}
}

func classifyMod(name, eco, fp, blob string) advisory {
	name = strings.TrimSpace(name)
	if name == "" {
		return advisory{}
	}
	kind := eco
	if kind == "" {
		kind = inferKind(name, fp)
	}
	if kind == "go" && isStdlib(name) {
		return advisory{name: name, kind: "stdlib", pin: pinFrom(blob)}
	}
	return advisory{name: name, kind: kind, pin: pinFrom(blob)}
}

func inferKind(name, fp string) string {
	if ecosystem(fp, "") == "npm" {
		return "npm"
	}
	if strings.Contains(name, ".") && strings.Contains(name, "/") {
		return "go"
	}
	if strings.HasPrefix(name, "@") || !strings.Contains(name, "/") {
		return "npm"
	}
	return "go"
}

func ecosystem(fp, blob string) string {
	switch {
	case strings.Contains(fp, "go.mod") || strings.Contains(fp, "go.sum"):
		return "go"
	case strings.Contains(fp, "package.json") || strings.Contains(fp, "package-lock") ||
		strings.Contains(fp, "yarn.lock") || strings.Contains(fp, "pnpm-lock"):
		return "npm"
	}
	low := strings.ToLower(blob)
	if strings.Contains(blob, "(Go)") || strings.Contains(low, "ecosystem: go") {
		return "go"
	}
	if strings.Contains(low, "(npm)") || strings.Contains(low, "ecosystem: npm") {
		return "npm"
	}
	return ""
}

func isImageTarget(fp string) bool {
	if fp == "" {
		return false
	}
	if strings.Contains(fp, "go.mod") || strings.Contains(fp, "go.sum") ||
		strings.Contains(fp, "package") || strings.Contains(fp, "yarn") ||
		strings.Contains(fp, "pnpm") {
		return false
	}
	if strings.Contains(fp, ":") && (strings.Contains(fp, "/") || strings.HasPrefix(fp, "sha256:")) {
		return true
	}
	return false
}

func isStdlib(mod string) bool {
	if !strings.Contains(mod, ".") {
		return true
	}
	for _, p := range stdlibPrefix {
		if mod == strings.TrimSuffix(p, "/") || strings.HasPrefix(mod, p) {
			return true
		}
	}
	return false
}

func pinFrom(blob string) string {
	if m := fixVer.FindStringSubmatch(blob); len(m) == 2 {
		return m[1]
	}
	return ""
}

func npmPrefix(repoPath string) string {
	if fileExists(filepath.Join(repoPath, "package.json")) {
		return repoPath
	}
	for _, d := range []string{"frontend", "ui", "web", "app"} {
		if fileExists(filepath.Join(repoPath, d, "package.json")) {
			return filepath.Join(repoPath, d)
		}
	}
	return repoPath
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

var renovateTitle = regexp.MustCompile(`(?i)^([a-z0-9][a-z0-9_-]*):\s+(\S+)\s+(\S+)\s+→\s+(\S+)\s+\(([a-z0-9_-]+)\)$`)

type renovateUpdate struct {
	id, manager, name, current, next, updateType, file string
	vuln                                               bool
}

func isRenovate(f models.Finding) bool {
	return strings.EqualFold(strings.TrimSpace(f.ToolName), "renovate")
}

func parseRenovateFinding(f models.Finding) (renovateUpdate, bool) {
	m := renovateTitle.FindStringSubmatch(strings.TrimSpace(f.Title))
	if len(m) != 6 {
		return renovateUpdate{}, false
	}
	desc := strings.ToLower(f.Description + " " + f.Title)
	return renovateUpdate{
		id:         f.ID,
		manager:    strings.ToLower(m[1]),
		name:       m[2],
		current:    m[3],
		next:       m[4],
		updateType: strings.ToLower(m[5]),
		file:       strings.TrimSpace(f.FilePath),
		vuln: strings.Contains(desc, "known vulnerability") ||
			strings.Contains(desc, "vulnerability alert") ||
			f.Severity == models.SeverityHigh || f.Severity == models.SeverityCritical,
	}, true
}

func renovateRank(t string) int {
	switch strings.ToLower(t) {
	case "patch", "pin":
		return 0
	case "digest":
		return 1
	case "minor":
		return 2
	case "major":
		return 3
	default:
		return 4
	}
}

func renovateAuto(u renovateUpdate) bool {
	if u.current == "" || u.next == "" || u.current == u.next {
		return false
	}
	if renovateHandsOff(u) {
		return false
	}
	switch u.updateType {
	case "patch", "pin":
		return true
	case "digest":
		return true
	case "minor":
		return u.vuln
	default:
		return false
	}
}

func pickConservative(updates []renovateUpdate) (apply renovateUpdate, skip []renovateUpdate, ok bool) {
	if len(updates) == 0 {
		return renovateUpdate{}, nil, false
	}
	best := -1
	for i, u := range updates {
		if !renovateAuto(u) {
			skip = append(skip, u)
			continue
		}
		if best < 0 || renovateRank(u.updateType) < renovateRank(updates[best].updateType) {
			if best >= 0 {
				skip = append(skip, updates[best])
			}
			best = i
			continue
		}
		skip = append(skip, u)
	}
	if best < 0 {
		return renovateUpdate{}, skip, false
	}
	return updates[best], skip, true
}

func applyRenovateBumps(ctx context.Context, repoPath string, byKey map[string][]renovateUpdate, res *Result) {
	applied := 0
	for _, group := range byKey {
		choice, skipped, ok := pickConservative(group)
		if ok {
			if err := applyRenovateUpdate(ctx, repoPath, choice); err != nil {
				// leave the whole group open for the agent
				continue
			}
			applied++
			res.Kept[choice.id] = "bumped " + choice.name + " " + choice.current + " → " + choice.next + " (" + choice.updateType + ")"
			for _, s := range skipped {
				res.Kept[s.id] = "kept " + s.name + " on " + choice.next + " instead of " + s.next + " (smaller safe bump)"
			}
			continue
		}
		// No conservative auto-apply (majors, or minor without a vuln).
		// Leave open — never mute Renovate.
	}
	if applied > 0 {
		if fileExists(filepath.Join(repoPath, "go.mod")) {
			tctx, cancel := context.WithTimeout(ctx, 90*time.Second)
			_ = runInRepo(tctx, repoPath, "go", "mod", "tidy")
			cancel()
			res.Files = append(res.Files, "go.mod", "go.sum")
		}
		res.Message = strings.TrimSpace(res.Message + " applied conservative Renovate bumps")
	}
}

func renovateHandsOff(u renovateUpdate) bool {
	mgr := strings.ToLower(strings.TrimSpace(u.manager))
	if mgr == "helm" || mgr == "helmv3" || mgr == "helm-values" {
		return true
	}
	if ProtectedRel(u.file) {
		return true
	}
	name := strings.ToLower(strings.TrimSpace(u.name))
	if name == "go" || strings.Contains(name, "golang") {
		return !sameReleaseLine(u.current, u.next)
	}
	for _, img := range []string{"postgres", "postgresql", "redis", "nginx"} {
		if name == img || strings.HasPrefix(name, img) || strings.Contains(name, "/"+img) {
			return u.updateType == "major" || u.updateType == "minor"
		}
	}
	return false
}

func sameReleaseLine(cur, next string) bool {
	cm, okc := majorMinor(cur)
	nm, okn := majorMinor(next)
	return okc && okn && cm == nm
}

func majorMinor(v string) (string, bool) {
	v = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(v)), "v")
	v = strings.TrimPrefix(v, "go")
	parts := strings.Split(v, ".")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", false
	}
	return parts[0] + "." + parts[1], true
}

func applyRenovateUpdate(ctx context.Context, repoPath string, u renovateUpdate) error {
	if renovateHandsOff(u) {
		return os.ErrInvalid
	}
	switch u.manager {
	case "gomod":
		if strings.EqualFold(u.name, "go") {
			return rewriteGoDirective(repoPath, u.next)
		}
		bctx, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		return runInRepo(bctx, repoPath, "go", "get", u.name+"@"+strings.TrimPrefix(u.next, "@"))
	case "npm":
		prefix := npmPrefix(repoPath)
		args := []string{"install", u.name + "@" + strings.TrimPrefix(u.next, "@"), "--no-fund", "--no-audit"}
		if prefix != "" && prefix != repoPath {
			args = append(args, "--prefix", prefix)
		}
		bctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()
		return runInRepo(bctx, repoPath, "npm", args...)
	default:
		if u.file == "" || u.current == u.next {
			return os.ErrInvalid
		}
		return replaceInRepoFile(repoPath, u.file, u.current, u.next)
	}
}

func rewriteGoDirective(repoPath, next string) error {
	path := filepath.Join(repoPath, "go.mod")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	next = strings.TrimPrefix(next, "go")
	next = strings.TrimSpace(next)
	cur := ""
	if m := regexp.MustCompile(`(?m)^go\s+(\d+\.\d+(?:\.\d+)?)\s*$`).FindSubmatch(data); len(m) == 2 {
		cur = string(m[1])
	}
	if cur != "" && !sameReleaseLine(cur, next) {
		return os.ErrInvalid
	}
	re := regexp.MustCompile(`(?m)^go\s+\d+\.\d+(?:\.\d+)?\s*$`)
	out := re.ReplaceAll(data, []byte("go "+next))
	if string(out) == string(data) {
		return os.ErrInvalid
	}
	return os.WriteFile(path, out, 0o644)
}

func replaceInRepoFile(repoPath, rel, old, new string) error {
	if old == "" || new == "" || old == new {
		return os.ErrInvalid
	}
	path := rel
	if !filepath.IsAbs(rel) {
		path = filepath.Join(repoPath, rel)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !strings.Contains(string(data), old) {
		return os.ErrNotExist
	}
	out := strings.ReplaceAll(string(data), old, new)
	return os.WriteFile(path, []byte(out), 0o644)
}
