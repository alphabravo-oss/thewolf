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
    "setuptools==83.0.0" \
    "msgpack==1.2.1" \
    "detect-secrets==${DETECT_SECRETS_VERSION}" \
    "sqlfluff==${SQLFLUFF_VERSION}" \
    "yamllint==${YAMLLINT_VERSION}"

# pip 26.2 still vendors msgpack 1.1.2 and ships a CycloneDX file that also
# describes build-only setuptools 70.3.0 as a runtime component. Replace the
# vulnerable vendored implementation with the already pinned 1.2.1 package and
# remove the inaccurate nested SBOM; the final-image SPDX document is generated
# independently by the release gate from the resulting filesystem.
pip_vendor="$(python -c 'import pathlib, pip; print(pathlib.Path(pip.__file__).parent / "_vendor")')"
msgpack_source="$(python -c 'import pathlib, msgpack; print(pathlib.Path(msgpack.__file__).parent)')"
rm -rf "${pip_vendor}/msgpack"
cp -R "${msgpack_source}" "${pip_vendor}/msgpack"
rm -f "${pip_vendor}/bom.cdx.json"
MSGPACK_PUREPYTHON=1 python -c 'import pip._vendor.msgpack as m; assert m.__version__ == "1.2.1"'

for bin in detect-secrets sqlfluff yamllint; do
    ln -sf "/opt/wolf-scanners/py-core/bin/${bin}" "/usr/local/bin/${bin}"
done

echo "Core Python scanners installed (small, pure-Python only)."
