# Deployment image release factory

Wolf publishes its five deployment and scanner-release control-plane images
through a transaction independent of the canonical scanner/fixer release:

| Variant | OCI repository | Docker target | Purpose |
|---|---|---|---|
| `runtime` | `wolf-runtime` | `runtime` | API, worker, UI, and CLI runtime |
| `proposal` | `wolf-proposal-runtime` | `proposal-runtime` | isolated proposal worker |
| `release-fixed` | `wolf-release-fixed-adapter` | `scanner-release-fixed-adapter` | fixed-input qualification lane |
| `release-quality` | `wolf-release-quality-adapter` | `scanner-release-quality-adapter` | quality and policy lane |
| `release-integration` | `wolf-release-integration-adapter` | `scanner-release-integration-adapter` | Compose and Kubernetes qualification lane |

These images are intentionally absent from `wolf-scanner-releases`. That
artifact retains exactly the four scanner and four fixer variants consumed by
scan execution. Deployment image releases use the separate
`wolf-deployment-image-releases` aggregate, allowing independent retention,
rollout, and trust policy without changing a scanner-set digest.

## Managed cadence and on-demand operations

`.github/workflows/deployment-images.yml` runs a complete rebuild each Monday
at 04:23 UTC and on relevant changes merged to `main`. Pull requests run the
transaction policy and receipt tests; they cannot write packages or request an
OIDC signing token.

An authorized operator can dispatch:

- `publish`, optionally with an attempt-bound `deployment-set-*` release ID and
  the `candidate` or `stable` channel; or
- `verify`, with an exact immutable release ID, to replay primary and mirror
  closure without moving a tag.

Dispatches and automatic publications fail unless the checked-out ref is
`main`. A supplied publish ID must end in the current GitHub run and attempt,
so a retry cannot reuse another attempt's staging or immutable identity.
`stable` is manual-only. Configure the repository's normal GitHub Actions
authorization and environment controls so only release administrators can run
the workflow or approve protected production changes.

The required repository secrets are:

- `DOCKERHUB_USERNAME`;
- `DOCKERHUB_TOKEN`.

GHCR authorization uses the job-scoped `GITHUB_TOKEN`. Docker Hub is mandatory
for this transaction; a missing or partially configured mirror is a hard
failure, not a degraded success.

## Transaction and evidence model

Publication proceeds through four boundaries:

1. Each target is built once as an `amd64`/`arm64` OCI index under a tag unique
   to the GitHub run, attempt, and source revision. Staging has no mutable
   channel and is not an immutable release.
2. Ten jobs resolve the exact two child digests from those published indexes.
   Each child is pulled and executed on a matching native runner, checked for
   non-root and variant-specific behavior, scanned with the locked Trivy
   databases for vulnerabilities, secrets, and license violations, and given
   a non-empty SPDX 2.3 document. A content-addressed receipt binds the child,
   parent index, source revision, platform, and every gate.
3. An image can receive an immutable release tag only after its two receipts
   are re-read and match the still-current staging index. The exact index is
   keyless-signed and receives GitHub build-provenance and SPDX attestations.
   ORAS recursively copies the signed graph to Docker Hub. Wolf verifies the
   mirror digest, its repository-specific signature and attestations, and that
   every primary referrer copied recursively is present at the mirror.
4. Exactly five verified image receipts and all ten child SPDX documents form
   the signed `wolf-deployment-image-releases` aggregate. That aggregate is
   independently signed, attested, recursively mirrored, and verified. Only
   after a final content-addressed closure receipt exists may image channels
   move. The mirrored aggregate channel moves next; the primary aggregate
   channel moves last and serves as the transaction commit marker.

GitHub retains child evidence, image receipts, the aggregate manifest/SPDX,
registry-referrer observations, and final closure receipts for 365 days.
Staging digest receipts are retained for 30 days. Registry immutability and
retention policies should retain every `deployment-set-*` tag and its
referrers for the supported release lifetime; unique `build-*` staging tags
may be garbage-collected after the evidence-retention window.

## Verification and rollout consumption

Consumers should select an image by the digest recorded in the aggregate, not
by a channel tag. Before deployment, enforce:

- aggregate and image signatures with the exact identity
  `.../.github/workflows/deployment-images.yml@refs/heads/main` and GitHub's
  Actions OIDC issuer;
- GitHub provenance with the aggregate's exact source revision;
- an SPDX attestation for every aggregate and image digest;
- the exact `linux/amd64,linux/arm64` image platform set; and
- primary/mirror digest and referrer closure.

The workflow's `verify` operation replays these properties against both
registries, byte-compares aggregate payloads, and verifies all five image
closures. Use it after registry maintenance, retention changes, credential
rotation, or before an emergency rollback.

## Qualification owned outside the repository

The repository tests the workflow structure, receipt validation, shell syntax,
and manifest rejection paths. A production qualification still requires a real
protected `main` run with GHCR, Docker Hub, GitHub OIDC, repository
attestations, both native runner architectures, and configured registry
retention/immutability controls. Record that run's aggregate digest and final
closure artifact in the release change record. No local test can substitute
for those external identities and registry capabilities.

After publication closure, run the protected
[remote-scan deployment qualification](remote-scan-deployment-qualification.md)
with the exact runtime, scanner, and PostgreSQL digests before promoting the
deployment set to stable.
