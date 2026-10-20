# Astronomer — single orchestrator for build, test, lint, generate, dev loop,
# security gates and container images (Technology Selection Guide ch.04/06/07).
#
# Every target is phony. VERSION/COMMIT/DATE use ?= so CI can override them.

.PHONY: help dev dev-down dev-status dev-logs build test test-integration lint vet fmt \
        generate sqlc migrate check security gosec govulncheck secrets-scan \
        fips-build updates go-image-check docker-build docker-build-% helm-lint e2e e2e-live frontend-build \
        frontend-check clean $(BINARIES)

SHELL := /bin/bash

# ── Build identity ───────────────────────────────────────────────────────────
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
MODULE      = github.com/alphabravocompany/astronomer-go
# The Go toolchain version is derived from go.mod — the Dockerfiles declare
# `ARG GO_VERSION` with no default so an image can never drift from go.mod.
GO_VERSION := $(shell awk '/^go /{print $$2}' go.mod)
BINARIES   := server worker agent astro

GO_LDFLAGS  = -s -w \
              -X $(MODULE)/pkg/version.Version=$(VERSION) \
              -X $(MODULE)/pkg/version.GitCommit=$(GIT_COMMIT) \
              -X $(MODULE)/pkg/version.BuildDate=$(BUILD_DATE)
FIPS_LDFLAGS = $(GO_LDFLAGS) -X $(MODULE)/pkg/version.FIPSMode=true

# ── Pinned tools (go run keeps them lockfile-independent and reproducible) ──
SQLC_VERSION        ?= v1.31.1
# gosec is pinned in tools/gosec/go.mod (v2.29.0)
GOVULNCHECK_VERSION ?= v1.9.0
GOTESTSUM_VERSION   ?= v1.13.0
GOLANGCI_LINT       ?= golangci-lint
GOTESTSUM           ?= go run gotest.tools/gotestsum@$(GOTESTSUM_VERSION)
SQLC                ?= go run github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)
OAPI_CODEGEN        ?= go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.5.1
TEST_RESULTS_DIR    ?= test-results

# ── Images ───────────────────────────────────────────────────────────────────
IMG_REGISTRY ?= ghcr.io/alphabravo-oss
IMG_TAG      ?= $(VERSION)
IMAGES       := server worker agent shell dr frontend
# Go builder image digest pin (one checked-in source of truth for every Go stage).
include deploy/docker/go-image.lock
DOCKER_BUILD_ARGS = --build-arg GO_VERSION=$(GO_VERSION) \
                    --build-arg GO_IMAGE_DIGEST=$(GO_IMAGE_DIGEST) \
                    --build-arg VERSION=$(VERSION) \
                    --build-arg GIT_COMMIT=$(GIT_COMMIT) \
                    --build-arg BUILD_DATE=$(BUILD_DATE)

help: ## Show this help
	@grep -E '^[a-zA-Z0-9_%-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'

# ── Dev loop ─────────────────────────────────────────────────────────────────
dev: ## Start the full local stack (Postgres, Redis, telemetry, Air server/worker, Vite)
	./scripts/dev.sh up

dev-down: ## Stop the local stack
	./scripts/dev.sh down

dev-status: ## Show local stack status
	./scripts/dev.sh status

dev-logs: ## Tail local stack logs
	./scripts/dev.sh logs

# ── Build ────────────────────────────────────────────────────────────────────
build: $(BINARIES) ## Build all production binaries under bin/

$(BINARIES):
	@mkdir -p bin
	CGO_ENABLED=0 go build -trimpath -ldflags "$(GO_LDFLAGS)" -o bin/$@ ./cmd/$@

fips-build: ## Build FIPS-mode binaries (GOEXPERIMENT=boringcrypto, -tags fips) under bin/fips/
	@mkdir -p bin/fips
	@for b in $(BINARIES); do \
		echo "fips-build $$b"; \
		GOEXPERIMENT=boringcrypto CGO_ENABLED=1 go build -trimpath -tags fips -ldflags "$(FIPS_LDFLAGS)" -o bin/fips/$$b ./cmd/$$b || exit 1; \
	done

# ── Test ─────────────────────────────────────────────────────────────────────
test: ## Unit tests (no Docker) with race detector, JUnit XML in test-results/
	@mkdir -p $(TEST_RESULTS_DIR)
	$(GOTESTSUM) --format pkgname --junitfile $(TEST_RESULTS_DIR)/unit.xml -- -race -count=1 ./...

test-integration: ## Unit + integration tests (`integration` build tag): testcontainers Postgres 17 + Redis, pgtestdb clone per test
	@mkdir -p $(TEST_RESULTS_DIR)
	$(GOTESTSUM) --format pkgname --junitfile $(TEST_RESULTS_DIR)/integration.xml -- -race -count=1 -tags integration -p 8 ./...

e2e: e2e-live ## Alias for e2e-live

e2e-live: ## Playwright live suite against the running dev stack (scripts/dev.sh up first)
	cd frontend && npx playwright test --config=playwright.live.config.ts

# ── Quality gates ────────────────────────────────────────────────────────────
lint: ## golangci-lint v2 (STANDARD preset + repo policy in .golangci.yml)
	$(GOLANGCI_LINT) run ./...

vet: ## go vet
	go vet ./...

fmt: ## Verify gofmt (does not modify files)
	@out="$$(gofmt -l $$(git ls-files '*.go' | grep -v '^internal/db/sqlc/' | grep -v '\.gen\.go$$'))"; \
	if [ -n "$$out" ]; then echo "gofmt needed on:"; echo "$$out"; exit 1; fi

frontend-check: ## Frontend type-check, lint, unit tests
	cd frontend && npm run type-check && npm run lint && npm test

frontend-build: ## Production frontend bundle
	cd frontend && npm run build

check: lint vet fmt build test ## Composite gate: lint vet fmt build test

# ── Generation ───────────────────────────────────────────────────────────────
sqlc: ## Regenerate sqlc output from internal/db/queries
	$(SQLC) generate

generate: sqlc ## Regenerate every checked-in artifact (sqlc, OpenAPI types/client, Go SDK)
	cp docs/openapi.yaml internal/handler/assets/openapi.yaml
	python3 scripts/categorical-contracts.py write
	node scripts/generate-openapi-types.mjs --write
	node scripts/generate-openapi-client.mjs --write
	$(OAPI_CODEGEN) -config oapi-codegen.yaml docs/openapi.yaml

# ── Migrations ───────────────────────────────────────────────────────────────
DATABASE_URL ?= postgres://astronomer:astronomer@localhost:15432/astronomer?sslmode=disable
migrate: ## Apply pending embedded goose migrations (DATABASE_URL); the server also does this at boot
	go run ./cmd/server migrate -database "$(DATABASE_URL)"

# ── Security gates (guide ch.06) ─────────────────────────────────────────────
security: gosec govulncheck secrets-scan ## gosec (fail on HIGH) + govulncheck + gitleaks full history

gosec: ## gosec SARIF, fails on HIGH severity findings
	@mkdir -p $(TEST_RESULTS_DIR)
	# tools/gosec/go.mod pins gosec v2.29.0 plus a golang.org/x/tools new enough to
	# read the Go 1.27 export-data format (the release pin alone cannot type-check).
	go run -modfile=tools/gosec/go.mod github.com/securego/gosec/v2/cmd/gosec -fmt sarif -out $(TEST_RESULTS_DIR)/gosec.sarif \
		-severity high -confidence medium -exclude-dir internal/db/sqlc -exclude-dir pkg/astroclient \
		-exclude=G115 ./...  # G115: written exception in docs/security-exceptions.md (Phase 1 sweep)

govulncheck: ## govulncheck SARIF on the shipping Go toolchain
	@mkdir -p $(TEST_RESULTS_DIR)
	go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) -format sarif -scan symbol ./... > $(TEST_RESULTS_DIR)/govulncheck.sarif
	go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) -scan symbol ./...

secrets-scan: ## gitleaks over the full history of the branch under review (HEAD)
	# Guide ch.06: the full commit history of the branch under review. Other refs in
	# this .git (old release lines, reference clones under refs/salvage/*) are not
	# part of the branch and are reviewed separately; see docs/decisions/index.md.
	gitleaks git --config=.gitleaks.toml --redact=100 --no-banner --log-opts=HEAD .

updates: ## Pending dependency upgrades (Go modules + npm)
	go list -u -m all 2>/dev/null | grep '\[' || true
	cd frontend && npm outdated || true

# ── Container images ─────────────────────────────────────────────────────────
docker-build: go-image-check $(addprefix docker-build-,$(IMAGES)) ## Build every production image

# Fails when go.mod's Go version and the go-image.lock pin disagree, or when the
# pinned digest is not the current index digest of golang:<version>-alpine.
go-image-check: ## Verify deploy/docker/go-image.lock matches go.mod and the registry
	@test "$(GO_IMAGE_VERSION)" = "$(GO_VERSION)" || { \
		echo "go-image.lock pins golang:$(GO_IMAGE_VERSION)-alpine but go.mod says go $(GO_VERSION)" >&2; exit 1; }
	@remote="$$(docker buildx imagetools inspect golang:$(GO_VERSION)-alpine --format '{{json .Manifest.Digest}}' | tr -d '"')"; \
	test "$$remote" = "$(GO_IMAGE_DIGEST)" || { \
		echo "golang:$(GO_VERSION)-alpine index digest is $$remote, go-image.lock pins $(GO_IMAGE_DIGEST)" >&2; exit 1; }; \
	echo "go-image.lock OK: golang:$(GO_VERSION)-alpine@$(GO_IMAGE_DIGEST)"

docker-build-frontend: ## Build the frontend image
	docker build --build-arg VERSION=$(VERSION) --build-arg GIT_COMMIT=$(GIT_COMMIT) --build-arg BUILD_DATE=$(BUILD_DATE) \
		-f frontend/Dockerfile -t $(IMG_REGISTRY)/astronomer-frontend:$(IMG_TAG) frontend

# Repository names match the chart and release workflow: server/worker/agent are
# astronomer-go-<name>; shell and dr are astronomer-<name>.
image_repo = $(if $(filter shell dr,$(1)),astronomer-$(1),astronomer-go-$(1))
docker-build-%: ## Build one image: docker-build-server|worker|agent|shell|dr
	docker build $(DOCKER_BUILD_ARGS) -f deploy/docker/Dockerfile.$* -t $(IMG_REGISTRY)/$(call image_repo,$*):$(IMG_TAG) .

# The base values profile requires release-stamped digests and real key material;
# lint with the dev profile, the qualified kube minor and throwaway lint-only keys.
HELM_LINT_ARGS = -f deploy/chart/values-dev.yaml --kube-version 1.35.0 \
	--set-string secrets.secretKey=helm-lint-only-jwt-signing-key-0000 \
	--set-string secrets.encryptionKey=AGE-SECRET-KEY-1YXRFCUA5M305R99XC5G7YRD9UFTGUE3PLFAC2Z2U8GSVLPZ9LQCQFHSDY9
helm-lint: ## helm lint + template dry-run of deploy/chart
	helm lint deploy/chart $(HELM_LINT_ARGS)
	helm template astronomer deploy/chart $(HELM_LINT_ARGS) > /dev/null

clean: ## Remove build and test output
	rm -rf bin tmp $(TEST_RESULTS_DIR) frontend/dist frontend/e2e-results
