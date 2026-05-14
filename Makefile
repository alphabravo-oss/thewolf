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

# Docker — wolf-scanners (the bundled scanner image)
SCANNERS_IMAGE := wolf-scanners
SCANNERS_TAG   ?= $(VERSION)
SCANNERS_REGISTRY ?= ghcr.io/alphabravocompany
SCANNERS_REF   := $(SCANNERS_IMAGE):$(SCANNERS_TAG)

# Tools
GOLANGCI_LINT := $(shell command -v golangci-lint 2>/dev/null)
AIR           := $(shell command -v air 2>/dev/null || echo $(shell go env GOPATH)/bin/air)

.PHONY: all build test lint vet fmt ui-build dev dev-api dev-ui docker docker-up docker-down clean help \
        scanners-build scanners-smoke scanners-push dev-scanners test-integration

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

## ui-build: Build the Next.js UI
ui-build:
	@echo "==> Building UI..."
	@if [ -f $(UI_DIR)/package.json ]; then \
		cd $(UI_DIR) && npm ci && NEXT_TELEMETRY_DISABLED=1 npm run build; \
	else \
		echo "==> No UI package.json found, skipping"; \
	fi

## dev: Start backend (Air live-reload) + frontend (Next.js HMR) together
dev:
	@echo "==> Starting dev environment (API + UI)..."
	@echo "    API:  http://localhost:8778"
	@echo "    UI:   http://localhost:3000"
	@echo ""
	@mkdir -p $(BUILD_DIR)/tmp
	@trap 'kill 0' EXIT; \
		$(AIR) & \
		(cd $(UI_DIR) && npm run dev) & \
		wait

## dev-api: Start only the Go backend with Air live-reload
dev-api:
	@echo "==> Starting API with live-reload..."
	@mkdir -p $(BUILD_DIR)/tmp
	$(AIR)

## dev-ui: Start only the Next.js UI dev server
dev-ui:
	@if [ -f $(UI_DIR)/package.json ]; then \
		cd $(UI_DIR) && npm run dev; \
	else \
		echo "==> No UI package.json found"; \
	fi

## ui-dev: (alias) Start UI development server
ui-dev: dev-ui

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

## scanners-smoke: Run smoke-test inside each built image
scanners-smoke:
	@for variant in default jvm rust codeql; do \
		image=$(SCANNERS_IMAGE); \
		if [ "$$variant" != "default" ]; then image=$(SCANNERS_IMAGE)-$$variant; fi; \
		if docker image inspect $$image:dev >/dev/null 2>&1; then \
			echo "==> Smoke test: $$image:dev"; \
			docker run --rm $$image:dev /usr/local/bin/smoke-test.sh || exit $$?; \
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
	@if [ -d $(UI_DIR)/.next ]; then rm -rf $(UI_DIR)/.next; fi
	@if [ -d $(UI_DIR)/out ]; then rm -rf $(UI_DIR)/out; fi
	@echo "==> Clean"

## help: Show this help message
help:
	@echo "The Wolf — Available targets:"
	@echo ""
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## /  /' | column -t -s ':'
