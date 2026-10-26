# Intentionally vulnerable Wolf sample

Do not deploy this code or use these dependencies. The Go program is inert:
its vulnerable handlers are present for static analysis and are never served.
It uses only the standard library, so Go analysis needs no module downloads.

Scan with Fast or Standard. Look for command injection, SQL injection and MD5
in `main.go`, fake credentials in `secrets.env`, Dockerfile warnings, and
separate vulnerabilities in the two dependencies pinned in `package-lock.json`.
The AWS credentials are public documentation examples. The GitHub token is
fabricated and has never been issued. None of these are usable credentials.

The first successful Standard scan becomes the automatic baseline. To demonstrate new
issues, copy `main.go` to `new.go`, change its package to `demonstration`, put
it in a new `demonstration/` directory, and scan again. Compare the new findings
with the existing baseline. Do not reset the baseline between scans.
