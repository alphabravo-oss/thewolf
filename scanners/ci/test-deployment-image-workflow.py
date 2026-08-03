#!/usr/bin/env python3
"""Static trust and transaction policy for deployment image publication."""

from __future__ import annotations

import pathlib
import re


WORKFLOW = pathlib.Path(".github/workflows/deployment-images.yml")
SCANNER_WORKFLOW = pathlib.Path(".github/workflows/scanners-image.yml")


def require(pattern: str, text: str, description: str) -> None:
    if re.search(pattern, text, re.MULTILINE | re.DOTALL) is None:
        raise AssertionError(f"missing deployment workflow invariant: {description}")


def reject(pattern: str, text: str, description: str) -> None:
    if re.search(pattern, text, re.MULTILINE | re.DOTALL) is not None:
        raise AssertionError(f"forbidden deployment workflow pattern: {description}")


def job(text: str, name: str) -> str:
    match = re.search(
        rf"^  {re.escape(name)}:\n(?P<body>.*?)(?=^  [a-zA-Z0-9_-]+:\n|\Z)",
        text,
        re.MULTILINE | re.DOTALL,
    )
    if match is None:
        raise AssertionError(f"missing deployment workflow job: {name}")
    return match.group("body")


def main() -> int:
    text = WORKFLOW.read_text(encoding="utf-8")
    scanner = SCANNER_WORKFLOW.read_text(encoding="utf-8")

    require(r'cron:\s*"23 4 \* \* 1"', text, "weekly complete rebuild")
    for path in ('"Dockerfile"', '"scanners/**"', '"ui/**"', '"scripts/e2e/**"'):
        if text.count(f"- {path}") < 2:
            raise AssertionError(f"missing push/PR path coverage for {path}")
    require(r"concurrency:.*cancel-in-progress:\s*false", text, "non-cancelling release transaction")
    require(
        r"\[\[ \"\$REF_NAME\" == main \]\].*?trusted only from main.*?"
        r"operation.*?publish.*?verify.*?release_id.*?RUN_ID.*?RUN_ATTEMPT",
        job(text, "prepare"),
        "main-only dispatch and attempt-unique release identity",
    )
    require(r"stable.*manual-only", job(text, "prepare"), "manual-only stable channel")

    stage = job(text, "build-staging")
    expected = {
        "runtime": ("runtime", "wolf-runtime"),
        "proposal": ("proposal-runtime", "wolf-proposal-runtime"),
        "release-fixed": ("scanner-release-fixed-adapter", "wolf-release-fixed-adapter"),
        "release-quality": ("scanner-release-quality-adapter", "wolf-release-quality-adapter"),
        "release-integration": (
            "scanner-release-integration-adapter",
            "wolf-release-integration-adapter",
        ),
    }
    for variant, (target, image) in expected.items():
        require(
            rf"variant:\s*{re.escape(variant)},\s*target:\s*{re.escape(target)},\s*image:\s*{re.escape(image)}",
            stage,
            f"{variant} staging image mapping",
        )
    require(r"platforms:\s*linux/amd64,linux/arm64", stage, "multi-platform staging")
    require(r"tags:.*staging_tag", stage, "attempt-unique staging tag")
    require(r"provenance:\s*false.*?sbom:\s*false", stage, "unambiguous two-child staging index")

    quality = job(text, "exact-child-quality")
    for variant, (_, image) in expected.items():
        for platform, arch in (("linux/amd64", "amd64"), ("linux/arm64", "arm64")):
            require(
                rf"variant:\s*{re.escape(variant)},\s*image:\s*{re.escape(image)},\s*"
                rf"platform:\s*{re.escape(platform)},\s*arch:\s*{arch}",
                quality,
                f"native exact-child tuple {variant}/{platform}",
            )
    require(
        r"runs-on:.*ubuntu-24\.04-arm.*?Resolve exact staged index and child digest.*?"
        r"exact child platform missing or duplicated.*?smoke-deployment-image\.sh",
        quality,
        "native published-child execution",
    )
    require(
        r"Gate exact child vulnerability, secret, and license policy.*?scanners:\s*vuln,secret,license.*?"
        r"Generate exact child SPDX.*?spdx-json.*?child-quality/v1",
        quality,
        "security, SPDX, and quality receipt closure",
    )

    images = job(text, "commit-images")
    require(
        r"needs:\s*\[prepare, exact-child-quality\].*?Rebind both quality receipts.*?"
        r"exactly two platform receipts.*?build/staging/.*?indexDigest == \$index",
        images,
        "immutable commit depends on both unchanged child receipts",
    )
    require(r"cosign sign --yes.*?attest-build-provenance.*?attest-sbom", images, "keyless image supply chain")
    require(
        r"oras copy --recursive.*?Keyless-sign mirrored image identity.*?"
        r"sourceReferrerParityVerified:true",
        images,
        "recursive mirror, mirror identity, and referrer parity",
    )
    require(
        r"certificate-identity.*deployment-images\.yml@refs/heads/main",
        images,
        "exact protected workflow signing identity",
    )

    aggregate = job(text, "commit-aggregate")
    require(
        r"needs:\s*\[prepare, commit-images\].*?receipts=\(build/image-publication/\*\.image\.json\).*?"
        r"\[\[ \$\{#receipts\[@\]\} -eq 5 \]\].*?\[\[ \$\{#sboms\[@\]\} -eq 10 \]\]",
        aggregate,
        "aggregate binds five images and ten child SBOMs",
    )
    require(
        r"wolf-deployment-image-releases.*?application/vnd\.wolf\.deployment-images\.release\.v1.*?"
        r"cosign sign --yes.*?attest-build-provenance.*?attest-sbom",
        aggregate,
        "independent signed aggregate artifact",
    )
    require(
        r"Recursively mirror primary aggregate.*?oras copy --recursive.*?"
        r"Verify full mirror aggregate closure.*?sourceReferrerParityVerified:true",
        aggregate,
        "aggregate recursive mirror closure",
    )
    closure_position = aggregate.index("Create final content-addressed closure receipt")
    alias_position = aggregate.index("Move image channels only after complete aggregate closure")
    if closure_position >= alias_position:
        raise AssertionError("mutable channels move before final aggregate closure")
    require(r"Move primary aggregate channel last", aggregate, "primary commit marker moves last")

    verify = job(text, "verify-release")
    require(
        r"Replay immutable deployment image release closure.*?byte-compare.*?cmp.*?"
        r"Replay every image digest, signature, attestation, and mirror closure",
        "verify-release:\n" + verify,
        "on-demand complete closure replay",
    )

    reject(
        r"(?:REPOSITORY|PRIMARY|MIRROR):.*wolf-scanner-releases",
        text,
        "deployment transaction mutates canonical scanner aggregate",
    )
    for variant in expected:
        reject(
            rf"dev\.wolf\.deployment-images\.variant.*?{re.escape(variant)}",
            scanner,
            "deployment variant inserted into canonical scanner workflow",
        )

    for line in text.splitlines():
        match = re.search(r"\buses:\s*([^\s]+)", line)
        if match is None or match.group(1).startswith("./"):
            continue
        reference = match.group(1)
        if re.fullmatch(r"[^@]+@[a-f0-9]{40}", reference) is None:
            raise AssertionError(f"action is not pinned to a full commit: {reference}")

    reject(r"continue-on-error", text, "best-effort supply-chain gate")
    print("deployment image workflow policy: PASS")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
