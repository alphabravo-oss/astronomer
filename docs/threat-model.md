# Threat Model

Date: 2026-06-14

Astronomer is a high-privilege Kubernetes management plane. The main security objective is to prevent a user, browser, token, proxy, or compromised component from gaining broader cluster access than intended.

## Shared Assets

| Asset | Protection goal |
|-------|-----------------|
| Browser session cookies and stream tickets | Prevent theft, replay, and leakage through URLs/logs. |
| API tokens and ArgoCD proxy tokens | Hash lookup material, encrypt reusable plaintext, scope use to intended routes/clusters. |
| Agent tunnel | Accept only registered agents and route traffic only to the owning cluster. |
| Adopted cluster Kubernetes API | Require Astronomer auth/RBAC before proxying; strip caller-controlled upstream credentials and impersonation headers. |
| Postgres | Durable source of truth for users, RBAC, audit, credentials, inventory, and operation rows. |
| Redis/asynq | Ephemeral queue and scheduling state; must not be the only durable record of an operator decision. |
| Management CRDs | Kubernetes-facing desired-state API for clusters, projects, baselines, bundles, agent profiles, and GitOps targets. |
| ArgoCD cluster Secrets and ApplicationSets | Deployment reconciliation surface, not the product database. |
| Management-plane Helm release | Deployment security boundary for network policy, pod security contexts, bootstrap credentials, ingress/TLS, and backup jobs. |
| Management backups | Durable copy of Postgres state; must remain encrypted/separated from encryption keys and periodically restore-tested. |
| Container images, Helm repos, and generated manifests | Supply-chain inputs that can change runtime permissions or code paths. |

## Browser Sessions

Trust boundary: browser to Go API.

Threats:

- XSS stealing long-lived tokens.
- CSRF on cookie-authenticated unsafe requests.
- Session token leakage through stream URLs, referrers, proxy logs, or screenshots.
- Stale localStorage tokens surviving logout.

Controls:

- Browser login, refresh, TOTP, SSO callback, and logout use HttpOnly `astronomer_session` and `astronomer_refresh` cookies.
- Unsafe cookie-authenticated requests require double-submit CSRF via `astronomer_csrf` and `X-CSRF-Token`.
- Browser EventSource/WebSocket clients use one-use stream tickets instead of `?token=`.
- Frontend startup/logout removes legacy localStorage token keys.
- API responses include CSP and standard browser hardening headers.

Review checks:

- Any new browser-authenticated mutation must either use bearer/API-token auth or pass CSRF validation.
- No new browser stream URL may carry a JWT, refresh token, API token, or ArgoCD proxy token.
- Session cookie changes must include auth middleware tests.

## Agent Tunnel

Trust boundary: management plane to adopted-cluster agent.

Threats:

- Cross-cluster request confusion.
- Server pod without the target websocket forwarding to the wrong owner.
- Client-supplied Kubernetes credentials or impersonation headers reaching the adopted cluster.
- Over-privileged agent ServiceAccount.

Controls:

- K8s proxy path requires Astronomer auth and cluster RBAC before forwarding.
- Mutating passthrough requests require API-token write scope when authenticated by API token.
- High-risk pod subresources (`exec`, `attach`, `portforward`) require `pods:exec`.
- Proxy strips inbound `Authorization`, cookies, `X-Forwarded-*`, `Host`, and `Impersonate-*` headers.
- Cross-pod forwarding uses the cluster owner locator and avoids self-forward loops.
- Agent manifests support `viewer`, `operator`, `namespace-viewer`,
  `namespace-operator`, `custom`, and `admin` RBAC profiles.

Review checks:

- New tunnel message types must include cluster ID validation and route-level authorization.
- New Kubernetes passthrough behavior must include tests for method/path/body forwarding and auth/RBAC denial.
- New agent RBAC rules must be justified in `docs/agent-privilege-profiles.md`.

## ArgoCD Cluster Proxy

Trust boundary: ArgoCD controller to Astronomer internal cluster proxy to adopted-cluster agent.

Threats:

- ArgoCD token reused against unrelated Astronomer APIs.
- Token for one cluster used against another cluster.
- ArgoCD and Helm-over-tunnel both owning the same baseline components.
- Lost ArgoCD Secret or DB row causing silent drift.

Controls:

- ArgoCD uses a dedicated internal `/api/v1/internal/argocd/clusters/{id}/k8s/*` proxy route.
- Proxy tokens use `astro_argocd_*` material, hash-based validation, encrypted reusable plaintext, and cluster ID scoping.
- Generic public k8s proxy remains human/API-token auth plus RBAC.
- Baseline cluster-template apply skips ArgoCD-owned baseline tools when ArgoCD baseline ownership is enabled.
- Cluster responses and CRD status expose ArgoCD adoption and baseline ownership state.

Review checks:

- ArgoCD proxy tokens must never be accepted by general API auth middleware.
- Token creation/rotation/deletion must update decommission and repair paths.
- Baseline ownership changes must update `docs/control-plane-state-contract.md`.

## Service Proxy

Trust boundary: browser/API caller to in-cluster service through adopted-cluster agent.

Threats:

- SSRF into arbitrary in-cluster services.
- Proxying Kubernetes control-plane namespaces.
- Mutating a tool UI/API without RBAC, scope, or audit.
- Enabled tool accidentally exposed despite needing no browser proxy.

Controls:

- Service proxy requires cluster RBAC and API-token write scope for mutating methods.
- Targets must be Kubernetes service-shaped and present in enabled tool allowlists.
- `service_proxy_allowed=false` disables proxy exposure at preset or subservice level.
- Control-plane namespaces are blocked unless explicitly allowed.
- Non-read service proxy requests emit audit records.

Review checks:

- New proxied tools must declare whether service proxy exposure is allowed.
- New service proxy route behavior must include SSRF, namespace, scope, and audit tests.

## In-Browser Kubectl Shell

Trust boundary: browser WebSocket to server to agent to ephemeral in-cluster shell pod.

Threats:

- Unauthorized shell creation.
- Session hijack or cross-cluster websocket confusion.
- Unbounded long-running sessions.
- Sensitive output captured in audit logs.

Controls:

- Shell session creation and websocket routes require cluster update permission.
- Sessions create scoped Kubernetes resources and are reaped by TTL/hard-cap cleanup.
- Input command lines are recorded; output bytes are not recorded.
- WebSocket clients use stream tickets instead of long-lived query JWTs.

Review checks:

- Shell changes must preserve session ownership checks and cleanup paths.
- Audit changes must not capture stdout/stderr payload bytes.
- Any new shell privilege must be reflected in the agent privilege matrix.

## Control-Plane State Split

Trust boundary: Postgres, Kubernetes CRDs, Argo CD, Redis, and target clusters each own different state.

Threats:

- Duplicate state owners racing and overwriting each other.
- Redis or Argo CD being treated as the durable source for user decisions.
- CRD reconciliation broadening tenant scope beyond the selected project/cluster.
- Target-cluster cached inventory becoming stale but still shown as authoritative.

Controls:

- Postgres is the durable product source for users, RBAC, audit, inventory metadata, credentials, and operation history.
- Redis/asynq stores queue state only; durable operations are persisted in Postgres before execution.
- Argo CD owns deployment convergence for registered clusters and generated ApplicationSets.
- Management CRDs provide declarative intent and status, but their controllers enforce ownership labels, finalizers, version pins, project selectors, and same-namespace reference rules before writing Argo resources or DB annotations.
- Target clusters remain source of truth for live Kubernetes objects; mirrored/cache views must surface stale or degraded collection state.

Review checks:

- Any new durable decision must land in Postgres or a Kubernetes CRD with status/finalizer semantics, not only Redis or process memory.
- Any new CRD-to-Argo writer must enforce Astronomer ownership labels and refuse to overwrite unowned resources.
- Any new selector that can target clusters must prove it is bounded by `astronomer.io/managed-by=astronomer` and tenant/project constraints.

## GitOps Baseline And Argo CD

Trust boundary: Astronomer-generated ApplicationSets in Argo CD to adopted-cluster deployments.

Threats:

- ApplicationSet selectors accidentally targeting non-Astronomer Argo cluster Secrets.
- Baseline components being deployed in the wrong order or replacing user-owned resources.
- Lost Argo cluster Secrets or stale labels silently breaking fan-out.
- Manual sync-window override bypassing maintenance controls without accountability.

Controls:

- Built-in baseline ApplicationSets use Astronomer-owned labels, sync phases/waves, and per-component ownership metadata.
- User-created ApplicationSet generators are constrained to Astronomer-managed cluster labels.
- Orphan and stale ApplicationSet/Application detection compares cached state and live Argo state.
- Sync-window overrides require a reason that is persisted in operation payload/events/audit metadata.
- Baseline ownership decisions for adopt, leave unmanaged, and replace are explicit and audited.

Review checks:

- New baseline components must declare namespace, chart/repo, sync phase, selector labels, catalog seed behavior, and ownership migration behavior.
- New Argo actions must be permission-gated, audited, and visible in operation history.
- Any replacement/adoption flow must refuse unsafe local-cluster or unregistered-cluster cases.

## Management-Plane Deployment

Trust boundary: Kubernetes namespace running Astronomer itself.

Threats:

- Lateral movement from one management-plane pod to another through open namespace networking.
- Hook or backup jobs running with weaker security contexts than the steady-state deployments.
- Ingress/Gateway, Argo CD, external DB/Redis, and Kubernetes API egress being broader than intended.
- Bootstrap credentials or generated secrets becoming long-lived unknowns.

Controls:

- Helm renders a namespace default-deny NetworkPolicy plus explicit component ingress/egress policies.
- Production values clear broad legacy egress and expose narrow CIDR buckets for HTTPS, Postgres, Redis, Kubernetes API, and identity-provider traffic.
- App pods, hook jobs, and backup jobs run non-root, drop capabilities, block privilege escalation, use seccomp, and use read-only root filesystems where possible.
- Writable scratch space is mounted as `emptyDir` only where required, such as `/tmp` for backup/preflight/migrate workflows and Postgres/Redis data volumes.
- Production preflight refuses missing external Postgres/Redis, weak Dex secrets, missing backup credentials, or invalid TLS posture.

Review checks:

- Chart template changes must run Helm lint/render and update render tests when security controls change.
- New chart-managed containers must declare resources, pod security context, container security context, and a clear writable-volume reason when root is not read-only.
- New network paths must be reflected in NetworkPolicy values and production override guidance.

## Backup And Restore

Trust boundary: Postgres backup artifacts, backup credentials, restore drill jobs, and operator key storage.

Threats:

- Backup artifacts plus encryption keys being stored together.
- Backups silently failing or becoming unrestorable.
- Restore-drill jobs writing to production beyond the status row.
- Backup credentials leaking through logs or diagnostics.

Controls:

- Nightly management-plane backups cover Postgres only; Redis is treated as rebuildable queue state.
- Restore drills restore into an ephemeral sidecar Postgres and write only the drill result to production.
- Backup jobs use dedicated credential mounts and tmp scratch volumes.
- Secret rotation procedures keep Fernet/JWT keys outside backup artifacts and support multi-key rotation windows.

Review checks:

- Backup-related changes must preserve the separation between backup data, encryption keys, and S3 credentials.
- Restore-drill changes must prove production writes stay limited to drill status rows.
- Any new backup credential path must be covered by redaction and rotation docs.

## Supply Chain

Trust boundary: source repository, CI, Helm chart, container images, third-party charts, and Argo sources.

Threats:

- Unpinned or compromised images/charts changing runtime behavior.
- Generated manifests adding broad RBAC, host access, or new egress without review.
- Dependency updates bypassing route/proxy/security tests.
- CRD schema changes introducing privilege-expanding fields without controller enforcement.

Controls:

- CI runs Go tests, frontend type checks/lint, route-security checks, Helm lint/render, and chart render contract tests.
- Chart values centralize image registries, pull secrets, and third-party image overrides for air-gapped installs.
- ComponentBundle and GitOpsTarget references enforce version pins and same-namespace governed template/schema references.
- High-risk routes are tracked in `docs/security-sensitive-routes.json`.

Review checks:

- Image, chart, or Argo source changes must document pinning, registry/mirror behavior, and required RBAC.
- New CRD fields must update schema, deepcopy/status tests, controller validation, docs, and ownership contracts.
- New high-risk routes must update the route registry and tests in the same change.

## PR Threat-Model Checklist

Use this checklist for security-sensitive pull requests:

- Identify which shared assets and trust boundaries changed.
- State the new or changed attacker path and the control that blocks it.
- Confirm route auth, RBAC, API-token scope, CSRF, audit, and proxy/header behavior when routes changed.
- Confirm new secrets are hash-only, encrypted, or intentionally external, and that rotation/redaction paths are updated.
- Confirm CRD/Postgres/Argo ownership changes preserve a single durable source of truth.
- Confirm chart changes preserve default-deny NetworkPolicy, least-privilege containers, and production preflight posture.
