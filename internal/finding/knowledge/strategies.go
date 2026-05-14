package knowledge

// This file registers the initial fix-strategy templates. Each strategy is
// a markdown block surfaced once per category in FIX-*.md docs.

func init() {
	for _, s := range builtinStrategies() {
		RegisterStrategy(s)
	}
}

func builtinStrategies() []Strategy {
	return []Strategy{
		{
			ID:         "parameterize-query",
			Title:      "Use parameterized queries",
			AppliesTo:  []string{"sql-injection"},
			Body:       sParameterizeQuery,
			References: []string{"CWE-89", "OWASP A03:2021"},
		},
		{
			ID:         "escape-html-output",
			Title:      "Escape values rendered into HTML",
			AppliesTo:  []string{"xss"},
			Body:       sEscapeHTMLOutput,
			References: []string{"CWE-79", "OWASP A03:2021"},
		},
		{
			ID:         "replace-weak-hash",
			Title:      "Replace MD5/SHA1 with a modern hash",
			AppliesTo:  []string{"weak-crypto"},
			Body:       sReplaceWeakHash,
			References: []string{"CWE-327"},
		},
		{
			ID:         "use-crypto-rand",
			Title:      "Use a cryptographically secure RNG",
			AppliesTo:  []string{"insecure-randomness"},
			Body:       sUseCryptoRand,
			References: []string{"CWE-338"},
		},
		{
			ID:         "rotate-and-remove-secret",
			Title:      "Rotate the secret and remove from version control",
			AppliesTo:  []string{"hardcoded-secret"},
			Body:       sRotateAndRemoveSecret,
			References: []string{"CWE-798"},
		},
		{
			ID:         "use-arg-list-no-shell",
			Title:      "Pass arguments as a list; avoid shell interpolation",
			AppliesTo:  []string{"command-injection"},
			Body:       sUseArgListNoShell,
			References: []string{"CWE-78"},
		},
		{
			ID:         "validate-redirect-target",
			Title:      "Validate redirect destinations against an allowlist",
			AppliesTo:  []string{"open-redirect"},
			Body:       sValidateRedirectTarget,
			References: []string{"CWE-601"},
		},
		{
			ID:         "set-secure-cookie-flags",
			Title:      "Set Secure, HttpOnly, and SameSite on cookies",
			AppliesTo:  []string{"insecure-cookie"},
			Body:       sSetSecureCookieFlags,
			References: []string{"CWE-614", "CWE-1004"},
		},
		{
			ID:         "use-safer-yaml-load",
			Title:      "Use safe YAML deserialization",
			AppliesTo:  []string{"unsafe-deserialization"},
			Body:       sUseSaferYAMLLoad,
			References: []string{"CWE-502"},
		},
		{
			ID:         "verify-jwt-signature",
			Title:      "Always verify JWT signatures",
			AppliesTo:  []string{"jwt-unverified"},
			Body:       sVerifyJWTSignature,
			References: []string{"CWE-347"},
		},
		{
			ID:         "non-root-container-user",
			Title:      "Run containers as a non-root user",
			AppliesTo:  []string{"container-misconfig"},
			Body:       sNonRootContainerUser,
			References: []string{"CWE-250"},
		},
		{
			ID:        "pin-base-image-digest",
			Title:     "Pin base images by digest, not tag",
			AppliesTo: []string{"container-misconfig"},
			Body:      sPinBaseImageDigest,
		},
		{
			ID:        "update-vulnerable-dependency",
			Title:     "Update the affected dependency",
			AppliesTo: []string{"vulnerable-dependency"},
			Body:      sUpdateVulnerableDependency,
		},
		{
			ID:         "remove-debug-endpoints",
			Title:      "Remove or gate debug endpoints in production",
			AppliesTo:  []string{"debug-exposure"},
			Body:       sRemoveDebugEndpoints,
			References: []string{"CWE-489"},
		},
		{
			ID:         "use-https-and-tls12-plus",
			Title:      "Require HTTPS with TLS 1.2 or higher",
			AppliesTo:  []string{"insecure-transport"},
			Body:       sUseHTTPSAndTLS12Plus,
			References: []string{"CWE-319"},
		},
		{
			ID:         "validate-input",
			Title:      "Validate inputs at the trust boundary",
			AppliesTo:  []string{"input-validation", "path-traversal"},
			Body:       sValidateInput,
			References: []string{"CWE-20", "CWE-22"},
		},
		{
			ID:        "use-context-with-timeout",
			Title:     "Bound long-running operations with a context timeout",
			AppliesTo: []string{"resource-exhaustion"},
			Body:      sUseContextWithTimeout,
		},
		{
			ID:        "restrict-cors-origins",
			Title:     "Restrict CORS to a known allowlist of origins",
			AppliesTo: []string{"cors-misconfig"},
			Body:      sRestrictCORSOrigins,
		},
		{
			ID:        "tool-docs-fallback",
			Title:     "See the scanner's documentation",
			AppliesTo: []string{"uncategorized"},
			Body:      sToolDocsFallback,
		},
	}
}

// Bodies are kept as package-level constants so the registration table stays
// scannable.

const sParameterizeQuery = "Replace string-concatenated SQL with placeholders bound at execution time.\n\n" +
	"**Before:**\n```go\ndb.Query(\"SELECT * FROM users WHERE id = \" + userID)\n```\n\n" +
	"**After:**\n```go\ndb.Query(\"SELECT * FROM users WHERE id = ?\", userID)\n```"

const sEscapeHTMLOutput = "User-controlled values rendered into HTML must be HTML-encoded. Prefer the\n" +
	"framework's templating system (it auto-encodes) over raw DOM injection APIs.\n" +
	"In React, JSX text nodes are safe. In vanilla DOM code, prefer `textContent`\n" +
	"for plain text or a vetted sanitizer when HTML is genuinely required."

const sReplaceWeakHash = "MD5 and SHA1 are no longer collision-resistant. Use SHA-256 (or better SHA-3,\n" +
	"BLAKE2) for integrity hashes. For password hashing, use bcrypt/scrypt/argon2.\n\n" +
	"**Before:**\n```go\nh := md5.Sum(data)\n```\n\n" +
	"**After:**\n```go\nh := sha256.Sum256(data)\n```"

const sUseCryptoRand = "`math/rand` (Go) and `Math.random()` (JS) are predictable. For tokens, IDs,\n" +
	"passwords, and secrets use a CSPRNG: `crypto/rand` in Go, `crypto.randomBytes`\n" +
	"in Node, `secrets` in Python."

const sRotateAndRemoveSecret = "1. Treat any committed secret as compromised. Rotate it at the issuing\n" +
	"   service immediately.\n" +
	"2. Remove the secret from the codebase. If it's been pushed, also rewrite\n" +
	"   git history (`git filter-repo` or BFG) — deleting in a new commit is\n" +
	"   not enough.\n" +
	"3. Move the value to an environment variable, secret manager, or vault.\n" +
	"4. Add the pattern to `.gitleaksignore` only if the finding is a known\n" +
	"   safe value (test fixture)."

const sUseArgListNoShell = "Invoking a shell with user input is the canonical command-injection vector.\n" +
	"Pass arguments as a list to `exec`/`spawn`/`subprocess` and skip the shell.\n\n" +
	"**Before:**\n```python\nsubprocess.call(\"convert \" + filename, shell=True)\n```\n\n" +
	"**After:**\n```python\nsubprocess.call([\"convert\", filename])\n```"

const sValidateRedirectTarget = "User-controlled redirects let attackers phish via your domain. Restrict\n" +
	"the redirect target to an allowlist of paths or hosts you own, and reject\n" +
	"everything else."

const sSetSecureCookieFlags = "Session and auth cookies must set:\n\n" +
	"- `Secure` — only sent over HTTPS\n" +
	"- `HttpOnly` — not readable from JavaScript\n" +
	"- `SameSite=Lax` (or `Strict`) — CSRF mitigation"

const sUseSaferYAMLLoad = "Default YAML loaders in many languages can instantiate arbitrary classes,\n" +
	"leading to RCE. Use `yaml.safe_load` (Python) or equivalent.\n\n" +
	"**Before:**\n```python\nyaml.load(data)\n```\n\n" +
	"**After:**\n```python\nyaml.safe_load(data)\n```"

const sVerifyJWTSignature = "Reject the `none` algorithm. Pin the expected algorithm explicitly when\n" +
	"verifying, otherwise a forged token with `alg: none` will be accepted.\n" +
	"Validate `exp`, `iss`, and `aud` claims too."

const sNonRootContainerUser = "Add `USER` to your Dockerfile (any non-zero UID) so a process breakout\n" +
	"doesn't immediately give root on the host namespace.\n\n" +
	"```dockerfile\nRUN adduser -D app\nUSER app\n```"

const sPinBaseImageDigest = "Tags are mutable. Pin to an immutable digest so rebuilds are reproducible\n" +
	"and a compromised tag can't backdoor your image.\n\n" +
	"```dockerfile\nFROM alpine@sha256:abcd...\n```"

const sUpdateVulnerableDependency = "Upgrade to a version that includes the fix. If no fixed version exists,\n" +
	"check whether the vulnerable code path is reachable from your code and,\n" +
	"if not, add a documented suppression. Subscribe to the upstream advisory\n" +
	"so you re-evaluate when a fix lands."

const sRemoveDebugEndpoints = "pprof, /debug, /admin without auth, verbose error pages — these leak\n" +
	"internals and grant attackers footholds. Gate them behind auth and\n" +
	"environment checks, or compile them out of production builds."

const sUseHTTPSAndTLS12Plus = "Disable plain HTTP for any endpoint that handles auth or PII. Reject\n" +
	"TLS 1.0/1.1 (now deprecated). Use HSTS to prevent protocol downgrade."

const sValidateInput = "Validate type, length, format, and range at the point a value crosses\n" +
	"into your trust zone (HTTP handler, queue consumer, etc.). For file\n" +
	"paths, reject `..` and absolute paths; canonicalize before comparison."

const sUseContextWithTimeout = "External calls without a deadline can pile up under partial failures and\n" +
	"exhaust connection pools or worker goroutines. Always pass a `context.\n" +
	"Context` with a sensible timeout to outbound HTTP / DB / RPC calls."

const sRestrictCORSOrigins = "`Access-Control-Allow-Origin: *` combined with credentials is a footgun.\n" +
	"Validate the request's `Origin` against an allowlist and reflect only\n" +
	"that exact origin."

const sToolDocsFallback = "This finding isn't in the knowledge base yet. Open the tool's own\n" +
	"documentation for guidance, or file an issue against the wolf knowledge\n" +
	"base so this rule gets a curated entry."
