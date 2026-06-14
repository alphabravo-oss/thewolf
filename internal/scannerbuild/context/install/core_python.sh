#!/usr/bin/env bash
# ============================================================
# Install Python-based scanners that are bundled in the default image.
#
# In wolf 2.0, the heavy/cross-language python tools (semgrep, checkov)
# are run from upstream-official images via the shim's
# UpstreamTools map. What remains here are the small pure-Python
# scanners we couldn't find a maintained upstream image for.
# ============================================================
set -euo pipefail

# shellcheck disable=SC1091
source /etc/wolf-scanners/versions.env

python3 -m venv /opt/wolf-scanners/py-core
# shellcheck disable=SC1091
. /opt/wolf-scanners/py-core/bin/activate

pip install --no-cache-dir --upgrade pip
pip install --no-cache-dir \
    "detect-secrets==${DETECT_SECRETS_VERSION}" \
    "sqlfluff==${SQLFLUFF_VERSION}" \
    "yamllint==${YAMLLINT_VERSION}"

for bin in detect-secrets sqlfluff yamllint; do
    ln -sf "/opt/wolf-scanners/py-core/bin/${bin}" "/usr/local/bin/${bin}"
done

echo "Core Python scanners installed (small, pure-Python only)."
