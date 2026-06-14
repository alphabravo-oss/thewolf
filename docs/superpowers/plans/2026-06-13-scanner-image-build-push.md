# Scanner Image Build & Push (server-driven, DockerHub-first, UI-managed)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Let an operator rebuild the four wolf-built scanner images and push them to an authenticated DockerHub account directly from the wolf server/UI, with right-sized images and streamed build logs.

**Architecture:** The wolf server runs `docker buildx build` + `docker push` itself (Docker is already a hard dependency for scanning). The `scanners/` build context is `go:embed`-ed into the binary so no repo checkout is needed at runtime. DockerHub credentials live in wolf's existing encrypted secret store as a new `dockerhub_token` type. A new SSE-streamed build subsystem surfaces in Settings → Scanner Images.

**Tech Stack:** Go 1.26 (chi, sqlx, os/exec → docker CLI), `go:embed`, existing SSE broker, React 19 + TanStack + the Nocturne UI system.

---

## Background — what already exists (verified 2026-06-13)

- All four images are **already published on DockerHub `alphabravodevops`** (`wolf-scanners`, `-jvm`, `-rust`, `-codeql`), tags `latest` + `dev`, pushed 2026-05-14 by the `scanners-image.yml` CI workflow. The default is **692 MB compressed**.
- The runtime already pulls from DockerHub (`internal/setup/scanners/scanners.go:73` defaults to `alphabravodevops/wolf-scanners:<tag>`).
- **The only breakage:** the runtime default tag is `2.0.0` (`scanners.go:71`), but CI only ever published `latest`/`dev` — no release cut a versioned tag. That's why the Scanners tab showed `REMOTE: ERROR`. The images aren't missing; the tag is.
- The default image's *unpacked* size (~3 GB) is dominated by a 1.2 GB Go-toolchain layer that `go-tools.sh` keeps on purpose (gosec/govulncheck need `go list`/`go env` at runtime) — but it retains the GOPATH module cache and toolchain docs/tests it doesn't need.
- CI builds multi-arch (amd64+arm64) via buildx + GHA cache. **Server-local builds will be single-arch (the host's platform)** — multi-arch from one host without emulation is slow; CI stays the multi-arch path.

### Confirmed decisions (from the operator)
| # | Decision | Choice |
|---|----------|--------|
| 1 | Where builds run when triggered from UI | **Wolf server builds locally** (buildx + push, streamed logs) |
| 2 | Image scoping | **Keep the current 4 images; just fix the Go bloat** |
| 3 | Registry + auth | **DockerHub primary; PAT stored as a `dockerhub_token` secret in wolf** |
| 4 | UI scope | **Per-image status + Rebuild & push + live build logs** |

---

## Core principle — local build needs no credentials

**Building and *using* images locally never requires DockerHub credentials.** Credentials gate *publishing* only.

- `push=false` → `docker buildx build --load` builds the image and loads it into the **local Docker daemon**. The scanner backend's pull policy is `IfNotPresent`, so the next scan finds the freshly-loaded image present and runs it with **no registry round-trip**.
- `push=true` → `docker login` + `--push` to DockerHub. This is the **only** path that needs the `dockerhub_token` secret.

So a fresh wolf install with zero creds can rebuild every image locally and scan with them; DockerHub is an opt-in *publish* step.

## Definition of Done

1. With **no** DockerHub credentials configured, `POST /api/v1/scanners/images/{variant}/build` with `{push:false}` builds the variant from the embedded context, `--load`s it into the local daemon, and a subsequent scan uses it (pull policy `IfNotPresent`, no pull attempt). Verified end-to-end.
2. `POST /api/v1/scanners/images/{variant}/build` builds the named variant (`default|jvm|rust|codeql`) from the embedded context and streams `docker buildx` output over SSE; `POST /api/v1/scanners/images/build-all` does all four. The request body is `{push: bool}`; `push=false` is the default and requires no credentials.
3. With `push=true`, `docker login` from a `dockerhub_token` secret succeeds, the freshly built image is tagged `:<wolf-version>` **and** `:latest` and pushed to `docker.io/<configured-namespace>/<image>`; the response reports the pushed refs + digests. `push=true` with no `dockerhub_token` secret returns 404 with a clear hint — but `push=false` is unaffected.
4. The default image's Go layer is slimmed: `go-tools.sh` removes `$GOPATH/pkg` (module/build cache) and strips the toolchain's `test/`, `doc/`, `api/`, `misc/` dirs, while gosec + govulncheck still pass a smoke `go list`/`go env` against a sample module. Net: default unpacked size drops by ≥600 MB with no tool regressions (verified by `scanners/smoke-test.sh`).
5. The runtime default tag mismatch is fixed: a build/push tags the running wolf version, and the `WOLF_SCANNERS_TAG` resolution + a startup log line make the active tag obvious. A documented `WOLF_SCANNERS_TAG=latest` fallback works today.
6. Settings → **Scanner Images** shows, per wolf-built variant: local digest, remote digest, "update available", last-built, and a **Rebuild (local)** button (plus **Rebuild all**) that is *always available regardless of credentials*; clicking streams live logs into a console panel and ends with success/failure. When a `dockerhub_token` secret exists, a **"push to DockerHub"** toggle appears next to the button, turning it into **Rebuild & push**. Missing credentials never disable the build — only the push toggle is hidden, with a hint linking to the credential card.
7. A **DockerHub credential** card in the Scanner Images page lets the operator save username + PAT; it is clearly labelled *optional — only needed to publish*. Absent credentials hide the push toggle but never block local builds.
8. Build/push is admin-scoped (`write:config`) and audit-logged.
9. `go build ./...`, `go vet ./...`, `go test ./...` green; `pnpm test`, `pnpm build`, `pnpm typecheck` green.
10. End-to-end: store a DockerHub PAT → Rebuild default with push → watch logs → image appears on DockerHub with the version tag → Scanners tab shows it `up to date`.

---

## File structure

**New backend:**
- `internal/scannerbuild/embed.go` — `//go:embed` of `scanners/Dockerfile*`, `scanners/install/**`, `scanners/*.env`, `scanners/*.yaml`; helper to materialize the context to a temp dir.
- `internal/scannerbuild/build.go` — `Builder` running `docker buildx build`/`docker push` via `os/exec`, streaming combined output through a callback; variant table (default/jvm/rust/codeql → Dockerfile + image suffix).
- `internal/scannerbuild/build_test.go` — context-materialization + arg-construction tests (no real docker).
- `internal/api/routes/scanner_build.go` — `BuildScannerImage`, `BuildAllScannerImages` handlers (SSE).
- `internal/api/routes/scanner_build_test.go`.

**Modified backend:**
- `internal/models/types.go` — add `KeyTypeDockerHubToken KeyType = "dockerhub_token"`.
- `internal/setup/scanners/scanners.go` — registry namespace + tag resolution helpers; expose the active image refs for the UI; keep DockerHub default.
- `internal/api/server.go` — register `/scanners/images/{variant}/build` + `/scanners/images/build-all` under `write:config`.
- `internal/api/openapi/spec.go` — catalog the new endpoints.
- `scanners/install/go-tools.sh` — the slimming cleanup.
- `Makefile` — keep build/push targets; ensure `:latest` + `:<version>` tagging parity with the server path.

**New frontend:**
- `ui/src/routes/_authed.settings.tsx` (Scanners tab section) or a dedicated `scanner-images` panel component — per-variant rows + Rebuild & push + log console.
- `ui/src/components/scanners/build-console.tsx` — SSE log streamer.
- `ui/src/components/scanners/dockerhub-credential.tsx` — save username + PAT.
- `ui/src/lib/scanner-build.ts` — typed client + SSE hook.

---

## Phase 1 — Slim the Go layer (no new infra; immediate size win)

### Task 1: Tighten go-tools.sh cleanup
**Files:** `scanners/install/go-tools.sh`, `scanners/smoke-test.sh`

- [ ] **Step 1:** Read `scanners/install/go-tools.sh` end-to-end; note that it keeps `/usr/local/go-toolchain` and `$GOBIN` but never removes `$GOPATH/pkg`.
- [ ] **Step 2:** After the binary-move + `rm -rf $GOCACHE`, add:
  ```bash
  # Drop the module + build cache used only to compile the tools — not
  # needed at runtime. gosec/govulncheck use `go list`/`go env`, which
  # need GOROOT (src + bin), not GOPATH modules.
  rm -rf "${GOPATH}/pkg" "${GOPATH}/bin"
  # Strip toolchain dirs unused at runtime (docs, tests, api, examples).
  rm -rf /usr/local/go-toolchain/{test,doc,api,misc}
  ```
- [ ] **Step 3:** In `scanners/smoke-test.sh`, ensure there's a check that `gosec --version`, `govulncheck -version`, `staticcheck --version` all run, and that `go env GOROOT` + `go list std >/dev/null` succeed inside the default image (proves the stripped toolchain still resolves modules).
- [ ] **Step 4:** Build the default locally: `make scanners-build`; record `docker image inspect ...:dev --format '{{.Size}}'` before/after; confirm ≥600 MB unpacked reduction.
- [ ] **Step 5:** Run `scanners/smoke-test.sh` against the new image; all Go tools green.
- [ ] **Step 6:** Commit: `perf(scanners): slim Go toolchain layer (drop GOPATH cache + toolchain docs/tests)`.

---

## Phase 2 — DockerHub credential + embedded build context

### Task 2: dockerhub_token secret type
**Files:** `internal/models/types.go`, a small handler-validation touch in `internal/api/routes/config.go`

- [ ] **Step 1:** Add `KeyTypeDockerHubToken KeyType = "dockerhub_token"` to the KeyType consts.
- [ ] **Step 2:** No schema change (secrets store is generic). The value stored is the PAT; the username goes in `key_name` (reuse the field) or a second `dockerhub_username` setting — choose `key_name = "<dockerhub-username>"`, encrypted value = PAT. Document this in the handler comment.
- [ ] **Step 3:** Test: create a `dockerhub_token` secret via `POST /config/secrets`, list it, confirm `key_type` round-trips. (Extend the existing secrets test.)
- [ ] **Step 4:** Commit: `feat(secrets): dockerhub_token key type`.

### Task 3: Embed the scanners build context
**Files:** `internal/scannerbuild/embed.go`, `internal/scannerbuild/build_test.go`

- [ ] **Step 1:** Create `embed.go`:
  ```go
  package scannerbuild

  import "embed"

  // The build context is ~100 KB of Dockerfiles + install scripts +
  // version pins — embedded so the server can build without a checkout.
  //go:embed all:context
  var contextFS embed.FS
  ```
  Then add a `make`/generate step (or a committed copy) placing the needed files under `internal/scannerbuild/context/` mirroring `scanners/` (Dockerfiles, `install/`, `versions.env`, `toolchains.yaml`, `tools.yaml`). Prefer a tiny `go:generate` that rsyncs `scanners/` → `internal/scannerbuild/context/` so it stays in sync; document it.
- [ ] **Step 2:** `Materialize(dir string) error` walks `contextFS` and writes every file to `dir`, preserving paths + 0755 on `*.sh`.
- [ ] **Step 3:** Test: `Materialize` to a temp dir; assert `Dockerfile` + `install/go-tools.sh` exist and the script is executable.
- [ ] **Step 4:** Commit: `feat(scannerbuild): embed the scanners build context`.

---

## Phase 3 — Build/push subsystem

### Task 4: Builder (docker buildx via os/exec, streamed)
**Files:** `internal/scannerbuild/build.go`, `internal/scannerbuild/build_test.go`

- [ ] **Step 1:** Define the variant table:
  ```go
  type Variant struct{ Name, Dockerfile, ImageSuffix string }
  var Variants = []Variant{
    {"default", "Dockerfile",        ""},
    {"jvm",     "Dockerfile.jvm",    "-jvm"},
    {"rust",    "Dockerfile.rust",   "-rust"},
    {"codeql",  "Dockerfile.codeql", "-codeql"},
  }
  ```
- [ ] **Step 2:** `type BuildRequest struct { Variant, Namespace, Version string; Push bool; DockerHubUser, DockerHubPAT string }`.
- [ ] **Step 3:** `Build(ctx, req, onLine func(string)) (BuildResult, error)`:
  1. Materialize context to a temp dir (defer cleanup).
  2. **Only if `req.Push`**, `docker login -u <user> --password-stdin` (PAT on stdin; never in argv/logs). A `push=false` build performs **no login and needs no credentials**.
  3. `docker buildx build --file <dockerfile> --build-arg WOLF_VERSION=<version> -t <ns>/<img>:<version> -t <ns>/<img>:latest -t <ns>/<img>:<active-runtime-tag> <flag> <ctxdir>` where `<flag>` is `--push` when `req.Push` else **`--load`** (loads into the local daemon). The third tag is the tag the runtime currently resolves (from `WOLF_SCANNERS_TAG`/default) so a freshly-built-local image is immediately picked up by the next scan without any registry round-trip — dedupe if it equals `:version` or `:latest`.
  4. Return refs (and, if available, the digest parsed from buildx output) plus a `LoadedLocally bool`.
  Redact the PAT from any echoed command line.
- [ ] **Step 4:** Tests (no docker): assert the constructed argv for **push (`--push`) vs local (`--load`)**, the tag list (`:version` + `:latest` + active-runtime-tag, deduped), that a `push=false` build constructs **no `docker login`** invocation, and that the PAT never appears in the argv or the echoed command string.
- [ ] **Step 5:** Commit: `feat(scannerbuild): buildx build/push runner with streamed output`.

### Task 5: API endpoints (SSE)
**Files:** `internal/api/routes/scanner_build.go`, `_test.go`, `server.go`, `openapi/spec.go`

- [ ] **Step 1:** `BuildScannerImage` (`POST /scanners/images/{variant}/build`): parse `{push: bool}` (default false); resolve namespace (setting `scanner_registry_namespace`, default `alphabravodevops`) + version (running wolf version); **only if `push`**, load the `dockerhub_token` secret (404-with-hint if absent) and pass the creds — a `push=false` build never reads the secret and never 404s on missing creds; set SSE headers; call `scannerbuild.Build` with `onLine` writing `data: <line>\n\n` + flush; emit a terminal `event: done` / `event: error`.
- [ ] **Step 2:** `BuildAllScannerImages` (`POST /scanners/images/build-all`): loop the four variants, prefixing each line with `[variant]`.
- [ ] **Step 3:** Register both under `r.With(auth.RequireScope(apikey.ScopeWriteConfig))`; add to the OpenAPI catalog.
- [ ] **Step 4:** Tests: 401 without creds, 403 with read-only token, 404 push-without-dockerhub-secret, and a happy path with `scannerbuild.Build` stubbed via an interface so no real docker runs.
- [ ] **Step 5:** Commit: `feat(api): scanner image build/push endpoints (SSE)`.

### Task 6: Fix the tag-mismatch + expose active refs
**Files:** `internal/setup/scanners/scanners.go`, `cmd/wolf/main.go` (startup log)

- [ ] **Step 1:** Add `ActiveImageRefs()` returning, per variant, the `<ns>/<img>:<tag>` the runtime will pull, so the UI shows exactly what's configured.
- [ ] **Step 2:** Keep the `2.0.0` default but log the resolved tag at startup, and document the `WOLF_SCANNERS_TAG` override. (The real fix is operational: a push from this feature creates the version tag.)
- [ ] **Step 3:** Test `ActiveImageRefs` honours `WOLF_SCANNERS_TAG` / `WOLF_SCANNERS_IMAGE`.
- [ ] **Step 4:** Commit: `feat(scanners): expose active image refs + log resolved tag`.

---

## Phase 4 — UI

### Task 7: Build client + SSE hook
**Files:** `ui/src/lib/scanner-build.ts`

- [ ] **Step 1:** `useScannerImages()` (status per variant, reuses `/scanners/images`), and `streamBuild(variant, push, onLine)` opening the SSE endpoint with the credential cookie.
- [ ] **Step 2:** Commit: `feat(ui): scanner-build client + SSE hook`.

### Task 8: DockerHub credential card
**Files:** `ui/src/components/scanners/dockerhub-credential.tsx`, settings wiring

- [ ] **Step 1:** Form: username + PAT → `POST /config/secrets` with `key_type=dockerhub_token`, `key_name=<username>`. Show "configured / not configured" using `/config/secrets` filtered to the type. Use the Nocturne `.panel` / `.btn` classes.
- [ ] **Step 2:** Commit: `feat(ui): DockerHub credential card`.

### Task 9: Scanner Images panel + build console
**Files:** `ui/src/components/scanners/build-console.tsx`, Scanners-tab section

- [ ] **Step 1:** Per-variant rows: name, local vs remote digest, up-to-date / update-available, last-built, and a **Rebuild (local)** button that is **always enabled regardless of credentials** + a header **Rebuild all**. When a `dockerhub_token` secret exists, render a small **"push to DockerHub"** checkbox/toggle beside the button; checking it makes the action **Rebuild & push** (`push:true`). With no secret, the toggle is replaced by a one-line hint ("Add a DockerHub token to publish") linking to the credential card — the build button stays active. `streamBuild(variant, push, …)` passes the toggle state.
- [ ] **Step 2:** `BuildConsole`: a monospace, auto-scrolling log panel fed by `streamBuild`; shows a running spinner, final success (loaded-locally, or pushed tag) or error. Uses `.glass-card` / `.path` / mono styling.
- [ ] **Step 3:** Commit: `feat(ui): Scanner Images panel with live build console`.

### Task 10: README + smoke + push
**Files:** `README.md`

- [ ] **Step 1:** Document the server-side build/push flow, the `dockerhub_token` secret, the `WOLF_SCANNERS_TAG` knob, and the slimmer Go layer.
- [ ] **Step 2:** Full verification: `go vet ./... && go test ./...`; `cd ui && pnpm test && pnpm build && pnpm typecheck`.
- [ ] **Step 3:** Manual e2e per DoD item 10 (real docker + a scratch DockerHub repo or `--load` only if no creds).
- [ ] **Step 4:** Commit + push.

---

## Risks

- **Server build resource cost.** `docker buildx build` of the default pulls a Debian base + installs ~22 toolchains — minutes of CPU + a few GB transient disk on the wolf host. Mitigation: single-arch (host platform) only from the server; CI remains the multi-arch publisher; surface a clear "this takes a few minutes" notice and stream progress.
- **Credential leakage.** The PAT must never reach argv, logs, or the SSE stream. Mitigation: `--password-stdin`, explicit redaction in any echoed command, and the secret stays in the encrypted store.
- **Embedded context drift.** `internal/scannerbuild/context/` must mirror `scanners/`. Mitigation: a `go:generate`/`make` sync step + a test that diffs key files; CI fails if stale.
- **buildx availability.** Server needs Docker buildx. Mitigation: a preflight `docker buildx version` check with an actionable error if absent.
- **Multi-arch expectation.** Operators may expect arm64+amd64 from the UI build. Mitigation: document that UI builds are host-arch; point at CI / `make` for multi-arch publishes.

---

## Out of scope
- Re-architecting into per-language images (decision: keep 4).
- ghcr.io as primary (decision: DockerHub primary; ghcr stays a CI mirror).
- Multi-arch from the server (CI's job).
