# Changelog

## [2.0.0] — TBD

### Breaking

- **Docker is now a hard runtime requirement.** Wolf-slim mounts the
  docker socket to spawn short-lived scanner containers per tool
  invocation. Host-installed scanner binaries are no longer used.
- **Finding fingerprints invalidate.** `FilePath` is now repo-relative
  (e.g. `pkg/foo.go`) instead of absolute (`/home/me/repo/pkg/foo.go`).
  Existing findings keep their old fingerprints and will look "new"
  after the first 2.0 scan. See `docs/MIGRATION_2_0.md` §"Migrating
  fingerprints".
- **Repos must live under `WOLF_REPOS_ROOT`.** Wolf-slim mounts this
  host directory read-only at `/repos` inside its container. The wolf
  CLI accepts paths like `/repos/myproject`.
- **Removed `exec.LookPath`-based scanner availability.** All plugins
  now check `container.IsScannersReady()` against the configured
  scanner image set.

### Added

- **Containerized scanners** — three-tier hybrid:
  - **Upstream-official images** routed per-tool (trivy, grype, syft,
    osv-scanner, gitleaks, trufflehog, hadolint, dockle, checkov, tflint,
    kubescape, kube-linter, semgrep, nuclei, vale, spectral, scancode,
    scorecard). No wolf rebuild burden for these; upstream maintainers
    handle multi-arch builds and CVE responses.
  - **Slim wolf-built default image** `wolf-scanners` (~600–900 MB on
    amd64) carrying the per-language tools that don't have a usable
    upstream image (bandit/ruff/mypy/pip-audit/radon/vulture, gosec/
    staticcheck/govulncheck, eslint, brakeman/rubocop, phpstan, swiftlint,
    cppcheck, shellcheck, detect-secrets, sqlfluff).
  - **Wolf-built bucket images** for heavy toolchains: `wolf-scanners-jvm`
    (~2 GB: infer + pmd + JDK), `wolf-scanners-rust` (~1.2 GB: clippy +
    rust toolchain), `wolf-scanners-codeql` (~700 MB: CodeQL, opt-in).
- **`internal/plugin/container/`** — new shim that translates the
  legacy `plugin.CommandContext` into `docker run --rm` invocations
  with read-only bind mounts, user mapping, network policy, resource
  caps, and per-tool image lookup via `Config.ImageOverrides`.
- **`internal/setup/scanners/`** — `LoadAndInstall`, `Doctor`, `Pull`
  helpers wired into `cmd/wolf/main.go` startup and CLI subcommands.
- **`wolf doctor`** — diagnostics: docker reachable, image present,
  uid/gid mapping, repos-root pairing.
- **`wolf pull scanners`** — pre-pull every configured scanner image.
- **`/api/scanners/config|doctor|pull`** REST endpoints.
- **`/scanners` UI page** — read-only config viewer + Doctor / Pull
  All Images buttons.
- **OpenSSF Scorecard** scanner — repo-hygiene scoring (branch
  protection, signed releases, pinned deps).
- **CI workflows**: `.github/workflows/scanners-image.yml` (matrix
  build of 4 images with smoke test) and `go.yml` (vet/build/test/
  lint).
- **`docs/MIGRATION_2_0.md`** and **`docs/RELEASE_NOTES_2_0.md`**.

### Changed

- `Dockerfile` (wolf-slim) now installs `docker-cli` and adds the wolf
  user to the docker group.
- `docker-compose.yml` mounts `/var/run/docker.sock` and
  `${WOLF_REPOS_ROOT}:/repos:ro`, threads all `WOLF_SCANNERS_*` env
  vars, declares a `wolf-scanners-db` named volume.
- `configs/wolf.yaml` gained a `scan.container` section with image,
  overrides, pull policy, network mode, memory/CPU caps, db volume,
  path translation.
- All 40 existing plugins migrated to `container.CommandContext`; path
  normalization (`/scan/foo` → `foo`) applied to finding paths.
- README rewritten for the container-only quick-start.

### Restored

- `internal/artifacts/` package (was referenced but missing from a
  prior commit; the build was broken before this release).
- `cmd/wolf/main.go` (was missing from the prior commit; CLI entry
  built from scratch with cobra subcommands).

### Migration

See `docs/MIGRATION_2_0.md` for the full upgrade guide. Short version:

```bash
docker compose down
docker compose pull wolf
mkdir -p ./repos && ln -s /your/project ./repos/your-project
docker compose up -d
docker compose exec wolf wolf pull scanners
docker compose exec wolf wolf doctor
docker compose exec wolf wolf scan --repo /repos/your-project
```
