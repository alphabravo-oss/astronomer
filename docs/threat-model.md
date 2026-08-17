# Threat Model

Date: 2026-08-05

Astronomer is a high-privilege Kubernetes management plane. The main security objective is to prevent a user, browser, token, proxy, or compromised component from gaining broader cluster access than intended.

## Shared Assets

| Asset | Protection goal |
|-------|-----------------|
| Browser session cookies and stream tickets | Prevent theft, replay, and leakage through URLs/logs. |
| API tokens and source credentials | Hash lookup material where applicable, encrypt reusable plaintext, and scope use to intended routes/projects/providers. |
| Agent tunnel | Accept only registered agents and route traffic only to the owning cluster. |
| Adopted cluster Kubernetes API | Require Astronomer auth/RBAC before proxying; strip caller-controlled upstream credentials and impersonation headers. |
| Postgres | Durable source of truth for users, RBAC, audit, credentials, inventory, and operation rows. |
| Redis/asynq | Ephemeral queue and scheduling state; must not be the only durable record of an operator decision. |
| Management CRDs | Kubernetes-facing desired-state API for clusters, projects, and agent profiles. |
| Delivery intent and rollout records | PostgreSQL-owned project intent, immutable revisions, frozen placement, approval, audit, and generation fencing. |
| Downstream Flux resources and checkpoints | Agent-owned reconciliation state and prune evidence, never the product database or placement authority. |
| Management-plane Helm release | Deployment security boundary for network policy, pod security contexts, bootstrap credentials, ingress/TLS, and backup jobs. |
| Management backups | Durable copy of Postgres state; must remain encrypted/separated from encryption keys and periodically restore-tested. |
| Container images, Helm repos, and generated manifests | Supply-chain inputs that can change runtime permissions or code paths. |
| Optional Charlie product-agent boundary | Keep AI/model output non-authoritative; isolate the separate Charlie service and enforce every read/write through current product-owned policy. |

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
- No new browser stream URL may carry a JWT, refresh token, API token, or delivery-source credential.
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

## Optional Charlie SRE Assistant

Trust boundaries: browser to Astronomer API; Astronomer server/worker to the
private local Charlie Product Bridge; Charlie product agent to Astronomer's
private MCP listener; product agent to separately deployed Charlie Central.

Threats:

- Prompt injection or model output being treated as authorization.
- An enabled feature or permissive Charlie mode bypassing live Astronomer RBAC,
  resource scope, approval, safety, budget, or fencing checks.
- Charlie server/worker code bypassing the local product agent and calling
  Charlie Central directly.
- The product agent receiving a Kubernetes service-account token, Astronomer API
  token, kubeconfig, or downstream tunnel capability.
- Read capability output leaking Secrets, credentials, raw manifests, unbounded
  logs, prompts, evidence, or cross-user/cross-deployment content.
- Approval replay, argument substitution, stale mode/disclosure/fencing state,
  duplicate dispatch during failover, or retry after an ambiguous write.
- Event storms duplicating investigations or automation acting on a recovered
  condition.
- A product-agent compromise using broad egress or MCP access to move laterally.

Controls:

- `feature.charlie=false` is default and a fresh deployment constructs no
  runtime listener, workload, Service, or network path. Disabling an installed
  integration atomically closes write admission, cancels/drains registered
  writes, removes the agent workload and private network surface, and retains
  only owner-bound resume/secrets plus durable product metadata/audit. Product
  and central activation are both required; either disable wins locally.
- The sole runtime path is a fixed cluster-local mTLS Product Bridge with exact
  DNS/SPIFFE identities. Browser clients never receive bridge, central, MCP, or
  enrollment credentials; server/worker direct central egress is prohibited.
- An installed, non-emergency connection may retain a private configuration-only
  Product MCP listener while operational mode is disabled so signed catalog
  rediscovery cannot deadlock. That surface accepts only MCP initialization and
  `tools/list`; `tools/call`, event/trigger consumers, receipt processing, and
  write admission still require live authority. Feature disable, disconnect,
  installation incompleteness, and emergency stop remove the listener.
- The generic agent has `automountServiceAccountToken: false`, no Role/Binding,
  no Astronomer API credential, and exact NetworkPolicy paths only.
- MCP discovery is the complete allowlist. Schemas reject unknown/unbounded
  inputs; v1 omits destructive, irreversible, shell/exec, arbitrary HTTP/SQL,
  raw Kubernetes, Secret, and all downstream-cluster operations.
- Every call rechecks feature/connection/emergency state, least-authority mode,
  signed action and exact arguments, current user/service identity and RBAC,
  affected-resource scope, disclosure revision, approval/auto allowlist,
  safety/preconditions, budgets/cooldowns, idempotency receipt, and fencing
  epoch. Deny wins; model output grants nothing.
- Approval binds deployment/session/turn/action/capability/argument digest,
  approver, target authorization, expiry, and one-time consumption. Writes are
  durably audited and claimed before dispatch, then product-verified.
- Conversation/evidence content remains in Charlie. Astronomer stores bounded
  session/finding/trigger metadata and opaque hashed authorization references.
  Metrics/logs/audit/support bundles use fixed content-free fields.
- Trigger decisions and thresholds live in Astronomer, use a durable outbox,
  deduplicate by stable fingerprint, coalesce repeats, honor grace/cooldown, and
  create actionable findings when execution is not permitted.
- Charlie may read Astronomer-owned downstream-agent connection telemetry but
  no capability proxies through an agent tunnel or queries downstream
  Kubernetes. Instrumented tunnel-spy tests enforce the boundary.
- Optional management-cluster Kubernetes visibility is owned and executed by
  the Product MCP, not the product agent. Profile and content-scope changes
  lower verified authority to disabled, change the disclosure digest, and
  require signed installation-bound rediscovery plus independent Charlie and
  Astronomer acknowledgement. Secret values, downstream targets, exec, attach,
  port-forward, generic API proxying, and unrestricted selectors remain hard
  prohibitions.

Review checks:

- Any new Charlie capability must document effect, risk, exact target bounds,
  input/output limits, RBAC mapping, redaction, precondition, verification,
  idempotency, rollback, cooldown/budget, and downstream-access proof.
- Capability/disclosure changes require explicit administrator acknowledgement;
  auto allowlists do not carry forward implicitly.
- New Charlie routes require feature, auth, RBAC, rate/concurrency, audit, CSRF,
  security-inventory, and disabled-state no-I/O tests.
- Test read-only, approval, auto, emergency disable, identity/RBAC revocation,
  failover at every write boundary, ambiguous outcomes, multi-deployment
  isolation, and direct-egress/downstream-tunnel denial before release.
- Follow [Charlie operations and runbooks](charlie-operations.md) for incidents,
  rotation, rollback, disconnect, and stop conditions.

## Flux-native delivery

Trust boundaries: browser/API to the delivery control plane; resolver to Git,
OCI, and chart providers; management plane to the outbound cluster agent; agent
to the fixed local Flux APIs; Flux to the target Kubernetes API.

Threats:

- a mutable branch or tag changing after approval;
- cross-project source, bundle, target, deployment, or catalog access;
- selector drift changing a rollout's membership after it starts;
- source SSRF, redirect/DNS rebinding, oversized responses, credential leakage,
  or unverified artifacts;
- replayed/stale assignments, forged status, concurrent writers, or unsafe
  pruning after a disconnect;
- arbitrary Flux objects, cross-namespace references, remote bases, or service
  accounts escaping the intended namespace/scope boundary;
- controller-version drift, a fourth controller, or mutable controller images;
- model or automation output bypassing approval, RBAC, maintenance windows,
  failure budgets, or rollout fencing.

Controls:

- PostgreSQL owns sources, immutable bundle versions, frozen placement,
  rollouts, approvals, deployments, generations, fences, audit, and status.
- Every project-scoped route normalizes one authoritative project and checks
  dedicated delivery RBAC. Mutable lifecycle commands require current
  `If-Match` state. The catalog enforces project visibility for both list and
  object lookup.
- Source resolution pins an immutable commit or digest, verifies configured
  identity/signature policy, bounds redirects/pages/bytes, and rejects private,
  reserved, link-local, loopback, and rebinding destinations unless an explicit
  reviewed policy permits the exact host.
- Placement is deterministic and frozen before release. Strategies, cohorts,
  approvals, windows, budgets, retries, and rollback use durable leases and
  generation fences; a retry cannot silently widen membership.
- Agent snapshots are cluster-bound, ETagged, generation-monotonic, size/object
  bounded, and checkpointed. Stale/replayed input and ambiguous prune evidence
  fail closed. Disconnect retains the last accepted desired state.
- The agent accepts only typed source/renderer/scope variants and materializes
  deterministic objects in reserved project control namespaces. Namespace
  assignments use bounded Roles; platform assignments may bind only the fixed
  platform-applier ClusterRole. Cross-namespace references and remote
  Kustomize bases are disabled.
- Releases ship exactly the digest-pinned source, Kustomize, and Helm
  controllers from an authenticated Flux release, with fixed API versions,
  network policy, non-root security context, and no generic Flux UI/API.
- Status ingestion verifies cluster/session/generation ownership, normalizes
  bounded conditions/events, coalesces noise, and marks stale data explicitly.

Review checks:

- New source or renderer variants require a typed schema, immutable resolution,
  verification, egress policy, size/page bounds, redaction, and materializer
  negative tests.
- New rollout actions require RBAC, audit, idempotency, current-state CAS,
  failure/rollback semantics, and project-isolation tests.
- Changes to object materialization must prove namespace/scope confinement,
  stable naming, takeover refusal, complete checkpointing, and safe pruning.
- Flux/controller changes must update the signed release contract, compatibility
  matrix, air-gap inventory, provenance, and live version qualification.

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

Trust boundary: PostgreSQL, management CRDs, Redis, cluster agents, local Flux,
and target clusters each own different state.

Threats:

- Duplicate state owners racing and overwriting each other.
- Redis or downstream Flux being treated as the durable source for user decisions.
- CRD reconciliation broadening tenant scope beyond the selected project/cluster.
- Target-cluster cached inventory becoming stale but still shown as authoritative.

Controls:

- Postgres is the durable product source for users, RBAC, audit, inventory metadata, credentials, and operation history.
- Redis/asynq stores queue state only; durable operations are persisted in Postgres before execution.
- PostgreSQL owns delivery intent and rollout history; agents own materialization,
  while local Flux owns source and workload convergence for the last accepted generation.
- Management CRDs cover only clusters, projects, and agent profiles. Their
  controllers enforce ownership, finalizers, and same-namespace profile
  references before writing product rows or status.
- Target clusters remain source of truth for live Kubernetes objects; mirrored/cache views must surface stale or degraded collection state.

Review checks:

- Any new durable decision must land in Postgres or a Kubernetes CRD with status/finalizer semantics, not only Redis or process memory.
- Any new downstream writer must enforce Astronomer ownership metadata and refuse to overwrite unowned resources.
- Any selector that can target clusters must prove project authorization,
  deterministic evaluation, frozen rollout membership, and bounded preview.

## Built-in platform bundles

Trust boundary: the reviewed release bundle catalog through the normal delivery
control plane to platform-scoped resources in an adopted cluster.

Threats include a retagged chart/image, a built-in escaping its reviewed
platform scope, user-owned object takeover, or baseline failure blocking cluster
registration without useful diagnostics.

Built-ins use immutable chart and image digests, the same source verification,
placement, rollout, assignment, materialization, status, and audit paths as user
delivery, and stable release-derived identities. They bind only the fixed
platform-applier role and refuse unowned objects. Compatibility requirements
and images are part of the signed release manifest and air-gap kit.

New built-ins must declare source/chart/image digests, namespace, release name,
scope, values, health behavior, Kubernetes bounds, required capabilities,
ownership/takeover behavior, rollback, and connected plus disconnected tests.

## Management-Plane Deployment

Trust boundary: Kubernetes namespace running Astronomer itself.

Threats:

- Lateral movement from one management-plane pod to another through open namespace networking.
- Hook or backup jobs running with weaker security contexts than the steady-state deployments.
- Ingress/Gateway, source resolution, external DB/Redis, and Kubernetes API egress being broader than intended.
- Bootstrap credentials or generated secrets becoming long-lived unknowns.
- Helm values or delivery credentials leaking through rendered manifests,
  rollout events, API responses, logs, support bundles, or status payloads.

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

Trust boundary: source repository, CI, Helm chart, container images, upstream
Flux release, delivery artifacts, and third-party charts.

Threats:

- Unpinned or compromised images/charts changing runtime behavior.
- Generated manifests adding broad RBAC, host access, or new egress without review.
- Dependency updates bypassing route/proxy/security tests.
- CRD schema changes introducing privilege-expanding fields without controller enforcement.

Controls:

- CI runs Go tests, frontend type checks/lint, route-security checks, Helm lint/render, and chart render contract tests.
- Chart values centralize image registries, pull secrets, and third-party image overrides for air-gapped installs.
- The signed release manifest binds the management chart/images, agent, exact
  Flux distribution, built-in bundles, SBOMs, provenance, and air-gap inventory.
- Delivery bundle versions resolve to immutable commits/digests and preserve
  verification evidence before rollout.
- High-risk routes are tracked in `docs/security-sensitive-routes.json`.

Review checks:

- Image, chart, Flux, or delivery-source changes must document pinning, verification, registry/mirror behavior, and required RBAC.
- New CRD fields must update schema, deepcopy/status tests, controller validation, docs, and ownership contracts.
- New high-risk routes must update the route registry and tests in the same change.

## PR Threat-Model Checklist

Use this checklist for security-sensitive pull requests:

- Identify which shared assets and trust boundaries changed.
- State the new or changed attacker path and the control that blocks it.
- Confirm route auth, RBAC, API-token scope, CSRF, audit, and proxy/header behavior when routes changed.
- Confirm new secrets are hash-only, encrypted, or intentionally external, and that rotation/redaction paths are updated.
- Confirm CRD/PostgreSQL/Flux ownership changes preserve a single durable source of truth.
- Confirm chart changes preserve default-deny NetworkPolicy, least-privilege containers, and production preflight posture.
