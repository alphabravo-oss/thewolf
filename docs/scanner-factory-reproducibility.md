# Managed/customer scanner factory reproducibility

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
