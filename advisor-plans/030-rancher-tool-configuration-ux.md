# Plan 030 — Exceed Rancher's supported-chart configuration UX

## Status and scope

**IN PROGRESS — first implementation increment complete in the qualification branch.** This plan covers the configuration, review, lifecycle feedback and destructive-action UI for Astronomer's twelve Tool entrypoints. It complements Plan 028's API qualification: an installation is not usable merely because the API accepts it, and a polished form does not qualify the underlying chart.

The reference is the checked-out Rancher Dashboard and Rancher chart source in this workspace. The goal is to retain Rancher's proven interaction patterns while making effective values, multi-release ownership and recovery more explicit.

## Rancher behavior verified in local code

| Rancher behavior | Local source | Astronomer requirement |
|---|---|---|
| Chart-specific Vue forms promote common settings | `../rancher-dashboard/shell/chart/monitoring/`, `shell/chart/gatekeeper.vue`, `shell/chart/istio.vue`, `shell/chart/rancher-backup/` | Curated settings for every installable Tool; never invent an unsupported Helm path. |
| `questions.yaml` drives grouped typed fields, conditional questions and subquestions | `../rancher-dashboard/shell/components/Questions/` | Declarative groups and types now; add conditional fields when a supported tool needs them. Keep raw YAML for the complete chart surface. |
| Form, values YAML and diff are separate views | `../rancher-dashboard/shell/pages/c/_cluster/apps/charts/install.vue` | Settings, YAML and Review modes must round-trip without dropping advanced keys. Review must use the server's effective per-release plan. |
| Upgrades preserve prior non-default customizations across chart versions | `install.vue` value merge and `diff(...)` flow | Upgrade UI must start from durable installed values, show version-aware changes and preserve unknown advanced keys. |
| Auto-install annotations add prerequisite charts before the primary chart | `install.vue` handling of `catalog.cattle.io/auto-install` | Astronomer release plans own CRD/dependency releases explicitly and display their order before install. |
| Install and remove actions create durable catalog operations and expose logs | `install.vue`; `shell/models/catalog.cattle.io.app.js` | Keep the operation drawer, per-release events, retry and rollback visible through terminal state. |
| Removal can require exact-name confirmation and can include the auto-installed CRD app | `shell/components/PromptRemove.vue` | Exact tool-name confirmation, honest retained-data language, reverse dependency order and explicit product-specific destructive prerequisites. |

## Interaction contract

1. **Settings** shows a short, curated set of high-value values grouped by operator intent: scaling, storage, resources, networking, monitoring, scheduling and general behavior. Display the effective preset value without serializing it as a user override.
2. **YAML** accepts all advanced values. Switching back to Settings preserves unknown keys and projects known values into the form. Invalid or non-object YAML blocks Review and Install with an inline error.
3. **Review** calls the same public preview API used by automation with the selected preset and current override. It shows the ordered releases, immutable chart versions, namespaces and a per-release diff from the preset baseline to the final server-merged values.
4. **Install** sends only explicit operator overrides. The server validates curated field types and enumerations even for direct API callers, deep-merges distribution defaults, preset and overrides, then persists one durable ordered plan.
5. **Progress and recovery** show each release independently, preserve failure detail, and offer only actions the backend can complete. A preview, accepted request or Ready pod is never presented as successful functional validation.
6. **Uninstall** requires the tool name, explains chart-owned versus retained resources, and records progress in the same operation surface. Tools with an upstream deletion guard require an explicit API field and product-owned preparation step; the UI must not tell users that retained storage is certainly deleted or preserved.

## Tool configuration coverage

| Tool entrypoint | Curated configuration surface | Special lifecycle behavior |
|---|---|---|
| CIS Scanner | alerts/severity, scan scheduling override, operator resources | CRDs before operator; scan canary required. |
| Dex | No member-cluster install form | Route operators to management Auth settings; reject member installs. |
| Fluent Bit | resource requests/limits | Standalone defaults must not require a ConfigMap owned by another workflow. |
| Trivy Operator | severity, unfixed-CVE policy, timeout, resources | VulnerabilityReport canary and ingestion proof. |
| cert-manager | CRDs, replicas, resources | Issuer/Certificate canary and Secret readback. |
| Gatekeeper | audit interval, replicas, resources | Constraint enforcement canary. |
| kube-state-metrics | replicas and resources | Prometheus query canary. |
| Prometheus Node Exporter | host networking and resources | Detect port conflicts with baseline monitoring before mutation. |
| ingress-nginx | replicas, service exposure, ingress class, metrics, resources | Ingress request canary. |
| Longhorn | default-class policy, replica count, reclaim policy, kubelet root | PVC write/read canary; explicit deletion confirmation and node-loss phase. |
| NeuVector | controller/scanner scale and manager exposure | CRDs before app; security scan canary. |
| Istio | base revision, autoscaling, replicas, injection policy, resources | Base before istiod; injected-workload traffic canary. |

## Implementation increments

### Increment A — safe authoring and review

- [x] Curated form schema for every applicable Tool entrypoint.
- [x] Settings and YAML use one canonical override object.
- [x] Unknown YAML keys survive a Settings edit.
- [x] Invalid YAML blocks mode changes and install.
- [x] Public preview API accepts the current override.
- [x] Review shows server-merged per-release diffs and ordered release identity.
- [ ] Add schema-declared conditional visibility and field-specific bounds where the chart has dependent settings.
- [ ] Populate cluster-backed choices such as StorageClass and existing Secret through read-only public APIs, with free text retained for GitOps-created resources.

### Increment B — upgrades

- [ ] Add the same Settings/YAML/Review flow to Tool upgrade.
- [ ] Hydrate the durable installed values and distinguish inherited preset, previous override and new edits.
- [ ] Show additions/removals caused by the chart version separately from operator edits.
- [ ] Warn when an old value path is absent from the target chart while preserving the user's ability to review and remove it.

### Increment C — lifecycle-specific UX

- [x] Show the complete ordered release plan before installation.
- [x] Track durable progress and per-release failures in the operation drawer.
- [x] Require exact-name uninstall confirmation and describe retention truthfully.
- [x] Implement Longhorn's explicit deletion-confirmation API contract and surface its data consequence.
- [ ] Preflight known collisions and prerequisites, including Node Exporter host port ownership and required privilege profiles.
- [ ] Link a completed install to its functional surface and last qualification/canary state.

## Verification

- Unit tests must prove YAML → Settings → YAML retains uncurated nested values, form edits emit only explicit overrides, invalid YAML cannot install, and Review sends the override to the preview endpoint.
- Handler tests must prove direct API requests receive the same curated type/enum validation as the UI and multi-release values are partitioned by `values_key`.
- Live browser validation must exercise all three modes with at least one single-release and one multi-release Tool.
- Plan 028 remains the authority for install, canary, RBAC, upgrade, rollback, restart recovery, uninstall and cleanup PASS/FAIL evidence.
