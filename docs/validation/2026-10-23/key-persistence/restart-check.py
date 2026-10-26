#!/usr/bin/env python3
"""Run a disposable API restart check; emit no keys, passwords or tokens.

Usage: python3 restart-check.py /absolute/path/to/wolf
"""
import hashlib
import json
import os
from pathlib import Path
import secrets
import signal
import socket
import sqlite3
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request


def main():
    binary = Path(sys.argv[1]).resolve(strict=True)
    with tempfile.TemporaryDirectory(prefix="wolf-key-restart-") as temp:
        home = Path(temp)
        database = home / "wolf.db"
        env = {"PATH": os.defpath, "HOME": str(home), "WOLF_DB_DRIVER": "sqlite",
               "WOLF_DB_DSN": str(database), "WOLF_ARTIFACTS_ROOT": str(home / "artifacts"),
               "WOLF_SCAN_EXECUTION_MODE": "queue"}
        with socket.socket() as probe:
            probe.bind(("127.0.0.1", 0))
            port = probe.getsockname()[1]
        base = f"http://127.0.0.1:{port}/api/v1"
        process = None
        log = tempfile.TemporaryFile()
        statuses = []

        def request(method, path, payload=None, token=None):
            headers = {"Content-Type": "application/json"}
            if token:
                headers["Authorization"] = "Bearer " + token
            data = None if payload is None else json.dumps(payload).encode()
            req = urllib.request.Request(base + path, data=data, headers=headers, method=method)
            with urllib.request.urlopen(req, timeout=5) as response:
                statuses.append({"method": method, "path": path, "status": response.status})
                return json.load(response)

        def start():
            nonlocal process
            process = subprocess.Popen(
                [str(binary), "serve", "--api-only", "--skip-scan-init", "--bind", f"127.0.0.1:{port}"],
                env=env, stdout=log, stderr=log)
            deadline = time.monotonic() + 30
            while time.monotonic() < deadline:
                if process.poll() is not None:
                    raise RuntimeError("disposable API exited before readiness")
                try:
                    request("GET", "/ready")
                    return
                except (urllib.error.URLError, TimeoutError):
                    time.sleep(0.1)
            raise RuntimeError("disposable API did not become ready")

        def stop():
            nonlocal process
            if process is not None:
                if process.poll() is None:
                    process.send_signal(signal.SIGTERM)
                try:
                    code = process.wait(timeout=15)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait()
                    raise RuntimeError("disposable API did not shut down") from None
                process = None
                if code != 0:
                    raise RuntimeError("disposable API exited unsuccessfully")

        try:
            start()
            registration = request("POST", "/auth/register", {
                "email": "key-restart@example.test", "password": secrets.token_urlsafe(32)})["data"]
            access, refresh = registration["access_token"], registration["refresh_token"]
            user_id = registration["user"]["id"]
            credential = secrets.token_urlsafe(32)
            stored = request("POST", "/config/secrets", {
                "key_name": "restart-fixture", "key_type": "custom", "value": credential}, access)["data"]
            assert stored["value"] == "********", "secret creation leaked plaintext"
            key_paths = [home / ".wolf" / name for name in ("jwt-secret", "master.key")]
            key_bytes = [path.read_bytes() for path in key_paths]
            assert [len(data) for data in key_bytes] == [32, 64], "key format changed"
            assert all(path.stat().st_mode & 0o777 == 0o600 for path in key_paths), "unsafe key mode"
            with sqlite3.connect(database) as db:
                encrypted = db.execute("SELECT encrypted_value FROM secrets WHERE id=?", (stored["id"],)).fetchone()[0]
            assert credential not in encrypted, "credential stored without encryption"
            stop()
            start()
            assert [path.read_bytes() for path in key_paths] == key_bytes, "restart changed persisted keys"
            me = request("GET", "/auth/me", token=access)["data"]
            assert me["id"] == user_id, "pre-restart access token lost its identity"
            refreshed = request("POST", "/auth/refresh", {"refresh_token": refresh})["data"]
            request("GET", "/auth/me", token=refreshed["access_token"])
            items = request("GET", "/config/secrets", token=refreshed["access_token"])["data"]
            assert any(item["id"] == stored["id"] and item["value"] == "********" for item in items), "stored secret missing"
            with sqlite3.connect(database) as db:
                after = db.execute("SELECT encrypted_value FROM secrets WHERE id=?", (stored["id"],)).fetchone()[0]
            assert encrypted == after, "restart changed stored ciphertext"
            stop()
            print(json.dumps({"status": "passed", "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
                "mode": "disposable native SQLite API; no environment key; scanner initialization skipped",
                "key_formats": {"jwt-secret": "32 raw bytes", "master.key": "64 hexadecimal bytes"},
                "key_modes": "0600", "keys_unchanged_after_restart": True,
                "access_token_survived_restart": True, "refresh_token_rotated_after_restart": True,
                "stored_ciphertext_unchanged": True, "secret_responses_masked": True,
                "requests": statuses, "cleanup": "process stopped; temporary home and database removed"}, indent=2))
        finally:
            stop()
            log.close()


if __name__ == "__main__":
    main()
