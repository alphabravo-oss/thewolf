#!/usr/bin/env bash
# Build a canonical, complete scanner-set manifest from the independently
# published image metadata files.
set -euo pipefail

output="${1:-}"
shift || true

[[ -n "$output" && $# -gt 0 ]] ||
    { echo "usage: build-release-manifest.sh OUTPUT IMAGE_METADATA..." >&2; exit 2; }

: "${SCANNER_RELEASE_ID:?SCANNER_RELEASE_ID is required}"
: "${DEFINITION_COMMIT:?DEFINITION_COMMIT is required}"
: "${SOURCE_DATE:?SOURCE_DATE is required}"
: "${OPERATION:?OPERATION is required}"
: "${AGGREGATE_SBOM_SHA256:?AGGREGATE_SBOM_SHA256 is required}"
: "${LOCK_DIGEST:?LOCK_DIGEST is required}"
: "${DEFINITION_DIGEST:?DEFINITION_DIGEST is required}"
: "${QUALIFICATION_RECEIPT_SHA256:?QUALIFICATION_RECEIPT_SHA256 is required}"
approval_receipt="${APPROVAL_RECEIPT_SHA256:-}"

[[ "$DEFINITION_COMMIT" =~ ^[a-f0-9]{40}$ ]] ||
    { echo "DEFINITION_COMMIT must be a full Git SHA" >&2; exit 2; }
[[ "$SOURCE_DATE" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T ]] ||
    { echo "SOURCE_DATE must be an RFC3339 timestamp" >&2; exit 2; }
for digest in "$LOCK_DIGEST" "$DEFINITION_DIGEST"; do
    [[ "$digest" =~ ^sha256:[a-f0-9]{64}$ ]] ||
        { echo "release definition digest is invalid" >&2; exit 2; }
done
[[ "$QUALIFICATION_RECEIPT_SHA256" =~ ^sha256:[a-f0-9]{64}$ ]] ||
    { echo "candidate qualification receipt digest is invalid" >&2; exit 2; }
if [[ "$OPERATION" == release ]]; then
    [[ "$approval_receipt" =~ ^sha256:[a-f0-9]{64}$ ]] ||
        { echo "protected release approval receipt digest is required" >&2; exit 2; }
elif [[ -n "$approval_receipt" ]]; then
    echo "approval receipt is valid only for a protected release" >&2
    exit 2
fi

tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT

jq -s \
    --arg release_id "$SCANNER_RELEASE_ID" \
    --arg commit "$DEFINITION_COMMIT" \
    --arg generated_at "$SOURCE_DATE" \
    --arg operation "$OPERATION" \
    --arg aggregate_sbom "$AGGREGATE_SBOM_SHA256" \
    --arg lock_digest "$LOCK_DIGEST" \
    --arg definition_digest "$DEFINITION_DIGEST" \
    --arg qualification_receipt "$QUALIFICATION_RECEIPT_SHA256" \
    --arg approval_receipt "$approval_receipt" \
    '
    def expected:
      {
        "default": {kind: "scanner", platforms: ["linux/amd64", "linux/arm64"]},
        "jvm": {kind: "scanner", platforms: ["linux/amd64", "linux/arm64"]},
        "rust": {kind: "scanner", platforms: ["linux/amd64", "linux/arm64"]},
        "codeql": {kind: "scanner", platforms: ["linux/amd64"]},
        "fixer-base": {kind: "fixer", platforms: ["linux/amd64", "linux/arm64"]},
        "fixer-api": {kind: "fixer", platforms: ["linux/amd64", "linux/arm64"]},
        "fixer-claude": {kind: "fixer", platforms: ["linux/amd64", "linux/arm64"]},
        "fixer-codex": {kind: "fixer", platforms: ["linux/amd64", "linux/arm64"]}
      };
    def fixer_engine: . == "fixer-api" or . == "fixer-claude" or . == "fixer-codex";
    (map(select(.variant == "fixer-base"))[0]) as $fixer_base |
    if ($aggregate_sbom | test("^sha256:[a-f0-9]{64}$") | not) then
      error("aggregate SBOM digest is invalid")
    elif length != 8 then
      error("release manifest requires exactly eight image variants")
    elif ([.[].variant] | unique | sort) != (expected | keys | sort) then
      error("release manifest image variants are incomplete or duplicated")
    elif any(.[];
      (.digest | test("^sha256:[a-f0-9]{64}$") | not)
      or (.imageKind != expected[.variant].kind)
      or (.releaseId != $release_id)
      or (.lockDigest != $lock_digest)
      or (.definitionDigest != $definition_digest)
      or ((.approvalReceiptDigest // "") != $approval_receipt)
      or ((.platforms | sort) != (expected[.variant].platforms | sort))
      or (.primary.repository | length == 0)
      or (.primary.verified != true)
      or (.sbom_sha256 | test("^sha256:[a-f0-9]{64}$") | not)
      or (.evidence.signatureVerificationSha256 | test("^sha256:[a-f0-9]{64}$") | not)
      or (.evidence.provenanceVerificationSha256 | test("^sha256:[a-f0-9]{64}$") | not)
      or (.evidence.sbomVerificationSha256 | test("^sha256:[a-f0-9]{64}$") | not)
      or (.evidence.referrersSha256 | test("^sha256:[a-f0-9]{64}$") | not)
      or (.signatureVerified != true)
      or (.provenanceVerified != true)
      or (.sbomVerified != true)
      or ((.mirror != null) and
          ((.mirror.repository | length == 0)
           or (.mirror.verified != true)
           or (.mirror.signatureVerified != true)
           or (.mirror.provenanceVerified != true)
           or (.mirror.sbomVerified != true)
           or (.mirror.referrersSha256 | test("^sha256:[a-f0-9]{64}$") | not)))
      or ((.variant | fixer_engine) and
          .baseReference != ($fixer_base.primary.repository + "@" + $fixer_base.digest))
    ) then
      error("release manifest contains invalid kind, dependency, digest, SBOM, repository, or platform metadata")
    else
      {
        schemaVersion: "wolf.scanners.release/v1",
        releaseId: $release_id,
        definitionCommit: $commit,
        definitionDigest: $definition_digest,
        lockDigest: $lock_digest,
        approvalReceiptDigest: (if $approval_receipt == "" then null else $approval_receipt end),
        generatedAt: $generated_at,
        operation: $operation,
        aggregateSbom: {
          mediaType: "application/spdx+json",
          sha256: $aggregate_sbom
        },
        qualificationReceipt: {
          mediaType: "application/vnd.wolf.scanner.candidate-qualification.v1+json",
          sha256: $qualification_receipt
        },
        images: (sort_by(.variant))
      }
    end
    ' "$@" | jq -S . >"$tmp"

mv "$tmp" "$output"
trap - EXIT
sha256sum "$output"
