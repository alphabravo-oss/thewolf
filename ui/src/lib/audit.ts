// Plain-language labels for audit-log rows. The API records the request
// (method, path) plus a server-classified `event_type` ("scans.created",
// "auth.login", ...); this turns those into "Started a scan". It never invents
// names: the log carries ids, not repository or user names.
export type AuditLike = { event_type?: string; action?: string; method: string; path: string; status_code: number };

/** Server-classified events (internal/api/middleware/audit_classify.go and auth routes). */
const EVENTS: Record<string, string> = {
  "auth.login": "Signed in",
  "auth.login.failed": "Failed sign-in attempt",
  "auth.sso.login": "Signed in with SSO",
  "auth.logout": "Signed out",
  "auth.refresh_replay": "Blocked a reused session token",
  "auth.mfa.enrolled": "Started two-factor setup",
  "auth.mfa.enabled": "Turned on two-factor sign-in",
  "auth.mfa.disabled": "Turned off two-factor sign-in",
  "auth.mfa.reset": "Reset a user's two-factor sign-in",
  "auth.password.changed": "Changed a password",
  "account.profile.updated": "Updated a profile",
  "user.created": "Added a user",
  "user.role.changed": "Changed a user's role",
  "user.deleted": "Deleted a user",
  "apikey.created": "Created an API key",
  "apikey.revoked": "Revoked an API key",
  "secret.created": "Added a secret",
  "secret.deleted": "Deleted a secret",
  "settings.updated": "Changed settings",
  "license.validated": "Checked a license",
  "license.install": "Installed a license",
  "plugin.installed": "Installed a plugin",
  "scan.release_rescan.created": "Queued a rescan for a scanner release",
};

/** Request shapes whose generic "<verb> a <noun>" reads badly. */
const PATHS: [method: string, path: RegExp, text: string][] = [
  ["POST", /^\/scans$/, "Started a scan"],
  ["POST", /^\/scans\/[^/]+\/retry$/, "Retried a scan"],
  ["POST", /^\/scans\/preflight$/, "Checked scan readiness"],
  ["PUT", /^\/findings\/[^/]+\/status$/, "Updated finding status"],
  ["POST", /^\/findings\/bulk$/, "Updated findings in bulk"],
  ["POST", /^\/repos$/, "Added a repository"],
  ["POST", /^\/repos\/[^/]+\/sync$/, "Synced a repository"],
  ["POST", /^\/repos\/[^/]+\/baselines$/, "Set a scan baseline"],
  ["POST", /^\/fixes$/, "Started a fix"],
  ["POST", /^\/fixes\/[^/]+\/resume$/, "Resumed a fix"],
  ["POST", /^\/remediations\/[^/]+\/accept$/, "Accepted a fix branch"],
  ["POST", /^\/vulnerabilities\/[^/]+\/merge$/, "Merged vulnerabilities"],
  ["POST", /^\/vulnerabilities\/[^/]+\/split$/, "Split a vulnerability"],
  ["POST", /^\/vulnerabilities\/[^/]+\/verify$/, "Verified a vulnerability"],
  ["POST", /^\/setup\/sample-repo$/, "Added the sample repository"],
  ["POST", /^\/sarif\/import$/, "Imported SARIF results"],
];

const NOUNS: Record<string, string> = {
  repos: "repository", scans: "scan", findings: "finding", fixes: "fix", collections: "collection",
  schedules: "schedule", suppressions: "suppression", policies: "policy", credentials: "credential",
  vulnerabilities: "vulnerability", nodes: "node", users: "user", tokens: "API key", secrets: "secret",
  remediations: "remediation", webhooks: "webhook",
};
const VERBS: Record<string, string> = { POST: "Created", PUT: "Updated", PATCH: "Updated", DELETE: "Deleted" };

const article = (noun: string) => (/^[aeiou]/i.test(noun) ? "an" : "a");

/** The path without the /api or /api/v1 prefix, matching what the server classifies. */
export function auditPath(path: string): string {
  return path.replace(/^\/api(?:\/v1)?(?=\/)/, "").replace(/\/$/, "");
}

export function describeAudit(e: AuditLike): { text: string; failed: boolean } {
  const failed = e.status_code >= 400;
  const path = auditPath(e.path);
  const known = EVENTS[e.event_type ?? ""] ?? PATHS.find(([m, re]) => m === e.method && re.test(path))?.[2];
  if (known) return { text: known, failed };
  const resource = path.split("/")[1] ?? "";
  const noun = NOUNS[resource] ?? (resource.replace(/-/g, " ").replace(/ies$/, "y").replace(/s$/, "") || "item");
  const verb = VERBS[e.method];
  // Unknown verb (an audited read or exotic method): fall back to the recorded action.
  return { text: verb ? `${verb} ${article(noun)} ${noun}` : `${e.action || e.method} ${noun}`, failed };
}
