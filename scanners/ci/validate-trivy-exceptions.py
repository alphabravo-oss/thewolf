#!/usr/bin/env python3
"""Validate that every scanner CVE exception is scoped and expires."""

from __future__ import annotations

import argparse
import datetime as dt
import pathlib
import re
import sys


VULN_RE = re.compile(r"^(CVE-\d{4}-\d{4,}|GHSA-[23456789cfghjmpqrvwx]{4}-[23456789cfghjmpqrvwx]{4}-[23456789cfghjmpqrvwx]{4})$")
ALLOWED_IMAGES = {
    "all",
    "default",
    "jvm",
    "rust",
    "codeql",
    "fixer-base",
    "fixer-api",
    "fixer-claude",
    "fixer-codex",
    "runtime",
    "proposal",
    "release-fixed",
    "release-quality",
    "release-integration",
}
MAX_EXCEPTION_DAYS = 180


def parse_metadata(raw: str, line_number: int) -> dict[str, str]:
    fields: dict[str, str] = {}
    for item in raw.split(";"):
        key, separator, value = item.strip().partition("=")
        if not separator or not key or not value:
            raise ValueError(f"line {line_number}: metadata must use key=value fields separated by semicolons")
        if key in fields:
            raise ValueError(f"line {line_number}: duplicate metadata key {key}")
        fields[key] = value
    expected = {"owner", "expires", "reason", "images"}
    missing = expected - fields.keys()
    extra = fields.keys() - expected
    if missing:
        raise ValueError(f"line {line_number}: missing metadata: {', '.join(sorted(missing))}")
    if extra:
        raise ValueError(f"line {line_number}: unknown metadata: {', '.join(sorted(extra))}")
    return fields


def validated_entries(path: pathlib.Path, today: dt.date) -> list[tuple[str, set[str]]]:
    seen: set[str] = set()
    entries: list[tuple[str, set[str]]] = []
    for line_number, raw in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        vulnerability, separator, metadata_raw = line.partition("#")
        vulnerability = vulnerability.strip()
        if VULN_RE.fullmatch(vulnerability) is None:
            raise ValueError(f"line {line_number}: invalid CVE/GHSA identifier {vulnerability!r}")
        if vulnerability in seen:
            raise ValueError(f"line {line_number}: duplicate exception {vulnerability}")
        seen.add(vulnerability)
        if not separator:
            raise ValueError(f"line {line_number}: exception metadata comment is required")

        metadata = parse_metadata(metadata_raw, line_number)
        if len(metadata["owner"]) < 3 or any(char.isspace() for char in metadata["owner"]):
            raise ValueError(f"line {line_number}: owner must be a team slug or email without whitespace")
        if len(metadata["reason"]) < 12:
            raise ValueError(f"line {line_number}: reason must contain at least 12 characters")
        images = {item.strip() for item in metadata["images"].split(",") if item.strip()}
        if not images or not images <= ALLOWED_IMAGES:
            raise ValueError(f"line {line_number}: images must use {', '.join(sorted(ALLOWED_IMAGES))}")
        if "all" in images and len(images) != 1:
            raise ValueError(f"line {line_number}: images=all cannot be combined with another image")
        try:
            expires = dt.date.fromisoformat(metadata["expires"])
        except ValueError as exc:
            raise ValueError(f"line {line_number}: expires must be YYYY-MM-DD") from exc
        if expires < today:
            raise ValueError(f"line {line_number}: exception expired on {expires.isoformat()}")
        if expires > today + dt.timedelta(days=MAX_EXCEPTION_DAYS):
            raise ValueError(
                f"line {line_number}: exception exceeds the {MAX_EXCEPTION_DAYS}-day maximum"
            )
        entries.append((vulnerability, images))
    return entries


def validate(path: pathlib.Path, today: dt.date) -> int:
    return len(validated_entries(path, today))


def render(path: pathlib.Path, today: dt.date, image: str, output: pathlib.Path) -> int:
    if image not in ALLOWED_IMAGES - {"all"}:
        raise ValueError(f"render image must use {', '.join(sorted(ALLOWED_IMAGES - {'all'}))}")
    selected = [
        vulnerability
        for vulnerability, images in validated_entries(path, today)
        if "all" in images or image in images
    ]
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text("".join(f"{item}\n" for item in selected), encoding="utf-8")
    return len(selected)


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("path", nargs="?", default=".trivyignore", type=pathlib.Path)
    parser.add_argument("--today", type=dt.date.fromisoformat, default=dt.datetime.now(dt.timezone.utc).date())
    parser.add_argument("--image", choices=sorted(ALLOWED_IMAGES - {"all"}))
    parser.add_argument("--output", type=pathlib.Path)
    args = parser.parse_args(argv)
    if (args.image is None) != (args.output is None):
        parser.error("--image and --output must be provided together")
    if args.image is not None:
        count = render(args.path, args.today, args.image, args.output)
        print(f"Trivy exception policy OK: rendered {count} exception(s) for {args.image}")
        return 0
    count = validate(args.path, args.today)
    print(f"Trivy exception policy OK: {count} active exception(s)")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main(sys.argv[1:]))
    except (OSError, ValueError) as exc:
        print(f"Trivy exception policy error: {exc}", file=sys.stderr)
        raise SystemExit(1)
