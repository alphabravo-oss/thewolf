# ============================================================
# Stage 1: Build the Go binary
# ============================================================
FROM golang:1.24-alpine AS builder

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

# ============================================================
# Stage 2: Build the Next.js UI
# ============================================================
FROM node:20-alpine AS ui-builder

WORKDIR /app/ui

COPY ui/package.json ui/package-lock.json* ./
RUN if [ -f package.json ]; then npm ci --prefer-offline; fi

COPY ui/ ./
RUN if [ -f package.json ]; then \
        NEXT_TELEMETRY_DISABLED=1 npm run build; \
    fi

# ============================================================
# Stage 3: Minimal runtime
# ============================================================
FROM alpine:3.20 AS runtime

LABEL org.opencontainers.image.title="The Wolf" \
      org.opencontainers.image.description="AI-Powered Code Analysis & Fix Engine" \
      org.opencontainers.image.source="https://github.com/alphabravocompany/thewolf" \
      org.opencontainers.image.vendor="WolfCorp"

RUN apk add --no-cache ca-certificates tzdata docker-cli \
    && addgroup -S wolf \
    && adduser -S wolf -G wolf \
    && addgroup -S docker \
    && adduser wolf docker

COPY --from=builder /wolf /usr/local/bin/wolf

# Copy UI build output if it exists
COPY --from=ui-builder /app/ui/.next/standalone /usr/share/wolf/ui/standalone 2>/dev/null || true
COPY --from=ui-builder /app/ui/.next/static /usr/share/wolf/ui/static 2>/dev/null || true
COPY --from=ui-builder /app/ui/public /usr/share/wolf/ui/public 2>/dev/null || true
# Fallback: copy static export if standalone not available
COPY --from=ui-builder /app/ui/out /usr/share/wolf/ui/out 2>/dev/null || true

RUN mkdir -p /home/wolf/.wolf \
    && chown -R wolf:wolf /home/wolf/.wolf

USER wolf

EXPOSE 8778

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["wolf", "version"]

ENTRYPOINT ["wolf"]
CMD ["serve", "--bind", "0.0.0.0"]
