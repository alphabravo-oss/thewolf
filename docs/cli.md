# CLI surfaces

The `wolf` binary is the same product as the API. Exit codes: `0` ok, `1` runtime, `2` usage, `3` not found, `4` auth, `5` quality-gate fail.

| Command | Purpose |
|---|---|
| `wolf serve` | API + UI |
| `wolf init` | First-run local layout |
| `wolf backup -o file.tar` / `wolf restore` | Control-plane backup |
| `wolf scan create --profile fast` | Start a scan |
| `wolf scan gate --fail-exit-code` | CI gate (exit 5) |
| `wolf system edition` / `coverage` / `license` | Commercial and scanner matrix |
| `wolf mcp` | MCP stdio; server needs `WOLF_MCP_ENABLED=1` |
| `wolf-enterprise serve` | Overlay composition root (private tree) |

See `docs/interface-parity.md` and `docs/disconnected.md`.
