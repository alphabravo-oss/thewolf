# Remote Scan Provenance

Wolf uses one scan-quality model for local paths, GitHub/Git clones, SSH remote
paths, SARIF imports, and future CI scans.

## Source Fields

Scan manifests and findings can include:

- `kind`
- `repo_id`
- `repo_path`
- `branch`
- `commit_sha`
- `dirty_state`
- `remote_node_id`
- `snapshot_strategy`

Credentials, tokens, private keys, and passwords must never be written to
provenance fields.

## Source Kinds

Current source kinds include:

- `local_path`
- `git_clone`
- `github`
- `ssh_path`
- `sarif_import`

CI should use a future explicit source kind instead of overloading GitHub or
local scans.

## Baseline Compatibility

Wolf rejects baseline comparisons across different repositories, incompatible
source kinds, and different SSH remote nodes by default.

## Validation

Run:

```sh
go test ./internal/api/routes ./internal/scantarget ./internal/sshclient
```
