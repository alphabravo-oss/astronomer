.PHONY: help build test lint fmt vet run verify sqlc sqlc-generate sqlc-check sdk error-codes error-codes-check \
        docker-build docker-build-server docker-build-agent docker-build-worker docker-build-migrate docker-build-frontend docker-build-all \
        migrate-up migrate-down migrate-create clean dev dev-down dev-clean \
        k3d-load k3d-bootstrap helm-install helm-uninstall k8s-apply k8s-delete \
        validate-live-b6 validate-live-argocd validate-live-argocd-register-appset validate-live-argocd-auto-adoption validate-live-dex validate-live-dex-oidc validate-live-generic-oidc validate-live-velero validate-live-cis validate-live-oci validate-live-projects

# ── Variables ────────────────────────────────────────────────────────────────
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
GIT_COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE  ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
MODULE       = github.com/alphabravocompany/astronomer-go
LDFLAGS      = -s -w \
               -X $(MODULE)/pkg/version.Version=$(VERSION) \
               -X $(MODULE)/pkg/version.GitCommit=$(GIT_COMMIT) \
               -X $(MODULE)/pkg/version.BuildDate=$(BUILD_DATE)

DATABASE_URL ?= postgres://astronomer:astronomer@localhost:5433/astronomer?sslmode=disable
SQLC_VERSION ?= v1.31.1
SQLC         ?= go run github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)

# Pinned oapi-codegen (Go SDK generator) — mirrors the sqlc pinned-tool pattern.
OAPI_CODEGEN_VERSION ?= v2.5.0
OAPI_CODEGEN         ?= go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$(OAPI_CODEGEN_VERSION)

# Image naming — override IMG_TAG=... to push semantic versions.
# IMG_REGISTRY carries the first-party GHCR prefix so locally-built images match
# the chart's default image refs (image.<x>.registry = ghcr.io/alphabravo-oss),
# so `make k3d-import-all` + a default `helm install` line up with no overrides.
IMG_TAG      ?= $(VERSION)
IMG_REGISTRY ?= ghcr.io/alphabravo-oss
IMG_SERVER   = $(IMG_REGISTRY)/astronomer-go-server:$(IMG_TAG)
IMG_AGENT    = $(IMG_REGISTRY)/astronomer-go-agent:$(IMG_TAG)
IMG_WORKER   = $(IMG_REGISTRY)/astronomer-go-worker:$(IMG_TAG)
IMG_MIGRATE  = $(IMG_REGISTRY)/astronomer-go-migrate:$(IMG_TAG)
# Frontend image name stays `astronomer-frontend` to match the Helm chart's default
# (deploy/chart/values.yaml -> frontend.image.repository). Build context is
# astronomer-go's own frontend/ directory.
IMG_FRONTEND = $(IMG_REGISTRY)/astronomer-frontend:$(IMG_TAG)
# astronomer-shell is the in-browser kubectl shell pod image.
# Owned end-to-end (alpine + kubectl from dl.k8s.io) so we don't depend
# on a third-party registry whose tag schedule we can't control.
IMG_SHELL    = $(IMG_REGISTRY)/astronomer-shell:$(IMG_TAG)

# k3d cluster name (override on the command line: `make k3d-bootstrap CLUSTER=foo`).
CLUSTER     ?= astronomer-mgmt

DOCKER_BUILD_ARGS = \
    --build-arg VERSION=$(VERSION) \
    --build-arg GIT_COMMIT=$(GIT_COMMIT) \
    --build-arg BUILD_DATE=$(BUILD_DATE)

# ── Targets ──────────────────────────────────────────────────────────────────

help: ## Show this help
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}'

build: ## Build all binaries to bin/
	@mkdir -p bin
	go build -ldflags '$(LDFLAGS)' -o bin/server   ./cmd/server
	go build -ldflags '$(LDFLAGS)' -o bin/worker   ./cmd/worker
	go build -ldflags '$(LDFLAGS)' -o bin/agent    ./cmd/agent
	go build -ldflags '$(LDFLAGS)' -o bin/astro    ./cmd/astro

test: ## Run tests with race detector
	go test -race -count=1 ./...

lint: ## Run golangci-lint
	golangci-lint run ./...

check-migrations: ## Lint *.up.sql migrations for unsafe ADD COLUMN NOT NULL patterns (T30)
	./scripts/check-migrations.sh

images.txt: ## Regenerate deploy/chart/images.txt — list of every image the chart pulls (T23)
	./scripts/extract-images.sh > deploy/chart/images.txt
	@echo ""
	@echo "deploy/chart/images.txt updated. Commit alongside any chart values change that adds/removes an image."

fmt: ## Format Go source files
	go fmt ./...

vet: ## Vet Go source files
	go vet ./...

openapi-embed: ## Sync the served spec asset from the source-of-truth docs/openapi.yaml
	cp docs/openapi.yaml internal/handler/assets/openapi.yaml

verify: ## Run the api-contract CI gate locally (mirrors .github/workflows/api-contract.yaml)
	go build ./...
	go vet ./...
	go test ./internal/handler/ ./internal/server/ ./internal/auth/ ./internal/server/middleware/ -count=1
	node scripts/openapi-coverage.mjs --check
	node scripts/generate-openapi-types.mjs --check
	@diff -q docs/openapi.yaml internal/handler/assets/openapi.yaml >/dev/null || { echo "FAIL: internal/handler/assets/openapi.yaml is stale — run 'make openapi-embed'"; exit 1; }
	go test ./internal/server/ -run RouteTable -count=1
	go test ./internal/handler/ -run TestApierrorCatalogCoverage -count=1

run: ## Run the server locally
	go run -ldflags '$(LDFLAGS)' ./cmd/server

error-codes: ## Regenerate docs/error-codes.md from internal/handler/apierror/codes.go
	node scripts/error-code-docs.mjs --write

error-codes-check: ## Fail if docs/error-codes.md is stale vs the apierror catalog
	node scripts/error-code-docs.mjs --check

sqlc-generate: ## Generate sqlc code
	$(SQLC) generate

sqlc: sqlc-generate ## Alias for sqlc-generate

sqlc-check: ## Regenerate sqlc and fail if generated files are stale
	SQLC_VERSION=$(SQLC_VERSION) ./scripts/check-sqlc-generated.sh

sdk: ## Generate the typed Go SDK (pkg/astroclient) from docs/openapi.yaml via oapi-codegen
	$(OAPI_CODEGEN) -config oapi-codegen.yaml docs/openapi.yaml

migrate-up: ## Run all migrations up
	migrate -database "$(DATABASE_URL)" -path internal/db/migrations up

migrate-down: ## Roll back one migration
	migrate -database "$(DATABASE_URL)" -path internal/db/migrations down 1

migrate-create: ## Create a new migration (NAME=<name>)
	@if [ -z "$(NAME)" ]; then echo "Usage: make migrate-create NAME=<name>"; exit 1; fi
	migrate create -ext sql -dir internal/db/migrations -seq $(NAME)

# ── Docker images ────────────────────────────────────────────────────────────

docker-build-server: ## Build server image
	docker build $(DOCKER_BUILD_ARGS) -f deploy/docker/Dockerfile.server -t $(IMG_SERVER) .

docker-build-agent: ## Build agent image
	docker build $(DOCKER_BUILD_ARGS) -f deploy/docker/Dockerfile.agent  -t $(IMG_AGENT)  .

docker-build-worker: ## Build worker image
	docker build $(DOCKER_BUILD_ARGS) -f deploy/docker/Dockerfile.worker -t $(IMG_WORKER) .

docker-build-migrate: ## Build migrate (golang-migrate + SQL files) image
	docker build -f deploy/docker/Dockerfile.migrate -t $(IMG_MIGRATE) .

docker-build-frontend: ## Build frontend (Next.js dashboard) image from frontend/
	docker build -f frontend/Dockerfile -t $(IMG_FRONTEND) frontend

docker-build-shell: ## Build astronomer-shell (in-cluster kubectl shell pod) image
	docker build -f deploy/docker/Dockerfile.shell -t $(IMG_SHELL) deploy/docker

docker-build-all: docker-build-server docker-build-agent docker-build-worker docker-build-migrate docker-build-frontend docker-build-shell ## Build all images

# Backward-compat alias: `make docker-build` still builds the server image.
docker-build: docker-build-server ## (alias) build server image

# ── k3d helpers ──────────────────────────────────────────────────────────────

k3d-load: ## Import a Docker image into the k3d cluster (IMG=<image:tag> CLUSTER=<name>)
	@if [ -z "$(IMG)" ]; then echo "Usage: make k3d-load IMG=astronomer-go-server:dev [CLUSTER=$(CLUSTER)]"; exit 1; fi
	k3d image import $(IMG) -c $(CLUSTER)

k3d-import-all: docker-build-all ## Build & import all images into k3d
	k3d image import $(IMG_SERVER) $(IMG_AGENT) $(IMG_WORKER) $(IMG_MIGRATE) $(IMG_FRONTEND) -c $(CLUSTER)

k3d-bootstrap: ## Bootstrap a local k3d cluster + apply manifests (CLUSTER=$(CLUSTER))
	CLUSTER=$(CLUSTER) IMG_TAG=$(IMG_TAG) ./scripts/k3d-bootstrap.sh

validate-live-b6: ## Validate live cluster.k8s_changed SSE flow (set AUTH_TOKEN or ASTRO_USERNAME/ASTRO_PASSWORD)
	./scripts/validate-live-b6.sh

validate-live-argocd: ## Validate live ArgoCD create/patch/delete flow (set AUTH_TOKEN or ASTRO_USERNAME/ASTRO_PASSWORD)
	./scripts/validate-live-argocd.sh

validate-live-argocd-register-appset: ## Validate live ArgoCD register + ApplicationSet fan-out flow (set AUTH_TOKEN or ASTRO_USERNAME/ASTRO_PASSWORD)
	./scripts/validate-live-argocd-register-appset.sh

validate-live-argocd-auto-adoption: ## Validate live ArgoCD auto-adoption + baseline fan-out (set AUTH_TOKEN or ASTRO_USERNAME/ASTRO_PASSWORD)
	./scripts/validate-live-argocd-auto-adoption.sh

validate-live-dex: ## Validate live Dex connector apply + redirect flow (set AUTH_TOKEN or ASTRO_USERNAME/ASTRO_PASSWORD)
	./scripts/validate-live-dex.sh

validate-live-dex-oidc: ## Validate live Dex external OIDC callback flow (requires docker; set AUTH_TOKEN or ASTRO_USERNAME/ASTRO_PASSWORD)
	./scripts/validate-live-dex-oidc.sh

validate-live-generic-oidc: ## Validate live direct generic OIDC callback flow (requires docker; set AUTH_TOKEN or ASTRO_USERNAME/ASTRO_PASSWORD)
	./scripts/validate-live-generic-oidc.sh

validate-live-velero: ## Validate live Velero backup + restore flow (set AUTH_TOKEN or ASTRO_USERNAME/ASTRO_PASSWORD)
	./scripts/validate-live-velero.sh

validate-live-cis: ## Validate live CIS scan + report ingestion flow (set AUTH_TOKEN or ASTRO_USERNAME/ASTRO_PASSWORD)
	./scripts/validate-live-cis.sh

validate-live-oci: ## Validate live OCI catalog create/sync/install flow (set AUTH_TOKEN or ASTRO_USERNAME/ASTRO_PASSWORD)
	./scripts/validate-live-oci.sh

smoke-fresh-cluster: ## Walk the full wizard against a fresh k3d cluster (requires ASTRO_PASSWORD)
	./scripts/smoke-fresh-cluster.sh

validate-live-projects: ## Validate live project enforcement + audit flow (set AUTH_TOKEN or ASTRO_USERNAME/ASTRO_PASSWORD)
	./scripts/validate-live-projects.sh

# ── Helm / kubectl ───────────────────────────────────────────────────────────

helm-install: ## Install/upgrade the Helm chart (CLUSTER=$(CLUSTER) NAMESPACE=astronomer)
	helm upgrade --install astronomer deploy/chart \
		--namespace $${NAMESPACE:-astronomer} --create-namespace \
		-f deploy/chart/values.yaml \
		--set image.server.tag=$(IMG_TAG) \
		--set image.worker.tag=$(IMG_TAG) \
		--set image.agent.tag=$(IMG_TAG) \
		--set image.migrate.tag=$(IMG_TAG) \
		--set frontend.image.tag=$(IMG_TAG)

helm-uninstall: ## Uninstall the Helm release
	helm uninstall astronomer --namespace $${NAMESPACE:-astronomer}

k8s-apply: ## Apply the raw manifests in deploy/k8s/
	kubectl apply -f deploy/k8s/

k8s-delete: ## Delete the raw manifests in deploy/k8s/
	kubectl delete -f deploy/k8s/ --ignore-not-found

# ── Dev (docker compose) ─────────────────────────────────────────────────────

clean: ## Remove build artifacts
	rm -rf bin/

dev: ## Start dev environment (docker compose)
	docker compose -f deploy/docker-compose.yml up -d

dev-down: ## Stop dev environment
	docker compose -f deploy/docker-compose.yml down

dev-clean: ## Stop dev environment and remove volumes
	docker compose -f deploy/docker-compose.yml down -v

# ── Load test ───────────────────────────────────────────────────────────────
# Synthetic-agent + HTTP workload driver. Tune via LOADTEST_* env vars; output
# is a markdown report containing a greppable `VERDICT: pass|fail` line plus
# per-scenario p50/p95/p99 and a server resource snapshot. See
# scripts/loadtest/README.md and docs/scale-baseline.md.
LOADTEST_SERVER   ?= http://localhost:8080
LOADTEST_CLUSTERS ?= 50
LOADTEST_RPS      ?= 100
LOADTEST_DURATION ?= 5m
LOADTEST_OUT      ?= loadtest-report-$(shell date +%Y%m%d).md
LOADTEST_TOKEN    ?=

load-test: ## Run the load-test harness (see scripts/loadtest/README.md)
	@LOADTEST_SERVER='$(LOADTEST_SERVER)' \
	 LOADTEST_CLUSTERS='$(LOADTEST_CLUSTERS)' \
	 LOADTEST_RPS='$(LOADTEST_RPS)' \
	 LOADTEST_DURATION='$(LOADTEST_DURATION)' \
	 LOADTEST_OUT='$(LOADTEST_OUT)' \
	 LOADTEST_TOKEN='$(LOADTEST_TOKEN)' \
	 go run ./scripts/loadtest
