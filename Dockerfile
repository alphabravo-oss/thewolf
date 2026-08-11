# ============================================================
# checkov:skip=CKV_DOCKER_2 — bucket images are one-shot scanner containers, not long-lived services
# Stage 1: Build the Go binary
# ============================================================
FROM golang:1.26.5-alpine@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2 AS builder

RUN apk add --no-cache gcc musl-dev sqlite-dev git

WORKDIR /app

# Cache module downloads in a separate layer
COPY go.mod go.sum ./
RUN go mod download && go mod verify

# Copy source and build
COPY . .

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

RUN CGO_ENABLED=1 go build \
    -trimpath \
    -ldflags "-s -w \
        -X main.version=${VERSION} \
        -X main.commit=${COMMIT} \
        -X main.buildDate=${BUILD_DATE} \
        -linkmode external -extldflags '-static'" \
    -tags 'sqlite_omit_load_extension netgo osusergo' \
    -o /wolf ./cmd/wolf/

# The managed release lanes are separate trust and dependency boundaries.
# Build static, lane-locked entrypoints so one image cannot select another
# lane at runtime. scannertools is shared only by the fixed and quality lanes.
RUN mkdir -p /release-adapters \
    && CGO_ENABLED=0 go build -trimpath -ldflags '-s -w' \
      -o /release-adapters/fixed ./cmd/wolf-scanner-release-fixed-adapter \
    && CGO_ENABLED=0 go build -trimpath -ldflags '-s -w' \
      -o /release-adapters/quality ./cmd/wolf-scanner-release-quality-adapter \
    && CGO_ENABLED=0 go build -trimpath -ldflags '-s -w' \
      -o /release-adapters/integration ./cmd/wolf-scanner-release-integration-adapter \
    && CGO_ENABLED=0 go build -trimpath -ldflags '-s -w' \
      -o /release-adapters/scannertools ./cmd/scannertools \
    && CGO_ENABLED=0 go test -c -trimpath \
      -o /release-adapters/python-parser-qualification.test ./plugins/python \
    && CGO_ENABLED=0 go test -c -trimpath \
      -o /release-adapters/scanner-rollout-qualification.test ./internal/scannerrollout
RUN CGO_ENABLED=0 go build -trimpath -ldflags '-s -w' \
      -o /release-adapters/synccontext ./internal/scannerbuild/cmd/synccontext

# ============================================================
# Pinned adapter tools
# ============================================================
FROM golang:1.26.5-alpine@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2 AS scanner-release-oras-tool

ARG TARGETARCH
ARG ORAS_VERSION=1.3.3

RUN apk add --no-cache ca-certificates git \
    && case "${TARGETARCH}" in \
      amd64|arm64) ;; \
      *) echo "unsupported adapter target architecture: ${TARGETARCH}" >&2; exit 1 ;; \
    esac \
    && mkdir -p /out/bin /out/licenses/oras \
    && CGO_ENABLED=0 GOOS=linux GOARCH="${TARGETARCH}" \
      GOBIN=/out/bin go install -trimpath -ldflags '-s -w' \
      "oras.land/oras/cmd/oras@v${ORAS_VERSION}" \
    && install -m 0444 "$(go env GOPATH)/pkg/mod/oras.land/oras@v${ORAS_VERSION}/LICENSE" \
      /out/licenses/oras/LICENSE

FROM golang:1.26.5-alpine@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2 AS scanner-release-trivy-tool

ARG TARGETARCH
ARG TRIVY_VERSION=0.73.0
ARG TRIVY_GO_GIT_VERSION=5.19.2
ARG TRIVY_ORAS_GO_VERSION=2.6.2

RUN apk add --no-cache ca-certificates git \
    && case "${TARGETARCH}" in \
      amd64|arm64) ;; \
      *) echo "unsupported adapter target architecture: ${TARGETARCH}" >&2; exit 1 ;; \
    esac \
    && mkdir -p /src /out/bin /out/licenses/trivy \
    && git clone --depth 1 --branch "v${TRIVY_VERSION}" https://github.com/aquasecurity/trivy.git /src/trivy \
    && cd /src/trivy \
    && go get \
      "github.com/go-git/go-git/v5@v${TRIVY_GO_GIT_VERSION}" \
      "oras.land/oras-go/v2@v${TRIVY_ORAS_GO_VERSION}" \
    && go mod download all \
    && go mod verify \
    && CGO_ENABLED=0 GOEXPERIMENT=jsonv2 GOOS=linux GOARCH="${TARGETARCH}" go build -trimpath \
      -ldflags "-s -w -X=github.com/aquasecurity/trivy/pkg/version/app.ver=${TRIVY_VERSION}" \
      -o /out/bin/trivy ./cmd/trivy \
    && install -m 0444 LICENSE /out/licenses/trivy/LICENSE \
    && rm -rf /src/trivy /root/.cache/go-build

# ============================================================
# Managed scanner-release adapter images
# ============================================================
FROM alpine:3.24@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b AS scanner-release-fixed-adapter

LABEL org.opencontainers.image.title="Wolf fixed release adapter" \
      org.opencontainers.image.source="https://github.com/alphabravo-oss/thewolf"

RUN apk add --no-cache ca-certificates tzdata git \
    && addgroup -S wolf \
    && adduser -S wolf -G wolf

COPY --from=builder --chmod=0555 /release-adapters/fixed /usr/local/bin/wolf-release-adapter
COPY --from=builder --chmod=0555 /release-adapters/scannertools /usr/local/bin/scannertools
COPY --from=builder --chmod=0555 /release-adapters/synccontext /usr/local/bin/synccontext
COPY --from=builder /usr/local/go /usr/local/go
COPY --from=scanner-release-oras-tool --chmod=0555 /out/bin/oras /usr/local/bin/oras
COPY --from=scanner-release-oras-tool /out/licenses/oras /usr/share/licenses/oras

ENV HOME=/tmp PATH=/usr/local/go/bin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
WORKDIR /workspace
USER wolf
ENTRYPOINT ["/usr/local/bin/wolf-release-adapter"]

FROM alpine:3.24@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b AS scanner-release-quality-adapter

LABEL org.opencontainers.image.title="Wolf quality release adapter" \
      org.opencontainers.image.source="https://github.com/alphabravo-oss/thewolf" \
      org.opencontainers.image.version.trivy="0.73.0" \
      org.opencontainers.image.version.oras="1.3.3"

RUN apk add --no-cache ca-certificates tzdata docker-cli git \
    && addgroup -S wolf \
    && adduser -S wolf -G wolf

COPY --from=builder --chmod=0555 /release-adapters/quality /usr/local/bin/wolf-release-adapter
COPY --from=builder --chmod=0555 /release-adapters/scannertools /usr/local/bin/scannertools
COPY --from=scanner-release-oras-tool --chmod=0555 /out/bin/oras /usr/local/bin/oras
COPY --from=scanner-release-trivy-tool --chmod=0555 /out/bin/trivy /usr/local/bin/trivy
COPY --from=scanner-release-oras-tool /out/licenses/oras /usr/share/licenses/oras
COPY --from=scanner-release-trivy-tool /out/licenses/trivy /usr/share/licenses/trivy

ENV HOME=/tmp
WORKDIR /workspace
USER wolf
ENTRYPOINT ["/usr/local/bin/wolf-release-adapter"]

FROM alpine:3.24@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b AS scanner-release-integration-adapter

LABEL org.opencontainers.image.title="Wolf integration release adapter" \
      org.opencontainers.image.source="https://github.com/alphabravo-oss/thewolf"

# The four qualification entrypoints and their fixed Compose fixture helper
# use trusted precompiled qualification binaries, jq and Python timing helpers
# in addition to Docker/Compose, kind and kubectl. Candidate checkout code is
# never compiled or executed in this credential-bearing lane.
RUN apk add --no-cache ca-certificates tzdata bash docker-cli docker-cli-compose \
      jq kind kubectl python3 \
    && addgroup -S wolf \
    && adduser -S wolf -G wolf \
    && mkdir -p /usr/local/libexec/wolf/release-qualification

COPY --from=builder --chmod=0555 /release-adapters/integration /usr/local/bin/wolf-release-adapter
COPY --from=builder --chmod=0555 /release-adapters/python-parser-qualification.test \
  /usr/local/libexec/wolf/release-qualification/python-parser-qualification.test
COPY --from=builder --chmod=0555 /release-adapters/scanner-rollout-qualification.test \
  /usr/local/libexec/wolf/release-qualification/scanner-rollout-qualification.test
COPY --from=scanner-release-oras-tool --chmod=0555 /out/bin/oras /usr/local/bin/oras
COPY --from=scanner-release-oras-tool /out/licenses/oras /usr/share/licenses/oras
COPY --chmod=0555 scripts/e2e/scanner-rollout-compose.sh scripts/e2e/scanner-rollout-kind.sh \
  scripts/e2e/scanner-rollout-compose-fixture-adapter.sh \
  scripts/e2e/scanner-quality-compose.sh scripts/e2e/scanner-quality-kind.sh \
  /usr/local/libexec/wolf/release-qualification/

ENV HOME=/tmp
WORKDIR /workspace
USER wolf
ENTRYPOINT ["/usr/local/bin/wolf-release-adapter"]

# ============================================================
# Dedicated proposal-worker runtime
# ============================================================
# Proposal generation validates a freshly fetched exact Git commit with the
# repository's pinned scannertools command. Keep the Go toolchain in this
# narrowly scoped image; the API/runtime image below remains minimal.
FROM golang:1.26.5-alpine@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2 AS proposal-runtime

LABEL org.opencontainers.image.title="Wolf scanner proposal runtime" \
      org.opencontainers.image.description="Non-root worker for generating and validating immutable scanner release proposals" \
      org.opencontainers.image.source="https://github.com/alphabravo-oss/thewolf"

RUN apk add --no-cache ca-certificates tzdata git \
    && addgroup -S wolf \
    && adduser -S wolf -G wolf

COPY --from=builder /wolf /usr/local/bin/wolf

RUN mkdir -p /home/wolf/.wolf \
    && chown -R wolf:wolf /home/wolf

USER wolf
ENTRYPOINT ["wolf"]
CMD ["scanner-release-worker", "--role=proposal"]

# ============================================================
# Stage 2: Build the Vite UI (ui/, pnpm)
# ============================================================
FROM node:24-alpine@sha256:d32cdf619f63fe0471182d08996dd516c6275bb5fd31ae06e55a570bd9e1ad43 AS ui-builder

RUN npm install -g pnpm@9.15.9

WORKDIR /app/ui

# Install deps from the lockfile first for caching.
COPY ui/package.json ui/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile

# Build the SPA.
COPY ui/ ./
RUN pnpm build

# ============================================================
# Stage 3: Minimal runtime
# ============================================================
FROM alpine:3.24@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b AS runtime

LABEL org.opencontainers.image.title="The Wolf" \
      org.opencontainers.image.description="AI-Powered Code Analysis & Fix Engine" \
      org.opencontainers.image.source="https://github.com/alphabravo-oss/thewolf" \
      org.opencontainers.image.vendor="WolfCorp"

RUN apk add --no-cache ca-certificates tzdata docker-cli docker-cli-buildx git \
    && addgroup -S wolf \
    && adduser -S wolf -G wolf \
    && addgroup -S docker \
    && adduser wolf docker \
    && test -x /usr/libexec/docker/cli-plugins/docker-buildx

COPY --from=builder /wolf /usr/local/bin/wolf
COPY --from=builder /app/scanners/tools.yaml /usr/share/wolf/scanners/tools.yaml
COPY --from=builder /app/scanners/scanner-lock.yaml /usr/share/wolf/scanners/scanner-lock.yaml

# Copy the SPA build output. The Go server's MountStaticUI auto-discovers
# this path; WOLF_UI_DIR can override it.
COPY --from=ui-builder /app/ui/dist /usr/share/wolf/ui/dist

RUN mkdir -p /home/wolf/.wolf \
    && chown -R wolf:wolf /home/wolf/.wolf

USER wolf

EXPOSE 8778

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["wolf", "version"]

ENTRYPOINT ["wolf"]
CMD ["serve", "--bind", "0.0.0.0:8778"]
