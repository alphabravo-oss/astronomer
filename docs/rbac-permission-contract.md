# RBAC Permission Contract

Date: 2026-06-14

Astronomer RBAC uses a small product vocabulary of resources and verbs. UI
visibility is only a convenience; every protected action must be enforced by the
server through this resource/action contract.

## Scopes

| Scope | Meaning | Enforcement rule |
| --- | --- | --- |
| `global` | Platform-wide authority. | Applies to every project and cluster. Use only for platform operations, global settings, global GitOps, audit, users, SSO, and fleet-wide support. |
| `cluster` | One adopted cluster. | Applies only when the request carries the matching cluster ID. |
| `project` | One project/tenant. | Applies only when the request carries the matching project ID. Project membership must not imply namespace access beyond the granted project role. |
| `namespace` | Optional narrowing on a cluster or project binding. | When a binding carries a namespace, the request must carry the same namespace via route params or `?namespace=`. Storage/API/UI authoring for namespace bindings is still pending, so this is an enforcement contract for bindings once present. |

## Resources

| Resource | Owns |
| --- | --- |
| `clusters` | Adopted cluster inventory, registration, decommission, labels, templates, baseline ownership decisions, and cluster-scoped metadata. |
| `projects` | Project records, project policy, namespaces, project catalog subscriptions, quotas, and tenant grouping. |
| `workloads` | Deployments, StatefulSets, DaemonSets, Jobs, CronJobs, rollout actions, scale, restart, and workload YAML changes. |
| `pods` | Pod list/detail/log/exec/proxy-style diagnostics. |
| `monitoring` | Metrics, dashboards, monitoring configuration, SLO views, and metric-backed health. |
| `alerts` | Alert rules, notification policy, alert state, silences, and alert-related operations. |
| `catalog` | Helm/OCI repositories, catalog metadata, installed charts, and catalog-driven installs/upgrades. |
| `logging` | Logging pipeline configuration and log views. |
| `backups` | Backup schedules, backup runs, restore requests, restore drills, and backup evidence. |
| `security` | Scans, compliance posture, CIS results, vulnerabilities, policy evidence, and security reports. |
| `rbac` | Roles, role templates, bindings, group mappings, permission previews, and effective permission inspection. |
| `settings` | Global platform settings that are not covered by a narrower resource. |
| `argocd` | Argo CD instances, repositories, AppProjects, Applications, ApplicationSets, sync, rollback, and Argo cluster registration. |
| `sso` | SSO/OIDC/SAML/Dex connector and authentication provider configuration. |
| `users` | Local users, activation, password reset, locking, and user lifecycle. |
| `audit_logs` | Audit search, export, detail, and read-audit evidence. |
| `agents` | Agent fleet, diagnostics, self-test, lifecycle operations, compatibility, and upgrade queueing. |
| `secrets` | Kubernetes Secret list/read/create/update/delete surfaces and secret-adjacent project tooling. |
| `configmaps` | Kubernetes ConfigMap list/read/create/update/delete surfaces. |
| `services` | Kubernetes Service management and service proxy surfaces. |
| `ingresses` | Kubernetes Ingress and Gateway-style entry resource management. |
| `storage` | PersistentVolumeClaims, PersistentVolumes, StorageClasses, and storage health actions. |
| `nodes` | Node inspect, cordon, uncordon, drain, labels, annotations, and taints. |
| `service_mesh` | Mesh policy, mTLS, route validation, and service mesh health. |
| `support_bundles` | Redacted diagnostic/support bundle generation and download. |
| `cluster_templates` | Cluster template CRUD and template catalog management. |
| `fleet_operations` | Legacy-named multi-cluster operation authoring and execution until the API is renamed. |
| `network_policies` | Global network policy template CRUD and policy-template management. |
| `*` | Owner/admin wildcard. Allowed only in built-in owner/admin roles or explicit break-glass grants. |

## Verbs

| Verb | Meaning |
| --- | --- |
| `create` | Create a product object, Kubernetes object, operation, or desired-state record. |
| `read` | Read one object or one detail view. |
| `update` | Mutate an existing object or policy. |
| `delete` | Delete or decommission an object. |
| `list` | List or search a collection. |
| `watch` | Subscribe to live or watch-style updates. |
| `scale` | Change workload replica counts. |
| `restart` | Trigger a restart/rollout-style action. |
| `exec` | Open an exec/attach style stream into a pod. |
| `logs` | Read pod or workload logs. |
| `proxy` | Forward traffic to a Kubernetes, service, pod, Argo, or agent-backed proxy target. |
| `sync` | Trigger Argo CD sync, rollback, or deployment convergence action. |
| `manage` | High-risk compound action that spans several lower-level verbs or controls lifecycle state. |
| `*` | Owner/admin wildcard. Allowed only in built-in owner/admin roles or explicit break-glass grants. |

## UI To Permission Map

| UI action family | Server permission |
| --- | --- |
| View cluster list/detail, health, registration progress | `clusters:read` and `clusters:list` |
| Register, edit, attach template, label, or adopt a cluster | `clusters:create` or `clusters:update` |
| Decommission a cluster | `clusters:delete` |
| View project list/detail and namespaces | `projects:read` and `projects:list` |
| Create/edit/delete project policy or namespaces | `projects:create`, `projects:update`, or `projects:delete` |
| View workload inventory/YAML | `workloads:read` and `workloads:list` |
| Create/update/delete workload YAML | `workloads:create`, `workloads:update`, or `workloads:delete` |
| Scale or restart workloads | `workloads:scale` or `workloads:restart` |
| View pod detail and events | `pods:read` and `pods:list` |
| Stream pod logs | `pods:logs` |
| Open pod exec/attach/shell | `pods:exec` |
| Use Kubernetes, pod, service, Argo UI, or internal proxy surfaces | Matching resource with `proxy`; high-risk pod proxy also requires `pods:exec` |
| View or mutate ConfigMaps | `configmaps:read/list/create/update/delete` |
| View or mutate Secrets | `secrets:read/list/create/update/delete` |
| Manage Services, Ingresses, and entry points | `services:*`, `ingresses:*`, or `service_mesh:*` as appropriate |
| Cordon, uncordon, drain, label, annotate, or taint nodes | `nodes:manage` or `nodes:update` |
| View or run Argo sync, rollback, AppProject, repository, or ApplicationSet actions | `argocd:read/list/sync/create/update/delete/manage` |
| Queue agent diagnostics, self-test, or upgrade | `agents:read`, `agents:list`, `agents:update`, or `agents:manage` |
| Manage backups or restores | `backups:create/read/update/delete/manage` |
| Manage scans, compliance, or security evidence | `security:create/read/update/delete/list` |
| Manage roles, bindings, templates, and permission previews | `rbac:create/read/update/delete/list` |
| Manage users | `users:create/read/update/delete/list` |
| Manage SSO and Dex connectors | `sso:create/read/update/delete/list` |
| Manage global settings | `settings:read/update/manage` |
| Search/export audit logs | `audit_logs:read` and `audit_logs:list` |
| Generate or download support bundles | `support_bundles:create/read/list` |

## Kubernetes Verb Map

| Backend permission | Kubernetes operation boundary |
| --- | --- |
| `workloads:read/list/watch` | `get`, `list`, `watch` on apps/batch workload resources. |
| `workloads:create/update/delete` | `create`, `patch`, `update`, `delete` on apps/batch workload resources through server-side handlers. |
| `workloads:scale` | `patch`/`update` on the workload `scale` subresource where available. |
| `workloads:restart` | `patch` workload pod-template annotations or issue the equivalent rollout mutation. |
| `pods:read/list/watch` | `get`, `list`, `watch` pods and pod status. |
| `pods:logs` | `get` pods/log. |
| `pods:exec` | `create` pods/exec or pods/attach using a one-use stream ticket. |
| `pods:proxy` | Proxy to pod subresources after high-risk subresource checks. |
| `secrets:read/list/watch` | `get`, `list`, `watch` Secrets, with values redacted from audit/export paths. |
| `secrets:create/update/delete` | `create`, `patch`, `update`, `delete` Secrets. |
| `configmaps:create/read/update/delete/list/watch` | Matching Kubernetes ConfigMap verbs. |
| `services:create/read/update/delete/list/watch/proxy` | Matching Kubernetes Service verbs plus service proxy forwarding. |
| `ingresses:create/read/update/delete/list/watch` | Matching Ingress/Gateway entry-point verbs. |
| `nodes:read/list/watch` | `get`, `list`, `watch` Nodes. |
| `nodes:update/manage` | `patch` nodes and create pod eviction requests during drain. |
| `storage:create/read/update/delete/list/watch` | Matching PVC/PV/StorageClass verbs. |
| `network_policies:*` | NetworkPolicy template management in Astronomer; per-cluster apply is executed through controlled project/cluster reconciliation. |
| `argocd:*` | Argo CD API and ApplicationSet operations. Argo controller then applies Kubernetes changes using its configured cluster credentials. |

## Review Rules

- New backend routes must name the exact `resource:verb` they enforce, or they
  must be listed as intentionally public/auth-flow exceptions in the route
  classification inventory.
- New UI mutations must use the same `resource:verb` contract in disabled
  states and server calls.
- New role templates must use only the canonical resource and verb lists above.
- Non-admin built-in roles must not use `*` unless the role is explicitly an
  owner/admin/break-glass role.
- Kubernetes Secret reads must be gated by `secrets:read`, `secrets:list`, or
  `secrets:watch`; generic cluster read access is not enough.
