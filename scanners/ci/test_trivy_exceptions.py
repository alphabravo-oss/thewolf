from __future__ import annotations

import datetime as dt
import importlib.util
import pathlib
import tempfile
import unittest


MODULE_PATH = pathlib.Path(__file__).with_name("validate-trivy-exceptions.py")
SPEC = importlib.util.spec_from_file_location("validate_trivy_exceptions", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class TrivyExceptionTests(unittest.TestCase):
    today = dt.date(2026, 7, 30)

    def validate_text(self, text: str) -> int:
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / ".trivyignore"
            path.write_text(text, encoding="utf-8")
            return MODULE.validate(path, self.today)

    def test_accepts_scoped_expiring_exception(self) -> None:
        count = self.validate_text(
            "CVE-2026-12345 # owner=security@example.test; "
            "expires=2026-08-30; reason=No upstream fix is available; images=jvm\n"
        )
        self.assertEqual(count, 1)

    def test_rejects_expired_exception(self) -> None:
        with self.assertRaisesRegex(ValueError, "expired"):
            self.validate_text(
                "CVE-2026-12345 # owner=security; expires=2026-07-29; "
                "reason=Compensating control is active; images=default\n"
            )

    def test_rejects_unbounded_exception(self) -> None:
        with self.assertRaisesRegex(ValueError, "expires"):
            self.validate_text(
                "CVE-2026-12345 # owner=security; expires=until upstream fix; "
                "reason=Waiting for an upstream release; images=default\n"
            )

    def test_rejects_unscoped_image(self) -> None:
        with self.assertRaisesRegex(ValueError, "images"):
            self.validate_text(
                "CVE-2026-12345 # owner=security; expires=2026-08-30; "
                "reason=Compensating control is active; images=everything\n"
            )

    def test_renders_only_matching_image_exceptions(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            source = root / ".trivyignore"
            output = root / "runtime.trivyignore"
            source.write_text(
                "CVE-2026-12345 # owner=security; expires=2026-08-30; "
                "reason=Runtime-only compensating control; images=runtime\n"
                "CVE-2026-23456 # owner=security; expires=2026-08-30; "
                "reason=Fixer-only compensating control; images=fixer-api\n"
                "GHSA-2345-6789-cfgh # owner=security; expires=2026-08-30; "
                "reason=Global compensating control; images=all\n",
                encoding="utf-8",
            )
            count = MODULE.render(source, self.today, "runtime", output)
            self.assertEqual(count, 2)
            self.assertEqual(
                output.read_text(encoding="utf-8"),
                "CVE-2026-12345\nGHSA-2345-6789-cfgh\n",
            )

    def test_accepts_every_published_image_scope(self) -> None:
        for image in sorted(MODULE.ALLOWED_IMAGES - {"all"}):
            count = self.validate_text(
                "CVE-2026-12345 # owner=security; expires=2026-08-30; "
                f"reason=Compensating control for image; images={image}\n"
            )
            self.assertEqual(count, 1)


if __name__ == "__main__":
    unittest.main()
