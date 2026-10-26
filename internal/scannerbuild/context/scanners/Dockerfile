# ============================================================
# checkov:skip=CKV_DOCKER_2 — bucket images are one-shot scanner containers, not long-lived services
# wolf-scanners — DEFAULT IMAGE
# ============================================================
# Slim bundle of the tools that don't have a usable upstream image.
# Heavy / cross-language tools (semgrep, trivy, grype, syft, gitleaks,
# trufflehog, hadolint, checkov, tflint, kubescape, kube-linter, nuclei,
# vale, spectral, scorecard, osv-scanner, dockle) run from
# the maintainer's official image via the wolf shim's UpstreamTools map.
#
# What this image carries:
#   - python lang tools : bandit, ruff, mypy, pip-audit, radon, vulture
#   - go lang tools     : gosec, staticcheck, govulncheck
#   - js lang tools     : eslint (npm bundles npm-audit)
#   - ruby lang tools   : brakeman, rubocop
#   - php lang tools    : phpstan
#   - native lang tools : cppcheck, shellcheck
#   - pure-python core  : detect-secrets, sqlfluff
#
# Build:
#   docker build -t wolf-scanners:dev -f scanners/Dockerfile scanners/
# ============================================================

FROM debian:trixie-slim@sha256:4e401d95de7083948053197a9c3913343cd06b706bf15eb6a0c3ccd26f436a0e AS pins
COPY versions.env /tmp/versions.env
COPY install/ /tmp/install/
RUN bash /tmp/install/version-inputs.sh /tmp/versions.env /tmp/install /pins

FROM debian:trixie-slim@sha256:4e401d95de7083948053197a9c3913343cd06b706bf15eb6a0c3ccd26f436a0e AS base
ENV DEBIAN_FRONTEND=noninteractive WOLF_SCANNERS_VARIANT=default \
    GEM_HOME=/opt/wolf-scanners/ruby GEM_PATH=/opt/wolf-scanners/ruby \
    PATH=/opt/wolf-scanners/ruby/bin:/usr/local/go-toolchain/bin:$PATH
COPY os-packages/ /etc/wolf-scanners/os-packages/
COPY install/os-packages.sh /usr/local/bin/install-os-packages
RUN bash /usr/local/bin/install-os-packages default

# Independent installers let BuildKit build in parallel and reuse unrelated
# language layers. Download/build caches are mounts, never runtime contents.
FROM base AS python-core
COPY --from=pins /pins/core_python.env /etc/wolf-scanners/versions.env
COPY install/core_python.sh /tmp/install.sh
RUN --mount=type=cache,target=/root/.cache/pip,sharing=locked bash /tmp/install.sh

FROM base AS python-lang
COPY --from=pins /pins/lang_python.env /etc/wolf-scanners/versions.env
COPY install/lang_python.sh /tmp/install.sh
RUN --mount=type=cache,target=/root/.cache/pip,sharing=locked bash /tmp/install.sh

FROM base AS go-tools
ARG TARGETARCH
COPY --from=pins /pins/go-tools.env /etc/wolf-scanners/versions.env
COPY go-tools/ /etc/wolf-scanners/go-tools/
COPY install/go-tools.sh /tmp/install.sh
RUN --mount=type=cache,id=wolf-go-mod,target=/root/.cache/go-mod,sharing=locked \
    --mount=type=cache,id=wolf-go-build-${TARGETARCH},target=/root/.cache/go-build,sharing=locked \
    GOMODCACHE=/root/.cache/go-mod GOCACHE=/root/.cache/go-build bash /tmp/install.sh

FROM base AS node-tools
COPY --from=pins /pins/lang_node.env /etc/wolf-scanners/versions.env
COPY install/lang_node.sh /tmp/install/lang_node.sh
RUN --mount=type=cache,target=/root/.npm,sharing=locked bash /tmp/install/lang_node.sh

FROM base AS ruby-tools
COPY --from=pins /pins/ruby.env /etc/wolf-scanners/versions.env
COPY install/ruby.sh /tmp/install/ruby.sh
RUN bash /tmp/install/ruby.sh

FROM base AS php-tools
COPY --from=pins /pins/php.env /etc/wolf-scanners/versions.env
COPY install/php.sh /tmp/install/php.sh
RUN bash /tmp/install/php.sh

FROM base AS runtime
COPY --from=python-core /opt/wolf-scanners/py-core /opt/wolf-scanners/py-core
COPY --from=python-lang /opt/wolf-scanners/py-lang /opt/wolf-scanners/py-lang
COPY --from=go-tools /usr/local/go-toolchain /usr/local/go-toolchain
COPY --from=go-tools /usr/local/bin/gosec /usr/local/bin/staticcheck /usr/local/bin/govulncheck /usr/local/bin/gokart /usr/local/bin/
RUN for tool in detect-secrets sqlfluff yamllint; do ln -s /opt/wolf-scanners/py-core/bin/$tool /usr/local/bin/$tool; done; \
    for tool in bandit ruff mypy pip-audit radon vulture; do ln -s /opt/wolf-scanners/py-lang/bin/$tool /usr/local/bin/$tool; done

RUN rm -rf /usr/lib/node_modules
COPY --from=node-tools /usr/lib/node_modules/ /usr/lib/node_modules/
RUN ln -sf ../lib/node_modules/npm/bin/npm-cli.js /usr/bin/npm \
    && ln -sf ../lib/node_modules/npm/bin/npx-cli.js /usr/bin/npx \
    && ln -sf ../lib/node_modules/eslint/bin/eslint.js /usr/bin/eslint \
    && ln -sf ../lib/node_modules/markdownlint-cli/markdownlint.js /usr/bin/markdownlint
COPY --from=ruby-tools /opt/wolf-scanners/ruby/ /opt/wolf-scanners/ruby/
COPY --from=php-tools /usr/local/bin/phpstan /usr/local/bin/phpstan

COPY install/core_native.sh install/lang_native.sh /tmp/install/
RUN bash /tmp/install/core_native.sh && bash /tmp/install/lang_native.sh
RUN apt-get purge -y --auto-remove build-essential pkg-config python3-dev ruby-dev libssl-dev libffi-dev \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/* /var/cache/apt/* /tmp/install /root/.npm /root/.cache /usr/share/doc/* /usr/share/man/*

COPY versions.env /etc/wolf-scanners/versions.env
COPY trufflehog-excludes.txt /etc/wolf-scanners/trufflehog-excludes.txt
COPY wolf-tool-entry smoke-test.sh install/db-refresh.sh /usr/local/bin/
COPY go-tools/test-gokart.sh /usr/local/bin/test-gokart.sh
RUN mv /usr/local/bin/db-refresh.sh /usr/local/bin/db-refresh \
    && chmod +x /usr/local/bin/wolf-tool-entry /usr/local/bin/smoke-test.sh /usr/local/bin/db-refresh
ARG TARGETPLATFORM
ARG BUILDPLATFORM
RUN STRICT=1; [ -n "$TARGETPLATFORM" ] && [ "$TARGETPLATFORM" != "$BUILDPLATFORM" ] && STRICT=0; \
    WOLF_SMOKE_STRICT="$STRICT" /usr/local/bin/smoke-test.sh
RUN groupadd -g 1000 wolf && useradd -u 1000 -g 1000 -m -d /home/wolf -s /bin/bash wolf

# Release identity belongs after installation: a new release ID must not
# rebuild identical tool versions.
ARG WOLF_VERSION=dev
LABEL org.opencontainers.image.title="wolf-scanners" \
      org.opencontainers.image.description="Language scanners for the Wolf code analysis engine" \
      org.opencontainers.image.source="https://github.com/alphabravo-oss/thewolf" \
      org.opencontainers.image.vendor="AlphaBravo" \
      org.opencontainers.image.version="${WOLF_VERSION}"
USER wolf
WORKDIR /scan
ENTRYPOINT ["/usr/local/bin/wolf-tool-entry"]
