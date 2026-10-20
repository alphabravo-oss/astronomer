---
status: "accepted"
date: 2026-10-20
deciders: ["AlphaBravoCompany"]
title: "Hard delete with transactional audit instead of deleted_at soft delete"
tags: [data]
---

# Hard delete with transactional audit instead of deleted_at soft delete

## Context

The guide's data chapter requires `deleted_at` on tables holding user data and a `deleted_at IS NULL` filter on every read. Astronomer has zero `deleted_at` columns across ~100 migrations and ~99 sqlc query files. Deletes are hard and every mutation writes an audit row through the transactional outbox (migration 015). Several domains additionally keep explicit lifecycle rows (cluster tombstones with `CLUSTER_TOMBSTONE_RETENTION_DAYS`, decommission records, revoked tokens) where a "deleted but recoverable" state is a product feature rather than a column.

## Decision Drivers

- Credentials, kubeconfigs and tokens MUST be gone when an operator deletes them (compliance and least-privilege); soft delete keeps secret material alive.
- Audit history (not the deleted row) is the recovery/forensics record.
- Adding `deleted_at` plus a filter to every sqlc query touches hundreds of queries during the Phase 2 rewrite window with high regression risk.

## Considered Options

- Hard delete + transactional audit outbox + explicit lifecycle rows where recovery matters (this ADR).
- Blanket `deleted_at` on all user-data tables with filtered reads.

## Decision Outcome

Chosen option: "Hard delete with audit", because secret-bearing rows must not survive deletion and the product already models recoverable deletes explicitly where users need them. Tables that need a recovery window (clusters, projects, catalogs) MUST use explicit lifecycle rows or tombstones with retention, never a nullable timestamp on the live row.

### Positive Consequences

- No secret material lingers after delete; no query-wide filter to forget.

### Negative Consequences

- A guide reviewer sees a literal "No" on the `deleted_at` row; this ADR is the documented exception.

## Consequences

New domains MUST decide at design time whether a recoverable delete is a product requirement and, if so, model it as an explicit state with a retention sweep. Phase 1/2 does not add `deleted_at`.

## Pros and Cons of the Options

### Hard delete with audit

- Good, because deletion of credentials is real.
- Good, because the audit outbox already records who deleted what and when.
- Bad, because accidental deletes of non-secret rows are not reversible in place.

### Blanket deleted_at

- Good, because it matches the guide literally.
- Bad, because every read needs the filter and secret rows outlive their deletion.

## Links

- Guide `02-data.md`; migration `015_audit_outbox`; `internal/audit`.
