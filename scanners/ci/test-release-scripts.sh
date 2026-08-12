#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$repo_root"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

fail() {
    printf 'FAIL: %s\n' "$*" >&2
    exit 1
}

assert_output() {
    local output="$1"
    local expected="$2"
    grep -Fxq "$expected" <<<"$output" || fail "missing output: $expected"
}

common=(
    GIT_SHA=0123456789abcdef0123456789abcdef01234567
    REF_NAME=main
    RUN_ID=12345
    RUN_ATTEMPT=2
)

output="$(env "${common[@]}" EVENT_NAME=schedule EVENT_SCHEDULE='17 2 * * *' \
    scanners/ci/release-meta.sh resolve)"
assert_output "$output" "operation=discover"
assert_output "$output" "run_discovery=true"
assert_output "$output" "run_build=false"
assert_output "$output" "run_vulnerability_db_refresh=true"

output="$(env "${common[@]}" EVENT_NAME=workflow_dispatch INPUT_OPERATION=validate \
    scanners/ci/release-meta.sh resolve)"
assert_output "$output" "run_validation=true"
assert_output "$output" "run_build=false"

output="$(env "${common[@]}" EVENT_NAME=schedule EVENT_SCHEDULE='43 3 * * 0' \
    scanners/ci/release-meta.sh resolve)"
assert_output "$output" "operation=candidate"
assert_output "$output" "run_build=true"
assert_output "$output" "run_validation=true"
assert_output "$output" "publish=true"
assert_output "$output" "aliases=candidate"
assert_output "$output" "run_os_package_refresh=false"
grep -Eq '^immutable_id=scanner-candidate-[0-9]{4}-w[0-9]{2}-0123456789ab-r12345-a2$' \
    <<<"$output" || fail "scheduled candidate identity is not attempt-unique"
retry_output="$(env GIT_SHA=0123456789abcdef0123456789abcdef01234567 \
    REF_NAME=main RUN_ID=12345 RUN_ATTEMPT=3 \
    EVENT_NAME=schedule EVENT_SCHEDULE='43 3 * * 0' \
    scanners/ci/release-meta.sh resolve)"
retry_id="$(awk -F= '$1 == "immutable_id" {print $2}' <<<"$retry_output")"
original_id="$(awk -F= '$1 == "immutable_id" {print $2}' <<<"$output")"
[[ "$retry_id" != "$original_id" && "$retry_id" == *-r12345-a3 ]] ||
    fail "candidate retry reused an immutable candidate identity"

output="$(env "${common[@]}" EVENT_NAME=push \
    scanners/ci/release-meta.sh resolve)"
assert_output "$output" "operation=candidate"
assert_output "$output" "run_discovery=true"
assert_output "$output" "run_validation=true"
assert_output "$output" "run_build=true"
assert_output "$output" "publish=false"
assert_output "$output" "aliases="
assert_output "$output" "immutable_id=scanner-candidate-main-0123456789ab-r12345-a2"

output="$(env "${common[@]}" EVENT_NAME=workflow_dispatch \
    INPUT_OPERATION=refresh-os-packages \
    INPUT_OS_PACKAGE_SNAPSHOT=20260730T000000Z \
    scanners/ci/release-meta.sh resolve)"
assert_output "$output" "operation=refresh-os-packages"
assert_output "$output" "run_os_package_refresh=true"
assert_output "$output" "run_build=false"
assert_output "$output" "os_package_snapshot=20260730T000000Z"

output="$(env "${common[@]}" EVENT_NAME=workflow_dispatch \
    INPUT_OPERATION=refresh-vulnerability-dbs \
    scanners/ci/release-meta.sh resolve)"
assert_output "$output" "operation=refresh-vulnerability-dbs"
assert_output "$output" "run_vulnerability_db_refresh=true"
assert_output "$output" "run_build=false"

if env "${common[@]}" EVENT_NAME=workflow_dispatch \
    INPUT_OPERATION=refresh-os-packages \
    INPUT_OS_PACKAGE_SNAPSHOT=latest \
    scanners/ci/release-meta.sh resolve >/dev/null 2>&1; then
    fail "invalid OS package snapshot was accepted"
fi

output="$(env "${common[@]}" EVENT_NAME=pull_request REF_NAME='123/merge' \
    scanners/ci/release-meta.sh resolve)"
assert_output "$output" "publish=false"
assert_output "$output" "immutable_id=scanner-candidate-pr-123-merge-0123456789ab"

output="$(env "${common[@]}" EVENT_NAME=workflow_dispatch INPUT_OPERATION=release \
    INPUT_CANDIDATE_ID=scanner-candidate-2026-w31-0123456789ab \
    INPUT_RELEASE_ID=scanner-set-2026.31.1 INPUT_CHANNEL=stable \
    INPUT_MIRROR_MODE=disabled scanners/ci/release-meta.sh resolve)"
assert_output "$output" "immutable_id=scanner-set-2026.31.1"
assert_output "$output" "candidate_id=scanner-candidate-2026-w31-0123456789ab"
assert_output "$output" "aliases=stable"
assert_output "$output" "mirror_mode=disabled"
assert_output "$output" "run_build=false"

if env "${common[@]}" EVENT_NAME=workflow_dispatch INPUT_OPERATION=release \
    INPUT_RELEASE_ID=scanner-set-2026.31.1 INPUT_CHANNEL=stable \
    INPUT_MIRROR_MODE=required scanners/ci/release-meta.sh resolve >/dev/null 2>&1; then
    fail "release without an exact candidate ID was accepted"
fi

if env GIT_SHA=0123456789abcdef0123456789abcdef01234567 \
    REF_NAME=feature RUN_ID=123 RUN_ATTEMPT=1 EVENT_NAME=workflow_dispatch \
    INPUT_OPERATION=candidate INPUT_PUBLISH=true \
    scanners/ci/release-meta.sh resolve >/dev/null 2>&1; then
    fail "managed candidate dispatch from an untrusted branch was accepted"
fi

for unapproved_operation in candidate security-rebuild; do
    if env "${common[@]}" EVENT_NAME=workflow_dispatch \
        INPUT_OPERATION="$unapproved_operation" INPUT_CHANNEL=stable \
        INPUT_PUBLISH=true INPUT_MIRROR_MODE=disabled \
        scanners/ci/release-meta.sh resolve >/dev/null 2>&1; then
        fail "$unapproved_operation operation was allowed to move stable"
    fi
done
output="$(env "${common[@]}" EVENT_NAME=workflow_dispatch INPUT_OPERATION=release \
    INPUT_CANDIDATE_ID=scanner-candidate-2026-w31-0123456789ab \
    INPUT_RELEASE_ID=scanner-set-2026.31.1 INPUT_CHANNEL=stable \
    INPUT_MIRROR_MODE=auto scanners/ci/release-meta.sh resolve)"
assert_output "$output" "aliases=stable"
assert_output "$output" "mirror_mode=auto"

if env "${common[@]}" EVENT_NAME=workflow_dispatch INPUT_OPERATION=release \
    INPUT_CANDIDATE_ID=scanner-candidate-2026-w31-0123456789ab \
    INPUT_RELEASE_ID=latest scanners/ci/release-meta.sh resolve >/dev/null 2>&1; then
    fail "unsafe release ID was accepted"
fi
if env "${common[@]}" EVENT_NAME=release RELEASE_TAG_NAME=v2.0.0-RC1 \
    scanners/ci/release-meta.sh resolve >/dev/null 2>&1; then
    fail "non-lower-case OCI release alias was accepted"
fi

for image_name in \
    wolf-scanners wolf-scanners-jvm wolf-scanners-rust wolf-scanners-codeql \
    wolf-fixer wolf-fixer-api wolf-fixer-claude wolf-fixer-codex wolf-fixer-engines; do
    output="$(env PRIMARY_REPOSITORY=ghcr.io/example MIRROR_REPOSITORY=docker.io/example \
        IMAGE_NAME="$image_name" IMMUTABLE_ID=scanner-set-2026.31.1 \
        ALIASES=stable RUN_ID=123 RUN_ATTEMPT=1 scanners/ci/release-meta.sh tags)"
    assert_output "$output" "primary_repository=ghcr.io/example/${image_name}"
    assert_output "$output" "staging_tag=build-scanner-set-2026.31.1-123-1"
done
if env PRIMARY_REPOSITORY=ghcr.io/example MIRROR_REPOSITORY=docker.io/example \
    IMAGE_NAME=wolf-fixer-claude-extra IMMUTABLE_ID=scanner-set-2026.31.1 \
    ALIASES=stable RUN_ID=123 RUN_ATTEMPT=1 \
    scanners/ci/release-meta.sh tags >/dev/null 2>&1; then
    fail "non-canonical image name was accepted"
fi

digest="sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
sbom_digest="sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
lock_digest="sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
definition_digest="sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
approval_digest="sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
evidence_digest="sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
candidate_id=scanner-candidate-2026-w31-0123456789ab
jq -nS \
    --arg release "$candidate_id" \
    --arg lock "$lock_digest" \
    --arg definition "$definition_digest" \
    --arg evidence "$evidence_digest" '{
      schemaVersion: "wolf.scanners.candidate-qualification/v1",
      releaseId: $release,
      lockDigest: $lock,
      definitionDigest: $definition,
      candidateImage: "ghcr.io/example/wolf-scanners@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
      platformReceiptCount: 8,
      evidence: {
        platformReceiptsSha256: $evidence,
        composeScannerSha256: $evidence,
        composeRolloutSha256: $evidence,
        kindScannerSha256: $evidence,
        kindRolloutSha256: $evidence
      },
      passed: true
    }' >"$tmp/candidate-qualification.json"
qualification_digest="sha256:$(sha256sum "$tmp/candidate-qualification.json" | awk '{print $1}')"

mkdir -p "$tmp/bin"
cat >"$tmp/bin/docker" <<'SH'
#!/usr/bin/env sh
set -eu
[ "$1 $2 $3" = "buildx imagetools inspect" ] || exit 90
if [ "${4:-}" = "--raw" ]; then
  printf '%s\n' "${DOCKER_MANIFEST:?}"
  exit 0
fi
printf '%s\n' "${DOCKER_DESCRIPTOR:?}"
SH
chmod 700 "$tmp/bin/docker"
descriptor="$(jq -cn \
    --arg digest "$digest" \
    --arg release scanner-set-2026.31.1 \
    --arg lock "$lock_digest" \
    --arg definition "$definition_digest" \
    '{
      digest: $digest,
      annotations: {
        "org.opencontainers.image.source": "https://github.com/example/wolf",
        "org.opencontainers.image.revision": "0123456789abcdef0123456789abcdef01234567",
        "org.opencontainers.image.version": $release,
        "dev.wolf.release.variant": "default",
        "dev.wolf.release.image-kind": "scanner",
        "dev.wolf.release.platforms": "linux/amd64",
        "dev.wolf.release.id": $release,
        "dev.wolf.release.lock-digest": $lock,
        "dev.wolf.release.definition-digest": $definition
      },
      manifests: [
        {platform: {os: "linux", architecture: "amd64"}}
      ]
    }')"
PATH="$tmp/bin:$PATH" DOCKER_DESCRIPTOR="$descriptor" DOCKER_MANIFEST="$descriptor" \
    scanners/ci/verify-image.sh \
    "ghcr.io/example/wolf-scanners@$digest" "$digest" \
    "linux/amd64" scanner-set-2026.31.1 \
    "$lock_digest" "$definition_digest" \
    "https://github.com/example/wolf" \
    0123456789abcdef0123456789abcdef01234567 \
    scanner-set-2026.31.1 default scanner >/dev/null
bad_descriptor="$(jq 'del(.annotations["dev.wolf.release.lock-digest"])' <<<"$descriptor")"
if PATH="$tmp/bin:$PATH" DOCKER_DESCRIPTOR="$bad_descriptor" DOCKER_MANIFEST="$bad_descriptor" \
    scanners/ci/verify-image.sh \
    "ghcr.io/example/wolf-scanners@$digest" "$digest" \
    "linux/amd64" scanner-set-2026.31.1 \
    "$lock_digest" "$definition_digest" \
    "https://github.com/example/wolf" \
    0123456789abcdef0123456789abcdef01234567 \
    scanner-set-2026.31.1 default scanner >/dev/null 2>&1; then
    fail "image index without the exact scanner lock annotation was accepted"
fi

for variant in default jvm rust codeql fixer-base fixer-api fixer-claude fixer-codex; do
    platforms='["linux/amd64"]'
    image_kind=scanner
    case "$variant" in
        default) image=wolf-scanners ;;
        jvm|rust|codeql) image="wolf-scanners-${variant}" ;;
        fixer-base) image=wolf-fixer; image_kind=fixer ;;
        fixer-api|fixer-claude|fixer-codex) image="wolf-${variant}"; image_kind=fixer ;;
    esac
    base_reference=""
    case "$variant" in
        fixer-api|fixer-claude|fixer-codex)
            base_reference="ghcr.io/example/wolf-fixer@${digest}"
            ;;
    esac
    jq -n \
        --arg variant "$variant" \
        --arg image_kind "$image_kind" \
        --arg image "$image" \
        --arg digest "$digest" \
        --arg sbom "$sbom_digest" \
        --arg base_reference "$base_reference" \
        --arg release_id scanner-set-2026.31.1 \
        --arg lock_digest "$lock_digest" \
        --arg definition_digest "$definition_digest" \
        --arg approval_digest "$approval_digest" \
        --arg evidence_digest "$evidence_digest" \
        --argjson platforms "$platforms" \
        '{
          variant: $variant,
          imageKind: $image_kind,
          image: $image,
          releaseId: $release_id,
          lockDigest: $lock_digest,
          definitionDigest: $definition_digest,
          approvalReceiptDigest: $approval_digest,
          digest: $digest,
          platforms: $platforms,
          baseReference: (if $base_reference == "" then null else $base_reference end),
          primary: {repository: ("ghcr.io/example/" + $image), verified: true},
          mirror: null,
          sbom_sha256: $sbom,
          evidence: {
            signatureVerificationSha256: $evidence_digest,
            provenanceVerificationSha256: $evidence_digest,
            sbomVerificationSha256: $evidence_digest,
            referrersSha256: $evidence_digest
          },
          signatureVerified: false,
          provenanceVerified: false,
          sbomVerified: false
        }' >"$tmp/${variant}.image.json"
    jq -n \
        --arg namespace "https://example.test/spdx/${variant}" \
        '{
          spdxVersion: "SPDX-2.3",
          dataLicense: "CC0-1.0",
          SPDXID: "SPDXRef-DOCUMENT",
          name: "fixture",
          documentNamespace: $namespace,
          creationInfo: {
            created: "2026-07-30T12:00:00Z",
            creators: ["Tool: fixture"]
          }
        }' >"$tmp/${variant}.spdx.json"
done

scanners/ci/aggregate-spdx.py \
    --release-id scanner-set-2026.31.1 \
    --created 2026-07-30T12:00:00Z \
    --output "$tmp/aggregate.spdx.json" \
    "$tmp"/*.spdx.json \
    >/dev/null
aggregate_sbom_sha="sha256:$(sha256sum "$tmp/aggregate.spdx.json" | awk '{print $1}')"

SCANNER_RELEASE_ID=scanner-set-2026.31.1 \
DEFINITION_COMMIT=0123456789abcdef0123456789abcdef01234567 \
SOURCE_DATE=2026-07-30T12:00:00Z \
OPERATION=release \
AGGREGATE_SBOM_SHA256="$aggregate_sbom_sha" \
LOCK_DIGEST="$lock_digest" \
DEFINITION_DIGEST="$definition_digest" \
QUALIFICATION_RECEIPT_SHA256="$qualification_digest" \
APPROVAL_RECEIPT_SHA256="$approval_digest" \
    scanners/ci/build-release-manifest.sh \
    "$tmp/release.json" \
    "$tmp"/*.image.json \
    >/dev/null

jq -e '
  .schemaVersion == "wolf.scanners.release/v1"
  and .releaseId == "scanner-set-2026.31.1"
  and .lockDigest == "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
  and .definitionDigest == "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
  and .approvalReceiptDigest == "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
  and (.images | length) == 8
  and ([.images[].imageKind] | map(select(. == "scanner")) | length) == 4
  and ([.images[].imageKind] | map(select(. == "fixer")) | length) == 4
  and .images[0].variant == "codeql"
  and (.aggregateSbom.sha256 | startswith("sha256:"))
' "$tmp/release.json" >/dev/null || fail "canonical release manifest is invalid"

jq '.baseReference = "ghcr.io/example/wolf-fixer@sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"' \
    "$tmp/fixer-codex.image.json" >"$tmp/fixer-codex-bad.image.json"
if SCANNER_RELEASE_ID=scanner-set-2026.31.1 \
    DEFINITION_COMMIT=0123456789abcdef0123456789abcdef01234567 \
    SOURCE_DATE=2026-07-30T12:00:00Z \
    OPERATION=release \
    AGGREGATE_SBOM_SHA256="$aggregate_sbom_sha" \
    LOCK_DIGEST="$lock_digest" \
    DEFINITION_DIGEST="$definition_digest" \
    QUALIFICATION_RECEIPT_SHA256="$qualification_digest" \
    APPROVAL_RECEIPT_SHA256="$approval_digest" \
    scanners/ci/build-release-manifest.sh \
        "$tmp/release-bad.json" \
        "$tmp/default.image.json" "$tmp/jvm.image.json" \
        "$tmp/rust.image.json" "$tmp/codeql.image.json" \
        "$tmp/fixer-base.image.json" "$tmp/fixer-api.image.json" \
        "$tmp/fixer-claude.image.json" "$tmp/fixer-codex-bad.image.json" \
        >/dev/null 2>&1; then
    fail "fixer engine with a mismatched base digest was accepted"
fi

jq '.digest = "sha256:bad"' \
    "$tmp/default.image.json" >"$tmp/default-bad-digest.image.json"
if SCANNER_RELEASE_ID=scanner-set-2026.31.1 \
    DEFINITION_COMMIT=0123456789abcdef0123456789abcdef01234567 \
    SOURCE_DATE=2026-07-30T12:00:00Z \
    OPERATION=release \
    AGGREGATE_SBOM_SHA256="$aggregate_sbom_sha" \
    LOCK_DIGEST="$lock_digest" \
    DEFINITION_DIGEST="$definition_digest" \
    QUALIFICATION_RECEIPT_SHA256="$qualification_digest" \
    APPROVAL_RECEIPT_SHA256="$approval_digest" \
    scanners/ci/build-release-manifest.sh \
        "$tmp/release-bad-digest.json" \
        "$tmp/default-bad-digest.image.json" "$tmp/jvm.image.json" \
        "$tmp/rust.image.json" "$tmp/codeql.image.json" \
        "$tmp/fixer-base.image.json" "$tmp/fixer-api.image.json" \
        "$tmp/fixer-claude.image.json" "$tmp/fixer-codex.image.json" \
        >/dev/null 2>&1; then
    fail "image with invalid digest was accepted"
fi

for variant in default jvm rust codeql fixer-base fixer-api fixer-claude fixer-codex; do
    jq --arg candidate "$candidate_id" '
      .releaseId = $candidate
      | .approvalReceiptDigest = null
      | .mirror = {
          repository: ("docker.io/example/" + .image),
          verified: true,
          referrersSha256: .evidence.referrersSha256,
          signatureVerified: false,
          provenanceVerified: false,
          sbomVerified: false
        }
    ' "$tmp/${variant}.image.json" >"$tmp/${variant}.candidate.image.json"
done
SCANNER_RELEASE_ID="$candidate_id" \
DEFINITION_COMMIT=0123456789abcdef0123456789abcdef01234567 \
SOURCE_DATE=2026-07-30T12:00:00Z \
OPERATION=candidate \
AGGREGATE_SBOM_SHA256="$aggregate_sbom_sha" \
LOCK_DIGEST="$lock_digest" \
DEFINITION_DIGEST="$definition_digest" \
QUALIFICATION_RECEIPT_SHA256="$qualification_digest" \
    scanners/ci/build-release-manifest.sh \
    "$tmp/candidate.json" \
    "$tmp"/*.candidate.image.json >/dev/null
candidate_oci_digest="sha256:1111111111111111111111111111111111111111111111111111111111111111"
candidate_manifest_sha="sha256:$(sha256sum "$tmp/candidate.json" | awk '{print $1}')"
jq -nS \
    --arg candidate "$candidate_id" \
    --arg oci "$candidate_oci_digest" \
    --arg manifest_sha "$candidate_manifest_sha" \
    --arg lock "$lock_digest" \
    --arg definition "$definition_digest" \
    --arg commit 0123456789abcdef0123456789abcdef01234567 \
    --arg evidence "$evidence_digest" \
    --slurpfile images <(jq -s '[.[] | {
      variant, imageKind, digest,
      primaryVerified: true,
      mirrorVerified: true
    }]' "$tmp"/*.candidate.image.json) '
    {
      schemaVersion: "wolf.scanners.candidate-verification/v1",
      candidate: {
        id: $candidate,
        ociDigest: $oci,
        manifestSha256: $manifest_sha,
        lockDigest: $lock,
        definitionDigest: $definition,
        definitionCommit: $commit
      },
      mirrorRequired: true,
      aggregate: {
        primary: {
          signatureVerificationSha256: $evidence,
          provenanceVerificationSha256: $evidence,
          sbomVerificationSha256: $evidence,
          referrersSha256: $evidence
        },
        mirror: {
          signatureVerificationSha256: $evidence,
          provenanceVerificationSha256: $evidence,
          sbomVerificationSha256: $evidence,
          referrersSha256: $evidence
        }
      },
      images: $images[0],
      closureVerified: true
    }
  ' >"$tmp/candidate-verification.json"
verification_sha="sha256:$(sha256sum "$tmp/candidate-verification.json" | awk '{print $1}')"
jq -nS \
    --arg release scanner-set-2026.31.2 \
    --arg candidate "$candidate_id" \
    --arg candidate_digest "$candidate_oci_digest" \
    --arg verification "$verification_sha" \
    --arg lock "$lock_digest" \
    --arg definition "$definition_digest" \
    --arg definition_commit 0123456789abcdef0123456789abcdef01234567 \
    --arg workflow_commit fedcba9876543210fedcba9876543210fedcba98 '
    {
      schemaVersion: "wolf.scanners.protected-approval/v2",
      releaseId: $release,
      candidateId: $candidate,
      candidateManifestDigest: $candidate_digest,
      verificationEvidenceDigest: $verification,
      lockDigest: $lock,
      definitionDigest: $definition,
      definitionCommit: $definition_commit,
      workflowDefinitionCommit: $workflow_commit,
      workflowRun: {id: "12345", attempt: "2"},
      protectedEnvironment: "scanner-release",
      approvalEnforcedBy: "github-protected-environment"
    }
  ' >"$tmp/approval.json"
approval_sha="sha256:$(sha256sum "$tmp/approval.json" | awk '{print $1}')"
RELEASE_ID=scanner-set-2026.31.2 \
SOURCE_DATE=2026-07-31T12:00:00Z \
PROMOTION_COMMIT=fedcba9876543210fedcba9876543210fedcba98 \
    scanners/ci/promote-release-manifest.sh \
    "$tmp/candidate.json" "$candidate_oci_digest" \
    "$tmp/candidate-verification.json" "$tmp/approval.json" \
    "$tmp/promoted-release.json" >/dev/null
jq -e \
    --arg candidate "$candidate_id" \
    --arg candidate_digest "$candidate_oci_digest" \
    --arg evidence "$verification_sha" \
    --arg approval "$approval_sha" '
  .releaseId == "scanner-set-2026.31.2"
  and .operation == "release"
  and .promotionCommit == "fedcba9876543210fedcba9876543210fedcba98"
  and .approvalReceiptDigest == $approval
  and .promotedFrom.candidateId == $candidate
  and .promotedFrom.candidateManifestDigest == $candidate_digest
  and .promotedFrom.verificationEvidenceDigest == $evidence
  and all(.images[]; .releaseId == $candidate)
' "$tmp/promoted-release.json" >/dev/null || fail "candidate was not promoted by exact identity"

jq '.closureVerified = false' "$tmp/candidate-verification.json" >"$tmp/tampered-verification.json"
if RELEASE_ID=scanner-set-2026.31.2 \
    SOURCE_DATE=2026-07-31T12:00:00Z \
    PROMOTION_COMMIT=fedcba9876543210fedcba9876543210fedcba98 \
    scanners/ci/promote-release-manifest.sh \
      "$tmp/candidate.json" "$candidate_oci_digest" \
      "$tmp/tampered-verification.json" "$tmp/approval.json" \
      "$tmp/tampered-release.json" >/dev/null 2>&1; then
    fail "release accepted candidate evidence that changed after approval"
fi

# Exercise the remote closure verifier with deterministic registry/signing
# doubles. This catches argument, schema, source-binding, and promoted-release
# replay regressions without requiring live registries in the unit suite.
cat >"$tmp/bin/docker" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
[[ "$1 $2 $3" == "buildx imagetools inspect" ]]
if [[ "${4:-}" == "--raw" ]]; then
  ref="$5"
else
  ref="$4"
fi
variant=default
kind=scanner
platforms='[{"platform":{"os":"linux","architecture":"amd64"}}]'
case "$ref" in
  *wolf-scanners-codeql*) variant=codeql ;;
  *wolf-scanners-jvm*) variant=jvm ;;
  *wolf-scanners-rust*) variant=rust ;;
  *wolf-fixer-api*) variant=fixer-api; kind=fixer ;;
  *wolf-fixer-claude*) variant=fixer-claude; kind=fixer ;;
  *wolf-fixer-codex*) variant=fixer-codex; kind=fixer ;;
  *wolf-fixer*) variant=fixer-base; kind=fixer ;;
esac
platform_csv=linux/amd64
manifest="$(jq -cn \
  --arg digest "${IMAGE_DIGEST:?}" \
  --arg candidate "${CLOSURE_CANDIDATE_ID:?}" \
  --arg lock "${CLOSURE_LOCK_DIGEST:?}" \
  --arg definition "${CLOSURE_DEFINITION_DIGEST:?}" \
  --arg variant "$variant" \
  --arg kind "$kind" \
  --arg platforms_csv "$platform_csv" \
  --argjson platforms "$platforms" '{
    digest: $digest,
    annotations: {
      "org.opencontainers.image.source": "https://github.com/example/wolf",
      "org.opencontainers.image.revision": "0123456789abcdef0123456789abcdef01234567",
      "org.opencontainers.image.version": $candidate,
      "dev.wolf.release.variant": $variant,
      "dev.wolf.release.image-kind": $kind,
      "dev.wolf.release.platforms": $platforms_csv,
      "dev.wolf.release.id": $candidate,
      "dev.wolf.release.lock-digest": $lock,
      "dev.wolf.release.definition-digest": $definition
    },
    manifests: $platforms
  }')"
if [[ "${4:-}" == "--raw" ]]; then
  jq -c 'del(.digest)' <<<"$manifest"
else
  printf '%s\n' "$manifest"
fi
SH
cat >"$tmp/bin/oras" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
case "$1 $2" in
  "manifest fetch")
    if [[ "${3:-}" == --descriptor ]]; then
      jq -cn --arg digest "${ORAS_DESCRIPTOR_DIGEST:?}" '{digest: $digest}'
    else
      jq -cn \
        --arg subject_kind "${ORAS_SUBJECT_KIND:?}" \
        --arg release "${ORAS_SUBJECT_ID:?}" \
        --arg candidate "${CLOSURE_CANDIDATE_ID:?}" \
        --arg lock "${CLOSURE_LOCK_DIGEST:?}" \
        --arg definition "${CLOSURE_DEFINITION_DIGEST:?}" '
        def layer($media; $name): {
          mediaType: $media,
          digest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
          size: 1,
          annotations: {"org.opencontainers.image.title": $name}
        };
        {
          schemaVersion: 2,
          mediaType: "application/vnd.oci.image.manifest.v1+json",
          artifactType: "application/vnd.wolf.scanner.release.v1",
          annotations: {
            "dev.wolf.release.id": $release,
            "dev.wolf.release.lock-digest": $lock,
            "dev.wolf.release.definition-digest": $definition,
            "dev.wolf.release.promoted-from": (if $subject_kind == "release" then $candidate else null end)
          },
          layers: ([
            layer("application/vnd.wolf.scanner.release.manifest.v1+json"; "scanner-release.json"),
            layer("application/spdx+json"; "scanner-release.spdx.json"),
            layer("application/vnd.wolf.scanner.candidate-qualification.v1+json"; "candidate-qualification.json")
          ] + (if $subject_kind == "release" then [
            layer("application/vnd.wolf.scanner.candidate-verification.v1+json"; "candidate-verification.json"),
            layer("application/vnd.wolf.scanner.protected-approval.v2+json"; "protected-approval.json")
          ] else [] end))
        }'
    fi
    ;;
  "discover --format")
    jq -cn --arg subject "${ORAS_DESCRIPTOR_DIGEST:?}" '{
      subject: $subject,
      referrers: [
        {artifactType:"application/vnd.dev.cosign.simplesigning.v1+json",digest:"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1"},
        {artifactType:"application/vnd.dev.sigstore.bundle.v0.3+json",digest:"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa2"},
        {artifactType:"application/vnd.dev.sigstore.bundle.v0.3+json",digest:"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa3"}
      ]
    }'
    ;;
  *)
    if [[ "$1" == pull ]]; then
      output=""
      while (($#)); do
        if [[ "$1" == -o ]]; then output="$2"; break; fi
        shift
      done
      [[ -n "$output" ]]
      mkdir -p "$output"
      cp "${ORAS_PAYLOAD_DIR:?}/"* "$output/"
    else
      exit 92
    fi
    ;;
esac
SH
cat >"$tmp/bin/cosign" <<'SH'
#!/usr/bin/env sh
set -eu
[ "$1" = verify ]
printf '[{"critical":{"identity":{"docker-reference":"fixture"}}}]\n'
SH
cat >"$tmp/bin/gh" <<'SH'
#!/usr/bin/env sh
set -eu
[ "$1 $2" = "attestation verify" ]
printf '{"verification":"passed"}\n'
SH
chmod 700 "$tmp/bin/docker" "$tmp/bin/oras" "$tmp/bin/cosign" "$tmp/bin/gh"

mkdir -p "$tmp/candidate-payload" "$tmp/release-payload"
cp "$tmp/candidate.json" "$tmp/candidate-payload/scanner-release.json"
cp "$tmp/aggregate.spdx.json" "$tmp/candidate-payload/scanner-release.spdx.json"
cp "$tmp/candidate-qualification.json" "$tmp/candidate-payload/candidate-qualification.json"
PATH="$tmp/bin:$PATH" \
GITHUB_REPOSITORY=example/wolf GITHUB_SERVER_URL=https://github.com \
ORAS_DESCRIPTOR_DIGEST="$candidate_oci_digest" \
ORAS_SUBJECT_KIND=candidate ORAS_SUBJECT_ID="$candidate_id" \
ORAS_PAYLOAD_DIR="$tmp/candidate-payload" \
IMAGE_DIGEST="$digest" CLOSURE_CANDIDATE_ID="$candidate_id" \
CLOSURE_LOCK_DIGEST="$lock_digest" \
CLOSURE_DEFINITION_DIGEST="$definition_digest" \
  scanners/ci/verify-release-closure.sh \
    ghcr.io/example/wolf-scanner-releases \
    docker.io/example/wolf-scanner-releases \
    "$candidate_id" "$tmp/verified-candidate" true >/dev/null
jq -e --arg candidate "$candidate_id" '
  .schemaVersion == "wolf.scanners.candidate-verification/v1"
  and .candidate.id == $candidate
  and (.images | length) == 8
  and .closureVerified == true
' "$tmp/verified-candidate/candidate-verification.json" >/dev/null || \
  fail "candidate closure verifier did not emit complete evidence"

cp "$tmp/promoted-release.json" "$tmp/release-payload/scanner-release.json"
cp "$tmp/aggregate.spdx.json" "$tmp/release-payload/scanner-release.spdx.json"
cp "$tmp/candidate-qualification.json" "$tmp/release-payload/candidate-qualification.json"
cp "$tmp/candidate-verification.json" "$tmp/release-payload/candidate-verification.json"
cp "$tmp/approval.json" "$tmp/release-payload/protected-approval.json"
release_oci_digest="sha256:2222222222222222222222222222222222222222222222222222222222222222"
PATH="$tmp/bin:$PATH" \
GITHUB_REPOSITORY=example/wolf GITHUB_SERVER_URL=https://github.com \
ORAS_DESCRIPTOR_DIGEST="$release_oci_digest" \
ORAS_SUBJECT_KIND=release ORAS_SUBJECT_ID=scanner-set-2026.31.2 \
ORAS_PAYLOAD_DIR="$tmp/release-payload" \
IMAGE_DIGEST="$digest" CLOSURE_CANDIDATE_ID="$candidate_id" \
CLOSURE_LOCK_DIGEST="$lock_digest" \
CLOSURE_DEFINITION_DIGEST="$definition_digest" \
  scanners/ci/verify-release-closure.sh \
    ghcr.io/example/wolf-scanner-releases \
    docker.io/example/wolf-scanner-releases \
    scanner-set-2026.31.2 "$tmp/verified-release" true >/dev/null
jq -e '
  .schemaVersion == "wolf.scanners.release-verification/v1"
  and .release.id == "scanner-set-2026.31.2"
  and .release.promotedCandidateId == "scanner-candidate-2026-w31-0123456789ab"
  and (.images | length) == 8
  and .closureVerified == true
' "$tmp/verified-release/release-verification.json" >/dev/null || \
  fail "protected release closure verifier did not replay complete evidence"

printf 'scanner release CI script tests: PASS\n'
