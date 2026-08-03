#!/usr/bin/env bash
set -euo pipefail

lock_file="${1:-scanners/quality/trivy-db.lock.json}"
evidence_file="${2:-}"

[[ -f "$lock_file" ]] || {
  echo "Trivy database lock is missing: $lock_file" >&2
  exit 1
}
repository="$(jq -er '.repository' "$lock_file")"
digest="$(jq -er '.digest' "$lock_file")"
recorded_at="$(jq -er '.recordedAt' "$lock_file")"
expires_at="$(jq -er '.expiresAt' "$lock_file")"
case "$repository" in
  ghcr.io/aquasecurity/trivy-db|ghcr.io/aquasecurity/trivy-java-db) ;;
  *)
    echo "unexpected Trivy database repository" >&2
    exit 1
    ;;
esac
[[ "$digest" =~ ^sha256:[a-f0-9]{64}$ ]] || {
  echo "invalid Trivy database digest" >&2
  exit 1
}
now_epoch="$(date -u +%s)"
recorded_epoch="$(jq -nr --arg value "$recorded_at" '$value | fromdateiso8601')"
expires_epoch="$(jq -nr --arg value "$expires_at" '$value | fromdateiso8601')"
(( recorded_epoch <= now_epoch + 300 && now_epoch < expires_epoch )) || {
  echo "Trivy database identity is not currently valid" >&2
  exit 1
}

manifest="$(
  docker buildx imagetools inspect "${repository}@${digest}" \
    --format '{{json .Manifest}}'
)"
resolved="$(jq -er '.digest' <<<"$manifest")"
[[ "$resolved" == "$digest" ]] || {
  echo "Trivy database digest mismatch: resolved $resolved, expected $digest" >&2
  exit 1
}

if [[ -n "$evidence_file" ]]; then
  mkdir -p "$(dirname "$evidence_file")"
  jq -n \
    --arg repository "$repository" \
    --arg digest "$digest" \
    --arg recordedAt "$recorded_at" \
    --arg expiresAt "$expires_at" \
    --arg verifiedAt "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    --arg databaseKind "$(if [[ "$repository" == *trivy-java-db ]]; then printf java; else printf vulnerability; fi)" \
    '{
      schemaVersion: "wolf.scanners/vulnerability-db-evidence/v1",
      provider: "trivy",
      databaseKind: $databaseKind,
      repository: $repository,
      digest: $digest,
      recordedAt: $recordedAt,
      expiresAt: $expiresAt,
      verifiedAt: $verifiedAt
    }' >"$evidence_file"
fi

printf '%s\n' "${repository}@${digest}"
