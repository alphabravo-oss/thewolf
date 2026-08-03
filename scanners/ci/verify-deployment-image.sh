#!/usr/bin/env bash
# Verify an immutable deployment/control-plane index and its release metadata.
set -euo pipefail

ref="${1:-}"
expected_digest="${2:-}"
release_id="${3:-}"
lock_digest="${4:-}"
definition_digest="${5:-}"
source_url="${6:-}"
revision="${7:-}"
variant="${8:-}"

[[ "$ref" == *:* || "$ref" == *@* ]] || { echo "image reference is required" >&2; exit 2; }
[[ "$expected_digest" =~ ^sha256:[a-f0-9]{64}$ ]] || { echo "invalid expected digest" >&2; exit 2; }
[[ "$release_id" =~ ^deployment-set-[a-z0-9][a-z0-9._-]{0,111}$ ]] || { echo "invalid release ID" >&2; exit 2; }
[[ "$lock_digest" =~ ^sha256:[a-f0-9]{64}$ ]] || { echo "invalid lock digest" >&2; exit 2; }
[[ "$definition_digest" =~ ^sha256:[a-f0-9]{64}$ ]] || { echo "invalid definition digest" >&2; exit 2; }
[[ "$source_url" =~ ^https://[^[:space:]]+$ ]] || { echo "invalid source URL" >&2; exit 2; }
[[ "$revision" =~ ^[a-f0-9]{40}$ ]] || { echo "invalid source revision" >&2; exit 2; }
[[ "$variant" =~ ^(runtime|proposal|release-fixed|release-quality|release-integration)$ ]] || {
    echo "invalid deployment image variant" >&2
    exit 2
}

descriptor="$(docker buildx imagetools inspect "$ref" --format '{{json .Manifest}}')"
actual_digest="$(jq -r '.digest // empty' <<<"$descriptor")"
[[ "$actual_digest" == "$expected_digest" ]] || {
    echo "deployment image digest mismatch for $ref" >&2
    exit 1
}

jq -e \
    --arg release "$release_id" \
    --arg lock "$lock_digest" \
    --arg definition "$definition_digest" \
    --arg source "$source_url" \
    --arg revision "$revision" \
    --arg variant "$variant" '
      .mediaType == "application/vnd.oci.image.index.v1+json" and
      .annotations["dev.wolf.deployment-images.release-id"] == $release and
      .annotations["dev.wolf.deployment-images.lock-digest"] == $lock and
      .annotations["dev.wolf.deployment-images.definition-digest"] == $definition and
      .annotations["dev.wolf.deployment-images.variant"] == $variant and
      .annotations["dev.wolf.deployment-images.image-kind"] == "deployment" and
      .annotations["dev.wolf.deployment-images.platforms"] == "linux/amd64,linux/arm64" and
      .annotations["org.opencontainers.image.source"] == $source and
      .annotations["org.opencontainers.image.revision"] == $revision and
      .annotations["org.opencontainers.image.version"] == $release and
      ([.manifests[]
        | select(.platform.os != "unknown" and .platform.architecture != "unknown")
        | .platform.os + "/" + .platform.architecture] | sort) ==
        ["linux/amd64", "linux/arm64"]
    ' <<<"$descriptor" >/dev/null || {
        echo "deployment image annotations or exact platform set are invalid for $ref" >&2
        exit 1
    }

printf 'verified deployment image %s at %s\n' "$ref" "$expected_digest"
