#!/usr/bin/env python3
"""Static policy checks for the scanner release-factory workflow."""

from __future__ import annotations

import pathlib
import re
import sys


WORKFLOW = pathlib.Path(".github/workflows/scanners-image.yml")


def require(pattern: str, text: str, description: str) -> None:
    if re.search(pattern, text, re.MULTILINE | re.DOTALL) is None:
        raise AssertionError(f"missing workflow invariant: {description}")


def reject(pattern: str, text: str, description: str) -> None:
    if re.search(pattern, text, re.MULTILINE | re.DOTALL) is not None:
        raise AssertionError(f"forbidden workflow pattern: {description}")


def run_blocks(text: str) -> list[str]:
    lines = text.splitlines()
    blocks: list[str] = []
    index = 0
    while index < len(lines):
        match = re.match(r"^(\s*)run:\s*\|[-+]?\s*$", lines[index])
        if match is None:
            index += 1
            continue
        indent = len(match.group(1))
        body: list[str] = []
        index += 1
        while index < len(lines):
            line = lines[index]
            if line.strip() and len(line) - len(line.lstrip()) <= indent:
                break
            body.append(line)
            index += 1
        blocks.append("\n".join(body))
    return blocks


def job_block(text: str, name: str) -> str:
    match = re.search(
        rf"^  {re.escape(name)}:\n(?P<body>.*?)(?=^  [a-zA-Z0-9_-]+:\n|\Z)",
        text,
        re.MULTILINE | re.DOTALL,
    )
    if match is None:
        raise AssertionError(f"missing workflow job: {name}")
    return match.group("body")


def main() -> int:
    text = WORKFLOW.read_text(encoding="utf-8")

    require(r'cron:\s*"17 2 \* \* \*"', text, "daily discovery schedule")
    require(r'cron:\s*"43 3 \* \* 0"', text, "weekly candidate schedule")
    if text.count('- ".dockerignore"') < 2:
        raise AssertionError(
            "missing workflow invariant: root Docker context changes trigger push and pull-request validation"
        )
    for path in ('"Dockerfile"', '"ui/**"', '"scripts/e2e/**"'):
        if text.count(f"- {path}") < 2:
            raise AssertionError(
                f"missing workflow invariant: {path} changes trigger push and pull-request final-runtime validation"
            )
    require(
        r"operation:\s*\n.*options:.*refresh-os-packages.*refresh-vulnerability-dbs.*candidate.*security-rebuild.*release.*verify",
        text,
        "on-demand operations",
    )
    require(
        r"os_package_refresh:.*make scanners-os-packages-refresh.*Upload reviewable refresh proposal",
        text,
        "on-demand immutable package refresh proposal",
    )
    require(
        r"Record update review separately from the weekly immutable rebuild.*?counts\.update_available == 0.*?counts\.failed == 0",
        text,
        "weekly rebuild records update review without suppressing the complete immutable build",
    )
    require(
        r"vulnerability_db_refresh:.*?scanners-vulnerability-dbs-refresh.*?Upload reviewable vulnerability database proposal",
        text,
        "daily and on-demand exact vulnerability database refresh proposal",
    )
    require(
        r"concurrency:.*github\.event_name == 'workflow_dispatch' && github\.run_id \|\| github\.event\.schedule \|\| github\.ref",
        text,
        "manual dispatch factory runs do not inherit stale operation concurrency",
    )
    require(
        r"concurrency:.*cancel-in-progress:\s*\$\{\{\s*github\.event_name == 'workflow_dispatch'\s*\}\}",
        text,
        "manual dispatch cancellation policy",
    )
    require(
        r"immutable_id=.*run_suffix|run_suffix=.*RUN_ATTEMPT",
        pathlib.Path("scanners/ci/release-meta.sh").read_text(encoding="utf-8"),
        "attempt-unique immutable candidate identities",
    )
    require(
        r"variant:\s*codeql\n\s+image_kind:\s*scanner\n\s+context:\s*scanners\n\s+dockerfile:\s*scanners/Dockerfile\.codeql\n"
        r"\s+image:\s*wolf-scanners-codeql\n\s+platforms:\s*linux/amd64(?:\s|$)",
        text,
        "CodeQL amd64-only publication",
    )
    reject(
        r"variant:\s*codeql.*?dockerfile:\s*(?:scanners/)?Dockerfile\.codeql\n"
        r"\s+image:\s*wolf-scanners-codeql\n\s+platforms:\s*linux/amd64,linux/arm64",
        text,
        "CodeQL arm64 publication",
    )
    require(r"Strict version and invocation smoke.*?WOLF_SMOKE_STRICT=1", text, "strict smoke gate")
    require(
        r"Execute every parser's valid and hostile compatibility corpus.*?"
        r"go test ./plugins/\.\.\. ./internal/scannerquality -count=1",
        text,
        "all parser compatibility tests are a mandatory validation gate",
    )
    require(
        r"Require immutable upstream image resolutions.*?"
        r"scannertools lock --check --require-resolved --json",
        text,
        "publication validation rejects unresolved upstream image tags",
    )
    require(
        r"fixer-quality:.*?variant:\s*base.*?variant:\s*api.*?variant:\s*claude.*?variant:\s*codex",
        text,
        "all fixer quality variants",
    )
    require(
        r"fixer-quality:.*?runs-on:\s*\$\{\{ matrix\.runner \}\}.*?runner:\s*ubuntu-24\.04-arm",
        text,
        "native arm64 fixer quality runner",
    )
    runtime_quality = job_block(text, "runtime-quality")
    for variant, target in (
        ("runtime", "runtime"),
        ("proposal", "proposal-runtime"),
        ("release-fixed", "scanner-release-fixed-adapter"),
        ("release-quality", "scanner-release-quality-adapter"),
        ("release-integration", "scanner-release-integration-adapter"),
    ):
        for platform, arch in (("linux/amd64", "amd64"), ("linux/arm64", "arm64")):
            require(
                rf"variant:\s*{re.escape(variant)}\n"
                rf"\s+target:\s*{re.escape(target)}\n"
                rf".*?platform:\s*{re.escape(platform)}\n"
                rf"\s+arch:\s*{re.escape(arch)}",
                runtime_quality,
                f"{variant} final-image {platform} quality lane",
            )
    require(
        r"Strict runtime and lane-boundary smoke.*?Config\.User.*?"
        r"org\.opencontainers\.image\.source.*?WOLF_SMOKE_STRICT|"
        r"Strict runtime and lane-boundary smoke.*?Config\.User.*?"
        r"org\.opencontainers\.image\.source.*?scanner-release-worker.*?"
        r"trivy --version.*?docker compose version",
        runtime_quality,
        "strict runtime metadata, command, and release-lane boundary smoke",
    )
    require(
        r"Verify locked vulnerability database identity.*?verify-trivy-db\.sh.*?"
        r"Final runtime vulnerability, secret, and license gate.*?"
        r"scanners:\s*vuln,secret,license",
        runtime_quality,
        "locked final-runtime vulnerability, secret, and license gate",
    )
    require(
        r"Validate runtime SPDX document.*?spdxVersion == \"SPDX-2\.3\".*?"
        r"packages.*?length > 0.*?relationships.*?length > 0",
        runtime_quality,
        "non-empty SPDX 2.3 evidence for every final runtime",
    )
    require(
        r"publish:\n.*?needs:\s*\[prepare, quality, fixer-quality, runtime-quality, release-approval\].*?"
        r"needs\.runtime-quality\.result == 'success'",
        text,
        "publication is blocked on all final-runtime quality lanes",
    )
    published_quality = job_block(text, "published-platform-quality")
    expected_published_tuples = {
        "default": ("scanner", "linux/amd64", "linux/arm64"),
        "jvm": ("scanner", "linux/amd64", "linux/arm64"),
        "rust": ("scanner", "linux/amd64", "linux/arm64"),
        "codeql": ("scanner", "linux/amd64"),
        "fixer-base": ("fixer", "linux/amd64", "linux/arm64"),
        "fixer-api": ("fixer", "linux/amd64", "linux/arm64"),
        "fixer-claude": ("fixer", "linux/amd64", "linux/arm64"),
        "fixer-codex": ("fixer", "linux/amd64", "linux/arm64"),
    }
    for variant, (kind, *platforms) in expected_published_tuples.items():
        for platform in platforms:
            require(
                rf"variant:\s*{re.escape(variant)},\s*image_kind:\s*{kind},\s*"
                rf"platform:\s*{re.escape(platform)}",
                published_quality,
                f"exact published {variant} {platform} quality tuple",
            )
    require(
        r"runs-on:\s*\$\{\{ matrix\.arch == 'arm64' && 'ubuntu-24\.04-arm'.*?"
        r"Resolve exact published child manifest.*?\.manifests\[\].*?child_digest.*?"
        r"Strict smoke against exact published child",
        published_quality,
        "published child manifests execute on matching native runners",
    )
    require(
        r"Scan exact published child vulnerability, secret, and license policy.*?"
        r"image-ref:\s*\$\{\{ steps\.child\.outputs\.child_ref \}\}.*?"
        r"Validate|Create content-addressed platform quality receipt.*?"
        r"wolf\.scanners\.published-platform-quality/v1",
        published_quality,
        "published child security, SPDX, and content-addressed receipt gate",
    )
    integration_quality = job_block(text, "integration-quality")
    require(
        r"needs:\s*\[prepare, publish, publish-fixer-engines, published-platform-quality\].*?"
        r"scanner-quality-compose\.sh.*?scanner-rollout-compose\.sh.*?"
        r"scanner-quality-kind\.sh.*?scanner-rollout-kind\.sh",
        integration_quality,
        "exact candidate scanner and rollout gates run in Compose and Kind",
    )
    require(
        r"wolf\.scanners\.candidate-qualification/v1.*?platformReceiptCount:\s*15.*?"
        r"scanner-candidate-qualification-",
        integration_quality,
        "candidate qualification receipt binds all 15 published platform tuples",
    )
    require(
        r"release-manifest:\n.*?needs:\s*\[prepare, publish, publish-fixer-engines, integration-quality, release-approval\].*?"
        r"needs\.integration-quality\.result == 'success'.*?"
        r"candidate-qualification\.json:application/vnd\.wolf\.scanner\.candidate-qualification\.v1\+json",
        text,
        "aggregate candidate publication is blocked on and embeds exact environment qualification",
    )
    require(
        r"variant:\s*fixer-base.*?image_kind:\s*fixer.*?image:\s*wolf-fixer.*?platforms:\s*linux/amd64,linux/arm64",
        text,
        "multi-platform fixer base publication",
    )
    for variant in ("fixer-api", "fixer-claude", "fixer-codex"):
        require(
            rf"variant:\s*{variant}.*?platforms:\s*linux/amd64,linux/arm64",
            text,
            f"multi-platform {variant} publication",
        )
    require(
        r"publish-fixer-engines:.*?needs:\s*\[prepare, publish, release-approval\].*?"
        r"WOLF_FIXER_BASE_REF=\$\{\{ steps\.base\.outputs\.reference \}\}",
        text,
        "fixer engines depend on exact published base output",
    )
    require(
        r"publish-fixer-engines:.*?if:\s*>-\s*always\(\)\s*&&.*?"
        r"needs\.publish\.result == 'success'.*?"
        r"needs\.release-approval\.result == 'success'.*?"
        r"needs\.release-approval\.outputs\.receipt_digest != ''",
        text,
        "candidate fixer publication tolerates skipped approval while release publication requires successful approval evidence",
    )
    require(
        r"release-manifest:.*?if:\s*>-\s*always\(\)\s*&&.*?"
        r"needs\.publish\.result == 'success'.*?"
        r"needs\.publish-fixer-engines\.result == 'success'.*?"
        r"needs\.release-approval\.result == 'success'.*?"
        r"needs\.release-approval\.outputs\.receipt_digest != ''",
        text,
        "candidate aggregate publication tolerates skipped approval while release publication requires successful approval evidence",
    )
    require(
        r"Verify and bind exact published fixer base.*?verify-image\.sh.*?cosign verify.*?gh attestation verify",
        text,
        "fixer base digest, signature, and provenance re-verification",
    )
    require(r"imageKind:\s*\$image_kind", text, "scanner/fixer manifest classification")
    require(r'imageKind:\s*"fixer"', text, "fixer engine manifest classification")
    require(r"scanners:\s*vuln,secret,license", text, "vulnerability, secret, and license gate")
    require(
        r"TRIVY_IGNORED_LICENSES:\s*GPL-1\.0-only,.*LGPL-3\.0-or-later,Sleepycat",
        text,
        "explicit redistributable open-source license allowlist",
    )
    require(
        r"ignored-licenses:\s*\$\{\{ env\.TRIVY_IGNORED_LICENSES \}\}",
        text,
        "all image gates consume the centralized license allowlist",
    )
    if text.count("validate-trivy-exceptions.py .trivyignore") < 6:
        raise AssertionError(
            "missing workflow invariant: every image class renders a scope-specific Trivy exception file"
        )
    reject(
        r"trivyignores:\s*\.trivyignore(?:\s|$)",
        text,
        "unfiltered global Trivy exceptions applied to every image",
    )
    require(
        r'echo "TRIVY_DB_REPOSITORY=\$main_ref" >>"\$GITHUB_ENV"',
        text,
        "exact-digest vulnerability database repository loaded from the reviewed lock",
    )
    require(
        r'echo "TRIVY_JAVA_DB_REPOSITORY=\$java_ref" >>"\$GITHUB_ENV"',
        text,
        "exact-digest Java vulnerability database repository loaded from the reviewed lock",
    )
    require(
        r"verify-trivy-db\.sh.*trivy-db\.lock\.json",
        text,
        "locked vulnerability database identity evidence gate",
    )
    require(
        r"verify-trivy-db\.sh.*trivy-java-db\.lock\.json",
        text,
        "locked Java vulnerability database identity evidence gate",
    )
    require(r"format:\s*spdx-json", text, "SPDX SBOM creation")
    require(r"aggregate-spdx\.py", text, "aggregate release SPDX SBOM")
    require(r"provenance:\s*mode=max", text, "BuildKit provenance")
    require(r"index:dev\.wolf\.release\.lock-digest=", text, "OCI index lock-digest annotation")
    for annotation in (
        "org.opencontainers.image.source",
        "org.opencontainers.image.revision",
        "org.opencontainers.image.version",
        "dev.wolf.release.variant",
        "dev.wolf.release.image-kind",
        "dev.wolf.release.platforms",
    ):
        require(
            rf"index:{re.escape(annotation)}=",
            text,
            f"OCI index {annotation} annotation",
        )
    require(r"verify-image\.sh.*?LOCK_DIGEST.*?DEFINITION_DIGEST", text, "post-push release annotation verification")
    require(r"oras discover --format json", text, "exact OCI referrer inventory")
    require(r"referrersSha256", text, "content-addressed referrer evidence")
    require(r"no-cache:.*?43 3 \* \* 0.*?security-rebuild", text, "fresh weekly/security build")
    require(r"actions/attest-build-provenance@", text, "GitHub provenance attestation")
    require(r"actions/attest-sbom@", text, "GitHub SBOM attestation")
    require(r"cosign sign --yes", text, "keyless signing")
    require(r"cosign verify", text, "signature verification")
    require(
        r"Verify mirror image, signature, provenance, and SBOM.*?gh attestation verify",
        text,
        "scanner mirror supply-chain verification",
    )
    require(
        r"Verify mirror engine, signature, provenance, and SBOM.*?gh attestation verify",
        text,
        "fixer mirror supply-chain verification",
    )
    require(
        r"Verify mirror aggregate signature, provenance, and SBOM.*?"
        r"gh attestation verify",
        text,
        "aggregate mirror supply-chain verification",
    )
    reject(
        r"Write immutable (?:image|engine) metadata\n\s+if:\s*always\(\)",
        text,
        "publication metadata emitted after failed evidence gates",
    )
    require(r"Create or verify immutable primary image tag.*?promote-image\.sh", text, "immutable promotion")
    require(r"Move aggregate alias last", text, "aggregate commit marker moved last")
    require(r"environment:\s*\n\s*name:\s*scanner-release", text, "protected release environment")
    require(
        r"resolve-release-candidate:.*?verify-release-closure\.sh.*?candidate_manifest_digest.*?verification_evidence_digest",
        text,
        "exact candidate closure is verified before approval",
    )
    require(
        r"release-approval:.*?needs:\s*\[prepare, resolve-release-candidate\].*?"
        r"protected-approval/v2.*?candidateManifestDigest.*?verificationEvidenceDigest.*?receipt_digest",
        text,
        "protected approval receipt binds exact candidate and verification evidence",
    )
    require(
        r"promote-release:.*?needs:\s*\[prepare, resolve-release-candidate, release-approval\].*?"
        r"promote-release-manifest\.sh.*?Move primary aggregate channel alias last",
        text,
        "protected release promotes exact candidate without rebuilding and moves aggregate last",
    )
    require(
        r"REVERIFIED_VERIFICATION_EVIDENCE_DIGEST:\s*\$\{\{ steps\.reverify\.outputs\.verification_evidence_digest \}\}.*?"
        r'\[\[ "\$REVERIFIED_VERIFICATION_EVIDENCE_DIGEST" == "\$VERIFICATION_EVIDENCE_DIGEST" \]\]',
        text,
        "post-approval closure evidence must equal the exact approved evidence digest",
    )
    require(
        r"Replay complete protected release closure before aliases.*?verify-release-closure\.sh.*?"
        r"Move mirrored image release and channel aliases.*?Move primary image release and channel aliases.*?"
        r"Move primary aggregate channel alias last",
        text,
        "the newly published protected release closure is replayed before any image or channel alias moves",
    )
    require(
        r"release-manifest:.*?needs\.prepare\.outputs\.operation != 'release'",
        text,
        "candidate aggregate build path cannot rebuild a protected release",
    )
    require(r"OPERATION.*?stable.*?APPROVAL_RECEIPT_DIGEST", text, "stable alias approval guard")
    require(r"PRIMARY_REGISTRY:\s*ghcr\.io", text, "GHCR primary")
    require(r"MIRROR_REGISTRY:\s*docker\.io", text, "Docker Hub mirror")
    reject(r"pull_request_target:", text, "privileged pull_request_target trigger")
    closure = pathlib.Path("scanners/ci/verify-release-closure.sh").read_text(encoding="utf-8")
    require(
        r"scanners-image\.yml@refs/heads/main\$",
        closure,
        "managed signature and attestation trust is restricted to protected main",
    )

    for action, revision in re.findall(r"\buses:\s*([^@\s]+)@([^\s#]+)", text):
        if action.startswith("./"):
            continue
        if re.fullmatch(r"[a-f0-9]{40}", revision) is None:
            raise AssertionError(f"action {action} is not pinned to a full commit SHA: {revision}")

    for block in run_blocks(text):
        if "${{ github.event." in block:
            raise AssertionError("event-controlled expression is interpolated directly into a run block")
        if "${{ secrets." in block:
            raise AssertionError("secret expression is interpolated directly into a run block")

    print("scanner release workflow policy tests: PASS")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (AssertionError, OSError) as exc:
        print(f"workflow policy failure: {exc}", file=sys.stderr)
        raise SystemExit(1)
