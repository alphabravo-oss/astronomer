.PHONY: help build test lint fmt vet run sqlc sqlc-generate docker-build migrate-up migrate-down migrate-create clean dev dev-down dev-clean

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

# ── Targets ──────────────────────────────────────────────────────────────────

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'

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

docker-build: ## Build server Docker image
	docker build --build-arg VERSION=$(VERSION) --build-arg GIT_COMMIT=$(shell git rev-parse --short HEAD) --build-arg BUILD_DATE=$(shell date -u +%Y-%m-%dT%H:%M:%SZ) -f deploy/docker/Dockerfile.server -t astronomer-go-server:$(VERSION) .

clean: ## Remove build artifacts
	rm -rf bin/

dev: ## Start dev environment (docker compose)
	docker compose -f deploy/docker-compose.yml up -d

dev-down: ## Stop dev environment
	docker compose -f deploy/docker-compose.yml down

dev-clean: ## Stop dev environment and remove volumes
	docker compose -f deploy/docker-compose.yml down -v
