#!/bin/sh
set -eu

if [ "${WOLF_RUN_ROLLOUT_COMPOSE_E2E:-0}" != "1" ]; then
  echo "SKIP: set WOLF_RUN_ROLLOUT_COMPOSE_E2E=1 to run real Compose rollout E2E"
  exit 0
fi

qualification_dir="${WOLF_RELEASE_QUALIFICATION_DIR:-/usr/local/libexec/wolf/release-qualification}"

for dependency in docker jq; do
  command -v "$dependency" >/dev/null 2>&1 || {
    echo "$dependency is required for Compose rollout E2E" >&2
    exit 2
  }
done
docker compose version >/dev/null
test -x "$qualification_dir/scanner-rollout-qualification.test" || {
  echo "trusted rollout qualification binary is unavailable in $qualification_dir" >&2
  exit 2
}

script_dir="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
adapter="$script_dir/scanner-rollout-compose-fixture-adapter.sh"
test -x "$adapter" || {
  echo "Compose rollout fixture adapter is unavailable" >&2
  exit 2
}

resolve_image() {
  requested="$1"
  case "$requested" in
    *@sha256:*)
      # An exact reference is already resolved. The trusted image-cache
      # implementation below performs the actual pull and digest readback.
      # Avoid a redundant pull here so remote-engine and local-mirror test
      # topologies do not have to expose the registry through the CLI
      # container's own network namespace as well.
      printf '%s\n' "$requested"
      ;;
    *)
      docker pull "$requested" >/dev/null
      docker image inspect --format '{{index .RepoDigests 0}}' "$requested"
      ;;
  esac
}

WOLF_ROLLOUT_E2E_NEW_IMAGE="$(
  resolve_image "${WOLF_ROLLOUT_COMPOSE_E2E_NEW_TAG:-docker.io/library/alpine:3.20}"
)"
export WOLF_ROLLOUT_E2E_NEW_IMAGE
WOLF_ROLLOUT_E2E_OLD_IMAGE="$(
  resolve_image "${WOLF_ROLLOUT_COMPOSE_E2E_OLD_TAG:-docker.io/library/alpine:3.19}"
)"
export WOLF_ROLLOUT_E2E_OLD_IMAGE
export WOLF_ROLLOUT_COMPOSE_E2E_ADAPTER="$adapter"

"$qualification_dir/scanner-rollout-qualification.test" \
  -test.run '^TestRealComposeCohortLifecycleAndRollback$' \
  -test.count=1 -test.v
