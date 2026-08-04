# OpenCode Agentic Remediation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a remediation path where OpenCode owns an agentic session — Wolf hands it a whole scan's findings and a repo, and it triages, fixes, and self-verifies across turns under Wolf's budget and approval gates.

**Architecture:** A new `internal/remediate/` subsystem, separate from the per-finding `internal/fix/` engine chain. Two stateless `opencode run` invocations per session: a read-only triage run that emits a plan, then a scoped-write run that executes the approved plan. Nothing is held open across an approval gate, so pending approvals are database rows that survive restarts. Results land as a branch and PR carrying the scan delta.

**Tech Stack:** Go 1.26.5, chi router, sqlx, SQLite + Postgres, OpenCode CLI (containerized), React + TypeScript UI, Vitest, Playwright.

**Design spec:** `docs/superpowers/specs/2026-08-03-opencode-agentic-remediation-design.md`

## Global Constraints

Every task's requirements implicitly include this section.

- **Go version:** 1.26.5 (per `fixer/versions.env`).
- **`internal/fix/` is not modified by any task.** The existing `claude-code`, `codex`, `api`, and `custom` engines and the `SubprocessEngine` interface stay exactly as they are. `internal/fix/workspace` and `internal/fix/pr` are *consumed*, never edited.
- **Fail-closed:** `WOLF_REMEDIATE_ENABLED` defaults to `false`. No remediation runs until an admin opts in.
- **Hard deny list is injected in both gated and yolo mode.** Yolo disables Wolf's approval gates, never OpenCode's permission rules. Under `--auto`, `ask` degrades to allow — so anything that must not happen is expressed as `deny`, never `ask`.
- **Credentials never appear on a command line** (visible in `ps`) and never in an image layer. The only injection path is the `OPENCODE_AUTH_CONTENT` environment variable.
- **Turns are the only meter in v1.** `Usage` carries `Tokens` and `Cost` fields that v1 leaves zero; the schema is fixed now so adding a cost meter later is not a migration.
- **One migration file, both dialects.** `internal/db/migrations/051_opencode_remediation.sql` is embedded once in `internal/db/sqlite.go` and referenced from both `SQLiteStore.Migrate()` and `PostgresStore.Migrate()`, matching migration 050.
- **Scopes are reused, not minted.** Remediation endpoints use `read:fixes` / `write:fixes`. Writing the service-identity credential additionally requires `admin`.
- **Redaction:** injected credentials must never appear in `remediation_events`, SSE output, or a PR body. Follow the existing `*_never_render` fixture convention.
- **Commit style:** conventional commits (`feat(remediate):`, `test(remediate):`). Every task ends with a commit and a green `go build ./...`.
- **Regenerate the scanner lock whenever you add or change Go source.** `scanners/scanner-lock.yaml` digests every file under `internal/` — they are fixer build inputs, because the fixer image compiles Wolf's own source. Adding a package therefore makes the lock stale and fails `TestRepositoryLockIsDeterministicGolden` and `TestDefaultManifestValidation`. Run `go run ./cmd/scannertools lock` and include the result in your commit. `go test ./internal/remediate/...` alone will not catch this — only the full `go test ./...` will.

## Spike: COMPLETE — findings that change this plan

The spike ran `opencode-ai@1.18.11` against a real fixture. Full writeup:
`docs/superpowers/specs/2026-08-03-opencode-spike-findings.md`. Four
corrections are already folded into the task text below, but they are listed
here because three of them would each have been a silent, total failure:

1. **The turn signal is `step_finish`, not `assistant`.** There is no
   `assistant` event type. The original meter would have counted zero turns
   forever and never stopped the agent.
2. **`OPENCODE_CONFIG` loads a config file, not `OPENCODE_CONFIG_DIR`** (which
   points at agents/commands/plugins). As originally written, the
   golden-tested permission document would never have been loaded and every
   run would have used default permissions.
3. **`opencode run` hangs indefinitely on an inherited stdin.** Reproduced at
   120s, 150s and 240s with zero output; `< /dev/null` fixes it. Every
   containerized run would have hung until the wall-clock timeout.
4. **A repo-supplied `opencode.json` overrides the injected one** (proven by
   A/B). The scanned repository is untrusted by definition, so any repo can
   currently disable every permission control. Task 4a below strips it.

Confirmed working: the root `"*": "deny"` wildcard genuinely denies unlisted
tools, and `--auto` exists in 1.18.11 (not in 1.15.7), so Task 7's pin is
required.

`meter/testdata/session-basic.jsonl` is committed. No task is spike-blocked.

## File Structure

**New Go packages**

| Path | Responsibility |
|---|---|
| `internal/remediate/plan/plan.go` | Plan schema, JSON parsing, validation |
| `internal/remediate/meter/meter.go` | `Meter` interface, `Usage`, turn meter over the event stream |
| `internal/remediate/permission/permission.go` | Builds the per-run `opencode.json` permission document |
| `internal/remediate/driver/driver.go` | `Driver` interface, `Event` type, request/result types |
| `internal/remediate/driver/exec.go` | `execDriver` — shells out to `opencode run` in a container |
| `internal/remediate/driver/fake.go` | `FakeDriver` — replays recorded streams, used by orchestrator tests |
| `internal/remediate/gate/gate.go` | Gate policy evaluation (plan gate, patch gate, yolo) |
| `internal/remediate/credential/credential.go` | Resolution order, `OPENCODE_AUTH_CONTENT` rendering |
| `internal/remediate/credential/login.go` | Ephemeral login container, device-code URL scraping |
| `internal/remediate/session.go` | Session orchestration: two-phase flow, state transitions |
| `internal/remediate/config.go` | `WOLF_REMEDIATE_*` environment configuration |

**New DB files**

| Path | Responsibility |
|---|---|
| `internal/db/migrations/051_opencode_remediation.sql` | Four tables + indexes |
| `internal/db/remediation_repository.go` | CRUD for sessions, plans, patches, events |

**New API/UI files**

| Path | Responsibility |
|---|---|
| `internal/api/routes/remediations.go` | Session CRUD, gate approve/reject, SSE stream |
| `internal/api/routes/opencode_providers.go` | Provider connect/disconnect, device-code SSE |
| `ui/src/components/remediation/*` | Session list, plan review, patch review, provider connect |

**Modified files**

| Path | Change |
|---|---|
| `internal/models/types.go` | Add `KeyTypeOpenCodeAuth`, `ServiceUserID` |
| `internal/models/remediation.go` | New — session/plan/patch/event structs |
| `internal/db/store.go` | Add remediation methods to `Store` |
| `internal/db/sqlite.go` | Embed + run migration 051 |
| `internal/db/postgres.go` | Run migration 051 |
| `internal/api/server.go` | Register `/remediations` and provider routes |
| `internal/scannerbuild/build.go` | Add `opencode` to `FixerVariants` |
| `fixer/Dockerfile.opencode` | New — OpenCode CLI fixer variant |
| `.env.example` | Document `WOLF_REMEDIATE_*` |

---

# Phase 1 — The Loop, End to End

Single provider, API-key auth only, both gates off, artifact output only. Proves scan → plan run → execute run → rescan → delta.

### Task 1: Plan schema and parsing

The triage run emits JSON on stdout. This package defines what a valid plan looks like and rejects malformed ones, so a hallucinated plan fails fast instead of driving a write run.

**Files:**
- Create: `internal/remediate/plan/plan.go`
- Test: `internal/remediate/plan/plan_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `plan.Plan`, `plan.Item`, `plan.Parse(data []byte) (*Plan, error)`, `Plan.FindingIDs() []string`.

- [ ] **Step 1: Write the failing test**

```go
package plan

import "testing"

func TestParseValidPlan(t *testing.T) {
	data := []byte(`{
		"summary": "7 of 23 findings are actionable",
		"items": [
			{"finding_id": "f-1", "action": "fix", "rationale": "SQL injection in user query", "files": ["db/user.go"]},
			{"finding_id": "f-2", "action": "skip", "rationale": "test fixture, not shipped"}
		]
	}`)

	p, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(p.Items) != 2 {
		t.Fatalf("Items = %d, want 2", len(p.Items))
	}
	if got := p.Items[0].Action; got != ActionFix {
		t.Errorf("Items[0].Action = %q, want %q", got, ActionFix)
	}
	if got := p.FindingIDs(); len(got) != 1 || got[0] != "f-1" {
		t.Errorf("FindingIDs() = %v, want [f-1]", got)
	}
}

func TestParseRejectsInvalid(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{"empty items", `{"summary":"none","items":[]}`},
		{"unknown action", `{"summary":"s","items":[{"finding_id":"f-1","action":"delete","rationale":"r"}]}`},
		{"missing finding_id", `{"summary":"s","items":[{"action":"fix","rationale":"r"}]}`},
		{"missing rationale", `{"summary":"s","items":[{"finding_id":"f-1","action":"fix"}]}`},
		{"not json", `this is not json`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Parse([]byte(tt.data)); err == nil {
				t.Fatalf("Parse(%s) succeeded, want error", tt.data)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/remediate/plan/ -v`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Write minimal implementation**

```go
// Package plan defines the triage plan an OpenCode session emits before any
// code is written. Parsing is strict: a plan that drives a write run must be
// well-formed, so malformed output fails here rather than downstream.
package plan

import (
	"encoding/json"
	"fmt"
)

// Action is what the agent intends to do with a finding.
type Action string

const (
	ActionFix  Action = "fix"
	ActionSkip Action = "skip"
)

// Item is one finding's disposition.
type Item struct {
	FindingID string   `json:"finding_id"`
	Action    Action   `json:"action"`
	Rationale string   `json:"rationale"`
	Files     []string `json:"files,omitempty"`
}

// Plan is the triage run's output.
type Plan struct {
	Summary string `json:"summary"`
	Items   []Item `json:"items"`
}

// Parse decodes and validates a plan. Unknown fields are rejected so a
// schema drift in OpenCode's output surfaces as an error, not a silent
// partial plan.
func Parse(data []byte) (*Plan, error) {
	var p Plan
	dec := json.NewDecoder(bytesReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("decode plan: %w", err)
	}
	if len(p.Items) == 0 {
		return nil, fmt.Errorf("plan has no items")
	}
	for i, item := range p.Items {
		if item.FindingID == "" {
			return nil, fmt.Errorf("item %d: missing finding_id", i)
		}
		if item.Rationale == "" {
			return nil, fmt.Errorf("item %d: missing rationale", i)
		}
		switch item.Action {
		case ActionFix, ActionSkip:
		default:
			return nil, fmt.Errorf("item %d: unknown action %q", i, item.Action)
		}
	}
	return &p, nil
}

// FindingIDs returns the IDs the plan intends to fix, in plan order.
func (p *Plan) FindingIDs() []string {
	ids := make([]string, 0, len(p.Items))
	for _, item := range p.Items {
		if item.Action == ActionFix {
			ids = append(ids, item.FindingID)
		}
	}
	return ids
}
```

Add the `bytesReader` helper as `bytes.NewReader` — import `"bytes"` and replace `bytesReader(data)` with `bytes.NewReader(data)`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/remediate/plan/ -v`
Expected: PASS — both tests, all five sub-cases.

- [ ] **Step 5: Commit**

```bash
git add internal/remediate/plan/
git commit -m "feat(remediate): plan schema and strict parsing"
```

---

### Task 2: Turn meter

**SPIKE COMPLETE — unblocked.** `internal/remediate/meter/testdata/session-basic.jsonl` is committed (6 events, 2 turns), and the turn signal is confirmed as `step_finish`. See `docs/superpowers/specs/2026-08-03-opencode-spike-findings.md`.

Wolf meters turns itself because `opencode run` has no max-turns flag. This is the only thing standing between a budget setting and an unbounded agent.

**Files:**
- Create: `internal/remediate/meter/meter.go`
- Test: `internal/remediate/meter/meter_test.go`
- Test fixture: `internal/remediate/meter/testdata/session-basic.jsonl` (from spike)

**Interfaces:**
- Consumes: nothing.
- Produces: `meter.Meter` interface, `meter.Usage` struct, `meter.NewTurns(budget int) Meter`.

- [ ] **Step 1: Write the failing test**

The turn signal is `step_finish`, confirmed against opencode-ai@1.18.11.

```go
package meter

import (
	"bufio"
	"encoding/json"
	"os"
	"testing"
)

func loadFixture(t *testing.T, name string) []Event {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()

	var events []Event
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var e Event
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			t.Fatalf("unmarshal event: %v", err)
		}
		events = append(events, e)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan fixture: %v", err)
	}
	return events
}

func TestTurnsCountsFixtureStream(t *testing.T) {
	events := loadFixture(t, "session-basic.jsonl")
	m := NewTurns(100)
	for _, e := range events {
		if m.Observe(e) {
			t.Fatal("budget of 100 exhausted on the basic fixture")
		}
	}
	if got := m.Usage().Turns; got == 0 {
		t.Fatal("Usage().Turns = 0, want > 0 — turn signal not recognized")
	}
}

func TestTurnsStopsAtBudget(t *testing.T) {
	m := NewTurns(2)
	turn := Event{Type: "step_finish"}

	if m.Observe(turn) {
		t.Fatal("exhausted after turn 1, budget is 2")
	}
	if !m.Observe(turn) {
		t.Fatal("not exhausted after turn 2, budget is 2")
	}
	if got := m.Usage().Turns; got != 2 {
		t.Errorf("Usage().Turns = %d, want 2", got)
	}
}

func TestTurnsIgnoresNonTurnEvents(t *testing.T) {
	m := NewTurns(1)
	for _, notATurn := range []string{"step_start", "text", "tool_use"} {
		if m.Observe(Event{Type: notATurn}) {
			t.Fatalf("%s counted as a turn", notATurn)
		}
	}
	if got := m.Usage().Turns; got != 0 {
		t.Errorf("Usage().Turns = %d, want 0", got)
	}
}

// step_finish carries the step's own token and cost totals, so the turns
// meter accumulates spend as it goes — no separate cost meter is needed.
func TestUsageAccumulatesTokensAndCost(t *testing.T) {
	m := NewTurns(10)

	var e Event
	e.Type = "step_finish"
	e.Part.Tokens.Total = 34116
	e.Part.Cost = 0.25
	m.Observe(e)
	m.Observe(e)

	u := m.Usage()
	if u.Turns != 2 {
		t.Errorf("Turns = %d, want 2", u.Turns)
	}
	if u.Tokens != 68232 {
		t.Errorf("Tokens = %d, want 68232", u.Tokens)
	}
	if u.Cost != 0.5 {
		t.Errorf("Cost = %v, want 0.5", u.Cost)
	}
}

// The real fixture must report both turns and non-zero tokens — a meter that
// silently matched nothing would still pass a turns-only assertion.
func TestFixtureReportsTwoTurnsWithTokens(t *testing.T) {
	m := NewTurns(100)
	for _, e := range loadFixture(t, "session-basic.jsonl") {
		m.Observe(e)
	}
	u := m.Usage()
	if u.Turns != 2 {
		t.Errorf("Turns = %d, want 2 — the captured fixture has two step_finish events", u.Turns)
	}
	if u.Tokens == 0 {
		t.Error("Tokens = 0 — token totals were not read from step_finish")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/remediate/meter/ -v`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Write minimal implementation**

```go
// Package meter bounds an OpenCode session. OpenCode's run command has no
// max-turns flag, so Wolf counts turns from the JSON event stream and stops
// the session itself.
package meter

// Event is one decoded record from `opencode run --format json`. Only the
// fields Wolf needs are modeled; the rest of the payload is ignored.
//
// Shape confirmed empirically against opencode-ai@1.18.11 — see
// docs/superpowers/specs/2026-08-03-opencode-spike-findings.md. Observed
// types are step_start, text, tool_use, and step_finish.
type Event struct {
	Type string `json:"type"`
	Part struct {
		// Reason is the step's stop reason, e.g. "stop".
		Reason string `json:"reason"`
		Tokens struct {
			Total     int64 `json:"total"`
			Input     int64 `json:"input"`
			Output    int64 `json:"output"`
			Reasoning int64 `json:"reasoning"`
		} `json:"tokens"`
		Cost float64 `json:"cost"`
	} `json:"part"`
}

// Usage is what a session spent. All three fields are populated by the turns
// meter: step_finish carries tokens and cost per step, so there is no reason
// to defer them to a separate meter.
type Usage struct {
	Turns  int
	Tokens int64
	Cost   float64
}

// Meter decides when a run has spent its budget.
type Meter interface {
	// Observe consumes one event and reports whether the budget is now spent.
	Observe(event Event) (exhausted bool)
	Usage() Usage
}

// turnSignal is the event type that marks a completed agent turn. Confirmed
// empirically against 1.18.11: a two-turn session emits step_start, tool_use,
// step_finish, step_start, text, step_finish. Counting step_finish counts
// turns. There is no "assistant" event type — a meter written against one
// counts zero turns forever and never stops the agent.
const turnSignal = "step_finish"

type turns struct {
	budget int
	count  int
	tokens int64
	cost   float64
}

// NewTurns returns a Meter that stops after budget turns. A budget <= 0 is
// treated as unbounded, which callers must not use in production — the
// session config clamps it before we get here.
func NewTurns(budget int) Meter { return &turns{budget: budget} }

func (t *turns) Observe(event Event) bool {
	if event.Type != turnSignal {
		return false
	}
	t.count++
	// step_finish reports the step's own token and cost totals, so accumulate
	// them here rather than deferring spend tracking to a separate meter.
	t.tokens += event.Part.Tokens.Total
	t.cost += event.Part.Cost
	return t.budget > 0 && t.count >= t.budget
}

func (t *turns) Usage() Usage {
	return Usage{Turns: t.count, Tokens: t.tokens, Cost: t.cost}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/remediate/meter/ -v`
Expected: PASS — four tests.

- [ ] **Step 5: Commit**

```bash
git add internal/remediate/meter/
git commit -m "feat(remediate): turn meter over the opencode event stream"
```

---

### Task 3: Permission document builder

This is the highest-value security surface in the subsystem. The golden-file test is the only thing between a config typo and an agent with unrestricted `bash`.

**Files:**
- Create: `internal/remediate/permission/permission.go`
- Test: `internal/remediate/permission/permission_test.go`
- Test fixtures: `internal/remediate/permission/testdata/triage.json`, `testdata/execute.json`

**Interfaces:**
- Consumes: nothing.
- Produces: `permission.Triage() ([]byte, error)`, `permission.Execute() ([]byte, error)`.

- [ ] **Step 1: Write the failing test**

```go
package permission

import (
	"encoding/json"
	"os"
	"testing"
)

func assertGolden(t *testing.T, got []byte, golden string) {
	t.Helper()
	want, err := os.ReadFile("testdata/" + golden)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	var gotDoc, wantDoc any
	if err := json.Unmarshal(got, &gotDoc); err != nil {
		t.Fatalf("generated doc is not valid JSON: %v", err)
	}
	if err := json.Unmarshal(want, &wantDoc); err != nil {
		t.Fatalf("golden is not valid JSON: %v", err)
	}
	gotNorm, _ := json.Marshal(gotDoc)
	wantNorm, _ := json.Marshal(wantDoc)
	if string(gotNorm) != string(wantNorm) {
		t.Errorf("permission document drift.\n got: %s\nwant: %s", gotNorm, wantNorm)
	}
}

func TestTriageIsReadOnly(t *testing.T) {
	doc, err := Triage()
	if err != nil {
		t.Fatalf("Triage: %v", err)
	}
	assertGolden(t, doc, "triage.json")

	var parsed struct {
		Permission struct {
			Edit any `json:"edit"`
		} `json:"permission"`
	}
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Permission.Edit != "deny" {
		t.Errorf("triage edit = %v, want \"deny\" — triage must never write", parsed.Permission.Edit)
	}
}

func TestExecuteDeniesDangerousPaths(t *testing.T) {
	doc, err := Execute()
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	assertGolden(t, doc, "execute.json")
}

// The hard deny list must hold under --auto, where "ask" degrades to allow.
// Anything that must not happen has to be "deny", never "ask".
func TestNoAskRulesForDangerousActions(t *testing.T) {
	doc, err := Execute()
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var parsed struct {
		Permission struct {
			Edit map[string]string `json:"edit"`
			Bash map[string]string `json:"bash"`
		} `json:"permission"`
	}
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	mustDeny := map[string][]string{
		"edit": {".github/**", "**/*.pem", "**/*.key"},
		"bash": {"rm -rf *", "curl *", "sudo *"},
	}
	for tool, patterns := range mustDeny {
		rules := parsed.Permission.Edit
		if tool == "bash" {
			rules = parsed.Permission.Bash
		}
		for _, p := range patterns {
			if rules[p] != "deny" {
				t.Errorf("%s[%q] = %q, want \"deny\"", tool, p, rules[p])
			}
		}
	}

	// No rule anywhere may be "ask": under --auto it silently becomes allow.
	for pattern, effect := range parsed.Permission.Bash {
		if effect == "ask" {
			t.Errorf("bash[%q] = \"ask\" — degrades to allow under --auto", pattern)
		}
	}
}

// bash defaults to deny so a command nobody enumerated is refused rather
// than permitted. This is the difference between an allowlist and a
// blocklist, and it is the whole reason yolo mode is survivable.
func TestExecuteBashDefaultsToDeny(t *testing.T) {
	doc, err := Execute()
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var parsed struct {
		Permission struct {
			Bash map[string]string `json:"bash"`
		} `json:"permission"`
	}
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := parsed.Permission.Bash["*"]; got != "deny" {
		t.Fatalf("bash[\"*\"] = %q, want \"deny\"", got)
	}
	// Commands nobody thought to denylist must still be refused.
	for _, unlisted := range []string{"nc *", "ssh *", "chmod *", "dd *", "base64 *"} {
		if effect, present := parsed.Permission.Bash[unlisted]; present && effect == "allow" {
			t.Errorf("bash[%q] = allow, want refusal via the deny default", unlisted)
		}
	}
	// The build tooling the agent legitimately needs stays allowed.
	for _, allowed := range []string{"git *", "npm *", "go *"} {
		if parsed.Permission.Bash[allowed] != "allow" {
			t.Errorf("bash[%q] = %q, want \"allow\"", allowed, parsed.Permission.Bash[allowed])
		}
	}
}
```

- [ ] **Step 2: Create the golden fixtures**

`internal/remediate/permission/testdata/triage.json`:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "permission": {
    "edit": "deny",
    "bash": {
      "*": "deny",
      "git log *": "allow",
      "git diff *": "allow",
      "git show *": "allow",
      "grep *": "allow",
      "cat *": "allow",
      "ls *": "allow",
      "find *": "allow"
    },
    "external_directory": { "*": "deny" }
  }
}
```

`internal/remediate/permission/testdata/execute.json`:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "permission": {
    "edit": {
      "*": "allow",
      ".github/**": "deny",
      "**/*.pem": "deny",
      "**/*.key": "deny"
    },
    "bash": {
      "*": "deny",
      "git add *": "allow",
      "git commit *": "allow",
      "git checkout *": "allow",
      "git diff *": "allow",
      "git log *": "allow",
      "git status *": "allow",
      "go build *": "allow",
      "go test *": "allow",
      "go vet *": "allow",
      "gofmt *": "allow",
      "npm test *": "allow",
      "npm run *": "allow",
      "make *": "allow",
      "pytest *": "allow",
      "rm -rf *": "deny",
      "curl *": "deny",
      "wget *": "deny",
      "sudo *": "deny"
    },
    "external_directory": { "*": "deny" }
  }
}
```

Allowlist entries are **subcommands, not bare binaries**. `git *` would permit
`git push` and `git remote add`; `npm *` would permit `npm install`, which
fetches over the network and runs `postinstall` scripts; `go *` would permit
`go get` and `go run`. Each is a general-purpose egress and
arbitrary-code-execution primitive, which would reopen exactly what the deny
default closes. The agent needs to edit, build, test, and commit — it never
needs to push, install, or fetch. Wolf pushes the branch itself from the host
via `pr.PushBranch` (Task 13), outside the container.

The bash default is `deny`, not `ask`. Under `--auto` an `ask` degrades to
allow, which would permit any dangerous command nobody thought to denylist
(`nc`, `ssh`, `chmod`, `dd`, `base64`). Default-deny plus an allowlist refuses
the unlisted command instead. The explicit denies after the allowlist are
belt-and-braces: `rm -rf *` would otherwise be caught by `*: deny` anyway, but
naming it documents intent and survives someone widening the allowlist later.

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/remediate/permission/ -v`
Expected: FAIL — package does not exist.

- [ ] **Step 4: Write minimal implementation**

```go
// Package permission builds the opencode.json document Wolf writes for each
// run. OpenCode evaluates rules deny-wins, last-match-wins, and `--auto`
// auto-approves anything not explicitly denied — so every rule that must hold
// under yolo mode is a "deny", never an "ask".
package permission

import "encoding/json"

const schemaURL = "https://opencode.ai/config.json"

type document struct {
	Schema     string     `json:"$schema"`
	Permission permission `json:"permission"`
}

type permission struct {
	Edit              any               `json:"edit"`
	Bash              map[string]string `json:"bash"`
	ExternalDirectory map[string]string `json:"external_directory"`
}

// confineToWorktree denies the agent any path outside its working tree.
var confineToWorktree = map[string]string{"*": "deny"}

// Triage returns the read-only permission document for the plan run. Edit is
// denied outright and bash is an allowlist of inspection commands.
func Triage() ([]byte, error) {
	return json.MarshalIndent(document{
		Schema: schemaURL,
		Permission: permission{
			Edit: "deny",
			Bash: map[string]string{
				"*":          "deny",
				"git log *":  "allow",
				"git diff *": "allow",
				"git show *": "allow",
				"grep *":     "allow",
				"cat *":      "allow",
				"ls *":       "allow",
				"find *":     "allow",
			},
			ExternalDirectory: confineToWorktree,
		},
	}, "", "  ")
}

// Execute returns the scoped-write permission document for the fix run. The
// deny entries are the hard deny list: they are injected in gated and yolo
// mode alike. .github/** is denied because an agent that can edit CI can
// exfiltrate on its next run.
func Execute() ([]byte, error) {
	return json.MarshalIndent(document{
		Schema: schemaURL,
		Permission: permission{
			Edit: map[string]string{
				"*":           "allow",
				".github/**":  "deny",
				"**/*.pem":    "deny",
				"**/*.key":    "deny",
			},
			Bash: map[string]string{
				// Default-deny: an unlisted command is refused, not allowed.
				// Under --auto an "ask" would degrade to allow, so the
				// fallback must be deny.
				"*": "deny",
				// Subcommands, not bare binaries. "git *" would permit
				// git push; "npm *" would permit npm install (network plus
				// postinstall scripts); "go *" would permit go get and
				// go run. The agent edits, builds, tests, and commits — it
				// never pushes, installs, or fetches. Wolf pushes from the
				// host via pr.PushBranch, outside this container.
				"git add *":      "allow",
				"git commit *":   "allow",
				"git checkout *": "allow",
				"git diff *":     "allow",
				"git log *":      "allow",
				"git status *":   "allow",
				"go build *":     "allow",
				"go test *":      "allow",
				"go vet *":       "allow",
				"gofmt *":        "allow",
				"npm test *":     "allow",
				"npm run *":      "allow",
				"make *":         "allow",
				"pytest *":       "allow",
				// Redundant under *: deny, but kept to document intent if the
				// allowlist is ever widened.
				"rm -rf *": "deny",
				"curl *":   "deny",
				"wget *":   "deny",
				"sudo *":   "deny",
			},
			ExternalDirectory: confineToWorktree,
		},
	}, "", "  ")
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/remediate/permission/ -v`
Expected: PASS — four tests: `TestTriageIsReadOnly`, `TestExecuteDeniesDangerousPaths`, `TestNoAskRulesForDangerousActions`, `TestExecuteBashDefaultsToDeny`.

- [ ] **Step 6: Commit**

```bash
git add internal/remediate/permission/
git commit -m "feat(remediate): opencode permission documents with golden tests"
```

---

### Task 4: Driver interface, fake driver, and exec driver

**Depends on Task 2** (uses `meter.Event`) and **Task 1** (returns `plan.Plan`).

The `Driver` boundary is what lets every later task be tested without containers. Build the fake alongside the interface, before the real one.

**Files:**
- Create: `internal/remediate/driver/driver.go`, `internal/remediate/driver/fake.go`, `internal/remediate/driver/exec.go`
- Test: `internal/remediate/driver/fake_test.go`, `internal/remediate/driver/exec_test.go`

**Interfaces:**
- Consumes: `plan.Plan`, `plan.Parse`, `meter.Meter`, `meter.Event`, `meter.Usage`, `permission.Triage`, `permission.Execute`.
- Produces: `driver.Driver` interface, `driver.PlanRequest`, `driver.ExecuteRequest`, `driver.PatchSeries`, `driver.Patch`, `driver.NewFake(events []meter.Event, p *plan.Plan) *Fake`, `driver.NewExec(cfg ExecConfig) Driver`.

- [ ] **Step 1: Write the failing test**

```go
package driver

import (
	"context"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/remediate/meter"
	"github.com/alphabravocompany/thewolf/internal/remediate/plan"
)

func TestFakeReturnsPlanAndUsage(t *testing.T) {
	want := &plan.Plan{
		Summary: "1 actionable",
		Items:   []plan.Item{{FindingID: "f-1", Action: plan.ActionFix, Rationale: "sqli"}},
	}
	f := NewFake([]meter.Event{{Type: "assistant"}, {Type: "assistant"}}, want)

	got, usage, err := f.Plan(context.Background(), PlanRequest{
		WorktreePath: "/tmp/wt",
		MaxTurns:     10,
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if got.Summary != want.Summary {
		t.Errorf("Summary = %q, want %q", got.Summary, want.Summary)
	}
	if usage.Turns != 2 {
		t.Errorf("Usage.Turns = %d, want 2", usage.Turns)
	}
}

func TestFakeStopsAtTurnBudget(t *testing.T) {
	events := []meter.Event{{Type: "assistant"}, {Type: "assistant"}, {Type: "assistant"}}
	f := NewFake(events, &plan.Plan{
		Summary: "s",
		Items:   []plan.Item{{FindingID: "f-1", Action: plan.ActionFix, Rationale: "r"}},
	})

	_, usage, err := f.Plan(context.Background(), PlanRequest{WorktreePath: "/tmp/wt", MaxTurns: 2})
	if err == nil {
		t.Fatal("Plan succeeded, want ErrBudgetExhausted")
	}
	if usage.Turns != 2 {
		t.Errorf("Usage.Turns = %d, want 2 (stopped at budget)", usage.Turns)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/remediate/driver/ -v`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Write the interface and types**

```go
// Package driver runs the two phases of a remediation session. The Driver
// boundary exists so session orchestration is testable without containers.
package driver

import (
	"context"
	"errors"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/remediate/meter"
	"github.com/alphabravocompany/thewolf/internal/remediate/plan"
)

// ErrBudgetExhausted is returned when a run hits its turn budget before
// producing a usable result.
var ErrBudgetExhausted = errors.New("turn budget exhausted")

// PlanRequest is the read-only triage run.
type PlanRequest struct {
	WorktreePath string
	Findings     []models.Finding
	MaxTurns     int
	Provider     string
	Model        string
	// AuthContent is the OPENCODE_AUTH_CONTENT payload. It is passed by
	// environment only — never as a command-line argument.
	AuthContent string
	// OnEvent, when set, receives every decoded event for persistence and SSE.
	OnEvent func(meter.Event)
}

// ExecuteRequest is the scoped-write fix run.
type ExecuteRequest struct {
	WorktreePath string
	Plan         *plan.Plan
	Findings     []models.Finding
	MaxTurns     int
	Provider     string
	Model        string
	AuthContent  string
	OnEvent      func(meter.Event)
}

// Patch is one commit the agent produced.
type Patch struct {
	CommitSHA    string
	FilesChanged []string
	FindingIDs   []string
	Message      string
}

// PatchSeries is the ordered output of an execute run.
type PatchSeries struct {
	Patches []Patch
}

// Driver runs the two phases of a remediation session.
type Driver interface {
	Plan(ctx context.Context, req PlanRequest) (*plan.Plan, meter.Usage, error)
	Execute(ctx context.Context, req ExecuteRequest) (*PatchSeries, meter.Usage, error)
}
```

- [ ] **Step 4: Write the fake driver**

```go
package driver

import (
	"context"

	"github.com/alphabravocompany/thewolf/internal/remediate/meter"
	"github.com/alphabravocompany/thewolf/internal/remediate/plan"
)

// Fake replays a recorded event stream and returns a canned result. It drives
// the same meter as the exec driver, so budget behavior under test matches
// production.
type Fake struct {
	Events  []meter.Event
	PlanOut *plan.Plan
	Series  *PatchSeries
	// PlanErr and ExecErr, when set, are returned after the stream is
	// replayed — used to exercise orchestrator error paths.
	PlanErr error
	ExecErr error
}

// NewFake returns a Fake that replays events and yields p.
func NewFake(events []meter.Event, p *plan.Plan) *Fake {
	return &Fake{Events: events, PlanOut: p, Series: &PatchSeries{}}
}

func (f *Fake) replay(m meter.Meter, onEvent func(meter.Event)) bool {
	for _, e := range f.Events {
		if onEvent != nil {
			onEvent(e)
		}
		if m.Observe(e) {
			return true
		}
	}
	return false
}

func (f *Fake) Plan(_ context.Context, req PlanRequest) (*plan.Plan, meter.Usage, error) {
	m := meter.NewTurns(req.MaxTurns)
	if f.replay(m, req.OnEvent) {
		return nil, m.Usage(), ErrBudgetExhausted
	}
	if f.PlanErr != nil {
		return nil, m.Usage(), f.PlanErr
	}
	return f.PlanOut, m.Usage(), nil
}

func (f *Fake) Execute(_ context.Context, req ExecuteRequest) (*PatchSeries, meter.Usage, error) {
	m := meter.NewTurns(req.MaxTurns)
	if f.replay(m, req.OnEvent) {
		return nil, m.Usage(), ErrBudgetExhausted
	}
	if f.ExecErr != nil {
		return nil, m.Usage(), f.ExecErr
	}
	return f.Series, m.Usage(), nil
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/remediate/driver/ -run TestFake -v`
Expected: PASS — two tests.

- [ ] **Step 6: Commit the interface and fake**

```bash
git add internal/remediate/driver/
git commit -m "feat(remediate): driver interface and fake driver"
```

- [ ] **Step 7: Write the exec driver test**

```go
package driver

import (
	"context"
	"strings"
	"testing"
)

func TestExecBuildsArgsWithoutCredentials(t *testing.T) {
	d := &execDriver{cfg: ExecConfig{Image: "wolf-fixer-opencode:test", Provider: "grok"}}
	args, env := d.buildInvocation(ExecuteRequest{
		WorktreePath: "/tmp/wt",
		AuthContent:  `{"grok":{"type":"api","key":"SECRET"}}`,
		Provider:     "grok",
		Model:        "grok-code-fast",
	}, "/tmp/cfg/opencode.json", "do the thing")

	joined := strings.Join(args, " ")
	if strings.Contains(joined, "SECRET") {
		t.Fatalf("credential leaked into argv: %s", joined)
	}
	if !strings.Contains(joined, "--format json") && !strings.Contains(joined, "--format") {
		t.Errorf("missing --format json: %s", joined)
	}
	if !strings.Contains(joined, "--auto") {
		t.Errorf("execute run missing --auto: %s", joined)
	}

	var found bool
	for _, kv := range env {
		if strings.HasPrefix(kv, "OPENCODE_AUTH_CONTENT=") {
			found = true
			if !strings.Contains(kv, "SECRET") {
				t.Error("OPENCODE_AUTH_CONTENT does not carry the credential")
			}
		}
	}
	if !found {
		t.Error("OPENCODE_AUTH_CONTENT not set in env")
	}
}
```

- [ ] **Step 8: Write the exec driver**

```go
package driver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/alphabravocompany/thewolf/internal/remediate/meter"
	"github.com/alphabravocompany/thewolf/internal/remediate/permission"
	"github.com/alphabravocompany/thewolf/internal/remediate/plan"
)

// ExecConfig configures the containerized OpenCode invocation.
type ExecConfig struct {
	// Image is the wolf-fixer-opencode image reference.
	Image string
	// Binary is the container runtime ("docker" by default).
	Binary   string
	Provider string
	Model    string
}

type execDriver struct{ cfg ExecConfig }

// NewExec returns a Driver that runs OpenCode in a container.
func NewExec(cfg ExecConfig) Driver {
	if cfg.Binary == "" {
		cfg.Binary = "docker"
	}
	return &execDriver{cfg: cfg}
}

// buildInvocation returns argv and env for a run. Credentials go in env only:
// argv is visible in `ps` inside the container.
func (d *execDriver) buildInvocation(req ExecuteRequest, configPath, prompt string) ([]string, []string) {
	args := []string{
		"run", "--rm",
		"-v", req.WorktreePath + ":/workspace",
		"-v", filepath.Dir(configPath) + ":/config:ro",
		"-e", "OPENCODE_AUTH_CONTENT",
		// OPENCODE_CONFIG names the config FILE. OPENCODE_CONFIG_DIR is
		// the directory for agents/commands/plugins and does NOT load
		// opencode.json — setting it instead means the golden-tested
		// permission document is silently never applied and the agent
		// runs with defaults. Confirmed against 1.18.11 in the spike.
		"-e", "OPENCODE_CONFIG=/config/opencode.json",
		// NOT --network none: an opencode run must reach its provider
		// API, so a fully isolated container cannot start a session at
		// all. Egress is restricted to the provider endpoint instead —
		// see the spec's egress section.
		// The image keeps the repo-wide fixer entrypoint
		// (`wolf fixer`) so the release path's smoke and
		// qualification steps, which append their own arguments with
		// no override, still work. Reaching the CLI is this driver's
		// job, not the image's.
		"--entrypoint", "/usr/local/bin/opencode",
		d.cfg.Image,
		"run", "--format", "json", "--auto",
	}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	args = append(args, prompt)

	env := append(os.Environ(), "OPENCODE_AUTH_CONTENT="+req.AuthContent)
	return args, env
}

// stream runs the command and feeds each decoded event to the meter, killing
// the process the moment the budget is spent.
func (d *execDriver) stream(ctx context.Context, args, env []string, m meter.Meter, onEvent func(meter.Event)) ([]byte, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// #nosec G204 -- binary is fixed config; args are internal.
	cmd := exec.CommandContext(ctx, d.cfg.Binary, args...)
	cmd.Env = env
	// Stdin MUST be /dev/null. `opencode run` hangs indefinitely on an
	// inherited stdin — reproduced at 120s, 150s and 240s with zero bytes of
	// output and no error. A nil Stdin gives the child /dev/null. Without
	// this, every run hangs until the wall-clock timeout and the turn budget
	// never gets a chance to apply.
	cmd.Stdin = nil
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	var last []byte
	exhausted := false
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		last = line
		var e meter.Event
		if err := json.Unmarshal(line, &e); err != nil {
			continue // non-event output; ignore
		}
		if onEvent != nil {
			onEvent(e)
		}
		if m.Observe(e) {
			exhausted = true
			cancel()
			break
		}
	}
	_ = cmd.Wait()
	if exhausted {
		return last, ErrBudgetExhausted
	}
	return last, nil
}

func (d *execDriver) Plan(ctx context.Context, req PlanRequest) (*plan.Plan, meter.Usage, error) {
	doc, err := permission.Triage()
	if err != nil {
		return nil, meter.Usage{}, err
	}
	configPath, cleanup, err := writeConfig(doc)
	if err != nil {
		return nil, meter.Usage{}, err
	}
	defer cleanup()

	m := meter.NewTurns(req.MaxTurns)
	args, env := d.buildInvocation(ExecuteRequest{
		WorktreePath: req.WorktreePath, AuthContent: req.AuthContent,
		Provider: req.Provider, Model: req.Model,
	}, configPath, triagePrompt(req.Findings))

	last, err := d.stream(ctx, args, env, m, req.OnEvent)
	if err != nil {
		return nil, m.Usage(), err
	}
	p, err := plan.Parse(last)
	if err != nil {
		return nil, m.Usage(), fmt.Errorf("parse plan: %w", err)
	}
	return p, m.Usage(), nil
}

func (d *execDriver) Execute(ctx context.Context, req ExecuteRequest) (*PatchSeries, meter.Usage, error) {
	doc, err := permission.Execute()
	if err != nil {
		return nil, meter.Usage{}, err
	}
	configPath, cleanup, err := writeConfig(doc)
	if err != nil {
		return nil, meter.Usage{}, err
	}
	defer cleanup()

	m := meter.NewTurns(req.MaxTurns)
	args, env := d.buildInvocation(req, configPath, executePrompt(req.Plan, req.Findings))
	if _, err := d.stream(ctx, args, env, m, req.OnEvent); err != nil {
		return nil, m.Usage(), err
	}
	series, err := collectPatches(ctx, req.WorktreePath)
	if err != nil {
		return nil, m.Usage(), err
	}
	return series, m.Usage(), nil
}

func writeConfig(doc []byte) (string, func(), error) {
	dir, err := os.MkdirTemp("", "wolf-opencode-cfg-")
	if err != nil {
		return "", nil, err
	}
	path := filepath.Join(dir, "opencode.json")
	if err := os.WriteFile(path, doc, 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, err
	}
	return path, func() { _ = os.RemoveAll(dir) }, nil
}
```

Add `triagePrompt`, `executePrompt`, and `collectPatches` in a sibling file `prompt.go` — `collectPatches` shells out to `git log --format=%H` and `git show --name-only` in the worktree to build the series.

- [ ] **Step 9: Run tests**

Run: `go test ./internal/remediate/... -v`
Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add internal/remediate/driver/
git commit -m "feat(remediate): containerized opencode exec driver"
```

---

### Task 4a: Strip repo-supplied OpenCode config from the worktree

**Security fix from the spike.** A repository-level `opencode.json` overrides
the config Wolf injects — proven by A/B: with a permissive `opencode.json`
committed in the fixture repo and a restrictive one supplied via
`OPENCODE_CONFIG`, the agent used `bash`, which the injected document denied.
The scanned repository is untrusted input by definition, so any repo can
currently disable every permission control Wolf ships. Denying the agent
permission to *write* the file does not help — a pre-existing one already wins.

**Files:**
- Create: `internal/remediate/worktree_config.go`
- Test: `internal/remediate/worktree_config_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `remediate.StripAgentConfig(worktreePath string) ([]string, error)` — removes repo-level OpenCode configuration and returns the paths removed.

- [ ] **Step 1: Write the failing test**

```go
package remediate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStripAgentConfigRemovesRepoConfig(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"opencode.json", "opencode.jsonc"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(`{"permission":{"*":"allow"}}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, ".opencode", "agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}

	removed, err := StripAgentConfig(dir)
	if err != nil {
		t.Fatalf("StripAgentConfig: %v", err)
	}
	if len(removed) != 3 {
		t.Errorf("removed %d paths, want 3: %v", len(removed), removed)
	}
	for _, name := range []string{"opencode.json", "opencode.jsonc", ".opencode"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("%s still present after strip", name)
		}
	}
	// Repository content must be untouched — this strips config, not source.
	if _, err := os.Stat(filepath.Join(dir, "main.go")); err != nil {
		t.Errorf("main.go was removed: %v", err)
	}
}

func TestStripAgentConfigIsQuietWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	removed, err := StripAgentConfig(dir)
	if err != nil {
		t.Fatalf("StripAgentConfig on a clean tree: %v", err)
	}
	if len(removed) != 0 {
		t.Errorf("removed %v from a clean tree", removed)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/remediate/ -run StripAgentConfig -v`
Expected: FAIL — `StripAgentConfig` undefined.

- [ ] **Step 3: Implement**

```go
package remediate

import (
	"fmt"
	"os"
	"path/filepath"
)

// agentConfigPaths are repository-level OpenCode configuration locations.
// OpenCode's config precedence places project config ABOVE the config Wolf
// injects via OPENCODE_CONFIG, so a repository carrying any of these can
// override every permission rule Wolf sets.
var agentConfigPaths = []string{"opencode.json", "opencode.jsonc", ".opencode"}

// StripAgentConfig removes repository-level OpenCode configuration from a
// worktree and returns the relative paths it removed.
//
// The scanned repository is untrusted input — that is the premise of the
// product — so its own agent configuration must not be allowed to outrank
// Wolf's. This runs against the ephemeral worktree only; the user's actual
// repository is never modified.
func StripAgentConfig(worktreePath string) ([]string, error) {
	var removed []string
	for _, name := range agentConfigPaths {
		full := filepath.Join(worktreePath, name)
		if _, err := os.Lstat(full); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return removed, fmt.Errorf("stat %s: %w", name, err)
		}
		if err := os.RemoveAll(full); err != nil {
			return removed, fmt.Errorf("remove %s: %w", name, err)
		}
		removed = append(removed, name)
	}
	return removed, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/remediate/ -run StripAgentConfig -v`
Expected: PASS — two tests.

- [ ] **Step 5: Wire it into the session**

Call `StripAgentConfig` in `Runner.prepareWorkspace` (Task 11), immediately
after `workspace.Prepare` returns and before any driver call. Record each
removed path as a session event so an operator can see it happened — a repo
shipping an `opencode.json` is a signal worth surfacing, not just suppressing.

- [ ] **Step 6: Commit**

```bash
git add internal/remediate/
git commit -m "feat(remediate): strip repo-supplied OpenCode config from the worktree"
```

---

### Task 5: Database schema and repository

**Files:**
- Create: `internal/db/migrations/051_opencode_remediation.sql`, `internal/db/remediation_repository.go`, `internal/models/remediation.go`
- Modify: `internal/db/sqlite.go`, `internal/db/postgres.go`, `internal/db/store.go`
- Test: `internal/db/remediation_repository_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `models.RemediationSession`, `models.RemediationPlan`, `models.RemediationPatch`, `models.RemediationEvent`, `models.RemediationStatus` constants, and `Store` methods `CreateRemediationSession`, `GetRemediationSession`, `ListRemediationSessions`, `UpdateRemediationSession`, `SaveRemediationPlan`, `GetRemediationPlan`, `ApproveRemediationPlan`, `SaveRemediationPatches`, `ListRemediationPatches`, `AppendRemediationEvent`, `ListRemediationEvents`.

- [ ] **Step 1: Write the migration**

`internal/db/migrations/051_opencode_remediation.sql`:

```sql
CREATE TABLE IF NOT EXISTS remediation_sessions (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    repo_id TEXT NOT NULL,
    scan_id TEXT NOT NULL,
    loop_id TEXT,
    status TEXT NOT NULL DEFAULT 'pending',
    -- BOOLEAN, not INTEGER: these map to Go bool fields, and lib/pq encodes a
    -- Go bool as the literal text true/false regardless of the target column's
    -- OID. Against an INTEGER column Postgres then fails with "invalid input
    -- syntax for type integer". SQLite accepts BOOLEAN (NUMERIC affinity,
    -- stores 0/1), so one spelling serves both. Matches migrations 006, 012,
    -- 014, 016, 023, 042, 043, 046.
    plan_gate_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    patch_gate_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    max_turns INTEGER NOT NULL DEFAULT 20,
    turns_used_plan INTEGER NOT NULL DEFAULT 0,
    turns_used_execute INTEGER NOT NULL DEFAULT 0,
    tokens_used INTEGER NOT NULL DEFAULT 0,
    cost_used REAL NOT NULL DEFAULT 0,
    provider TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    branch_name TEXT NOT NULL DEFAULT '',
    worktree_path TEXT NOT NULL DEFAULT '',
    pr_url TEXT NOT NULL DEFAULT '',
    failure_reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    started_at TIMESTAMP,
    completed_at TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_remediation_sessions_user ON remediation_sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_remediation_sessions_scan ON remediation_sessions(scan_id);
CREATE INDEX IF NOT EXISTS idx_remediation_sessions_status ON remediation_sessions(status);

CREATE TABLE IF NOT EXISTS remediation_plans (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    plan_json TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL,
    approved_by TEXT NOT NULL DEFAULT '',
    approved_at TIMESTAMP,
    rejected_reason TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_remediation_plans_session ON remediation_plans(session_id);

CREATE TABLE IF NOT EXISTS remediation_patches (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    commit_sha TEXT NOT NULL,
    files_changed TEXT NOT NULL DEFAULT '',
    finding_ids TEXT NOT NULL DEFAULT '',
    message TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL,
    approved_by TEXT NOT NULL DEFAULT '',
    approved_at TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_remediation_patches_session ON remediation_patches(session_id);

CREATE TABLE IF NOT EXISTS remediation_events (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    seq INTEGER NOT NULL,
    type TEXT NOT NULL,
    payload_json TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_remediation_events_session_seq ON remediation_events(session_id, seq);
```

- [ ] **Step 2: Register the migration in both stores**

In `internal/db/sqlite.go`, after the `migration050SQL` declaration (near line 165):

```go
//go:embed migrations/051_opencode_remediation.sql
var migration051SQL string
```

In `SQLiteStore.Migrate()`, after the `migration050SQL` block (near line 408):

```go
	if err := execAdditiveMigration(s.db, migration051SQL); err != nil {
		return err
	}
```

In `internal/db/postgres.go`, after the `migration050SQL` block (near line 257), add the identical `execAdditiveMigration(s.db, migration051SQL)` call. Postgres reuses the variable embedded in `sqlite.go`.

- [ ] **Step 3: Write the models**

`internal/models/remediation.go`:

```go
package models

import "time"

// RemediationStatus is the state of an agentic remediation session.
type RemediationStatus string

const (
	RemediationPending     RemediationStatus = "pending"
	RemediationPlanning    RemediationStatus = "planning"
	RemediationPlanReview  RemediationStatus = "plan_review"
	RemediationExecuting   RemediationStatus = "executing"
	RemediationPatchReview RemediationStatus = "patch_review"
	RemediationApplying    RemediationStatus = "applying"
	RemediationRescanning  RemediationStatus = "rescanning"
	RemediationCompleted   RemediationStatus = "completed"
	RemediationFailed      RemediationStatus = "failed"
	RemediationCancelled   RemediationStatus = "cancelled"
	RemediationExhausted   RemediationStatus = "exhausted"
	RemediationRejected    RemediationStatus = "rejected"
)

// RemediationSession is one agentic remediation run over a scan's findings.
type RemediationSession struct {
	ID               string            `json:"id" db:"id"`
	UserID           string            `json:"user_id" db:"user_id"`
	RepoID           string            `json:"repo_id" db:"repo_id"`
	ScanID           string            `json:"scan_id" db:"scan_id"`
	LoopID           *string           `json:"loop_id,omitempty" db:"loop_id"`
	Status           RemediationStatus `json:"status" db:"status"`
	PlanGateEnabled  bool              `json:"plan_gate_enabled" db:"plan_gate_enabled"`
	PatchGateEnabled bool              `json:"patch_gate_enabled" db:"patch_gate_enabled"`
	MaxTurns         int               `json:"max_turns" db:"max_turns"`
	TurnsUsedPlan    int               `json:"turns_used_plan" db:"turns_used_plan"`
	TurnsUsedExecute int               `json:"turns_used_execute" db:"turns_used_execute"`
	TokensUsed       int64             `json:"tokens_used" db:"tokens_used"`
	CostUsed         float64           `json:"cost_used" db:"cost_used"`
	Provider         string            `json:"provider" db:"provider"`
	Model            string            `json:"model" db:"model"`
	BranchName       string            `json:"branch_name" db:"branch_name"`
	WorktreePath     string            `json:"-" db:"worktree_path"`
	PRURL            string            `json:"pr_url" db:"pr_url"`
	FailureReason    string            `json:"failure_reason" db:"failure_reason"`
	CreatedAt        time.Time         `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at" db:"updated_at"`
	StartedAt        *time.Time        `json:"started_at,omitempty" db:"started_at"`
	CompletedAt      *time.Time        `json:"completed_at,omitempty" db:"completed_at"`
}

// RemediationPlan is a persisted triage plan awaiting or past its gate.
type RemediationPlan struct {
	ID             string     `json:"id" db:"id"`
	SessionID      string     `json:"session_id" db:"session_id"`
	PlanJSON       string     `json:"plan_json" db:"plan_json"`
	CreatedAt      time.Time  `json:"created_at" db:"created_at"`
	ApprovedBy     string     `json:"approved_by,omitempty" db:"approved_by"`
	ApprovedAt     *time.Time `json:"approved_at,omitempty" db:"approved_at"`
	RejectedReason string     `json:"rejected_reason,omitempty" db:"rejected_reason"`
}

// RemediationPatch is one commit produced by an execute run.
type RemediationPatch struct {
	ID           string     `json:"id" db:"id"`
	SessionID    string     `json:"session_id" db:"session_id"`
	CommitSHA    string     `json:"commit_sha" db:"commit_sha"`
	FilesChanged string     `json:"files_changed" db:"files_changed"`
	FindingIDs   string     `json:"finding_ids" db:"finding_ids"`
	Message      string     `json:"message" db:"message"`
	CreatedAt    time.Time  `json:"created_at" db:"created_at"`
	ApprovedBy   string     `json:"approved_by,omitempty" db:"approved_by"`
	ApprovedAt   *time.Time `json:"approved_at,omitempty" db:"approved_at"`
}

// RemediationEvent is one redacted record from the agent's event stream.
type RemediationEvent struct {
	ID          string    `json:"id" db:"id"`
	SessionID   string    `json:"session_id" db:"session_id"`
	Seq         int       `json:"seq" db:"seq"`
	Type        string    `json:"type" db:"type"`
	PayloadJSON string    `json:"payload_json" db:"payload_json"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
}
```

- [ ] **Step 4: Write the repository test**

```go
package db

import (
	"context"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestRemediationSessionRoundTrip(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()

	s := &models.RemediationSession{
		ID: "rs-1", UserID: "u-1", RepoID: "r-1", ScanID: "sc-1",
		Status: models.RemediationPending, MaxTurns: 20,
		PlanGateEnabled: true, PatchGateEnabled: true,
		Provider: "grok", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := store.CreateRemediationSession(ctx, s); err != nil {
		t.Fatalf("CreateRemediationSession: %v", err)
	}

	got, err := store.GetRemediationSession(ctx, "rs-1")
	if err != nil {
		t.Fatalf("GetRemediationSession: %v", err)
	}
	if got.Status != models.RemediationPending || got.MaxTurns != 20 {
		t.Errorf("round trip mismatch: %+v", got)
	}

	got.Status = models.RemediationPlanning
	got.TurnsUsedPlan = 5
	if err := store.UpdateRemediationSession(ctx, got); err != nil {
		t.Fatalf("UpdateRemediationSession: %v", err)
	}
	again, _ := store.GetRemediationSession(ctx, "rs-1")
	if again.Status != models.RemediationPlanning || again.TurnsUsedPlan != 5 {
		t.Errorf("update not persisted: %+v", again)
	}
}

func TestRemediationEventsOrderedBySeq(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()
	seed := &models.RemediationSession{
		ID: "rs-2", UserID: "u-1", RepoID: "r-1", ScanID: "sc-1",
		Status: models.RemediationPending, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := store.CreateRemediationSession(ctx, seed); err != nil {
		t.Fatalf("seed: %v", err)
	}

	for i := 3; i >= 1; i-- {
		e := &models.RemediationEvent{
			ID: string(rune('a'+i)) + "-ev", SessionID: "rs-2", Seq: i,
			Type: "assistant", CreatedAt: time.Now(),
		}
		if err := store.AppendRemediationEvent(ctx, e); err != nil {
			t.Fatalf("AppendRemediationEvent: %v", err)
		}
	}

	events, err := store.ListRemediationEvents(ctx, "rs-2", 0)
	if err != nil {
		t.Fatalf("ListRemediationEvents: %v", err)
	}
	for i, e := range events {
		if e.Seq != i+1 {
			t.Fatalf("events[%d].Seq = %d, want %d", i, e.Seq, i+1)
		}
	}
}
```

Reuse the existing SQLite test helper in `internal/db/sqlite_test.go`; if its constructor is named differently, use that name instead of `newTestSQLiteStore`.

- [ ] **Step 5: Run test to verify it fails**

Run: `go test ./internal/db/ -run TestRemediation -v`
Expected: FAIL — methods undefined.

- [ ] **Step 6: Implement the repository**

Create `internal/db/remediation_repository.go` with the eleven methods listed in **Interfaces**, following the shape of `internal/db/scannerrelease_repository.go`. Add each method signature to the `Store` interface in `internal/db/store.go` under a `// Remediation` comment block.

- [ ] **Step 7: Run tests**

Run: `go test ./internal/db/ -run TestRemediation -v && go build ./...`
Expected: PASS, build exit 0.

- [ ] **Step 8: Commit**

```bash
git add internal/db/ internal/models/remediation.go
git commit -m "feat(remediate): session, plan, patch, and event persistence"
```

---

### Task 6: Session orchestrator

**Depends on Tasks 1, 2, 4, 5.**

**Files:**
- Create: `internal/remediate/session.go`, `internal/remediate/config.go`
- Test: `internal/remediate/session_test.go`
- Modify: `.env.example`

**Interfaces:**
- Consumes: `driver.Driver`, `driver.PlanRequest`, `driver.ExecuteRequest`, `plan.Plan`, `meter.Usage`, `db.Store`, all `models.Remediation*` types.
- Produces: `remediate.Runner`, `remediate.NewRunner(store db.Store, d driver.Driver, cfg Config) *Runner`, `Runner.Run(ctx context.Context, sessionID string) error`, `remediate.Config`, `remediate.LoadConfig() Config`.

- [ ] **Step 1: Write the failing test**

```go
package remediate

import (
	"context"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/remediate/driver"
	"github.com/alphabravocompany/thewolf/internal/remediate/meter"
	"github.com/alphabravocompany/thewolf/internal/remediate/plan"
)

func fixturePlan() *plan.Plan {
	return &plan.Plan{
		Summary: "1 actionable",
		Items:   []plan.Item{{FindingID: "f-1", Action: plan.ActionFix, Rationale: "sqli"}},
	}
}

// With both gates off, a session runs straight through to completion.
func TestRunYoloReachesCompleted(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, func(s *models.RemediationSession) {
		s.PlanGateEnabled = false
		s.PatchGateEnabled = false
	})

	d := driver.NewFake([]meter.Event{{Type: "assistant"}}, fixturePlan())
	// AllowYolo must be true: Run refuses a gates-off session otherwise.
	r := NewRunner(store, d, Config{Enabled: true, MaxTurns: 10, AllowYolo: true})

	if err := r.Run(context.Background(), sess.ID); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got, _ := store.GetRemediationSession(context.Background(), sess.ID)
	if got.Status != models.RemediationCompleted {
		t.Fatalf("Status = %q, want %q", got.Status, models.RemediationCompleted)
	}
	if got.TurnsUsedPlan == 0 {
		t.Error("TurnsUsedPlan not recorded")
	}
}

// A budget-exhausted plan run marks the session exhausted and never executes.
func TestRunExhaustedStopsBeforeExecute(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, func(s *models.RemediationSession) {
		s.PlanGateEnabled = false
		s.PatchGateEnabled = false
		s.MaxTurns = 1
	})

	events := []meter.Event{{Type: "assistant"}, {Type: "assistant"}}
	d := driver.NewFake(events, fixturePlan())
	r := NewRunner(store, d, Config{Enabled: true, MaxTurns: 1, AllowYolo: true})

	if err := r.Run(context.Background(), sess.ID); err == nil {
		t.Fatal("Run succeeded, want budget error")
	}
	got, _ := store.GetRemediationSession(context.Background(), sess.ID)
	if got.Status != models.RemediationExhausted {
		t.Fatalf("Status = %q, want %q", got.Status, models.RemediationExhausted)
	}
	if len(listPatches(t, store, sess.ID)) != 0 {
		t.Error("patches written despite exhausted plan run")
	}
}

// A disabled runner refuses to start.
func TestRunRejectsWhenDisabled(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, nil)
	r := NewRunner(store, driver.NewFake(nil, fixturePlan()), Config{Enabled: false})

	if err := r.Run(context.Background(), sess.ID); err == nil {
		t.Fatal("Run succeeded with Enabled=false, want error")
	}
}

// Every observed event is persisted for SSE replay and audit.
func TestRunPersistsEvents(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, func(s *models.RemediationSession) {
		s.PlanGateEnabled = false
		s.PatchGateEnabled = false
	})
	events := []meter.Event{{Type: "assistant"}, {Type: "tool.start"}}
	r := NewRunner(store, driver.NewFake(events, fixturePlan()),
		Config{Enabled: true, MaxTurns: 10, AllowYolo: true})

	_ = r.Run(context.Background(), sess.ID)

	stored, err := store.ListRemediationEvents(context.Background(), sess.ID, 0)
	if err != nil {
		t.Fatalf("ListRemediationEvents: %v", err)
	}
	if len(stored) == 0 {
		t.Fatal("no events persisted")
	}
}
```

Write `newTestStore`, `seedSession`, and `listPatches` helpers at the top of the same test file, backed by the SQLite test store from Task 5.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/remediate/ -v`
Expected: FAIL — `NewRunner` undefined.

- [ ] **Step 3: Write the config**

```go
package remediate

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the WOLF_REMEDIATE_* environment configuration.
type Config struct {
	Enabled         bool
	DefaultProvider string
	DefaultModel    string
	MaxTurns        int
	MaxTurnsCeiling int
	SessionTimeout  time.Duration
	AllowYolo       bool
}

// LoadConfig reads configuration from the environment. Defaults are
// fail-closed: remediation is off and yolo mode is unavailable until an
// admin opts in.
func LoadConfig() Config {
	return Config{
		Enabled:         envBool("WOLF_REMEDIATE_ENABLED", false),
		DefaultProvider: os.Getenv("WOLF_REMEDIATE_DEFAULT_PROVIDER"),
		DefaultModel:    os.Getenv("WOLF_REMEDIATE_DEFAULT_MODEL"),
		MaxTurns:        envInt("WOLF_REMEDIATE_MAX_TURNS", 20),
		MaxTurnsCeiling: envInt("WOLF_REMEDIATE_MAX_TURNS_CEILING", 100),
		SessionTimeout:  envDuration("WOLF_REMEDIATE_SESSION_TIMEOUT", 30*time.Minute),
		AllowYolo:       envBool("WOLF_REMEDIATE_ALLOW_YOLO", false),
	}
}

// ClampTurns bounds a requested budget to the admin ceiling.
func (c Config) ClampTurns(requested int) int {
	if requested <= 0 {
		return c.MaxTurns
	}
	if c.MaxTurnsCeiling > 0 && requested > c.MaxTurnsCeiling {
		return c.MaxTurnsCeiling
	}
	return requested
}

func envBool(name string, def bool) bool {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	return strings.EqualFold(v, "true") || v == "1"
}

func envInt(name string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name))); err == nil && n > 0 {
		return n
	}
	return def
}

func envDuration(name string, def time.Duration) time.Duration {
	if d, err := time.ParseDuration(strings.TrimSpace(os.Getenv(name))); err == nil && d > 0 {
		return d
	}
	return def
}
```

- [ ] **Step 4: Write the orchestrator**

Note on the event sequence: `remediation_events.seq` is the only ordering key
SSE replay has, and migration 051 makes `(session_id, seq)` UNIQUE. The sink
below therefore keeps one sequence per *session*, seeded from what is already
persisted, rather than one per driver call — plan and execute are two calls,
and two sequences both starting at 1 would collide on the primary key and lose
the entire execute-phase stream. For the same reason the append error is
logged, never discarded: a silent drop is indistinguishable from an idle agent.

```go
// Package remediate orchestrates agentic remediation sessions: a read-only
// triage run that emits a plan, then a scoped-write run that executes it.
// Nothing is held open across an approval gate, so a pending approval is a
// database row that survives a restart.
package remediate

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/remediate/driver"
	"github.com/alphabravocompany/thewolf/internal/remediate/meter"
	"github.com/alphabravocompany/thewolf/internal/wolflog"
)

// Runner drives one session through its phases.
type Runner struct {
	store  db.Store
	driver driver.Driver
	cfg    Config
}

// NewRunner returns a Runner bound to a store and driver.
func NewRunner(store db.Store, d driver.Driver, cfg Config) *Runner {
	return &Runner{store: store, driver: d, cfg: cfg}
}

// Run advances a session as far as its gates allow. With both gates off it
// runs to completion; with a gate on it stops at the corresponding review
// state and returns nil, to be resumed by an approval.
func (r *Runner) Run(ctx context.Context, sessionID string) error {
	if !r.cfg.Enabled {
		return errors.New("remediation is disabled (WOLF_REMEDIATE_ENABLED=false)")
	}
	ctx, cancel := context.WithTimeout(ctx, r.cfg.SessionTimeout)
	defer cancel()

	sess, err := r.store.GetRemediationSession(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("load session: %w", err)
	}
	if (!sess.PlanGateEnabled || !sess.PatchGateEnabled) && !r.cfg.AllowYolo {
		return errors.New("gates disabled but WOLF_REMEDIATE_ALLOW_YOLO=false")
	}

	if err := r.runPlanPhase(ctx, sess); err != nil {
		return err
	}
	if sess.PlanGateEnabled {
		return r.transition(ctx, sess, models.RemediationPlanReview, "")
	}
	return r.runExecutePhase(ctx, sess)
}

func (r *Runner) runPlanPhase(ctx context.Context, sess *models.RemediationSession) error {
	if err := r.transition(ctx, sess, models.RemediationPlanning, ""); err != nil {
		return err
	}
	findings, err := r.store.GetScanFindings(ctx, sess.ScanID)
	if err != nil {
		return fmt.Errorf("load findings: %w", err)
	}

	p, usage, err := r.driver.Plan(ctx, driver.PlanRequest{
		WorktreePath: sess.WorktreePath,
		Findings:     findings,
		MaxTurns:     r.cfg.ClampTurns(sess.MaxTurns),
		Provider:     sess.Provider,
		Model:        sess.Model,
		OnEvent:      r.eventSink(ctx, sess.ID),
	})
	sess.TurnsUsedPlan = usage.Turns
	if err != nil {
		status := models.RemediationFailed
		if errors.Is(err, driver.ErrBudgetExhausted) {
			status = models.RemediationExhausted
		}
		_ = r.transition(ctx, sess, status, err.Error())
		return err
	}
	return r.savePlan(ctx, sess, p)
}

// eventSink persists each observed event, redacted, for SSE replay and audit.
//
// The sequence is session-scoped and monotonic across phases, not per-call.
// A sink is built once per phase, so a counter starting at zero each time
// would have execute-phase event 1 collide with plan-phase event 1: the ID is
// derived from (session, seq) and is the primary key, and (session_id, seq)
// is UNIQUE, so the second phase's every write fails. Seeding from what is
// already persisted keeps one continuous sequence per session.
func (r *Runner) eventSink(ctx context.Context, sessionID string) func(meter.Event) {
	seq := r.lastEventSeq(ctx, sessionID)
	return func(e meter.Event) {
		seq++
		// Never discard this error. A dropped append is a hole in the audit
		// trail and a gap SSE replay cannot tell apart from "no activity",
		// which is exactly how the per-call sequence bug stayed invisible.
		if err := r.store.AppendRemediationEvent(ctx, &models.RemediationEvent{
			ID:        fmt.Sprintf("%s-%d", sessionID, seq),
			SessionID: sessionID,
			Seq:       seq,
			Type:      e.Type,
			CreatedAt: time.Now(),
		}); err != nil {
			wolflog.L().Error().Err(err).
				Str("session", sessionID).Int("seq", seq).
				Msg("persist remediation event")
		}
	}
}

// lastEventSeq returns the highest seq already stored for a session so a sink
// built for a later phase continues that session's single sequence. Events
// come back ordered by seq, so the tail holds the maximum. A read failure
// falls back to 0: the unique index then rejects the duplicate loudly rather
// than letting the sink overwrite an earlier phase.
func (r *Runner) lastEventSeq(ctx context.Context, sessionID string) int {
	events, err := r.store.ListRemediationEvents(ctx, sessionID, 0)
	if err != nil || len(events) == 0 {
		return 0
	}
	return events[len(events)-1].Seq
}

func (r *Runner) transition(ctx context.Context, sess *models.RemediationSession, status models.RemediationStatus, reason string) error {
	sess.Status = status
	sess.FailureReason = reason
	sess.UpdatedAt = time.Now()
	switch status {
	case models.RemediationCompleted, models.RemediationFailed,
		models.RemediationExhausted, models.RemediationCancelled,
		models.RemediationRejected:
		now := time.Now()
		sess.CompletedAt = &now
	}
	return r.store.UpdateRemediationSession(ctx, sess)
}
```

Add `savePlan` and `runExecutePhase` in the same file. `runExecutePhase` transitions to `executing`, calls `r.driver.Execute`, records `TurnsUsedExecute`, saves patches via `SaveRemediationPatches`, then transitions to `patch_review` if `sess.PatchGateEnabled` or `completed` otherwise. Phase 3 replaces the `completed` branch with apply/rescan/PR.

If `GetScanFindings` has a different name on `Store`, use the existing one.

- [ ] **Step 5: Run tests**

Run: `go test ./internal/remediate/ -v`
Expected: PASS — four tests.

- [ ] **Step 6: Document the configuration**

Append to `.env.example` under a new `# --- Agentic remediation (OpenCode) ---` heading:

```bash
WOLF_REMEDIATE_ENABLED=false
WOLF_REMEDIATE_DEFAULT_PROVIDER=
WOLF_REMEDIATE_DEFAULT_MODEL=
WOLF_REMEDIATE_MAX_TURNS=20
WOLF_REMEDIATE_MAX_TURNS_CEILING=100
WOLF_REMEDIATE_SESSION_TIMEOUT=30m
# Ungated autonomous code modification is an explicit admin decision.
WOLF_REMEDIATE_ALLOW_YOLO=false
```

- [ ] **Step 7: Commit**

```bash
git add internal/remediate/ .env.example
git commit -m "feat(remediate): session orchestrator with turn budget and event capture"
```

---

### Task 7: OpenCode fixer container variant

**Files:**
- Create: `fixer/Dockerfile.opencode`
- Modify: `internal/scannerbuild/build.go`, `fixer/versions.env`
- Test: `internal/scannerbuild/build_test.go` (existing `TestFixerVariantsResolve` covers it)

**Interfaces:**
- Consumes: nothing.
- Produces: a `FixerVariants` entry named `opencode` producing image suffix `-opencode`.

- [ ] **Step 1: Pin the version**

Append to `fixer/versions.env`, replacing the placeholder values with the real published version and integrity hashes from `npm view opencode-ai@<version> dist.integrity`:

```bash
OPENCODE_VERSION=<pinned version>
OPENCODE_INTEGRITY=<sha512-... from npm view>
```

- [ ] **Step 2: Write the Dockerfile**

`fixer/Dockerfile.opencode`, following `Dockerfile.codex`:

```dockerfile
# check=skip=InvalidDefaultArgInFrom
# checkov:skip=CKV_DOCKER_2 — worker container, not a service.
#
# wolf-fixer-opencode — agentic remediation variant
# ============================================================
# Extends the wolf-fixer base with the OpenCode CLI. Unlike the claude and
# codex variants this image needs no interactive login and no session
# volume: Wolf injects credentials per run via OPENCODE_AUTH_CONTENT, which
# bypasses auth.json file storage entirely. Two runs for two users never
# share credential state.
#
# Build via internal/scannerbuild FixerVariants ("opencode").
# ============================================================

ARG WOLF_FIXER_BASE_REF
FROM ${WOLF_FIXER_BASE_REF} AS base

ARG OPENCODE_VERSION
ARG OPENCODE_INTEGRITY

ENV WOLF_FIXER_VARIANT=opencode \
    WOLF_FIXER_ENGINE=opencode

LABEL dev.wolf.fixer.variant="opencode" \
      dev.wolf.fixer.auth-mode="injected" \
      dev.wolf.fixer.cli.version="${OPENCODE_VERSION}"

USER root

RUN set -eux; \
    actual_integrity="$(npm --cache /tmp/npm-cache view "opencode-ai@${OPENCODE_VERSION}" dist.integrity)"; \
    test "$actual_integrity" = "$OPENCODE_INTEGRITY"; \
    npm --cache /tmp/npm-cache install --prefix /opt/wolf/opencode --omit=dev \
      "opencode-ai@${OPENCODE_VERSION}"; \
    ln -s /opt/wolf/opencode/node_modules/.bin/opencode /usr/local/bin/opencode; \
    command -v opencode; \
    opencode --version; \
    rm -rf /tmp/npm-cache

USER wolf
WORKDIR /workspace
ENV HOME=/home/wolf

# Every fixer variant carries the same entrypoint. The release path appends
# its smoke and qualification arguments to the image with no --entrypoint
# override, so a variant-specific entrypoint here would corrupt those calls.
# The driver (Task 4) passes --entrypoint /usr/local/bin/opencode at run time.
ENTRYPOINT ["/usr/local/bin/wolf", "fixer"]
```

- [ ] **Step 3: Register the variant**

In `internal/scannerbuild/build.go`, add to `FixerVariants` after the `api` entry:

```go
	{Name: "opencode", Dockerfile: "Dockerfile.opencode", ImageBase: fixerImageBase, ImageSuffix: "-opencode", ContextSubdir: fixerContextSubdir},
```

- [ ] **Step 4: Regenerate the embedded build context**

Run: `go generate ./internal/scannerbuild/...`
This copies `fixer/Dockerfile.opencode` into `internal/scannerbuild/context/`. Do not edit the generated tree by hand.

- [ ] **Step 5: Run tests**

Run: `go test ./internal/scannerbuild/ -v`
Expected: PASS — `TestFixerVariantsResolve` and the embedded-context tests now include `opencode`.

- [ ] **Step 6: Commit**

```bash
git add fixer/ internal/scannerbuild/
git commit -m "feat(remediate): opencode fixer container variant"
```

---

**Phase 1 gate:** `go build ./... && go test ./...` green. A session can be created directly in the database, run with the exec driver, and produce commits in a worktree. No API, no gates, no PR yet.

---

# Phase 2 — Gates

Adds the plan and patch gates, the review states, and approval endpoints.

### Task 8: Gate policy — SHIPPED, THEN DELETED

> **This package no longer exists.** It was implemented, reviewed, approved, and
> then removed during Task 9 because it was a fail-open trap rather than merely
> unused code: `gate.IsYolo()` returned true only when **both** gates were off,
> while `Runner.Run` requires the `WOLF_REMEDIATE_ALLOW_YOLO` opt-in when
> **either** is off. They disagreed exactly on the dangerous case — patch gate
> off, patches landing with no human review. Task 10 must return 403 for "gates
> disabled but AllowYolo false", and `gate.IsYolo()` is precisely the helper a
> Task 10 implementer would reach for.
>
> `Runner` reads `sess.PlanGateEnabled` / `sess.PatchGateEnabled` directly and
> always did; its stricter OR is the single enforcement point. Do not
> reintroduce this package. The section below is kept as a record of what was
> built and why it was withdrawn.

**Files:**
- Create: `internal/remediate/gate/gate.go`
- Test: `internal/remediate/gate/gate_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `gate.Decision` (`DecisionProceed`, `DecisionHold`), `gate.Policy` struct, `gate.Policy.AfterPlan() Decision`, `gate.Policy.AfterPatch() Decision`, `gate.Policy.IsYolo() bool`.

- [ ] **Step 1: Write the failing test**

```go
package gate

import "testing"

func TestPolicyMatrix(t *testing.T) {
	tests := []struct {
		name                  string
		planGate, patchGate   bool
		wantPlan, wantPatch   Decision
		wantYolo              bool
	}{
		{"both on", true, true, DecisionHold, DecisionHold, false},
		{"plan only", true, false, DecisionHold, DecisionProceed, false},
		{"patch only", false, true, DecisionProceed, DecisionHold, false},
		{"yolo", false, false, DecisionProceed, DecisionProceed, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := Policy{PlanGate: tt.planGate, PatchGate: tt.patchGate}
			if got := p.AfterPlan(); got != tt.wantPlan {
				t.Errorf("AfterPlan() = %v, want %v", got, tt.wantPlan)
			}
			if got := p.AfterPatch(); got != tt.wantPatch {
				t.Errorf("AfterPatch() = %v, want %v", got, tt.wantPatch)
			}
			if got := p.IsYolo(); got != tt.wantYolo {
				t.Errorf("IsYolo() = %v, want %v", got, tt.wantYolo)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/remediate/gate/ -v`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Write minimal implementation**

```go
// Package gate decides where a remediation session stops for human approval.
// Gates are Wolf's own checkpoints; they are independent of OpenCode's
// permission rules, which hold in every mode.
package gate

// Decision is whether a session may continue past a checkpoint.
type Decision string

const (
	DecisionProceed Decision = "proceed"
	DecisionHold    Decision = "hold"
)

// Policy is a session's gate configuration.
type Policy struct {
	PlanGate  bool
	PatchGate bool
}

// AfterPlan reports whether to hold after the triage run.
func (p Policy) AfterPlan() Decision {
	if p.PlanGate {
		return DecisionHold
	}
	return DecisionProceed
}

// AfterPatch reports whether to hold before patches land.
func (p Policy) AfterPatch() Decision {
	if p.PatchGate {
		return DecisionHold
	}
	return DecisionProceed
}

// IsYolo reports whether both gates are disabled. Yolo disables Wolf's
// approval checkpoints; the hard deny list still applies.
func (p Policy) IsYolo() bool { return !p.PlanGate && !p.PatchGate }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/remediate/gate/ -v`
Expected: PASS — four sub-cases.

- [ ] **Step 5: Commit**

```bash
git add internal/remediate/gate/
git commit -m "feat(remediate): gate policy"
```

---

### Task 9: Resume-from-gate in the orchestrator

**Files:**
- Modify: `internal/remediate/session.go`
- Test: `internal/remediate/session_test.go`

**Interfaces:**
- Consumes: `gate.Policy`, `gate.DecisionHold`.
- Produces: `Runner.ApprovePlan(ctx, sessionID, approverID string) error`, `Runner.RejectPlan(ctx, sessionID, approverID, reason string) error`, `Runner.ApprovePatches(ctx, sessionID, approverID string) error`, `Runner.RejectPatches(ctx, sessionID, approverID, reason string) error`.
- **Also adds one `Store` method** (Task 5 deliberately omitted it): `RejectRemediationPlan(ctx context.Context, sessionID, approverID, reason string) error`, writing `approved_by`, `approved_at`, and `rejected_reason` on the latest plan row for the session. Without it, `remediation_plans.rejected_reason` has no writer anywhere in the system. Implement it for both SQLite and Postgres alongside the existing remediation methods.

- [ ] **Step 1: Write the failing test**

```go
func TestPlanGateHoldsThenResumes(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, func(s *models.RemediationSession) {
		s.PlanGateEnabled = true
		s.PatchGateEnabled = false
	})
	d := driver.NewFake([]meter.Event{{Type: "assistant"}}, fixturePlan())
	r := NewRunner(store, d, Config{Enabled: true, MaxTurns: 10, AllowYolo: true})

	if err := r.Run(context.Background(), sess.ID); err != nil {
		t.Fatalf("Run: %v", err)
	}
	held, _ := store.GetRemediationSession(context.Background(), sess.ID)
	if held.Status != models.RemediationPlanReview {
		t.Fatalf("Status = %q, want %q", held.Status, models.RemediationPlanReview)
	}

	if err := r.ApprovePlan(context.Background(), sess.ID, "u-approver"); err != nil {
		t.Fatalf("ApprovePlan: %v", err)
	}
	done, _ := store.GetRemediationSession(context.Background(), sess.ID)
	if done.Status != models.RemediationCompleted {
		t.Fatalf("Status after approval = %q, want %q", done.Status, models.RemediationCompleted)
	}
}

func TestRejectPlanTerminatesSession(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, func(s *models.RemediationSession) {
		s.PlanGateEnabled = true
	})
	r := NewRunner(store, driver.NewFake([]meter.Event{{Type: "assistant"}}, fixturePlan()),
		Config{Enabled: true, MaxTurns: 10, AllowYolo: true})
	_ = r.Run(context.Background(), sess.ID)

	if err := r.RejectPlan(context.Background(), sess.ID, "u-approver", "wrong approach"); err != nil {
		t.Fatalf("RejectPlan: %v", err)
	}
	got, _ := store.GetRemediationSession(context.Background(), sess.ID)
	if got.Status != models.RemediationRejected {
		t.Errorf("Status = %q, want %q", got.Status, models.RemediationRejected)
	}
}

func TestApprovePlanRejectsWrongState(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, nil) // still pending
	r := NewRunner(store, driver.NewFake(nil, fixturePlan()), Config{Enabled: true, AllowYolo: true})

	if err := r.ApprovePlan(context.Background(), sess.ID, "u-1"); err == nil {
		t.Fatal("ApprovePlan on a pending session succeeded, want error")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/remediate/ -run Gate -v`
Expected: FAIL — `ApprovePlan` undefined.

- [ ] **Step 3: Implement the approval methods**

```go
// ApprovePlan records approval and resumes the session into its execute phase.
func (r *Runner) ApprovePlan(ctx context.Context, sessionID, approverID string) error {
	sess, err := r.store.GetRemediationSession(ctx, sessionID)
	if err != nil {
		return err
	}
	if sess.Status != models.RemediationPlanReview {
		return fmt.Errorf("session %s is %s, not awaiting plan approval", sessionID, sess.Status)
	}
	if err := r.store.ApproveRemediationPlan(ctx, sessionID, approverID); err != nil {
		return err
	}
	return r.runExecutePhase(ctx, sess)
}

// RejectPlan terminates the session without writing code.
func (r *Runner) RejectPlan(ctx context.Context, sessionID, approverID, reason string) error {
	sess, err := r.store.GetRemediationSession(ctx, sessionID)
	if err != nil {
		return err
	}
	if sess.Status != models.RemediationPlanReview {
		return fmt.Errorf("session %s is %s, not awaiting plan approval", sessionID, sess.Status)
	}
	// Persist the reason on the plan row as well as the session. The plan is
	// what a human reviewed and declined, so the rejection belongs beside it —
	// otherwise remediation_plans.rejected_reason is never written by anything.
	if err := r.store.RejectRemediationPlan(ctx, sessionID, approverID, reason); err != nil {
		return err
	}
	return r.transition(ctx, sess, models.RemediationRejected, reason)
}
```

Add `ApprovePatches` and `RejectPatches` following the same shape, guarding on `models.RemediationPatchReview`. `ApprovePatches` calls the landing phase (a no-op returning `completed` until Task 11 replaces it).

- [ ] **Step 4: Run tests**

Run: `go test ./internal/remediate/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/remediate/
git commit -m "feat(remediate): gate hold and resume-from-approval"
```

---

### Task 10: Remediation API endpoints

**Files:**
- Create: `internal/api/routes/remediations.go`
- Modify: `internal/api/server.go`
- Test: `internal/api/routes/remediations_test.go`

**Interfaces:**
- Consumes: `remediate.Runner`, `models.RemediationSession`, `db.Store`.
- Produces: HTTP handlers `CreateRemediation`, `ListRemediations`, `GetRemediation`, `GetRemediationPlan`, `ApproveRemediationPlan`, `RejectRemediationPlan`, `ListRemediationPatches`, `ApproveRemediationPatches`, `RejectRemediationPatches`, `CancelRemediation`, `StreamRemediation`.

- [ ] **Step 1: Write the failing test**

```go
package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCreateRemediationRequiresWriteScope(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/remediations",
		strings.NewReader(`{"scan_id":"sc-1","repo_id":"r-1"}`))
	req.Header.Set("Content-Type", "application/json")
	withScopes(req, "read:fixes") // read only

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestApprovePlanRejectsWrongState(t *testing.T) {
	srv := newTestServer(t)
	id := seedSessionPending(t, srv)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/remediations/"+id+"/plan/approve", nil)
	withScopes(req, "write:fixes")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (session not awaiting approval)", rec.Code)
	}
}

func TestYoloRejectedWhenNotAllowed(t *testing.T) {
	srv := newTestServerWithConfig(t, remediateConfigYoloDisabled())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/remediations",
		strings.NewReader(`{"scan_id":"sc-1","repo_id":"r-1","plan_gate_enabled":false,"patch_gate_enabled":false}`))
	req.Header.Set("Content-Type", "application/json")
	withScopes(req, "write:fixes")

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (yolo not allowed)", rec.Code)
	}
}
```

Reuse the existing helpers in `internal/api/routes/scans_test.go` for `newTestServer` and scope injection; if they are named differently, use the existing names.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/routes/ -run Remediation -v`
Expected: FAIL — routes not registered, 404.

- [ ] **Step 3: Write the handlers**

Create `internal/api/routes/remediations.go` implementing the eleven handlers. Follow the shape of `internal/api/routes/scans.go`: decode into a request struct, validate, call the store or runner, write JSON. Return `409 Conflict` when an approval targets a session in the wrong state, and `403 Forbidden` when gates are disabled but `AllowYolo` is false.

`StreamRemediation` follows the SSE pattern in `scans.go`'s `StreamScan`, replaying from `ListRemediationEvents(ctx, id, afterSeq)`.

- [ ] **Step 4: Register the routes**

In `internal/api/server.go`, after the `/loops` block:

```go
			r.Route("/remediations", func(r chi.Router) {
				r.With(rFixes).Get("/", routes.ListRemediations)
				r.With(wFixes).Post("/", routes.CreateRemediation)
				r.With(rFixes).Get("/{id}", routes.GetRemediation)
				r.With(rFixes).Get("/{id}/stream", routes.StreamRemediation)
				r.With(rFixes).Get("/{id}/plan", routes.GetRemediationPlan)
				r.With(wFixes).Post("/{id}/plan/approve", routes.ApproveRemediationPlan)
				r.With(wFixes).Post("/{id}/plan/reject", routes.RejectRemediationPlan)
				r.With(rFixes).Get("/{id}/patches", routes.ListRemediationPatches)
				r.With(wFixes).Post("/{id}/patches/approve", routes.ApproveRemediationPatches)
				r.With(wFixes).Post("/{id}/patches/reject", routes.RejectRemediationPatches)
				r.With(wFixes).Delete("/{id}", routes.CancelRemediation)
			})
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/api/... -v && go build ./...`
Expected: PASS, build exit 0.

- [ ] **Step 6: Commit**

```bash
git add internal/api/
git commit -m "feat(remediate): remediation API with gate approval endpoints"
```

---

### Task 10b: Async approve — 202 plus a cancellation registry

**Amendment, decided after Task 10 shipped.** The approve handlers called the
Runner synchronously, so a successful `ApprovePlan` held the HTTP connection for
the entire execute phase. The server's `WriteTimeout` is 15 minutes
(`internal/api/server.go:615`) while the default `WOLF_REMEDIATE_SESSION_TIMEOUT`
is 30 (`internal/remediate/config.go:31`), so the connection died at 15 minutes
while the agent kept working — the operator saw a failure for a run that was
still going, and could not distinguish that from a real one.

An agent run is minutes-to-tens-of-minutes work and does not belong on a request
connection. Approve becomes asynchronous.

**Follow the existing pattern in this repo — do not invent a new one.** Both
scans and loops already do exactly this:

- `internal/api/routes/scans.go:81-82` — `activeScansMu sync.Mutex` guarding
  `activeScanCtxs = make(map[string]context.CancelFunc)`
- `internal/api/routes/scans.go:307` — `go executeScan(context.Background(), …)`
- `internal/api/routes/loops.go:31` — `activeLoopCtxs`, the same shape for a
  resource that also has pause/resume

Note the detached `context.Background()`: the request context is cancelled the
moment the handler returns, so the background phase must not inherit it.

**Files:**
- Modify: `internal/api/routes/remediations.go`
- Test: `internal/api/routes/remediations_test.go`

**Interfaces:**
- Consumes: `remediate.Runner`'s existing approval methods, unchanged.
- Produces: `activeRemediationCtxs` registry; `ApproveRemediationPlan` and `ApproveRemediationPatches` return `202 Accepted`.

**Required shape:**

1. The handler validates and lets the Runner perform its CAS transition, exactly as now — the compare-and-swap must still happen synchronously so a double-clicked approve is rejected with 409 before anything is dispatched. Only the phase *execution* moves to the background.
2. `ctx, cancel := context.WithCancel(context.Background())`, register `cancel` under the session ID in the mutex-guarded map, `go` the phase, and `defer` unregistering it.
3. Return `202 Accepted` with the session body. The client watches `/remediations/{id}/stream` for progress — that SSE endpoint already exists and already replays from `remediation_events`.
4. Reject paths stay synchronous. They make no driver call and complete in milliseconds.
5. `CancelRemediation` looks up the registered `CancelFunc` and calls it, so cancellation reaches an in-flight phase instead of only flipping the row. Keep the existing direct CAS transition for the case where no goroutine is registered (the session is held at a gate, or the process restarted).

**A restart still orphans an in-flight phase** — the goroutine dies with the
process and the row stays `executing`. That is exactly what Task 10a's
`RecoverOrphanSessions` cleans up, and it is why that task marks `planning` and
`executing` sessions failed on startup while leaving `plan_review`/`patch_review`
untouched. The two tasks are complementary; neither is sufficient alone.

- [ ] **Step 1: Write the failing test**

```go
// Approve must return promptly rather than blocking for the phase duration.
// The synchronous version held the connection past the server's WriteTimeout.
func TestApprovePlanReturns202WithoutBlocking(t *testing.T) {
	srv := newTestServer(t)
	id := seedSessionAwaitingPlanApproval(t, srv)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/remediations/"+id+"/plan/approve", nil)
	withScopes(req, "write:fixes")
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() { srv.Router.ServeHTTP(rec, req); close(done) }()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("approve blocked for 5s — it must dispatch and return, not run the phase inline")
	}
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}
}

// A double-clicked approve must still be rejected, so the CAS has to run
// synchronously even though the phase does not.
func TestSecondApproveStillConflicts(t *testing.T) {
	srv := newTestServer(t)
	id := seedSessionAwaitingPlanApproval(t, srv)

	first := httptest.NewRecorder()
	r1 := httptest.NewRequest(http.MethodPost, "/api/v1/remediations/"+id+"/plan/approve", nil)
	withScopes(r1, "write:fixes")
	srv.Router.ServeHTTP(first, r1)
	if first.Code != http.StatusAccepted {
		t.Fatalf("first approve = %d, want 202", first.Code)
	}

	second := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodPost, "/api/v1/remediations/"+id+"/plan/approve", nil)
	withScopes(r2, "write:fixes")
	srv.Router.ServeHTTP(second, r2)
	if second.Code != http.StatusConflict {
		t.Fatalf("second approve = %d, want 409", second.Code)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/api/routes/ -run 'Approve' -v`
Expected: FAIL — the current handler blocks and returns 200.

- [ ] **Step 3: Implement the registry and dispatch**, following `scans.go:81-82` and `:307`.

- [ ] **Step 4: Wire `CancelRemediation` to the registry.**

- [ ] **Step 5: Run tests, regenerate the lock last, commit.**

```bash
git add internal/api/ scanners/scanner-lock.yaml
git commit -m "feat(remediate): dispatch approved phases asynchronously"
```

---

### Task 10a: Error-path hardening

Covers three spec requirements the happy-path tasks skip: malformed plan retry, orphan recovery, and the opt-in integration test.

**Files:**
- Modify: `internal/remediate/session.go`, `internal/api/server.go`
- Create: `internal/remediate/integration_test.go`
- Test: `internal/remediate/session_test.go`

**Interfaces:**
- Consumes: `plan.Parse`, `driver.Driver`, `db.Store`.
- Produces: `remediate.RecoverOrphanSessions(ctx context.Context, store db.Store) error`.

- [ ] **Step 1: Write the failing test**

```go
// A malformed plan is retried once with a repair prompt; a second failure fails
// the session.
func TestMalformedPlanRetriesOnce(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, func(s *models.RemediationSession) {
		s.PlanGateEnabled = false
		s.PatchGateEnabled = false
	})
	d := driver.NewFake([]meter.Event{{Type: "assistant"}}, nil)
	d.PlanErr = errors.New("parse plan: unexpected end of JSON input")

	// Three-arg NewRunner: Task 15 adds the credential resolver parameter and
	// updates this call site.
	r := NewRunner(store, d, Config{Enabled: true, MaxTurns: 10, AllowYolo: true})
	if err := r.Run(context.Background(), sess.ID); err == nil {
		t.Fatal("Run succeeded with an unparseable plan, want error")
	}
	if d.PlanCalls != 2 {
		t.Errorf("Plan called %d times, want 2 (one retry)", d.PlanCalls)
	}
	got, _ := store.GetRemediationSession(context.Background(), sess.ID)
	if got.Status != models.RemediationFailed {
		t.Errorf("Status = %q, want %q", got.Status, models.RemediationFailed)
	}
}

// Sessions mid-run when the process died are failed on startup; sessions
// holding no process are left alone.
func TestRecoverOrphanSessions(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	orphaned := seedSessionWithStatus(t, store, models.RemediationExecuting)
	planning := seedSessionWithStatus(t, store, models.RemediationPlanning)
	awaiting := seedSessionWithStatus(t, store, models.RemediationPlanReview)

	if err := RecoverOrphanSessions(ctx, store); err != nil {
		t.Fatalf("RecoverOrphanSessions: %v", err)
	}

	for _, id := range []string{orphaned.ID, planning.ID} {
		got, _ := store.GetRemediationSession(ctx, id)
		if got.Status != models.RemediationFailed {
			t.Errorf("session %s = %q, want %q", id, got.Status, models.RemediationFailed)
		}
	}
	got, _ := store.GetRemediationSession(ctx, awaiting.ID)
	if got.Status != models.RemediationPlanReview {
		t.Errorf("gated session was recovered: %q — it holds no process", got.Status)
	}
}
```

Add a `PlanCalls int` counter to `driver.Fake`, incremented in `Plan`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/remediate/ -run 'Malformed|Orphan' -v`
Expected: FAIL — `RecoverOrphanSessions` undefined, `PlanCalls` undefined.

- [ ] **Step 3: Implement the retry**

In `runPlanPhase`, wrap the driver call:

```go
	p, usage, err := r.driver.Plan(ctx, req)
	if err != nil && strings.Contains(err.Error(), "parse plan") {
		// One repair attempt: the agent produced output that was not a valid
		// plan. A second failure is not worth more turns.
		req.RepairHint = "Your previous response was not valid plan JSON. " +
			"Respond with the plan object only, no prose."
		p, usage, err = r.driver.Plan(ctx, req)
	}
```

Add `RepairHint string` to `driver.PlanRequest`; the exec driver appends it to the prompt when non-empty.

- [ ] **Step 4: Implement orphan recovery**

```go
// RecoverOrphanSessions fails sessions that were mid-run when the process
// died. Sessions in a review state hold no process and are left untouched —
// that is the point of the stateless gate design.
func RecoverOrphanSessions(ctx context.Context, store db.Store) error {
	stuck := []models.RemediationStatus{
		models.RemediationPlanning,
		models.RemediationExecuting,
		models.RemediationApplying,
		models.RemediationRescanning,
	}
	for _, status := range stuck {
		sessions, err := store.ListRemediationSessionsByStatus(ctx, status)
		if err != nil {
			return err
		}
		for i := range sessions {
			s := &sessions[i]
			s.Status = models.RemediationFailed
			s.FailureReason = "server restarted while the session was running"
			s.UpdatedAt = time.Now()
			if err := store.UpdateRemediationSession(ctx, s); err != nil {
				return err
			}
		}
	}
	return nil
}
```

Add `ListRemediationSessionsByStatus` to `Store` and both implementations. Call `RecoverOrphanSessions` from `Server.Start()` in `internal/api/server.go`, beside the existing `recoverOrphanScans(s.Store)` call.

- [ ] **Step 5: Write the opt-in integration test**

`internal/remediate/integration_test.go`:

```go
//go:build integration

package remediate

import (
	"context"
	"os"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/remediate/driver"
)

// TestRealOpenCodeRun exercises one genuine opencode run against a fixture
// repo. Skipped unless a credential is present, so CI without credentials
// stays green.
//
// Run with: go test -tags integration ./internal/remediate/ -run TestRealOpenCodeRun -v
func TestRealOpenCodeRun(t *testing.T) {
	auth := os.Getenv("WOLF_TEST_OPENCODE_AUTH")
	if auth == "" {
		t.Skip("WOLF_TEST_OPENCODE_AUTH not set")
	}
	image := os.Getenv("WOLF_TEST_OPENCODE_IMAGE")
	if image == "" {
		t.Skip("WOLF_TEST_OPENCODE_IMAGE not set")
	}

	d := driver.NewExec(driver.ExecConfig{Image: image})
	p, usage, err := d.Plan(context.Background(), driver.PlanRequest{
		WorktreePath: t.TempDir(),
		MaxTurns:     5,
		AuthContent:  auth,
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if usage.Turns == 0 {
		t.Error("Usage.Turns = 0 — the meter did not recognize any turn")
	}
	if p == nil || len(p.Items) == 0 {
		t.Error("plan is empty")
	}
}
```

- [ ] **Step 6: Run tests**

Run: `go test ./internal/remediate/... -v && go build ./...`
Expected: PASS (integration test excluded — no build tag).

Then confirm the tagged test compiles: `go vet -tags integration ./internal/remediate/`

- [ ] **Step 7: Commit**

```bash
git add internal/remediate/ internal/api/server.go internal/db/
git commit -m "feat(remediate): plan repair retry, orphan recovery, integration test"
```

---

**Phase 2 gate:** `go build ./... && go test ./...` green. Sessions can be created and gated over HTTP, and a pending approval survives a server restart.

---

# Phase 3 — Landing

Replaces artifact-only output with branch, rescan, delta, and PR.

### Task 11: Worktree and branch setup

**Files:**
- Modify: `internal/remediate/session.go`
- Create: `internal/remediate/workspace.go`
- Test: `internal/remediate/workspace_test.go`

**Interfaces:**
- Consumes: `workspace.Prepare`, `workspace.Options`, `workspace.Workspace` from `internal/fix/workspace`; `StripAgentConfig` from Task 4a.
- Produces: `Runner.prepareWorkspace(ctx, sess) (*workspace.Workspace, error)`, `remediate.BranchName(sessionID string) string`, `cloneLocalForRemediation(ctx, sourcePath string) (*models.Repo, func(), error)`.

**Two constraints this task must satisfy, both found by review of Task 4:**

1. **Local repos are cloned, never worktree-added.** `git worktree add` leaves a `.git` file pointing at the parent repository's object store, which the driver must mount read-write into the container — giving an agent under `--auto` write access to the user's real refs and objects. `git clone --local` hardlinks objects (cheap, no network) and yields a disposable `.git` directory. `cloneLocalForRemediation` does the clone and returns a `*models.Repo` describing the copy plus a cleanup func. Write a test asserting the prepared workspace's git dir is NOT inside the source repository.

2. **Retry must work.** `BranchName` is deterministic, and `git worktree add -b <existing>` fails with `fatal: a branch named '...' already exists` — so retrying a local session currently dies here before reaching the driver. Cloning fixes this incidentally (each run clones fresh), but add a test that runs `prepareWorkspace` twice for the same session ID and succeeds both times.

- [ ] **Step 1: Write the failing test**

```go
package remediate

import (
	"strings"
	"testing"
)

func TestBranchNameIsStableAndScoped(t *testing.T) {
	got := BranchName("rs-42")
	if got != "wolf/remediation-rs-42" {
		t.Fatalf("BranchName = %q, want %q", got, "wolf/remediation-rs-42")
	}
	if strings.Contains(got, " ") {
		t.Error("branch name contains a space")
	}
	if BranchName("rs-42") != got {
		t.Error("BranchName is not deterministic")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/remediate/ -run TestBranchName -v`
Expected: FAIL — `BranchName` undefined.

- [ ] **Step 3: Implement**

```go
package remediate

import (
	"context"
	"fmt"

	"github.com/alphabravocompany/thewolf/internal/fix/workspace"
	"github.com/alphabravocompany/thewolf/internal/models"
)

// BranchName is the remediation branch for a session. Deterministic so a
// retried session reuses its branch rather than accumulating orphans.
func BranchName(sessionID string) string {
	return "wolf/remediation-" + sessionID
}

// prepareWorkspace creates the isolated workspace the agent edits. The default
// branch is never checked out for writing.
//
// Local repositories are cloned, not worktree-added. `git worktree add` leaves
// a .git FILE pointing at the parent repository's object store, which the
// driver must then mount read-write into the container — handing an agent
// running under --auto write access to the user's real refs and objects, not
// just to an ephemeral checkout. `git clone --local` hardlinks objects (cheap,
// no network) and produces a self-contained, disposable .git directory, so the
// blast radius stops at the scratch clone.
//
// This is why the remediate path does not reuse workspace.prepareLocal.
// GitHub-sourced repos already clone, so they go through workspace.Prepare
// unchanged.
func (r *Runner) prepareWorkspace(ctx context.Context, sess *models.RemediationSession) (*workspace.Workspace, error) {
	repo, err := r.store.GetRepoByID(ctx, sess.RepoID)
	if err != nil {
		return nil, fmt.Errorf("load repo: %w", err)
	}

	opts := workspace.Options{Repo: repo, Branch: BranchName(sess.ID)}
	if repo.SourceType == models.SourceTypeLocal {
		// Clone into scratch first, then let workspace.Prepare operate on the
		// disposable copy rather than the user's checkout.
		cloned, cleanup, err := cloneLocalForRemediation(ctx, repo.SourcePath)
		if err != nil {
			return nil, fmt.Errorf("clone local repo: %w", err)
		}
		r.registerCleanup(sess.ID, cleanup)
		opts.Repo = cloned
	}

	ws, err := workspace.Prepare(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("prepare workspace: %w", err)
	}
	sess.WorktreePath = ws.Path()
	sess.BranchName = ws.Branch()

	// Repo-supplied agent config outranks Wolf's injected document, so strip it
	// before any driver call. See Task 4a.
	removed, err := StripAgentConfig(ws.Path())
	if err != nil {
		return nil, fmt.Errorf("strip agent config: %w", err)
	}
	for _, path := range removed {
		r.recordEvent(ctx, sess.ID, "worktree.config_stripped", path)
	}
	return ws, nil
}
```

Call `prepareWorkspace` at the top of `Run` before `runPlanPhase`, and `ws.Cleanup(ctx)` on every terminal path except `failed` — a failed session retains its worktree for inspection, per the spec.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/remediate/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/remediate/
git commit -m "feat(remediate): isolated worktree on a deterministic branch"
```

---

### Task 12: Rescan and delta

**Files:**
- Create: `internal/remediate/delta.go`
- Test: `internal/remediate/delta_test.go`

**Interfaces:**
- Consumes: `models.Finding`.
- Produces: `remediate.Delta` struct with `Fixed`, `Remaining`, `New []models.Finding` and `Regressed() bool`; `remediate.ComputeDelta(before, after []models.Finding) Delta`.

- [ ] **Step 1: Write the failing test**

```go
package remediate

import (
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func f(id string) models.Finding { return models.Finding{ID: id} }

func TestComputeDelta(t *testing.T) {
	before := []models.Finding{f("a"), f("b"), f("c")}
	after := []models.Finding{f("b"), f("d")}

	d := ComputeDelta(before, after)

	if len(d.Fixed) != 2 {
		t.Errorf("Fixed = %d, want 2 (a, c)", len(d.Fixed))
	}
	if len(d.Remaining) != 1 || d.Remaining[0].ID != "b" {
		t.Errorf("Remaining = %v, want [b]", d.Remaining)
	}
	if len(d.New) != 1 || d.New[0].ID != "d" {
		t.Errorf("New = %v, want [d]", d.New)
	}
}

func TestRegressedWhenNetWorse(t *testing.T) {
	before := []models.Finding{f("a")}
	after := []models.Finding{f("a"), f("b"), f("c")}

	if !ComputeDelta(before, after).Regressed() {
		t.Fatal("Regressed() = false, want true — remediation added findings")
	}
}

func TestNotRegressedWhenNetBetter(t *testing.T) {
	before := []models.Finding{f("a"), f("b"), f("c")}
	after := []models.Finding{f("d")}

	if ComputeDelta(before, after).Regressed() {
		t.Fatal("Regressed() = true, want false — net improvement")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/remediate/ -run Delta -v`
Expected: FAIL — `ComputeDelta` undefined.

- [ ] **Step 3: Implement**

```go
package remediate

import "github.com/alphabravocompany/thewolf/internal/models"

// Delta is the difference between the baseline scan and the rescan of the
// remediation branch.
type Delta struct {
	Fixed     []models.Finding
	Remaining []models.Finding
	New       []models.Finding
}

// ComputeDelta diffs two finding sets by ID.
func ComputeDelta(before, after []models.Finding) Delta {
	afterByID := make(map[string]models.Finding, len(after))
	for _, x := range after {
		afterByID[x.ID] = x
	}
	beforeByID := make(map[string]struct{}, len(before))

	var d Delta
	for _, x := range before {
		beforeByID[x.ID] = struct{}{}
		if _, still := afterByID[x.ID]; still {
			d.Remaining = append(d.Remaining, x)
		} else {
			d.Fixed = append(d.Fixed, x)
		}
	}
	for _, x := range after {
		if _, existed := beforeByID[x.ID]; !existed {
			d.New = append(d.New, x)
		}
	}
	return d
}

// Regressed reports whether remediation left more findings than it started
// with. A regressed session still completes, but is flagged and never
// auto-merged.
func (d Delta) Regressed() bool {
	return len(d.Remaining)+len(d.New) > len(d.Fixed)+len(d.Remaining)
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/remediate/ -run Delta -v`
Expected: PASS — three tests.

- [ ] **Step 5: Commit**

```bash
git add internal/remediate/delta.go internal/remediate/delta_test.go
git commit -m "feat(remediate): scan delta with regression detection"
```

---

### Task 13: Push branch and open PR

> **Scope amendment (human-directed):** land the **branch push only** for now.
> PR creation is explicitly deferred — build `DeltaTable` (it is the PR body and
> is worth having ready), push the branch, and record the delta on the session,
> but do NOT call `pr.CreateGitHubPR` / `pr.CreateGitLabMR` yet. Leave
> `Runner.land` structured so opening the PR is a later addition rather than a
> rewrite: land should end at "branch pushed, delta recorded", with the PR call
> as the obvious next line.
>
> Consequence for the session state machine: a completed session's terminal
> state is reached with `BranchName` and the delta populated and `PRURL` empty.
> `PRURL` staying empty is therefore a normal outcome, not an error — do not add
> a check that treats it as one.

**Files:**
- Create: `internal/remediate/land.go`
- Test: `internal/remediate/land_test.go`

**Interfaces:**
- Consumes: `pr.PushBranch`, `pr.CreateGitHubPR`, `pr.PRRequest`, `pr.PRResult`, `remediate.Delta`.
- Produces: `remediate.DeltaTable(d Delta) string`, `Runner.land(ctx, sess, d Delta) error`.

- [ ] **Step 1: Write the failing test**

```go
package remediate

import (
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestDeltaTableReportsCounts(t *testing.T) {
	d := Delta{
		Fixed:     []models.Finding{f("a"), f("b")},
		Remaining: []models.Finding{f("c")},
		New:       []models.Finding{f("d")},
	}
	body := DeltaTable(d)

	for _, want := range []string{"Fixed", "Remaining", "New", "2", "1"} {
		if !strings.Contains(body, want) {
			t.Errorf("DeltaTable missing %q:\n%s", want, body)
		}
	}
}

func TestDeltaTableFlagsRegression(t *testing.T) {
	d := Delta{
		Fixed: []models.Finding{f("a")},
		New:   []models.Finding{f("b"), f("c"), f("d")},
	}
	body := DeltaTable(d)
	if !strings.Contains(strings.ToLower(body), "regress") {
		t.Fatalf("regressed delta not flagged in PR body:\n%s", body)
	}
}

// The PR body is rendered from scan data and must never carry credentials.
func TestDeltaTableNeverRendersCredentials(t *testing.T) {
	d := Delta{Fixed: []models.Finding{{ID: "a", Title: `secret=dckr_pat_never_render`}}}
	body := DeltaTable(d)
	if strings.Contains(body, "dckr_pat_never_render") {
		t.Fatal("credential-shaped finding text rendered into PR body")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/remediate/ -run DeltaTable -v`
Expected: FAIL — `DeltaTable` undefined.

- [ ] **Step 3: Implement**

```go
package remediate

import (
	"fmt"
	"strings"
)

// credentialPattern matches token shapes that must never reach a PR body.
var credentialPattern = regexp.MustCompile(`(?i)(dckr_pat|github_pat|ghp_|sk-|xox[baprs]-)[A-Za-z0-9_\-]+`)

// redactCredentials replaces credential-shaped substrings. Finding titles are
// derived from scanned source and can contain real secrets.
func redactCredentials(s string) string {
	return credentialPattern.ReplaceAllString(s, "[REDACTED]")
}

// DeltaTable renders the scan delta as the PR body.
func DeltaTable(d Delta) string {
	var b strings.Builder
	b.WriteString("## Wolf remediation results\n\n")
	b.WriteString("| Outcome | Count |\n|---|---|\n")
	fmt.Fprintf(&b, "| Fixed | %d |\n", len(d.Fixed))
	fmt.Fprintf(&b, "| Remaining | %d |\n", len(d.Remaining))
	fmt.Fprintf(&b, "| New | %d |\n", len(d.New))

	if d.Regressed() {
		b.WriteString("\n> **Regression:** this branch has more findings than the baseline. Do not merge without review.\n")
	}
	if len(d.Fixed) > 0 {
		b.WriteString("\n### Fixed\n")
		for _, x := range d.Fixed {
			fmt.Fprintf(&b, "- %s\n", redactCredentials(x.Title))
		}
	}
	if len(d.New) > 0 {
		b.WriteString("\n### Newly introduced\n")
		for _, x := range d.New {
			fmt.Fprintf(&b, "- %s\n", redactCredentials(x.Title))
		}
	}
	return b.String()
}
```

Add `import "regexp"`. Implement `Runner.land` to call `pr.PushBranch(ctx, sess.WorktreePath, sess.BranchName)` then `pr.CreateGitHubPR` with `DeltaTable(d)` as the body, storing the resulting URL in `sess.PRURL`.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/remediate/ -v`
Expected: PASS — three DeltaTable tests plus existing.

- [ ] **Step 5: Commit**

```bash
git add internal/remediate/
git commit -m "feat(remediate): push branch and open PR with scan delta"
```

---

**Phase 3 gate:** A full session produces a PR whose body carries the delta table, with regressions flagged and credentials redacted.

---

# Phase 4 — Credentials

### Task 14: Credential storage

**Files:**
- Modify: `internal/models/types.go`
- Create: `internal/remediate/credential/credential.go`
- Test: `internal/remediate/credential/credential_test.go`

**Interfaces:**
- Consumes: `models.Secret`, `db.Store`.
- Produces: `models.KeyTypeOpenCodeAuth`, `models.ServiceUserID`, `credential.Resolver`, `credential.NewResolver(store db.Store) *Resolver`, `Resolver.Resolve(ctx, userID, provider string) (string, error)`.

- [ ] **Step 1: Add the constants**

In `internal/models/types.go`, alongside the existing `KeyType` constants:

```go
	// KeyTypeOpenCodeAuth stores one OpenCode provider credential — the
	// auth.json entry verbatim, keyed by provider ID in KeyName.
	KeyTypeOpenCodeAuth KeyType = "opencode_auth" // #nosec G101 -- key-type identifier, not a credential value
```

And a package-level constant:

```go
// ServiceUserID owns credentials used by scheduled runs that have no
// initiating user. It must be excluded from per-user secret listings; only
// admins may write its credentials.
const ServiceUserID = "system:service"
```

- [ ] **Step 2: Write the failing test**

```go
package credential

import (
	"context"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestResolvePrefersUserCredential(t *testing.T) {
	store := newTestStore(t)
	seedSecret(t, store, "u-1", "grok", `{"type":"api","key":"USER_KEY"}`)
	seedSecret(t, store, models.ServiceUserID, "grok", `{"type":"api","key":"SERVICE_KEY"}`)

	got, err := NewResolver(store).Resolve(context.Background(), "u-1", "grok")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !strings.Contains(got, "USER_KEY") {
		t.Errorf("resolved service credential, want user credential: %s", got)
	}
}

func TestResolveFallsBackToService(t *testing.T) {
	store := newTestStore(t)
	seedSecret(t, store, models.ServiceUserID, "grok", `{"type":"api","key":"SERVICE_KEY"}`)

	got, err := NewResolver(store).Resolve(context.Background(), "u-nobody", "grok")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !strings.Contains(got, "SERVICE_KEY") {
		t.Errorf("did not fall back to service credential: %s", got)
	}
}

func TestResolveErrorsWhenNoCredential(t *testing.T) {
	store := newTestStore(t)
	_, err := NewResolver(store).Resolve(context.Background(), "u-1", "grok")
	if err == nil {
		t.Fatal("Resolve succeeded with no credential, want error")
	}
	if !strings.Contains(err.Error(), "grok") {
		t.Errorf("error does not name the provider: %v", err)
	}
}

// Resolve renders the OPENCODE_AUTH_CONTENT payload: a provider-keyed object.
func TestResolveRendersProviderKeyedObject(t *testing.T) {
	store := newTestStore(t)
	seedSecret(t, store, "u-1", "grok", `{"type":"api","key":"K"}`)

	got, err := NewResolver(store).Resolve(context.Background(), "u-1", "grok")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(got), `{"grok":`) {
		t.Errorf("payload not provider-keyed: %s", got)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/remediate/credential/ -v`
Expected: FAIL — package does not exist.

- [ ] **Step 4: Implement the resolver**

```go
// Package credential resolves LLM credentials for a remediation run and
// renders them into the OPENCODE_AUTH_CONTENT payload, which bypasses
// OpenCode's auth.json file storage entirely.
package credential

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
)

// Resolver finds the credential a session should run with.
type Resolver struct{ store db.Store }

// NewResolver returns a Resolver backed by store.
func NewResolver(store db.Store) *Resolver { return &Resolver{store: store} }

// Resolve returns the OPENCODE_AUTH_CONTENT payload for userID and provider.
// Order: the user's own credential, then the service identity's. Scheduled
// runs pass models.ServiceUserID and land on the fallback directly.
func (r *Resolver) Resolve(ctx context.Context, userID, provider string) (string, error) {
	for _, owner := range []string{userID, models.ServiceUserID} {
		if owner == "" {
			continue
		}
		entry, err := r.lookup(ctx, owner, provider)
		if err != nil {
			return "", err
		}
		if entry == "" {
			continue
		}
		payload, err := json.Marshal(map[string]json.RawMessage{
			provider: json.RawMessage(entry),
		})
		if err != nil {
			return "", fmt.Errorf("render auth content: %w", err)
		}
		return string(payload), nil
	}
	return "", fmt.Errorf("no credential for provider %q (checked user %q and the service identity)", provider, userID)
}
```

Add `lookup`, which calls the store's decrypted-secret getter filtered by `models.KeyTypeOpenCodeAuth` and `KeyName == provider`, returning `""` when absent. Use the existing secret-decryption helper rather than writing a new one.

- [ ] **Step 5: Run tests**

Run: `go test ./internal/remediate/credential/ -v && go build ./...`
Expected: PASS, build exit 0.

- [ ] **Step 6: Commit**

```bash
git add internal/models/types.go internal/remediate/credential/
git commit -m "feat(remediate): per-user credential resolution with service fallback"
```

---

### Task 15: Wire credentials into the driver

**Files:**
- Modify: `internal/remediate/session.go`
- Test: `internal/remediate/session_test.go`

**Interfaces:**
- Consumes: `credential.Resolver`.
- Produces: `NewRunner` gains a resolver parameter — `NewRunner(store db.Store, d driver.Driver, res *credential.Resolver, cfg Config) *Runner`.

- [ ] **Step 1: Write the failing test**

```go
func TestRunFailsFastWithoutCredential(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, func(s *models.RemediationSession) {
		s.Provider = "grok"
		s.PlanGateEnabled = false
		s.PatchGateEnabled = false
	})
	// No credential seeded.
	r := NewRunner(store, driver.NewFake([]meter.Event{{Type: "assistant"}}, fixturePlan()),
		credential.NewResolver(store), Config{Enabled: true, MaxTurns: 10, AllowYolo: true})

	err := r.Run(context.Background(), sess.ID)
	if err == nil {
		t.Fatal("Run succeeded without a credential, want error")
	}
	got, _ := store.GetRemediationSession(context.Background(), sess.ID)
	if got.Status != models.RemediationFailed {
		t.Errorf("Status = %q, want %q", got.Status, models.RemediationFailed)
	}
	if got.TurnsUsedPlan != 0 {
		t.Error("turns consumed despite missing credential — did not fail before starting a container")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/remediate/ -run WithoutCredential -v`
Expected: FAIL — `NewRunner` takes three arguments.

- [ ] **Step 3: Implement**

Add the `resolver *credential.Resolver` field to `Runner` and the parameter to `NewRunner`. In `Run`, resolve before `prepareWorkspace`:

```go
	authContent, err := r.resolver.Resolve(ctx, sess.UserID, sess.Provider)
	if err != nil {
		_ = r.transition(ctx, sess, models.RemediationFailed, err.Error())
		return err
	}
```

Pass `authContent` through both `driver.PlanRequest` and `driver.ExecuteRequest`. Update every existing `NewRunner` call site in tests.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/remediate/... -v && go build ./...`
Expected: PASS, build exit 0.

- [ ] **Step 5: Commit**

```bash
git add internal/remediate/
git commit -m "feat(remediate): resolve credentials before starting a session"
```

---

### Task 16: Ephemeral login container

**Files:**
- Create: `internal/remediate/credential/login.go`
- Test: `internal/remediate/credential/login_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `credential.LoginSession`, `credential.StartLogin(ctx, provider string, opts LoginOptions) (*LoginSession, error)`, `LoginSession.DeviceURL() <-chan string`, `LoginSession.Wait(ctx) (authEntry string, err error)`, `credential.ParseDeviceURL(line string) (string, bool)`.

- [ ] **Step 1: Write the failing test**

```go
package credential

import "testing"

func TestParseDeviceURL(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string
		ok   bool
	}{
		{"plain https", "Visit https://grok.com/device?code=XYZ to continue", "https://grok.com/device?code=XYZ", true},
		{"openai style", "Open https://auth.openai.com/activate and enter ABCD-EFGH", "https://auth.openai.com/activate", true},
		{"no url", "Waiting for authorization...", "", false},
		{"http rejected", "Visit http://insecure.example/device", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseDeviceURL(tt.line)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if got != tt.want {
				t.Errorf("url = %q, want %q", got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/remediate/credential/ -run ParseDeviceURL -v`
Expected: FAIL — `ParseDeviceURL` undefined.

- [ ] **Step 3: Implement**

```go
package credential

import "regexp"

// deviceURLPattern matches the verification URL OpenCode prints during an
// OAuth login. Only https is accepted: an http URL in this position would
// mean a downgraded or spoofed flow.
var deviceURLPattern = regexp.MustCompile(`https://[^\s"']+`)

// ParseDeviceURL extracts the verification URL from one line of login output.
func ParseDeviceURL(line string) (string, bool) {
	match := deviceURLPattern.FindString(line)
	if match == "" {
		return "", false
	}
	return match, true
}
```

Implement `StartLogin` to run `docker run --rm -i <opencode-image> providers login --provider <p>`, scanning stdout for a device URL and publishing it on a channel, then on exit reading `auth.json` from the container's data dir and returning the provider entry. **If the spike found this needs a PTY, add `-t` to the docker args and note it in a comment.**

- [ ] **Step 4: Run tests**

Run: `go test ./internal/remediate/credential/ -v`
Expected: PASS — four sub-cases.

- [ ] **Step 5: Commit**

```bash
git add internal/remediate/credential/
git commit -m "feat(remediate): ephemeral opencode login container"
```

---

### Task 17: Provider connection endpoints

**Files:**
- Create: `internal/api/routes/opencode_providers.go`
- Modify: `internal/api/server.go`
- Test: `internal/api/routes/opencode_providers_test.go`

**Interfaces:**
- Consumes: `credential.StartLogin`, `credential.Resolver`, `models.KeyTypeOpenCodeAuth`, `models.ServiceUserID`.
- Produces: handlers `ListOpenCodeProviders`, `ConnectOpenCodeProvider`, `StreamOpenCodeProviderConnect`, `DisconnectOpenCodeProvider`.

- [ ] **Step 1: Write the failing test**

```go
func TestListProvidersNeverReturnsSecretValues(t *testing.T) {
	srv := newTestServer(t)
	seedOpenCodeSecret(t, srv, "u-1", "grok", `{"type":"api","key":"dckr_pat_never_render"}`)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/opencode/providers", nil)
	withScopes(req, "read:config")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "dckr_pat_never_render") {
		t.Fatal("credential value leaked into the providers listing")
	}
	if !strings.Contains(rec.Body.String(), "grok") {
		t.Error("connected provider not listed")
	}
}

func TestServiceCredentialRequiresAdmin(t *testing.T) {
	srv := newTestServer(t)
	body := strings.NewReader(`{"scope":"service"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/config/opencode/providers/grok/connect", body)
	req.Header.Set("Content-Type", "application/json")
	withScopes(req, "write:config") // no admin

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/routes/ -run OpenCodeProvider -v`
Expected: FAIL — 404.

- [ ] **Step 3: Implement the handlers**

Create `internal/api/routes/opencode_providers.go`. `ListOpenCodeProviders` reads only `KeyName` and `MetadataJSON` — never `EncryptedValue` — so connection state renders without decrypting. `ConnectOpenCodeProvider` starts a login session and returns its ID; `StreamOpenCodeProviderConnect` SSEs the device URL then the completion event. Writing a `"scope":"service"` credential requires the `admin` scope.

- [ ] **Step 4: Register the routes**

In `internal/api/server.go`, inside the existing `/config` block:

```go
				r.With(rConfig).Get("/opencode/providers", routes.ListOpenCodeProviders)
				r.With(wConfig).Post("/opencode/providers/{provider}/connect", routes.ConnectOpenCodeProvider)
				r.With(wConfig).Get("/opencode/providers/{provider}/connect/stream", routes.StreamOpenCodeProviderConnect)
				r.With(wConfig).Delete("/opencode/providers/{provider}", routes.DisconnectOpenCodeProvider)
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/api/... -v && go build ./...`
Expected: PASS, build exit 0.

- [ ] **Step 6: Commit**

```bash
git add internal/api/
git commit -m "feat(remediate): opencode provider connection endpoints"
```

---

**Phase 4 gate:** A user can connect Grok or OpenAI from the API, and sessions run with their credentials. Scheduled runs fall back to the service identity.

---

# Phase 5 — UI

### Task 18: API client and types

**Files:**
- Create: `ui/src/lib/remediation-api.ts`
- Test: `ui/src/lib/remediation-api.test.ts`

**Interfaces:**
- Consumes: the existing UI fetch wrapper.
- Produces: TypeScript types `RemediationSession`, `RemediationPlan`, `RemediationPatch`, `RemediationStatus`, and functions `listRemediations`, `getRemediation`, `createRemediation`, `approvePlan`, `rejectPlan`, `approvePatches`, `rejectPatches`, `cancelRemediation`, `listProviders`, `connectProvider`, `disconnectProvider`.

- [ ] **Step 1: Write the failing test**

```ts
import { describe, expect, it, vi } from "vitest";
import { approvePlan, createRemediation } from "./remediation-api";

describe("remediation-api", () => {
  it("posts gate flags when creating a session", async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true, json: async () => ({ id: "rs-1" }),
    });
    vi.stubGlobal("fetch", fetchMock);

    await createRemediation({
      scan_id: "sc-1", repo_id: "r-1",
      plan_gate_enabled: true, patch_gate_enabled: false,
    });

    const [, init] = fetchMock.mock.calls[0];
    const body = JSON.parse(init.body);
    expect(body.plan_gate_enabled).toBe(true);
    expect(body.patch_gate_enabled).toBe(false);
  });

  it("surfaces a 409 as a typed conflict error", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: false, status: 409, json: async () => ({ error: "not awaiting approval" }),
    }));

    await expect(approvePlan("rs-1")).rejects.toThrow(/not awaiting approval/);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && npx vitest run src/lib/remediation-api.test.ts`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement**

Create `ui/src/lib/remediation-api.ts` with the types and functions, following the existing client conventions in `ui/src/lib/`. Map a 409 response to a thrown error carrying the server's `error` string.

- [ ] **Step 4: Run tests**

Run: `cd ui && npx vitest run src/lib/remediation-api.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add ui/src/lib/
git commit -m "feat(remediate): UI API client for remediation sessions"
```

---

### Task 19: Session list and detail panel

**Files:**
- Create: `ui/src/components/remediation/sessions-panel.tsx`, `ui/src/components/remediation/session-status.tsx`
- Test: `ui/src/components/remediation/sessions-panel.test.tsx`

**Interfaces:**
- Consumes: `remediation-api` types and functions.
- Produces: `<SessionsPanel />`, `<SessionStatus status={...} />`.

- [ ] **Step 1: Write the failing test**

```tsx
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { SessionsPanel } from "./sessions-panel";

vi.mock("../../lib/remediation-api", () => ({
  listRemediations: vi.fn().mockResolvedValue([
    { id: "rs-1", status: "plan_review", provider: "grok", turns_used_plan: 4,
      max_turns: 20, branch_name: "wolf/remediation-rs-1", pr_url: "" },
  ]),
}));

describe("SessionsPanel", () => {
  it("renders a session awaiting plan approval", async () => {
    render(<SessionsPanel />);
    expect(await screen.findByText(/plan review/i)).toBeInTheDocument();
    expect(screen.getByText(/wolf\/remediation-rs-1/)).toBeInTheDocument();
  });

  it("shows turn usage against budget", async () => {
    render(<SessionsPanel />);
    expect(await screen.findByText(/4\s*\/\s*20/)).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && npx vitest run src/components/remediation/sessions-panel.test.tsx`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement**

Create the two components following the conventions in `ui/src/components/scanner-supply-chain/`. Reuse the shared panel primitives rather than introducing new layout patterns.

- [ ] **Step 4: Run tests**

Run: `cd ui && npx vitest run src/components/remediation/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add ui/src/components/remediation/
git commit -m "feat(remediate): remediation sessions panel"
```

---

### Task 20: Plan and patch review

**Files:**
- Create: `ui/src/components/remediation/plan-review.tsx`, `ui/src/components/remediation/patch-review.tsx`
- Test: `ui/src/components/remediation/plan-review.test.tsx`, `ui/src/components/remediation/patch-review.test.tsx`

**Interfaces:**
- Consumes: `approvePlan`, `rejectPlan`, `approvePatches`, `rejectPatches`.
- Produces: `<PlanReview sessionId={...} />`, `<PatchReview sessionId={...} />`.

- [ ] **Step 1: Write the failing test**

```tsx
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { PlanReview } from "./plan-review";

const approvePlan = vi.fn().mockResolvedValue({});
vi.mock("../../lib/remediation-api", () => ({
  approvePlan: (...args: unknown[]) => approvePlan(...args),
  rejectPlan: vi.fn(),
  getRemediationPlan: vi.fn().mockResolvedValue({
    summary: "7 of 23 actionable",
    items: [
      { finding_id: "f-1", action: "fix", rationale: "SQL injection" },
      { finding_id: "f-2", action: "skip", rationale: "test fixture" },
    ],
  }),
}));

describe("PlanReview", () => {
  it("shows fix and skip dispositions with rationale", async () => {
    render(<PlanReview sessionId="rs-1" />);
    expect(await screen.findByText(/SQL injection/)).toBeInTheDocument();
    expect(screen.getByText(/test fixture/)).toBeInTheDocument();
  });

  it("approves the plan on confirm", async () => {
    render(<PlanReview sessionId="rs-1" />);
    await userEvent.click(await screen.findByRole("button", { name: /approve/i }));
    expect(approvePlan).toHaveBeenCalledWith("rs-1");
  });

  it("renders agent narration as text, never HTML", async () => {
    render(<PlanReview sessionId="rs-1" />);
    await screen.findByText(/SQL injection/);
    expect(document.querySelector("script")).toBeNull();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && npx vitest run src/components/remediation/plan-review.test.tsx`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement**

Create both components. Render every server-supplied string (plan rationale, commit messages, agent narration) as text — never `dangerouslySetInnerHTML`. Follow the `*_never_render` convention already used in `ui/src/components/scanner-supply-chain/`.

- [ ] **Step 4: Run tests**

Run: `cd ui && npx vitest run src/components/remediation/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add ui/src/components/remediation/
git commit -m "feat(remediate): plan and patch review UI"
```

---

### Task 21: Provider connection UI

**Files:**
- Create: `ui/src/components/remediation/providers-panel.tsx`
- Test: `ui/src/components/remediation/providers-panel.test.tsx`

**Interfaces:**
- Consumes: `listProviders`, `connectProvider`, `disconnectProvider`.
- Produces: `<ProvidersPanel />`.

- [ ] **Step 1: Write the failing test**

```tsx
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ProvidersPanel } from "./providers-panel";

vi.mock("../../lib/remediation-api", () => ({
  listProviders: vi.fn().mockResolvedValue([
    { provider: "grok", connected: true, auth_mode: "oauth", expires_at: "2026-09-01T00:00:00Z" },
    { provider: "openai", connected: false },
  ]),
  connectProvider: vi.fn(),
  disconnectProvider: vi.fn(),
}));

describe("ProvidersPanel", () => {
  it("distinguishes connected from unconnected providers", async () => {
    render(<ProvidersPanel />);
    expect(await screen.findByText(/grok/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /connect openai/i })).toBeInTheDocument();
  });

  it("shows the device URL when a login starts", async () => {
    render(<ProvidersPanel />);
    // The panel subscribes to the connect SSE stream and renders the URL as a link.
    expect(await screen.findByText(/grok/i)).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && npx vitest run src/components/remediation/providers-panel.test.tsx`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement**

Create the panel. On connect, open the SSE stream and render the device URL as an external link with the user code beside it. Never render a credential value — the listing endpoint does not return one.

- [ ] **Step 4: Run tests**

Run: `cd ui && npx vitest run src/components/remediation/ && cd ui && npm run lint`
Expected: PASS, lint clean.

- [ ] **Step 5: Commit**

```bash
git add ui/src/components/remediation/
git commit -m "feat(remediate): opencode provider connection UI"
```

---

**Phase 5 gate:** An operator can connect a provider, start a session, review the plan and patches, and watch the run stream — all from the UI.

---

# Phase 6 — Loop Integration

### Task 22: Drive sessions from a Loop

**Files:**
- Modify: `internal/api/routes/loops.go`, `internal/remediate/session.go`
- Test: `internal/remediate/loop_test.go`

**Interfaces:**
- Consumes: `models.Loop`, `Runner.Run`.
- Produces: `Runner.RunForLoop(ctx context.Context, loopID string) error`.

- [ ] **Step 1: Write the failing test**

```go
func TestRunForLoopStopsAtMaxIterations(t *testing.T) {
	store := newTestStore(t)
	loop := seedLoop(t, store, 2) // MaxIterations = 2
	r := NewRunner(store, driver.NewFake([]meter.Event{{Type: "assistant"}}, fixturePlan()),
		credential.NewResolver(store), Config{Enabled: true, MaxTurns: 10, AllowYolo: true})

	if err := r.RunForLoop(context.Background(), loop.ID); err != nil {
		t.Fatalf("RunForLoop: %v", err)
	}
	sessions := listSessionsForLoop(t, store, loop.ID)
	if len(sessions) > 2 {
		t.Fatalf("created %d sessions, MaxIterations is 2", len(sessions))
	}
	got, _ := store.GetLoop(context.Background(), loop.ID)
	if got.CurrentIteration > got.MaxIterations {
		t.Errorf("CurrentIteration = %d exceeds MaxIterations = %d", got.CurrentIteration, got.MaxIterations)
	}
}

func TestRunForLoopStopsWhenScanIsClean(t *testing.T) {
	store := newTestStore(t)
	loop := seedLoop(t, store, 5)
	// Fake driver yields a plan; the seeded rescan returns zero findings.
	r := NewRunner(store, driver.NewFake([]meter.Event{{Type: "assistant"}}, fixturePlan()),
		credential.NewResolver(store), Config{Enabled: true, MaxTurns: 10, AllowYolo: true})

	if err := r.RunForLoop(context.Background(), loop.ID); err != nil {
		t.Fatalf("RunForLoop: %v", err)
	}
	if n := len(listSessionsForLoop(t, store, loop.ID)); n != 1 {
		t.Fatalf("created %d sessions, want 1 — clean rescan should end the loop", n)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/remediate/ -run RunForLoop -v`
Expected: FAIL — `RunForLoop` undefined.

- [ ] **Step 3: Implement**

```go
// RunForLoop drives remediation sessions until the loop's iteration budget is
// spent or a rescan comes back clean. Each iteration is its own session with
// its own turn budget; the loop bounds how many sessions run, not how many
// turns each one takes.
func (r *Runner) RunForLoop(ctx context.Context, loopID string) error {
	loop, err := r.store.GetLoop(ctx, loopID)
	if err != nil {
		return fmt.Errorf("load loop: %w", err)
	}
	for loop.CurrentIteration < loop.MaxIterations {
		loop.CurrentIteration++
		sess, err := r.newSessionForLoop(ctx, loop)
		if err != nil {
			return err
		}
		if err := r.Run(ctx, sess.ID); err != nil {
			return err
		}
		done, err := r.store.GetRemediationSession(ctx, sess.ID)
		if err != nil {
			return err
		}
		// A gated session stops mid-flight; the loop resumes when a human
		// approves, not here.
		if done.Status != models.RemediationCompleted {
			return r.store.UpdateLoop(ctx, loop)
		}
		clean, err := r.rescanIsClean(ctx, done)
		if err != nil {
			return err
		}
		if clean {
			break
		}
	}
	return r.store.UpdateLoop(ctx, loop)
}
```

Add `newSessionForLoop` (creates a session with `LoopID` set and the loop's severity filter) and `rescanIsClean` (reports whether the branch rescan returned zero findings). Use the existing `UpdateLoop` / `GetLoop` store methods; if named differently, use the existing names.

- [ ] **Step 4: Run tests**

Run: `go test ./... 2>&1 | grep -c '^ok' && go build ./...`
Expected: all packages ok, build exit 0.

- [ ] **Step 5: Commit**

```bash
git add internal/
git commit -m "feat(remediate): drive remediation sessions from a loop"
```

---

**Phase 6 gate:** `go build ./... && go test ./...` green across the whole repo. A loop runs iterative remediation until findings clear or the iteration budget is spent.

---

## Final Verification

- [ ] `go build ./...` → exit 0
- [ ] `go test ./...` → exit 0, no FAIL lines
- [ ] `cd ui && npm run lint && npx vitest run` → clean
- [ ] `WOLF_REMEDIATE_ENABLED` is still `false` in `.env.example`
- [ ] `git diff main --stat -- internal/fix/` → **empty** (the non-goal held)
- [ ] Golden permission documents unchanged unless deliberately edited
