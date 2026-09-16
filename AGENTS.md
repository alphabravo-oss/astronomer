# Astronomer contributor guide

## Product boundary

- Astronomer adopts and operates existing Kubernetes clusters; it does not provision them.
- Flux is the downstream reconciliation engine. Do not introduce Fleet or legacy delivery-runtime paths.
- Prefer one canonical implementation over compatibility aliases, duplicate stacks, or silent fallbacks. Public API compatibility must be an explicit documented contract.
- Security-sensitive dependencies fail closed. Authorization, mutation audit, and tenant scoping are invariants, not optional middleware.

## Repository map

- `cmd/`: server, worker, agent, and `astro` CLI entry points.
- `internal/`: Go application domains and infrastructure.
- `internal/handler/`: HTTP handlers; new domain logic should live in a domain package rather than expanding this package.
- `internal/db/queries/`: sqlc source queries.
- `internal/db/sqlc/`: generated sqlc output; do not edit by hand.
- `frontend/`: Vite, React 19, TanStack Router/Query operator console.
- `deploy/chart/`: Helm chart and production defaults.
- `docs/`: current operator and architecture documentation. Historical material belongs under `docs/archive/`.

## Local workflow

```bash
make dev
cd frontend && npm ci && npm run dev
```

`make dev` starts the backend stack and exposes the API on port 8001. Vite runs on port 3000. Use `make dev-full` for the containerized frontend.

Frontend formatting is defined by `frontend/.prettierrc.json`. Run Prettier on
the files you edit, then stage them. `make install-hooks` opts into the checked-in
pre-commit hook; `cd frontend && npm run format:check` checks the exact staged
blobs (including partially staged files), excludes generated artifacts, and never
rewrites either the index or working tree. This incremental gate does not claim
that untouched historical files are already normalized.

Before handing off a change, run the narrow tests for touched packages, then the applicable gates:

```bash
go test ./path/to/touched/package
go vet ./internal/... ./cmd/...
cd frontend && npm run type-check && npm run lint && npm test
make verify-enterprise VERIFY_SCOPE=backend   # or frontend / helm / all
```

## Generated artifacts

- Change SQL in `internal/db/queries/*.sql`, then run `sqlc generate`; verify with `make sqlc-check`.
- Change `docs/openapi.yaml`, then run `make openapi-generate`; the embedded spec, frontend operations/types, route inventory, and Go SDK are generated together.
- Charlie bridge generated contracts are refreshed through the scripts referenced by `make charlie-contract-check`.
- Change the `astro` Cobra command tree, then run `make cli-docs`; verify with `make cli-docs-check`.
- Change `internal/config.Config` or its defaults, then run `make config-docs` to refresh both `docs/configuration.md` and `.env.example`; verify with `make config-docs-check`.
- Never patch generated output without changing its source and running the generator.

## Non-obvious gates

- `scripts/check-frontend-raw-transport.mjs`: prevents new ad-hoc frontend HTTP transports.
- `scripts/check-docs.mjs`: validates links, current/historical classification, and retired terminology.
- `scripts/check-complexity-budget.mjs`: enforces repository-wide file and named-function no-growth ceilings. After deliberately reducing an oversized unit, refresh `docs/architecture/complexity-baseline.json` with `node scripts/check-complexity-budget.mjs --write-baseline` and review the complete baseline diff.
- `scripts/test-flake-report.mjs --validate`: enforces the zero-unowned-retry/quarantine policy.
- `scripts/check-dependency-boundaries.mjs`: enforces package-direction rules.
- `scripts/check-migrations.sh`: rejects unsafe or blocking migration patterns.
- `scripts/generate-cli-docs.mjs`: keeps the generated CLI reference synchronized with the shipped command tree.
- `scripts/generate-config-docs.mjs`: keeps the operator configuration reference synchronized with the canonical server config.
- `internal/config/env_access_contract_test.go`: forbids hidden direct environment reads in runtime packages; add typed `Config` fields and inject them through composition instead.

Preserve unrelated working-tree changes. Use focused, additive patches and report any verification that could not run.
