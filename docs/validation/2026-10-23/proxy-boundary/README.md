# D07.1 — one explicit proxy trust boundary

2026-10-23. Production-router reproduction showed six rapid login requests from one socket peer bypassing the five-request authentication burst by changing forwarding headers. The router's unconditional Chi `RealIP` middleware overwrote the peer before Wolf's rate limiter inspected it. Separately, audit events read the first `X-Forwarded-For` entry without verifying the proxy, while cookies and SSO callback URLs accepted any `X-Forwarded-Proto` assertion.

The API now parses `WOLF_TRUSTED_PROXIES` once at construction. It accepts literal addresses or CIDRs, defaults to no proxy trust (including loopback), and rejects the entire configuration on an invalid entry with a warning. Forwarded client addresses are selected right to left through explicitly trusted hops, stopping at the nearest untrusted address. Multiple XFF header values form one chain. Invalid required hops fall back to the socket peer. `X-Real-IP` and `Forwarded` are ignored. Ambiguous or duplicate protocol values do not assert HTTPS. Actual TLS remains authoritative.

The resolved address/protocol are server-owned request context. Rate limiting and both audit paths share `ClientIP`; cookie and SSO scheme decisions share `IsHTTPS`. `RemoteAddr` remains the socket peer for request logs. Internally dispatched MCP requests preserve this validated context instead of treating copied headers as a new transport assertion. `TestForwardedContextSurvivesInternalDispatch` exercises the same context/transport-preserving re-entry used by `mcpAPI`.

`TestForwardedHeadersCannotBypassAuthenticationRateLimit` reproduces the real router failure. `TestTrustedProxyRateLimitUsesNearestUntrustedHop` verifies an attacker-controlled prefix cannot rotate the bucket behind a trusted proxy and that a distinct client has its own bucket. `TestSessionCookieUsesTrustedTransport` verifies secure-cookie decisions and recorded authentication audit addresses for default/loopback/trusted/untrusted/invalid configurations. `TestForwardedHeaderTrustBoundary` covers eighteen address/protocol/configuration cases, direct TLS without middleware, duplicate header values, IPv6 and mapped IPv4.

The endpoint sweep's existing `reqFrom` helper previously supplied only a forged XFF value. It now sets the simulated socket `RemoteAddr` using `net.JoinHostPort`; production trust is not weakened to accommodate test fixtures. Compose passes the explicit variable, `.env.example` defaults it empty, and deployment/authentication guidance documents configuration and the upgrade requirement. `compose.json` records configuration rendering only, not a deployed Caddy or ingress qualification.

```sh
go test -race ./internal/api ./internal/api/middleware ./internal/api/routes -run 'Test.*(Forwarded|TrustedProxy|SessionCookie|RateLimiter|SSO|MCP)' -count=1
make scanners-sync
WOLF_TEST_POSTGRES_DSN='<owned fixture in postgres.json>' make test
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run
/tmp/wolf-plan-gosec-bin -fmt json -out docs/validation/2026-10-23/proxy-boundary/gosec.json -severity high -confidence high -exclude-dir=internal/scannerbuild/context ./cmd/... ./internal/... ./pkg/... ./plugins/...
```

Existing public-proxy installations must set actual immediate proxy addresses/CIDRs and restart the API. An empty, stale or invalid allowlist fails closed: rate limits use the proxy's shared bucket and forwarded HTTPS is not accepted. The final trusted proxy must append/replace the actual client address and overwrite the protocol header. Dedicated/stable proxy addressing and ingress deployment remain operator/rollout requirements; no broad network is implicitly trusted.

D07 remains open for login failure accounting and persistence, MFA/recovery concurrency and replay, bootstrap validation/failure handling, no re-promotion and break-glass qualification. The audit found process-local login accounting and MFA recovery consumption implemented as separate read/update operations; those require further checks and repair. This proxy change does not qualify distributed rate limiting or the entire authentication system. Nothing was deployed.

Evidence sequencing: `reproduction.txt` is the original router-level bypass;
`contracts.txt`, `contracts-qualified.txt` and `contracts-final.txt` record the
initial repairs and expanded checks. `race.txt` passed the first full candidate.
Subsequent caller review identified the internal-dispatch regression in that
candidate, reproduced in `internal-dispatch-reproduction.txt`; it is not claimed
as a defect in the original pre-proxy code. `contracts-verified.txt`,
`sync-verified.txt`, `lint-final.txt` and `race-final.txt` apply to the final
context-preserving implementation, including the actual MCP client regressions.

Final verification: `race-final.txt` passes all **136 packages** uncached under
`-race`, including SQLite/PostgreSQL and the accumulated D06 matrix. Final lint
reports **0 issues**; scanner sync validates 53 tools, 25 upstream images and 51
version pins. Gosec remains release-blocking with **29 high/high findings**
(25 G703, one G702, three G704), no analysis errors or waivers. The temporary
PostgreSQL fixture and anonymous volume were removed and their absence verified.
`checks.json`, `source.json` and `sha256.json` identify outcomes and working-tree
inputs; this is not an immutable release bundle or a deployment receipt.
