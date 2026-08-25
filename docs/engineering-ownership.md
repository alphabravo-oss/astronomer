# Engineering ownership and review boundaries

Status: normative engineering policy
Last reviewed: 2026-08-23

Ownership below names accountable roles rather than individual people. A team
may map these roles to GitHub teams in its deployment, but the required review
discipline is portable and testable in every fork.

| Boundary | Accountable owner | Mandatory additional review | Required evidence |
| --- | --- | --- | --- |
| Authentication, authorization, session/token handling, proxying, secret access | Security/platform | Domain owner and security reviewer | Cross-tenant negative tests, threat-model update, audit/redaction proof |
| PostgreSQL schema and sqlc queries | Data/platform | Domain owner and upgrade reviewer | Forward migration, rollback policy, PG16/17 fresh+upgrade test, representative query plan |
| Durable operations, task registry, outbox, schedules, worker runtime | Reliability/platform | Domain owner | Registered ownership, idempotency/replay/restart test, queue-age/DLQ observability |
| Delivery sources, placement, rollouts, assignment/status protocol, Flux materialization | Delivery/platform | Security and reliability reviewers | Compatibility matrix, generation/CAS tests, disconnected/reconnect and rollback evidence |
| Agent identity, capability negotiation, tunnel ownership | Connectivity/platform | Security and reliability reviewers | N/N-1 compatibility, duplicate-session/failover tests, credential rotation/revocation proof |
| Public REST API and generated SDK/client | API/platform | Owning domain and frontend reviewer | OpenAPI coverage/quality, RBAC metadata, typed errors, generated drift check |
| Management CRDs and controllers | Kubernetes platform | API and data reviewers | Schema/default validation, status/finalizer/ownership tests, CRD docs |
| Frontend API modules and domain hooks | Owning frontend domain | API reviewer | Generated-operation use, loading/empty/error/denied state tests |
| Design system, accessibility, navigation, global state | Frontend platform | UX/accessibility reviewer | Component tests, zero-warning lint, axe and visual evidence |
| Helm, release, compatibility, images, air-gap | Release/platform | Security and reliability reviewers | Lint/render contracts, immutable manifest, SBOM/signature, install/upgrade evidence |
| Backup/restore/DR and key custody | Reliability/platform | Security and data reviewers | Restore drill, recorded RPO/RTO, runbook update |

## Dependency direction

- `cmd/*` and `internal/server` are composition roots. They may wire domain
  services and lifecycles but must not become the only place where domain
  policy can be tested.
- HTTP route modules depend on domain handlers and explicit middleware policy.
  Handlers do not import the server composition package.
- Workers use the typed task registry/runtime. Domain code cannot enqueue an
  unregistered string task or rely on Redis as the sole record of intent.
- Database/sqlc code cannot import handlers, servers, or frontend concepts.
- Frontend routes render screens and bind navigation. Network calls live in
  `src/lib/api` domain modules and domain hooks; generated wire types come from
  the OpenAPI output.
- Shared UI primitives contain no product-domain policy. Domain-specific
  status mapping stays with the domain.

Package-boundary and direct-call checks in `scripts/code-health-inventory.mjs`,
the task inventory, route inventory, and Go import cycle compiler enforce the
machine-testable portion of these rules.

## Complexity policy

`node scripts/check-complexity-budget.mjs` applies budgets to changed code:

- a new production Go or TypeScript module must be cohesive and remain below
  the configured file budget;
- a new oversized function or production module is rejected, while known
  legacy hotspots have explicit repository-wide no-growth ceilings;
- known legacy hotspots have explicit no-growth ceilings and lower extraction
  targets;
- generated files, vendored code, tests, and snapshots are excluded.

The current extraction targets are:

| Hotspot | No-growth ceiling | Extraction target | Intended modules |
| --- | ---: | ---: | --- |
| `internal/server/server.go` | 1,200 | 1,200 | transport lifecycle plus typed production composition phases |
| `internal/handler/resources.go` | 1,000 | 1,000 | request/proxy behavior plus resource presentation adapters |
| `frontend/src/lib/api.ts` | 3,100 | 800 | generated operations plus domain API modules |
| `frontend/src/lib/hooks.ts` | 2,600 | 800 | domain-owned query/mutation hooks |
| `frontend/src/types/index.ts` | 2,300 | 800 | generated wire types plus domain view models |
| generic resource route | 4,650 | 1,200 | schema adapters, shared resource primitives, resource-family screens |

New functionality must land in the intended domain module. Moving code without
characterization tests is not decomposition; behavior-preserving extraction
and a lower enforced ceiling must happen together.

## Review checklist

Every material change answers these questions in its pull request:

1. Which durable owner and public contract change?
2. Which tenant/object authorization and negative case apply?
3. What is the idempotency, replay, timeout, concurrency, and failure model?
4. Can a secret appear in storage, payloads, logs, audit, metrics, traces,
   support bundles, or errors?
5. What happens across an N/N-1 rolling deployment and restore?
6. Which unit, contract, live, accessibility, visual, or scale evidence proves
   the result?
