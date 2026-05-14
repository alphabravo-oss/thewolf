# ============================================================
# checkov:skip=CKV_DOCKER_2 — bucket images are one-shot scanner containers, not long-lived services
# wolf-scanners — DEFAULT IMAGE
# ============================================================
# Slim bundle of the tools that don't have a usable upstream image.
# Heavy / cross-language tools (semgrep, trivy, grype, syft, gitleaks,
# trufflehog, hadolint, checkov, tflint, kubescape, kube-linter, nuclei,
# vale, spectral, scancode, scorecard, osv-scanner, dockle) run from
# the maintainer's official image via the wolf shim's UpstreamTools map.
#
# What this image carries:
#   - python lang tools : bandit, ruff, mypy, pip-audit, radon, vulture
#   - go lang tools     : gosec, staticcheck, govulncheck
#   - js lang tools     : eslint (npm bundles npm-audit)
#   - ruby lang tools   : brakeman, rubocop
#   - php lang tools    : phpstan
#   - native lang tools : cppcheck, shellcheck
#   - swift lang tools  : swiftlint (only on supported arches)
#   - pure-python core  : detect-secrets, sqlfluff
#
# Build:
#   docker build -t wolf-scanners:dev -f scanners/Dockerfile scanners/
# ============================================================

FROM debian:trixie-slim AS base

ARG WOLF_VERSION=dev
LABEL org.opencontainers.image.title="wolf-scanners" \
      org.opencontainers.image.description="Slim language-tool image for the Wolf code analysis engine" \
      org.opencontainers.image.source="https://github.com/alphabravocompany/thewolf" \
      org.opencontainers.image.vendor="AlphaBravo" \
      org.opencontainers.image.version="${WOLF_VERSION}"

ENV DEBIAN_FRONTEND=noninteractive \
    WOLF_SCANNERS_VARIANT=default

# Base packages. Note we no longer need libicu-dev (scancode is upstream)
# or libxml2-dev (semgrep is upstream).
RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates curl wget unzip tar gzip xz-utils \
        git jq \
        python3 python3-pip python3-venv python3-dev \
        nodejs npm \
        ruby ruby-dev rubygems \
        php-cli \
        build-essential pkg-config \
        libssl-dev libffi-dev \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

COPY versions.env       /etc/wolf-scanners/versions.env
COPY trufflehog-excludes.txt /etc/wolf-scanners/trufflehog-excludes.txt
COPY wolf-tool-entry    /usr/local/bin/wolf-tool-entry
COPY smoke-test.sh      /usr/local/bin/smoke-test.sh
COPY install/db-refresh.sh /usr/local/bin/db-refresh
RUN chmod +x /usr/local/bin/wolf-tool-entry /usr/local/bin/smoke-test.sh /usr/local/bin/db-refresh

# Core: small pure-Python tools that don't have a stable upstream image.
COPY install/core_python.sh   /tmp/install/core_python.sh
RUN  bash /tmp/install/core_python.sh

# Core node: currently nothing (spectral is upstream).
COPY install/core_node.sh     /tmp/install/core_node.sh
RUN  bash /tmp/install/core_node.sh

# Core native (shellcheck).
COPY install/core_native.sh   /tmp/install/core_native.sh
RUN  bash /tmp/install/core_native.sh

# Language-specific tools.
COPY install/lang_python.sh   /tmp/install/lang_python.sh
RUN  bash /tmp/install/lang_python.sh

COPY install/lang_node.sh     /tmp/install/lang_node.sh
RUN  bash /tmp/install/lang_node.sh

COPY install/go-tools.sh      /tmp/install/go-tools.sh
RUN  bash /tmp/install/go-tools.sh

COPY install/ruby.sh          /tmp/install/ruby.sh
RUN  bash /tmp/install/ruby.sh

COPY install/php.sh           /tmp/install/php.sh
RUN  bash /tmp/install/php.sh

COPY install/swift.sh         /tmp/install/swift.sh
RUN  bash /tmp/install/swift.sh

COPY install/lang_native.sh   /tmp/install/lang_native.sh
RUN  bash /tmp/install/lang_native.sh

# Strip build-only packages once all the installers have run. The native
# extensions they compiled are already linked into pip/gem/npm trees, so
# the toolchain itself is dead weight (and a CVE surface). Reuse the
# runtime libs (libssl3, libffi8) — only the -dev / build-essential
# variants come off.
RUN apt-get update && apt-get purge -y --auto-remove \
        build-essential pkg-config \
        python3-dev ruby-dev \
        libssl-dev libffi-dev \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/* /var/cache/apt/* \
              /tmp/install /root/.npm /root/.cache \
              /usr/share/doc/* /usr/share/man/*

RUN groupadd -g 1000 wolf && \
    useradd  -u 1000 -g 1000 -m -d /home/wolf -s /bin/bash wolf

USER wolf
WORKDIR /scan
ENTRYPOINT ["/usr/local/bin/wolf-tool-entry"]
