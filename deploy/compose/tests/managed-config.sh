#!/usr/bin/env bash
set -euo pipefail

project_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"

# Import Compose's resolved interpolation environment without evaluating .env
# as shell code. Explicit process environment values retain precedence.
while IFS='=' read -r name value; do
  if [[ "$name" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]] && [[ -z "${!name+x}" ]]; then
    printf -v "$name" '%s' "$value"
    export "${name?}"
  fi
done < <(docker compose --project-directory "$project_dir" config --environment)

fail() {
  printf 'managed Compose configuration: %s\n' "$1" >&2
  exit 1
}

require_value() {
  local name="$1"
  [[ -n "${!name:-}" ]] || fail "$name is required"
}

require_absolute_file() {
  local name="$1"
  local maximum="${2:-16777216}"
  require_value "$name"
  [[ "${!name}" == /* ]] || fail "$name must be an absolute host path"
  [[ ! -L "${!name}" ]] || fail "$name must not be a symbolic link"
  [[ -f "${!name}" ]] || fail "$name must name a regular file"
  local size
  size="$(wc -c <"${!name}")"
  [[ "$size" -gt 0 && "$size" -le "$maximum" ]] ||
    fail "$name must be a non-empty bounded regular file"
}

require_absolute_directory() {
  local name="$1"
  require_value "$name"
  [[ "${!name}" == /* ]] || fail "$name must be an absolute host path"
  [[ ! -L "${!name}" ]] || fail "$name must not be a symbolic link"
  [[ -d "${!name}" ]] || fail "$name must name a directory"
}

require_immutable_image() {
  local name="$1"
  require_value "$name"
  [[ "${!name}" =~ ^[^[:space:]@]+@sha256:[0-9a-f]{64}$ ]] ||
    fail "$name must be image@sha256:<64-lowercase-hex>"
}

require_dns_name() {
  local name="$1"
  require_value "$name"
  [[ "${!name}" =~ ^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$ ]] ||
    fail "$name must be a Kubernetes DNS name"
}

require_optional_dns_name() {
  local name="$1"
  [[ -z "${!name:-}" ]] || require_dns_name "$name"
}

require_credential_or_identity() {
  local secret_name="$1"
  local identity_name="$2"
  local identity="${!identity_name:-false}"
  [[ "$identity" == "true" || "$identity" == "false" ]] ||
    fail "$identity_name must be true or false"
  [[ -n "${!secret_name:-}" || "$identity" == "true" ]] ||
    fail "$secret_name or $identity_name=true is required"
}

require_value WOLF_SCANNER_RELEASE_SOURCE_URL
[[ "$WOLF_SCANNER_RELEASE_SOURCE_URL" =~ ^https://[^/@?#]+/[^@?#]+$ ]] ||
  fail "WOLF_SCANNER_RELEASE_SOURCE_URL must be credential-free HTTPS without query or fragment"
require_value WOLF_SCANNER_RELEASE_PRIMARY_REGISTRY_ID
require_value WOLF_SCANNER_RELEASE_MIRROR_REGISTRY_ID
[[ "$WOLF_SCANNER_RELEASE_PRIMARY_REGISTRY_ID" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$ ]] ||
  fail "WOLF_SCANNER_RELEASE_PRIMARY_REGISTRY_ID is invalid"
[[ "$WOLF_SCANNER_RELEASE_MIRROR_REGISTRY_ID" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$ ]] ||
  fail "WOLF_SCANNER_RELEASE_MIRROR_REGISTRY_ID is invalid"
[[ "$WOLF_SCANNER_RELEASE_PRIMARY_REGISTRY_ID" != "$WOLF_SCANNER_RELEASE_MIRROR_REGISTRY_ID" ]] ||
  fail "primary and mirror registry IDs must differ"

require_immutable_image WOLF_SCANNER_RELEASE_STEP_IMAGE
require_immutable_image WOLF_SCANNER_RELEASE_FIXED_ADAPTER_IMAGE
require_immutable_image WOLF_SCANNER_RELEASE_QUALITY_ADAPTER_IMAGE
require_immutable_image WOLF_SCANNER_RELEASE_INTEGRATION_ADAPTER_IMAGE

require_value WOLF_SCANNER_RELEASE_K8S_API_SERVER
[[ "$WOLF_SCANNER_RELEASE_K8S_API_SERVER" =~ ^https://[^/?#]+$ ]] ||
  fail "WOLF_SCANNER_RELEASE_K8S_API_SERVER must be an HTTPS origin"
require_dns_name WOLF_SCANNER_RELEASE_K8S_NAMESPACE
require_dns_name WOLF_SCANNER_RELEASE_K8S_INSTANCE
require_dns_name WOLF_SCANNER_RELEASE_BUILDX_NAMESPACE
[[ "$WOLF_SCANNER_RELEASE_K8S_NAMESPACE" != "$WOLF_SCANNER_RELEASE_BUILDX_NAMESPACE" ]] ||
  fail "Buildx and release Jobs must use separate namespaces"
require_dns_name WOLF_SCANNER_RELEASE_WORKSPACE_PVC
require_dns_name WOLF_SCANNER_RELEASE_BUILDKIT_SERVICE_ACCOUNT
require_dns_name WOLF_SCANNER_RELEASE_FIXED_SERVICE_ACCOUNT
require_dns_name WOLF_SCANNER_RELEASE_QUALITY_SERVICE_ACCOUNT
require_dns_name WOLF_SCANNER_RELEASE_INTEGRATION_SERVICE_ACCOUNT
require_dns_name WOLF_SCANNER_RELEASE_SIGNER_SERVICE_ACCOUNT

require_credential_or_identity \
  WOLF_SCANNER_RELEASE_FIXED_REGISTRY_CREDENTIAL_SECRET \
  WOLF_SCANNER_RELEASE_FIXED_REGISTRY_CREDENTIAL_WORKLOAD_IDENTITY
require_credential_or_identity \
  WOLF_SCANNER_RELEASE_QUALITY_REGISTRY_CREDENTIAL_SECRET \
  WOLF_SCANNER_RELEASE_QUALITY_REGISTRY_CREDENTIAL_WORKLOAD_IDENTITY
require_credential_or_identity \
  WOLF_SCANNER_RELEASE_INTEGRATION_REGISTRY_CREDENTIAL_SECRET \
  WOLF_SCANNER_RELEASE_INTEGRATION_REGISTRY_CREDENTIAL_WORKLOAD_IDENTITY
require_value WOLF_SCANNER_RELEASE_FIXED_REGISTRY_CREDENTIAL_SECRET
require_value WOLF_SCANNER_RELEASE_QUALITY_REGISTRY_CREDENTIAL_SECRET
require_value WOLF_SCANNER_RELEASE_QUALITY_ENGINE_CREDENTIAL_SECRET
require_value WOLF_SCANNER_RELEASE_INTEGRATION_REGISTRY_CREDENTIAL_SECRET
require_value WOLF_SCANNER_RELEASE_INTEGRATION_ENGINE_CREDENTIAL_SECRET
require_value WOLF_SCANNER_SIGNER_PROFILE_SECRET
require_credential_or_identity \
  WOLF_SCANNER_SIGNER_CREDENTIAL_SECRET \
  WOLF_SCANNER_SIGNER_WORKLOAD_IDENTITY
require_dns_name WOLF_SCANNER_SIGNER_PROFILE_SECRET
require_optional_dns_name WOLF_SCANNER_SIGNER_CREDENTIAL_SECRET
require_optional_dns_name WOLF_SCANNER_RELEASE_FIXED_REGISTRY_CREDENTIAL_SECRET
require_optional_dns_name WOLF_SCANNER_RELEASE_QUALITY_REGISTRY_CREDENTIAL_SECRET
require_optional_dns_name WOLF_SCANNER_RELEASE_QUALITY_ENGINE_CREDENTIAL_SECRET
require_optional_dns_name WOLF_SCANNER_RELEASE_INTEGRATION_REGISTRY_CREDENTIAL_SECRET
require_optional_dns_name WOLF_SCANNER_RELEASE_INTEGRATION_ENGINE_CREDENTIAL_SECRET

identities=(
  "$WOLF_SCANNER_RELEASE_BUILDKIT_SERVICE_ACCOUNT"
  "$WOLF_SCANNER_RELEASE_FIXED_SERVICE_ACCOUNT"
  "$WOLF_SCANNER_RELEASE_QUALITY_SERVICE_ACCOUNT"
  "$WOLF_SCANNER_RELEASE_INTEGRATION_SERVICE_ACCOUNT"
  "$WOLF_SCANNER_RELEASE_SIGNER_SERVICE_ACCOUNT"
)
declare -A seen_identities=()
for identity in "${identities[@]}"; do
  [[ -z "${seen_identities[$identity]:-}" ]] || fail "managed service accounts must be distinct"
  seen_identities[$identity]=1
done

secrets=(
  "$WOLF_SCANNER_SIGNER_PROFILE_SECRET"
  "${WOLF_SCANNER_SIGNER_CREDENTIAL_SECRET:-}"
  "${WOLF_SCANNER_RELEASE_FIXED_REGISTRY_CREDENTIAL_SECRET:-}"
  "${WOLF_SCANNER_RELEASE_QUALITY_REGISTRY_CREDENTIAL_SECRET:-}"
  "${WOLF_SCANNER_RELEASE_QUALITY_ENGINE_CREDENTIAL_SECRET:-}"
  "${WOLF_SCANNER_RELEASE_INTEGRATION_REGISTRY_CREDENTIAL_SECRET:-}"
  "${WOLF_SCANNER_RELEASE_INTEGRATION_ENGINE_CREDENTIAL_SECRET:-}"
)
declare -A seen_secrets=()
for secret in "${secrets[@]}"; do
  [[ -z "$secret" ]] && continue
  [[ -z "${seen_secrets[$secret]:-}" ]] || fail "managed signer and adapter Secrets must be distinct"
  seen_secrets[$secret]=1
done

require_absolute_directory WOLF_SCANNER_RELEASE_WORKSPACE_HOST
require_absolute_file WOLF_SCANNER_RELEASE_KUBECONFIG_HOST_FILE 1048576
require_absolute_file WOLF_SCANNER_RELEASE_K8S_TOKEN_HOST_FILE 65536
require_absolute_file WOLF_SCANNER_RELEASE_K8S_CA_HOST_FILE 1048576
require_absolute_directory WOLF_SCANNER_RELEASE_BUILDX_DOCKER_CONFIG_HOST_DIR
[[ -f "$WOLF_SCANNER_RELEASE_BUILDX_DOCKER_CONFIG_HOST_DIR/config.json" ]] ||
  fail "Buildx Docker config directory must contain config.json"
require_absolute_file WOLF_SCANNER_SIGNER_PROFILE_HOST_FILE 1048576
require_absolute_directory WOLF_SCANNER_SIGNER_CREDENTIAL_HOST_DIR
require_absolute_file WOLF_SCANNER_SIGNER_ADAPTER_HOST_FILE
[[ -x "$WOLF_SCANNER_SIGNER_ADAPTER_HOST_FILE" ]] ||
  fail "WOLF_SCANNER_SIGNER_ADAPTER_HOST_FILE must be executable"
if [[ -n "${WOLF_SCANNER_RELEASE_GIT_AUTHORIZATION_FILE:-}" || -n "${WOLF_SCANNER_RELEASE_GIT_AUTHORIZATION_HOST_FILE:-}" ]]; then
  [[ "${WOLF_SCANNER_RELEASE_GIT_AUTHORIZATION_FILE:-}" == "/run/wolf/git/authorization" ]] ||
    fail "WOLF_SCANNER_RELEASE_GIT_AUTHORIZATION_FILE must use the fixed container target"
  require_absolute_file WOLF_SCANNER_RELEASE_GIT_AUTHORIZATION_HOST_FILE 65536
fi

python3 - "$WOLF_SCANNER_RELEASE_BUILDX_DOCKER_CONFIG_HOST_DIR/config.json" <<'PY'
import json
import os
import stat
import sys

path = sys.argv[1]
info = os.stat(path, follow_symlinks=False)
if not stat.S_ISREG(info.st_mode) or info.st_size <= 0 or info.st_size > 65536:
    raise SystemExit("managed Compose configuration: config.json must be a bounded regular file")
with open(path, "rb") as handle:
    document = json.load(handle)
auths = document.get("auths") if isinstance(document, dict) else None
if not isinstance(auths, dict) or len(auths) != 1:
    raise SystemExit("managed Compose configuration: config.json must contain exactly one auths entry")
PY

docker compose --project-directory "$project_dir" \
  --profile scanner-release-managed config --quiet
printf 'Managed Compose configuration checks passed\n'
