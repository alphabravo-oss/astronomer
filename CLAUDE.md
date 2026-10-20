# Astronomer — engineering principles and conventions

Astronomer is a self-hosted Kubernetes management plane that adopts clusters
other tooling provisioned, governs them with policy and delivers through Flux.
This file is the project-root source of truth for engineering discipline
(AlphaBravo Technology Selection Guide, ch.08). `AGENTS.md` is derived from it.

## Principles

1. **Correctness over speed.** A merged change that produces wrong output is a
   regression even if every gate passed. Pre-production is the cheapest window
   to restructure for correctness.
2. **Modular by default.** One package owns one domain. Handler → service →
   store layering stays separate; database queries never appear in handler
   files and HTTP/RPC types never appear in query files.
3. **Proto enums as the single source of truth.** Wire enums are defined once
   in `.proto` (Phase 2, ADR-0007); until then the OpenAPI enum catalog is the
   source and conversion happens in one boundary file per domain.
4. **Air-gap first.** Every runtime code path works without an out-of-cluster
   network call; public registries and update checks are opt-in.
5. **Real backends in tests.** Integration tests use real PostgreSQL and Redis
   (testcontainers), never a mock of a transport boundary. End-to-end tests
   drive the production binary through the real login (`tests/e2e-live`).
6. **Fix root causes, not symptoms.** Workarounds carry `// WORKAROUND: <issue>`
   and a tracking issue; flaky tests are diagnosed, never retried.
7. **No backward-compatibility burden in pre-production.** Rename, restructure
   and delete freely; migrations may be hard cutovers with Up and Down.

## Rules

- RFC 2119 keywords are binding. A `SHOULD` needs a stated reason to skip.
- Uber and Google Go style guides are the references; `golangci-lint` v2
  STANDARD plus `.golangci.yml` is the contract. Never disable a linter on the
  CLI.
- Configuration is read only in `internal/config` (and the agent/CLI loaders
  allow-listed by `internal/config/env_access_contract_test.go`).
- Errors returned to clients go through `handler.RespondRequestError` /
  `apierror` codes; internal messages are never leaked.
- Secrets never appear at INFO+ in logs; redaction lives in
  `internal/observability`.
- Generated code (`internal/db/sqlc`, `pkg/astroclient`, OpenAPI TypeScript
  types) is regenerated with `make generate`, never edited.
- Decisions that deviate from the guide are ADRs in `docs/decisions/`.

## Dev loop

| Command | What it does |
| --- | --- |
| `make dev` / `scripts/dev.sh up` | Postgres 17 + Redis + Tempo/Loki/Alloy/Prometheus/Grafana (compose), Air-reloaded `server` and `worker`, Vite dashboard on :3000 |
| `scripts/dev.sh status|logs|down` | Inspect or stop the stack; logs under `tmp/logs/` |
| `make migrate` / `make migrate-down` | Apply / roll back migrations (`DATABASE_URL`) |
| `make check` | `lint vet fmt build test` — the PR gate |
| `make test` / `make test-integration` | Unit (no Docker) / integration (`-tags integration`, real backends) |
| `make security` | gosec (fail on HIGH) + govulncheck + gitleaks over full history |
| `make e2e-live` | Playwright against the running dev stack with a real login |
| `make docker-build` | All images; `GO_VERSION` derived from `go.mod` |
| `make fips-build` | `GOEXPERIMENT=boringcrypto -tags fips` binaries under `bin/fips/` |
| `make updates` | `go list -u -m all` + `npm outdated` |

Ports (see `.env.dev`): API `:8000`, server metrics `:9090`, worker metrics
`:9091`, Postgres `:15432`, Redis `:16379`, Vite `:3000`, Grafana `:3001`,
Prometheus `:9092`, Tempo OTLP gRPC `:4317`.

## Architecture

- `cmd/{server,worker,agent,astro}` are composition roots and nothing else
  (`migrator`, `keyrotate`, `dexconfigcheck` remain until Phase 1 replaces them).
- `internal/<domain>` owns a domain; `internal/handler` is the legacy flat
  handler package being split domain by domain (ADR-0007).
- `internal/db`: pgx v5 pool with the composite otelpgx + metrics tracer,
  sqlc queries under `internal/db/queries`, migrations under
  `internal/db/migrations`.
- `internal/server`: chi router; middleware order is recovery → request-id +
  tracing → logging → metrics → body limits → authn → audit → authz → handler.
- `internal/observability`: slog JSON handler with redaction, OTLP gRPC traces,
  Prometheus metrics (`service_build_info`, RED counters incl.
  `astronomer_request_errors_total{route,reason}`).
- `frontend/`: React 19, Vite 8, TypeScript 7, Tailwind v4 (CSS-first,
  ADR-0006), Radix/shadcn primitives, TanStack Query + TanStack Router
  (ADR-0001).
- `deploy/chart`: the Helm chart (CNPG-managed PostgreSQL, Flux delivery,
  ADR-0004). `deploy/docker`: multi-stage distroless images.

## Conventions

- Conventional commits; every commit builds and passes `make check`.
- Tests are table-driven, call `t.Parallel()` first, use `t.Cleanup`.
- Bash stays under ~200 lines with `set -Eeuo pipefail`; longer tooling is Go.
- Version pins: Go from `go.mod`, Node from `.node-version`, images by digest in
  production Dockerfiles and the chart.
