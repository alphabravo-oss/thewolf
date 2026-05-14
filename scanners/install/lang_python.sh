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
    "bandit==${BANDIT_VERSION}" \
    "ruff==${RUFF_VERSION}" \
    "mypy==${MYPY_VERSION}" \
    "pip-audit==${PIP_AUDIT_VERSION}" \
    "radon==${RADON_VERSION}" \
    "vulture==${VULTURE_VERSION}"

for bin in bandit ruff mypy pip-audit radon vulture; do
    ln -sf "/opt/wolf-scanners/py-lang/bin/${bin}" "/usr/local/bin/${bin}"
done

echo "Python language-specific scanners installed."
