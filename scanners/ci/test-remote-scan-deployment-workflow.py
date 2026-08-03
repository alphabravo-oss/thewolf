#!/usr/bin/env python3
"""Static fail-closed policy for exact deployment-matrix qualification."""

from __future__ import annotations

import pathlib
import re


WORKFLOW = pathlib.Path(".github/workflows/remote-scan-deployment-qualification.yml")


def require(pattern: str, text: str, description: str) -> None:
    if re.search(pattern, text, re.MULTILINE | re.DOTALL) is None:
        raise AssertionError(f"missing remote scan qualification invariant: {description}")


def reject(pattern: str, text: str, description: str) -> None:
    if re.search(pattern, text, re.MULTILINE | re.DOTALL) is not None:
        raise AssertionError(f"forbidden remote scan qualification pattern: {description}")


def job(text: str, name: str) -> str:
    match = re.search(
        rf"^  {re.escape(name)}:\n(?P<body>.*?)(?=^  [a-zA-Z0-9_-]+:\n|\Z)",
        text,
        re.MULTILINE | re.DOTALL,
    )
    if match is None:
        raise AssertionError(f"missing qualification job: {name}")
    return match.group("body")


def main() -> int:
    text = WORKFLOW.read_text(encoding="utf-8")
    prepare = job(text, "prepare")
    require(r"workflow_dispatch:.*?runtime_image:.*?scanner_image:.*?postgres_image:", text, "three exact image inputs")
    require(r"refs/heads/main.*?\^\[\^\[:space:\]@\]\+@sha256:\[a-f0-9\]\{64\}\$", prepare, "main-only exact refs")
    require(r"environment: scanner-release-qualification", job(text, "native"), "protected native environment")
    require(r"database: \[sqlite, postgres\].*?remote-scan-native\.sh", job(text, "native"), "native two-database matrix")
    require(r"database: \[sqlite, postgres\].*?remote-scan-compose\.sh", job(text, "compose"), "Compose two-database matrix")
    require(r"WOLF_E2E_KIND_MEMORY_PVS: \"0\".*?remote-scan-kind\.sh", job(text, "kind"), "Kind real storage provisioner")
    require(r"needs: \[prepare, native, compose, kind\].*?\$\{#receipts\[@\]\}.*?-eq 5", job(text, "closure"), "five-receipt closure")
    require(r"retention-days: 365", text, "long-lived qualification evidence")
    reject(r"continue-on-error", text, "best-effort matrix gate")
    lines = text.splitlines()
    for input_name in ("runtime_image", "scanner_image", "postgres_image"):
        marker = f"      {input_name}:"
        start = lines.index(marker)
        body: list[str] = []
        for line in lines[start + 1 :]:
            if line.startswith("      ") and not line.startswith("        "):
                break
            body.append(line)
        if any(line.lstrip().startswith("default:") for line in body):
            raise AssertionError(f"forbidden mutable default for {input_name}")

    for line in text.splitlines():
        match = re.search(r"\buses:\s*([^\s]+)", line)
        if match is None or match.group(1).startswith("./"):
            continue
        reference = match.group(1)
        if re.fullmatch(r"[^@]+@[a-f0-9]{40}", reference) is None:
            raise AssertionError(f"action is not pinned to a full commit: {reference}")

    print("remote scan deployment qualification workflow policy: PASS")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
