#!/usr/bin/env bash
# Create a protected release manifest from an already-published candidate.
# Image metadata is never rewritten: a release points at the exact candidate
# image digests that were verified before the protected-environment approval.
set -euo pipefail

candidate_manifest="${1:-}"
candidate_digest="${2:-}"
verification_report="${3:-}"
approval_receipt="${4:-}"
output="${5:-}"

[[ -f "$candidate_manifest" && -f "$verification_report" && -f "$approval_receipt" && -n "$output" ]] || {
    echo "usage: promote-release-manifest.sh CANDIDATE_MANIFEST CANDIDATE_DIGEST VERIFICATION_REPORT APPROVAL_RECEIPT OUTPUT" >&2
    exit 2
}
: "${RELEASE_ID:?RELEASE_ID is required}"
: "${SOURCE_DATE:?SOURCE_DATE is required}"
: "${PROMOTION_COMMIT:?PROMOTION_COMMIT is required}"

[[ "$candidate_digest" =~ ^sha256:[a-f0-9]{64}$ ]] || {
    echo "candidate OCI digest is invalid" >&2
    exit 2
}
[[ "$RELEASE_ID" =~ ^scanner-set-[0-9]{4}\.(0[1-9]|[1-4][0-9]|5[0-3])\.[1-9][0-9]*$ ]] || {
    echo "release ID is invalid" >&2
    exit 2
}
[[ "$SOURCE_DATE" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T ]] || {
    echo "SOURCE_DATE must be RFC3339" >&2
    exit 2
}
[[ "$PROMOTION_COMMIT" =~ ^[a-f0-9]{40}$ ]] || {
    echo "PROMOTION_COMMIT must be a full Git SHA" >&2
    exit 2
}

candidate_file_digest="sha256:$(sha256sum "$candidate_manifest" | awk '{print $1}')"
verification_digest="sha256:$(sha256sum "$verification_report" | awk '{print $1}')"
approval_digest="sha256:$(sha256sum "$approval_receipt" | awk '{print $1}')"
candidate_id="$(jq -er '.releaseId' "$candidate_manifest")"
lock_digest="$(jq -er '.lockDigest' "$candidate_manifest")"
definition_digest="$(jq -er '.definitionDigest' "$candidate_manifest")"
definition_commit="$(jq -er '.definitionCommit' "$candidate_manifest")"

jq -e \
    --arg candidate "$candidate_id" \
    --arg candidate_digest "$candidate_digest" \
    --arg manifest_sha "$candidate_file_digest" \
    --arg lock "$lock_digest" \
    --arg definition "$definition_digest" \
    --arg commit "$definition_commit" '
  .schemaVersion == "wolf.scanners.candidate-verification/v1"
  and .candidate.id == $candidate
  and .candidate.ociDigest == $candidate_digest
  and .candidate.manifestSha256 == $manifest_sha
  and .candidate.lockDigest == $lock
  and .candidate.definitionDigest == $definition
  and .candidate.definitionCommit == $commit
  and .mirrorRequired == true
  and .closureVerified == true
  and (.aggregate.primary.signatureVerificationSha256 | test("^sha256:[a-f0-9]{64}$"))
  and (.aggregate.primary.provenanceVerificationSha256 | test("^sha256:[a-f0-9]{64}$"))
  and (.aggregate.primary.sbomVerificationSha256 | test("^sha256:[a-f0-9]{64}$"))
  and (.aggregate.primary.referrersSha256 | test("^sha256:[a-f0-9]{64}$"))
  and (.aggregate.mirror.signatureVerificationSha256 | test("^sha256:[a-f0-9]{64}$"))
  and (.aggregate.mirror.provenanceVerificationSha256 | test("^sha256:[a-f0-9]{64}$"))
  and (.aggregate.mirror.sbomVerificationSha256 | test("^sha256:[a-f0-9]{64}$"))
  and (.aggregate.mirror.referrersSha256 | test("^sha256:[a-f0-9]{64}$"))
  and (.images | length) == 8
  and all(.images[];
    (.digest | test("^sha256:[a-f0-9]{64}$"))
    and .primaryVerified == true
    and .mirrorVerified == true)
' "$verification_report" >/dev/null || {
    echo "candidate verification report does not bind the exact complete mirrored closure" >&2
    exit 1
}

jq -e \
    --arg release "$RELEASE_ID" \
    --arg candidate "$candidate_id" \
    --arg candidate_digest "$candidate_digest" \
    --arg evidence "$verification_digest" \
    --arg lock "$lock_digest" \
    --arg definition "$definition_digest" \
    --arg commit "$definition_commit" \
    --arg promotion_commit "$PROMOTION_COMMIT" '
  .schemaVersion == "wolf.scanners.protected-approval/v2"
  and .releaseId == $release
  and .candidateId == $candidate
  and .candidateManifestDigest == $candidate_digest
  and .verificationEvidenceDigest == $evidence
  and .lockDigest == $lock
  and .definitionDigest == $definition
  and .definitionCommit == $commit
  and .workflowDefinitionCommit == $promotion_commit
  and .protectedEnvironment == "scanner-release"
  and .approvalEnforcedBy == "github-protected-environment"
  and (.workflowRun.id | test("^[0-9]+$"))
  and (.workflowRun.attempt | test("^[0-9]+$"))
' "$approval_receipt" >/dev/null || {
    echo "approval receipt does not bind this exact candidate and verification evidence" >&2
    exit 1
}

jq -e --arg candidate "$candidate_id" '
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
  . as $root
  | .schemaVersion == "wolf.scanners.release/v1"
  and .releaseId == $candidate
  and ($candidate | test("^scanner-candidate-[a-z0-9][a-z0-9._-]{0,94}$"))
  and (.operation == "candidate" or .operation == "security-rebuild")
  and .approvalReceiptDigest == null
  and (.promotedFrom == null)
  and ([.images[].variant] | unique | sort) == (expected | keys | sort)
  and all(.images[];
    .releaseId == $candidate
    and .lockDigest == $root.lockDigest
    and .definitionDigest == $root.definitionDigest
    and .approvalReceiptDigest == null
    and .imageKind == expected[.variant].kind
    and ((.platforms | sort) == (expected[.variant].platforms | sort))
    and (.digest | test("^sha256:[a-f0-9]{64}$"))
    and .primary.verified == true
    and .mirror.verified == true
    and .signatureVerified == true
    and .provenanceVerified == true
    and .sbomVerified == true)
' "$candidate_manifest" >/dev/null || {
    echo "candidate manifest is not a complete, mirrored, verified candidate closure" >&2
    exit 1
}

tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT
jq -S \
    --arg release "$RELEASE_ID" \
    --arg generated "$SOURCE_DATE" \
    --arg candidate "$candidate_id" \
    --arg candidate_digest "$candidate_digest" \
    --arg candidate_manifest_sha "$candidate_file_digest" \
    --arg verification "$verification_digest" \
    --arg approval "$approval_digest" \
    --arg promotion_commit "$PROMOTION_COMMIT" '
  .releaseId = $release
  | .generatedAt = $generated
  | .operation = "release"
  | .approvalReceiptDigest = $approval
  | .promotionCommit = $promotion_commit
  | .promotedFrom = {
      candidateId: $candidate,
      candidateManifestDigest: $candidate_digest,
      candidateManifestSha256: $candidate_manifest_sha,
      verificationEvidenceDigest: $verification
    }
' "$candidate_manifest" >"$tmp"
mv "$tmp" "$output"
trap - EXIT
sha256sum "$output"
