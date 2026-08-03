#!/usr/bin/env python3
"""Unit tests for the deployment image aggregate manifest contract."""

from __future__ import annotations

import importlib.util
import json
import pathlib
import tempfile
import unittest


SCRIPT = pathlib.Path(__file__).with_name("build-deployment-image-manifest.py")
SPEC = importlib.util.spec_from_file_location("deployment_manifest", SCRIPT)
assert SPEC is not None and SPEC.loader is not None
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class ManifestTests(unittest.TestCase):
    release_id = "deployment-set-2026.31.r123-a1"
    digest = "sha256:" + "a" * 64

    def receipt(self, variant: str, image: str) -> dict[str, object]:
        return {
            "schemaVersion": "wolf.deployment-images.image-publication/v1",
            "releaseId": self.release_id,
            "variant": variant,
            "image": image,
            "digest": self.digest,
            "platforms": ["linux/amd64", "linux/arm64"],
            "children": [
                {
                    "platform": "linux/amd64",
                    "digest": "sha256:" + "b" * 64,
                    "qualityReceiptSha256": "sha256:" + "c" * 64,
                },
                {
                    "platform": "linux/arm64",
                    "digest": "sha256:" + "d" * 64,
                    "qualityReceiptSha256": "sha256:" + "e" * 64,
                },
            ],
            "primary": {
                "repository": f"ghcr.io/example/{image}",
                "verified": True,
                "referrersSha256": self.digest,
            },
            "mirror": {
                "repository": f"docker.io/example/{image}",
                "verified": True,
                "referrersSha256": self.digest,
            },
        }

    def write_receipts(self, root: pathlib.Path) -> list[pathlib.Path]:
        paths: list[pathlib.Path] = []
        for variant, image in MODULE.EXPECTED.items():
            path = root / f"{variant}.json"
            path.write_text(json.dumps(self.receipt(variant, image)), encoding="utf-8")
            paths.append(path)
        return paths

    def test_accepts_exact_five_image_closure(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            paths = self.write_receipts(pathlib.Path(directory))
            output = pathlib.Path(directory) / "manifest.json"
            status = MODULE.main(
                [
                    "--release-id",
                    self.release_id,
                    "--revision",
                    "f" * 40,
                    "--source",
                    "https://github.com/example/repo",
                    "--created",
                    "2026-07-31T12:00:00Z",
                    "--output",
                    str(output),
                    *(str(path) for path in paths),
                ]
            )
            self.assertEqual(status, 0)
            manifest = json.loads(output.read_text(encoding="utf-8"))
            self.assertEqual(manifest["schemaVersion"], "wolf.deployment-images.release/v1")
            self.assertEqual(len(manifest["images"]), 5)

    def test_rejects_missing_image(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            paths = self.write_receipts(pathlib.Path(directory))[:-1]
            with self.assertRaisesRegex(ValueError, "exactly one receipt"):
                MODULE.main(
                    [
                        "--release-id",
                        self.release_id,
                        "--revision",
                        "f" * 40,
                        "--source",
                        "https://github.com/example/repo",
                        "--created",
                        "2026-07-31T12:00:00Z",
                        "--output",
                        str(pathlib.Path(directory) / "manifest.json"),
                        *(str(path) for path in paths),
                    ]
                )

    def test_rejects_unverified_mirror(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            paths = self.write_receipts(root)
            value = json.loads(paths[0].read_text(encoding="utf-8"))
            value["mirror"]["verified"] = False
            paths[0].write_text(json.dumps(value), encoding="utf-8")
            with self.assertRaisesRegex(ValueError, "mirror closure"):
                MODULE.load_receipt(paths[0], self.release_id)

    def test_rejects_platform_substitution(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            path = self.write_receipts(root)[0]
            value = json.loads(path.read_text(encoding="utf-8"))
            value["children"][1]["platform"] = "linux/amd64"
            path.write_text(json.dumps(value), encoding="utf-8")
            with self.assertRaisesRegex(ValueError, "child platform set"):
                MODULE.load_receipt(path, self.release_id)


if __name__ == "__main__":
    unittest.main()
