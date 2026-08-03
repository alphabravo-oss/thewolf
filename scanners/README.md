# wolf-scanners — four runtime images in an eight-image release

This directory builds the **four images** that together bundle every scanner that `thewolf` orchestrates. Wolf-slim spawns one short-lived container per tool invocation, picking the right image per tool.

A complete managed release also carries the four signed fixer artifacts from
`../fixer/` (`base`, `api`, `claude`, and `codex`). They share the same lock,
platform, SBOM, provenance, signing, mirror, and offline-bundle contract but
are classified as `image_kind=fixer`, so scan assignment and scanner rollout
never treat them as scanner runtime images. See
[`../docs/scanner-release-management.md`](../docs/scanner-release-management.md).

See `../PLAN.md` §5.1 for the full design rationale.
See `tools.yaml` for the source manifest, `toolchains.yaml` for scanner image
runtime/toolchain metadata, `build-policy.yaml` for required release variants,
`scanner-lock.yaml` for the generated immutable definition, and `TOOLS.md` for
the generated scanner table. `os-packages.yaml` and
`os-packages.lock.yaml` define the reproducible operating-system package layer.

## Image matrix

| Image | Build with | Tools | Approx size |
|---|---|---|---|
| `wolf-scanners` | `Dockerfile` | Small bundled tools (bandit, ruff, gosec, eslint, detect-secrets, sqlfluff, …) | ~2–2.5 GB |
| `wolf-scanners-jvm` | `Dockerfile.jvm` | detekt, infer, pmd, OpenJDK | ~2 GB |
| `wolf-scanners-rust` | `Dockerfile.rust` | clippy + rust toolchain | ~1.2 GB |
| `wolf-scanners-codeql` | `Dockerfile.codeql` | CodeQL CLI (license-restricted) | ~700 MB |

Operators pull only the images their repos need. A typical Python/JS shop never pulls `-jvm` or `-rust`.

## Layout

```text
scanners/
├── Dockerfile           # default image (core + small lang tools)
├── Dockerfile.jvm       # JVM bucket
├── Dockerfile.rust      # Rust bucket
├── Dockerfile.codeql    # CodeQL (license-gated)
├── tools.yaml           # authoritative scanner manifest
├── toolchains.yaml      # scanner image base/runtime toolchain metadata
├── build-policy.yaml    # required release variants and platforms
├── os-packages.yaml     # reviewed direct-package policy by variant/platform
├── os-packages.lock.yaml # generated snapshot/index/package identities
├── os-packages/         # generated apt sources, exact pins, artifact records
├── scanner-lock.yaml    # generated wolf.scanners/v1 release definition
├── versions.env         # pinned versions consumed by scanner builds
├── wolf-tool-entry      # PID-1 entrypoint for every image
├── smoke-test.sh        # variant-aware smoke test (WOLF_SCANNERS_VARIANT env)
├── install/
│   ├── core_python.sh   # sqlfluff, detect-secrets, yamllint
│   ├── core_node.sh     # currently empty; spectral is upstream
│   ├── os-packages.sh   # shared immutable-snapshot/exact-pin installer
│   ├── core_native.sh   # verifies locked shellcheck installation
│   ├── downloads.sh     # github release tars (trivy, grype, syft, ...)
│   ├── lang_python.sh   # bandit, ruff, mypy, pip-audit, radon, vulture
│   ├── lang_node.sh     # eslint
│   ├── lang_native.sh   # verifies locked cppcheck installation
│   ├── go-tools.sh      # gosec, staticcheck, govulncheck
│   ├── ruby.sh          # brakeman, rubocop
│   ├── php.sh           # phpstan
│   ├── swift.sh         # swiftlint
│   ├── jvm.sh           # detekt + infer + pmd (Dockerfile.jvm only)
│   ├── rust.sh          # rust toolchain + clippy (Dockerfile.rust only)
│   ├── codeql.sh        # codeql bundle (Dockerfile.codeql only)
│   ├── vuln-db-bake.sh  # build-time vuln DB cache (default image)
│   └── db-refresh.sh    # runtime DB refresh helper
├── testdata/            # tiny fixture for in-CI scanning
├── LICENSES.md          # per-tool license inventory
└── README.md            # this file
```

## Building locally

```shell
# From repo root
make scanners-validate         # validates manifest, docs, version pins, routing
make scanners-bump TOOL=semgrep VERSION=1.94.1
go run ./cmd/scannertools lock --check --require-resolved

make scanners-build            # builds default wolf-scanners:dev
make scanners-build-jvm        # builds wolf-scanners-jvm:dev
make scanners-build-rust       # builds wolf-scanners-rust:dev
make scanners-build-codeql     # builds wolf-scanners-codeql:dev
make scanners-build-all        # all four

make scanners-smoke            # smoke-tests every image already built
make dev-scanners              # interactive shell inside the default image
```

A single-image build takes ~10–25 minutes on a cold cache. All four together is ~30–50 minutes serial, much less in CI buildx with concurrency.

## Deterministic scanner lock

`scanner-lock.yaml` is generated from every scanner build input: the tool and
toolchain manifests, version pins, build policy, Dockerfiles, installer scripts,
entrypoint, and smoke test. It contains all 49 runtime scanners, the exact base
image digests, the required platform set for each image variant, and immutable
digests resolved for upstream scanner images. Its `lockDigest` is calculated
from canonical JSON with the digest field omitted, so YAML formatting and Go map
insertion order cannot affect its identity.

```shell
# Regenerate while retaining already verified image digests.
go run ./cmd/scannertools lock

# CI drift/schema/parity check; does not access registries.
go run ./cmd/scannertools lock --check --require-resolved

# Re-resolve every mutable upstream tag. A changed digest is rejected.
go run ./cmd/scannertools lock --refresh-images

# Accept a reviewed upstream tag mutation and write the new lock.
go run ./cmd/scannertools lock --refresh-images --accept-tag-mutation

# Emit a stable JSON operation result for automation.
go run ./cmd/scannertools lock --check --require-resolved --json
```

The offline `--check` path uses the checked-in resolved references as its cache.
Registry access is only performed by `--refresh-images`. A release factory must
use `--require-resolved`; unresolved mutable tags are never publication-ready.
Version bumps regenerate the lock automatically.

## Reproducible operating-system packages

Every scanner Dockerfile delegates its operating-system package layer to
`install/os-packages.sh`; direct `apt install` commands elsewhere are rejected
by validation. `os-packages.yaml` is the reviewed package-name policy for every
variant and supported architecture. The generated lock records:

- the immutable Debian snapshot timestamp;
- Debian and Debian-security Release and package-index SHA-256 identities;
- every directly requested package's exact name, version, architecture, source,
  filename, and package SHA-256;
- exact per-architecture NodeSource `.deb` URLs and SHA-256 values.

Generated `os-packages/` files are the only package inputs copied into images.
Direct packages are installed with exact `name:architecture=version` pins.
Dependencies can resolve only from the same immutable snapshot. External
artifacts are downloaded over HTTPS and checksum-verified before local apt
installation. The Debian slim base does not contain a configured CA bundle.
Explicit refresh therefore downloads the exact locked, architecture-independent
`ca-certificates` package over HTTPS into the generated build inputs. The image
verifies its SHA-256 and extracts only its trust bundle before contacting the
HTTPS-only snapshots; apt then installs and configures that same exact package
pin normally. Snapshot metadata is also authenticated with Debian's embedded
archive key.

Normal validation is entirely offline:

```shell
make scanners-os-packages-check
go run ./cmd/scannertools os-packages --check --json
```

Network resolution is possible only through an explicit refresh. The Make
target also refreshes the embedded build context and dependent scanner lock:

```shell
make scanners-os-packages-refresh SNAPSHOT=20260730T000000Z
```

Review the policy, generated lock, generated pins/artifacts, embedded-context
parity, and `scanner-lock.yaml` change together. The weekly release-factory run
and manual `refresh-os-packages` operation produce the same changes as a
reviewable artifact; they do not mutate the repository or registry.

## How the shim picks an image per tool

The Go shim (`internal/plugin/container/`) resolves the image for each tool:

```go
cfg.ImageFor("bandit")  // → wolf-scanners:1.0          (default)
cfg.ImageFor("infer")   // → wolf-scanners-jvm:1.0     (override)
cfg.ImageFor("clippy")  // → wolf-scanners-rust:1.0    (override)
cfg.ImageFor("codeql")  // → wolf-scanners-codeql:1.0  (override)
```

The default override map is built by `container.DefaultBucketImages(base, version)` at wolf startup. Operators may inject additional overrides via wolf.yaml.

## Adding a new tool

1. Add the Wolf plugin under `../plugins/<bucket>/<tool>.go` using `container.CommandContext` (see `plugins/python/bandit.go` for the canonical pattern).
2. Add a `tools.yaml` entry with the tool's category, integration tier, pinned version, update source, and docs metadata.
3. Pin the matching version variable in `versions.env`.
4. Add an install line in the appropriate `install/<file>.sh`. Use a `core_*` script if the tool is cross-language, `lang_*` for language-specific small tools, or write a new script + Dockerfile if the tool needs a new heavy toolchain.
5. Add a smoke-test line in `smoke-test.sh` under the right variant block.
6. If the tool lives in a non-default image, add it to `internal/plugin/container/buckets.go`.
7. Regenerate the lock and docs, then validate:
   `go run ./cmd/scannertools lock && make scanners-docs && make scanners-validate`.
8. Build + smoke: `make scanners-build && make scanners-smoke` (and the matching variant if applicable).
9. Update `LICENSES.md` with the tool's license.

## Smoke test contract

`smoke-test.sh` is the canonical contract for "the image is healthy". It runs once at image-build time (the build fails if any tool is missing) and again at CI time. Each Dockerfile sets `WOLF_SCANNERS_VARIANT={default,jvm,rust,codeql}` so the smoke test asserts only the tools that belong in the variant.
