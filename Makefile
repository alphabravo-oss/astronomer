.PHONY: help build test lint fmt vet run sqlc sqlc-generate \
        docker-build docker-build-server docker-build-agent docker-build-worker docker-build-migrate docker-build-all \
        migrate-up migrate-down migrate-create clean dev dev-down dev-clean \
        k3d-load k3d-bootstrap helm-install helm-uninstall k8s-apply k8s-delete \
        validate-live-b6 validate-live-argocd validate-live-argocd-register-appset validate-live-dex validate-live-dex-oidc validate-live-generic-oidc validate-live-velero validate-live-cis validate-live-oci validate-live-projects

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

# Image naming — override IMG_TAG=... to push semantic versions.
IMG_TAG     ?= $(VERSION)
IMG_SERVER   = astronomer-go-server:$(IMG_TAG)
IMG_AGENT    = astronomer-go-agent:$(IMG_TAG)
IMG_WORKER   = astronomer-go-worker:$(IMG_TAG)
IMG_MIGRATE  = astronomer-go-migrate:$(IMG_TAG)

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

test: ## Run tests with race detector
	go test -race -count=1 ./...

lint: ## Run golangci-lint
	golangci-lint run ./...

fmt: ## Format Go source files
	go fmt ./...

vet: ## Vet Go source files
	go vet ./...

run: ## Run the server locally
	go run -ldflags '$(LDFLAGS)' ./cmd/server

sqlc-generate: ## Generate sqlc code
	sqlc generate

sqlc: sqlc-generate ## Alias for sqlc-generate

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

docker-build-all: docker-build-server docker-build-agent docker-build-worker docker-build-migrate ## Build all images

# Backward-compat alias: `make docker-build` still builds the server image.
docker-build: docker-build-server ## (alias) build server image

# ── k3d helpers ──────────────────────────────────────────────────────────────

k3d-load: ## Import a Docker image into the k3d cluster (IMG=<image:tag> CLUSTER=<name>)
	@if [ -z "$(IMG)" ]; then echo "Usage: make k3d-load IMG=astronomer-go-server:dev [CLUSTER=$(CLUSTER)]"; exit 1; fi
	k3d image import $(IMG) -c $(CLUSTER)

k3d-import-all: docker-build-all ## Build & import all images into k3d
	k3d image import $(IMG_SERVER) $(IMG_AGENT) $(IMG_WORKER) $(IMG_MIGRATE) -c $(CLUSTER)

k3d-bootstrap: ## Bootstrap a local k3d cluster + apply manifests (CLUSTER=$(CLUSTER))
	CLUSTER=$(CLUSTER) IMG_TAG=$(IMG_TAG) ./scripts/k3d-bootstrap.sh

validate-live-b6: ## Validate live cluster.k8s_changed SSE flow (set AUTH_TOKEN or ASTRO_USERNAME/ASTRO_PASSWORD)
	./scripts/validate-live-b6.sh

validate-live-argocd: ## Validate live ArgoCD create/patch/delete flow (set AUTH_TOKEN or ASTRO_USERNAME/ASTRO_PASSWORD)
	./scripts/validate-live-argocd.sh

validate-live-argocd-register-appset: ## Validate live ArgoCD register + ApplicationSet fan-out flow (set AUTH_TOKEN or ASTRO_USERNAME/ASTRO_PASSWORD)
	./scripts/validate-live-argocd-register-appset.sh

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
		--set image.migrate.tag=$(IMG_TAG)

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
