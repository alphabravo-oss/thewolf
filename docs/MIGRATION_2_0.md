# Migrating to wolf 2.0 — Containerized Scanners

Wolf 2.0 replaces the host-installed scanner model with a Docker-based one.
Operators no longer need ~40 tools installed on the host; wolf-slim spawns
short-lived scanner containers on demand. See
[`PLAN-containerized-scanner-execution.md`](PLAN-containerized-scanner-execution.md)
for the full design.

This guide covers the migration path for existing wolf 1.x installations.

## Breaking changes

1. **Docker is now a hard requirement.** Wolf-slim mounts `/var/run/docker.sock`
   and spawns scanner containers; the docker daemon must be reachable.

2. **Host-installed tools are no longer used.** Wolf 2.0 ignores whatever
   binaries are on `$PATH`. Removing `bandit`, `gosec`, `semgrep`, etc. from
   the host between scans changes nothing about wolf's output (and reclaims
   gigabytes of disk).

3. **Finding fingerprints change.** File paths in findings are now stored
   repo-relative (`pkg/foo.go`) rather than absolute (`/home/me/repo/pkg/foo.go`).
   This makes findings portable across hosts but invalidates existing
   fingerprints — every old finding looks "new" to the dedup/triage layer.
   Two options:
   - **Wipe the findings table** and start fresh (recommended if you don't
     have a triage workflow yet).
   - **Run the migration helper** to recompute fingerprints in place
     (see "Migrating fingerprints" below).

4. **Repos must live under `WOLF_REPOS_ROOT`.** Wolf-slim mounts this host
   directory read-only at `/repos` inside its container. The wolf CLI accepts
   paths like `/repos/myproject`; the path translation to host paths is
   automatic but only works if the repo is under the mounted root.

5. **`wolf scan --repo /absolute/host/path`** must instead be
   `wolf scan --repo /repos/myproject` from inside the wolf container, or
   `wolf scan --repo /Users/me/myproject` only if wolf is running directly
   on the host (no compose).

## Upgrading

```bash
# 1. Stop wolf 1.x
docker compose down

# 2. Pull the new wolf-slim image
docker compose pull wolf

# 3. Place the repos you want to scan under ./repos/ (or set WOLF_REPOS_ROOT)
mkdir -p ./repos
ln -s /path/to/your/project ./repos/your-project

# 4. Start the new wolf
docker compose up -d

# 5. Pre-pull the scanner image (optional — happens lazily otherwise)
docker compose exec wolf wolf pull scanners

# 6. Confirm everything is healthy
docker compose exec wolf wolf doctor

# 7. (Optional) Migrate existing findings to the new fingerprint format
docker compose exec wolf wolf migrate fingerprints --strip-prefix=/old/host/root

# 8. Run a scan
docker compose exec wolf wolf scan --repo /repos/your-project
```

## Migrating fingerprints

Old fingerprints are computed from `ToolName + RuleID + AbsolutePath`. After
the upgrade, `FilePath` is repo-relative, so fingerprints differ.

The provided helper (`internal/scan/runner.MigrateFingerprints(db, prefix)`)
rewrites the `findings.file_path` column to strip a given prefix, then
recomputes `findings.fingerprint`. Idempotent.

If you decide instead to **wipe** the findings table:

```sql
DELETE FROM findings;
DELETE FROM scans;   -- optional, depends on whether scans should be kept
```

## Air-gapped operators

If your wolf host can't reach `ghcr.io`, pre-load images on a connected
machine and transfer:

```bash
# Connected host
docker pull ghcr.io/alphabravocompany/wolf-scanners:1.0
docker save ghcr.io/alphabravocompany/wolf-scanners:1.0 \
  | gzip > wolf-scanners-1.0.tgz

# Air-gapped host
gunzip < wolf-scanners-1.0.tgz | docker load
export WOLF_SCANNERS_PULL_POLICY=Never
docker compose up -d
```

Repeat for each variant (jvm, rust, codeql) you need.

## License obligations

Wolf 2.0 bundles ~40+ tools in the scanner images. Some have notable license
terms — see `scanners/LICENSES.md`. The most operationally relevant:

- **CodeQL** — license-restricted; commercial use requires a GitHub Enterprise
  Advanced Security subscription. The codeql image is built separately so
  operators can choose not to pull it.
- **TruffleHog** — AGPL-3.0. If you offer wolf as a hosted service, you must
  publish your wolf-scanners build sources. AlphaBravo's source is open at
  the public wolf repo.
- **cppcheck, hadolint, shellcheck** — GPL-3.0. Bundling in a container is
  fine; redistributing modified sources requires source disclosure.

## Rollback

Wolf 2.0 is forward-compatible with the wolf 1.x findings table (other than
the fingerprint discontinuity discussed above). To roll back:

```bash
docker compose down
git checkout v1.x
docker compose up -d
```

The findings table will be readable but new scans will use 1.x's host-exec
plugin code. New fingerprints will once again use absolute paths — wolf 1.x
won't recognize the repo-relative ones written by 2.0.
