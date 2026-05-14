#!/usr/bin/env bash
# ============================================================
# Install Node-based scanners that are bundled in the default image.
#
# In wolf 2.0, spectral runs from stoplight/spectral upstream via the
# shim's UpstreamTools map. Nothing left here. This script is a hook
# for future Node-based scanners that lack an upstream image.
# ============================================================
set -euo pipefail

# shellcheck disable=SC1091
source /etc/wolf-scanners/versions.env

echo "core_node.sh: no node tools to install for this variant."
