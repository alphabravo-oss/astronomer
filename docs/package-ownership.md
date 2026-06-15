# Package Ownership

Date: 2026-06-14
Status: Active engineering policy

This document defines package boundaries for the management plane. New code
should land in the owning package below unless a design review documents why a
cross-boundary dependency is required.

## Backend Boundaries

| Package | Owns | Must not own |
| --- | --- | --- |
| `internal/server` | HTTP server construction, route registration, dependency wiring, top-level middleware ordering, readiness/liveness, startup validation, CRD/controller wiring. | Domain business rules, provider-specific API clients, UI DTO formatting. |
| `internal/server/middleware` | Request authentication context, RBAC enforcement middleware, CSRF, audit/read-audit middleware, request IDs, request logging, HTTP metrics. | Handler-specific authorization decisions or domain mutations. |
| `internal/handler` | Versioned HTTP resource handlers, request/response DTOs, per-domain validation, audit action emission, and thin orchestration into worker/db/client packages. | Long-running reconciliation loops, direct background scheduling, shared Kubernetes primitives that multiple handlers need. |
| `internal/auth` | Password hashing, JWT/API token lifecycle, auth crypto, OIDC/OAuth helpers, token scope definitions, bootstrap admin rules. | HTTP route registration, handler response formatting, RBAC role evaluation. |
| `internal/rbac` | Role/resource/action model, policy evaluation, built-in roles, binding semantics, permission explanation primitives. | Route wiring, Kubernetes API calls, UI-specific labels. |
| `internal/agent` | In-cluster agent configuration, tunnel client behavior, local Kubernetes/service proxy execution, agent health, least-privilege runtime helpers. | Management-plane DB writes, browser/user authorization decisions. |
| `internal/tunnel` and `internal/tunnel2` | Server-side agent connection lifecycle, stream proxying, internal Kubernetes/Helm request relays, tunnel metrics/audit hooks. | Domain state transitions that belong in handlers/workers. |
| `internal/gitops` | GitOps source parsing, desired-state translation, repository/provider helpers, GitOps target reconciliation primitives. | Built-in Argo CD HTTP client behavior or cluster inventory CRUD. |
| `internal/handler/argocd` | Argo CD API client surface and Argo-specific request/response helpers. | Generic GitOps source parsing or Kubernetes proxy authorization. |
| `internal/monitoring` | Monitoring provider abstractions, query rendering, metrics collection helpers, health/status translation. | HTTP DTOs or alert rule persistence. |
| `internal/db` and `internal/db/sqlc` | Migrations, query definitions, generated query code, hand-written sqlc extensions, migration safety tests. | Business rules that can be expressed in handlers/workers. |
| `internal/worker` | Worker process setup, asynq instrumentation, scheduler wiring, queue metrics, common enqueue conventions. | Task-specific domain logic. |
| `internal/worker/tasks` | Long-running operations, drift reconciliation, retryable convergence, async task payloads, task-level audit side effects. | HTTP request parsing or direct browser response behavior. |
| `internal/redaction` | Shared best-effort redaction for diagnostics, support bundles, logs, and exported JSON-like payloads. | UI preserve-value sentinels or provider-specific secret merge rules. |

## Review Rules

- A route change must identify its handler owner, auth/RBAC posture, audit
  posture, and generated route inventory impact.
- A background operation must have an owning task type, idempotency story,
  retry behavior, structured log identifiers, metrics, and tests.
- A reusable helper moves out of a domain package only after at least two
  domains need it and the extracted API is smaller than the duplicated code.
- A package may depend inward on lower-level primitives, but not upward on
  route/server setup. For example, `internal/worker/tasks` may use
  `internal/db/sqlc`, but `internal/db/sqlc` must not import worker or handler
  packages.
- Secret-handling code must follow `docs/secret-handling-policy.md`; broad
  diagnostic redaction and UI preserve-value sentinels are intentionally
  different surfaces.

## Definition of Done for New Packages

- Package purpose is clear from its name and top-level comments.
- Imports do not create cycles or route-layer dependencies in lower packages.
- Tests live with the package that owns the behavior.
- Public functions are small, named for the domain behavior they expose, and
  do not leak unrelated package internals.
- Documentation or the master plan is updated when a new durable package
  boundary is introduced.
