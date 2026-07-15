# Agent Privilege Profiles

Astronomer adopted-cluster agents support six Kubernetes RBAC profiles. The profile is rendered into the agent install manifest and recorded on the cluster row as the reserved annotation `astronomer.io/agent-privilege-profile`.

Use the narrowest profile that supports the workflows the cluster needs.

| Profile | Intended use | Kubernetes access | Supported workflows | Expected limitations |
|---------|--------------|-------------------|---------------------|----------------------|
| `viewer` | Inventory, monitoring, and read-only inspection | `get`, `list`, and `watch` for common core, apps, batch, autoscaling, networking, policy, and CRD resources; read-only non-resource health/version endpoints | Cluster overview, resource browsing, logs, health probes, discovery, ArgoCD baseline observation through Astronomer | No workload mutation, no exec/attach/port-forward, no tool installation, no registry patching, no direct remediation |
| `operator` (**PRIVILEGED / near-admin — H4**) | Day-to-day operations, but NOT safely contained | Viewer permissions plus create/update/patch/delete for common workload, service, namespace, **secret (cluster-wide read+write)**, ingress, NetworkPolicy, PDB, Role, and RoleBinding resources, **plus pod exec/attach/port-forward**; CRDs remain read-only | Workload lifecycle operations, common tool installs that do not need CRD creation, pod exec/attach/port-forward, namespace/service updates, service proxy for approved tools | No _direct_ self-escalation (cannot create/update CRDs, ClusterRoles, ClusterRoleBindings, admission webhooks, storage classes via rbac.authorization.k8s.io write). **HOWEVER this tier is effectively cluster-admin-equivalent via _indirect_ escalation:** cluster-wide secret read exposes every ServiceAccount token (incl. cluster-admin-bound SAs), and exec into a kube-system/control-plane pod yields that pod's identity. Treat `operator` as a privileged, audited, non-default opt-in — not a contained role. |
| `namespace-viewer` | Namespace-scoped inventory | Read-only workload, logs, service, autoscaling, networking, batch, and policy resources in the agent namespace | Team-scoped inspection where cluster-wide inventory is not allowed | No cluster-scoped resources, no mutations, no exec/attach/port-forward, no CRDs, no node/namespace inventory |
| `namespace-operator` | Namespace-scoped operations | Namespace-local workload mutations, logs, exec/attach/port-forward, services, NetworkPolicies, PDBs, Roles, and RoleBindings in the agent namespace | Team-scoped workload operation in locked-down clusters | No cluster-scoped resources, no CRDs, no ClusterRoles/ClusterRoleBindings, no secrets by default |
| `custom` | Externally managed RBAC | No default Kubernetes permissions from the Astronomer manifest | Operators bind exactly the permissions they want outside the generated manifest | Capability inference is intentionally conservative; live diagnostics should verify required workflows |
| `admin` | Compatibility, bootstrap, and break-glass | `*` API groups, `*` resources, `*` verbs, and `*` non-resource URLs | All existing Astronomer workflows, including components that need cluster-wide installation or CRD management | Largest blast radius. Prefer only when baseline components or operator workflows require cluster-admin-like access |

## Selection Surfaces

- UI/API-created clusters default to `viewer` (least privilege). The registration wizard exposes a profile selector (defaulting to viewer); broadening to `operator`/`admin` is an explicit, auditable choice recorded on the cluster annotations before the manifest is rendered.
- `Cluster` CRDs can declare `spec.agent.privilegeProfile` directly.
- `Cluster` CRDs can also declare `spec.agent.profileRef` pointing to a same-namespace `AgentProfile`. The Cluster reconciler resolves that profile, records `management.astronomer.io/agent-profile-ref`, and writes the resolved `astronomer.io/agent-privilege-profile` annotation used by manifest rendering.
- Referenced `AgentProfile` resources can also project install metadata into the same registration manifest path: `install.image`, `install.serviceAccountName`, and `install.podLabels`.
- The server normalizes an unspecified (missing or empty) value to `viewer` (least privilege), and an explicit unrecognized value (such as a typo) also fails closed to `viewer` — a no-annotation adoption never silently grants broad access.

## Upgrade note: implicit admin removal

The running agent, registration manifest, fleet heartbeat, self-test, and
upgrade planner now use the same effective-profile rule: a missing, blank, or
unrecognized profile resolves to `viewer`. Older agent deployments that omitted
`ASTRONOMER_PRIVILEGE_PROFILE` may previously have run as implicit `admin`; the
agent emits a startup warning when it detects that legacy omission.

Before upgrading, operators who intentionally depend on full-management access
must set `ASTRONOMER_PRIVILEGE_PROFILE=admin` explicitly (normally by
re-rendering the registration manifest with the `admin` annotation). Review
that choice as a risk acceptance. Do not set `admin` merely to make a denied
feature work: select a compatible explicit profile or grant narrowly reviewed
custom RBAC, then validate the required workflow.

Viewer does not start the Secret informer. Secret watching is enabled only for
an explicitly compatible normalized profile; unavailable capabilities are
reported as denied instead of silently widening the effective profile.

## Install Metadata

`AgentProfile.spec.install` controls install-time manifest details without making operators hand-edit generated YAML:

- `image` overrides the agent image rendered into the Deployment.
- `serviceAccountName` overrides the generated ServiceAccount name and matching RoleBinding subject.
- `podLabels` adds deterministic labels to the agent pod template for local policy, cost, or inventory selectors.

The Cluster reconciler stores these as reserved annotations:

- `management.astronomer.io/agent-image`
- `management.astronomer.io/agent-service-account-name`
- `management.astronomer.io/agent-pod-labels`

The renderer validates supported CRD inputs before projection. Manually setting these annotations through API automation should use the same constraints: Kubernetes image text without control characters, DNS-1123 service account names, and valid Kubernetes label keys and values.

## Capability Enforcement

`AgentProfile.spec.capabilities` is validated against the selected `privilegeProfile`. A profile cannot claim a capability the rendered RBAC profile cannot support. Supported capability keys are:

- `watch`
- `logs`
- `exec` / `shell`
- `helm`
- `service_proxy`
- `mutate`
- `secrets`
- `rbac`
- `cluster_scope`
- `namespace_scoped`
- `cluster_admin`
- `custom_rbac`
- `capability_inference`

For built-in profiles, `allowedRules` must stay inside the same permission boundary as the rendered manifest. For example, `viewer` cannot add `pods/exec`, mutating verbs, secrets, or wildcard rules, and `namespace-operator` cannot add cluster-scoped resources or secrets. `admin` can declare broad rules. `custom` can declare externally managed RBAC, and capability claims such as `exec` are accepted only when the declared rules include the matching resource and verb.

## ArgoCD Baseline Interaction

ArgoCD-owned platform baseline reconciliation uses the ArgoCD cluster proxy identity, not the human user's browser session. The adopted-cluster agent still needs enough Kubernetes access to serve the proxied API requests ArgoCD makes through Astronomer.

For least privilege, start with `operator` for clusters where baseline components are already installed or where baseline components do not need cluster-admin resources. Use `admin` for clusters where Astronomer or ArgoCD must install CRDs, ClusterRoles, ClusterRoleBindings, admission webhooks, or similar cluster-scoped resources.

## Operational Guidance

- Treat `admin` as an explicit risk acceptance and keep it visible in cluster review.
- Prefer `operator` only for managed workload clusters whose required
  operations justify its near-admin capability set.
- Prefer `viewer` for audit-only or inventory-only clusters.
- Prefer `namespace-operator` or `namespace-viewer` when Astronomer should only operate inside the agent namespace.
- Use `custom` only when you manage the Role/ClusterRole bindings outside the generated manifest.
- Re-render and re-apply the agent manifest after changing the profile with
  `kubectl apply --server-side --field-manager=astronomer-bootstrap -f -`.
- Validate important workflows after profile changes: resource browsing, logs, shell/exec, tool install, ArgoCD sync, backup, scan, and decommission.

## Enforcement model (M8 — important)

The privilege boundary is the **in-cluster RBAC** the manifest renders (the
agent ServiceAccount's ClusterRole/RoleBinding) — enforced by the target
cluster's API server. This matches Rancher's model: the API-server RBAC is the
ceiling, and the agent proxies calls with its own SA token (identity-stripped),
so it can never exceed what that SA is granted.

The `PRIVILEGE_PROFILE` value in the agent ConfigMap is **advisory/informational
only** — it records which profile was selected and drives the rendered RBAC at
install time, but the running agent does **not** perform a second, independent
profile check on each request. Consequences:

- If the in-cluster ClusterRole is tampered with or drifts (widened out of band),
  the agent has no second gate that would catch it — the API server's RBAC is
  the single source of truth. Review the rendered ClusterRole, not just the
  ConfigMap label, when auditing a cluster's actual privilege.
- Re-rendering and server-side-applying the manifest with field manager
  `astronomer-bootstrap` re-asserts the intended RBAC without taking ownership
  of the agent's durable credential; do that after any suspected drift.
- (Optional hardening, not implemented: a startup `SelfSubjectRulesReview` that
  alerts/refuses if the agent's live permissions exceed its declared profile.)
