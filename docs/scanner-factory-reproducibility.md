# Managed/customer scanner factory reproducibility

## GoKart compatibility build

`scanners/go-tools/gokart.patch` adapts GoKart 0.5.1 to the locked Go analysis
dependencies. It uses the existing x/tools driver for SSA prerequisites/facts
and handles synthetic functions with no package. Finding filtering compares
file/line/column identities so distinct sources and sinks on the same line are
retained. `build-gokart.sh` patches a
temporary source copy, retains the committed module graph, and uses a relative
module replacement so temporary directory names do not enter build metadata.
The patch and build/check scripts are inputs to the scanner lock and embedded
build context. Do not substitute an unpatched `go install ...@version` build.

From the repository root, a local executable regression check is:

```sh
bash scanners/go-tools/build-gokart.sh /tmp/wolf-gokart-check
bash scanners/go-tools/test-gokart.sh /tmp/wolf-gokart-check
go test ./plugins/go -run GoKart -count=1
```

The executable check requires the locked Go toolchain, Python 3, and standard
shell tools already present in the default scanner image; it disables module
network access and checks unsafe, same-line unsafe, safe, and invalid-source inputs. Native image
smoke runs the same check. Cross-architecture emulation does not replace native
release qualification. A local passing binary does not update deployed images.

## Factory comparison

Wolf compares managed and customer factory outputs with a deterministic,
offline command. The comparison is intentionally not a byte-for-byte image
digest assertion: registry namespaces, release IDs, builder identities,
signatures, attestation subjects, execution timestamps, and SPDX document
namespaces are expected to differ between independently operated factories.
Those values are retained in the report under `nondeterministicFields`; they
are never silently discarded.

Run the comparison from the repository root:

```sh
go run ./cmd/scannertools reproducibility \
  --managed build/managed.factory-evidence.json \
  --customer build/customer.factory-evidence.json \
  --output build/factory-reproducibility-report.json
```

The command exits nonzero for malformed/incomplete evidence or any mismatch in
a reproducible property. It writes a non-equivalent report before returning a
mismatch error, so CI and operators retain the exact failing comparison.

## Evidence contract

Both files use
`wolf.scanners.factory-reproducibility-evidence/v1` and are expected to be
content-addressed and signed by their factory. The JSON object contains:

- `factory`: a bounded factory ID and `managed` or `customer` kind;
- `release`: the canonical `wolf.scanners.release/v1` manifest;
- `scannerLock`: the complete resolved scanner lock, not only a caller-supplied
  digest;
- `policy`: the exact quality/promotion policy digest and, when available, the
  digest of the reviewed declared-policy evidence;
- `images`: one normalized evidence object for each of the canonical eight
  variants;
- `quality`: exact corpus/policy identities and the complete set of measured
  `wolf.scanners/quality-evidence/v2` runs.

Unknown fields, trailing JSON, oversized documents, incomplete matrices,
unverified release images, empty SBOMs, and partial quality coverage fail
closed. The release and embedded lock must agree on the lock and definition
digests. Quality evidence must cover every tool in the embedded lock on both
the stable and candidate sides with parser-clean, digest-bound output.

Each normalized image evidence object has this shape:

```json
{
  "variant": "default",
  "annotations": {
    "org.opencontainers.image.source": "https://git.example/wolf",
    "org.opencontainers.image.revision": "0123456789abcdef0123456789abcdef01234567",
    "dev.wolf.release.variant": "default",
    "dev.wolf.release.image-kind": "scanner",
    "dev.wolf.release.platforms": "linux/amd64,linux/arm64",
    "dev.wolf.release.lock-digest": "sha256:...",
    "dev.wolf.release.definition-digest": "sha256:..."
  },
  "provenance": {
    "buildType": "https://mobyproject.org/buildkit@v1",
    "builderId": "customer-builder-1",
    "invocationId": "customer-run-123",
    "startedAt": "2026-07-31T12:00:00Z",
    "finishedAt": "2026-07-31T12:10:00Z",
    "materials": [
      {
        "uri": "git+https://git.example/wolf@0123456789abcdef0123456789abcdef01234567",
        "digest": {"sha1": "0123456789abcdef0123456789abcdef01234567"}
      }
    ],
    "subjects": [
      {"name": "registry.customer/wolf-scanners", "digest": {"sha256": "..."}}
    ]
  },
  "sbom": {
    "spdxVersion": "SPDX-2.3",
    "dataLicense": "CC0-1.0",
    "name": "customer-default",
    "documentNamespace": "https://spdx.example/customer/default/...",
    "creationInfo": {"created": "2026-07-31T12:10:00Z", "creators": ["Tool: trivy"]},
    "packages": []
  }
}
```

The example abbreviates digest values and the package list for readability;
real evidence requires valid exact digests and at least one package per image.
Collectors must normalize raw SLSA/in-toto provenance into `buildType`,
`materials`, builder/invocation/timestamps, and `subjects`. They must preserve
the complete SPDX package objects, including checksums, external references,
supplier, download location, file-analysis state, and license declarations.

## Properties that must match

The report compares canonical SHA-256 projections of:

1. definition commit, definition digest, and scanner-lock digest;
2. exactly these eight image/platform entries:
   `default`, `jvm`, `rust`, `codeql`, `fixer-base`, `fixer-api`,
   `fixer-claude`, and `fixer-codex`;
3. the complete tool objects in the scanner lock;
4. resolved scanner/fixer build-policy inputs;
5. declared policy digests;
6. corpus, reviewed-expectation, vulnerability-database, and controlled-network
   identities from measured quality evidence;
7. stable/candidate execution mode, output kind/digest, parse-error count, and
   canonical findings for every tool, bound to that tool's golden corpus,
   vulnerability database, and controlled-network policy;
8. deterministic OCI annotations for each image;
9. provenance build type and complete material set for each image; and
10. normalized SPDX package inventory for each image.

Package `SPDXID` values are document-local and are excluded from package
identity. Package name, version, supplier, download location, `filesAnalyzed`,
licenses, checksums, and external references remain comparison inputs.

## Explicit nondeterminism

Every report records both factory values and whether they happened to be equal
for the following classes:

- release ID, operation lane, generation time, and approval receipt;
- aggregate and per-image SPDX document digests;
- factory ID, image name/digest, registry repository/receipts, and fixer base
  reference;
- signature/provenance/SBOM/referrer receipt digests;
- provenance builder, invocation, timestamps, and output subjects; and
- SPDX name, namespace, creator, and creation timestamp;
- vulnerability-database observation timestamps; and
- factory-local controlled-network names and IDs.

Per-tool image references/digests, raw-output digests, duration, output size,
and peak memory are copied into `nondeterministicFields` and remain in the
signed quality evidence. They are not used as cross-factory equivalence keys
because they bind factory-local images and runtime measurements; their
normalized finding/output identities are compared.

Adding another nondeterministic field requires a reviewed code change. The
input cannot supply an ignore list or JSON path, preventing a customer or
managed factory from exempting a real mismatch at runtime.

## Operational sequence

1. Build the same reviewed definition commit and exact scanner lock in both
   factories.
2. Complete each factory's strict image, parser/finding, security, SBOM,
   signing, provenance, Compose, and Kubernetes gates.
3. Capture the verified release manifest, full lock, reviewed policy identity,
   normalized image evidence, and complete measured quality runs.
4. Sign/content-address each factory evidence file.
5. Verify both signatures under the appropriate trust policy.
6. Run `scannertools reproducibility` and retain its report with the candidate
   approval evidence.

The comparator does not authenticate evidence by itself. Signature and trust
verification must happen before comparison; keeping those responsibilities
separate allows the same deterministic comparator to run in managed, private,
and air-gapped environments.

## Cached builds and dependency updates

Use BuildKit for scanner, fixer, and application images. The default scanner
image installs language families in independent stages. `install/version-inputs.sh`
derives each stage's inputs from the canonical `versions.env`; changing an
upstream image pin does not reinstall local tools, and changing Bandit does not
reinstall Go, Ruby, PHP, or Node tools. Go, pip, npm and application pnpm caches
are BuildKit mounts. Release-version labels follow installation, so a new
release ID reuses identical tool layers. Application and release-adapter Go
builds have separate targets.

The weekly scheduled scanner build reuses the existing CI cache. An explicitly
requested security rebuild still bypasses layers. Cache hits never bypass
runtime smoke, parser fixtures, vulnerability checks, signatures, or promotion
requirements. A fresh immutable OS snapshot intentionally invalidates the base
and its dependent stages.

Normal scanner update flow:

```sh
make scanners-check-updates
make scanners-bump TOOL=bandit VERSION=<reviewed-stable-version>
make scanners-validate
make scanners-build
```

`scanners-bump` updates the manifest and canonical environment, downloads and
hashes declared source artifacts, refreshes the Go command dependency graph
when applicable, synchronizes embedded build inputs, and regenerates the lock
and inventory. Source downloads must use HTTPS and succeed before their hashes
are written. For an update containing upstream images, resolve their immutable
references before qualification:

```sh
go run ./cmd/scannertools lock --refresh-images --require-resolved
make scanners-validate
```

Run `make scanners-sync` after editing installer scripts, Dockerfiles, fixer
inputs, or compiled Wolf source. Never hand-edit `internal/scannerbuild/context`.
`make scanners-cache-check` verifies installer dependency isolation. The existing
discovery/candidate/approval/promotion workflow remains the release authority;
local builds do not promote the stable channel.

Fixer updates use `fixer/versions.env`, matching Dockerfile arguments and
`scanners/build-policy.yaml`, the `fixer/go-tools` module, and the on-demand CLI
pins in `internal/fix/install/spec.go`. Refresh integrity values for both CPU
architectures and run `make scanners-sync`. TypeScript 7 requires its verified
architecture-specific npm package as well as the main package. The base image
smokes every installed tool; engine variants must also pass native-platform
CLI smoke. Optional OpenCode remains outside the four-variant release table.

On 2026-10-13, all four scanner image builds passed on linux/amd64. A repeat
default-image build changing only `WOLF_VERSION` completed in 0.60 seconds with
45 cached steps on the same builder. This measures warm local cache reuse, not
cold CI or cross-architecture performance. ShellCheck and Cppcheck in the
scanner image still follow the exact Debian stable snapshot package versions;
they are not claims of latest upstream tool releases.
