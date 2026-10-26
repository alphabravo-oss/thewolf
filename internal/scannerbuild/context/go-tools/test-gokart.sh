#!/usr/bin/env bash
# Exercise the actual analyzer and SARIF/exit contract, without network access.
# Usage: bash scanners/go-tools/test-gokart.sh /absolute/path/to/gokart
set -euo pipefail
binary="$(command -v "${1:-gokart}")"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
export GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local GOWORK=off
export GOCACHE="${GOCACHE:-$work/cache}"
cd "$work"
printf 'sources:\n  types:\n    net/http: [Request]\n' > analyzers.yml
printf 'module wolf.test/gokart\n\ngo 1.23\n' > go.mod
cat > fixture.go <<'GO'
package fixture
import (
    "net/http"
    "os/exec"
)
func Handler(w http.ResponseWriter, r *http.Request) {
    _ = exec.Command("sh", "-c", r.URL.Query().Get("cmd")).Run()
}
GO
status=0
"$binary" scan --sarif --exitCode --input "$work/analyzers.yml" ./... > unsafe.json || status=$?
[[ "$status" == 1 ]] || { echo "unsafe scan exit $status, expected findings exit 1" >&2; exit 1; }
python3 - <<'PY'
import json
with open('unsafe.json') as source:
    report = json.load(source)
assert any(r['ruleId'] == 'CWE-78: OS Command Injection'
           for run in report['runs'] for r in run.get('results', [])), report
PY
# A source parameter and sink on the same line are still distinct expressions.
cat > fixture.go <<'GO'
package fixture
import ("net/http"; "os/exec")
func Handler(w http.ResponseWriter, r *http.Request) { _ = exec.Command("sh", "-c", r.URL.Query().Get("cmd")).Run() }
GO
status=0
"$binary" scan --sarif --exitCode --input "$work/analyzers.yml" ./... > inline.json || status=$?
[[ "$status" == 1 ]] || { echo "inline unsafe scan exit $status, expected findings exit 1" >&2; exit 1; }
python3 - <<'PY'
import json
with open('inline.json') as source:
    report = json.load(source)
assert any(r['ruleId'] == 'CWE-78: OS Command Injection'
           for run in report['runs'] for r in run.get('results', [])), report
PY
cat > fixture.go <<'GO'
package fixture
import "net/http"
func Handler(w http.ResponseWriter, r *http.Request) {
    _, _ = w.Write([]byte("safe"))
}
GO
"$binary" scan --sarif --exitCode --input "$work/analyzers.yml" ./... > safe.json
python3 - <<'PY'
import json
with open('safe.json') as source:
    report = json.load(source)
assert report['runs'] and all(not run.get('results') for run in report['runs']), report
PY
printf 'package fixture\nfunc Broken( {\n' > fixture.go
if "$binary" scan --sarif --exitCode --input "$work/analyzers.yml" ./... > broken.json 2> broken.log; then
    echo 'invalid source was reported as a successful scan' >&2
    exit 1
fi
echo 'GoKart unsafe, inline-unsafe, safe, and invalid-source execution checks passed'
