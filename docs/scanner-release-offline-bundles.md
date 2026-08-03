# Scanner release offline export and import

Wolf can move an immutable scanner release between connected and air-gapped
installations without loading the bundle into API or CLI memory.

## API contract

- `GET /api/v1/scanner-supply-chain/releases/{id}/export`
  requires `read:scanner-supply-chain`. `bundle_version=1` preserves the
  metadata-only format; `bundle_version=2` embeds a complete OCI transfer.
  Repeat `platform=linux/amd64` to select one or more platforms in v2, or omit
  it to export every published platform.
- `POST /api/v1/scanner-supply-chain/release-imports`
  requires `admin:scanner-supply-chain`, `Idempotency-Key`, and
  `X-Wolf-Import-Reason`. The request body is the bundle itself. Set
  `no_network=true` for bundle-only import, or set `registry_target_id` to a
  configured private registry for idempotent upload and exact readback. Those
  two options are mutually exclusive.
- API export defaults to v1 so existing integrations retain their historical
  response. The CLI defaults to v2 for complete transfers.
- The compressed upload limit is 8 GiB. Extraction additionally limits the
  archive to 10,000 regular files, 4 GiB per file, and 16 GiB total. Paths,
  links, duplicate entries, undeclared files, sizes, and SHA-256 digests are
  checked before persistence. Export enforces the same size limits, so Wolf
  does not produce a bundle its import path cannot consume.
- A successful first import returns `201`; replaying the same imported release
  and manifest returns `200` with `created: false`. Reusing a release ID,
  release name, or manifest digest for different content returns `409`.

The export response includes:

- `X-Wolf-Manifest-Digest`
- `X-Wolf-Bundle-Digest`
- `X-Wolf-Bundle-Signature-Status` (`signed-<provider>` or `unsigned`)
- `X-Wolf-Bundle-Schema`
- `X-Wolf-Bundle-Platforms`
- `X-Wolf-Release-ID`

The import response keeps two separate claims:

- `integrity_verified: true` means the bundle index, manifest, inventory, and
  all embedded file hashes matched.
- `signature_status` is `verified-<algorithm>`, `present-not-verified`, or
  `unsigned`.
- `bundle_schema` and `oci_closure_verified` identify whether complete v2 OCI
  closure validation ran.
- `network_mode` is `no-network` or `registry-enabled`.
- `destination_read_back_verified` is true only after every destination
  manifest and the final platform mapping have been re-read.
- `registry_mappings` records each image key, its signed source identity, exact
  destination identity, digest, and readback result.
- `external_signatures_verified` is true only when the configured offline
  verifier returned one strictly bound success result for every logical image
  and every primary/mirror registry target. A partial, duplicate, mismatched,
  malformed, oversized, failed, or timed-out verifier result fails closed.
- `external_signature_verification_digest` identifies the canonical complete
  result set. Wolf also stores a protected, content-addressed
  `offline-image-signature-verification` receipt and binds its digest into the
  append-only release event. An idempotent replay must reproduce the same
  verification decision and receipt.

Wolf preserves all OCI referrer records and their blobs in v2, including image
signatures, SBOMs, provenance, certificates, and transparency evidence exposed
by the source registry. Preserving and closure-checking those bytes is not by
itself a signature claim: the independent verifier result is required before
Wolf sets `external_signatures_verified`.

## Versioned bundle contents

Both versions contain:

- canonical `release-manifest.json`;
- `bundle-index.json`, binding every archive entry to its size and SHA-256
  digest;
- `evidence/release-inventory.json`, containing the release, policy snapshot,
  tools, images, platforms, and artifact inventory;
- local evidence files only when their stored URI resolves to a regular file
  beneath Wolf's configured artifact root;
- metadata and hashes for remote or OCI-hosted evidence that is not embedded;
- an optional `signatures/release-manifest.ed25519.json` portable signature
  envelope (the historical filename is retained for compatibility across
  algorithms).

Version 1 uses schema `wolf.scanner-release-bundle/v1` and media type
`application/vnd.wolf.scanner-release-bundle.v1+tar+zstd`. It intentionally
retains the historical metadata-only behavior: OCI image bytes are not
embedded, so operators must mirror image digests separately.

Version 2 uses schema `wolf.scanner-release-bundle/v2` and media type
`application/vnd.wolf.scanner-release-bundle.v2+tar+zstd`. It additionally
contains:

- the selected OCI image index or manifest;
- every selected platform manifest, config, and layer;
- the original source index when platform selection creates a derived index;
- every referrer manifest returned for the source index and selected platform
  manifests, plus all configs and layers reachable from those referrers;
- signed `oci_records` for every embedded digest, size, media type, kind, and
  canonical `oci/blobs/sha256/<hex>` path;
- `source_reference` and `source_digest` beside the exact transferred
  reference and digest, preserving original-to-derived identity.

Platform selection is strict. A requested platform must exist once in the
source index and its digest must match the already-published release inventory.
The derived index is deterministic and signed as part of the portable release
manifest.

## Verification and write ordering

Import treats uploaded bytes as untrusted until all checks complete:

1. Stream zstd/tar into a newly created empty temporary directory using bounded
   memory and disk accounting. Only canonical regular-file paths are accepted;
   absolute/traversal paths, links, devices, duplicate entries, undeclared
   entries, and size-limit violations fail closed.
2. Verify every archive entry against `bundle-index.json`, then verify the
   canonical release-manifest digest and portable inventory identity.
3. Verify the portable manifest signature against the configured signer trust
   policy. An embedded public key is never trusted. Break-glass import must be
   explicit and remains labeled unverified.
4. For v2, verify every signed OCI record and recursively walk every
   index/manifest descriptor. Configs, layers, trust referrers, subjects, and
   platform digests must remain inside the signed closure.
5. For v2, invoke the deployment-owned offline image verifier once for every
   image/registry-target pair and require the exact complete result set.
6. Only after those checks may Wolf upload content or persist a release.

With `registry_target_id`, blobs are inspected by digest and uploaded only when
missing. Image/platform manifests are uploaded before the root, and trust
referrer manifests after their subjects. Existing exact manifests are skipped.
Wolf then re-fetches every manifest/referrer by digest, re-fetches the root,
and verifies the exact destination platform mapping before recording the
imported release and original-to-destination identity mapping.

With `no_network=true`, Wolf creates no registry client and makes no registry
request. Verification and release persistence use only bundle bytes, local
trust policies, the deployment-owned verifier, artifact storage, and the
database. The verifier contract supplies verified local file paths and a
minimal environment; the verifier executable must itself be file-only. Enforce
that property through code review and the API pod's deployment egress policy.

## Signing and trust configuration

Export is unsigned unless `WOLF_SCANNER_SIGNER_PROFILE_FILE`,
`WOLF_SCANNER_SIGNER_ADAPTER`, and a durable
`WOLF_SCANNER_SIGNER_JOURNAL` are configured. The profile contains opaque
references only; the API never reads private-key material. See
[Customer-managed scanner release signing](scanner-release-signing.md).

Import trust is independent. Configure
`WOLF_SCANNER_BUNDLE_TRUST_POLICY_FILE` with explicitly trusted public keys:

```json
{
  "schema_version": "wolf.scanner-bundle-trust/v1",
  "keys": [
    {
      "key_id": "scanner-release-prod-2026",
      "algorithm": "ed25519",
      "public_key": "<base64 32-byte Ed25519 public key>"
    }
  ]
}
```

For adapter signatures, trust entries additionally contain `profile_digest`,
`public_key_pem`, expected identity/issuer/subject, and
`trust_root_digest`. Wolf verifies against this pinned key and never trusts a
public key supplied by the bundle. Legacy Ed25519 trust entries remain
supported.

Without a trusted portable signature, import fails closed. An administrator can
use `allow_unverified=true` only as an explicit break-glass path. Archive and
content integrity checks still run, the supplied reason is audited, imported
releases are not rollback-eligible, and the result is never labeled verified.

### Offline image-signature verifier

Version 2 has a second, independent trust boundary. Configure both:

- `WOLF_SCANNER_BUNDLE_IMAGE_VERIFIER`: an absolute executable path owned by
  the deployment; and
- `WOLF_SCANNER_BUNDLE_IMAGE_TRUST_POLICY_FILE`: an absolute, non-empty,
  bounded public trust-policy file.

Both must be configured together. The verifier is executed directly without a
shell, receives only `PATH`, `SSL_CERT_FILE`, and `SSL_CERT_DIR` from the host,
and reads one JSON request from stdin. Wolf computes `operation_id` over the
entire request with the operation field empty. Every closure path is absolute,
beneath the already verified extraction root, and is accompanied by the signed
digest, size, media type, and OCI kind. A representative request is:

```json
{
  "schema_version": "wolf.scanner-offline-image-verification-request/v1",
  "operation_id": "sha256:<request digest>",
  "trust_policy_path": "/run/wolf/offline-bundles/images/trust-policy.json",
  "trust_policy_digest": "sha256:<policy digest>",
  "image_key": "default",
  "registry_target_id": "primary",
  "image_digest": "sha256:<image index digest>",
  "signature_digest": "sha256:<raw signature payload digest>",
  "signature_artifact_digest": "sha256:<OCI signature root digest>",
  "signature_media_type": "application/vnd.oci.image.manifest.v1+json",
  "certificate_digest": "sha256:<optional certificate digest>",
  "identity": "scanner-release@example.com",
  "issuer": "https://issuer.example.com",
  "subject": "registry.example/wolf/scanners@sha256:<image digest>",
  "trust_root": "sha256:<trust root digest>",
  "signing_operation_id": "sha256:<signing operation digest>",
  "closure": [
    {
      "digest": "sha256:<OCI object digest>",
      "size": 1234,
      "media_type": "application/vnd.oci.image.manifest.v1+json",
      "kind": "oci-trust-manifest",
      "path": "/verified/extraction/root/oci/blobs/sha256/<hex>"
    }
  ]
}
```

The verifier must validate bytes by digest and size, reconstruct the OCI
signature graph, verify the signature over the exact image digest against the
supplied policy, enforce identity/issuer/subject/trust-root expectations, and
emit exactly one JSON object with no trailing data:

```json
{
  "schema_version": "wolf.scanner-offline-image-verification-result/v1",
  "operation_id": "sha256:<same request digest>",
  "trust_policy_digest": "sha256:<same policy digest>",
  "image_key": "default",
  "registry_target_id": "primary",
  "image_digest": "sha256:<same image digest>",
  "signature_digest": "sha256:<same signature digest>",
  "signature_artifact_digest": "sha256:<same OCI signature root>",
  "identity": "scanner-release@example.com",
  "issuer": "https://issuer.example.com",
  "subject": "registry.example/wolf/scanners@sha256:<image digest>",
  "trust_root": "sha256:<same trust root>",
  "verifier_id": "company-cosign-offline",
  "verifier_version": "2.6.0+policy.4",
  "evidence_digest": "sha256:<verifier evidence digest>",
  "verified": true
}
```

Requests and each output stream are capped at 1 MiB. Wolf never sends private
keys or registry credentials to the verifier. A v2 import rejects missing or
failed image verification unless an administrator explicitly supplies
`allow_unverified=true`; that break-glass release is not rollback-eligible.

Helm mounts the two public policies independently:

```yaml
scannerRelease:
  offlineBundles:
    portableTrustPolicySecret: scanner-portable-bundle-trust
    portableTrustPolicyKey: trust-policy.json
    imageVerifier:
      enabled: true
      path: /usr/local/bin/company-image-verifier
      trustPolicySecret: scanner-image-signature-trust
      trustPolicyKey: image-trust-policy.json
```

The verifier binary must be baked into the immutable API image. Compose uses
the fixed `/run/wolf/offline-bundles/...` targets documented in `.env.example`
and read-only host mounts.

## CLI

```sh
# Complete all-platform export (the CLI defaults to bundle version 2).
wolf scanner release export RELEASE_ID \
  --file scanner-release.tar.zst

# Platform-selective export.
wolf scanner release export RELEASE_ID \
  --bundle-version 2 \
  --platform linux/amd64 \
  --platform linux/arm64 \
  --file scanner-release-linux.tar.zst

# Air-gapped import: require a signature trusted by the destination server and
# do not contact a registry.
wolf scanner release import scanner-release.tar.zst \
  --reason "approved air-gap transfer CHG-1234" \
  --idempotency-key CHG-1234-release-import \
  --no-network

# Connected private-registry import with idempotent upload and readback.
wolf scanner release import scanner-release.tar.zst \
  --reason "approved private registry transfer CHG-1235" \
  --idempotency-key CHG-1235-release-import \
  --registry-target registry-private-prod

# Break glass. Integrity is verified, but signature_status will not be verified.
wolf scanner release import scanner-release.tar.zst \
  --reason "emergency recovery approved in INC-9876" \
  --allow-unverified
```

Use `--file -` to stream export bytes to stdout, or `-` as the import filename
to stream from stdin. The CLI uses temporary files and an atomic install for
normal export paths so interrupted transfers do not leave a completed-looking
bundle.

## Operational validation

Before enabling signed transfer in production:

1. Mount only the opaque profile and adapter credential/workload identity on
   the exporting API role; mount public trust policy on importing API roles.
2. Export a known release and confirm `signed-<provider>`.
3. Import into a staging installation and confirm `integrity_verified: true`
   and `signature_status: verified-<algorithm>`. For v2 also confirm
   `oci_closure_verified: true`, `external_signatures_verified: true`, and a
   non-empty external verification digest.
4. Replay with the same idempotency key and confirm `created: false`.
5. Import with `--registry-target`, repeat it, and confirm the mappings and
   digests are identical, `destination_read_back_verified: true`, and no blob
   is uploaded twice.
6. Disconnect registry networking, import a v2 bundle with `--no-network`, and
   confirm it succeeds without an outbound registry request.
7. Exercise the hostile corpus: corrupt, truncated, unsigned, absolute and
   traversal paths, link entries, duplicate entries, oversized files/totals,
   wrong platforms, and conflicting replay identities must all fail before
   registry or durable release writes.
8. Try an unknown signing key and confirm `422` without the break-glass flag.
9. Confirm the raw bundle exists under
   `WOLF_ARTIFACTS_ROOT/scanner-release-bundles/` and the imported release,
   inventory, images, and evidence metadata survive restart.
10. Review the `scanner.release.imported` critical audit event and the
   `scanner.release.bundle_exported` informational event.

Back up imported bundle files with the artifact root. Database restoration
without the matching artifact backup preserves inventory hashes but not the
portable payload.
