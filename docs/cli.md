# CLI surfaces

The `wolf` binary is the same product as the API. Exit codes: `0` ok, `1` runtime, `2` usage, `3` not found, `4` auth, `5` quality-gate fail.

| Command | Purpose |
|---|---|
| `wolf serve` | API + UI |
| `wolf init` | First-run local layout |
| `wolf backup -o file.tar` / `wolf restore` | Control-plane backup (SQLite) |
| `wolf db migrate-from-sqlite --sqlite wolf.db --postgres DSN` | One-time copy of a SQLite install into PostgreSQL ([deployment](deployment.md#move-from-sqlite-to-postgresql)) |
| `wolf scan create --profile fast` | Start a scan |
| `wolf scan gate <scan-id> --fail-exit-code` | CI gate (exit 5) |
| `wolf system edition` / `coverage` / `license` | Commercial and scanner matrix |
| `wolf scan import-coverage <id>` | Import measured test reports for the exact tested commit; nine formats |
| `wolf scan test-coverage <id>` / `coverage-files <id>` | Measured totals, policy checks, and source-file coverage |
| `wolf mcp` | MCP stdio; server needs `WOLF_MCP_ENABLED=1` |
| `wolf-enterprise serve` | Overlay composition root (private tree) |

See `docs/interface-parity.md` and `docs/disconnected.md`.

## Automation sequence

```sh
wolf scan create --request-file scan.json --idempotency-key build-123 --output json
wolf scan wait <scan-id> --timeout 30m --interval 2s
wolf scan gate <scan-id> --fail-exit-code
```

The request file accepts the complete REST scan body in JSON or YAML, including
source selection, commit, paths, categories and client reference. Explicit CLI
flags override file values. Preserve the returned scan ID and reuse the same
idempotency key only for the same request. `scan wait` succeeds only for a
completed execution; failed/cancelled/unknown states and timeouts fail. A
completed execution still requires a gate check for tool coverage and findings.

The repository's GitHub Actions helper uses curl and jq, selects the PR head
commit, captures the scan ID, waits with a deadline, verifies the returned commit,
and accepts only pass/warn. It reads authentication from Actions secrets through
environment variables. No separate Wolf CLI installation is required.

`wolf auth refresh` renews human login credentials; use scoped API keys for CI.
`wolf admin database get`, `test --file database.json`, and `save --file database.json`
manage the administrator database configuration without DSNs in command arguments.

Local `wolf scan` defaults `--concurrency`, `--heavy-concurrency`, and
`--network-concurrency` to `0` (automatic Docker-host sizing). Positive values
set per-scan ceilings; CPU and memory admission still applies. The runner logs
the detected capacity, reserved budget and effective limits before execution.

Local `wolf scan --repo <path>` exits `1` when any tool fails, a requested tool
does not run, no tools run, parsing is incomplete, execution is cancelled, or
the canonical report cannot be written. Partial findings and available artifacts
are retained. Findings alone do not fail execution: a successful scan can find
vulnerabilities. Use the server quality-gate command above for policy verdicts.

`wolf settings scheduling` shows the server’s current detected scheduling plan.
