# OpenCode Spike — Findings

**Date:** 2026-08-03
**CLI tested:** `opencode-ai@1.18.11` (installed to a scratch prefix; the machine's global install was 1.15.7 and was not touched)
**Model:** `opencode/deepseek-v4-flash-free` (free tier — no paid quota consumed)
**Method:** real `opencode run` invocations against a throwaway git fixture, with `--print-logs --log-level DEBUG`

Answers the open questions in
`docs/superpowers/specs/2026-08-03-opencode-agentic-remediation-design.md`.
Every finding below is empirical — a command was run and its output observed.
Nothing here is inferred from documentation.

## Summary — four corrections to the plan, two of them load-bearing

| # | Finding | Impact |
|---|---|---|
| 1 | Turn signal is `step_finish`, not `assistant` | Meter would have counted **zero turns** — unbounded agent |
| 2 | `OPENCODE_CONFIG`, not `OPENCODE_CONFIG_DIR` | Permission document would **never have loaded** |
| 3 | `opencode run` hangs unless stdin is closed | Every container run would **hang forever** |
| 4 | A repo-supplied `opencode.json` **overrides** ours | Untrusted repo can **disable every control** |

Two confirmations: the root `"*": "deny"` wildcard works, and the 1.18.11 pin is correct.

---

## Q4 — Event stream and the turn signal

**ANSWERED. The turn boundary is `step_finish`.**

`opencode run --format json` emits newline-delimited JSON, one object per line:

```json
{"type":"step_start","timestamp":1785789873976,"sessionID":"ses_…","part":{…}}
{"type":"text","timestamp":1785789879956,"sessionID":"ses_…","part":{"text":"OK",…}}
{"type":"step_finish","timestamp":1785789879999,"sessionID":"ses_…","part":{
   "reason":"stop",
   "tokens":{"total":34116,"input":34113,"output":3,"reasoning":0,
             "cache":{"write":0,"read":0}},
   "cost":0}}
```

Observed event types: `step_start`, `text`, `tool_use`, `step_finish`.

A two-turn session emits `step_start, tool_use, step_finish, step_start, text,
step_finish` — so **counting `step_finish` counts turns**. The debug log
corroborates: `message=loop session.id=… step=1`.

**The plan's assumed `{"type":"assistant"}` does not exist.** A meter written
against it would count zero turns forever, and since `NewTurns` treats a
budget that is never reached as "keep going", the agent would have been
effectively unbounded. This is the single most important finding.

**Bonus — cost metering is free.** `step_finish.part` already carries `tokens`
and `cost`. The spec deferred cost metering to a future `Meter` implementation
on the theory that OAuth plan usage has no per-token cost; that reasoning still
holds for billing, but the *data* is present per step at no extra work. `Usage`
can populate `Turns`, `Tokens`, and `Cost` from day one. The `Meter` interface
does not need to change — the turns meter simply fills all three fields.

### Required `meter.Event` shape

```go
type Event struct {
    Type string `json:"type"`
    Part struct {
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

const turnSignal = "step_finish"
```

## Q5 — Does a root `"*": "deny"` actually deny?

**ANSWERED: YES. Confirmed by A/B test.**

The JSON schema at `https://opencode.ai/config.json` declares `PermissionConfig`
as `anyOf[ PermissionActionConfig, object ]`, where the object form names exactly
fifteen keys — `read, edit, glob, grep, list, bash, task, external_directory,
todowrite, question, webfetch, websearch, lsp, doom_loop, skill` — and **no `"*"`
key**. A `"*"` entry lands in `additionalProperties`, so the schema alone does
not prove it behaves as a wildcard.

It does. Same prompt ("Use the bash tool to run: ls -1"), same model, same
fixture:

| Config | Tool the agent used |
|---|---|
| none | `bash`, completed |
| `{"*":"deny","read":"allow","glob":"allow","grep":"allow","list":"allow"}` | `read` — bash never invoked |

The agent was explicitly told to use bash and fell back to an allowed tool.
**The root-deny design in the permission package is validated.**

Note the fifteen-key list matches what the whole-branch review reported, and
confirms `webfetch`/`websearch` are real keys that default to allow when unset.

## Q6 — Which environment variable loads a config file?

**ANSWERED: `OPENCODE_CONFIG`. The plan's `OPENCODE_CONFIG_DIR` is wrong.**

Running with `OPENCODE_CONFIG=<path>/opencode.json` produced four debug-log
lines referencing that exact path, and the config took effect (Q5's A/B above
depends on it). `OPENCODE_CONFIG_DIR` points at the directory holding agents,
commands, and plugins — not the config file.

The plan's Task 4 `buildInvocation` sets `OPENCODE_CONFIG_DIR=/config`. As
written, **the golden-tested permission document would never be loaded**, and
every run would use default permissions. The container must instead set:

```
-e OPENCODE_CONFIG=/config/opencode.json
```

The default config search path, observed in the logs, is:
`~/.config/opencode/{config,opencode}.json[c]`, then
`~/.opencode/opencode.json[c]`.

## Q7 — Can the scanned repository override Wolf's permissions?

**ANSWERED: YES. This is a security hole and needs fixing before any driver work.**

With a permissive `opencode.json` committed in the fixture repo AND a
restrictive document supplied via `OPENCODE_CONFIG`, the agent used **`bash`** —
the repo's config won.

The scanned repository is untrusted input by definition. As designed, any
repository can neutralize every permission control by shipping its own
`opencode.json`. The existing mitigation (denying the agent permission to
*write* `opencode.json`) does not help: a pre-existing file already wins.

**Required mitigation.** Wolf creates the worktree, so it can strip
repo-level configuration before invoking the agent: delete `opencode.json`,
`opencode.jsonc`, and `.opencode/` from the worktree after checkout and before
the run. Removal must happen in the worktree only — never in the user's actual
repository — and should be recorded in the session's event log so an operator
can see it happened.

This is cheap and fully within Wolf's control. It should be a task in Phase 1,
not deferred.

## NEW — `opencode run` hangs unless stdin is closed

**Not previously a question; found while debugging. Load-bearing.**

`opencode run --format json …` with an inherited stdin **hangs indefinitely** —
observed three times, at 120s, 150s, and 240s timeouts, producing zero bytes of
stdout and no error. Adding `< /dev/null` makes the identical command exit 0
and emit its events immediately.

The driver must close or redirect the child's stdin. In Go:

```go
cmd.Stdin = nil // exec.Cmd with a nil Stdin reads from /dev/null
```

Without this, every containerized run hangs until the wall-clock timeout kills
it, and the turn budget never gets a chance to apply. The spec's
`WOLF_REMEDIATE_SESSION_TIMEOUT` would have masked this as a mysterious
"every session times out" symptom.

## Version confirmation

`--auto` exists in **1.18.11** ("auto-approve permissions that are not
explicitly denied (dangerous!)") and **does not exist in 1.15.7**. Task 7's pin
of 1.18.11 is correct and required — the design does not work on the older CLI.

The machine's global install is 1.15.7. Testing was done against 1.18.11
installed to a scratch prefix; the global install was left untouched.

## Not tested

- OAuth device-code harvesting in a non-TTY container (spec questions 1–2). The machine already holds authenticated credentials for five providers — Anthropic (oauth), OpenAI (oauth), xAI (oauth), and two Z.AI api-key entries — which confirms OAuth-on-plans is real, but the *ephemeral container harvest* flow was not exercised.
- Refresh-token rotation (spec question 3).
- OpenCode's "managed config" tier as a non-overridable home for the deny list. Worth investigating as a cleaner fix for Q7 than stripping files, but stripping is sufficient and simpler.
