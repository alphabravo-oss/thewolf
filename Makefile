# ============================================================
# The Wolf — Build & Development Makefile
# ============================================================

BINARY      := wolf
MODULE      := github.com/alphabravocompany/thewolf
CMD         := ./cmd/wolf/
BUILD_DIR   := ./build
UI_DIR      := ./ui

# Version info
VERSION     ?= $(shell git describe --tags --always 2>/dev/null || echo dev)
COMMIT      ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE  ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS     := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildDate=$(BUILD_DATE)

# Docker — wolf-slim (the orchestrator + UI)
DOCKER_IMAGE := thewolf
DOCKER_TAG   ?= $(VERSION)
# Registry for multi-arch publishing of the app image (docker-buildx).
DOCKER_REGISTRY ?= ghcr.io/alphabravo-oss

# Docker — wolf-scanners (the bundled scanner image).
# SCANNERS_REGISTRY defaults to the public GHCR namespace the runtime pulls
# from. Override it for a private mirror or local registry.
SCANNERS_IMAGE := wolf-scanners
SCANNERS_TAG   ?= stable
SCANNERS_REGISTRY ?= ghcr.io/alphabravo-oss
SCANNERS_REF   := $(SCANNERS_IMAGE):$(SCANNERS_TAG)

# Tools
GOLANGCI_LINT := $(shell command -v golangci-lint 2>/dev/null)
AIR           := $(shell command -v air 2>/dev/null || echo $(shell go env GOPATH)/bin/air)

.PHONY: all build test lint vet fmt ui-build dev dev-api dev-ui docker docker-buildx docker-up docker-down clean help helm-validate \
        scanners-build scanners-buildx scanners-buildx-all scanners-buildx-setup scanners-smoke scanners-push scanners-validate scanners-quality scanners-docs scanners-docs-check scanners-upstream-check scanners-bump scanners-os-packages-check scanners-os-packages-refresh scanners-vulnerability-dbs-check scanners-vulnerability-dbs-refresh dev-scanners test-integration

## all: Build everything (Go binary + UI)
all: build ui-build

## build: Build the Go binary
build:
	@echo "==> Building $(BINARY)..."
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY) $(CMD)
	@echo "==> Built: $(BUILD_DIR)/$(BINARY)"

## test: Run all Go tests
test:
	@echo "==> Running tests..."
	go test -race -count=1 -timeout 120s ./...

## test-short: Run tests in short mode (skip integration tests)
test-short:
	@echo "==> Running short tests..."
	go test -short -race -count=1 -timeout 60s ./...

## test-cover: Run tests with coverage report
test-cover:
	@echo "==> Running tests with coverage..."
	@mkdir -p $(BUILD_DIR)
	go test -race -count=1 -timeout 120s -coverprofile=$(BUILD_DIR)/coverage.out ./...
	go tool cover -html=$(BUILD_DIR)/coverage.out -o $(BUILD_DIR)/coverage.html
	@echo "==> Coverage report: $(BUILD_DIR)/coverage.html"

## lint: Run golangci-lint
lint:
ifdef GOLANGCI_LINT
	@echo "==> Running golangci-lint..."
	golangci-lint run ./...
else
	@echo "==> golangci-lint not installed. Install: https://golangci-lint.run/welcome/install/"
	@echo "==> Running go vet as fallback..."
	go vet ./...
endif

## vet: Run go vet
vet:
	@echo "==> Running go vet..."
	go vet ./...

## fmt: Format Go source files
fmt:
	@echo "==> Formatting Go files..."
	gofmt -s -w .
	@echo "==> Done"

## ui-build: Build the v2 Vite UI (ui/)
ui-build:
	@echo "==> Building Wolf UI (Vite)..."
	@if [ -f $(UI_DIR)/package.json ]; then \
		cd $(UI_DIR) && corepack pnpm install --frozen-lockfile && corepack pnpm build; \
	else \
		echo "==> No UI package.json found at $(UI_DIR), skipping"; \
	fi

## helm-validate: Lint and verify immutable-image and network-policy chart invariants
helm-validate:
	bash deploy/helm/wolf/tests/render-security.sh

## ui-dev-install: One-time install for local dev (after a fresh clone)
ui-dev-install:
	cd $(UI_DIR) && corepack pnpm install

## dev: Start backend (Air live-reload) + frontend (Vite HMR) together
dev:
	@echo "==> Starting dev environment (API + UI)..."
	@echo "    API:  http://localhost:8778"
	@echo "    UI:   http://localhost:3000  (proxies /api → :8778)"
	@echo ""
	@mkdir -p $(BUILD_DIR)/tmp
	@trap 'kill 0' EXIT; \
		$(AIR) & \
		(cd $(UI_DIR) && corepack pnpm dev) & \
		wait

## dev-api: Start only the Go backend with Air live-reload
dev-api:
	@echo "==> Starting API with live-reload..."
	@mkdir -p $(BUILD_DIR)/tmp
	$(AIR)

## dev-ui: Start only the Vite UI dev server
dev-ui:
	@if [ -f $(UI_DIR)/package.json ]; then \
		cd $(UI_DIR) && corepack pnpm dev; \
	else \
		echo "==> No UI package.json found at $(UI_DIR)"; \
	fi

## ui-dev: (alias) Start UI development server
ui-dev: dev-ui

## scanners-validate: Validate scanner manifest, version pins, image routing, embedded context, generated docs, and OS package lock
scanners-validate: scanners-docs-check scanners-os-packages-check scanners-vulnerability-dbs-check scanners-context-check scanners-quality
	@echo "==> Validating scanner metadata..."
	go run ./cmd/scannertools validate

## scanners-context-check: Verify the embedded scanner/fixer build context is canonical
scanners-context-check:
	@echo "==> Checking embedded scanner/fixer build context..."
	go run ./internal/scannerbuild/cmd/synccontext --check

## scanners-quality: Validate complete tool/variant fixture, parser, threshold, and vulnerability-DB coverage
scanners-quality:
	@echo "==> Validating deterministic scanner quality policy and corpus..."
	go run ./cmd/scannertools quality

## scanners-os-packages-check: Offline validation of the committed OS package lock and generated build inputs
scanners-os-packages-check:
	@echo "==> Checking locked scanner OS package snapshot..."
	go run ./cmd/scannertools os-packages --check

## scanners-os-packages-refresh: Explicit network refresh (usage: make scanners-os-packages-refresh SNAPSHOT=20260730T000000Z)
scanners-os-packages-refresh:
	@if [ -z "$(SNAPSHOT)" ]; then \
		echo "usage: make scanners-os-packages-refresh SNAPSHOT=YYYYMMDDTHHMMSSZ"; \
		exit 2; \
	fi
	go run ./cmd/scannertools os-packages --refresh --snapshot "$(SNAPSHOT)"
	go generate ./internal/scannerbuild/...
	go run ./cmd/scannertools lock
	go run ./cmd/scannertools os-packages --check

## scanners-vulnerability-dbs-check: Validate both exact Trivy DB locks offline
scanners-vulnerability-dbs-check:
	@echo "==> Checking locked Trivy vulnerability database identities..."
	go run ./cmd/scannertools vulnerability-dbs --check

## scanners-vulnerability-dbs-refresh: Resolve both Trivy DB tags into reviewable 8-day locks
scanners-vulnerability-dbs-refresh:
	@echo "==> Refreshing exact Trivy vulnerability database identities..."
	go run ./cmd/scannertools vulnerability-dbs --refresh $(if $(RECORDED_AT),--recorded-at "$(RECORDED_AT)",)
	go run ./internal/scannerbuild/cmd/synccontext
	go run ./cmd/scannertools lock
	go run ./cmd/scannertools vulnerability-dbs --check

## scanners-docs: Regenerate scanner tool documentation from scanners/tools.yaml
scanners-docs:
	@echo "==> Regenerating scanner tool docs..."
	go run ./cmd/scannertools docs

## scanners-docs-check: Verify generated scanner docs are current
scanners-docs-check:
	@echo "==> Checking generated scanner tool docs..."
	go run ./cmd/scannertools docs --check

## scanners-upstream-check: Verify upstream scanner image tags resolve for amd64/arm64
scanners-upstream-check:
	@echo "==> Checking upstream scanner image manifests..."
	go run ./cmd/scannertools upstream-images --platforms linux/amd64,linux/arm64

## scanners-bump: Bump one scanner pin (usage: make scanners-bump TOOL=semgrep VERSION=1.94.1)
scanners-bump:
	@if [ -z "$(TOOL)" ] || [ -z "$(VERSION)" ]; then \
		echo "usage: make scanners-bump TOOL=<name> VERSION=<version>"; \
		exit 2; \
	fi
	go run ./cmd/scannertools bump --tool "$(TOOL)" --version "$(VERSION)"

## scanners-build: Build the DEFAULT wolf-scanners image (core + small lang tools)
scanners-build:
	@echo "==> Building $(SCANNERS_REF) (default)..."
	docker build \
		--build-arg WOLF_VERSION=$(VERSION) \
		-f scanners/Dockerfile \
		-t $(SCANNERS_REF) \
		-t $(SCANNERS_IMAGE):dev \
		scanners/
	@echo "==> Built: $(SCANNERS_REF) (tagged also as $(SCANNERS_IMAGE):dev)"

## scanners-build-jvm: Build the JVM bucket image (infer + pmd + JDK)
scanners-build-jvm:
	docker build \
		--build-arg WOLF_VERSION=$(VERSION) \
		-f scanners/Dockerfile.jvm \
		-t $(SCANNERS_IMAGE)-jvm:$(SCANNERS_TAG) \
		-t $(SCANNERS_IMAGE)-jvm:dev \
		scanners/

## scanners-build-rust: Build the Rust bucket image (clippy + rust toolchain)
scanners-build-rust:
	docker build \
		--build-arg WOLF_VERSION=$(VERSION) \
		-f scanners/Dockerfile.rust \
		-t $(SCANNERS_IMAGE)-rust:$(SCANNERS_TAG) \
		-t $(SCANNERS_IMAGE)-rust:dev \
		scanners/

## scanners-build-codeql: Build the CodeQL bucket image (license-gated)
scanners-build-codeql:
	docker build \
		--build-arg WOLF_VERSION=$(VERSION) \
		-f scanners/Dockerfile.codeql \
		-t $(SCANNERS_IMAGE)-codeql:$(SCANNERS_TAG) \
		-t $(SCANNERS_IMAGE)-codeql:dev \
		scanners/

## scanners-build-all: Build all four scanner images
scanners-build-all: scanners-build scanners-build-jvm scanners-build-rust scanners-build-codeql

# Multi-arch (buildx). Builds for both arches and PUSHES (a manifest list can't
# be --load'ed into the local docker). Requires a buildx builder with QEMU for
# the non-native arch and a logged-in registry. The Dockerfiles run the smoke
# test strict on the native arch and lenient on the emulated arch, so a single
# Linux host can cross-build without QEMU-flaky tools (ruff) failing the build.
# Override PLATFORMS, SCANNERS_REGISTRY, or SCANNERS_TAG as needed.
PLATFORMS ?= linux/amd64,linux/arm64

## scanners-buildx-setup: Create+use a buildx builder with QEMU (run once per host)
scanners-buildx-setup:
	docker run --privileged --rm tonistiigi/binfmt --install all
	-docker buildx create --name wolf-multiarch --driver docker-container --use
	docker buildx inspect --bootstrap

## scanners-buildx: Multi-arch build+push of the DEFAULT image (amd64+arm64)
scanners-buildx:
	docker buildx build --platform $(PLATFORMS) --build-arg WOLF_VERSION=$(VERSION) \
		-f scanners/Dockerfile -t $(SCANNERS_REGISTRY)/$(SCANNERS_IMAGE):$(SCANNERS_TAG) --push scanners/

## scanners-buildx-all: Multi-arch build+push of default + jvm + rust (amd64+arm64); codeql is amd64-only
scanners-buildx-all: scanners-buildx
	docker buildx build --platform $(PLATFORMS) --build-arg WOLF_VERSION=$(VERSION) \
		-f scanners/Dockerfile.jvm  -t $(SCANNERS_REGISTRY)/$(SCANNERS_IMAGE)-jvm:$(SCANNERS_TAG)  --push scanners/
	docker buildx build --platform $(PLATFORMS) --build-arg WOLF_VERSION=$(VERSION) \
		-f scanners/Dockerfile.rust -t $(SCANNERS_REGISTRY)/$(SCANNERS_IMAGE)-rust:$(SCANNERS_TAG) --push scanners/
	docker buildx build --platform linux/amd64 --build-arg WOLF_VERSION=$(VERSION) \
		-f scanners/Dockerfile.codeql -t $(SCANNERS_REGISTRY)/$(SCANNERS_IMAGE)-codeql:$(SCANNERS_TAG) --push scanners/

## scanners-smoke: Run smoke-test inside each built image
scanners-smoke:
	@for variant in default jvm rust codeql; do \
		image=$(SCANNERS_IMAGE); \
		if [ "$$variant" != "default" ]; then image=$(SCANNERS_IMAGE)-$$variant; fi; \
		if docker image inspect $$image:dev >/dev/null 2>&1; then \
			echo "==> Smoke test: $$image:dev"; \
			docker run --rm -e WOLF_SMOKE_STRICT=1 $$image:dev /usr/local/bin/smoke-test.sh || exit $$?; \
		else \
			echo "==> Skip $$image:dev (not built)"; \
		fi \
	done

## scanners-push: Push every built scanner image to the configured registry
scanners-push:
	@for variant in "" -jvm -rust -codeql; do \
		image=$(SCANNERS_IMAGE)$$variant; \
		if docker image inspect $$image:$(SCANNERS_TAG) >/dev/null 2>&1; then \
			docker tag  $$image:$(SCANNERS_TAG) $(SCANNERS_REGISTRY)/$$image:$(SCANNERS_TAG); \
			docker push $(SCANNERS_REGISTRY)/$$image:$(SCANNERS_TAG); \
		fi \
	done

## dev-scanners: Open an interactive shell inside the default wolf-scanners image
dev-scanners:
	docker run --rm -it \
		--entrypoint /bin/bash \
		-v $(PWD):/scan:ro \
		$(SCANNERS_IMAGE):dev

## test-integration: Run integration tests (require wolf-scanners:dev to be built)
test-integration:
	@echo "==> Running integration tests (requires $(SCANNERS_IMAGE):dev)..."
	go test -tags=integration -race -count=1 -timeout 600s ./...

## docker: Build Docker image
docker:
	@echo "==> Building Docker image $(DOCKER_IMAGE):$(DOCKER_TAG)..."
	docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		-t $(DOCKER_IMAGE):$(DOCKER_TAG) \
		-t $(DOCKER_IMAGE):latest \
		.
	@echo "==> Built: $(DOCKER_IMAGE):$(DOCKER_TAG)"

## docker-buildx: Multi-arch build+push of the app image (amd64+arm64).
## Requires `make scanners-buildx-setup` (QEMU+builder) once and a logged-in
## registry. PUSHES (a manifest list can't be --load'ed). The app image has no
## arch-fragile smoke step, so it cross-builds cleanly. Override PLATFORMS,
## DOCKER_REGISTRY, or DOCKER_TAG as needed.
docker-buildx:
	@echo "==> Multi-arch build+push $(DOCKER_REGISTRY)/$(DOCKER_IMAGE):$(DOCKER_TAG) [$(PLATFORMS)]..."
	docker buildx build --platform $(PLATFORMS) \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		-t $(DOCKER_REGISTRY)/$(DOCKER_IMAGE):$(DOCKER_TAG) \
		-t $(DOCKER_REGISTRY)/$(DOCKER_IMAGE):latest \
		--push \
		.

## docker-up: Start services with docker-compose
docker-up:
	@echo "==> Starting docker-compose services..."
	WOLF_VERSION=$(VERSION) WOLF_COMMIT=$(COMMIT) WOLF_BUILD_DATE=$(BUILD_DATE) \
		docker compose up -d --build

## docker-up-pg: Start services with PostgreSQL
docker-up-pg:
	@echo "==> Starting docker-compose services with PostgreSQL..."
	WOLF_VERSION=$(VERSION) WOLF_COMMIT=$(COMMIT) WOLF_BUILD_DATE=$(BUILD_DATE) \
		docker compose --profile postgres up -d --build

## docker-down: Stop docker-compose services
docker-down:
	docker compose down

## docker-logs: Tail docker-compose logs
docker-logs:
	docker compose logs -f

## clean: Clean build artifacts
clean:
	@echo "==> Cleaning..."
	rm -rf $(BUILD_DIR)
	rm -f $(BINARY)
	@if [ -d $(UI_DIR)/dist ]; then rm -rf $(UI_DIR)/dist; fi
	@if [ -d $(UI_DIR)/out ]; then rm -rf $(UI_DIR)/out; fi
	@echo "==> Clean"

## help: Show this help message
help:
	@echo "The Wolf — Available targets:"
	@echo ""
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## /  /' | column -t -s ':'
