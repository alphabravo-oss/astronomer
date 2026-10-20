---
status: "accepted"
date: 2026-10-20
deciders: ["AlphaBravoCompany"]
title: "Tenancy key is (cluster_id, project_id, namespace), not org_id"
tags: [data, security]
---

# Tenancy key is (cluster_id, project_id, namespace), not org_id

## Context

The guide (`02-data.md`, `06-security.md`) requires multi-tenant tables to carry `org_id` and every read/write to scope by it, with the authorization interceptor enforcing the tenant boundary. Astronomer is a single-organisation management plane: one installation serves one operator organisation, and isolation is between adopted clusters, projects and namespaces. The RBAC engine (`internal/rbac`) already evaluates every permission against `(cluster_id, project_id, namespace)` scopes, and `middleware.RequirePermission` derives the scope from URL parameters.

## Decision Drivers

- There is exactly one organisation per installation; `org_id` would be a constant column on 100+ tables.
- Isolation that matters to operators is cluster/project/namespace, which is already enforced centrally.
- Adding a synthetic `org_id` would create a second, weaker tenancy boundary that reviewers must reason about.

## Considered Options

- Record the tenancy key as `(cluster_id, project_id, namespace)` and treat the RBAC scope evaluation as the interceptor-level tenant check.
- Add `org_id` to every table and scope every query by it.

## Decision Outcome

Chosen option: "cluster/project/namespace tenancy key", because it reflects the product's actual isolation model and is enforced in one place today. Multi-organisation hosting, if it ever becomes a requirement, is a new product decision and gets its own ADR.

### Positive Consequences

- No schema churn; the authorization map (Phase 1) keys on the same scope.

### Negative Consequences

- A future multi-org deployment would require a migration that touches every table.

## Consequences

The Phase 1 static authorization map (`route -> permission`) MUST express scope in these three dimensions. New tables that hold cluster- or project-owned data MUST carry the scoping columns.

## Pros and Cons of the Options

### Cluster/project/namespace key

- Good, because it matches existing enforcement and the product boundary.
- Bad, because the guide's wording (`org_id`) does not map 1:1.

### Add org_id

- Good, because it matches the guide literally.
- Bad, because it is a constant column with no enforcement value in a single-org product.

## Links

- Guide `02-data.md`, `06-security.md`; `internal/rbac`, `internal/server/middleware/rbac.go`.
