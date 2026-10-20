---
status: "proposed"
date: 2026-10-20
deciders: ["AlphaBravoCompany"]
title: "REST/OpenAPI to Connect-RPC migration strategy"
tags: [surface, architecture]
---

# REST/OpenAPI to Connect-RPC migration strategy

## Context

The guide makes Connect-RPC on chi the canonical API surface with proto enums as the single source of truth and generated Go/TypeScript clients. Astronomer exposes 855 REST operations (`docs/openapi.yaml`, 42k lines) from one flat 108k-LOC `internal/handler` package, with a hand-written axios layer (30k LOC) and OpenAPI-generated TypeScript types (25k LOC) and Go SDK (5.4 MB). Non-RPC surfaces (OAuth/OIDC/SCIM callbacks, SSE streams, the kubectl WebSocket shell, Grafana/Loki proxies, `/healthz`, `/metrics`) are legitimately REST/WS and stay on chi regardless.

## Decision Drivers

- The handler/service/store split (guide ch.01) must touch every handler anyway; binding each extracted domain to a Connect service is marginal cost on top of that rewrite, whereas a second REST-to-Connect pass later would touch everything twice.
- One codegen pipeline on both sides (buf + protoc-gen-es + connect-go) replaces ~15 OpenAPI scripts, two overlay ledgers and the 5.4 MB SDK that dominates git history.
- No users and CI off: pre-production hard cutovers are allowed (guide ch.08 principle 7).
- Measured velocity must decide the long tail; 855 operations is too many to commit blind.

## Considered Options

- Migrate domain by domain to Connect v2 mounted on the existing chi router, with a decision gate after three pilot domains.
- Keep REST/OpenAPI and record it as a permanent exception.
- Big-bang rewrite.

## Decision Outcome

Chosen option: "Domain-by-domain with a decision gate", scheduled as Phase 2. Pilot order: auth/sessions, user preferences/table views/platform settings, cloud credentials. After the third domain this ADR is amended with the measured cost per operation and one of two outcomes: (a) continue to all ~32 route groups, or (b) "Connect for new/rewritten domains, REST retained for the long tail" as the documented exception. Until then `docs/openapi.yaml`, `oapi-codegen.yaml` and the generated clients remain in the tree.

### Positive Consequences

- Each migrated domain gets the three-layer layout, proto enums and interceptors in one pass.
- The decision gate bounds the risk.

### Negative Consequences

- Two API styles coexist during Phase 2; the frontend carries axios and Connect-Web transports until the gate decides.

## Consequences

Phase 2 scaffolding: `proto/astronomer/v1/`, `buf.yaml`/`buf.gen.yaml`, `make generate` running buf + sqlc, `internal/api/apiutil` error mapping, interceptor chain `recover -> request-id/otel -> logging -> authn -> authz(map) -> protovalidate`, `frontend/src/lib/connect.ts`. Streaming/WebSocket/OAuth surfaces stay REST on `/api/v1`.

## Pros and Cons of the Options

### Domain-by-domain with a gate

- Good, because cost is measured before the long tail is committed.
- Bad, because of temporary duality.

### Keep REST permanently

- Good, because nothing moves.
- Bad, because the OpenAPI maintenance machinery and SDK blob stay forever and enums remain Python-generated.

### Big-bang rewrite

- Good, because duality is short.
- Bad, because 855 operations cannot be rewritten safely without users or CI feedback.

## Links

- Guide `03-surface.md`; gap analysis Phase 2.
