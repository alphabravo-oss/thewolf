# Community interface parity

Community actions must be available through UI, REST, CLI, webhook, pipeline,
and MCP (when enabled). MCP is default **off**.

| Action | UI | REST | CLI | Webhook | Pipeline | MCP |
|---|---|---|---|---|---|---|
| Start scan | yes | `POST /scans` | `wolf scan create` | GitHub inbound | Actions snippet | no |
| List scans | yes | `GET /scans` | `wolf scan list` | — | — | `wolf.scans.list` |
| Gate | yes | `GET /scans/{id}/gate` | `wolf scan gate --fail-exit-code` | outbound `scan.completed` | exit 5 | no |
| List findings | yes | `GET /findings` | `wolf finding list` | — | — | `wolf.findings.list` |
| List vulnerabilities | yes | `GET /vulnerabilities` | `wolf vulnerability list` | — | — | `wolf.vulnerabilities.list` |
| List repos | yes | `GET /repos` | `wolf repo list` | — | — | `wolf.repos.list` |
| Retry scan | yes | `POST /scans/{id}/retry` | `wolf scan retry` | — | — | no |
| Suppress | yes | `POST /suppressions` | `wolf suppress` | — | — | no |
| Edition status | Settings | `GET /edition` | `wolf system edition` | — | — | `wolf.edition.get` |
| Coverage | `/coverage` | `GET /coverage` | `wolf system coverage` | — | — | `wolf.coverage.get` |
| License | Settings | `GET /license` | `wolf system license` | — | — | `wolf.license.get` |
| Evidence | Vulnerability page | `GET /evidence` | `wolf system evidence` / `wolf vulnerability evidence` | — | — | no |
| MCP | — | `POST /mcp` | `wolf mcp` (stdio) | — | — | session |
| Webhook events | Settings | `GET /webhooks/events` | `wolf system webhook-events` | outbound | — | no |
| Scan profiles | Start dialog | `GET /scan-profiles` | `wolf system scan-profiles` | — | `--profile` | `wolf.scan-profiles.get` |

MCP JSON-RPC is `POST /mcp` behind session auth. Set `WOLF_MCP_ENABLED=1`.
It calls the same store helpers as REST after `auth.Middleware`; it does not
open a second database path.
