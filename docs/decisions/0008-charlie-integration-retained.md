---
status: "accepted"
date: 2026-10-20
deciders: ["AlphaBravoCompany"]
title: "Charlie integration is retained"
tags: [architecture, integrations]
---

# Charlie integration is retained

## Context

`internal/charlie` (~32k LOC), the `astro` CLI Charlie commands and the dashboard Charlie routes integrate Astronomer with Charlie, the sibling AI operations product, through a pinned Product Bridge contract (`internal/charlie/contract`), an MCP listener, durable action approvals and an in-cluster agent whose release is reconciled from Charlie's advertised artifacts. The realignment cleanup deleted the Charlie *qualification* harness (`internal/charliequalification`, the qualification/candidate-deploy hook binaries) but not the integration.

## Decision Drivers

- Charlie is a shipping product-family capability; removing it is a product decision, not an engineering-alignment one.
- The integration boundary already carries authorization, findings, durable actions, version negotiation and redaction.

## Considered Options

- Retain the integration (this ADR), delete only qualification theatre.
- Delete the integration as the "single largest deletable domain".

## Decision Outcome

Chosen option: "Retain", because the owner confirmed Charlie integration stays in scope. The artifact reconciler P0 (release re-applied every interval because the applied digests were never persisted) is fixed in this phase: `ArtifactReconciler` writes the applied chart/image digests back to `charlie_connections` after a successful Helm apply and the Helm upgrade caps `MaxHistory` at 5.

### Positive Consequences

- Product capability preserved; the runtime defect is fixed at the root.

### Negative Consequences

- `internal/charlie` is one of the packages the Phase 2 domain split must handle.

## Consequences

Charlie code follows the same phases as every other domain. The generated bridge client under `internal/charlie/contract/pinned` is excluded from lint like other generated code.

## Pros and Cons of the Options

### Retain

- Good, because it keeps a shipping capability.
- Bad, because it is a large domain to carry through Phase 2.

### Delete

- Good, because it removes 32k LOC.
- Bad, because it removes a product feature.

## Links

- `internal/charlie/artifact_reconcile.go`, `internal/charlie/activation_helm.go`, `docs/runbooks/charlie-integration.md`.
