# Scanner release factory CI

The scanner release factory in `.github/workflows/scanners-image.yml` is the
managed GitHub Actions implementation of the scanner supply-chain contract. It
does not replace Wolf's durable release database or rollout controller. It
produces the immutable, verifiable artifacts those components consume.

## Schedules and operations

| Trigger | Operation | Registry writes |
|---|---|---|
| Daily at 02:17 UTC | Read-only update discovery plus exact Trivy DB lock proposal | None |
| Sunday at 03:43 UTC | No-cache complete weekly candidate from the reviewed lock | Immutable candidate plus `candidate` alias |
| Push to `main` affecting scanner definitions | Complete commit candidate | Immutable candidate plus legacy `main` aliases |
| Pull request affecting scanner definitions | Seven-platform quality matrix | None |
| Application release publication | Compatibility scanner candidate | Immutable candidate plus existing semver/`latest` aliases |
| Manual `validate` | Definition and helper validation | None |
| Manual `discover` | Read-only update discovery | None |
| Manual `refresh-os-packages` | Resolve a requested immutable snapshot and upload a review patch | None |
| Manual `refresh-vulnerability-dbs` | Resolve both Trivy DB tags and upload exact-digest lock/context/lockfile review artifacts | None |
| Manual `candidate` | Candidate build, optionally dry-run | Only when `publish` is selected |
| Manual `security-rebuild` | Priority candidate rebuild | Only when `publish` is selected |
| Manual `release` with `candidate_id` | Protected `scanner-set-YYYY.WW.N` promotion of that exact candidate | Aggregate and aliases only; scanner images are never rebuilt |
| Manual `verify` | Verify a published `scanner-set-YYYY.WW.N` | None |

GitHub cron is UTC. The durable Wolf scheduler remains authoritative for
organization timezone, maintenance-window, catch-up, and database-lease
semantics; these repository schedules ensure the managed central factory still
performs daily/weekly work independently.

The package-refresh job runs only when explicitly requested. It uses the same
deterministic generator used locally, refreshes `scanner-lock.yaml` and the
embedded build context, validates the result, and uploads the generated locks
plus a binary Git patch and checksums. Manual runs require an explicit
`YYYYMMDDTHHMMSSZ` value. Pending package proposals never prevent the weekly
freshness candidate from rebuilding the last reviewed immutable lock.

The daily and on-demand vulnerability-database job resolves `trivy-db:2` and
`trivy-java-db:1` independently, creates exact-digest eight-day locks,
regenerates the scanner lock, validates the result, and uploads a review
artifact. Image jobs derive `TRIVY_DB_REPOSITORY` and
`TRIVY_JAVA_DB_REPOSITORY` from those reviewed files. Applying either proposal
remains a reviewed Git change; the workflow has no repository-write
permission.

## Candidate publication transaction

Each candidate image variant is built to a unique
`build-<immutable-id>-<run-id>-<attempt>` staging tag. The workflow:

1. Validates the manifest, generated documentation, embedded build-context
   parity, upstream platforms, helper tests, and scanner shell scripts.
2. Builds and strictly smoke-tests each supported platform. Default, JVM, and
   Rust require amd64 and arm64. CodeQL is intentionally amd64-only.
3. Scans the final images for high/critical vulnerabilities, secrets, and
   license-policy violations.
4. Generates per-platform and per-image SPDX JSON SBOMs.
5. Pushes BuildKit SBOM/provenance attestations and GitHub build/SBOM
   attestations.
6. Keyless-signs every primary image digest and verifies the expected workflow
   identity.
7. Creates an immutable image tag only if absent, or verifies that an existing
   tag already has the exact digest and platforms.
8. Mirrors immutable images when configured and verifies mirror digest,
   signature, and exact OCI referrer inventory.
9. Builds a canonical candidate scanner-set manifest and an aggregate SPDX
   index, then publishes, signs, attests, and verifies the aggregate OCI
   artifact.
10. Records exact signature/provenance/SBOM verification document digests and
    sorted primary/mirror referrer inventories in the per-image release
    metadata.
11. Moves candidate image aliases and finally moves the candidate aggregate
    alias as the commit marker.

Weekly candidates and on-demand security rebuilds disable the publication
cache, so unchanged Dockerfiles cannot hide refreshed base packages or advisory
database content behind old BuildKit layers.

An immutable tag is never overwritten with a different digest. A failure before
the aggregate alias update leaves diagnosable staging/immutable artifacts but
does not advertise an incomplete release through a channel.

## Exact candidate promotion

A manual `release` is a promotion transaction, not another build. The caller
must provide the immutable `candidate_id` to promote. Before protected approval,
the workflow resolves that candidate's primary and required Docker Hub
aggregate digests and replays its complete closure: every image/platform,
annotation, mirror digest, signature, build attestation, SPDX attestation,
referrer inventory, manifest, and aggregate SBOM. The resulting verification
report is content addressed.

After the `scanner-release` environment grants approval, the workflow verifies
the same candidate closure again and rejects any change from the approved
identity. It then:

1. Creates a new immutable release aggregate whose image entries are
   byte-for-byte the approved candidate image entries; no scanner image is
   rebuilt, copied under a different digest, or substituted.
2. Embeds the approval receipt and candidate verification report in the
   aggregate payload and records their SHA-256 digests.
3. Publishes, signs, attests, mirrors, and fully re-verifies that aggregate.
4. Promotes the already-approved immutable image digests to release aliases,
   completing all required mirror updates before primary updates.
5. Moves the primary aggregate channel alias last as the publication commit
   marker.

Release verification repeats the entire closure replay; checking only the
aggregate signature or manifest shape is insufficient. Candidate publication
and release promotion therefore have distinct immutable IDs, while their
scanner image digests remain exactly equal.

## Approval and trust

Only manual `release` operations can request `stable`. They use the
`scanner-release` GitHub environment and emit a content-addressed approval
receipt bound to the release ID, candidate ID, exact candidate aggregate OCI
digest, content-addressed candidate verification evidence, scanner-lock and
definition digests, candidate source/workflow identity, promotion commit,
protected environment, and workflow run. Candidate and security-rebuild
dispatches are rejected if they request `stable`; stable also requires mirror
mode `required`.

Configure that environment with required reviewers and deployment protection
rules. Approval of a workflow dispatch without a protected environment is not
equivalent to enterprise separation of duties.

Managed signing is keyless through GitHub's OIDC identity. Verification pins:

- OIDC issuer `https://token.actions.githubusercontent.com`;
- this repository;
- `.github/workflows/scanners-image.yml`;
- the source commit for GitHub attestations.

All third-party actions are pinned to full commit SHAs.

## Registries and secrets

GHCR (`ghcr.io/alphabravo-oss`) is always the primary registry and uses the
job-scoped `GITHUB_TOKEN`. Docker Hub mirroring is disabled for public releases
and remains only as a legacy/manual scanner-factory mode controlled by
`mirror_mode`:

- `auto`: mirror when both secrets exist; a mirror outage is recorded as
  degraded without discarding a valid primary release.
- `required`: missing credentials or any mirror failure blocks publication.
- `disabled`: never authenticate to or write Docker Hub.

For legacy mirror testing, configure both `DOCKERHUB_USERNAME` and
`DOCKERHUB_TOKEN`, or neither. The workflow rejects partial configuration.
Secrets enter only step environments or the pinned login action and are never
written to outputs, summaries, labels, build arguments, or artifact files.

## Artifact repositories

| Artifact | GHCR repository pattern |
|---|---|
| Default image | `ghcr.io/alphabravo-oss/wolf-scanners` |
| JVM image | `ghcr.io/alphabravo-oss/wolf-scanners-jvm` |
| Rust image | `ghcr.io/alphabravo-oss/wolf-scanners-rust` |
| CodeQL image | `ghcr.io/alphabravo-oss/wolf-scanners-codeql` |
| Aggregate release | `ghcr.io/alphabravo-oss/wolf-scanner-releases` |

The same repository names are used under the Docker Hub mirror namespace.
Runtime consumers must resolve an alias to the aggregate manifest and persist
the image digests. They must not run directly from `stable`, `candidate`,
`main`, or `latest`.

## Local validation

These checks do not need registry credentials:

```bash
make scanners-validate
make scanners-os-packages-check
# Explicit network operation:
make scanners-os-packages-refresh SNAPSHOT=20260730T000000Z
make scanners-upstream-check
go test ./scanners/ci/discover
python3 scanners/ci/test_aggregate_spdx.py
scanners/ci/test-release-scripts.sh
scanners/ci/test-workflow-policy.py
shellcheck scanners/ci/*.sh scanners/smoke-test.sh scanners/install/*.sh
yamllint .github/workflows/scanners-image.yml
```

`.trivyignore` entries are validated as release policy. Every exception needs
an owner, fixed expiry (maximum 180 days), actionable reason, and exact image
scope. An expired, unbounded, duplicated, or blanket exception blocks the
factory before any build or registry write.

Run `actionlint` against the workflow as an additional CI authoring check. A
full end-to-end publication also requires GHCR package write access, GitHub
artifact-attestation support, OIDC, Sigstore transparency-log access, and
Docker Hub credentials when mirroring.

The discovery command is intentionally read-only:

```bash
go run ./scanners/ci/discover \
  --definition-commit "$(git rev-parse HEAD)" \
  --minimum-coverage 0.70 \
  --output build/scanner-discovery.json
```

The JSON report records the manifest digest, coverage, source failures,
unknown/manual sources, current pins, and available versions. Candidate pin
changes remain reviewable Git changes; discovery never rewrites
`scanners/tools.yaml` or `scanners/versions.env`.
