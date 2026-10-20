# Astronomer contributor guide (derived from CLAUDE.md)

`CLAUDE.md` is the source of truth for principles, rules and architecture;
this file summarises what an agent or new contributor needs to act.

## Product boundary

- Astronomer adopts and operates existing Kubernetes clusters; it does not provision them.
- Flux on each managed cluster is the delivery engine; PostgreSQL (CNPG in production) is the authoritative state store (ADR-0004).
- Security-sensitive dependencies fail closed: authorization, mutation audit and scope checks are invariants.

## Repository map

- `cmd/`: `server`, `worker`, `agent`, `astro` composition roots (+ `migrator`, `keyrotate`, `dexconfigcheck` until Phase 1).
- `internal/`: Go domains and infrastructure; `internal/handler/` is the legacy flat HTTP package being split per domain.
- `internal/db/queries/` (sqlc source) and `internal/db/sqlc/` (generated, never hand-edited).
- `frontend/`: Vite + React 19 + TanStack Router/Query dashboard; `frontend/tests/e2e-live/` is the real-server Playwright suite.
- `deploy/chart/`: Helm chart; `deploy/docker/`: distroless images; `deploy/docker-compose.yml`: dev backing services.
- `docs/decisions/`: ADRs; `docs/runbooks/`: operator runbooks; `docs/threat-model.md`.

## Workflow

```bash
scripts/dev.sh up            # full local stack (compose + Air server/worker + Vite)
make check                   # lint vet fmt build test
make test-integration        # real Postgres/Redis, -tags integration
make security                # gosec + govulncheck + gitleaks
make e2e-live                # Playwright against the running stack
```

- Change SQL in `internal/db/queries/*.sql`, then `make sqlc`. Change `docs/openapi.yaml`, then `make generate`.
- Never patch generated output; change the source and regenerate.
- Record any deviation from the Technology Selection Guide as an ADR in `docs/decisions/`.
- Keep commits conventional and self-contained; report any verification that could not run.
