from __future__ import annotations

import importlib.util
import json
import pathlib
import tempfile
import unittest


MODULE_PATH = pathlib.Path(__file__).with_name("aggregate-spdx.py")
SPEC = importlib.util.spec_from_file_location("aggregate_spdx", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class AggregateSPDXTests(unittest.TestCase):
    def test_aggregate_is_stable_and_references_every_document(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            sources = []
            for name in ("default", "codeql"):
                path = root / f"{name}.spdx.json"
                path.write_text(
                    json.dumps(
                        {
                            "spdxVersion": "SPDX-2.3",
                            "documentNamespace": f"https://example.test/{name}",
                        }
                    ),
                    encoding="utf-8",
                )
                sources.append(path)

            first = MODULE.aggregate(
                "scanner-set-2026.31.1",
                "2026-07-30T12:00:00-04:00",
                sources,
            )
            second = MODULE.aggregate(
                "scanner-set-2026.31.1",
                "2026-07-30T16:00:00Z",
                list(reversed(sources)),
            )

            self.assertEqual(first, second)
            self.assertEqual(first["creationInfo"]["created"], "2026-07-30T16:00:00Z")
            self.assertEqual(len(first["externalDocumentRefs"]), 2)
            self.assertEqual(len(first["relationships"]), 2)

    def test_rejects_non_spdx_input(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            source = pathlib.Path(directory) / "bad.json"
            source.write_text(
                json.dumps(
                    {
                        "spdxVersion": "SPDX-2.2",
                        "documentNamespace": "https://example.test/bad",
                    }
                ),
                encoding="utf-8",
            )
            with self.assertRaisesRegex(ValueError, "SPDX-2.3"):
                MODULE.aggregate(
                    "scanner-set-2026.31.1",
                    "2026-07-30T16:00:00Z",
                    [source],
                )

    def test_rejects_unsafe_release_id(self) -> None:
        with self.assertRaisesRegex(ValueError, "release ID"):
            MODULE.aggregate("$(id)", "2026-07-30T16:00:00Z", [])


if __name__ == "__main__":
    unittest.main()
