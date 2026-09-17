# ADR: Frontend application stack and API boundary

- **Status:** Accepted
- **Date:** 2026-09-17
- **Target:** Astronomer v1.1.0
- **Owners:** Frontend platform, API, security, and SRE
- **Review trigger:** A new non-HTTP API surface, a second frontend runtime, or a
  supported Node-major change
- **Machine contracts:** [`docs/openapi.yaml`](../../openapi.yaml),
  [`frontend/package.json`](../../../frontend/package.json), and
  [frontend state ownership](../frontend-state-ownership.md)

## Context

The console needs one durable boundary between a large browser application and
the management API. Earlier guides mentioned several reasonable alternatives,
including React Router, React Hook Form with Zod, and Connect-Web. Leaving those
as implicit choices makes generated contracts, cancellation, authentication,
and upgrade ownership ambiguous.

## Decision

### REST and OpenAPI

The supported browser, CLI, and SDK API is versioned JSON over HTTP and is
described by the canonical OpenAPI document. This matches the Go HTTP server,
same-origin cookie and CSRF boundary, CLI, and generated SDKs. Connect-Web is
not introduced as a second transport. Streaming features use the existing
authenticated WebSocket and server-sent-event contracts where ordinary HTTP
request/response behavior is insufficient.

### Generated client boundary

Frontend network modules call generated OpenAPI operations and derive wire
types from `frontend/src/types/openapi.generated.ts`. Application-facing
adapters may map those wire shapes into view models, but they must not recreate
high-risk response unions or call the raw transport for a mounted ordinary JSON
operation. The Kubernetes proxy is the deliberate exception: it carries
dynamic Kubernetes payloads through a bounded, permission-checked adapter and
does not pretend those payloads are Astronomer OpenAPI resources.

Every core list and detail query accepts and forwards `AbortSignal`. The raw-
transport, generated-shape, generated-client freshness, and query-cancellation
checks in `npm run code-health` enforce this boundary.

### TanStack application libraries

- TanStack Router owns file-based route generation, typed parameters, route
  loading, and route guards. React Router is not installed as a parallel route
  graph.
- TanStack Query is the only server-state cache. It owns cancellation, retry,
  invalidation, and pagination state; server data is not copied into a second
  global store.
- TanStack Form and the application form kit own new complex forms, accessible
  error summaries, submit state, and field composition. React Hook Form and a
  second client-validation schema stack are not introduced. The API remains
  authoritative for domain validation.
- TanStack Table and Virtual provide the shared table and large-option-list
  primitives. They do not authorize data: the API performs scope filtering,
  stable sorting, search, counts, and pagination.

Small, isolated native React forms may remain when adopting the form kit would
add no state or accessibility value. They still use semantic `<form>` submit
behavior and the shared field/error primitives.

### Runtime and package baseline

Node **24.21.0** and npm **11.19.0** are the accepted build baseline. Local
version files, package engines, container builds, active workflows, and docs
must agree. There is no deliberate Node-version exception. The production
frontend is static output served by nginx; Node is not a production runtime.

## Consequences

- One OpenAPI change can be checked against server routes, generated browser
  operations, TypeScript types, CLI, and SDK consumers.
- The UI does not gain a second router, form framework, or server-state cache.
- View-model adapters remain useful, but generated wire types and cancellation
  must reach the transport edge.
- Moving to Connect, another router/form stack, or another Node major requires
  a superseding ADR plus migration and release-gate changes.

## Rejected alternatives

### Connect-Web as a parallel API

Rejected because it would duplicate authentication, CSRF, documentation, SDK,
and operational boundaries without a current product requirement that the
existing HTTP/WebSocket/SSE surfaces cannot meet.

### React Router or React Hook Form alongside TanStack

Rejected because parallel framework ownership increases bundle, test, and
accessibility policy surface. A future replacement must be a deliberate
migration, not per-feature choice.

### Handwritten response models at the transport edge

Rejected because they drift silently from OpenAPI. Explicit view models are
allowed only behind generated wire types and focused mapping tests.

## Review date

Review with the Node 26 support decision or by **2027-09-17**, whichever comes
first.
