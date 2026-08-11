# PLAN: Containerized Scanner Execution

| Field | Value |
|---|---|
| **Status** | Draft — pending approval |
| **Author** | <mjclaude1@alphabravo.io> |
| **Created** | 2026-05-14 |
| **Scope** | v1: scanner tools (~40 plugins). Fix engine (claude/codex/git/gh/glab) deferred to v2. |
| **Target release** | wolf 2.0 (breaking change — fingerprints invalidate) |

---

## 0. TL;DR

`thewolf` currently shells out to ~40 host-installed binaries (bandit, gosec, semgrep, trivy, gitleaks, …) via `os/exec`. Each plugin in `plugins/<bucket>/*.go` calls `exec.LookPath("toolname")` for availability and `exec.CommandContext(...)` for execution. This couples wolf to a complex install matrix on every host and produces non-reproducible findings (version drift between hosts).

This change replaces that with:

1. **A new container image, `wolf-scanners:<wolf-version>`,** that bundles every scanner pinned at known versions (~6–8 GB).
2. **A new invocation shim** in `internal/plugin/container/` that translates the existing `exec.Cmd`-style call into `docker run --rm -v $REPO:/scan:ro --user $(id -u):$(id -g) wolf-scanners:tag <tool> <args>`.
3. **A topology change** (already partly present in `docker-compose.yml`): wolf-slim runs in its own container with the docker socket mounted (Docker-out-of-Docker), spawning short-lived scanner containers on demand.
4. **Removal of `exec.LookPath` from plugins.** Availability becomes a single check ("is the scanners image present?") shared across all plugins.

End state for an operator: `docker compose up -d`, point wolf at a repo, identical findings on any host. No `pip install`, no `go install`, no `npm i -g`.

---

## 1. Background

### 1.1 Current architecture

- **Wolf orchestrator** (`cmd/wolf/` → `internal/scan/runner/runner.go`) selects N plugins for a scan based on detected languages (`internal/scan/detector/`), runs them concurrently via `errgroup.SetLimit(concurrency)`, deduplicates findings, and emits fingerprints.
- **Plugins** (`plugins/<bucket>/*.go`, ~40 files) each implement `models.Plugin`:

  ```go
  type Plugin interface {
      Name() string
      Category() Category
      Languages() []Language
      CheckAvailable() bool                                      // → exec.LookPath
      Execute(ctx context.Context, opts ExecuteOpts) ([]Finding, error) // → exec.CommandContext
  }
  ```

- **Process control** (`internal/plugin/command.go`) wraps `exec.CommandContext` to put each child in its own process group, so context cancel kills the whole subprocess tree (essential for tools like semgrep that fork pysemgrep → semgrep-core).
- **Output parsing** lives in each plugin. Tools generally emit JSON on stdout; `cmd.Output()` captures it; the plugin unmarshals and maps to `models.Finding`.
- **Streaming** is per-tool via `opts.OnOutput(line)`, which the runner pipes to the SSE broker for live UI updates.
- **Today's Dockerfile** packages the wolf Go binary + Next.js UI on Alpine, but contains **no scanners** — scanners are expected to be installed on the host.

### 1.2 The pain

| Pain | Evidence |
|---|---|
| Install burden | README requires the user to install ~40 tools across 5 language runtimes. Realistic only for full-stack dev hosts. |
| Version skew | Two hosts with different Bandit minor versions emit different finding sets for the same repo. Fingerprints diverge. Trend metrics lie. |
| Host pollution | Scanners pull in transitive Python/Node/Rust toolchains. Conflicts with whatever else lives on the host. |
| Untrusted code exposure | Some scanners (notably `mypy`, `eslint` with project plugins) load code from the scanned repo. Running that under the host user is a credible vuln surface. |
| Distribution | Shipping wolf to non-dev users (auditors, compliance, SREs) requires shipping a fleet of scanner installers. |

### 1.3 What's already in place

- `Dockerfile` builds a slim `wolf` image (no scanners).
- `docker-compose.yml` runs wolf + optional Postgres with a `wolf-data` volume.
- `internal/plugin/command.go` already provides a clean abstraction over `exec.Cmd` we can extend.
- The runner uses `errgroup` + semaphore for concurrency — already container-agnostic.
- `OnOutput` callback for streaming is already plumbed through.

---

## 2. Goals & Non-Goals

### 2.1 Goals

1. **Eliminate scanner install burden.** A wolf operator runs `docker compose up -d` and pulls one image; everything works.
2. **Reproducible findings across hosts.** `wolf-scanners:1.0` pins every tool at a specific version. Two hosts on the same wolf version produce byte-identical findings (modulo network-dependent vuln DBs, called out separately).
3. **Host isolation.** Scanned code never executes against host filesystem or host user. Scanners run with `--user $(id -u):$(id -g)`, read-only repo mount, default no inbound network.
4. **Single-artifact distribution.** Two images (wolf-slim + wolf-scanners) is the entire install. No host package management required beyond Docker.
5. **Preserve operator-facing behavior.** Same concurrency knob, same streaming, same finding model, same UI. The execution backend changes; the contract around it doesn't.
6. **Incremental migration path.** Plugin-by-plugin migration; tests for each plugin pass against the shim before the old `exec.LookPath` path is deleted.

### 2.2 Non-goals (v1)

- **Containerizing the fix engine.** `claude`, `codex`, `git`, `gh`, `glab` continue to be host binaries. Tracked separately for v2.
- **Per-language image splits.** `wolf-scanners-python`, `wolf-scanners-go`, etc. are not v1. One fat image only.
- **Rootless docker, Podman, or Kubernetes Jobs as execution backends.** v1 ships docker-socket-only. The shim abstraction allows other backends later.
- **Sandbox-grade isolation** (gVisor, Kata, Firecracker). Out of scope; covered by the standard Linux namespace model docker gives us.
- **Migrating existing finding fingerprints.** Fingerprints will change for all findings because paths normalize from absolute-host to repo-relative. v1 will document this as a one-time invalidation. (See §6.4.)
- **Removing host-installed-tools support.** v1 makes this a hard cut: container-only. No `scan.execution: host | container | auto` toggle.

### 2.3 Constraints inherited

- ~40 plugin files exist today. The diff must touch each minimally (delete `CheckAvailable` body, swap `plugin.CommandContext` → `container.CommandContext`). Anything more invasive multiplies review surface 40×.
- The plugin output-parsing code is the actual value of each plugin file; it must not change.
- Tests in `plugins/<bucket>/*_test.go` exist for most plugins. They must keep passing.
- Wolf is currently distributed as a single Go binary in CI. Adding "Docker required" is itself a directional change that this plan formalizes.

---

## 3. Architectural Decisions (locked, from brainstorm)

| # | Decision | Reasoning |
|---|---|---|
| D1 | **Topology: wolf-slim container + wolf-scanners fat container; docker socket mounted into wolf-slim.** | Clean separation of orchestrator and scan environment. Mirrors how CI runners work. Wolf-slim stays small; scanners get their own attack surface. |
| D2 | **Invocation: one `docker run --rm` per tool invocation; concurrency stays wolf-side via existing `errgroup.SetLimit(concurrency)`.** | Preserves existing parallelism, streaming, dedup, cancel logic. ~40 plugins barely change. Per-container startup (~0.3–1s) is amortized by concurrency. |
| D3 | **Hybrid image strategy** (revised twice). Default state: wolf-built **slim** `wolf-scanners` image (~600–900 MB) carrying only the tools without a maintained upstream image (`bandit`, `ruff`, `mypy`, `pip-audit`, `radon`, `vulture`, `gosec`, `staticcheck`, `govulncheck`, `eslint`/`npm-audit`, `brakeman`, `rubocop`, `phpstan`, `cppcheck`, `shellcheck`, `swiftlint`, `detect-secrets`, `sqlfluff`); plus **wolf-built bucket images** `wolf-scanners-jvm` (infer + pmd), `wolf-scanners-rust` (clippy), `wolf-scanners-codeql` (codeql); plus **upstream-official images** routed per-tool via `Config.UpstreamTools` for `trivy`, `grype`, `syft`, `osv-scanner`, `gitleaks`, `trufflehog`, `hadolint`, `dockle`, `checkov`, `tflint`, `kubescape`, `kube-linter`, `semgrep`, `nuclei`, `vale`, `spectral`, `scancode`, `scorecard`. The shim resolves tool→image via `Config.ImageFor()` walking `UpstreamTools → ImageOverrides → default Image`. Operators can disable the upstream tier with `WOLF_SCANNERS_DISABLE_UPSTREAM=1` to force everything through wolf-built images. | **Why this evolved:** an all-bundled v1 image was 6–8 GB; a 4-image split shrank the typical user's pull but still had every arm64 release pin to maintain; routing the well-supported tools to their upstream-published images shrinks our default image to under 1 GB and eliminates the upstream-version-pin maintenance burden for those tools. |
| D4 | **Container-only; no host fallback.** | Reproducibility is the whole point. A silent host fallback would let two users get different findings — defeats the goal. Devs iterate on tool invocations via `make dev-scanners` (interactive shell into the image). |
| D5 | **Scope: scanners only.** Fix engine stays on host for v1. | Smaller blast radius, cleaner spec, no secrets-in-image questions in v1. v2 will containerize the fix engine in `wolf-fixer`. |

---

## 4. High-Level Architecture

```
┌──────────────────────────────────────────────────────────────────────────┐
│  Host                                                                    │
│                                                                          │
│   ┌──────────────────────┐         ┌─────────────────────────────────┐   │
│   │ wolf-slim container  │         │ wolf-scanners container (1/tool)│   │
│   │                      │         │  short-lived, --rm, --user uid  │   │
│   │ - Go binary          │ docker  │                                 │   │
│   │ - Next.js UI         │ run     │  /scan ── (ro) ── host repo     │   │
│   │ - SQLite DB volume   │ ───────▶│  /out  ── (rw) ── tmpdir        │   │
│   │ - docker.sock mount  │         │  env: HOME=/tmp, NO_COLOR=1     │   │
│   └─────────┬────────────┘         │                                 │   │
│             │                      │  exec: bandit -r /scan ... ▶ JSON│  │
│             │ stdout/stderr piped  │                                 │   │
│             ◀──────────────────────│  exit                           │   │
│             │                      └─────────────────────────────────┘   │
│             │                                                            │
│   ┌─────────┴────────────┐                                               │
│   │ /var/run/docker.sock │  ← mounted into wolf-slim (DooD)              │
│   └──────────────────────┘                                               │
│                                                                          │
│   ┌──────────────────────┐                                               │
│   │ host filesystem      │                                               │
│   │  /home/me/myrepo  ──┼─── bind-mounted into scanners as /scan (ro)   │
│   └──────────────────────┘                                               │
└──────────────────────────────────────────────────────────────────────────┘
```

Lifecycle for one scan:

1. Operator: `wolf scan --repo /home/me/myrepo` (via UI or CLI).
2. Wolf-slim: detector picks N tools.
3. Wolf-slim: availability check — confirms `wolf-scanners:<wolf-version>` is present locally (pull if missing per policy).
4. Wolf-slim: opens an errgroup with `Concurrency` slots. For each selected tool, calls the container shim:

   ```
   docker run --rm --user 1000:1000 \
     -v /home/me/myrepo:/scan:ro \
     -v /tmp/wolf-scan-<id>/out:/out \
     --network bridge \
     --memory 2g --cpus 1.5 \
     wolf-scanners:1.0 \
     bandit -r /scan -f json --exit-zero
   ```

5. Scanner container: tool runs, writes JSON to stdout, exits.
6. Wolf-slim: captures stdout (existing `cmd.Output()` path), pipes stderr line-by-line to `OnToolOutput` (existing path).
7. Plugin: parses JSON, emits `[]models.Finding` with paths normalized (`/scan/foo.py` → `foo.py`).
8. Runner: dedupes, fingerprints, persists.

---

## 5. Detailed Design

### 5.1 The scanner images — three-tier hybrid

**Decision recap** (D3): three tiers, each optimized for its tools.

#### Tier 1 — Upstream-official images (no wolf rebuild)

For the tools where the maintainer publishes a multi-arch image we trust, the wolf shim routes invocations directly to their published image. We pin the version in `versions.env` and map the tool → image in `internal/plugin/container/buckets.go`'s `DefaultUpstreamTools()`:

| Tool | Upstream image | Notes |
|---|---|---|
| trivy | `aquasec/trivy:0.57.0` | multi-arch, maintainer-built |
| grype | `anchore/grype:v0.84.0` | multi-arch |
| syft | `anchore/syft:v1.17.0` | multi-arch |
| osv-scanner | `ghcr.io/google/osv-scanner:v1.9.1` | |
| gitleaks | `zricethezav/gitleaks:v8.21.2` | |
| trufflehog | `trufflesecurity/trufflehog:3.83.5` | |
| hadolint | `hadolint/hadolint:v2.12.0-alpine` | |
| dockle | `goodwithtech/dockle:v0.4.14` | |
| checkov | `bridgecrew/checkov:3.2.297` | |
| tflint | `ghcr.io/terraform-linters/tflint:v0.54.0` | |
| kubescape | `quay.io/kubescape/kubescape-cli:v3.0.22` | |
| kube-linter | `stackrox/kube-linter:v0.7.1` | |
| semgrep | `semgrep/semgrep:1.92.0` | |
| nuclei | `projectdiscovery/nuclei:v3.3.5` | |
| vale | `jdkato/vale:v3.9.1` | |
| spectral | `stoplight/spectral:6.13.1` | |
| scancode | `ghcr.io/nexb/scancode-toolkit:v32.3.0` | avoids the pyicu/libicu-dev build complexity |
| scorecard | `gcr.io/openssf/scorecard:v5.0.0` | OpenSSF repo-hygiene |
| renovate | `ghcr.io/renovatebot/renovate:39.55.0` | dry-run / detect-only; flags outdated + vulnerable deps across many ecosystems (Helm, GitHub Actions, Dockerfile base images, Terraform modules — coverage trivy/grype/osv don't have) |
| kics | `checkmarx/kics:v2.1.3` | Multi-format IaC SAST (Terraform, K8s, Dockerfile, CloudFormation, Ansible, Helm, ARM, OpenAPI, Pulumi). ~3k rules. |
| conftest | `openpolicyagent/conftest:v0.56.0` | Policy-as-code (OPA Rego) for any YAML/JSON/HCL/Dockerfile. |
| pluto | `us-docker.pkg.dev/fairwinds-ops/oss/pluto:v5.20.4` | Deprecated/removed Kubernetes API detection. |
| detekt | `detekt/detekt:v1.23.7` | Kotlin static analyzer. |
| bearer | `bearer/bearer:1.49.0` | PII / data-flow / privacy scanner (GDPR/HIPAA). |

These are pulled lazily on first invocation (or eagerly via `wolf pull scanners`). Operators opt out with `WOLF_SCANNERS_DISABLE_UPSTREAM=1`.

#### Tier 2 — Wolf-built default image (`wolf-scanners`)

Carries the tools that DON'T have a maintained upstream image — mostly small pure-language tools where installing from pip/npm/go/gem is simpler than depending on a community-maintained image:

| Tool | Install |
|---|---|
| bandit, ruff, mypy, pip-audit, radon, vulture | pip |
| detect-secrets, sqlfluff | pip (pure-Python core) |
| gosec, staticcheck, govulncheck | go install (Go 1.23 toolchain pulled at build time) |
| eslint | npm -g |
| brakeman, rubocop | gem |
| phpstan | composer phar |
| swiftlint | github release (best-effort; not all arches) |
| cppcheck, shellcheck | apt |

Estimated size: **~600–900 MB** on amd64. Always pulled.

#### Tier 3 — Wolf-built bucket images

Heavy toolchains that don't make sense in the default image and don't have a clean upstream:

| Image | Tools | Approx size | Pulled when |
|---|---|---|---|
| `wolf-scanners-jvm` | infer, pmd + OpenJDK | ~2 GB | when a Java/Kotlin/C/C++ scan requests them |
| `wolf-scanners-rust` | clippy + rust toolchain | ~1.2 GB | when a Rust scan runs clippy |
| `wolf-scanners-codeql` | codeql + query packs | ~700 MB | only when `codeql` is explicitly enabled (license-gated) |

#### Wolf's tool→image resolution

The shim's `Config.ImageFor(toolName)` walks the lookup chain:

1. **`UpstreamTools[tool]`** — upstream image, no wolf-tool-entry; shim drops the tool-name arg and (if specified) emits `--entrypoint`.
2. **`ImageOverrides[tool]`** — our bucket image with wolf-tool-entry semantics; shim invokes as `docker run <image> <tool> <args...>`.
3. **default `Image`** — the wolf-built default image.

`Config.UpstreamSpec(tool)` returns the optional spec used at runtime to decide whether the shim invokes via wolf-tool-entry or treats the image as a direct entrypoint.

Operators may override or extend either map via wolf.yaml's `scan.container.image_overrides` and `scan.container.upstream_tools`.

### 5.1.1 The legacy `wolf-scanners` image content

The default image's content list (from the table above):

**Repository layout (new):**

```
scanners/
  Dockerfile              # multi-stage; final stage is the runtime image
  versions.env            # source-of-truth pinned versions for every tool
  install/                # per-tool install scripts (composable, sourced from Dockerfile)
    bandit.sh
    gosec.sh
    semgrep.sh
    ...
  smoke-test.sh           # runs `--version` on every installed tool; fails if missing
  README.md               # how to add a new tool to the image
```

**Image guarantees:**

| Property | Value |
|---|---|
| Base | `debian:12-slim` (need glibc for several tools; alpine struggles with semgrep/codeql) |
| Default user | `wolf:wolf` (uid 1000, gid 1000) — overridable via `--user` at runtime |
| Default workdir | `/scan` |
| Default `CMD` | none (must pass a tool name explicitly) |
| Default `ENTRYPOINT` | `/usr/local/bin/wolf-tool-entry` — see §5.4 |
| HEALTHCHECK | none (containers are short-lived) |
| Labels | `org.opencontainers.image.{title,version,source,revision}` |

**Tool inventory (one row per tool, version source-of-truth in `scanners/versions.env`):**

| Tool | Version (initial) | Install via | Image-size delta |
|---|---|---|---|
| semgrep | pinned | pip | 400 MB |
| trivy | pinned | github release tar | 120 MB |
| gitleaks | pinned | github release tar | 30 MB |
| truffleHog | pinned | github release tar | 80 MB |
| detect-secrets | pinned | pip | 40 MB |
| bandit | pinned | pip | 20 MB |
| ruff | pinned | pip | 25 MB |
| mypy | pinned | pip | 30 MB |
| pip-audit | pinned | pip | 15 MB |
| radon | pinned | pip | 10 MB |
| vulture | pinned | pip | 8 MB |
| gosec | pinned | go install | 25 MB |
| staticcheck | pinned | go install | 30 MB |
| govulncheck | pinned | go install | 18 MB |
| eslint | pinned | npm -g | 300 MB |
| npm-audit | (bundled with npm) | — | 0 (npm is base) |
| clippy | pinned | rustup component | 1.2 GB |
| infer | pinned | github release tar | 1.5 GB |
| cppcheck | pinned | apt | 30 MB |
| pmd | pinned | github release tar | 80 MB |
| brakeman | pinned | gem | 30 MB |
| rubocop | pinned | gem | 40 MB |
| swiftlint | pinned | github release tar | 60 MB |
| phpstan | pinned | composer | 50 MB |
| hadolint | pinned | github release tar | 6 MB |
| dockle | pinned | github release tar | 20 MB |
| checkov | pinned | pip | 200 MB |
| tflint | pinned | github release tar | 15 MB |
| kubescape | pinned | github release tar | 80 MB |
| kube-linter | pinned | github release tar | 50 MB |
| syft | pinned | github release tar | 50 MB |
| grype | pinned | github release tar | 40 MB |
| osv-scanner | pinned | github release tar | 60 MB |
| scancode | pinned | pip | 250 MB |
| spectral | pinned | npm -g | 80 MB |
| vale | pinned | github release tar | 15 MB |
| nuclei | pinned | github release tar | 80 MB |
| shellcheck | pinned | apt | 8 MB |
| sqlfluff | pinned | pip | 60 MB |
| codeql | pinned | github release zip | 600 MB |

**Estimated total: ~6.2 GB.** Layer caching makes incremental rebuilds (e.g., bump one tool) fast.

**Vuln-DB strategy.** Several tools ship a vuln database that updates over time (trivy, grype, govulncheck, osv-scanner, semgrep registry). Policy:

- **Build-time bake.** During image build, run `trivy image --download-db-only`, `grype db update`, `govulncheck` (no-op for DB; uses online query at scan time), `semgrep --download` for the default ruleset. These caches are committed to the image so the first scan after pull doesn't pay the DB-download tax.
- **Runtime refresh.** Optional. Controlled by env var `WOLF_SCANNERS_DB_REFRESH={never|once|each}` (default: `never`). When set to `once`, wolf-slim runs `docker run --rm wolf-scanners:tag /usr/local/bin/db-refresh` at startup to update vuln DBs in a named volume mounted into every scanner run. When `each`, the refresh runs before every scan invocation (slow; not recommended).
- **Egress kill switch.** If `WOLF_SCANNERS_NETWORK=none`, scanner containers run with `--network none`. Tools that require network (trivy DB updates, semgrep registry pull, nuclei DAST) degrade as documented per tool.

### 5.2 The `wolf-slim` image updates

`Dockerfile` (existing) changes:

```diff
  FROM alpine:3.20 AS runtime
  ...
- RUN apk add --no-cache ca-certificates tzdata \
+ RUN apk add --no-cache ca-certificates tzdata docker-cli \
      && addgroup -S wolf \
      && adduser -S wolf -G wolf
  ...
```

We **only need the docker CLI**, not the docker daemon. The CLI talks to the host daemon via the mounted socket.

`docker-compose.yml` (existing) changes:

```diff
   wolf:
     build: ...
     ports:
       - "${WOLF_PORT:-8778}:8778"
     volumes:
       - wolf-data:/home/wolf/.wolf
+      - /var/run/docker.sock:/var/run/docker.sock:ro
+      - ${WOLF_REPOS_ROOT:-./repos}:/repos:ro
     environment:
       ...
+      - WOLF_SCANNERS_IMAGE=${WOLF_SCANNERS_IMAGE:-ghcr.io/alphabravocompany/wolf-scanners:${WOLF_VERSION:-dev}}
+      - WOLF_SCANNERS_PULL_POLICY=${WOLF_SCANNERS_PULL_POLICY:-IfNotPresent}
+      - WOLF_SCANNERS_NETWORK=${WOLF_SCANNERS_NETWORK:-bridge}
+      - WOLF_HOST_REPOS_ROOT=${WOLF_REPOS_ROOT:-./repos}
```

The `WOLF_HOST_REPOS_ROOT` env tells wolf-slim what the host path of `/repos` is, because when wolf-slim spawns a scanner container, the `-v` flag must use a **host path** (the docker daemon resolves bind mounts against the host filesystem, not against wolf-slim's filesystem). This is the well-known DooD bind-mount gotcha and is called out in §6.3.

**Note for non-compose deployments.** A user running wolf-slim with raw `docker run` must pass both `-v /var/run/docker.sock:/var/run/docker.sock` and `-v $HOST_REPOS_ROOT:/repos:ro` with `WOLF_HOST_REPOS_ROOT=$HOST_REPOS_ROOT` set. We document this prominently.

### 5.3 The invocation shim — `internal/plugin/container/`

**New package, new file: `internal/plugin/container/runner.go`**

```go
// Package container provides a docker-backed replacement for exec.CommandContext
// that runs a tool inside the wolf-scanners image.
package container

import (
    "context"
    "fmt"
    "io"
    "os"
    "os/exec"
    "strconv"
    "strings"
    "syscall"
    "time"
)

// Config holds runtime configuration for the container backend.
// Populated from wolf.yaml + env at startup; passed to plugins through
// models.ExecuteOpts (new field: ContainerCfg *container.Config).
type Config struct {
    Image       string        // e.g. "ghcr.io/alphabravocompany/wolf-scanners:1.0"
    PullPolicy  PullPolicy    // IfNotPresent | Always | Never
    Network     string        // "bridge" (default) | "none" | "host" (discouraged)
    UID         int           // os.Getuid() resolved once at startup
    GID         int           // os.Getgid() resolved once at startup
    HostReposRoot string      // host path that corresponds to /repos inside wolf-slim
    InContainerReposRoot string // wolf-slim's view, default "/repos"
    Memory      string        // "2g" — passed to --memory
    CPUs        string        // "1.5" — passed to --cpus
    DBVolume    string        // optional named volume for vuln DBs
}

type PullPolicy int
const (
    PullIfNotPresent PullPolicy = iota
    PullAlways
    PullNever
)

// CommandContext returns an *exec.Cmd whose Run/Output executes the requested
// tool inside the wolf-scanners image with the given repo mounted at /scan (ro).
//
//   cfg     — populated container config
//   repoDir — host path of the repo being scanned. Must be inside or equal to
//             cfg.HostReposRoot (we'll bind /repos as a single mount).
//   tool    — binary name inside the image (e.g. "bandit", "gosec")
//   args    — args to pass to the tool, already expressed in CONTAINER paths
//             (i.e. "/scan/foo" not the host path)
//
// The returned *exec.Cmd has been wired so that ctx cancellation triggers
// `docker kill <container-name>`, equivalent to the process-group kill that
// internal/plugin/command.go provides for host execs.
func CommandContext(ctx context.Context, cfg *Config, repoDir, tool string, args ...string) *exec.Cmd {
    name := fmt.Sprintf("wolf-scan-%s-%d", tool, time.Now().UnixNano())

    // Translate repoDir to the host-equivalent path that the docker daemon
    // can bind-mount. We assume repoDir is already a host path supplied by
    // the caller (the API layer maps user input → host path).
    dockerArgs := []string{
        "run", "--rm",
        "--name", name,
        "--user", fmt.Sprintf("%d:%d", cfg.UID, cfg.GID),
        "--read-only", // root filesystem ro; /tmp is writable via tmpfs below
        "--tmpfs", "/tmp:rw,size=512m,mode=1777",
        "-v", fmt.Sprintf("%s:/scan:ro", repoDir),
        "--workdir", "/scan",
        "--network", cfg.Network,
    }
    if cfg.Memory != "" {
        dockerArgs = append(dockerArgs, "--memory", cfg.Memory)
    }
    if cfg.CPUs != "" {
        dockerArgs = append(dockerArgs, "--cpus", cfg.CPUs)
    }
    if cfg.DBVolume != "" {
        dockerArgs = append(dockerArgs, "-v", fmt.Sprintf("%s:/var/lib/wolf-db", cfg.DBVolume))
    }
    dockerArgs = append(dockerArgs, cfg.Image, tool)
    dockerArgs = append(dockerArgs, args...)

    cmd := exec.CommandContext(ctx, "docker", dockerArgs...)

    // Override Cancel so context cancellation hits the running container,
    // not just the host-side docker CLI process. `docker kill` is the only
    // signal that reliably stops the tool inside.
    cmd.Cancel = func() error {
        kill := exec.Command("docker", "kill", name)
        _ = kill.Run() // best effort
        if cmd.Process != nil {
            return syscall.Kill(cmd.Process.Pid, syscall.SIGTERM)
        }
        return nil
    }
    return cmd
}

// HasFilesWithExtension is a CONTAINER-side equivalent of
// plugin.HasFilesWithExtension. Walks the repo via the host process (we already
// have host path) — there's no need to enter the container just to check file
// existence. Kept for API parity.
func HasFilesWithExtension(repoDir, ext string) bool { ... }
```

**New file: `internal/plugin/container/translate.go`**

```go
package container

import "strings"

// NormalizePath converts a container-side path (typically "/scan/foo/bar.py")
// to a repo-relative path ("foo/bar.py"). Returns the input unchanged if it
// doesn't start with /scan/.
func NormalizePath(p string) string {
    const prefix = "/scan/"
    if strings.HasPrefix(p, prefix) {
        return strings.TrimPrefix(p, prefix)
    }
    if p == "/scan" {
        return ""
    }
    return p
}
```

**New file: `internal/plugin/container/probe.go`**

```go
package container

// EnsureImage confirms cfg.Image is available locally. Behavior is controlled
// by cfg.PullPolicy:
//   IfNotPresent: pull if absent, no-op if present
//   Always:       always pull (slow on each startup; useful for "dev" tags)
//   Never:        error if absent
// Returns an error with operator-friendly diagnostics if the image cannot be
// made ready.
func EnsureImage(ctx context.Context, cfg *Config) error { ... }

// ImageReady is a cheap check used by plugin CheckAvailable():
//   `docker image inspect <image>` exit code 0 → true, else false.
// Cached for the lifetime of the wolf process; refreshed if EnsureImage runs.
func ImageReady(cfg *Config) bool { ... }
```

**Existing `internal/plugin/command.go` is preserved unchanged** for the (deferred) fix-engine path. We are deliberately keeping the host-exec helper for non-scanner code paths.

### 5.4 The image entrypoint

Inside `wolf-scanners`, `ENTRYPOINT ["/usr/local/bin/wolf-tool-entry"]` runs:

```bash
#!/usr/bin/env bash
# Minimal entrypoint. Exists to:
#   1. Normalize the environment (HOME, NO_COLOR, PYTHONDONTWRITEBYTECODE).
#   2. Validate the requested tool exists; fail with a clear message if not.
#   3. Forward signals (default `exec`-based forwarding).
set -euo pipefail

export HOME=/tmp
export NO_COLOR=1
export PYTHONDONTWRITEBYTECODE=1
export PYTHONUNBUFFERED=1

tool="${1:-}"
if [[ -z "$tool" ]]; then
    echo "wolf-scanners: no tool specified" >&2
    exit 64
fi
shift

if ! command -v "$tool" >/dev/null 2>&1; then
    echo "wolf-scanners: tool '$tool' not present in image" >&2
    exit 127
fi

exec "$tool" "$@"
```

Exit codes 64 (no tool) and 127 (tool missing) are recognized by `internal/plugin/container/runner.go` and surfaced as actionable `ToolDiagnostic` entries in `internal/plugin/diagnostic.go`.

### 5.5 Plugin migration pattern

Per plugin, the diff is small. Worked example — `plugins/python/bandit.go`:

**Before:**

```go
func (p *BanditPlugin) CheckAvailable() bool {
    _, err := exec.LookPath("bandit")
    return err == nil
}

func (p *BanditPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
    if !plugin.HasFilesWithExtension(opts.RepoPath, "py") { ... }
    if opts.Timeout > 0 { ... }
    cmd := plugin.CommandContext(ctx, "bandit", "-r", opts.RepoPath, "-f", "json", "--exit-zero")
    out, err := cmd.Output()
    ...
    return parseBanditOutput(out)
}
```

**After:**

```go
func (p *BanditPlugin) CheckAvailable() bool {
    return container.ImageReady(plugin.ContainerConfig())
}

func (p *BanditPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
    if !plugin.HasFilesWithExtension(opts.RepoPath, "py") { ... }
    if opts.Timeout > 0 { ... }
    cmd := container.CommandContext(ctx, opts.ContainerCfg, opts.RepoPath,
        "bandit", "-r", "/scan", "-f", "json", "--exit-zero")
    out, err := cmd.Output()
    ...
    findings, perr := parseBanditOutput(out)
    if perr != nil { return nil, perr }
    for i := range findings {
        findings[i].FilePath = container.NormalizePath(findings[i].FilePath)
    }
    return findings, nil
}
```

Key invariants:

- The tool's CLI args change from referencing `opts.RepoPath` (host) to referencing `/scan` (container).
- After parsing, paths in findings are normalized via `container.NormalizePath`.
- `CheckAvailable` is now uniform across all plugins. We may extract this to a default method on a base struct in a later pass; v1 just inlines it.

**Plugin-specific quirks** worth flagging up front:

| Plugin | Note |
|---|---|
| `gosec` | Uses `cmd.Dir = goDir` where `goDir` is the host path of the dir containing `go.mod`. With the shim, we need to **(a) find the relative path** of that dir inside the repo and **(b) pass `--workdir /scan/<reldir>`** instead of `cmd.Dir`. |
| `govulncheck` | Requires network egress to query `vuln.go.dev`. Documented as a tool that fails under `WOLF_SCANNERS_NETWORK=none`. |
| `eslint` | Resolves `eslint.config.js` from the repo and may require `node_modules` to load shareable configs. We must verify `--resolve-plugins-relative-to /scan` is honored, or document the limitation. |
| `nuclei` | DAST tool — does not scan the repo at all. Takes `opts.Target` (URL) and the repo mount is irrelevant. Shim still works (mounts /scan as empty noise) but we may want a `container.CommandContextNoRepo` variant for cleanliness. |
| `codeql` | Builds a database (writes to disk). Needs writable workspace. Use the `/tmp` tmpfs (512 MB) or, for large repos, mount a named volume at `/codeql-cache` and pass via `--codeql-cache-dir`. |
| `infer` | Java/C++ requires the repo to build. Needs the relevant JDK/maven/gradle inside the image. Already covered by the image inventory but the build command is repo-specific — document that users may need to set `WOLF_INFER_BUILD_CMD`. |
| `trivy fs` / `grype` | Read vuln DB from `/var/lib/wolf-db` named volume when `WOLF_SCANNERS_DB_REFRESH` is non-`never`. |

### 5.6 Configuration changes

Additions to `configs/wolf.yaml`:

```yaml
scan:
  concurrency: 8
  timeout: "30m"
  default_preset: "standard"
  exclude_patterns: [...]

  # NEW
  container:
    image: "ghcr.io/alphabravocompany/wolf-scanners:${WOLF_VERSION}"
    pull_policy: "IfNotPresent"   # IfNotPresent | Always | Never
    network: "bridge"             # bridge | none | host (discouraged)
    memory: "2g"
    cpus: "1.5"
    db:
      refresh: "never"            # never | once | each
      volume: "wolf-scanners-db"
    host_repos_root: "/repos"     # what wolf-slim sees that maps to the host bind
```

Env-var overrides (12-factor): every field is overridable via `WOLF_SCAN_CONTAINER_<UPPER_SNAKE>`. The Go config loader gains a new struct binding under `internal/setup/config.go`.

### 5.7 Streaming, output capture, cancellation

| Concern | How it works today (host) | How it works after (container) |
|---|---|---|
| Capture stdout JSON | `cmd.Output()` returns combined stdout bytes | Same — `docker run` forwards container stdout to the CLI's stdout, which `exec.Cmd` captures unchanged |
| Stream stderr line-by-line to UI | `cmd.StderrPipe()` + scanner → `opts.OnOutput(line)` | Same — `docker run` forwards container stderr to the CLI's stderr |
| Cancel mid-scan | Context cancel → `cmd.Cancel` → `kill -SIGKILL <pgid>` | Context cancel → `cmd.Cancel` → `docker kill <name>` (kills the container, which kills the tool) + SIGTERM to the local docker CLI |
| Exit code propagation | `cmd.Wait()` returns `*exec.ExitError` with code | Same — docker CLI exits with the container's exit code (passthrough) |

Net effect: from the caller's perspective in each plugin, the `*exec.Cmd` returned by the shim behaves identically to the one returned by `plugin.CommandContext`. **This is the whole point of the shim's design.**

### 5.8 Image lifecycle: pull, tag, distribute

**Registry.** Initial home: `ghcr.io/alphabravocompany/wolf-scanners`. Public on first release; we can flip to private if licensing concerns about bundled tools arise (see §10).

**Tagging policy:**

- **Per release:** `wolf-scanners:1.0.0`, `wolf-scanners:1.0`, `wolf-scanners:1` — semver fanout.
- **Per commit on main:** `wolf-scanners:main`, `wolf-scanners:main-<short-sha>`.
- **Per PR (CI only):** `wolf-scanners:pr-<num>` (cleaned up on close).
- **`latest`** intentionally **not used** — wolf's pull policy is version-pinned by default.

**Pull behavior:**

- At wolf-slim startup, if `WOLF_SCANNERS_PULL_POLICY=IfNotPresent` (default), wolf checks `docker image inspect $WOLF_SCANNERS_IMAGE` and pulls if missing.
- The pull blocks startup. On a slow link this is ~5–10 minutes for a 6 GB image; we log progress and surface it in the UI as "Pulling scanners image…".
- `WOLF_SCANNERS_PULL_POLICY=Always` is intended for dev tags only.
- `WOLF_SCANNERS_PULL_POLICY=Never` lets air-gapped operators pre-load the image and confirm wolf won't reach out.

**Air-gapped install path:** documented in README. `docker save wolf-scanners:1.0 | gzip > scanners.tgz` on a connected box; `gunzip < scanners.tgz | docker load` on the target.

### 5.9 Build pipeline (CI)

New GitHub Actions workflow: `.github/workflows/scanners-image.yml`.

```yaml
name: wolf-scanners image
on:
  push:
    paths: ["scanners/**", ".github/workflows/scanners-image.yml"]
  release:
    types: [published]
  workflow_dispatch:

jobs:
  build:
    runs-on: ubuntu-latest-large  # 6 GB image build is memory-hungry
    permissions:
      contents: read
      packages: write
    steps:
      - uses: actions/checkout@v4
      - uses: docker/setup-buildx-action@v3
      - uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}
      - name: Build & smoke-test
        run: |
          docker build -t wolf-scanners:test scanners/
          docker run --rm wolf-scanners:test /usr/local/bin/smoke-test.sh
      - name: Compute tags
        id: tags
        run: |
          # See docs/scanners-image-tagging.md
          echo "tags=..." >> $GITHUB_OUTPUT
      - uses: docker/build-push-action@v5
        with:
          context: scanners/
          push: true
          tags: ${{ steps.tags.outputs.tags }}
          cache-from: type=gha
          cache-to: type=gha,mode=max
```

`scanners/smoke-test.sh` runs `<tool> --version` for every tool in the inventory and fails on any non-zero exit. This is the single source of truth for "the image is healthy."

---

## 6. Migration & Compatibility

### 6.1 Backward compatibility posture

This is a **breaking change** by design:

- Operators with host-installed tools must adopt Docker.
- Finding fingerprints invalidate (paths change form). One-time event.
- The `scan.execution` config knob does not exist; container is the only mode.

We will tag this release **wolf 2.0**.

### 6.2 Phased rollout

Five phases. Each phase ends at a hard pass gate (§7). Phases land as separate PRs; phases 1–4 can co-exist on `main` with host-exec still working (because plugins migrate one-by-one and the old `plugin.CommandContext` stays in tree).

**Phase 0 — image foundation.** Build `wolf-scanners:dev` locally; smoke-test passes. No Go code changes.

**Phase 1 — shim & probe.** Add `internal/plugin/container/`. Unit tests, integration test against `wolf-scanners:dev`. No plugin uses it yet. `plugin.CommandContext` (host) unchanged.

**Phase 2 — pilot plugin (bandit).** Migrate Bandit. Add a regression test comparing findings against a fixture repo using both backends; assert equivalent finding set (modulo path normalization).

**Phase 3 — bulk plugin migration.** Migrate plugins in language-bucket batches. Each batch is one PR. Each batch must pass the per-plugin test suite AND the cross-batch fixture regression.

**Phase 4 — host-exec removal.** Delete `exec.LookPath` calls from all plugins (now redundant), remove `plugin.CommandContext` (it's only used by fix-engine which we deferred — but fix-engine has its own helper, so we can delete this one). Update README, docker-compose.yml, Dockerfile, configs.

**Phase 5 — release.** Build wolf-scanners:2.0, tag wolf 2.0, publish release notes including the fingerprint-invalidation guidance.

### 6.3 The DooD bind-mount gotcha

When wolf-slim runs inside a container and spawns `docker run -v $PATH:/scan`, **the docker daemon resolves `$PATH` against the HOST's filesystem, not wolf-slim's view**. So if wolf-slim has the repo at `/repos/myrepo` (because docker-compose mounted `./repos` → `/repos`), it must spawn the scanner with `-v /actual/host/path/to/repos/myrepo:/scan`, not `-v /repos/myrepo:/scan`.

The shim handles this via `cfg.HostReposRoot` (the host path) + `cfg.InContainerReposRoot` (wolf-slim's path). The API layer accepts repo paths from the user, validates they live under `InContainerReposRoot`, and computes the host equivalent before passing to the shim:

```go
hostPath := strings.Replace(repoPath, cfg.InContainerReposRoot, cfg.HostReposRoot, 1)
```

This is the most common DooD bug. We must have a regression test for it (§7 P1 gate).

### 6.4 Fingerprint invalidation

`internal/scan/runner/runner.go:82` defines:

```go
func Fingerprint(toolName, ruleID, title, filePath string) string {
    identifier := ruleID
    if identifier == "" { identifier = title }
    input := toolName + ":" + identifier + ":" + filePath
    ...
}
```

Old paths: `/Users/mj/myrepo/foo/bar.py`.
New paths: `foo/bar.py`.

Fingerprints differ. On upgrade, every existing finding looks "new" to the dedup/triage layer, and historical metrics will discontinuity.

**Mitigation strategy:**

1. **Document the discontinuity** in release notes; recommend operators either accept it or wipe the findings table.
2. **Provide a one-shot migration command:** `wolf migrate fingerprints --strip-prefix=<old-host-root>` rewrites paths in the DB to repo-relative form and recomputes fingerprints in place. Idempotent.
3. **Tag the migration** in the DB schema so wolf detects "this DB was migrated" and shows operators a banner if they downgrade.

### 6.5 Inner-loop dev workflow

Developers iterating on plugin parsers can run scanners inside the image without going through wolf:

```
make dev-scanners        # docker run --rm -it -v $PWD:/scan wolf-scanners:dev bash
                          # → inside: bandit -r /scan -f json | jq
```

We add this Make target in Phase 0.

---

## 7. Tasks & Pass Gates

Each task is a unit of work. Each phase ends at a pass gate that must be **demonstrably satisfied** — not just claimed — before the next phase starts.

### Phase 0 — Image foundation

| ID | Task | Owner | Notes |
|---|---|---|---|
| P0.1 | Create `scanners/Dockerfile` (multi-stage, debian:12-slim base) | | |
| P0.2 | Create `scanners/versions.env` with pinned versions for all ~40 tools | | Source of truth; referenced by all install scripts |
| P0.3 | Create `scanners/install/<tool>.sh` for each tool group (python, go, node, rust, java, ruby, swift, php, native, downloads) | | One script per language bucket; tools listed in §5.1 |
| P0.4 | Create `scanners/smoke-test.sh` — runs `<tool> --version` on each tool, fails on any missing | | This script is the contract |
| P0.5 | Create `/usr/local/bin/wolf-tool-entry` (see §5.4) | | |
| P0.6 | Add `Makefile` targets: `scanners-build`, `scanners-smoke`, `dev-scanners` | | `dev-scanners` opens interactive shell |
| P0.7 | Pin vuln DB caches at build time (trivy, grype, semgrep registry) | | See §5.1 vuln-DB strategy |
| P0.8 | Add `.dockerignore` in `scanners/` to avoid sending the full repo as build context | | |

**Pass gate P0 — must demonstrate:**

- `make scanners-build` produces `wolf-scanners:dev` locally, size 5–8 GB.
- `make scanners-smoke` exits 0; output lists `<tool>: <version>` for every tool in `scanners/versions.env`.
- `docker run --rm wolf-scanners:dev bandit --version` works.
- `docker run --rm -v $PWD:/scan:ro wolf-scanners:dev bandit -r /scan -f json` produces parseable JSON when run against a fixture repo with a known Python finding.

### Phase 1 — Shim & probe

| ID | Task | Owner | Notes |
|---|---|---|---|
| P1.1 | Create `internal/plugin/container/runner.go` — `Config`, `CommandContext` (see §5.3) | | |
| P1.2 | Create `internal/plugin/container/translate.go` — `NormalizePath` | | |
| P1.3 | Create `internal/plugin/container/probe.go` — `EnsureImage`, `ImageReady` | | |
| P1.4 | Add `ContainerCfg *container.Config` to `models.ExecuteOpts` | | Single config object propagated through the runner |
| P1.5 | Wire config loading from `wolf.yaml` + env (`internal/setup/config.go`) | | |
| P1.6 | Add unit tests for `NormalizePath` (covers `/scan/x` → `x`, `/scan` → `""`, non-prefixed → unchanged) | | |
| P1.7 | Add unit tests for `CommandContext` arg construction (no actual docker exec — table-driven asserts on the command line constructed) | | |
| P1.8 | Add unit tests for `EnsureImage` with mocked docker CLI (use exec-fake pattern) | | |
| P1.9 | Add integration test `TestShim_Bandit_E2E` that requires `wolf-scanners:dev`, runs bandit against `plugins/python/testdata/`, asserts ≥1 finding | | Tagged `//go:build integration`; runs in CI on a job that pre-builds the image |
| P1.10 | Cancellation test: `TestShim_CancelMidScan` starts a long semgrep scan, cancels the context, asserts the container is gone within 2s (poll `docker ps -a --filter name=wolf-scan-*`) | | |
| P1.11 | DooD bind-mount test: `TestShim_HostPathTranslation` confirms `cfg.HostReposRoot` substitution works | | Regression test for §6.3 |

**Pass gate P1 — must demonstrate:**

- `go test ./internal/plugin/container/...` passes.
- `go test -tags=integration ./internal/plugin/container/...` passes against `wolf-scanners:dev`.
- A cancelled scan leaves no orphan `wolf-scan-*` containers (`docker ps -a` clean).
- Image-not-present gives an actionable error (`wolf: scanners image 'foo:bar' not found; run 'wolf pull scanners' or 'docker pull foo:bar'`).

### Phase 2 — Pilot plugin (bandit)

| ID | Task | Owner | Notes |
|---|---|---|---|
| P2.1 | Migrate `plugins/python/bandit.go` to use `container.CommandContext` | | See worked example in §5.5 |
| P2.2 | Apply `NormalizePath` to all `FilePath` fields in returned findings | | |
| P2.3 | Update `plugins/python/bandit_test.go` to test against the shim via an integration build tag (host-exec test stays for now to allow side-by-side comparison) | | |
| P2.4 | Add regression test: `TestBandit_BackendParity` runs Bandit against `plugins/python/testdata/` via both backends and asserts identical finding sets (modulo path form) | | This is the canonical pattern for the bulk migration |

**Pass gate P2 — must demonstrate:**

- Bandit, run via the shim, produces a finding set equivalent to host-exec Bandit (same tool version) on the fixture repo.
- The integration test runs green in CI.
- Documentation example in §5.5 reflects the actual code.

### Phase 3 — Bulk plugin migration

Plugins migrate in batches. Each batch is one PR. Order:

| Batch | Plugins | Risk notes |
|---|---|---|
| 3a Python | bandit (already done in P2), ruff, mypy, pip-audit, radon, vulture | Smallest blast radius; bandit pattern proven |
| 3b Go | gosec, staticcheck, govulncheck | `gosec` has `cmd.Dir` quirk (§5.5); govulncheck needs network |
| 3c JavaScript | eslint, npm-audit | eslint config resolution is repo-dependent; verify shareable configs work |
| 3d Cross-language SAST | semgrep, codeql | codeql needs writable workspace; semgrep needs network for registry rules |
| 3e Cross-language SCA | trivy, grype, osv-scanner | Vuln-DB volume must work |
| 3f Secrets | gitleaks, trufflehog, detect-secrets | gitleaks needs `.git/` in /scan — verify it's not stripped by `exclude_patterns` |
| 3g Containers | hadolint, dockle, checkov | Hadolint needs `Dockerfile` in /scan |
| 3h Infrastructure | tflint, kubescape, kube-linter | |
| 3i Native | cppcheck, infer | Infer needs JDK/build tools; long-running |
| 3j Other languages | clippy, brakeman, rubocop, swiftlint, phpstan, pmd | |
| 3k DAST/Other | nuclei (target-based), syft, scancode, spectral, vale, shellcheck, sqlfluff | nuclei doesn't read /scan |

| ID | Task | Notes |
|---|---|---|
| P3.<batch> | One PR per batch. For each plugin: (a) swap exec→container, (b) normalize paths, (c) backend-parity test | Use the bandit migration as the template |

**Pass gate P3 — must demonstrate (per batch):**

- All existing unit tests in `plugins/<bucket>/*_test.go` pass against the shim.
- Backend-parity test (host vs container) produces equivalent finding sets for each tool.
- For network-dependent tools, an offline-mode test confirms graceful failure with an actionable error.
- `wolf scan --tools <tool>` works end-to-end via wolf CLI from inside `docker compose up`.

### Phase 4 — Host-exec removal

| ID | Task | Notes |
|---|---|---|
| P4.1 | Delete `exec.LookPath` calls from all migrated plugin files | These are now dead code |
| P4.2 | Remove `plugin.CommandContext` from public API of `internal/plugin/` (it's only called by scanner plugins; fix-engine has its own helper) | Document the removal in CHANGELOG; if fix-engine code paths used it, give them a private helper. |
| P4.3 | Update `Dockerfile` (wolf-slim): add `docker-cli` package | See §5.2 |
| P4.4 | Update `docker-compose.yml`: socket mount, repos mount, env vars | See §5.2 |
| P4.5 | Update `configs/wolf.yaml`: `scan.container.*` section with defaults | See §5.6 |
| P4.6 | Update `internal/setup/config.go` to bind the new config struct | |
| P4.7 | Add startup health check: refuse to start if scanners image is not reachable AND pull policy is `Never` AND no image is present | Fail fast with operator-friendly error |
| P4.8 | Update `wolf setup` to verify docker is reachable and offer to pre-pull the scanners image | |
| P4.9 | Update README to remove all "install tool X" sections and replace with "docker compose up" | |
| P4.10 | Add a `wolf pull scanners` CLI command — thin wrapper around `docker pull` for the configured image | UX nicety |
| P4.11 | Add a `wolf doctor` CLI command — checks docker reachability, image presence, sample tool invocation, mount round-trip | Diagnostics |

**Pass gate P4 — must demonstrate:**

- Fresh checkout, no host tools installed, only Docker present: `docker compose up -d && docker compose exec wolf wolf scan --repo /repos/sample` produces findings.
- `wolf doctor` reports OK on a clean install; reports actionable errors when (a) docker is down, (b) image is missing, (c) repos mount is mis-configured.
- Removing host-installed Bandit between scans changes nothing about wolf's output. (Confirms no fallback path.)

### Phase 5 — Release

| ID | Task | Notes |
|---|---|---|
| P5.1 | CI workflow `.github/workflows/scanners-image.yml` builds and pushes `wolf-scanners` on release | See §5.9 |
| P5.2 | CI workflow that builds wolf-slim now also pulls a matching `wolf-scanners` tag and runs the end-to-end test | |
| P5.3 | Release notes: highlight breaking changes (fingerprint invalidation, docker requirement, removal of host-exec) | |
| P5.4 | Migration guide for fingerprints (§6.4) | Includes the `wolf migrate fingerprints` command |
| P5.5 | Air-gapped install docs | `docker save \| docker load` walkthrough |
| P5.6 | Tag `wolf 2.0.0`, publish `wolf-scanners:2.0.0` + `wolf-scanners:2.0` + `wolf-scanners:2` | |

**Pass gate P5 — must demonstrate:**

- A user starting from `docker compose up` on a fresh VM with only Docker installed can run a scan against a sample repo in under 15 minutes (including image pull).
- The release artifact bundle (wolf-slim image + wolf-scanners image + release notes + migration guide) is published and downloadable from a single page.
- Downgrade test: stop wolf 2.0, start wolf 1.x against the same DB — the migration banner appears and findings are still readable.

---

## 8. Testing Strategy

### 8.1 Test pyramid

| Layer | What | Where | When |
|---|---|---|---|
| Unit | Shim arg construction, path normalization, config binding | `internal/plugin/container/*_test.go` | Every PR; `go test ./...` |
| Plugin parser | Each plugin's output-parsing logic (unchanged from today) | `plugins/<bucket>/*_test.go` | Every PR |
| Integration (per plugin) | Real `docker run` against `wolf-scanners:dev` on fixture repos | `plugins/<bucket>/*_integration_test.go`, build-tagged | Phase 1+; nightly + PRs touching plugins |
| Backend parity | Host-exec vs container-exec produce equivalent findings | `plugins/<bucket>/*_parity_test.go`, build-tagged | Phases 2–3 only; deleted in Phase 4 |
| Cancellation | Mid-scan cancel cleans up containers | `internal/plugin/container/runner_cancel_test.go` | Every PR |
| DooD path translation | Host path substitution works | `internal/plugin/container/runner_dood_test.go` | Every PR |
| End-to-end | `wolf scan` via UI/CLI inside docker-compose | `e2e/` (new) | Nightly + release |

### 8.2 CI matrix

- Plugin parser unit tests: every PR.
- Container-shim unit tests: every PR.
- Integration tests: PRs that touch `scanners/`, `internal/plugin/container/`, or `plugins/`. Pre-builds `wolf-scanners:test` in CI.
- Backend parity: PRs that touch any plugin. Removed in Phase 4.
- End-to-end docker-compose test: nightly.

### 8.3 Fixture repos

`plugins/<bucket>/testdata/` already exists for some plugins. We expand to cover every plugin migrated in Phase 3, with deterministic findings (committed expected-output JSON files updated when the pinned tool version bumps).

---

## 9. Failure Modes & Diagnostics

For each failure mode, wolf must produce an actionable error. Existing `internal/plugin/diagnostic.go` is the right home for these.

| Mode | Symptom | Diagnostic |
|---|---|---|
| Docker daemon down | `docker run` exits with "Cannot connect to the Docker daemon" | `"wolf needs Docker. Start Docker Desktop, or check that the daemon socket is mounted into wolf-slim."` |
| Image missing, pull policy `Never` | `EnsureImage` errors | `"scanners image 'X:Y' not present locally; either pre-pull it (docker pull X:Y) or set WOLF_SCANNERS_PULL_POLICY=IfNotPresent."` |
| Image missing, pull policy `IfNotPresent`, registry unreachable | Pull times out | `"could not pull scanners image 'X:Y' (network: <error>). Check connectivity to ghcr.io, or download the image and`docker load`it (air-gapped install: <link>)."` |
| Bind mount path not on host | `docker run` fails with "no such file or directory" | `"the repo path '/repos/x' inside wolf-slim must correspond to a host path; check WOLF_HOST_REPOS_ROOT and the docker-compose volumes section."` |
| Tool not in image | Entrypoint exits 127 | `"tool 'X' not present in wolf-scanners:Y. Rebuild the image or upgrade wolf — image and wolf versions must match."` |
| OOM | Container killed (exit code 137) | `"tool 'X' was killed (OOM); increase scan.container.memory in wolf.yaml (currently <value>) and retry."` |
| Network required, network=none | Tool fails with DNS errors | `"tool 'X' needs network access; either set WOLF_SCANNERS_NETWORK=bridge (default) or skip this tool with --tools."` |
| Permission denied on /scan | Tool can't read repo | `"the bind mount /scan is read-only as designed. If a tool wrote to /scan (it shouldn't), open an issue; if the host file perms are wrong, fix on the host."` |
| /tmp full | Tool fails writing to /tmp | `"tool 'X' exhausted the 512 MB /tmp tmpfs; increase --tmpfs size in scan.container or report the tool as needing more scratch space."` |

`wolf doctor` (P4.11) runs each of these checks proactively where possible.

---

## 10. Security Considerations

### 10.1 Docker socket mount

Mounting `/var/run/docker.sock` into wolf-slim grants wolf-slim **effective root on the host** — it can launch any container with any mount. We accept this because:

- It's the same posture every CI runner takes (Jenkins, GitLab Runner, Buildkite agent, Argo Workflows).
- Wolf-slim is operator-controlled; it isn't running untrusted code itself.
- The alternative (running wolf-slim as root on the host directly) is strictly worse.

**Hardening posture documented:**

- Mount the socket **read-only** where possible (most docker operations need write — this works only for `inspect`/`ps`-only deployments). v1 mounts read-write.
- Run wolf-slim as a non-root user *inside* the container (already done in current Dockerfile: `USER wolf`). The socket permission on the host typically requires being in the `docker` group; we document this.
- Recommend operators not run wolf-slim with `--privileged` (it doesn't need it).
- Recommend operators apply a Docker auth profile that restricts wolf-slim to spawning containers from `wolf-scanners:*` only, if their environment supports it.

### 10.2 Scanned-code attack surface

The scanner container's posture limits damage from malicious code in the scanned repo:

- `/scan` is **read-only** — no persistence.
- `--user $(id -u)` runs the tool as a non-root user inside the container.
- `--read-only` root filesystem — the only writable space is the 512 MB `/tmp` tmpfs.
- No host filesystem access except `/scan` (read-only) and an optional vuln-DB volume.
- No docker socket inside the scanner container — it can't pivot to spawning more containers.
- Default network is bridged egress — no inbound. We allow operators to flip to `--network none` for paranoid mode.
- Container is `--rm` — no persistent state survives.

**Residual risk**: a scanner tool with a known RCE (e.g., a Semgrep yaml-parsing bug) could be exploited by crafted repo content. The image is `--read-only` and the user is non-root, so the blast radius is the tmpfs and the network egress for the lifetime of the container. We pin tool versions and respond to CVEs by bumping the image.

### 10.3 Tool license obligations

`wolf-scanners` bundles tools with varying licenses (Apache-2, MIT, GPL-3, BSD, proprietary trial in some cases — codeql is the most restrictive). We will:

- Maintain a `scanners/LICENSES.md` listing each tool, its version, and its license URL.
- Confirm redistribution rights for each tool before adding it to a public image.
- For tools with restrictive licenses (codeql), document operator obligations.

This is a v1 must-have. If a license blocks public-image distribution, that tool moves to a separate "extras" image users opt into.

---

## 11. Performance Budget

| Metric | Today (host) | After (container) | Acceptable? |
|---|---|---|---|
| Cold-start scan, ~10 tools on a 5k-LOC Python repo | ~45s | ~50–55s (10 × ~0.7s container startup, parallelized) | Yes |
| Warm-start scan (image cached, DB warm) | ~45s | ~46–48s | Yes |
| First-ever boot (image pull) | n/a | +5–10 min for image pull | One-time; documented |
| Cancellation latency | <1s (SIGKILL pgroup) | <2s (`docker kill` + container teardown) | Yes |
| Memory per scan | ~500 MB across tools | ~500 MB + ~50 MB per concurrent container ≈ 900 MB at concurrency=8 | Yes; configurable |
| Disk (cache) | 0 | ~6 GB (image) + ~500 MB (vuln DBs) | Documented |

If real-world measurements during Phase 3 show >25 % regression on cold-start time, we'll add a long-lived "warm" scanner-container mode (Option C from the brainstorm) as a follow-up. This is **not** in v1.

---

## 12. Open Questions

| # | Question | Owner | Resolution by |
|---|---|---|---|
| OQ1 | Public vs. private registry for wolf-scanners? Affects license review depth. | mj | Phase 0 |
| OQ2 | Does the operator UI need a "pulling image…" progress indicator, or is a CLI log sufficient? | mj | Phase 1 |
| OQ3 | Should we ship an ARM64 image variant alongside AMD64? (Several tools — infer, codeql — have weak ARM support.) | mj | Phase 0 |
| OQ4 | Do we expose a `--scanners-image` flag on `wolf scan` for testing/dev, or only via config? | mj | Phase 4 |
| OQ5 | Compose-less install path — should we ship a `wolf-up` script that runs `docker run` with the right flags so users don't need docker-compose? | mj | Phase 5 |

---

## 13. Future Work (post-v1)

- **v2 — fix engine containerization.** Spec for `wolf-fixer` image with claude/codex/git/gh/glab and writable workspace. Same shim pattern, different config (writable mount, secrets, longer lifetime).
- **Per-language image splits.** If user demand warrants it, split `wolf-scanners` into `wolf-scanners-core` + `wolf-scanners-{python,go,js,...}`. The shim already abstracts which image to use per tool, so this is an image-build change, not a wolf change.
- **Long-lived warm scanner container.** If cold-start latency becomes a problem, add an opt-in `scan.container.mode: warm` that keeps a `wolf-scanners` container running and uses `docker exec` per tool.
- **Kubernetes execution backend.** Replace `docker run` with `kubectl run --rm -i --restart=Never` for wolf deployed on K8s. Same shim, different driver.
- **Rootless / Podman.** The docker CLI is mostly compatible with Podman's CLI; the shim should work with minimal tweaks.
- **OCI distribution spec.** Sign and verify the scanner image with cosign.

---

## 14. Out of Scope

- Anything related to the fix engine (claude/codex/git/gh/glab).
- Anything related to AI providers (anthropic, openai SDKs — those stay in wolf-slim).
- Anything related to repo *cloning* or *fetching*. The shim assumes the caller hands it a host path. How the repo got there is unchanged.
- Anything related to UI redesign. The UI talks to the same APIs; the only UI change is the "scanners image: pulling..." status surfaced from a new endpoint.

---

## 15. Glossary

| Term | Definition |
|---|---|
| **wolf-slim** | The existing wolf container (Go binary + Next.js UI). Renamed conceptually; the image is still `thewolf:tag` in the Dockerfile. |
| **wolf-scanners** | The new fat image containing all ~40 scanner tools. Tagged `wolf-scanners:<wolf-version>`. |
| **DooD** | Docker-out-of-Docker. The pattern where a container has `/var/run/docker.sock` mounted in and uses the host daemon (as opposed to "DinD" — Docker-in-Docker — where the container runs its own daemon). |
| **The shim** | `internal/plugin/container/` package. Provides `CommandContext` as a docker-backed replacement for `plugin.CommandContext`. |
| **Pass gate** | A demonstrable condition that must be true before the next phase begins. Not a self-attestation. |
| **Backend parity** | The property that host-exec and container-exec produce equivalent finding sets on the same input. Verified by parity tests in Phases 2–3, then deleted in Phase 4. |
