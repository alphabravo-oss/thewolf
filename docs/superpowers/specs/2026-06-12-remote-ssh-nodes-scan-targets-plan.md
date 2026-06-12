# Design & Plan: Remote SSH Nodes and Multi-Source Scan Targets

- **Date:** 2026-06-12
- **Branch:** `main`
- **Status:** Proposed
- **Author:** Codex planning session

---

## 1. Goal

Make Wolf able to scan code wherever an engineer is working:

1. **Local checkout** on the Wolf host or inside the Wolf container.
2. **Git-hosted repository** such as GitHub/GitLab, cloned or refreshed by Wolf.
3. **Remote Linux development node over SSH**, where the engineer has an unpushed
   branch and wants to scan it before pushing.

The immediate target is remote SSH nodes. CI integration is explicitly out of scope
for this phase, but the design must not paint us into a corner. CI should later reuse
the same scan-target abstraction and API-token auth surface.

### Success Criteria

- A user can register a remote Linux node with SSH connection details.
- Wolf can validate connectivity, remote `git` state, and scanner prerequisites.
- A user can add a repo path from that remote node and select a branch.
- A user can start a scan against the exact branch and commit currently present on
  the remote node.
- Scan findings, logs, reports, branch, commit SHA, and node provenance are stored in
  the existing Wolf scan history.
- Local scans continue to work unchanged.
- Existing API-token and CLI flows remain compatible.
- No SSH private key, password, or secret token is logged or returned by API responses.

---

## 2. Current State

### 2.1 What Exists

- `Repo` has `source_type`, `source_path`, and `default_branch`.
- Current source types are `local`, `github`, `gitlab`, and `git`.
- API scan execution calls `executeScan(..., repo.SourcePath, branch, req)`.
- `executeScan` treats `repo.SourcePath` as a local filesystem path.
- Branch listing calls local `git` against `repo.SourcePath`.
- GitHub/GitLab clone helpers exist in `internal/repo`, but are not wired into the
  API/UI scan execution path.
- `wolf scan --repo <path>` is a direct local one-shot scan.
- API tokens are available for automation, but CI-specific workflows are not built.

### 2.2 Gaps

- No remote node model.
- No API endpoints for creating, testing, listing, or deleting remote nodes.
- No SSH credential storage model for node access.
- No remote filesystem browsing.
- No remote git branch/commit inspection.
- No scan executor that can run against a remote checkout.
- No remote artifact/log collection strategy.
- No UI for remote nodes or remote repos.
- No explicit scan provenance model beyond branch and repo ID.
- No threat model around SSH command execution.

---

## 3. Product Model

### 3.1 Core Concepts

**Scan Target**

A scan target is the thing Wolf can prepare and scan. It abstracts over where code
lives.

Supported target kinds after this project:

- `local_path`: existing behavior.
- `git_clone`: remote Git URL cloned into Wolf-managed cache.
- `ssh_path`: path on a registered remote SSH node.

**Remote Node**

A machine Wolf can reach over SSH. It is not a worker pool yet; it is a remote
development environment that hosts source checkouts.

Fields:

- ID
- display name
- host
- port
- username
- auth method
- associated secret IDs
- allowed root paths
- default shell, optional
- status metadata
- last successful check time
- created/updated timestamps

**Remote Repo**

A repo whose source is a path on a remote node.

Fields:

- repo ID
- source type: `ssh`
- remote node ID
- remote path
- default branch
- last known commit SHA
- detected languages/frameworks

**Execution Mode**

Remote scans can be executed two ways. The first implementation should support one
mode and leave room for the other.

1. **Remote Wolf execution**: connect over SSH and run `wolf scan --repo <path>` on
   the remote host, then collect normalized results.
2. **Remote archive execution**: use SSH/SFTP/rsync to copy an archive or checkout
   snapshot back to the Wolf host, then run existing scanners locally.

Recommended first implementation: **remote archive execution**.

Reasoning:

- Reuses existing scanner runner and artifact pipeline.
- Does not require Docker/scanner images on the engineer's dev server.
- Keeps all scanning container execution on the Wolf host.
- Avoids requiring remote users to install Wolf on every dev server.
- Produces consistent findings because the same local runner is used.

Remote Wolf execution can be added later for very large repos or restricted networks.

---

## 4. User Workflows

### 4.1 Add a Remote Node

1. User opens Settings -> Nodes.
2. User enters:
   - name
   - host
   - port
   - username
   - auth method
   - allowed repo roots, e.g. `/home/alice/src`, `/workspaces`
3. User selects or creates an SSH credential secret.
4. Wolf tests:
   - TCP connection
   - SSH handshake
   - host key verification
   - user identity
   - remote shell command execution
   - availability of `git`, `tar`, and basic POSIX utilities
5. Node is saved only if validation passes, unless user explicitly saves as disabled.

### 4.2 Add a Remote Repo

1. User chooses “Add repo”.
2. User selects source: Local, Git URL, Remote SSH node.
3. For Remote SSH node:
   - select node
   - browse or enter a path under allowed roots
   - Wolf validates path exists and is a git working tree
   - Wolf lists branches and current branch
   - Wolf captures current commit SHA and dirty status
4. User saves repo.

### 4.3 Scan Remote Branch Before Push

1. Engineer has branch checked out on remote server.
2. In Wolf UI or CLI, user selects remote repo.
3. Wolf shows:
   - remote node
   - path
   - current branch
   - current commit
   - dirty/untracked status
4. User starts scan.
5. Wolf prepares a snapshot:
   - fail or warn on dirty state depending on setting
   - create archive with `git archive` plus optionally tracked dirty changes
   - transfer snapshot to Wolf scan workspace
6. Existing scanner runner scans snapshot locally.
7. Findings are mapped back to repo-relative paths.
8. Scan record stores remote node/path/branch/commit provenance.

### 4.4 CLI Usage

Initial CLI examples:

```bash
wolf node create --name devbox --host dev.example.com --user alice --root /home/alice/src
wolf node doctor devbox
wolf repo create --name api --node devbox --path /home/alice/src/api
wolf scan create --repo <repo-id> --branch feature/auth
wolf scan watch <scan-id>
```

For quick one-shot remote scans:

```bash
wolf scan --ssh devbox:/home/alice/src/api --branch current
```

The one-shot command is optional for the first API-first milestone. The API/UI path is
higher priority.

---

## 5. Architecture

### 5.1 New Packages

`internal/nodes`

- Remote node domain service.
- Validation orchestration.
- Node-level policy checks.
- Converts DB models into runtime SSH configs.

`internal/sshclient`

- Thin, testable wrapper around `golang.org/x/crypto/ssh`.
- Host key checking.
- Command execution with context timeout.
- SFTP or tar-stream helpers.
- Redacted structured logging.

`internal/scantarget`

- Resolves a `Repo` + requested branch into a local scan workspace.
- Interface:

```go
type PreparedTarget struct {
    WorkspacePath string
    Cleanup func()
    Provenance ScanProvenance
}

type Resolver interface {
    Prepare(ctx context.Context, repo models.Repo, branch string) (*PreparedTarget, error)
}
```

Resolvers:

- `LocalResolver`
- `GitCloneResolver`
- `SSHArchiveResolver`

`internal/repo` can keep GitHub/GitLab helpers, but scan execution should call
`scantarget.Resolver`, not source-specific clone code directly.

### 5.2 Database Model

Add migration `012_remote_nodes_and_scan_targets.sql`.

Tables:

```sql
remote_nodes (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    host TEXT NOT NULL,
    port INTEGER NOT NULL DEFAULT 22,
    username TEXT NOT NULL,
    auth_method TEXT NOT NULL,
    credential_secret_id TEXT REFERENCES secrets(id),
    host_key TEXT NOT NULL DEFAULT '',
    allowed_roots TEXT NOT NULL DEFAULT '[]',
    disabled_at TIMESTAMP,
    last_checked_at TIMESTAMP,
    last_check_status TEXT NOT NULL DEFAULT '',
    last_check_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

Extend `repos`:

```sql
ALTER TABLE repos ADD COLUMN remote_node_id TEXT REFERENCES remote_nodes(id);
ALTER TABLE repos ADD COLUMN remote_path TEXT NOT NULL DEFAULT '';
ALTER TABLE repos ADD COLUMN last_commit_sha TEXT NOT NULL DEFAULT '';
ALTER TABLE repos ADD COLUMN last_dirty_state TEXT NOT NULL DEFAULT '';
```

Alternative: do not alter `repos`; encode remote path in `source_path` and use a
separate `repo_sources` table. Recommended: alter `repos` minimally because existing
UI/API already centers on repo records.

Extend `scans`:

```sql
ALTER TABLE scans ADD COLUMN source_type TEXT NOT NULL DEFAULT '';
ALTER TABLE scans ADD COLUMN remote_node_id TEXT;
ALTER TABLE scans ADD COLUMN source_path TEXT NOT NULL DEFAULT '';
ALTER TABLE scans ADD COLUMN commit_sha TEXT NOT NULL DEFAULT '';
ALTER TABLE scans ADD COLUMN dirty_state TEXT NOT NULL DEFAULT '';
ALTER TABLE scans ADD COLUMN prepared_workspace TEXT NOT NULL DEFAULT '';
```

Reasoning:

- Scan provenance must be immutable. Repo metadata can change later.
- Reports should show what was scanned, not what the repo points to today.

### 5.3 Models

Add:

- `models.RemoteNode`
- `models.RemoteNodeStatus`
- `models.ScanProvenance`

Extend:

- `models.SourceType` with `SourceTypeSSH = "ssh"`
- `models.Repo` with `RemoteNodeID`, `RemotePath`, `LastCommitSHA`, `LastDirtyState`
- `models.Scan` with immutable source/provenance fields

### 5.4 Store Interface

Add methods:

```go
CreateRemoteNode(ctx, node)
GetRemoteNodeByID(ctx, id)
ListRemoteNodesByUser(ctx, userID)
UpdateRemoteNode(ctx, node)
DeleteRemoteNode(ctx, id)
TouchRemoteNodeCheck(ctx, id, status, error)
```

Update SQLite and Postgres stores together.

### 5.5 API Endpoints

Add route group `/api/v1/nodes`.

Endpoints:

| Method | Path | Scope | Purpose |
| --- | --- | --- | --- |
| GET | `/nodes` | `read:config` | List remote nodes |
| POST | `/nodes` | `write:config` | Create remote node |
| GET | `/nodes/{id}` | `read:config` | Get node |
| PUT | `/nodes/{id}` | `write:config` | Update node |
| DELETE | `/nodes/{id}` | `write:config` | Delete node |
| POST | `/nodes/{id}/check` | `write:config` | Run connectivity/prereq check |
| GET | `/nodes/{id}/browse?path=` | `read:repos` | Browse allowed remote paths |
| GET | `/nodes/{id}/git-info?path=` | `read:repos` | Validate git repo, branches, commit |

Extend `/repos`:

- `POST /repos` accepts `source_type: "ssh"`, `remote_node_id`, `remote_path`.
- Local repos remain unchanged.
- For SSH repos, `source_path` can be stored as `ssh://node-id/path` or the raw
  `remote_path`; use dedicated fields for clarity.

Extend `/scans`:

- Scan creation can request branch as today.
- Server resolves repo using `scantarget`.
- Scan response includes provenance.

### 5.6 CLI Commands

Add command group:

```text
wolf node list
wolf node create
wolf node get
wolf node update
wolf node delete
wolf node doctor
wolf node browse
wolf node git-info
```

Extend repo command:

```text
wolf repo create --node <node-id-or-name> --path /remote/path --name <name>
```

Extend scan command:

```text
wolf scan create --repo <repo-id> --branch <branch>
wolf scan --ssh <node-name>:/remote/path --branch current
```

Only API-client commands are required for first milestone. Direct local one-shot SSH
scan can be later.

### 5.7 UI

Settings:

- Add `Nodes` tab.
- Node list table:
  - name
  - host
  - username
  - status
  - last checked
  - actions: check, edit, delete
- Node create/edit modal:
  - host, port, username
  - auth method
  - credential selector
  - allowed roots
  - host key policy

Repo add flows:

- Source segmented control:
  - Local path
  - Git URL
  - Remote SSH node
- Remote SSH fields:
  - node select
  - path input/browse
  - branch dropdown from remote git info
  - status panel showing current branch/commit/dirty state

Scan creation:

- For SSH repos, show node/path provenance.
- Branch selector uses remote git-info endpoint.
- Warn if branch is dirty/uncommitted based on policy.

Scan detail:

- Show source type.
- Show remote node name, path, commit SHA, dirty state.
- Reports include same metadata.

---

## 6. Security Design

### 6.1 SSH Credential Handling

Supported auth methods:

1. Private key stored in Wolf secrets.
2. Private key path on Wolf host, restricted to allowed configured paths.
3. SSH agent forwarding from Wolf host, optional later.
4. Password auth should be avoided initially. If added, store as secret and mark
   discouraged.

Private key secrets:

- Stored through existing encrypted secrets system.
- Never returned through API.
- Never logged.
- Redacted in errors.

### 6.2 Host Key Verification

Do not default to blind trust for production.

Policies:

- `strict`: stored host key must match.
- `trust_on_first_use`: first successful connection stores host key.
- `insecure_ignore`: development only, visible warning.

Recommended default: `trust_on_first_use` for self-hosted usability, with UI warning
and ability to pin/rotate host key.

### 6.3 Path Controls

Every node has `allowed_roots`.

Remote commands must reject paths that:

- are empty
- contain NUL
- escape allowed roots after `realpath`
- are not directories
- are not git worktrees when used as repos

Browsing must not allow arbitrary filesystem traversal outside allowed roots.

### 6.4 Command Execution Controls

Avoid ad hoc string concatenation.

Use a small command builder:

- command name from allowlist: `git`, `tar`, `test`, `find`, `realpath`, `stat`
- arguments escaped with single-purpose shell quoting
- fixed remote shell prefix: `set -euo pipefail`
- context timeout for every command
- max stdout/stderr bytes captured

Preferred approach for archive:

```bash
git -C <repo> archive --format=tar <commit>
```

For dirty-state support, start with:

- detect dirty/untracked
- by default require clean worktree
- later support including dirty changes by generating a patch and applying it to the
  archive workspace

Reasoning:

- Scanning uncommitted changes is valuable, but reproducing exact dirty state safely is
  more complex.
- First robust version should scan the exact committed branch/commit and clearly fail
  or warn on dirty state.

### 6.5 Network Controls

Remote node connections can be SSRF-like if arbitrary hosts are accepted.

Mitigations:

- `write:config` required to create/update nodes.
- Audit node create/update/delete/check actions.
- Optional server env allowlist:
  - `WOLF_SSH_ALLOWED_CIDRS`
  - `WOLF_SSH_BLOCK_PRIVATE_RANGES=false` for self-hosted deployments
- Reject link-local metadata addresses by default unless explicitly allowed.

### 6.6 Audit Logging

Audit:

- node create/update/delete
- node check
- remote browse
- remote git-info
- remote scan start

Include:

- user ID
- node ID
- repo ID
- remote path
- command category, not full secrets
- status code

---

## 7. Implementation Phases

## Phase 0: Threat Model and Target Abstraction

### Tasks

- Add short threat model doc section to this plan or `docs/security/remote-nodes.md`.
- Define `scantarget.Resolver` interface.
- Refactor existing scan path so local scans go through `LocalResolver`.
- Preserve behavior for local path scans.
- Add provenance object to scan preparation even for local scans.

### Definition of Done

- Existing local scan tests pass.
- `executeScan` no longer assumes every repo source path is directly scannable.
- No user-visible change yet.

### Tests

- Unit test `LocalResolver`.
- Existing scan route tests still pass.
- Regression test local repo scan stores source type/path provenance.

---

## Phase 1: Remote Node Data Model and API

### Tasks

- Add DB migration for `remote_nodes`.
- Add repo and scan provenance columns.
- Add `models.RemoteNode`.
- Add SQLite/Postgres store methods.
- Add `/api/v1/nodes` CRUD routes.
- Add OpenAPI endpoint table entries.
- Add CLI node command group as API client commands.
- Add audit logging for mutating node endpoints.

### Definition of Done

- Users with `write:config` can create/update/delete nodes.
- Users with `read:config` can list/get nodes.
- API never returns secret material.
- OpenAPI route coverage passes.
- CLI can list/create/get/delete nodes.

### Tests

- SQLite migration creates node tables.
- Postgres query syntax unit/compile coverage.
- API tests for create/list/get/update/delete.
- Scope tests:
  - read-only token cannot create/update/delete
  - write token can create
  - unauthenticated rejected
- OpenAPI coverage test updated.
- CLI e2e for node CRUD.

---

## Phase 2: SSH Client and Node Doctor

### Tasks

- Add `internal/sshclient`.
- Implement key-based auth from stored secret.
- Implement host key policies.
- Implement command execution with:
  - context timeout
  - max output bytes
  - redaction
  - structured result
- Implement `nodes.CheckNode`.
- Add `POST /nodes/{id}/check`.
- Add `wolf node doctor`.
- Store last check status/error/time.

### Definition of Done

- Node doctor validates connection and reports actionable failures.
- Host key mismatch is detected and reported.
- Missing `git`/`tar` are detected.
- Secrets never appear in logs or response payloads.

### Tests

- Unit tests for command quoting.
- Unit tests for redaction.
- Unit tests for allowed host key policies.
- Integration test with local ephemeral SSH server or test double.
- API test for check result shape.
- Failure tests:
  - unknown node
  - disabled node
  - bad host key
  - command timeout

---

## Phase 3: Remote Browse and Git Info

### Tasks

- Implement `GET /nodes/{id}/browse?path=`.
- Implement `GET /nodes/{id}/git-info?path=`.
- Enforce allowed roots using remote `realpath`.
- List child directories only; do not expose files unless needed.
- Return git info:
  - is_git_repo
  - branches
  - current_branch
  - head_commit
  - dirty
  - untracked_count
  - remote URLs redacted if credentials embedded

### Definition of Done

- UI/CLI can inspect remote repo paths safely.
- Paths outside allowed roots are rejected.
- Branch picker works for remote repos.

### Tests

- Path normalization and root-escape tests.
- Browse happy path.
- Browse outside root rejected.
- Git info against:
  - valid repo
  - non-repo directory
  - missing path
  - repo with dirty worktree
- Output byte-limit tests.

---

## Phase 4: Remote Repo Creation

### Tasks

- Extend `SourceType` with `ssh`.
- Extend repo model/store with `remote_node_id`, `remote_path`, commit metadata.
- Update `POST /repos` validation for SSH source.
- For SSH repos:
  - require valid node
  - require path under allowed root
  - require git worktree
  - detect default/current branch
  - capture commit and dirty state
- Add UI “Remote SSH node” repo add flow.
- Add CLI `repo create --node --path`.

### Definition of Done

- A user can save a remote SSH repo.
- Duplicate remote repos are deduplicated by `(node_id, normalized_remote_path)`.
- Local repo creation still works.
- Remote repo appears in repo list/detail with source metadata.

### Tests

- API create remote repo success.
- API reject invalid node.
- API reject path outside root.
- API reject non-git path.
- Dedup test for same node/path.
- UI typecheck/build.
- CLI e2e for remote repo create using mocked API or test server.

---

## Phase 5: Remote Archive Scan Resolver

### Tasks

- Implement `SSHArchiveResolver`.
- Resolve branch:
  - `current` means remote current branch
  - explicit branch must exist
- Capture commit SHA before scan.
- Detect dirty state.
- Policy setting:
  - `remote_scan_dirty_policy = fail|warn`
  - recommended default: `fail`
- Create remote tar archive with `git archive`.
- Stream archive to local temp workspace.
- Run existing scanner runner on temp workspace.
- Cleanup workspace after scan completion.
- Persist provenance on scan.

### Definition of Done

- Remote SSH repo can be scanned from API/UI.
- Findings use repo-relative paths.
- Reports include remote node/path/branch/commit.
- Dirty worktree behavior is explicit and tested.
- Existing local scans remain unchanged.

### Tests

- Unit test resolver with fake SSH client.
- Integration test with local git repo served through SSH test double.
- Scan route test for SSH repo starts and stores provenance.
- Dirty policy tests:
  - fail rejects dirty worktree
  - warn proceeds and records dirty state
- Cleanup test removes temp workspace.
- Cancellation test stops transfer/scanner when scan is cancelled.

---

## Phase 6: UI Integration

### Tasks

- Add Settings -> Nodes tab.
- Add node create/edit/delete/check UI.
- Add remote browse modal.
- Add remote source option to repo add panels.
- Add remote branch picker.
- Add scan detail provenance display.
- Add scanner setup warning if remote scans are prepared locally but scanners are not
  installed on Wolf host.

### Definition of Done

- User can complete full workflow from UI:
  - add node
  - check node
  - add remote repo
  - select branch
  - start scan
  - watch progress
  - view findings/report
- UI makes local/Git/SSH source distinctions clear.
- No visible secret material.

### Tests

- React typecheck.
- Vite build.
- Component tests if test framework exists; otherwise route-level smoke with Playwright
  or Browser plugin:
  - nodes tab renders
  - add-node form validates required fields
  - disabled registration/settings unaffected
  - remote repo add flow handles API success/error states
- Screenshot verification for settings/nodes and repo add flow.

---

## Phase 7: Git URL Scan Resolver

This can happen before or after SSH scanning, but should use the same `scantarget`
interface.

### Tasks

- Wire existing GitHub/GitLab clone helpers into `GitCloneResolver`.
- Support generic HTTPS Git URL.
- Use configured secrets for private repos.
- Refresh existing cache safely:
  - fetch
  - checkout requested branch/commit
  - avoid scanning stale branch
- Store provenance.

### Definition of Done

- GitHub/GitLab source repos are actually cloneable/scannable.
- Existing source type constants become real behavior, not metadata only.

### Tests

- Unit tests for URL parsing.
- Resolver tests using local bare git repo.
- Private token is not logged.
- Branch checkout test.
- Stale cache refresh test.

---

## Phase 8: Hardening and Operational Controls

### Tasks

- Add rate limits for node check/browse/git-info.
- Add global SSH timeout settings:
  - `remote_ssh_connect_timeout`
  - `remote_ssh_command_timeout`
  - `remote_scan_transfer_timeout`
- Add transfer size limits:
  - max archive bytes
  - max repo files, optional
- Add admin settings for dirty policy and allowed CIDRs.
- Add docs:
  - setup remote node
  - security notes
  - troubleshooting
  - known limitations

### Definition of Done

- Remote operations have clear timeouts and size limits.
- Operators can restrict network targets.
- Docs are sufficient for a first user to set this up without reading code.

### Tests

- Timeout tests.
- Archive size-limit tests.
- CIDR allow/deny tests.
- Docs smoke review for commands and env vars.

---

## 8. Data Flow: SSH Archive Scan

1. User starts scan for SSH repo.
2. API loads repo and remote node.
3. `SSHArchiveResolver.Prepare` runs:
   - validate node enabled
   - validate remote path under allowed root
   - `git rev-parse --abbrev-ref HEAD`
   - `git rev-parse HEAD`
   - `git status --porcelain`
   - apply dirty policy
   - `git archive --format=tar <commit>`
   - stream tar to local temp directory
4. Resolver returns local workspace + provenance.
5. Existing `runner.Run` scans workspace.
6. Findings are persisted against original repo ID.
7. Scan record stores immutable remote provenance.
8. Temp workspace is deleted.

---

## 9. Definition of Done for Entire Project

Functional:

- Local scan works as before.
- Remote SSH node CRUD works through API, CLI, and UI.
- Remote node doctor provides actionable diagnostics.
- Remote path browse and git-info work within allowed roots.
- Remote repo creation works.
- Remote SSH repo scan works and records provenance.
- Git URL resolver either works or is explicitly documented as next phase.

Security:

- SSH credentials encrypted at rest.
- Secrets are never returned or logged.
- Host key verification exists.
- Path traversal outside allowed roots is blocked.
- Remote command execution is restricted and time-bounded.
- Audit log records remote node changes and remote scan starts.
- Credentialed CORS/auth changes remain intact.

Quality:

- SQLite and Postgres migrations implemented.
- OpenAPI endpoint table updated.
- CLI commands implemented for all node endpoints.
- Unit, API, CLI, and UI build tests pass.
- Remote behavior has fake/integration tests that do not require a real external server.
- Documentation includes setup and troubleshooting.

---

## 10. Test Matrix

### Backend Unit Tests

- SSH command quoting.
- Secret redaction.
- Host key policy.
- Allowed-root path validation.
- Dirty worktree parser.
- Git branch parser.
- Scan target resolver selection.
- Local resolver no-op behavior.
- SSH archive resolver with fake client.

### Database Tests

- SQLite migration creates tables/columns.
- Remote node CRUD.
- Repo with SSH source persists and loads.
- Scan provenance persists and loads.
- Delete user cascades remote nodes if intended.
- Delete node behavior:
  - reject if repos reference it, or
  - soft-delete/disable. Recommended: soft-delete/disable first.

### API Tests

- Node CRUD scope enforcement.
- Node check success/failure.
- Browse allowed path.
- Browse denied path.
- Git info success/non-repo.
- Remote repo create success/failure.
- Remote scan create success.
- Remote dirty policy failure.
- Public endpoints remain public.
- Auth/session tests still pass.

### CLI Tests

- `wolf node create/list/get/delete`.
- `wolf node doctor`.
- `wolf node browse`.
- `wolf repo create --node`.
- JSON output shape.
- Exit codes for auth, not found, validation errors.

### UI Tests

- Typecheck.
- Production build.
- Settings -> Nodes renders.
- Node form validation.
- Node check loading/success/error states.
- Remote repo add flow.
- Remote scan provenance display.

### Security Tests

- Secret values redacted in all responses.
- Secret values redacted in logs/errors.
- Host key mismatch rejected.
- Path traversal rejected:
  - `..`
  - symlink escaping root
  - absolute path outside root
- Command timeout cancels.
- Output cap enforced.
- Disallowed CIDR rejected if configured.

---

## 11. Configuration

New settings/env:

```text
WOLF_SSH_ALLOWED_CIDRS=
WOLF_SSH_BLOCK_LINK_LOCAL=true
WOLF_REMOTE_SCAN_DIR=/tmp/wolf-remote-scans
WOLF_REMOTE_SCAN_MAX_ARCHIVE_BYTES=1073741824
WOLF_REMOTE_SSH_CONNECT_TIMEOUT=10s
WOLF_REMOTE_SSH_COMMAND_TIMEOUT=30s
WOLF_REMOTE_SCAN_TRANSFER_TIMEOUT=10m
```

New DB settings:

```text
remote_scan_dirty_policy=fail
remote_scan_cleanup_workspaces=true
```

---

## 12. Open Questions

1. Should remote dirty worktrees be supported in v1, or should v1 require clean commit
   state?
   - Recommendation: require clean by default; add dirty patch support later.
2. Should node credentials be global admin-managed or per-user?
   - Recommendation: per-user ownership now; admin can see/manage all later if RBAC is
     added.
3. Should deleting a node delete remote repos?
   - Recommendation: soft-disable nodes; prevent hard delete while repos reference it.
4. Should remote nodes run scanners themselves later?
   - Recommendation: yes as Phase 9, but not initial implementation.
5. Should SSH agent forwarding be supported?
   - Recommendation: not in v1. Stored deploy key or host key path is more auditable.

---

## 13. Recommended First Milestone

Build a narrow vertical slice:

1. Data model for remote nodes.
2. Node CRUD API + CLI.
3. SSH check using key secret.
4. Remote git-info endpoint.
5. SSH repo creation.
6. SSH archive resolver with clean-worktree-only policy.
7. Scan detail provenance.

Do not start with UI polish. First prove the backend and CLI can run a real remote scan
end to end with a local test SSH server or a controlled dev VM. Then wire the UI on top
of the stable API.

---

## 14. Implementation Notes From 2026-06-12 Pass

Implemented the v1 vertical slice described above:

- Added `remote_nodes` persistence for SQLite and Postgres.
- Added `ssh` repo source type and encrypted SSH credential types:
  - `ssh_private_key`
  - `ssh_password`
- Added `/api/v1/nodes` CRUD plus:
  - `POST /nodes/{id}/check`
  - `GET /nodes/{id}/browse`
  - `GET /nodes/{id}/git-info`
- Added OpenAPI catalog entries for every node route.
- Added SSH command execution with host-key verification by default.
- Added explicit password auth support via encrypted credential secrets.
- Added scan target preparation:
  - local repos pass through unchanged
  - SSH repos export `git archive` over SSH, decode locally, extract into a temporary workspace, and scan through the existing runner
- Added scan provenance fields:
  - `source_type`
  - `remote_node_id`
  - `source_path`
  - `commit_sha`
  - `dirty_state`
  - `prepared_workspace`
- Added default dirty-worktree enforcement:
  - `remote_scan_dirty_policy=fail` by default
  - `remote_scan_dirty_policy=allow` records dirty state but scans committed archive content only
- Added CLI support:
  - `wolf node list|get|create|update|delete|check|browse|git-info`
  - `wolf repo create --type ssh --node <id> --path <remote-path>`
- Added UI support:
  - Settings -> Secrets includes SSH private key and SSH password secret types
  - Settings -> Nodes manages SSH nodes and runs checks
  - Collection add-repo flow supports SSH node repos
  - Repo list/detail distinguish SSH repos and display remote provenance

Additional issue found and fixed:

- Postgres migration sequence had skipped older scan metadata migrations. The Postgres migrator now applies migrations 008 and 009 before API token/session/remote-node migrations, preserving `ai_log_cost` and `tools_errors` parity.
- The scanner CLI command block had a duplicated `RunE` line. It was fixed while adding node CLI commands.

Validation completed:

```text
go test ./...
npm run typecheck
npm run build
```

Targeted tests added:

- SSH shell quoting.
- SSH archive scan preparation with a fake SSH runner.
- Dirty remote worktree rejection by default.
- Dirty remote worktree allow policy.

Known intentional v1 constraints:

- SSH host-key verification requires a `known_hosts` entry unless `WOLF_SSH_INSECURE_SKIP_HOST_KEY=true` is explicitly set for dev/test.
- Remote scans use committed git archive content. Uncommitted remote changes are not included.
- Remote scanners do not execute on the remote node yet; scanners still run locally against a prepared archive workspace.
- GitHub Actions/CI entrypoints remain future work.
