#!/usr/bin/env bash
# ============================================================
# Install Python-specific scanners (Python repos only).
# Separate venv from core so the two layers cache independently.
# ============================================================
set -euo pipefail

# shellcheck disable=SC1091
source /etc/wolf-scanners/versions.env

python3 -m venv /opt/wolf-scanners/py-lang
# shellcheck disable=SC1091
. /opt/wolf-scanners/py-lang/bin/activate

pip install --no-cache-dir --upgrade pip
pip install --no-cache-dir \
    "setuptools==83.0.0" \
    "msgpack==1.2.1" \
    "bandit==${BANDIT_VERSION}" \
    "ruff==${RUFF_VERSION}" \
    "mypy==${MYPY_VERSION}" \
    "pip-audit==${PIP_AUDIT_VERSION}" \
    "radon==${RADON_VERSION}" \
    "vulture==${VULTURE_VERSION}"

# Keep pip's private msgpack copy aligned with the patched runtime dependency;
# see core_python.sh for why its upstream nested SBOM is removed.
pip_vendor="$(python -c 'import pathlib, pip; print(pathlib.Path(pip.__file__).parent / "_vendor")')"
msgpack_source="$(python -c 'import pathlib, msgpack; print(pathlib.Path(msgpack.__file__).parent)')"
rm -rf "${pip_vendor}/msgpack"
cp -R "${msgpack_source}" "${pip_vendor}/msgpack"
rm -f "${pip_vendor}/bom.cdx.json"
MSGPACK_PUREPYTHON=1 python -c 'import pip._vendor.msgpack as m; assert m.__version__ == "1.2.1"'

for bin in bandit ruff mypy pip-audit radon vulture; do
    ln -sf "/opt/wolf-scanners/py-lang/bin/${bin}" "/usr/local/bin/${bin}"
done

echo "Python language-specific scanners installed."
