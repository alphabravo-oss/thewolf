# OpenCode Agentic Remediation — Design

**Date:** 2026-08-03
**Branch:** `feat/opencode-remediation`
**Status:** Design approved, pending implementation plan

## Problem

Wolf finds security issues but fixes them one at a time. The existing fix
engine (`internal/fix/engine`) exposes a `SubprocessEngine` interface whose
unit of work is a single finding:

```go
Fix(ctx context.Context, req FixRequest) (*FixResult, error)  // req.Finding is one finding
```

Wolf's planner decides the order, then calls the engine once per finding. That
is the right shape for surgical fixes, but it wastes what an agentic coding
tool is actually good at. An agent handed the whole finding set can see that
nine findings share one root cause, that six are test fixtures worth skipping,
and that fixing #3 first makes #11 disappear. Per-finding invocation throws
that context away on every call.

This design adds a second remediation path where the agent owns the session:
Wolf hands OpenCode the entire finding set and a repository, and OpenCode
triages, prioritizes, fixes, and self-verifies across multiple turns. Wolf
retains control of budget, approval gates, and verification by rescan.

## Goals

- Hand a full scan result to an agentic session that triages and fixes across turns.
- Bound every run by a turn budget, metered by Wolf.
- Gate the run at two points — after triage, and before patches land — with each gate independently switchable. Both off is "yolo mode".
- Support per-user LLM credentials via OAuth (subscription plans) and API keys, plus a service identity for scheduled runs.
- Land results as a branch and PR whose body carries the scan delta.
- Leave the existing per-finding engine chain untouched and fully functional.

## Non-Goals

- Replacing or deprecating the `claude-code`, `codex`, `api`, or `custom` engines. They remain the right tool for single-finding fixes and are not modified.
- Cost or token ceilings. Turns are the only meter in v1. OAuth runs bill against subscription plans, not per-token, so a cost ceiling is meaningless for them. The meter is defined as an interface so a token/cost meter can be added later for API-key runs without reworking callers.
- Session-mode driving (`opencode serve` + `--attach`). v1 uses stateless invocations. The `Driver` interface leaves room for it.
- Multi-account credentials per provider. OpenCode's `auth.json` is keyed by provider ID and `set()` overwrites, so one credential per provider per identity is the ceiling regardless of what Wolf does.

## Architecture

### Control flow

```
scan ──► findings.json
           │
           ▼
   RUN 1  opencode run --format json
          permission: { edit: deny, bash: <read-only allowlist> }
          prompt: triage these findings, emit a plan
           │
           ▼  plan persisted
     [GATE 1: plan]      skippable · no live process · survives restart
           │ approved
           ▼
   RUN 2  opencode run --format json --auto
          permission: { edit: allow(<scoped>), <hard deny list> }
          prompt: execute this approved plan
           │
           ▼  commits in worktree @ wolf/remediation-<session>
     [GATE 2: patch]     skippable
           │ approved
           ▼
     rescan branch ──► delta ──► push branch ──► open PR (body = delta)
```

Both runs are separate `opencode run` processes. Nothing is held open across a
gate, so a pending approval is just a database row. A server restart mid-gate
loses no work and orphans no worktree.

The cost of statelessness is that run 2 re-reads context the triage run already
gathered. This is accepted: it buys crash-safety and removes an entire class of
lease/timeout/orphan-recovery bugs, which the scanner release subsystem already
demonstrates is expensive to get right.

### Why a new subsystem, not a new engine

`SubprocessEngine` is per-finding by construction. A session that reasons across
the whole finding set cannot implement it without lying about its contract. So:

- `internal/remediate/` — new, session-owned remediation.
- `internal/fix/` — unchanged, per-finding remediation.

Both are driven from the loop layer and both write to the same worktree and PR
machinery.

### Packages

| Package | Responsibility | Depends on |
|---|---|---|
| `internal/remediate` | Session orchestration: run the two-phase flow, evaluate gates, persist state | driver, plan, gate, meter, credential |
| `internal/remediate/driver` | `Driver` interface + `exec` implementation that shells out to `opencode run` | credential |
| `internal/remediate/plan` | Plan schema, JSON parsing, validation | — |
| `internal/remediate/gate` | Gate policy evaluation (plan gate, patch gate, yolo) | — |
| `internal/remediate/meter` | `Meter` interface + `turns` implementation; consumes the JSON event stream | — |
| `internal/remediate/credential` | Resolve per-user → service credentials, render `OPENCODE_AUTH_CONTENT`, drive the ephemeral login container | `internal/db` |
| `internal/remediate/permission` | Build the per-run `opencode.json` permission document | — |

Each package is independently testable. `driver` is the only one that touches a
subprocess; everything else is pure enough to unit test without containers.

### Reused as-is

- `internal/fix/workspace` — worktree creation and cleanup.
- `internal/fix/pr` — branch push and PR creation.
- `models.Loop` — iteration accounting for multi-round remediation.
- The existing scan path — the rescan is an ordinary scan against the branch.

## Interfaces

```go
// Driver runs the two phases of a remediation session.
type Driver interface {
    Plan(ctx context.Context, req PlanRequest) (*plan.Plan, Usage, error)
    Execute(ctx context.Context, req ExecuteRequest) (*PatchSeries, Usage, error)
}

// Meter decides when a run has spent its budget. The turns implementation
// counts assistant turns in the JSON event stream; a future cost meter can
// sum token usage without changing this interface or its callers.
type Meter interface {
    // Observe consumes one event from the stream and reports whether the
    // budget is now exhausted.
    Observe(event driver.Event) (exhausted bool)
    Usage() Usage
}

type Usage struct {
    Turns int
    // Tokens and Cost are populated only by meters that track them; the
    // turns meter leaves them zero.
    Tokens int64
    Cost   float64
}
```

`Usage` carries fields the v1 meter does not populate. This is deliberate: it
fixes the persistence schema now so adding a cost meter later is not a
migration.

## Permission model

Wolf generates an `opencode.json` per run. OpenCode evaluates rules with
deny-wins semantics and last-match-wins ordering, and `--auto` auto-approves
anything not explicitly denied.

**Run 1 (triage) — read-only:**

```json
{ "permission": {
    "edit": "deny",
    "bash": { "*": "deny", "git log *": "allow", "git diff *": "allow",
              "grep *": "allow", "cat *": "allow", "ls *": "allow" },
    "external_directory": { "*": "deny" } } }
```

**Run 2 (execute) — scoped write:**

```json
{ "permission": {
    "edit": { "*": "allow", ".github/**": "deny", "**/*.pem": "deny",
              "**/*.key": "deny" },
    "bash": { "*": "deny",
              "git add *": "allow", "git commit *": "allow",
              "git checkout *": "allow", "git diff *": "allow",
              "git log *": "allow", "git status *": "allow",
              "go build *": "allow", "go test *": "allow",
              "go vet *": "allow", "gofmt *": "allow",
              "npm test *": "allow", "npm run *": "allow",
              "make *": "allow", "pytest *": "allow",
              "rm -rf *": "deny", "curl *": "deny", "sudo *": "deny" },
    "external_directory": { "*": "deny" } } }
```

The deny entries are the **hard deny list** and are injected in both gated and
yolo mode. Yolo mode disables Wolf's approval gates; it does not disable
OpenCode's permission rules.

**Bash is default-deny, not default-ask.** Under `--auto` an `ask` degrades to
allow, so a bash fallback of `ask` would permit every command nobody thought to
denylist — `nc`, `ssh`, `chmod`, `dd`, `base64`. An allowlist refuses the
unlisted command instead; a blocklist only stops what we remembered to name.
No rule in either document is `ask`.

**The allowlist names subcommands, not binaries.** `git *` would permit
`git push` and `git remote add`; `npm *` would permit `npm install`, which
fetches over the network and runs `postinstall` scripts; `go *` would permit
`go get` and `go run`. Each is a general-purpose egress and
arbitrary-code-execution primitive that would reopen what the deny default
closes. The agent edits, builds, tests, and commits — it never pushes,
installs, or fetches. Wolf pushes the branch from the host via
`pr.PushBranch`, outside the container.

Egress is controlled at two layers — this allowlist and the container's
`--network none` — and neither is sufficient alone. The allowlist cannot stop
a permitted binary from opening a socket; the network policy cannot stop local
arbitrary code execution. Do not remove one because the other appears to cover
it.

`edit` keeps `*: allow` because the agent must be able to modify arbitrary
source files to fix findings — that is the job. The risk there is bounded by
path denies plus `external_directory: deny`, which confines every write to the
worktree.

`external_directory: deny` confines the agent to the worktree.

## Data model

Migration `051_opencode_remediation.sql`. If Postgres and SQLite need divergent
DDL, split into `051_opencode_remediation_postgres.sql` and
`051_opencode_remediation_sqlite.sql`, following the precedent set by migration
030.

**`remediation_sessions`**

| Column | Notes |
|---|---|
| `id` | primary key |
| `user_id` | initiating user; the reserved service identity for scheduled runs |
| `repo_id`, `scan_id` | source scan the findings come from |
| `loop_id` | nullable; set when a `Loop` drives this session |
| `status` | see state machine below |
| `plan_gate_enabled`, `patch_gate_enabled` | booleans; both false = yolo |
| `max_turns` | budget for each run phase |
| `turns_used_plan`, `turns_used_execute` | metered actuals |
| `tokens_used`, `cost_used` | reserved for future meters; zero in v1 |
| `provider`, `model` | e.g. `grok`, `grok-code-fast` |
| `branch_name`, `worktree_path` | populated once run 2 starts |
| `pr_url` | populated after landing |
| `created_at`, `updated_at`, `started_at`, `completed_at` | |

**`remediation_plans`** — `id`, `session_id`, `plan_json`, `created_at`,
`approved_by`, `approved_at`, `rejected_reason`.

**`remediation_patches`** — `id`, `session_id`, `commit_sha`, `files_changed`,
`finding_ids`, `created_at`, `approved_by`, `approved_at`.

**`remediation_events`** — `id`, `session_id`, `seq`, `type`, `payload_json`,
`created_at`. The captured JSON event stream, used for SSE replay and audit.
Payloads are redacted before persistence (see Security).

### State machine

```
pending ──► planning ──► plan_review ──► executing ──► patch_review
                              │                             │
                              │ (gate off: skip)            │ (gate off: skip)
                              └──────────► executing        └──────► applying
                                                                        │
                                                    applying ──► rescanning ──► completed
```

Terminal states: `completed`, `failed`, `cancelled`, `exhausted` (budget spent
before the plan was finished), `rejected` (a human declined at either gate).

## Credentials

### Storage

Reuses the existing per-user `secrets` table (`models.Secret`) rather than
adding a credential store:

- `KeyType` — new constant `KeyTypeOpenCodeAuth`.
- `KeyName` — the OpenCode provider ID (`openai`, `grok`, `anthropic`).
- `EncryptedValue` — the provider's `auth.json` entry, verbatim.
- `MetadataJSON` — `{"auth_mode": "oauth"|"api", "expires_at": ..., "account_id": ...}`, so the UI can show connection state without decrypting.

Scheduled runs have no user. They resolve against a reserved service identity,
introduced as a new constant `models.ServiceUserID`. This identity must be
excluded from per-user secret listing endpoints so its credentials are not
enumerable by ordinary users; only admins may write them.

### Resolution order

1. If the session has a `user_id` that is not the service identity, look up that user's credential for the configured provider.
2. Otherwise, or if the user has none, fall back to the service identity's credential.
3. If neither exists, fail the session before starting a container, with an actionable error naming the provider.

### Injection

`OPENCODE_AUTH_CONTENT` bypasses `auth.json` file storage entirely. Wolf renders
the resolved credential into that env var for the run container. No credential
file is baked into the image and no volume is mounted, so two runs for two users
never share credential state.

**Known limitation — refresh token persistence.** OAuth entries carry
`refresh`/`access`/`expires`. When injected by env var, OpenCode can refresh the
access token in-container but cannot persist it back. If a provider rotates
refresh tokens on use, the next run replays a dead token. v1 mitigates this by
harvesting the container's final credential state on exit and writing it back to
the secret when it differs from what was injected. **Whether this is necessary
per provider is exactly what the throwaway spike must answer** — see Open
Questions.

### OAuth onboarding

Wolf does not implement OAuth. It drives OpenCode's own implementation:

1. User clicks *Connect Grok* in the UI.
2. Wolf starts an ephemeral container running `opencode providers login --provider grok`.
3. Wolf scrapes the device-code URL from container output and streams it to the UI.
4. User completes the flow in their browser.
5. On container exit, Wolf harvests `auth.json`, extracts the provider entry, encrypts it into the user's secrets, and destroys the container.

New providers work the day OpenCode adds them, with no Wolf changes. This
generalizes the existing pattern — the `claude` and `codex` fixer variants are
already labeled `auth-mode="interactive-session"` and rely on a
`docker exec -it` login persisted on a `/home/wolf` volume. The ephemeral
harvest replaces that shared-volume state with per-user database state.

## Container

New fixer variant, following the existing table in `internal/scannerbuild/build.go`:

```go
{Name: "opencode", Dockerfile: "Dockerfile.opencode",
 ImageBase: fixerImageBase, ImageSuffix: "-opencode",
 ContextSubdir: fixerContextSubdir},
```

`fixer/Dockerfile.opencode` follows `Dockerfile.codex`: pinned version and
integrity hash verified at build time, non-root `wolf` user, `/workspace`
workdir. It differs in auth mode — labeled `auth-mode="injected"` rather than
`interactive-session`, because credentials arrive via `OPENCODE_AUTH_CONTENT`
and no session volume is needed.

Adding the variant requires regenerating the embedded build context with
`go generate ./internal/scannerbuild/...`.

## API

| Method | Path | Scope |
|---|---|---|
| `POST` | `/remediations` | `write:fixes` |
| `GET` | `/remediations` | `read:fixes` |
| `GET` | `/remediations/{id}` | `read:fixes` |
| `GET` | `/remediations/{id}/stream` | `read:fixes` — SSE, replays `remediation_events` |
| `GET` | `/remediations/{id}/plan` | `read:fixes` |
| `POST` | `/remediations/{id}/plan/approve` | `write:fixes` |
| `POST` | `/remediations/{id}/plan/reject` | `write:fixes` |
| `GET` | `/remediations/{id}/patches` | `read:fixes` |
| `POST` | `/remediations/{id}/patches/approve` | `write:fixes` |
| `POST` | `/remediations/{id}/patches/reject` | `write:fixes` |
| `DELETE` | `/remediations/{id}` | `write:fixes` — cancel |
| `GET` | `/config/opencode/providers` | `read:config` — connection state |
| `POST` | `/config/opencode/providers/{provider}/connect` | `write:config` — start login container |
| `GET` | `/config/opencode/providers/{provider}/connect/stream` | `write:config` — SSE device-code URL |
| `DELETE` | `/config/opencode/providers/{provider}` | `write:config` — disconnect |

Reuses the existing `read:fixes` / `write:fixes` scopes rather than minting new
ones; remediation is a fix operation. Writing the **service identity** credential
additionally requires `admin`.

## Configuration

```bash
WOLF_REMEDIATE_ENABLED=false          # fail-closed, consistent with release mode
WOLF_REMEDIATE_DEFAULT_PROVIDER=      # opencode provider id
WOLF_REMEDIATE_DEFAULT_MODEL=
WOLF_REMEDIATE_MAX_TURNS=20           # per run phase
WOLF_REMEDIATE_MAX_TURNS_CEILING=100  # admin cap; per-session values clamp to this
WOLF_REMEDIATE_SESSION_TIMEOUT=30m    # wall-clock kill switch per run
WOLF_REMEDIATE_ALLOW_YOLO=false       # admin must opt in before gates can be disabled
```

`WOLF_REMEDIATE_ALLOW_YOLO` exists so ungated autonomous code modification is an
explicit administrative decision, not a per-user checkbox. `SESSION_TIMEOUT` is a
backstop independent of the turn meter: a single turn that hangs on a network
call would otherwise never trip a turn-based budget.

## Error handling

| Failure | Behavior |
|---|---|
| No credential resolves | Fail before starting a container; error names the provider and the missing identity. |
| Turn budget exhausted mid-plan | Terminate the run, persist partial output, mark `exhausted`. No patches applied. |
| Wall-clock timeout | SIGTERM the container, then SIGKILL after grace. Mark `failed`. Worktree cleaned. |
| Malformed plan JSON | Retry the plan run once with a repair prompt. Second failure marks `failed`. |
| Patch does not apply | Mark `failed`, retain the worktree for inspection, do not push. |
| Rescan finds *more* findings than baseline | Complete the session but flag the regression in the PR body and set a `regressed` marker. Never auto-merge. |
| Container OOM / non-zero exit | Mark `failed`, persist captured stderr, clean the worktree. |
| Server restart mid-run | The run process is gone. On startup, sessions in `planning` or `executing` are marked `failed` with an orphan reason. Sessions in `plan_review` or `patch_review` are untouched — they hold no process. |

The orphan recovery mirrors `recoverOrphanScans` in `internal/api/server.go`.

## Testing

- **Unit** — `plan` parsing and validation, `gate` policy evaluation across the yolo/gated matrix, `meter` turn counting against recorded event-stream fixtures, `permission` document generation, `credential` resolution order including the service fallback.
- **Golden files** — the generated `opencode.json` for both run phases, asserted byte-for-byte so a permission regression cannot land silently. This is the highest-value test in the suite: it is the only thing standing between a config typo and an agent with unrestricted `bash`.
- **Fake driver** — a `Driver` implementation replaying recorded event streams, so session orchestration, gates, state transitions, and error paths are tested without containers or network.
- **Redaction** — assert that credentials injected via `OPENCODE_AUTH_CONTENT` never appear in `remediation_events`, SSE output, or the PR body. Follows the existing `*_never_render` fixture convention in the UI tests.
- **Integration (opt-in, tagged)** — one real `opencode run` against a fixture repo with a seeded finding, gated behind a build tag and a credential env var so CI without credentials skips it.

## Security

- The agent runs non-root in an ephemeral worktree with `external_directory: deny`.
- Credentials reach the container only via `OPENCODE_AUTH_CONTENT`, never on the command line (visible in `ps`) and never written to an image layer.
- `remediation_events` payloads are redacted before persistence. The agent can read repository contents including secrets it finds, and its narration lands in the event stream.
- The remediation branch is never merged automatically. The PR is the human checkpoint that survives even yolo mode.
- `.github/**` is in the hard deny list: an agent that can edit CI can exfiltrate anything on its next run.
- Every gate approval records `approved_by` and flows through the existing classified audit middleware.

## Phasing

Each phase is independently shippable and leaves the tree green.

**Phase 1 — the loop, end to end.** Single provider, API-key auth only, both
gates off, artifact output only. Proves scan → plan run → execute run → rescan
→ delta. Includes the container variant, the `Driver` and `Meter` interfaces,
turn metering, and the fake-driver test harness. Artifact-only output is
scaffolding for this phase, not a supported landing mode — phase 3 replaces it
with the branch-and-PR flow described in Architecture, and `WOLF_REMEDIATE_ENABLED`
stays false by default until then.

**Phase 2 — gates.** Plan gate and patch gate, the `plan_review`/`patch_review`
states, approval endpoints, and `WOLF_REMEDIATE_ALLOW_YOLO`.

**Phase 3 — landing.** Branch push, PR creation with the delta table, regression
flagging. Wires in `internal/fix/pr`.

**Phase 4 — credentials.** Per-user secrets, service identity, resolution order,
and the ephemeral login container with device-code proxying.

**Phase 5 — UI.** Remediation panel, plan review, patch review, provider
connection management.

**Phase 6 — loop integration.** Drive sessions from `models.Loop` for
multi-iteration remediation.

API-key auth carries phases 1–3, so the OAuth work in phase 4 lands against a
system already proven end to end.

## Open questions — deferred to a throwaway spike

These are answered on a **separate throwaway branch**, not this one. Findings
fold back into this spec before phase 4 is planned.

1. Does the device-code URL reliably appear on `opencode providers login` stdout in a non-TTY container, or does it require a PTY?
2. Can `auth.json` be harvested cleanly on container exit, and what is the exit behavior if the user abandons the login?
3. Do Grok and OpenAI rotate refresh tokens on use? This determines whether the write-back path is mandatory or belt-and-braces.
4. What does the `--format json` event stream actually emit per turn, and what is the correct signal to count as "a turn"?

Question 4 gates phase 1 (the meter cannot be written without it), so the spike
should answer it first even though the rest concerns phase 4.

If question 1 or 2 fails, the fallback is the paste-the-blob onboarding flow;
only the `credential` package's acquisition path changes, and storage,
injection, and resolution are unaffected.

## Risks

| Risk | Mitigation |
|---|---|
| Agent makes plausible but wrong fixes | Rescan verifies; PR is mandatory; regression flagging catches net-negative runs. |
| Runaway spend on API-key auth | Turn budget plus wall-clock timeout in v1; the `Meter` interface admits a cost meter without refactoring. |
| Prompt injection via repository content or finding text | Hard deny list is not agent-modifiable; `external_directory: deny`; no network egress tools allowed (`curl`/`sudo` denied). |
| Turn-counting drifts from OpenCode's event schema | Meter tests run against recorded fixtures; a schema change fails tests rather than silently uncapping the budget. |
| Scope creep into replacing the per-finding engines | Explicit non-goal; `internal/fix/` is not modified by any phase. |
