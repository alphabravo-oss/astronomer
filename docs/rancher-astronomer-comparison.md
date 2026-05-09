# Astronomer Go vs Rancher Comparison Matrix

Last updated: March 10, 2026

## Scope

This document compares the current `astronomer-go` implementation against:

- Rancher as the benchmark for enterprise multi-cluster Kubernetes management
- The intended Astronomer product direction:
  - open and popular ecosystem choices
  - ArgoCD instead of Fleet
  - Prometheus + Thanos instead of Rancher-specific monitoring packaging assumptions
  - management of existing/registered clusters first
  - no accidental drift into closed or tightly coupled platform internals

This is not a claim that `astronomer-go` should clone Rancher feature-for-feature. It is a status and gap analysis for the intended product strategy.

## Status Legend

- `At Parity`: good enough for the intended Astronomer strategy, not necessarily identical to Rancher internals
- `Behind But Aligned`: not yet as deep as Rancher, but going in the right product direction
- `Intentionally Excluded`: Rancher capability we are not trying to replicate
- `Needs Decision`: area where product scope or architectural direction is still unresolved

## Executive Summary

`astronomer-go` is not beyond Rancher overall. It is strongest today in the monitoring/control-plane direction, where the architecture now matches the intended Astronomer strategy better than the original Go rewrite did:

- monitoring stack lifecycle is now controller-like instead of request-driven
- Prometheus + Thanos is the enterprise metrics direction
- monitoring operations are durable, auditable, drift-aware, and readiness-gated
- GitOps direction is ArgoCD, not Fleet

The product is still behind Rancher in total operational breadth, especially in:

- broader controller depth across all subsystems
- app/catalog lifecycle maturity beyond monitoring
- full enterprise cluster lifecycle and reconciliation breadth
- deeper RBAC/project tenancy hardening
- large-scale GitOps orchestration depth

## Matrix

| Area | Rancher Baseline | Astronomer Direction | Current Status | Assessment |
|---|---|---|---|---|
| Multi-cluster registration and management | Strong | Must match | Existing cluster registration and management path exists | `Behind But Aligned` |
| Cluster provisioning and lifecycle | Strong | Optional / probably excluded | Not a primary `astronomer-go` focus | `Intentionally Excluded` |
| GitOps engine | Fleet | ArgoCD | ArgoCD APIs and integration exist, but not full lifecycle/controller depth | `Behind But Aligned` |
| Monitoring architecture | Prometheus-based | Prometheus + Thanos | Real backend config, stack lifecycle, reconciler, operations, readiness, rollback | `At Parity` for intended direction |
| Alerting execution model | Prometheus/Alertmanager ecosystem | PromQL-backed + managed routing | App-managed rules/events plus shared Alertmanager assets and PromQL-backed evaluation | `Behind But Aligned` |
| Logging | Operational integration | Standard OSS stack | Present, but not yet as deep or operator-driven as Rancher-scale integrations | `Behind But Aligned` |
| Security scanning/policy | Broad ecosystem integrations | OSS-first security | Implemented API surface, but not yet deep enterprise execution breadth | `Behind But Aligned` |
| Backup/restore | Platform ops features | Keep | Present, but not controller-grade across all flows | `Behind But Aligned` |
| Catalog/app lifecycle | Mature chart/app operations | Keep, but OSS/open choices | Tools/catalog exist, monitoring now uses operation model | `Behind But Aligned` |
| Service proxy / cluster access | Strong | Must match | Present and used by monitoring readiness and tool access | `At Parity` for core use |
| Workloads/resources UX/API | Strong | Must match | Major tunnel-backed paths implemented; some generic-resource behavior still compatibility-grade | `Behind But Aligned` |
| Metrics history | Prometheus TSDB | Prometheus + Thanos | Implemented direction; stack and control plane exist | `At Parity` for architecture |
| Controller/reconciler model | Strong | Must match for critical subsystems | Monitoring has it now; rest of platform mostly does not | `Behind But Aligned` |
| RBAC and tenancy | Mature multi-scope model | Must match | Core paths exist, but hardening and scope enforcement depth still trails Rancher | `Behind But Aligned` |
| Auditability / operations traceability | Strong | Must match | Monitoring operations now durable with event trails | `At Parity` for monitoring, behind elsewhere |
| Fleet-specific internal workflows | Used in Rancher | Replace with ArgoCD and simpler controllers | Not implemented | `Intentionally Excluded` |

## Area-by-Area Assessment

### 1. Cluster Management

Status: `Behind But Aligned`

What is working:

- cluster registration exists
- tunnel-backed management exists
- workloads, resources, metrics, logs, and service proxying are present
- the architecture is suited to managing existing clusters without tight cloud-provider coupling

What is still behind Rancher:

- less controller depth around ongoing reconciliation of cluster state
- less complete operational lifecycle around cluster health, drift, and recovery workflows
- fewer enterprise guardrails around scale, failure domains, and remediation loops

Next steps:

1. Add controller-style reconciliation for cluster connectivity, cluster health, and cluster capability state.
2. Persist observed cluster capability and drift state instead of computing it only at request time.
3. Add operation models for more cluster-affecting actions, not just monitoring.

### 2. Cluster Provisioning

Status: `Intentionally Excluded`

Rancher:

- provisions clusters and manages full cluster lifecycle in multiple environments
- uses internal workflows that include Fleet in some areas and other provider-specific controllers

Astronomer direction:

- do not replicate Rancher’s full provisioning stack unless product scope changes
- manage existing clusters well instead of overexpanding into infrastructure lifecycle prematurely

Handling today:

- `astronomer-go` is oriented toward registration and management of existing clusters

Next steps:

- keep this excluded unless the product explicitly decides that cluster creation/provisioning is in scope

### 3. GitOps

Status: `Behind But Aligned`

Rancher:

- Fleet is both a product capability and an internal dependency in some workflows

Astronomer direction:

- ArgoCD instead of Fleet
- stay aligned to the broader OSS Kubernetes ecosystem

Handling today:

- ArgoCD integration exists
- the strategic substitution is correct
- lifecycle depth and controller maturity still lag Rancher/Fleet depth

What still needs work:

- durable operation model for ArgoCD lifecycle similar to monitoring
- controller-style reconciliation for ArgoCD instances and application health
- stronger app/project/cluster scoping around GitOps targets
- better rollout, failure, retry, and drift reporting

Next steps:

1. Move ArgoCD lifecycle onto durable operations and a reconciler, following the monitoring pattern.
2. Add status/readiness/drift checks for ArgoCD instances and applications.
3. Define explicit product boundaries where ArgoCD replaces Fleet and where no Fleet-like feature will be offered.

### 4. Monitoring

Status: `At Parity` for intended Astronomer direction

This is the most enterprise-mature subsystem in the current Go rewrite.

What now exists:

- Prometheus + Thanos architecture direction
- shared Thanos lifecycle
- shared Alertmanager lifecycle
- per-cluster monitoring stack lifecycle
- durable monitoring operations
- server-hosted reconciler with live cluster access
- replace-required preflight for immutable stateful changes
- object-store secret externalization
- operation event trails
- pod, service, and smoke-query readiness gates
- optional rollback-on-upgrade-failure
- backend-level default operation policy

Why this matters:

- this is now much closer to Rancher’s operational model than the original synthetic metrics path
- it is also better aligned with the intended open ecosystem strategy

Remaining work:

- component-specific status summaries beyond the current readiness checks
- deeper declarative reconciliation of shared stacks over time
- external secret manager integration instead of DB-originated credential material
- broader failure remediation policies and SLO-style health surfacing

Next steps:

1. Add dedicated monitoring controller status endpoints for reconciler health, queue depth, and stuck operations.
2. Add external secret manager support.
3. Add richer shared-Thanos and shared-Alertmanager health summaries.

### 5. Alerting

Status: `Behind But Aligned`

What is good:

- PromQL-backed evaluation exists
- shared Alertmanager config is managed from application state
- rules, silences, channels, and events are durable
- shared monitoring stack and alerting assets are now tied together operationally

What is still behind Rancher-grade enterprise behavior:

- app-side evaluation still owns too much of the alert execution model
- rule types and evaluation semantics are still incomplete compared with a fully native Prometheus/Alertmanager/Ruler workflow
- deeper separation between rule authoring, alert delivery, and alert execution is still needed

Next steps:

1. Move more rule execution toward native Ruler-compatible flows.
2. Keep app DB for policy/UI state, but reduce heuristic fallback paths.
3. Add richer routing, inhibition, and enterprise alert policy controls.

### 6. Catalog / Apps / Tools

Status: `Behind But Aligned`

What exists:

- catalog sync and chart/version ingestion
- tool lifecycle paths
- monitoring now uses a durable operation model instead of direct handler-owned mutations

What is still missing:

- the same operation/reconciler depth should be applied to tools and app lifecycle broadly
- drift detection and rollout policy are weaker than Rancher’s app/controller model

Next steps:

1. Move tool and app lifecycle onto durable operations.
2. Add readiness, rollback, and drift detection for tool installs/upgrades.
3. Standardize this subsystem on the same controller pattern used by monitoring.

### 7. Workloads and Generic Resources

Status: `Behind But Aligned`

What exists:

- major workload/resource read paths are implemented
- service proxy, pod logs, and related cluster operations work through the tunnel

What still trails Rancher:

- some generic resource mutations are still compatibility-grade rather than resource-specific workflows
- controller-level reconciliation is limited
- enterprise UX semantics around failures, retries, and consistency are still thinner

Next steps:

1. Replace remaining proxy-only mutation paths with explicit resource-aware handlers where needed.
2. Add durable operations for disruptive workload actions like restart/scale/delete where auditability matters.
3. Tighten API semantics to remove remaining placeholder-grade behavior.

### 8. RBAC and Tenancy

Status: `Behind But Aligned`

What exists:

- multi-scope RBAC model is present
- auth hardening work has already improved token handling and identity wiring

What still needs work:

- more exhaustive scope enforcement across all subsystem handlers
- stronger project/cluster scoping in lifecycle operations
- more controller-aware authorization around reconciler-driven actions

Next steps:

1. Audit all handlers for consistent scope checks.
2. Add reconciliation-safe service identities and audit attribution for controller actions.
3. Add explicit tenancy tests for project and cluster scoping.

### 9. Security, Logging, Backups

Status: `Behind But Aligned`

What exists:

- these subsystems are present in the API and have more than placeholder coverage
- backup and restore already had an operation model before monitoring did

What still trails enterprise depth:

- execution and reconciliation patterns are not yet as mature as monitoring
- some integrations are still pragmatic rather than deeply operator-driven

Next steps:

1. Apply the monitoring operation/reconciler pattern to backup/restore and other stateful operational features.
2. Add stronger execution telemetry and readiness/completion semantics.
3. Standardize subsystem health and drift reporting.

## How We Are Handling Fleet vs ArgoCD

Rancher reality:

- Rancher uses Fleet as a product GitOps engine
- Rancher also uses Fleet-related patterns in some of its own internal and provisioning-oriented workflows

Astronomer handling:

- we are not adopting Fleet as a platform dependency
- we are using ArgoCD as the GitOps surface
- we should deepen the ArgoCD path rather than reintroducing Fleet-like internals by another name

This is the right choice for the product strategy because:

- ArgoCD is broadly adopted and operator-friendly
- it reduces dependence on Rancher-specific control-plane assumptions
- it fits the “more flexible and open source popular” requirement better

Implication:

- where Rancher uses Fleet internally, Astronomer should prefer:
  - ArgoCD where the problem is GitOps
  - direct controller/reconciler patterns where the problem is control-plane state convergence
  - not a hidden second GitOps engine

## How We Are Handling Rancher Provisioning Features

Current handling:

- not replicated as a first-class `astronomer-go` capability
- the product currently focuses on management of existing/registered clusters

Why:

- it keeps the platform concentrated on multi-cluster operations
- it avoids ballooning into cloud/provider lifecycle complexity too early
- it avoids inheriting Rancher’s provisioning surface area just because Rancher has it

If this changes:

- provisioning must become an explicit product decision, not accidental scope creep
- it should be evaluated as a separate subsystem with its own controller model, not bolted into the current management plane casually

## Recommendation

The current strategy is correct:

- keep ArgoCD instead of Fleet
- keep Prometheus + Thanos for monitoring
- keep provisioning out of scope unless product explicitly changes
- continue applying the monitoring controller pattern across the rest of the platform

## Highest-Value Next Work

1. ArgoCD lifecycle and reconciliation
2. Tool/app lifecycle operations and readiness/rollback
3. RBAC and tenancy hardening across all controller-driven actions
4. Standardized controller/operation health endpoints across major subsystems
5. Broader subsystem migration from request-driven mutations to durable operations
