# Agent Privilege Profiles

Astronomer adopted-cluster agents support three Kubernetes RBAC profiles. The profile is rendered into the agent install manifest and recorded on the cluster row as the reserved annotation `astronomer.io/agent-privilege-profile`.

Use the narrowest profile that supports the workflows the cluster needs.

| Profile | Intended use | Kubernetes access | Supported workflows | Expected limitations |
|---------|--------------|-------------------|---------------------|----------------------|
| `viewer` | Inventory, monitoring, and read-only inspection | `get`, `list`, and `watch` for common core, apps, batch, autoscaling, networking, policy, and CRD resources; read-only non-resource health/version endpoints | Cluster overview, resource browsing, logs, health probes, discovery, ArgoCD baseline observation through Astronomer | No workload mutation, no exec/attach/port-forward, no tool installation, no registry patching, no direct remediation |
| `operator` | Day-to-day operations without cluster-admin | Viewer permissions plus create/update/patch/delete for common workload, service, namespace, secret, ingress, NetworkPolicy, PDB, Role, and RoleBinding resources; CRDs remain read-only | Workload lifecycle operations, common tool installs that do not need CRD creation, pod exec/attach/port-forward, namespace/service updates, service proxy for approved tools | Cannot create or update CRDs, ClusterRoles, ClusterRoleBindings, admission webhooks, storage classes, or other cluster-admin resources |
| `admin` | Compatibility, bootstrap, and break-glass | `*` API groups, `*` resources, `*` verbs, and `*` non-resource URLs | All existing Astronomer workflows, including components that need cluster-wide installation or CRD management | Largest blast radius. Prefer only when baseline components or operator workflows require cluster-admin-like access |

## Selection Surfaces

- UI/API-created clusters default to `admin` unless a narrower profile is recorded on the cluster annotations before the manifest is rendered.
- `Cluster` CRDs can declare `spec.agent.privilegeProfile` as `viewer`, `operator`, or `admin`.
- The server normalizes invalid or missing values to `admin` for compatibility with existing clusters.

## ArgoCD Baseline Interaction

ArgoCD-owned platform baseline reconciliation uses the ArgoCD cluster proxy identity, not the human user's browser session. The adopted-cluster agent still needs enough Kubernetes access to serve the proxied API requests ArgoCD makes through Astronomer.

For least privilege, start with `operator` for clusters where baseline components are already installed or where baseline components do not need cluster-admin resources. Use `admin` for clusters where Astronomer or ArgoCD must install CRDs, ClusterRoles, ClusterRoleBindings, admission webhooks, or similar cluster-scoped resources.

## Operational Guidance

- Treat `admin` as an explicit risk acceptance and keep it visible in cluster review.
- Prefer `operator` for managed workload clusters.
- Prefer `viewer` for audit-only or inventory-only clusters.
- Re-render and re-apply the agent manifest after changing the profile.
- Validate important workflows after profile changes: resource browsing, logs, shell/exec, tool install, ArgoCD sync, backup, scan, and decommission.
