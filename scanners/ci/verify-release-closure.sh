#!/usr/bin/env bash
# Re-verify a published candidate aggregate and every digest it names. The
# canonical output is content-addressed and is the object approved by the
# protected release environment.
set -euo pipefail

release_repository="${1:-}"
mirror_release_repository="${2:-}"
candidate_id="${3:-}"
output_dir="${4:-}"
require_mirror="${5:-true}"

[[ "$release_repository" =~ ^[a-z0-9.-]+(/[a-z0-9._-]+)+$ ]] || {
    echo "invalid primary release repository" >&2
    exit 2
}
[[ "$mirror_release_repository" =~ ^[a-z0-9.-]+(/[a-z0-9._-]+)+$ ]] || {
    echo "invalid mirror release repository" >&2
    exit 2
}
subject_kind=""
if [[ "$candidate_id" =~ ^scanner-candidate-[a-z0-9][a-z0-9._-]{0,94}$ ]]; then
    subject_kind=candidate
elif [[ "$candidate_id" =~ ^scanner-set-[0-9]{4}\.(0[1-9]|[1-4][0-9]|5[0-3])\.[1-9][0-9]*$ ]]; then
    subject_kind=release
else
    echo "invalid candidate or release ID" >&2
    exit 2
fi
[[ -n "$output_dir" ]] || { echo "output directory is required" >&2; exit 2; }
[[ "$require_mirror" == true || "$require_mirror" == false ]] || {
    echo "require_mirror must be true or false" >&2
    exit 2
}
: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"
: "${GITHUB_SERVER_URL:?GITHUB_SERVER_URL is required}"

certificate_identity_regexp="${CERTIFICATE_IDENTITY_REGEXP:-^${GITHUB_SERVER_URL}/${GITHUB_REPOSITORY}/.github/workflows/scanners-image.yml@refs/heads/main$}"
mkdir -p "$output_dir/evidence" "$output_dir/payload"

sha_file() {
    printf 'sha256:%s' "$(sha256sum "$1" | awk '{print $1}')"
}

resolve_digest() {
    oras manifest fetch --descriptor "$1" \
        | jq -er '.digest | select(test("^sha256:[a-f0-9]{64}$"))'
}

verify_subject() {
    local repository="$1"
    local digest="$2"
    local source_commit="$3"
    local label="$4"
    local prefix="$output_dir/evidence/$label"

    cosign verify \
        --certificate-identity-regexp "$certificate_identity_regexp" \
        --certificate-oidc-issuer https://token.actions.githubusercontent.com \
        "${repository}@${digest}" >"${prefix}.signature.json"
    gh attestation verify "oci://${repository}@${digest}" \
        --repo "$GITHUB_REPOSITORY" \
        --source-digest "$source_commit" >"${prefix}.provenance.json"
    gh attestation verify "oci://${repository}@${digest}" \
        --repo "$GITHUB_REPOSITORY" \
        --source-digest "$source_commit" \
        --predicate-type https://spdx.dev/Document/v2.3 >"${prefix}.sbom.json"
    scanners/ci/discover-referrers.sh \
        "${repository}@${digest}" "$digest" "${prefix}.referrers.json" \
        "signature, provenance, and SPDX referrers are not complete"

    jq -nS \
        --arg signature "$(sha_file "${prefix}.signature.json")" \
        --arg provenance "$(sha_file "${prefix}.provenance.json")" \
        --arg sbom "$(sha_file "${prefix}.sbom.json")" \
        --arg referrers "$(sha_file "${prefix}.referrers.json")" '{
          signatureVerificationSha256: $signature,
          provenanceVerificationSha256: $provenance,
          sbomVerificationSha256: $sbom,
          referrersSha256: $referrers
        }' >"${prefix}.summary.json"
}

primary_digest="$(resolve_digest "${release_repository}:${candidate_id}")"
cosign verify \
    --certificate-identity-regexp "$certificate_identity_regexp" \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com \
    "${release_repository}@${primary_digest}" >"$output_dir/evidence/aggregate-primary.signature.json"
oras pull "${release_repository}@${primary_digest}" -o "$output_dir/payload" >/dev/null
oras manifest fetch "${release_repository}@${primary_digest}" \
    >"$output_dir/evidence/aggregate-primary.oci.json"

manifest="$output_dir/payload/scanner-release.json"
aggregate_sbom="$output_dir/payload/scanner-release.spdx.json"
qualification_receipt="$output_dir/payload/candidate-qualification.json"
[[ -f "$manifest" && -f "$aggregate_sbom" && -f "$qualification_receipt" ]] || {
    echo "candidate aggregate is missing its manifest, SPDX document, or qualification receipt" >&2
    exit 1
}
definition_commit="$(jq -er '.definitionCommit | select(test("^[a-f0-9]{40}$"))' "$manifest")"
definition_digest="$(jq -er '.definitionDigest | select(test("^sha256:[a-f0-9]{64}$"))' "$manifest")"
lock_digest="$(jq -er '.lockDigest | select(test("^sha256:[a-f0-9]{64}$"))' "$manifest")"
image_release_id="$candidate_id"
aggregate_source_commit="$definition_commit"

jq -e \
    --arg subject_kind "$subject_kind" \
    --arg release_id "$candidate_id" \
    --arg lock "$lock_digest" \
    --arg definition "$definition_digest" '
  .mediaType == "application/vnd.oci.image.manifest.v1+json"
  and .artifactType == "application/vnd.wolf.scanner.release.v1"
  and .annotations["dev.wolf.release.id"] == $release_id
  and .annotations["dev.wolf.release.lock-digest"] == $lock
  and .annotations["dev.wolf.release.definition-digest"] == $definition
  and ([.layers[] | select(.mediaType == "application/vnd.wolf.scanner.release.manifest.v1+json")] | length) == 1
  and ([.layers[] | select(.mediaType == "application/spdx+json")] | length) == 1
  and ([.layers[] | select(.mediaType == "application/vnd.wolf.scanner.candidate-qualification.v1+json")] | length) == 1
  and (if $subject_kind == "release" then
         ([.layers[] | select(.mediaType == "application/vnd.wolf.scanner.candidate-verification.v1+json")] | length) == 1
         and ([.layers[] | select(.mediaType == "application/vnd.wolf.scanner.protected-approval.v2+json")] | length) == 1
       else
         ([.layers[]] | length) == 3
       end)
' "$output_dir/evidence/aggregate-primary.oci.json" >/dev/null || {
    echo "aggregate OCI artifact type, annotations, or payload media types are invalid" >&2
    exit 1
}

if [[ "$subject_kind" == candidate ]]; then
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
    and .signatureVerified == true
    and .provenanceVerified == true
    and .sbomVerified == true)
' "$manifest" >/dev/null || {
      echo "candidate aggregate manifest is incomplete or internally inconsistent" >&2
      exit 1
  }
else
  approval="$output_dir/payload/protected-approval.json"
  candidate_report="$output_dir/payload/candidate-verification.json"
  [[ -f "$approval" && -f "$candidate_report" ]] || {
      echo "release aggregate is missing approval or candidate-verification evidence" >&2
      exit 1
  }
  image_release_id="$(jq -er '.promotedFrom.candidateId | select(test("^scanner-candidate-[a-z0-9][a-z0-9._-]{0,94}$"))' "$manifest")"
  aggregate_source_commit="$(jq -er '.promotionCommit | select(test("^[a-f0-9]{40}$"))' "$manifest")"
  approval_digest="$(sha_file "$approval")"
  candidate_report_digest="$(sha_file "$candidate_report")"
  jq -e \
      --arg release "$candidate_id" \
      --arg candidate "$image_release_id" \
      --arg approval "$approval_digest" \
      --arg report "$candidate_report_digest" '
    . as $root
    | .schemaVersion == "wolf.scanners.release/v1"
    and .releaseId == $release
    and .operation == "release"
    and .approvalReceiptDigest == $approval
    and .promotedFrom.candidateId == $candidate
    and .promotedFrom.verificationEvidenceDigest == $report
    and (.promotedFrom.candidateManifestDigest | test("^sha256:[a-f0-9]{64}$"))
    and (.promotedFrom.candidateManifestSha256 | test("^sha256:[a-f0-9]{64}$"))
    and ([.images[].variant] | unique | length) == 8
    and all(.images[];
      .releaseId == $candidate
      and .lockDigest == $root.lockDigest
      and .definitionDigest == $root.definitionDigest
      and .approvalReceiptDigest == null
      and (.digest | test("^sha256:[a-f0-9]{64}$"))
      and .primary.verified == true
      and .mirror.verified == true
      and .signatureVerified == true
      and .provenanceVerified == true
      and .sbomVerified == true)
  ' "$manifest" >/dev/null || {
      echo "release aggregate is incomplete or does not retain the exact candidate image closure" >&2
      exit 1
  }
  candidate_manifest_digest="$(jq -er '.promotedFrom.candidateManifestDigest' "$manifest")"
  candidate_manifest_sha="$(jq -er '.promotedFrom.candidateManifestSha256' "$manifest")"
  jq -e \
      --arg release "$candidate_id" \
      --arg candidate "$image_release_id" \
      --arg candidate_digest "$candidate_manifest_digest" \
      --arg report "$candidate_report_digest" \
      --arg lock "$lock_digest" \
      --arg definition "$definition_digest" \
      --arg commit "$definition_commit" \
      --arg promotion_commit "$aggregate_source_commit" '
    .schemaVersion == "wolf.scanners.protected-approval/v2"
    and .releaseId == $release
    and .candidateId == $candidate
    and .candidateManifestDigest == $candidate_digest
    and .verificationEvidenceDigest == $report
    and .lockDigest == $lock
    and .definitionDigest == $definition
    and .definitionCommit == $commit
    and .workflowDefinitionCommit == $promotion_commit
    and .protectedEnvironment == "scanner-release"
    and .approvalEnforcedBy == "github-protected-environment"
  ' "$approval" >/dev/null
  jq -e \
      --arg candidate "$image_release_id" \
      --arg candidate_digest "$candidate_manifest_digest" \
      --arg candidate_manifest_sha "$candidate_manifest_sha" \
      --arg lock "$lock_digest" \
      --arg definition "$definition_digest" \
      --arg commit "$definition_commit" '
    .schemaVersion == "wolf.scanners.candidate-verification/v1"
    and .candidate.id == $candidate
    and .candidate.ociDigest == $candidate_digest
    and .candidate.manifestSha256 == $candidate_manifest_sha
    and .candidate.lockDigest == $lock
    and .candidate.definitionDigest == $definition
    and .candidate.definitionCommit == $commit
    and .mirrorRequired == true
    and .closureVerified == true
    and (.images | length) == 8
  ' "$candidate_report" >/dev/null
fi

if [[ "$subject_kind" == release ]]; then
    jq -e --arg candidate "$image_release_id" \
        '.annotations["dev.wolf.release.promoted-from"] == $candidate' \
        "$output_dir/evidence/aggregate-primary.oci.json" >/dev/null || {
        echo "release aggregate is missing the exact promoted candidate annotation" >&2
        exit 1
    }
else
    jq -e '(.annotations["dev.wolf.release.promoted-from"] // "") == ""' \
        "$output_dir/evidence/aggregate-primary.oci.json" >/dev/null || {
        echo "candidate aggregate unexpectedly declares a promoted release source" >&2
        exit 1
    }
fi

expected_qualification="$(jq -er '.qualificationReceipt.sha256 | select(test("^sha256:[a-f0-9]{64}$"))' "$manifest")"
actual_qualification="$(sha_file "$qualification_receipt")"
[[ "$actual_qualification" == "$expected_qualification" ]] || {
    echo "candidate qualification receipt digest does not match the release manifest" >&2
    exit 1
}
jq -e \
    --arg release "$image_release_id" \
    --arg lock "$lock_digest" \
    --arg definition "$definition_digest" '
  .schemaVersion == "wolf.scanners.candidate-qualification/v1"
  and .releaseId == $release
  and .lockDigest == $lock
  and .definitionDigest == $definition
  and .platformReceiptCount == 15
  and .passed == true
  and all(.evidence[]; test("^sha256:[a-f0-9]{64}$"))
' "$qualification_receipt" >/dev/null || {
    echo "candidate qualification receipt is incomplete or bound to another definition" >&2
    exit 1
}

primary_base="${release_repository%/wolf-scanner-releases}"
mirror_base="${mirror_release_repository%/wolf-scanner-releases}"
[[ "$primary_base" != "$release_repository" && "$mirror_base" != "$mirror_release_repository" ]] || {
    echo "release repositories must use the canonical wolf-scanner-releases aggregate name" >&2
    exit 2
}
jq -e \
    --arg primary "$primary_base" \
    --arg mirror "$mirror_base" \
    --argjson require_mirror "$require_mirror" '
  all(.images[];
    .primary.repository == ($primary + "/" + .image)
    and (if $require_mirror then
           .mirror.repository == ($mirror + "/" + .image)
         else
           (.mirror == null or .mirror.repository == ($mirror + "/" + .image))
         end))
' "$manifest" >/dev/null || {
    echo "release manifest image repositories do not match the canonical primary/mirror mapping" >&2
    exit 1
}

expected_sbom="$(jq -er '.aggregateSbom.sha256 | select(test("^sha256:[a-f0-9]{64}$"))' "$manifest")"
actual_sbom="$(sha_file "$aggregate_sbom")"
[[ "$actual_sbom" == "$expected_sbom" ]] || {
    echo "aggregate SPDX digest does not match the candidate manifest" >&2
    exit 1
}
jq -e '.spdxVersion == "SPDX-2.3"' "$aggregate_sbom" >/dev/null

# Repeat the aggregate verification now that the signed manifest has supplied
# the exact source commit used by GitHub provenance policy.
verify_subject "$release_repository" "$primary_digest" "$aggregate_source_commit" aggregate-primary

mirror_digest=""
if [[ "$require_mirror" == true ]]; then
    mirror_digest="$(resolve_digest "${mirror_release_repository}:${candidate_id}")"
    [[ "$mirror_digest" == "$primary_digest" ]] || {
        echo "candidate aggregate mirror digest differs from primary" >&2
        exit 1
    }
    verify_subject "$mirror_release_repository" "$mirror_digest" "$aggregate_source_commit" aggregate-mirror
else
    jq -nS '{notRequired: true}' >"$output_dir/evidence/aggregate-mirror.summary.json"
fi

images_json="$output_dir/evidence/images.jsonl"
: >"$images_json"
source_url="${GITHUB_SERVER_URL}/${GITHUB_REPOSITORY}"
while IFS=$'\t' read -r variant image_kind digest platforms primary mirror; do
    [[ -n "$mirror" || "$require_mirror" != true ]] || {
        echo "candidate image $variant has no verified mirror" >&2
        exit 1
    }
    scanners/ci/verify-image.sh \
        "${primary}@${digest}" "$digest" "$platforms" \
        "$image_release_id" "$lock_digest" "$definition_digest" \
        "$source_url" "$definition_commit" "$image_release_id" \
        "$variant" "$image_kind"
    verify_subject "$primary" "$digest" "$definition_commit" "${variant}-primary"
    primary_evidence="$(jq -c . "$output_dir/evidence/${variant}-primary.summary.json")"
    mirror_evidence=null
    mirror_verified=false
    if [[ -n "$mirror" ]]; then
        scanners/ci/verify-image.sh \
            "${mirror}@${digest}" "$digest" "$platforms" \
            "$image_release_id" "$lock_digest" "$definition_digest" \
            "$source_url" "$definition_commit" "$image_release_id" \
            "$variant" "$image_kind"
        verify_subject "$mirror" "$digest" "$definition_commit" "${variant}-mirror"
        mirror_evidence="$(jq -c . "$output_dir/evidence/${variant}-mirror.summary.json")"
        mirror_verified=true
    fi
    jq -nS \
        --arg variant "$variant" \
        --arg image_kind "$image_kind" \
        --arg digest "$digest" \
        --arg primary "$primary" \
        --arg mirror "$mirror" \
        --argjson primary_evidence "$primary_evidence" \
        --argjson mirror_evidence "$mirror_evidence" \
        --argjson mirror_verified "$mirror_verified" '{
          variant: $variant,
          imageKind: $image_kind,
          digest: $digest,
          primaryRepository: $primary,
          mirrorRepository: (if $mirror == "" then null else $mirror end),
          primaryVerified: true,
          mirrorVerified: $mirror_verified,
          primaryEvidence: $primary_evidence,
          mirrorEvidence: $mirror_evidence
        }' >>"$images_json"
done < <(
    jq -r '.images | sort_by(.variant)[] | [
      .variant,
      .imageKind,
      .digest,
      (.platforms | join(",")),
      .primary.repository,
      (.mirror.repository // "")
    ] | @tsv' "$manifest"
)

aggregate_mirror_evidence=null
if [[ "$require_mirror" == true ]]; then
    aggregate_mirror_evidence="$(jq -c . "$output_dir/evidence/aggregate-mirror.summary.json")"
fi
report_path="$output_dir/candidate-verification.json"
if [[ "$subject_kind" == candidate ]]; then
  jq -nS \
    --arg candidate "$candidate_id" \
    --arg oci_digest "$primary_digest" \
    --arg manifest_sha "$(sha_file "$manifest")" \
    --arg lock "$lock_digest" \
    --arg definition "$definition_digest" \
    --arg commit "$definition_commit" \
    --arg primary_repository "$release_repository" \
    --arg mirror_repository "$mirror_release_repository" \
    --arg aggregate_sbom "$actual_sbom" \
    --argjson mirror_required "$require_mirror" \
    --argjson primary_evidence "$(jq -c . "$output_dir/evidence/aggregate-primary.summary.json")" \
    --argjson mirror_evidence "$aggregate_mirror_evidence" \
    --slurpfile images "$images_json" '{
      schemaVersion: "wolf.scanners.candidate-verification/v1",
      candidate: {
        id: $candidate,
        ociDigest: $oci_digest,
        manifestSha256: $manifest_sha,
        lockDigest: $lock,
        definitionDigest: $definition,
        definitionCommit: $commit
      },
      mirrorRequired: $mirror_required,
      aggregate: {
        sbomSha256: $aggregate_sbom,
        primary: ({repository: $primary_repository} + $primary_evidence),
        mirror: (if $mirror_evidence == null then null else ({repository: $mirror_repository} + $mirror_evidence) end)
      },
      images: $images,
      closureVerified: true
    }' >"$report_path"
else
  report_path="$output_dir/release-verification.json"
  jq -nS \
      --arg release "$candidate_id" \
      --arg candidate "$image_release_id" \
      --arg oci_digest "$primary_digest" \
      --arg manifest_sha "$(sha_file "$manifest")" \
      --arg lock "$lock_digest" \
      --arg definition "$definition_digest" \
      --arg definition_commit "$definition_commit" \
      --arg promotion_commit "$aggregate_source_commit" \
      --arg primary_repository "$release_repository" \
      --arg mirror_repository "$mirror_release_repository" \
      --arg aggregate_sbom "$actual_sbom" \
      --argjson mirror_required "$require_mirror" \
      --argjson primary_evidence "$(jq -c . "$output_dir/evidence/aggregate-primary.summary.json")" \
      --argjson mirror_evidence "$aggregate_mirror_evidence" \
      --slurpfile images "$images_json" '{
        schemaVersion: "wolf.scanners.release-verification/v1",
        release: {
          id: $release,
          promotedCandidateId: $candidate,
          ociDigest: $oci_digest,
          manifestSha256: $manifest_sha,
          lockDigest: $lock,
          definitionDigest: $definition,
          definitionCommit: $definition_commit,
          promotionCommit: $promotion_commit
        },
        mirrorRequired: $mirror_required,
        aggregate: {
          sbomSha256: $aggregate_sbom,
          primary: ({repository: $primary_repository} + $primary_evidence),
          mirror: (if $mirror_evidence == null then null else ({repository: $mirror_repository} + $mirror_evidence) end)
        },
        images: $images,
        closureVerified: true
      }' >"$report_path"
fi

report_digest="$(sha_file "$report_path")"
printf 'candidate_manifest_digest=%s\n' "$primary_digest"
printf 'verification_evidence_digest=%s\n' "$report_digest"
printf 'definition_commit=%s\n' "$definition_commit"
printf 'definition_digest=%s\n' "$definition_digest"
printf 'lock_digest=%s\n' "$lock_digest"
if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
    {
        printf 'candidate_manifest_digest=%s\n' "$primary_digest"
        printf 'verification_evidence_digest=%s\n' "$report_digest"
        printf 'definition_commit=%s\n' "$definition_commit"
        printf 'definition_digest=%s\n' "$definition_digest"
        printf 'lock_digest=%s\n' "$lock_digest"
    } >>"$GITHUB_OUTPUT"
fi
