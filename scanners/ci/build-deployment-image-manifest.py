#!/usr/bin/env python3
"""Validate five deployment image receipts and build their release manifest."""

from __future__ import annotations

import argparse
import datetime as dt
import json
import pathlib
import re
import sys
from typing import Any


DIGEST = re.compile(r"^sha256:[a-f0-9]{64}$")
RELEASE_ID = re.compile(r"^deployment-set-[a-z0-9][a-z0-9._-]{0,111}$")
REVISION = re.compile(r"^[a-f0-9]{40}$")
EXPECTED = {
    "runtime": "wolf-runtime",
    "proposal": "wolf-proposal-runtime",
    "release-fixed": "wolf-release-fixed-adapter",
    "release-quality": "wolf-release-quality-adapter",
    "release-integration": "wolf-release-integration-adapter",
}
PLATFORMS = ["linux/amd64", "linux/arm64"]


def valid_digest(value: object) -> bool:
    return isinstance(value, str) and DIGEST.fullmatch(value) is not None


def load_receipt(path: pathlib.Path, release_id: str) -> dict[str, Any]:
    with path.open(encoding="utf-8") as stream:
        value = json.load(stream)
    if not isinstance(value, dict):
        raise ValueError(f"{path}: receipt root must be an object")
    variant = value.get("variant")
    if variant not in EXPECTED:
        raise ValueError(f"{path}: unknown deployment image variant")
    if value.get("schemaVersion") != "wolf.deployment-images.image-publication/v1":
        raise ValueError(f"{path}: unsupported receipt schema")
    if value.get("releaseId") != release_id:
        raise ValueError(f"{path}: release identity mismatch")
    if value.get("image") != EXPECTED[variant]:
        raise ValueError(f"{path}: image mapping mismatch")
    if value.get("platforms") != PLATFORMS:
        raise ValueError(f"{path}: exact amd64/arm64 platform set is required")
    if not valid_digest(value.get("digest")):
        raise ValueError(f"{path}: invalid index digest")
    children = value.get("children")
    if not isinstance(children, list) or len(children) != 2:
        raise ValueError(f"{path}: exactly two child receipts are required")
    if sorted(child.get("platform") for child in children if isinstance(child, dict)) != PLATFORMS:
        raise ValueError(f"{path}: child platform set mismatch")
    for child in children:
        if not isinstance(child, dict) or not valid_digest(child.get("digest")):
            raise ValueError(f"{path}: invalid child digest")
        if child.get("qualityReceiptSha256") is None or not valid_digest(
            child.get("qualityReceiptSha256")
        ):
            raise ValueError(f"{path}: invalid child quality receipt digest")
    for registry in ("primary", "mirror"):
        target = value.get(registry)
        if not isinstance(target, dict) or target.get("verified") is not True:
            raise ValueError(f"{path}: {registry} closure is not verified")
        if not isinstance(target.get("repository"), str) or not target["repository"]:
            raise ValueError(f"{path}: {registry} repository is missing")
        if not valid_digest(target.get("referrersSha256")):
            raise ValueError(f"{path}: {registry} referrer evidence is invalid")
    return value


def created_at(value: str) -> str:
    try:
        parsed = dt.datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError as exc:
        raise ValueError("created must be RFC3339") from exc
    if parsed.tzinfo is None:
        raise ValueError("created must include a timezone")
    return parsed.astimezone(dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--release-id", required=True)
    parser.add_argument("--revision", required=True)
    parser.add_argument("--source", required=True)
    parser.add_argument("--created", required=True)
    parser.add_argument("--output", required=True, type=pathlib.Path)
    parser.add_argument("receipts", nargs="+", type=pathlib.Path)
    args = parser.parse_args(argv)

    if RELEASE_ID.fullmatch(args.release_id) is None:
        raise ValueError("invalid deployment image release ID")
    if REVISION.fullmatch(args.revision) is None:
        raise ValueError("invalid source revision")
    if not args.source.startswith("https://") or any(c.isspace() for c in args.source):
        raise ValueError("source must be an HTTPS URL")

    images = [load_receipt(path, args.release_id) for path in args.receipts]
    variants = [item["variant"] for item in images]
    if len(images) != len(EXPECTED) or set(variants) != set(EXPECTED) or len(set(variants)) != len(EXPECTED):
        raise ValueError("exactly one receipt for every deployment image is required")

    result = {
        "schemaVersion": "wolf.deployment-images.release/v1",
        "releaseId": args.release_id,
        "source": args.source,
        "revision": args.revision,
        "createdAt": created_at(args.created),
        "platforms": PLATFORMS,
        "images": sorted(images, key=lambda item: item["variant"]),
    }
    args.output.parent.mkdir(parents=True, exist_ok=True)
    with args.output.open("w", encoding="utf-8", newline="\n") as stream:
        json.dump(result, stream, indent=2, sort_keys=True)
        stream.write("\n")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main(sys.argv[1:]))
    except (OSError, ValueError, json.JSONDecodeError) as exc:
        print(f"deployment image manifest error: {exc}", file=sys.stderr)
        raise SystemExit(1)
