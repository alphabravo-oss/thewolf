#!/usr/bin/env bash
set -euo pipefail

chart_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT

wolf_digest="sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
postgres_digest="sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
step_digest="sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
coordinator_digest="sha256:1111111111111111111111111111111111111111111111111111111111111111"
fixed_digest="sha256:2222222222222222222222222222222222222222222222222222222222222222"
quality_digest="sha256:3333333333333333333333333333333333333333333333333333333333333333"
integration_digest="sha256:4444444444444444444444444444444444444444444444444444444444444444"

helm lint "$chart_dir" \
  --set-string masterKey=test-master-key \
  --set-string postgres.password=test-postgres-password \
  --set-string image.digest="$wolf_digest" \
  --set-string postgres.digest="$postgres_digest"

helm template wolf "$chart_dir" \
  --set-string masterKey=test-master-key \
  --set-string postgres.password=test-postgres-password \
  --set-string image.digest="$wolf_digest" \
  --set-string postgres.digest="$postgres_digest" \
  >"$tmp_dir/rendered.yaml"

grep -q "image: ghcr.io/alphabravo-oss/thewolf@$wolf_digest" "$tmp_dir/rendered.yaml"
grep -q "image: postgres@$postgres_digest" "$tmp_dir/rendered.yaml"
grep -q "automountServiceAccountToken: false" "$tmp_dir/rendered.yaml"
grep -A20 'app.kubernetes.io/component: postgres' "$tmp_dir/rendered.yaml" |
  grep -q 'runAsNonRoot: true'
grep -A20 'app.kubernetes.io/component: postgres' "$tmp_dir/rendered.yaml" |
  grep -q 'runAsUser: 70'
grep -q 'name: PGDATA' "$tmp_dir/rendered.yaml"
grep -q 'mountPath: /var/run/postgresql' "$tmp_dir/rendered.yaml"
grep -A4 'name: run' "$tmp_dir/rendered.yaml" | grep -q 'medium: Memory'
grep -A4 'name: run' "$tmp_dir/rendered.yaml" | grep -q 'sizeLimit: 16Mi'

helm template wolf "$chart_dir" \
  --set-string masterKey=test-master-key \
  --set-string postgres.password=test-postgres-password \
  --set-string image.digest="$wolf_digest" \
  --set-string postgres.digest="$postgres_digest" \
  --set-string postgres.storageClassName=qualified-storage \
  >"$tmp_dir/storage-class.yaml"
grep -A12 'name: wolf-wolf-postgres' "$tmp_dir/storage-class.yaml" |
  grep -q 'storageClassName: "qualified-storage"'
grep -A12 "name: wolf-wolf-scanner-network-required" "$tmp_dir/rendered.yaml" |
  grep -q "egress: \\[\\]"
if grep -q "name: wolf-wolf-scanner-custom-build" "$tmp_dir/rendered.yaml"; then
  echo "custom build worker must be disabled by default" >&2
  exit 1
fi
if grep -q "name: wolf-wolf-fixer" "$tmp_dir/rendered.yaml"; then
  echo "fixer worker must be disabled by default" >&2
  exit 1
fi

helm template wolf "$chart_dir" \
  --set-string masterKey=test-master-key \
  --set-string postgres.password=test-postgres-password \
  --set-string image.digest="$wolf_digest" \
  --set-string postgres.digest="$postgres_digest" \
  --set fixer.enabled=true \
  >"$tmp_dir/fixer.yaml"
grep -q "app.kubernetes.io/component: fixer" "$tmp_dir/fixer.yaml"
grep -q "name: wolf-wolf-fixer-home" "$tmp_dir/fixer.yaml"
grep -q 'mountPath: /home/wolf' "$tmp_dir/fixer.yaml"
if grep -Eq "WOLF_SCANNER_BUNDLE_(TRUST_POLICY_FILE|IMAGE_VERIFIER|IMAGE_TRUST_POLICY_FILE)" "$tmp_dir/rendered.yaml"; then
  echo "offline bundle trust must remain unmounted by default" >&2
  exit 1
fi

helm template wolf "$chart_dir" \
  --set-string masterKey=test-master-key \
  --set-string postgres.password=test-postgres-password \
  --set-string image.digest="$wolf_digest" \
  --set-string postgres.digest="$postgres_digest" \
  --set-string scannerRelease.offlineBundles.portableTrustPolicySecret=portable-bundle-trust \
  --set-string scannerRelease.offlineBundles.portableTrustPolicyKey=portable.json \
  --set scannerRelease.offlineBundles.imageVerifier.enabled=true \
  --set-string scannerRelease.offlineBundles.imageVerifier.path=/usr/local/bin/company-image-verifier \
  --set-string scannerRelease.offlineBundles.imageVerifier.trustPolicySecret=image-signature-trust \
  --set-string scannerRelease.offlineBundles.imageVerifier.trustPolicyKey=images.json \
  >"$tmp_dir/offline-bundle-trust.yaml"
grep -q "WOLF_SCANNER_BUNDLE_TRUST_POLICY_FILE" "$tmp_dir/offline-bundle-trust.yaml"
grep -q "WOLF_SCANNER_BUNDLE_IMAGE_VERIFIER" "$tmp_dir/offline-bundle-trust.yaml"
grep -q "/usr/local/bin/company-image-verifier" "$tmp_dir/offline-bundle-trust.yaml"
grep -q "secretName: portable-bundle-trust" "$tmp_dir/offline-bundle-trust.yaml"
grep -q "secretName: image-signature-trust" "$tmp_dir/offline-bundle-trust.yaml"
grep -q "portable.json" "$tmp_dir/offline-bundle-trust.yaml"
grep -q "images.json" "$tmp_dir/offline-bundle-trust.yaml"

if helm template wolf "$chart_dir" \
  --set-string masterKey=test-master-key \
  --set-string postgres.password=test-postgres-password \
  --set-string image.digest="$wolf_digest" \
  --set-string postgres.digest="$postgres_digest" \
  --set scannerRelease.offlineBundles.imageVerifier.enabled=true \
  >"$tmp_dir/offline-bundle-missing-trust.yaml" 2>"$tmp_dir/offline-bundle-missing-trust.err"; then
  echo "expected enabled image verifier without a trust Secret to fail" >&2
  exit 1
fi
grep -q "imageVerifier.trustPolicySecret is required" "$tmp_dir/offline-bundle-missing-trust.err"

if helm template wolf "$chart_dir" \
  --set-string masterKey=test-master-key \
  --set-string postgres.password=test-postgres-password \
  >"$tmp_dir/missing-digests.yaml" 2>"$tmp_dir/missing-digests.err"; then
  echo "expected rendering without immutable image digests to fail" >&2
  exit 1
fi
grep -q "image.digest is required" "$tmp_dir/missing-digests.err"

helm template wolf "$chart_dir" \
  --set-string masterKey=test-master-key \
  --set-string postgres.password=test-postgres-password \
  --set-string image.digest="$wolf_digest" \
  --set-string postgres.digest="$postgres_digest" \
  --set "networkPolicy.scannerEgressCIDRs={203.0.113.0/24}" \
  >"$tmp_dir/allowlisted.yaml"
grep -A18 "name: wolf-wolf-scanner-network-required" "$tmp_dir/allowlisted.yaml" |
  grep -q 'cidr: "203.0.113.0/24"'

helm template wolf "$chart_dir" \
  --set-string masterKey=test-master-key \
  --set-string postgres.password=test-postgres-password \
  --set-string image.digest="$wolf_digest" \
  --set-string postgres.digest="$postgres_digest" \
  --set scannerRelease.builder.enabled=true \
  --set-string scannerRelease.builder.backend=kubernetes-job \
  --set-string scannerRelease.builder.stepImage="registry.example/wolf-step@$step_digest" \
  >"$tmp_dir/kubernetes-builder.yaml"
grep -A90 "name: wolf-wolf-scanner-release-builder" "$tmp_dir/kubernetes-builder.yaml" |
  grep -q -- "--executor-backend=kubernetes-job"
grep -A110 "name: wolf-wolf-scanner-release-builder" "$tmp_dir/kubernetes-builder.yaml" |
  grep -q "automountServiceAccountToken: true"
grep -A140 "name: wolf-wolf-scanner-release-builder" "$tmp_dir/kubernetes-builder.yaml" |
  grep -q "WOLF_SCANNER_RELEASE_WORKSPACE_PVC"
grep -A170 "name: wolf-wolf-scanner-release-builder" "$tmp_dir/kubernetes-builder.yaml" |
  grep -q "claimName: wolf-wolf-workspace"
if grep -A170 "name: wolf-wolf-scanner-release-builder" "$tmp_dir/kubernetes-builder.yaml" |
  grep -q "docker.sock"; then
  echo "Kubernetes release builder must not mount a Docker socket" >&2
  exit 1
fi

helm template wolf "$chart_dir" \
  --set-string masterKey=test-master-key \
  --set-string postgres.password=test-postgres-password \
  --set-string image.digest="$wolf_digest" \
  --set-string postgres.digest="$postgres_digest" \
  --set scannerRelease.customBuild.enabled=true \
  >"$tmp_dir/custom-build-worker.yaml"
grep -A110 "name: wolf-wolf-scanner-custom-build" "$tmp_dir/custom-build-worker.yaml" |
  grep -q "scanner-custom-build-worker"
grep -A150 "name: wolf-wolf-scanner-custom-build" "$tmp_dir/custom-build-worker.yaml" |
  grep -q "path: /var/run/docker.sock"
grep -A100 "name: wolf-wolf-scanner-custom-build" "$tmp_dir/custom-build-worker.yaml" |
  grep -q "automountServiceAccountToken: false"
if grep -A130 "name: wolf-wolf-api" "$tmp_dir/custom-build-worker.yaml" |
  grep -q "docker.sock"; then
  echo "Kubernetes API pod must not mount the custom-build engine socket" >&2
  exit 1
fi

if helm template wolf "$chart_dir" \
  --set-string masterKey=test-master-key \
  --set-string postgres.password=test-postgres-password \
  --set-string image.digest="$wolf_digest" \
  --set-string postgres.digest="$postgres_digest" \
  --set scannerRelease.customBuild.enabled=true \
  --set-string scannerRelease.customBuild.engineMode=remote \
  >"$tmp_dir/remote-custom-build.yaml" 2>"$tmp_dir/remote-custom-build.err"; then
  echo "expected unsupported remote custom-build engine to fail closed" >&2
  exit 1
fi
grep -Eq "engineMode.*(node-local|enum)" "$tmp_dir/remote-custom-build.err"

if helm template wolf "$chart_dir" \
  --set-string masterKey=test-master-key \
  --set-string postgres.password=test-postgres-password \
  --set-string image.digest="$wolf_digest" \
  --set-string postgres.digest="$postgres_digest" \
  --set scannerRelease.builder.enabled=true \
  --set-string scannerRelease.builder.backend=kubernetes-job \
  --set-string scannerRelease.builder.stepImage=registry.example/wolf-step:latest \
  >"$tmp_dir/mutable-step.yaml" 2>"$tmp_dir/mutable-step.err"; then
  echo "expected mutable scanner release step image to fail" >&2
  exit 1
fi
grep -Eiq "stepImage.*(digest-pinned|does not match pattern)" "$tmp_dir/mutable-step.err"

managed_args=(
  --namespace wolf-system
  --set-string masterKey=test-master-key
  --set-string postgres.password=test-postgres-password
  --set-string image.digest="$wolf_digest"
  --set-string postgres.digest="$postgres_digest"
  --set scannerRelease.builder.enabled=true
  --set-string scannerRelease.builder.backend=managed
  --set-string scannerRelease.builder.image="registry.example/wolf-coordinator@$coordinator_digest"
  --set-string scannerRelease.builder.stepImage="registry.example/wolf-step@$step_digest"
  --set 'scannerRelease.builder.platforms={linux/amd64,linux/arm64}'
  --set-string scannerRelease.builder.managed.primaryRegistryID=primary-target
  --set-string scannerRelease.builder.managed.mirrorRegistryID=mirror-target
  --set-string scannerRelease.builder.managed.dockerConfigSecret=primary-buildx-docker
  --set-string scannerRelease.builder.managed.kubernetes.namespace=wolf-system
  --set-string scannerRelease.builder.managed.kubernetes.buildxNamespace=wolf-buildkit
  --set-string scannerRelease.builder.managed.kubernetes.workspacePVC=wolf-wolf-workspace
  --set-string scannerRelease.builder.managed.networkPolicy.destinations.fixed[0].cidr=198.51.100.10/32
  --set-string scannerRelease.builder.managed.networkPolicy.destinations.fixed[0].ports[0].protocol=TCP
  --set scannerRelease.builder.managed.networkPolicy.destinations.fixed[0].ports[0].port=443
  --set-string scannerRelease.builder.managed.networkPolicy.destinations.quality[0].cidr=198.51.100.20/32
  --set-string scannerRelease.builder.managed.networkPolicy.destinations.quality[0].ports[0].protocol=TCP
  --set scannerRelease.builder.managed.networkPolicy.destinations.quality[0].ports[0].port=443
  --set-string scannerRelease.builder.managed.networkPolicy.destinations.quality[0].ports[1].protocol=TCP
  --set scannerRelease.builder.managed.networkPolicy.destinations.quality[0].ports[1].port=2376
  --set-string scannerRelease.builder.managed.networkPolicy.destinations.integration[0].cidr=198.51.100.30/32
  --set-string scannerRelease.builder.managed.networkPolicy.destinations.integration[0].ports[0].protocol=TCP
  --set scannerRelease.builder.managed.networkPolicy.destinations.integration[0].ports[0].port=443
  --set-string scannerRelease.builder.managed.networkPolicy.destinations.integration[0].ports[1].protocol=TCP
  --set scannerRelease.builder.managed.networkPolicy.destinations.integration[0].ports[1].port=2376
  --set-string scannerRelease.builder.managed.networkPolicy.destinations.signer[0].cidr=198.51.100.40/32
  --set-string scannerRelease.builder.managed.networkPolicy.destinations.signer[0].ports[0].protocol=TCP
  --set scannerRelease.builder.managed.networkPolicy.destinations.signer[0].ports[0].port=443
  --set scannerRelease.signing.enabled=true
  --set-string scannerRelease.signing.profileSecret=signer-profile
  --set-string scannerRelease.signing.credentialSecret=signer-credential
  --set-string scannerRelease.builder.managed.adapters.fixed.image="registry.example/fixed@$fixed_digest"
  --set-string scannerRelease.builder.managed.adapters.fixed.registryCredentialSecret=fixed-registry-credential
  --set-string scannerRelease.builder.managed.adapters.quality.image="registry.example/quality@$quality_digest"
  --set-string scannerRelease.builder.managed.adapters.quality.registryCredentialSecret=quality-registry-credential
  --set-string scannerRelease.builder.managed.adapters.quality.engineCredentialSecret=quality-engine-credential
  --set-string scannerRelease.builder.managed.adapters.integration.image="registry.example/integration@$integration_digest"
  --set-string scannerRelease.builder.managed.adapters.integration.registryCredentialSecret=integration-registry-credential
  --set-string scannerRelease.builder.managed.adapters.integration.engineCredentialSecret=integration-engine-credential
)

helm template wolf "$chart_dir" "${managed_args[@]}" \
  --set-string scannerRelease.builder.managed.sourceURL=https://git.example/wolf/scanners.git \
  >"$tmp_dir/managed-builder.yaml"

managed_builder="$(grep -n 'name: wolf-wolf-scanner-release-builder$' "$tmp_dir/managed-builder.yaml" | tail -1 | cut -d: -f1)"
sed -n "${managed_builder},$((managed_builder + 230))p" "$tmp_dir/managed-builder.yaml" >"$tmp_dir/managed-builder-deployment.yaml"
grep -q -- "--executor-backend=managed" "$tmp_dir/managed-builder-deployment.yaml"
grep -q "WOLF_SCANNER_RELEASE_BUILDX_PATH" "$tmp_dir/managed-builder-deployment.yaml"
grep -q "/usr/libexec/docker/cli-plugins/docker-buildx" "$tmp_dir/managed-builder-deployment.yaml"
grep -q "WOLF_SCANNER_RELEASE_BUILDX_NAMESPACE" "$tmp_dir/managed-builder-deployment.yaml"
grep -q "WOLF_SCANNER_RELEASE_BUILDKIT_SERVICE_ACCOUNT" "$tmp_dir/managed-builder-deployment.yaml"
grep -q "WOLF_SCANNER_RELEASE_BUILDX_DOCKER_CONFIG" "$tmp_dir/managed-builder-deployment.yaml"
grep -q "mountPath: /run/wolf/buildx-docker" "$tmp_dir/managed-builder-deployment.yaml"
grep -q "WOLF_SCANNER_RELEASE_SIGNER_SERVICE_ACCOUNT" "$tmp_dir/managed-builder-deployment.yaml"
grep -q "WOLF_SCANNER_RELEASE_FIXED_ADAPTER_IMAGE" "$tmp_dir/managed-builder-deployment.yaml"
grep -q "WOLF_SCANNER_RELEASE_QUALITY_ADAPTER_IMAGE" "$tmp_dir/managed-builder-deployment.yaml"
grep -q "WOLF_SCANNER_RELEASE_INTEGRATION_ADAPTER_IMAGE" "$tmp_dir/managed-builder-deployment.yaml"
grep -q "automountServiceAccountToken: true" "$tmp_dir/managed-builder-deployment.yaml"
grep -q "WOLF_SCANNER_RELEASE_K8S_INSTANCE" "$tmp_dir/managed-builder-deployment.yaml"

for lane in ordinary fixed quality integration signer; do
  policy="$(grep -n "name: wolf-wolf-release-$lane-egress$" "$tmp_dir/managed-builder.yaml" | cut -d: -f1)"
  if [[ -z "$policy" ]]; then
    echo "missing managed $lane release Job NetworkPolicy" >&2
    exit 1
  fi
  sed -n "${policy},$((policy + 70))p" "$tmp_dir/managed-builder.yaml" >"$tmp_dir/managed-$lane-policy.yaml"
  grep -q "app.kubernetes.io/instance: wolf" "$tmp_dir/managed-$lane-policy.yaml"
  grep -q "wolf.security/lane: $lane" "$tmp_dir/managed-$lane-policy.yaml"
  grep -q "kubernetes.io/metadata.name: kube-system" "$tmp_dir/managed-$lane-policy.yaml"
  grep -q "protocol: UDP, port: 53" "$tmp_dir/managed-$lane-policy.yaml"
  grep -q "protocol: TCP, port: 53" "$tmp_dir/managed-$lane-policy.yaml"
done
grep -q 'cidr: "198.51.100.10/32"' "$tmp_dir/managed-fixed-policy.yaml"
grep -q 'cidr: "198.51.100.20/32"' "$tmp_dir/managed-quality-policy.yaml"
grep -q 'protocol: TCP, port: 2376' "$tmp_dir/managed-quality-policy.yaml"
grep -q 'cidr: "198.51.100.30/32"' "$tmp_dir/managed-integration-policy.yaml"
grep -q 'protocol: TCP, port: 2376' "$tmp_dir/managed-integration-policy.yaml"
grep -q 'cidr: "198.51.100.40/32"' "$tmp_dir/managed-signer-policy.yaml"

grep -A45 "name: wolf-wolf-scanner-release-buildx" "$tmp_dir/managed-builder.yaml" |
  grep -q "namespace: wolf-buildkit"
grep -A45 "name: wolf-wolf-scanner-release-buildx" "$tmp_dir/managed-builder.yaml" |
  grep -q 'resources: \["deployments", "statefulsets"\]'
grep -A45 "name: wolf-wolf-scanner-release-buildx" "$tmp_dir/managed-builder.yaml" |
  grep -q 'resources: \["pods/exec"\]'
if grep -A45 "name: wolf-wolf-scanner-release-buildx" "$tmp_dir/managed-builder.yaml" |
  grep -q 'resources: \["secrets"\]'; then
  echo "Buildx Role must not read Kubernetes Secrets" >&2
  exit 1
fi
grep -A20 "name: wolf-wolf-scanner-release-jobs" "$tmp_dir/managed-builder.yaml" |
  grep -q 'resources: \["jobs"\]'
if grep -A20 "name: wolf-wolf-scanner-release-jobs" "$tmp_dir/managed-builder.yaml" |
  grep -q 'deployments'; then
  echo "release Job Role must not receive Buildx controller permissions" >&2
  exit 1
fi

api_deployment="$(grep -n 'name: wolf-wolf-api$' "$tmp_dir/managed-builder.yaml" | tail -1 | cut -d: -f1)"
sed -n "${api_deployment},$((api_deployment + 135))p" "$tmp_dir/managed-builder.yaml" >"$tmp_dir/managed-api.yaml"
grep -q "automountServiceAccountToken: false" "$tmp_dir/managed-api.yaml"
if grep -Eq 'WOLF_SCANNER_SIGNER|signer-(profile|credentials)' "$tmp_dir/managed-api.yaml"; then
  echo "API deployment must not receive managed signing identity or Secrets" >&2
  exit 1
fi

if helm template wolf "$chart_dir" "${managed_args[@]}" \
  >"$tmp_dir/managed-missing-source.yaml" 2>"$tmp_dir/managed-missing-source.err"; then
  echo "expected managed render without source URL to fail" >&2
  exit 1
fi
grep -q "managed.sourceURL" "$tmp_dir/managed-missing-source.err"

if helm template wolf "$chart_dir" "${managed_args[@]}" \
  --set-string scannerRelease.builder.managed.sourceURL=https://git.example/wolf/scanners.git \
  --set-string scannerRelease.builder.managed.adapters.fixed.image=registry.example/fixed:latest \
  >"$tmp_dir/managed-mutable-adapter.yaml" 2>"$tmp_dir/managed-mutable-adapter.err"; then
  echo "expected mutable managed adapter image to fail" >&2
  exit 1
fi
grep -Eiq "adapters.fixed.image.*(digest-pinned|does not match pattern)" "$tmp_dir/managed-mutable-adapter.err"

if helm template wolf "$chart_dir" "${managed_args[@]}" \
  --set-string scannerRelease.builder.managed.sourceURL=https://git.example/wolf/scanners.git \
  --set-string scannerRelease.builder.image=registry.example/wolf-coordinator:latest \
  >"$tmp_dir/managed-mutable-coordinator.yaml" 2>"$tmp_dir/managed-mutable-coordinator.err"; then
  echo "expected mutable managed coordinator image to fail" >&2
  exit 1
fi
grep -Eiq "builder.image.*(digest-pinned|does not match pattern)" "$tmp_dir/managed-mutable-coordinator.err"

if helm template wolf "$chart_dir" "${managed_args[@]}" \
  --set-string scannerRelease.builder.managed.sourceURL=https://git.example/wolf/scanners.git \
  --set-string scannerRelease.builder.managed.dockerConfigSecret= \
  >"$tmp_dir/managed-missing-docker-config.yaml" 2>"$tmp_dir/managed-missing-docker-config.err"; then
  echo "expected missing managed Docker config Secret to fail" >&2
  exit 1
fi
grep -q "managed.dockerConfigSecret" "$tmp_dir/managed-missing-docker-config.err"

if helm template wolf "$chart_dir" "${managed_args[@]}" \
  --set-string scannerRelease.builder.managed.sourceURL=https://git.example/wolf/scanners.git \
  --set-string scannerRelease.builder.managed.kubernetes.buildxNamespace=wolf-system \
  >"$tmp_dir/managed-reused-namespace.yaml" 2>"$tmp_dir/managed-reused-namespace.err"; then
  echo "expected managed Buildx and Job namespace collision to fail" >&2
  exit 1
fi
grep -q "must use separate Kubernetes namespaces" "$tmp_dir/managed-reused-namespace.err"

if helm template wolf "$chart_dir" "${managed_args[@]}" \
  --set-string scannerRelease.builder.managed.sourceURL=https://git.example/wolf/scanners.git \
  --set-string scannerRelease.signing.serviceAccountName=reused-lane \
  --set-string scannerRelease.builder.managed.adapters.fixed.serviceAccountName=reused-lane \
  >"$tmp_dir/managed-reused-identity.yaml" 2>"$tmp_dir/managed-reused-identity.err"; then
  echo "expected reused managed service account to fail" >&2
  exit 1
fi
grep -q "service accounts must all be distinct" "$tmp_dir/managed-reused-identity.err"

if helm template wolf "$chart_dir" "${managed_args[@]}" \
  --set-string scannerRelease.builder.managed.sourceURL=https://git.example/wolf/scanners.git \
  --set-string scannerRelease.builder.managed.adapters.fixed.registryCredentialSecret=signer-credential \
  >"$tmp_dir/managed-reused-secret.yaml" 2>"$tmp_dir/managed-reused-secret.err"; then
  echo "expected reused managed credential Secret to fail" >&2
  exit 1
fi
grep -q "credential Secrets must all be distinct" "$tmp_dir/managed-reused-secret.err"

if helm template wolf "$chart_dir" "${managed_args[@]}" \
  --set-string scannerRelease.builder.managed.sourceURL=https://git.example/wolf/scanners.git \
  --set-string scannerRelease.builder.managed.gitAuthorizationSecret=signer-profile \
  >"$tmp_dir/managed-reused-git-secret.yaml" 2>"$tmp_dir/managed-reused-git-secret.err"; then
  echo "expected reused managed Git authorization Secret to fail" >&2
  exit 1
fi
grep -q "credential Secrets must all be distinct" "$tmp_dir/managed-reused-git-secret.err"

if helm template wolf "$chart_dir" "${managed_args[@]}" \
  --set-string scannerRelease.builder.managed.sourceURL=https://git.example/wolf/scanners.git \
  --set-string scannerRelease.builder.managed.adapters.quality.engineCredentialSecret= \
  --set scannerRelease.builder.managed.adapters.quality.workloadIdentity=true \
  >"$tmp_dir/managed-missing-quality-engine-secret.yaml" 2>"$tmp_dir/managed-missing-quality-engine-secret.err"; then
  echo "expected missing quality remote-engine credential Secret to fail" >&2
  exit 1
fi
grep -Eq "quality.*engineCredentialSecret|engineCredentialSecret.*minLength" "$tmp_dir/managed-missing-quality-engine-secret.err"

if helm template wolf "$chart_dir" "${managed_args[@]}" \
  --set-string scannerRelease.builder.managed.sourceURL=https://git.example/wolf/scanners.git \
  --set-string scannerRelease.builder.managed.adapters.fixed.registryCredentialSecret= \
  --set scannerRelease.builder.managed.adapters.fixed.workloadIdentity=true \
  >"$tmp_dir/managed-missing-fixed-registry-secret.yaml" 2>"$tmp_dir/managed-missing-fixed-registry-secret.err"; then
  echo "expected missing fixed registry credential Secret to fail" >&2
  exit 1
fi
grep -Eq "fixed.*registryCredentialSecret|registryCredentialSecret.*minLength" "$tmp_dir/managed-missing-fixed-registry-secret.err"

if helm template wolf "$chart_dir" "${managed_args[@]}" \
  --set-string scannerRelease.builder.managed.sourceURL=https://git.example/wolf/scanners.git \
  --set-string scannerRelease.builder.managed.adapters.integration.engineCredentialSecret= \
  --set scannerRelease.builder.managed.adapters.integration.workloadIdentity=true \
  >"$tmp_dir/managed-missing-integration-engine-secret.yaml" 2>"$tmp_dir/managed-missing-integration-engine-secret.err"; then
  echo "expected missing integration remote-engine credential Secret to fail" >&2
  exit 1
fi
grep -Eq "integration.*engineCredentialSecret|engineCredentialSecret.*minLength" "$tmp_dir/managed-missing-integration-engine-secret.err"

for lane in fixed quality integration signer; do
  if helm template wolf "$chart_dir" "${managed_args[@]}" \
    --set-string scannerRelease.builder.managed.sourceURL=https://git.example/wolf/scanners.git \
    --set-string "scannerRelease.builder.managed.networkPolicy.destinations.$lane=" \
    >"$tmp_dir/managed-missing-$lane-egress.yaml" 2>"$tmp_dir/managed-missing-$lane-egress.err"; then
    echo "expected missing managed $lane egress destination to fail" >&2
    exit 1
  fi
  grep -Eq "destinations.*$lane|minItems" "$tmp_dir/managed-missing-$lane-egress.err"
done

if helm template wolf "$chart_dir" "${managed_args[@]}" \
  --set-string scannerRelease.builder.managed.sourceURL=https://git.example/wolf/scanners.git \
  --set networkPolicy.enabled=false \
  >"$tmp_dir/managed-network-policy-disabled.yaml" 2>"$tmp_dir/managed-network-policy-disabled.err"; then
  echo "expected managed render with NetworkPolicy disabled to fail" >&2
  exit 1
fi
grep -q "networkPolicy.enabled must be true" "$tmp_dir/managed-network-policy-disabled.err"

if helm template wolf "$chart_dir" "${managed_args[@]}" \
  --set-string scannerRelease.builder.managed.sourceURL=https://git.example/wolf/scanners.git \
  --set-string scannerRelease.builder.managed.networkPolicy.dns.namespaceSelectorLabels= \
  >"$tmp_dir/managed-missing-dns-selector.yaml" 2>"$tmp_dir/managed-missing-dns-selector.err"; then
  echo "expected managed render without a DNS namespace selector to fail" >&2
  exit 1
fi
grep -Eq "dns.namespaceSelectorLabels.*(required|object)" "$tmp_dir/managed-missing-dns-selector.err"

echo "Helm security render checks passed"
