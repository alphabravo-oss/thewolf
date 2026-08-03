#!/usr/bin/env bash
set -euo pipefail

project_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
validator="$project_dir/deploy/compose/tests/managed-config.sh"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT

mkdir -p "$tmp_dir/workspace" "$tmp_dir/docker" "$tmp_dir/signer-credentials"
printf 'apiVersion: v1\nclusters: []\n' >"$tmp_dir/kubeconfig"
printf 'token\n' >"$tmp_dir/token"
printf 'test-ca\n' >"$tmp_dir/ca.crt"
printf '{"auths":{"registry.example":{"auth":"dGVzdDp0ZXN0"}}}\n' >"$tmp_dir/docker/config.json"
printf '{"schema_version":"wolf.scanner-signer-profile/v1"}\n' >"$tmp_dir/profile.json"
printf '#!/bin/sh\nexit 0\n' >"$tmp_dir/signer-adapter"
chmod 700 "$tmp_dir/signer-adapter"

digest_a="sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
digest_b="sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
digest_c="sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
digest_d="sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"

valid_environment=(
  "WOLF_SCANNER_RELEASE_SOURCE_URL=https://git.example/wolf/scanners.git"
  "WOLF_SCANNER_RELEASE_PRIMARY_REGISTRY_ID=primary"
  "WOLF_SCANNER_RELEASE_MIRROR_REGISTRY_ID=mirror"
  "WOLF_SCANNER_RELEASE_WORKSPACE_HOST=$tmp_dir/workspace"
  "WOLF_SCANNER_RELEASE_WORKSPACE_PVC=wolf-workspace"
  "WOLF_SCANNER_RELEASE_STEP_IMAGE=registry.example/wolf-step@$digest_a"
  "WOLF_SCANNER_RELEASE_K8S_API_SERVER=https://kubernetes.example"
  "WOLF_SCANNER_RELEASE_K8S_NAMESPACE=wolf-release"
  "WOLF_SCANNER_RELEASE_K8S_INSTANCE=wolf-compose"
  "WOLF_SCANNER_RELEASE_BUILDX_NAMESPACE=wolf-buildkit"
  "WOLF_SCANNER_RELEASE_BUILDKIT_SERVICE_ACCOUNT=wolf-buildkit"
  "WOLF_SCANNER_RELEASE_KUBECONFIG_HOST_FILE=$tmp_dir/kubeconfig"
  "WOLF_SCANNER_RELEASE_K8S_TOKEN_HOST_FILE=$tmp_dir/token"
  "WOLF_SCANNER_RELEASE_K8S_CA_HOST_FILE=$tmp_dir/ca.crt"
  "WOLF_SCANNER_RELEASE_BUILDX_DOCKER_CONFIG_HOST_DIR=$tmp_dir/docker"
  "WOLF_SCANNER_RELEASE_FIXED_ADAPTER_IMAGE=registry.example/fixed@$digest_b"
  "WOLF_SCANNER_RELEASE_FIXED_REGISTRY_CREDENTIAL_SECRET=fixed-registry-credential"
  "WOLF_SCANNER_RELEASE_FIXED_REGISTRY_CREDENTIAL_WORKLOAD_IDENTITY=false"
  "WOLF_SCANNER_RELEASE_FIXED_SERVICE_ACCOUNT=wolf-fixed"
  "WOLF_SCANNER_RELEASE_QUALITY_ADAPTER_IMAGE=registry.example/quality@$digest_c"
  "WOLF_SCANNER_RELEASE_QUALITY_REGISTRY_CREDENTIAL_SECRET=quality-registry-credential"
  "WOLF_SCANNER_RELEASE_QUALITY_ENGINE_CREDENTIAL_SECRET=quality-engine-credential"
  "WOLF_SCANNER_RELEASE_QUALITY_REGISTRY_CREDENTIAL_WORKLOAD_IDENTITY=false"
  "WOLF_SCANNER_RELEASE_QUALITY_SERVICE_ACCOUNT=wolf-quality"
  "WOLF_SCANNER_RELEASE_INTEGRATION_ADAPTER_IMAGE=registry.example/integration@$digest_d"
  "WOLF_SCANNER_RELEASE_INTEGRATION_REGISTRY_CREDENTIAL_SECRET=integration-registry-credential"
  "WOLF_SCANNER_RELEASE_INTEGRATION_ENGINE_CREDENTIAL_SECRET=integration-engine-credential"
  "WOLF_SCANNER_RELEASE_INTEGRATION_REGISTRY_CREDENTIAL_WORKLOAD_IDENTITY=false"
  "WOLF_SCANNER_RELEASE_INTEGRATION_SERVICE_ACCOUNT=wolf-integration"
  "WOLF_SCANNER_SIGNER_PROFILE_HOST_FILE=$tmp_dir/profile.json"
  "WOLF_SCANNER_SIGNER_CREDENTIAL_HOST_DIR=$tmp_dir/signer-credentials"
  "WOLF_SCANNER_SIGNER_ADAPTER_HOST_FILE=$tmp_dir/signer-adapter"
  "WOLF_SCANNER_SIGNER_PROFILE_SECRET=signer-profile"
  "WOLF_SCANNER_SIGNER_CREDENTIAL_SECRET=signer-credential"
  "WOLF_SCANNER_SIGNER_WORKLOAD_IDENTITY=false"
  "WOLF_SCANNER_RELEASE_SIGNER_SERVICE_ACCOUNT=wolf-signer"
)

env "${valid_environment[@]}" bash "$validator" >/dev/null

if env "${valid_environment[@]}" WOLF_SCANNER_RELEASE_SOURCE_URL= bash "$validator" >/dev/null 2>&1; then
  echo "expected missing managed source URL to fail" >&2
  exit 1
fi
if env "${valid_environment[@]}" WOLF_SCANNER_RELEASE_FIXED_ADAPTER_IMAGE=registry.example/fixed:latest bash "$validator" >/dev/null 2>&1; then
  echo "expected mutable managed adapter image to fail" >&2
  exit 1
fi
if env "${valid_environment[@]}" WOLF_SCANNER_RELEASE_STEP_IMAGE=registry.example/wolf-step:latest bash "$validator" >/dev/null 2>&1; then
  echo "expected mutable managed step image to fail" >&2
  exit 1
fi
if env "${valid_environment[@]}" WOLF_SCANNER_RELEASE_BUILDX_DOCKER_CONFIG_HOST_DIR= bash "$validator" >/dev/null 2>&1; then
  echo "expected missing Buildx Docker config directory to fail" >&2
  exit 1
fi
if env "${valid_environment[@]}" WOLF_SCANNER_RELEASE_BUILDX_NAMESPACE=wolf-release bash "$validator" >/dev/null 2>&1; then
  echo "expected managed Buildx and Job namespace collision to fail" >&2
  exit 1
fi
if env "${valid_environment[@]}" WOLF_SCANNER_RELEASE_FIXED_SERVICE_ACCOUNT=wolf-signer bash "$validator" >/dev/null 2>&1; then
  echo "expected repeated managed service account to fail" >&2
  exit 1
fi
if env "${valid_environment[@]}" WOLF_SCANNER_RELEASE_FIXED_REGISTRY_CREDENTIAL_SECRET=signer-credential bash "$validator" >/dev/null 2>&1; then
  echo "expected repeated managed credential Secret to fail" >&2
  exit 1
fi
if env "${valid_environment[@]}" \
  WOLF_SCANNER_RELEASE_FIXED_REGISTRY_CREDENTIAL_SECRET= \
  WOLF_SCANNER_RELEASE_FIXED_REGISTRY_CREDENTIAL_WORKLOAD_IDENTITY=true \
  bash "$validator" >/dev/null 2>&1; then
  echo "expected missing fixed registry credential Secret to fail" >&2
  exit 1
fi
if env "${valid_environment[@]}" \
  WOLF_SCANNER_RELEASE_QUALITY_ENGINE_CREDENTIAL_SECRET= \
  WOLF_SCANNER_RELEASE_QUALITY_REGISTRY_CREDENTIAL_WORKLOAD_IDENTITY=true \
  bash "$validator" >/dev/null 2>&1; then
  echo "expected missing quality remote-engine credential Secret to fail" >&2
  exit 1
fi
if env "${valid_environment[@]}" \
  WOLF_SCANNER_RELEASE_INTEGRATION_ENGINE_CREDENTIAL_SECRET= \
  WOLF_SCANNER_RELEASE_INTEGRATION_REGISTRY_CREDENTIAL_WORKLOAD_IDENTITY=true \
  bash "$validator" >/dev/null 2>&1; then
  echo "expected missing integration remote-engine credential Secret to fail" >&2
  exit 1
fi

echo "Managed Compose config tests passed"
