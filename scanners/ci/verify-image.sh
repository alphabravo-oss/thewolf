#!/usr/bin/env bash
# Verify an OCI image/index digest and its exact required platform set.
set -euo pipefail

ref="${1:-}"
expected_digest="${2:-}"
required_csv="${3:-}"
expected_release_id="${4:-}"
expected_lock_digest="${5:-}"
expected_definition_digest="${6:-}"
expected_source="${7:-}"
expected_revision="${8:-}"
expected_version="${9:-}"
expected_variant="${10:-}"
expected_image_kind="${11:-}"

[[ -n "$ref" ]] || { echo "usage: verify-image.sh REF DIGEST PLATFORMS [RELEASE_ID LOCK_DIGEST DEFINITION_DIGEST SOURCE REVISION VERSION VARIANT IMAGE_KIND]" >&2; exit 2; }
[[ "$expected_digest" =~ ^sha256:[a-f0-9]{64}$ ]] ||
    { echo "invalid expected digest: $expected_digest" >&2; exit 2; }
[[ -n "$required_csv" ]] || { echo "required platforms are empty" >&2; exit 2; }
if [[ -n "$expected_release_id$expected_lock_digest$expected_definition_digest$expected_source$expected_revision$expected_version$expected_variant$expected_image_kind" ]]; then
    [[ "$expected_release_id" =~ ^[a-z0-9][a-z0-9._-]{0,126}$ ]] || {
        echo "invalid expected release ID" >&2
        exit 2
    }
    for value in "$expected_lock_digest" "$expected_definition_digest"; do
        [[ "$value" =~ ^sha256:[a-f0-9]{64}$ ]] || {
            echo "invalid expected definition identity" >&2
            exit 2
        }
    done
    [[ "$expected_source" =~ ^https://[^[:space:]]+$ ]] || { echo "invalid expected source URL" >&2; exit 2; }
    [[ "$expected_revision" =~ ^[a-f0-9]{40}$ ]] || { echo "invalid expected source revision" >&2; exit 2; }
    [[ "$expected_version" == "$expected_release_id" ]] || { echo "version must match release identity" >&2; exit 2; }
    [[ "$expected_variant" =~ ^(default|jvm|rust|codeql|fixer-base|fixer-api|fixer-claude|fixer-codex|fixer-engines)$ ]] || {
        echo "invalid expected image variant" >&2
        exit 2
    }
    [[ "$expected_image_kind" == scanner || "$expected_image_kind" == fixer ]] || {
        echo "invalid expected image kind" >&2
        exit 2
    }
fi

# A tag read immediately after it is written can still resolve to the previous
# manifest: GHCR is eventually consistent, and a verify that runs a second or
# two behind an `oras copy` sees the stale digest and fails a publish that in
# fact succeeded. Re-read a few times with a short backoff before treating a
# mismatch as real. A genuinely wrong digest still fails, just a little later.
descriptor=""
manifest=""
actual_digest=""
for attempt in 1 2 3 4 5; do
    descriptor="$(docker buildx imagetools inspect "$ref" --format '{{json .Manifest}}')"
    manifest="$(docker buildx imagetools inspect --raw "$ref")"
    actual_digest="$(jq -r '.digest // empty' <<<"$descriptor")"
    [[ "$actual_digest" != "$expected_digest" ]] || break
    if [[ "$attempt" -lt 5 ]]; then
        printf 'digest for %s not settled yet (expected %s, got %s); retrying\n' \
            "$ref" "$expected_digest" "${actual_digest:-missing}" >&2
        sleep $((attempt * 3))
    fi
done
[[ "$actual_digest" == "$expected_digest" ]] || {
    printf 'digest mismatch for %s: expected %s, got %s\n' "$ref" "$expected_digest" "${actual_digest:-missing}" >&2
    exit 1
}

if [[ -n "$expected_release_id" ]]; then
    actual_release_id="$(jq -r '.annotations["dev.wolf.release.id"] // empty' <<<"$manifest")"
    actual_lock_digest="$(jq -r '.annotations["dev.wolf.release.lock-digest"] // empty' <<<"$manifest")"
    actual_definition_digest="$(jq -r '.annotations["dev.wolf.release.definition-digest"] // empty' <<<"$manifest")"
    actual_source="$(jq -r '.annotations["org.opencontainers.image.source"] // empty' <<<"$manifest")"
    actual_revision="$(jq -r '.annotations["org.opencontainers.image.revision"] // empty' <<<"$manifest")"
    actual_version="$(jq -r '.annotations["org.opencontainers.image.version"] // empty' <<<"$manifest")"
    actual_variant="$(jq -r '.annotations["dev.wolf.release.variant"] // empty' <<<"$manifest")"
    actual_image_kind="$(jq -r '.annotations["dev.wolf.release.image-kind"] // empty' <<<"$manifest")"
    actual_platform_annotation="$(jq -r '.annotations["dev.wolf.release.platforms"] // empty' <<<"$manifest")"
    [[ "$actual_release_id" == "$expected_release_id" &&
       "$actual_lock_digest" == "$expected_lock_digest" &&
       "$actual_definition_digest" == "$expected_definition_digest" &&
       "$actual_source" == "$expected_source" &&
       "$actual_revision" == "$expected_revision" &&
       "$actual_version" == "$expected_version" &&
       "$actual_variant" == "$expected_variant" &&
       "$actual_image_kind" == "$expected_image_kind" &&
       "$actual_platform_annotation" == "$required_csv" ]] || {
        printf 'release annotations for %s do not match the exact source/revision/version/variant/kind/platform/lock identity\n' "$ref" >&2
        exit 1
    }
fi

actual_platforms=()
while IFS= read -r platform; do
    [[ -n "$platform" ]] && actual_platforms+=("$platform")
done < <(
    jq -r '
      if .manifests then
        .manifests[]
        | select(.platform.os != "unknown" and .platform.architecture != "unknown")
        | .platform.os + "/" + .platform.architecture
      else empty end
    ' <<<"$descriptor" | sort -u
)

IFS=, read -ra required_platforms <<<"$required_csv"
if [[ ${#actual_platforms[@]} -eq 0 && ${#required_platforms[@]} -eq 1 ]]; then
    actual_platforms=("${required_platforms[0]//[[:space:]]/}")
fi

for platform in "${required_platforms[@]}"; do
    platform="${platform//[[:space:]]/}"
    [[ "$platform" =~ ^linux/(amd64|arm64)$ ]] || {
        printf 'unsupported required platform %s\n' "$platform" >&2
        exit 2
    }
    if ! printf '%s\n' "${actual_platforms[@]}" | grep -Fxq "$platform"; then
        printf 'required platform %s missing from %s (found: %s)\n' \
            "$platform" "$ref" "${actual_platforms[*]:-none}" >&2
        exit 1
    fi
done

if [[ ${#actual_platforms[@]} -ne ${#required_platforms[@]} ]]; then
    printf 'unexpected platform set for %s (required: %s; found: %s)\n' \
        "$ref" "$required_csv" "${actual_platforms[*]:-none}" >&2
    exit 1
fi

printf 'verified %s at %s for platforms %s\n' "$ref" "$actual_digest" "$required_csv"
