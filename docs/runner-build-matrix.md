# Runner build and qualification inventory

Captured 2026-10-23 for PLAN L07/L07a. This inventories every checked-in
Dockerfile outside the generated build-context mirror. A recipe or CI job is
not evidence that its current image has passed runtime qualification.

Run the build commands below from the Community repository root. Replace
`<image>` with a local review tag. Production qualification uses the resulting
exact digest, not that tag. Fixer engine image recipes are no longer part of
Community; they live in the Wolf Enterprise overlay.

| Role | Recipe / target | Build command | Declared platform / consumer | Publication and qualification |
|---|---|---|---|---|
| API, UI, scan worker, CLI | `Dockerfile`, `runtime` | `docker build --target runtime -t <image> .` | Deployment workflow: AMD64 + ARM64. Compose `wolf` / `scan-worker`; Helm API/worker | `deployment-images.yml`, `wolf-runtime`; native smoke via `scanners/ci/smoke-deployment-image.sh <digest> <platform> runtime`; real API/worker restart qualification separately required |
| Proposal worker | `Dockerfile`, `proposal-runtime` | `docker build --target proposal-runtime -t <image> .` | AMD64 + ARM64; isolated proposal role | Same deployment workflow, `wolf-proposal-runtime`; smoke variant `proposal`; repository/proposal credentials remain separately governed |
| Fixed-input release adapter | `Dockerfile`, `scanner-release-fixed-adapter` | `docker build --target scanner-release-fixed-adapter -t <image> .` | AMD64 + ARM64; fixed release lane | Same workflow, `wolf-release-fixed-adapter`; smoke variant `release-fixed` |
| Quality release adapter | `Dockerfile`, `scanner-release-quality-adapter` | `docker build --target scanner-release-quality-adapter -t <image> .` | AMD64 + ARM64; quality/policy lane | Same workflow, `wolf-release-quality-adapter`; smoke variant `release-quality`; pinned Trivy DBs and policy corpus required |
| Integration release adapter | `Dockerfile`, `scanner-release-integration-adapter` | `docker build --target scanner-release-integration-adapter -t <image> .` | AMD64 + ARM64; Compose/Kubernetes qualification lane | Same workflow, `wolf-release-integration-adapter`; smoke variant `release-integration`; actual cluster tests required |
| Default scanners | `scanners/Dockerfile` | `docker build -f scanners/Dockerfile -t <image> scanners` | Release policy: AMD64; default scanner assignments | `scanners-image.yml`, `wolf-scanners`; built-in `smoke-test.sh`, parser/quality corpus, and positive/negative actual scans |
| JVM scanners | `scanners/Dockerfile.jvm` | `docker build -f scanners/Dockerfile.jvm -t <image> scanners` | Release policy: AMD64; JVM assignments | Same scanner workflow, `wolf-scanners-jvm`; native executable and representative scanner checks required |
| Rust scanners | `scanners/Dockerfile.rust` | `docker build -f scanners/Dockerfile.rust -t <image> scanners` | Release policy: AMD64; Rust assignments | Same workflow, `wolf-scanners-rust`; native executable and representative scanner checks required |
| CodeQL scanners | `scanners/Dockerfile.codeql` | `docker build -f scanners/Dockerfile.codeql -t <image> scanners` | Release policy: AMD64; CodeQL assignments | Same workflow, `wolf-scanners-codeql`; database extraction/query execution required, not just a version probe |
| Minimal Bandit shim fixture | `scanners/Dockerfile.min` | `docker build -f scanners/Dockerfile.min -t <image> scanners` | Local integration fixture; not a production complete scanner variant | No production release channel; `TestShim_Bandit_E2E`; Dockerfile explicitly says not for production |
| Legacy AI runner | `Dockerfile.ai-runner` | `docker build -f Dockerfile.ai-runner -t <image> .` | Legacy recipe; no current production matrix consumer found | No qualified publication channel. Node 24 base, unpinned npm agent installs, and tolerated install failure prevent enterprise qualification. Retain for compatibility; do not present it as a current certified fixer |
| Scanner-analysis test input | `testdata/Dockerfile` | Not an application build | Deliberately scanned test fixture | Excluded from shipped runtime/runner qualification; do not harden away intentional test input |

## Shared inputs and dependencies

- The five deployment targets share the root build context. `runtime` includes
  the UI and main Wolf binary; the three release adapters use lane-specific
  executables; proposal includes the source-editing toolchain.
- `VERSION`, `COMMIT`, and `BUILD_DATE` stamp deployment builds.
  Use a recorded source revision and working-tree identity for local evidence;
  protected release sign-off requires a merged source revision.
- Scanner recipes use `scanners/versions.env`, `tools.yaml`, `toolchains.yaml`,
  the locked OS snapshot, installers, and `go-tools/`. `WOLF_VERSION` is a label
  input, not a substitute for the scanner lock and actual image digest.
- `scanners/build-policy.yaml` is the owned scanner platform authority.
  Its current four tuples are AMD64 only. The deployment workflow's ten
  AMD64/ARM64 tuples are a separate matrix. Dockerfile architecture arguments
  alone do not add an advertised platform.
- `internal/scannerbuild/context/` mirrors these inputs for managed builds.
  Run `make scanners-sync` after changing source/pins; never edit the mirror
  as an independent recipe.

## Remaining release gate

Native local build/smoke evidence is retained under
`validation/2026-10-23/`. It does not establish signed publication, Docker Hub
mirror parity, genuine agent authentication, ARM64 execution, cluster egress,
or the full remote-scan matrix. PLAN L07/L07a/L08 and Q05 stay open until those
requirements and the separate combined-worker qualification have evidence.
