#!/usr/bin/env python3
"""Create a deterministic SPDX 2.3 release index over image SPDX documents."""

from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import pathlib
import re
import sys
from typing import Any


SPDX_VERSION = "SPDX-2.3"
SHA256_RE = re.compile(r"^[a-f0-9]{64}$")
SAFE_ID_RE = re.compile(r"[^A-Za-z0-9.-]+")


def file_sha256(path: pathlib.Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def load_document(path: pathlib.Path) -> dict[str, Any]:
    with path.open(encoding="utf-8") as stream:
        value = json.load(stream)
    if not isinstance(value, dict):
        raise ValueError(f"{path}: SPDX root must be an object")
    if value.get("spdxVersion") != SPDX_VERSION:
        raise ValueError(f"{path}: expected {SPDX_VERSION}")
    namespace = value.get("documentNamespace")
    if not isinstance(namespace, str) or not namespace.startswith(("https://", "http://")):
        raise ValueError(f"{path}: documentNamespace must be an HTTP(S) URI")
    return value


def safe_id(value: str) -> str:
    value = SAFE_ID_RE.sub("-", value).strip("-.")
    if not value:
        raise ValueError("SPDX document name cannot produce an empty external ID")
    return value[:80]


def parse_created(value: str) -> str:
    normalized = value.replace("Z", "+00:00")
    try:
        parsed = dt.datetime.fromisoformat(normalized)
    except ValueError as exc:
        raise ValueError("created must be an RFC3339 timestamp") from exc
    if parsed.tzinfo is None:
        raise ValueError("created must include a timezone")
    return parsed.astimezone(dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def aggregate(
    release_id: str,
    created: str,
    sources: list[pathlib.Path],
) -> dict[str, Any]:
    if not re.fullmatch(r"[a-z0-9][a-z0-9._-]{0,126}", release_id):
        raise ValueError("release ID is not an OCI-safe lower-case identifier")
    if not sources:
        raise ValueError("at least one source SPDX document is required")

    refs: list[dict[str, Any]] = []
    relationships: list[dict[str, str]] = []
    used_ids: set[str] = set()
    input_digest = hashlib.sha256()
    for path in sorted(sources, key=lambda item: item.name):
        document = load_document(path)
        checksum = file_sha256(path)
        if SHA256_RE.fullmatch(checksum) is None:
            raise AssertionError("sha256 implementation returned an invalid digest")
        candidate = safe_id(path.stem)
        external_id = f"DocumentRef-{candidate}"
        if external_id in used_ids:
            raise ValueError(f"duplicate SPDX external document ID: {external_id}")
        used_ids.add(external_id)
        refs.append(
            {
                "externalDocumentId": external_id,
                "spdxDocument": document["documentNamespace"],
                "checksum": {"algorithm": "SHA256", "checksumValue": checksum},
            }
        )
        relationships.append(
            {
                "spdxElementId": "SPDXRef-DOCUMENT",
                "relationshipType": "DESCRIBES",
                "relatedSpdxElement": f"{external_id}:SPDXRef-DOCUMENT",
            }
        )
        input_digest.update(path.name.encode())
        input_digest.update(b"\0")
        input_digest.update(checksum.encode())
        input_digest.update(b"\0")

    namespace_digest = input_digest.hexdigest()
    return {
        "spdxVersion": SPDX_VERSION,
        "dataLicense": "CC0-1.0",
        "SPDXID": "SPDXRef-DOCUMENT",
        "name": f"wolf-scanners-release-{release_id}",
        "documentNamespace": (
            "https://github.com/alphabravocompany/thewolf/"
            f"spdx/scanner-releases/{release_id}/{namespace_digest}"
        ),
        "creationInfo": {
            "created": parse_created(created),
            "creators": ["Tool: wolf-scanner-release-factory"],
        },
        "externalDocumentRefs": refs,
        "relationships": relationships,
    }


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--release-id", required=True)
    parser.add_argument("--created", required=True)
    parser.add_argument("--output", required=True, type=pathlib.Path)
    parser.add_argument("sources", nargs="+", type=pathlib.Path)
    args = parser.parse_args(argv)

    result = aggregate(args.release_id, args.created, args.sources)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    with args.output.open("w", encoding="utf-8", newline="\n") as stream:
        json.dump(result, stream, indent=2, sort_keys=True)
        stream.write("\n")
    print(f"sha256:{file_sha256(args.output)}  {args.output}")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main(sys.argv[1:]))
    except (OSError, ValueError, json.JSONDecodeError) as exc:
        print(f"aggregate SPDX error: {exc}", file=sys.stderr)
        raise SystemExit(1)
