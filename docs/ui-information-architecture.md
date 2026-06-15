# Astronomer UI Information Architecture

Date: 2026-06-14

Astronomer is a day-2 Kubernetes management product for adopted clusters. The UI should prioritize repeated operator workflows: inspect current state, understand risk, take a gated action, watch convergence, and audit what happened.

## Primary Areas

Dashboard:

- Purpose: fleet health summary, recent activity, urgent drift, degraded clusters, and shortcuts into common recovery paths.
- Primary users: platform operators and support engineers.
- Required states: loading, empty fleet, partial data, degraded dependency, permission-limited view.

Clusters:

- Purpose: adopted-cluster inventory, registration progress, cluster detail, Kubernetes resource explorer, workloads, tools, registries, snapshots, service mesh, image scans, network access, and GitOps ownership.
- Primary users: platform operators, cluster owners, support engineers.
- Interaction standard: high-risk actions must show permission state, dry-run or preview where possible, audit behavior, and operation progress.

Cluster Explorer:

- Purpose: cross-cluster and per-cluster Kubernetes inspection for pods, workloads, services, ingresses, nodes, namespaces, storage, config, secrets metadata, CRDs, and custom resources.
- Primary users: platform operators and support engineers.
- Interaction standard: list pages use `DataTable`; detail/repair surfaces use `PageShell`, `PageHeader`, `PageSection`, `DrawerShell`, `ActionButton`, and `OperationTimeline`.

Projects:

- Purpose: tenancy, namespace membership, quotas, cloud credentials, catalogs, and project policy.
- Primary users: project owners and platform operators.
- Interaction standard: project-scoped pages must make cluster/project scope visible and avoid cross-tenant ambiguity.

GitOps:

- Purpose: Argo CD instances, Applications, ApplicationSets, AppProjects, repos, managed clusters, sync windows, rollback, orphan detection, and baseline ownership.
- Primary users: GitOps operators and platform operators.
- Interaction standard: every sync, rollback, adopt, replace, and unregister action requires clear target identity, audit trail, and convergence status.

Agents:

- Purpose: agent fleet health, capabilities, diagnostics, compatibility, upgrade plans, self-tests, and offline behavior.
- Primary users: platform operators and support engineers.
- Interaction standard: disconnected/offline states must explain which actions are blocked, queue-safe, or stale.

Observability:

- Purpose: metrics, logging, alerting, anomaly baselines, operations queues, and support bundle entry points.
- Primary users: platform operators and SREs.
- Interaction standard: degraded data must show data source, freshness, and runbook destination.

Security:

- Purpose: image scans, CIS scans, policies, compliance baseline posture, secret-read auditing, and sensitive route review.
- Primary users: security auditors and platform operators.
- Interaction standard: findings must show severity, affected scope, current owner, and next safe action.

Backups:

- Purpose: storage locations, schedules, runs, restores, restore drills, and backup health.
- Primary users: platform operators and disaster recovery owners.
- Interaction standard: restore actions must identify source, destination, destructive risk, and rollback/retry path.

Catalog:

- Purpose: curated platform tools, project catalogs, chart install/upgrade paths, and component discovery.
- Primary users: platform operators and project owners.
- Interaction standard: install/upgrade flows must show target cluster, namespace, values, dry-run status, and operation progress.

RBAC:

- Purpose: roles, bindings, effective permissions, templates, and access explanation.
- Primary users: platform admins and auditors.
- Interaction standard: permission grants must show scope, source binding, and high-risk grants before save.

Settings:

- Purpose: platform configuration, auth, SSO, SMTP, Vault, GitOps sources, quotas, webhooks, operations, cluster groups, compliance, templates, widgets, and read-audit policies.
- Primary users: platform admins.
- Interaction standard: settings pages use explicit save/test actions, validation summaries, and audit behavior.

Audit:

- Purpose: who/what/when/where investigation across mutating, read, auth, and system events.
- Primary users: auditors, platform operators, and support engineers.
- Interaction standard: row detail uses `ActivityDetailsDrawer`; filters must include actor, target, action, class, result, cluster, project, correlation ID, request ID, and time range.

## Navigation Rules

- Sidebar owns primary areas and permission-gates entries that are not available.
- Topbar breadcrumbs show current hierarchy and remain compact.
- `Cmd/Ctrl+K` opens the command palette for pages, clusters, projects, GitOps apps, resource-search shortcuts, and runbook destinations.
- `/` focuses the topbar resource search.
- Cross-cluster Kubernetes search lives at `/dashboard/search`; the command palette navigates into it instead of running fan-out search inside the overlay.

## Page Pattern Rules

- Use `PageShell` for page spacing.
- Use `PageHeader` for page title, description, eyebrow, and primary actions.
- Use `PageSection` for unframed content groups.
- Use `DataTable` for structured lists.
- Use `StatusBadge` for health, sync, drift, agent, permission, and result states.
- Use `StatePanel` variants for loading, empty, error, permission, disconnected, and stale states.
- Use `ActionButton` for save, destructive, loading, dry-run, approval-required, and disabled-with-reason actions.
- Use `DrawerShell` or `ActivityDetailsDrawer` for side-panel detail.
- Use `OperationTimeline` for multi-step registration, upgrades, restores, syncs, and repair flows.
