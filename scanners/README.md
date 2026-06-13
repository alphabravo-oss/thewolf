# wolf-scanners — split into 4 images

This directory builds the **four images** that together bundle every scanner that `thewolf` orchestrates. Wolf-slim spawns one short-lived container per tool invocation, picking the right image per tool.

See `../PLAN.md` §5.1 for the full design rationale.
See `tools.yaml` for the source manifest, `toolchains.yaml` for scanner image
runtime/toolchain metadata, and `TOOLS.md` for the generated scanner table.

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
├── versions.env         # pinned versions consumed by scanner builds
├── wolf-tool-entry      # PID-1 entrypoint for every image
├── smoke-test.sh        # variant-aware smoke test (WOLF_SCANNERS_VARIANT env)
├── install/
│   ├── core_python.sh   # sqlfluff, detect-secrets, yamllint
│   ├── core_node.sh     # currently empty; spectral is upstream
│   ├── core_native.sh   # shellcheck (apt)
│   ├── downloads.sh     # github release tars (trivy, grype, syft, ...)
│   ├── lang_python.sh   # bandit, ruff, mypy, pip-audit, radon, vulture
│   ├── lang_node.sh     # eslint
│   ├── lang_native.sh   # cppcheck (apt)
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

make scanners-build            # builds default wolf-scanners:dev
make scanners-build-jvm        # builds wolf-scanners-jvm:dev
make scanners-build-rust       # builds wolf-scanners-rust:dev
make scanners-build-codeql     # builds wolf-scanners-codeql:dev
make scanners-build-all        # all four

make scanners-smoke            # smoke-tests every image already built
make dev-scanners              # interactive shell inside the default image
```

A single-image build takes ~10–25 minutes on a cold cache. All four together is ~30–50 minutes serial, much less in CI buildx with concurrency.

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
7. Regenerate docs and validate: `make scanners-docs && make scanners-validate`.
8. Build + smoke: `make scanners-build && make scanners-smoke` (and the matching variant if applicable).
9. Update `LICENSES.md` with the tool's license.

## Smoke test contract

`smoke-test.sh` is the canonical contract for "the image is healthy". It runs once at image-build time (the build fails if any tool is missing) and again at CI time. Each Dockerfile sets `WOLF_SCANNERS_VARIANT={default,jvm,rust,codeql}` so the smoke test asserts only the tools that belong in the variant.
