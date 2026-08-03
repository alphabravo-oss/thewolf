#!/bin/sh
set -eu

# Test-only Compose adapter used by scanner-rollout-compose.sh. It deliberately
# supports one image so an exact running-container image ID can be read back
# without pretending to be a general production deployment adapter.

: "${WOLF_ROLLOUT_COMPOSE_E2E_ROOT:?absolute E2E root is required}"
case "$WOLF_ROLLOUT_COMPOSE_E2E_ROOT" in
  /*) ;;
  *) echo "WOLF_ROLLOUT_COMPOSE_E2E_ROOT must be absolute" >&2; exit 2 ;;
esac

for dependency in docker jq; do
  command -v "$dependency" >/dev/null 2>&1 || {
    echo "$dependency is required" >&2
    exit 2
  }
done

payload="$(mktemp "${TMPDIR:-/tmp}/wolf-compose-assignment.XXXXXX")"
trap 'rm -f "$payload"' EXIT HUP INT TERM
dd of="$payload" bs=1048576 count=2 2>/dev/null

action="$(jq -er '.action' "$payload")"
assignment="$(jq -ec '.assignment' "$payload")"
cohort_id="$(jq -er '.cohort_id' <<EOF
$assignment
EOF
)"
image_count="$(jq -er '.image_references | length' <<EOF
$assignment
EOF
)"
if [ "$image_count" -ne 1 ]; then
  echo "fixture adapter requires exactly one release image" >&2
  exit 2
fi
image_ref="$(jq -er '.image_references | to_entries | sort_by(.key) | .[0].value' <<EOF
$assignment
EOF
)"
case "$image_ref" in
  *@sha256:????????????????????????????????????????????????????????????????) ;;
  *) echo "fixture adapter requires an exact sha256 image reference" >&2; exit 2 ;;
esac

if command -v sha256sum >/dev/null 2>&1; then
  cohort_key="$(printf '%s' "$cohort_id" | sha256sum | cut -c1-20)"
else
  cohort_key="$(printf '%s' "$cohort_id" | shasum -a 256 | cut -c1-20)"
fi
state_dir="$WOLF_ROLLOUT_COMPOSE_E2E_ROOT/$cohort_key"
compose_file="$state_dir/compose.yml"
project="wolfrollout${cohort_key}"
mkdir -p "$state_dir"

case "$action" in
  apply)
    cat >"$compose_file" <<EOF
services:
  scanner:
    image: '$image_ref'
    command: ["sh", "-c", "trap : TERM INT; sleep 3600 & wait"]
    labels:
      wolf.dev/scanner-cohort: '$cohort_id'
EOF
    docker compose --project-name "$project" --file "$compose_file" \
      up --detach --pull always --force-recreate
    container_id="$(docker compose --project-name "$project" --file "$compose_file" ps --quiet scanner)"
    [ -n "$container_id" ] || {
      echo "Compose did not return a scanner container" >&2
      exit 1
    }
    expected_image_id="$(docker image inspect --format '{{.Id}}' "$image_ref")"
    observed_image_id="$(docker inspect --format '{{.Image}}' "$container_id")"
    [ "$expected_image_id" = "$observed_image_id" ] || {
      echo "running Compose container does not use the exact pulled image" >&2
      exit 1
    }
    observed_at="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
    jq -cn \
      --argjson assignment "$assignment" \
      --arg observed_at "$observed_at" \
      '{
        operation_id: $assignment.operation_id,
        release_id: $assignment.release_id,
        manifest_digest: $assignment.manifest_digest,
        image_digests: $assignment.image_digests,
        ready: true,
        observed_at: $observed_at
      }'
    ;;
  paused)
    docker compose --project-name "$project" --file "$compose_file" pause
    ;;
  resumed)
    docker compose --project-name "$project" --file "$compose_file" unpause
    ;;
  cancelled)
    docker compose --project-name "$project" --file "$compose_file" down --remove-orphans
    ;;
  *)
    echo "unsupported Compose fixture action: $action" >&2
    exit 2
    ;;
esac
