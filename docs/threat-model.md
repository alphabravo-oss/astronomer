# Threat Model

Date: 2026-06-12

Astronomer is a high-privilege Kubernetes management plane. The main security objective is to prevent a user, browser, token, proxy, or compromised component from gaining broader cluster access than intended.

## Shared Assets

| Asset | Protection goal |
|-------|-----------------|
| Browser session cookies and stream tickets | Prevent theft, replay, and leakage through URLs/logs. |
| API tokens and ArgoCD proxy tokens | Hash lookup material, encrypt reusable plaintext, scope use to intended routes/clusters. |
| Agent tunnel | Accept only registered agents and route traffic only to the owning cluster. |
| Adopted cluster Kubernetes API | Require Astronomer auth/RBAC before proxying; strip caller-controlled upstream credentials and impersonation headers. |
| Postgres | Durable source of truth for users, RBAC, audit, credentials, inventory, and operation rows. |
| ArgoCD cluster Secrets and ApplicationSets | Deployment reconciliation surface, not the product database. |

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
- Agent manifests support `viewer`, `operator`, and `admin` RBAC profiles.

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
