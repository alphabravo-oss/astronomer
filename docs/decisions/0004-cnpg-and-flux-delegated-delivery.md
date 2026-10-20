---
status: "accepted"
date: 2026-10-20
deciders: ["AlphaBravoCompany"]
title: "Preserve CNPG-managed PostgreSQL and Flux-delegated delivery"
tags: [architecture, data, delivery]
---

# Preserve CNPG-managed PostgreSQL and Flux-delegated delivery

## Context

Plan 032 (the previous guide-alignment attempt, now deleted from the tree) recorded a "product architecture that MUST be preserved" list. This ADR carries that list forward so the realignment never erodes it:

| Area | Preserved decision |
| --- | --- |
| Product boundary | Adopt and operate existing clusters; no provisioning subsystem, CAPI or Crossplane merely to match the guide |
| Production database | CloudNativePG-managed PostgreSQL (17.x) with backups, PITR and synchronous replication semantics |
| External database | Explicit supported external PostgreSQL (DSN/CA/Secret ownership) |
| Development database | Bundled PostgreSQL is development-only |
| Cache/queue | Valkey-compatible Redis, asynq, durable outboxes (ADR-0003) |
| Delivery | Local Flux source/kustomize/helm controllers on each managed cluster; no Fleet/Argo, no central remote-kubeconfig executor |
| Agent transport | Outbound authenticated agent identity and tunnel with least privilege, rotation, generation fences |
| Product state | PostgreSQL is authoritative; intent is never reconstructed from Flux objects |
| Object storage | S3 API (AWS S3 and qualified S3-compatible endpoints); no mandatory MinIO runtime |
| Charlie | Product Bridge contract and qualified artifacts (ADR-0008) |
| Release | Signed manifests, images, SBOMs, provenance |

## Decision Drivers

- These are the behaviours customers are evaluated on; the guide alignment is about engineering discipline, not product scope.
- CNPG preservation checklist: retain `postgres.mode: cloudNativePG`, `deploy/chart/templates/postgres-cnpg.yaml`, scheduled backups, external Barman credentials; never change `imageName` majors on a live data directory; keep operator upgrades distinct from server-major upgrades.

## Considered Options

- Preserve (this ADR).
- Replace delivery with a central controller or the database with a bundled StatefulSet to "simplify" the guide mapping.

## Decision Outcome

Chosen option: "Preserve", because the guide explicitly allows Helm/Flux-based delivery and PostgreSQL-authoritative state; nothing in the guide requires replacing them.

### Positive Consequences

- The realignment phases can be reviewed against a fixed list of non-negotiables.

### Negative Consequences

- Some guide chapters (air-gap registry resolver, operator placement) must be implemented around Flux rather than replacing it.

## Consequences

Every later phase (goose migrations, age encryption, Connect-RPC) MUST keep CNPG data directories, keys and backup histories intact across ordinary upgrades.

## Pros and Cons of the Options

### Preserve

- Good, because it protects production behaviour and existing qualification.
- Bad, because a few guide mappings are indirect.

### Replace

- Good, because a literal guide mapping is simpler to audit.
- Bad, because it destroys the product's differentiators and existing evidence.

## Links

- `docs/architecture/decisions/flux-native-delivery.md`, `deploy/chart/templates/postgres-cnpg.yaml`.
