# Charlie Agent Integration Plan for Astronomer

> **Status:** Implementation and release-qualification record. Audited against
> the v1 code and tests on 2026-08-05. The fail-closed, reconciliation, and local
> emergency-deny decisions below are part of v1. Unchecked boxes are deliberate
> release gates, not implied implementation: the remaining server test/lifecycle
> gaps are recorded in section 5.1, while A10/A11 acceptance and A14 require the
> integrated UI or a live deployment.
>
> **Implementation branch:** `feat/charlie-core-integration`, audited through
> schema version 149 on 2026-08-05.
>
> **Cross-repository dependency:**
> `../../charlie/docs/product-agent-integration-platform-plan.md` defines the
> generic onboarding, Product Bridge, agent HA, isolation, authority-mode,
> finding, capability-disclosure, and artifact contracts. If that contract
> changes, update this plan before implementation.
>
> **Executor contract:** Complete tasks in dependency order. Check a box only
> after its tests pass and evidence is recorded. Never copy credentials, private
> keys, prompts, model output, logs containing sensitive content, or Kubernetes
> Secret values into source, fixtures, audit, screenshots, or this document.

## 1. Outcome and architecture

Astronomer gains a native Charlie experience while Charlie remains a completely
separate service. The **only Charlie workload installed in Astronomer is the
generic Charlie agent**. All runtime communication with central Charlie passes
through that local agent.

```text
Astronomer browser
  -> Astronomer API (normal session, CSRF, ownership, RBAC)
    -> private mTLS Product Bridge on local Charlie agent
      -> separately deployed Charlie central
      -> elected local Charlie agent worker
        -> private mTLS Astronomer MCP server
          -> Astronomer management-plane services and bounded DB/server telemetry
```

### Terminology

- **Charlie product agent:** the generic Charlie bridge installed beside
  Astronomer. It connects to Charlie central and invokes the private
  Astronomer-owned MCP catalog.
- **Astronomer cluster agent:** Astronomer's existing agent installed in each
  downstream cluster. Its connection is part of Astronomer's control-plane
  health, but it is not a Charlie execution target in v1.

### Non-negotiable boundaries

- [x] **A-001** Never install Charlie central, Charlie PostgreSQL, Charlie RustFS,
  or Charlie's OCI service in the Astronomer chart.
- [x] **A-002** Install only the generic Charlie agent chart and image.
- [x] **A-003** Do not add an independent Astronomer-to-Charlie central runtime
  client or central runtime credential.
- [x] **A-004** Route chat, mode, sessions, events, streaming, approvals, status,
  and investigations through the local Product Bridge.
- [x] **A-005** Block Astronomer server/worker direct egress to Charlie central;
  allow only the Charlie agent and Kubernetes artifact pull path.
- [x] **A-006** Give the Charlie agent no Kubernetes service-account token or
  Kubernetes RBAC.
- [x] **A-007** Give Charlie no Astronomer API token, kubeconfig, downstream
  cluster token, provider credential, or database credential.
- [x] **A-008** Let Astronomer remain authoritative for product data, actor
  identity, RBAC, UI visibility, event selection, redaction, and business rules.
- [x] **A-009** Let Charlie remain authoritative for models, RAG, routes,
  conversations, orchestration, agent policy mode, approvals, durable central
  commands, model usage, and central audit.
- [x] **A-010** Treat product input and model output as untrusted data; neither
  can grant authority.
- [x] **A-011** Limit v1 SRE reach to the Astronomer installation, the Kubernetes
  management cluster that hosts it, and Astronomer-owned downstream-agent
  connection metadata.
- [x] **A-012** Never let a Charlie capability proxy through an Astronomer cluster
  agent tunnel or otherwise query or mutate a downstream cluster.
- [x] **A-013** Keep Charlie code paths, routes, UI, workers, trigger dispatch,
  MCP listener, agent installation, Secrets, and network access dormant unless
  `feature.charlie` is enabled and an administrator explicitly activates a valid
  connection.
- [x] **A-014** Enforce every action by the strict intersection of feature state,
  Charlie central mode, capability effect/risk, live Astronomer RBAC, exact
  management-resource scope, approval state, automation allowlist, and safety
  limits; any denial wins.
- [x] **A-015** Emit actionable Astronomer alerts/findings when Charlie diagnoses
  an issue but mode, approval, authorization, or safety policy prevents execution.

## 2. Current state and plan scope

Facts the implementer must confirm before changing code:

- Astronomer is a Go control plane with PostgreSQL/sqlc, Redis/Asynq, Chi HTTP
  routing, generated OpenAPI clients, audit/event systems, and an existing
  management-cluster Kubernetes client.
- The React 19/Vite/TanStack frontend uses file routes, TanStack Query, the
  existing design system, and deep-linked settings pages.
- Canonical resources and verbs live in `internal/rbac/types.go`; server routes
  combine authentication, API-token scope, RBAC, cluster scope, CSRF, and audit.
- Reusable integration credentials are encrypted with `auth.Encryptor`; bearer
  tokens are stored hash-only; secret-looking columns must be added to
  `docs/secret-column-inventory.md` and its guard tests.
- The Charlie integration migration series is `147` through `149`: the base
  schema, trust/release hardening, and signed immutable artifact references.
- Astronomer already owns installation readiness, cluster-agent connection
  state, metrics, logs, audit, self-management GitOps, backups, task outbox, and
  managed-cluster tunnels. Charlie capabilities must wrap bounded existing
  services rather than implement parallel Kubernetes clients or reuse broad
  tunnel/proxy handlers.
- Argo CD is the desired-state owner for managed platform components. The local
  Charlie agent must likewise be represented as an Argo-managed management-plane
  application.

### In scope

- Onboarding-package import and verification.
- Local mutual-TLS identity and certificate lifecycle.
- Argo-managed local agent installation and Charlie OCI pulls.
- Local Product Bridge client and browser-facing proxy APIs.
- Astronomer-owned MCP server and capability catalog.
- User/service authorization, delegation, approvals, action idempotency, and
  audit.
- Global chat, investigations, automation triggers, admin UI, diagnostics,
  observability, air-gap support, and complete validation.
- Feature-gated isolation, emergency disable, actionable findings/alerts,
  destructive-action protection, and content-safe operational logging.
- Management-cluster SRE diagnosis for the Astronomer installation itself.
- Astronomer-owned metadata about downstream cluster-agent connections,
  heartbeats, errors, upgrades, ingestion, commands, and fleet-wide patterns.

### Explicitly out of scope

- Deploying or configuring central Charlie models/providers/RAG from
  Astronomer.
- Duplicating Charlie conversation contents in Astronomer PostgreSQL.
- Browser calls to the local agent or Charlie central.
- Direct Kubernetes access from the Charlie agent.
- Arbitrary shell, exec, apply, proxy, Secret reads, or destructive lifecycle
  operations as Charlie tools.
- Listing downstream pods, workloads, nodes, events, namespaces, logs, or other
  Kubernetes resources.
- Querying downstream Kubernetes APIs or using an Astronomer cluster-agent
  connection as a transport for Charlie.
- Installing, upgrading, restarting, deleting, or reconfiguring downstream
  components or Astronomer cluster agents.
- Rotating downstream credentials or triggering downstream Helm, Argo, backup,
  security, or fleet operations.
- Multiple active Charlie deployments for one Astronomer installation in v1.

## 3. Fixed product decisions

- One active Charlie connection and one logical Charlie agent per Astronomer
  installation.
- The onboarding package chooses two or more agent replicas. They operate with
  one fenced leader and standby replicas; any healthy replica may proxy
  idempotent bridge traffic.
- The Charlie product agent runs in a dedicated management-cluster namespace with
  exact NetworkPolicy and mTLS paths to Astronomer MCP and Charlie central.
- Product Bridge and MCP both require distinct mutual-TLS identities.
- User-created chat is private to its initiating user.
- Event-triggered investigations are shared with users who have Charlie read
  permission and read permission for every affected resource.
- Chat runs under the current user's live RBAC.
- Background work runs under a dedicated, explicitly scoped Charlie service
  identity.
- Approval requires both `charlie:approve` and the underlying target permission.
- Mode is changed from Astronomer through the local agent, but Charlie central's
  readback is authoritative.
- Default automation rules cover high/critical alerts, delayed agent
  disconnects, and failed operational workflows.
- Agent-fleet trigger thresholds are configurable Astronomer rules; Charlie does
  not hardcode the grace period, flap window, fleet percentage, or version policy.
- `disabled` starts no session, trigger, finding work, central claim, or MCP call;
  health/configuration status remains available to administrators.
- `read_only` permits authorized disclosed reads and creates actionable findings;
  it never dispatches a write even if a user asks for one.
- `approval` requires one exact, expiring user approval plus the approver's live
  target permission immediately before dispatch.
- `auto` permits only explicitly reviewed auto-eligible capability/scope pairs
  under the automation service identity, action budgets, cooldowns, preconditions,
  and circuit breakers. Destructive or irreversible capability disclosures are
  rejected entirely in v1.
- Mode is a ceiling, not a grant; Astronomer RBAC and MCP validation always apply.
- An Astronomer cluster ID may identify an agent connection record, but it never
  authorizes or addresses a downstream Kubernetes request in v1.
- Charlie supplies its agent chart/image through Charlie OCI; external
  registries are optional mirrors only.
- The signed onboarding package must name the Charlie Central origin's exact
  `charlie/agent` image and `charlie/agent-chart` Helm repositories by immutable
  digest. Astronomer rejects a second artifact host even when the package is
  otherwise signed. An isolated installation issues its package from the
  separately deployed internal Charlie origin and its seeded Charlie OCI.

### Authority decision

```text
allowed = feature enabled
       AND connection active
       AND Charlie mode permits capability effect
       AND capability disclosure/risk permits it
       AND live user or automation identity RBAC permits exact target
       AND exact approval or auto allowlist permits it
       AND safety preconditions/budget/cooldown/circuit breaker permit it
       AND fencing/idempotency checks permit it
```

Modes and Astronomer roles are separate controls. `charlie:create/read` allows a
user to use and inspect authorized Charlie surfaces; `charlie:approve` allows an
otherwise-authorized user to decide an exact waiting action; `charlie:manage`
controls connection/mode/policy administration; the hidden automation service
identity receives only named capabilities and scopes. None of these permissions
alone grants the underlying management-resource action, and deny always wins.

### Isolation and actionable-alert invariant

- [x] The feature gate prevents Charlie route, UI, worker, trigger, finding,
  listener, and installation initialization when unavailable.
- [x] An installed disabled integration exposes authenticated health,
  configuration, and MCP catalog discovery only; `tools/call` and all runtime
  work remain unavailable.
- [x] `read_only`, `approval`, and `auto` are ceilings over the same live
  Astronomer RBAC/resource-scope checks, never privilege-bearing roles.
- [x] Every write is non-destructive, action-ID idempotent, preconditioned,
  bounded, audited, and product-post-verified; missing disclosure fails closed.
- [x] When execution is not permitted, a material diagnosis becomes a
  deduplicated actionable finding/alert with evidence, impact, a safe next step,
  verification, exact denial reason, approval link when eligible, and deep link.
- [x] Finding acknowledgement never counts as action approval, and a mode change
  never grants underlying target permission.

## 4. Data model and public interfaces

### 4.1 Migration 147

Create `147_charlie_agent_integration.up.sql` and its down migration. Use UUID
primary keys, bounded enums/check constraints, foreign keys, timestamps, and
indexes that match query paths.

#### `charlie_connections` — singleton active integration

- `id`, installation UUID, Charlie product/deployment/route identifiers;
- Charlie base URL and public CA/signing fingerprint metadata;
- onboarding schema/API/agent/chart/image versions and immutable digests;
- local logical agent ID and Product Bridge/MCP service names;
- encrypted local CA/private-key and Astronomer-side TLS client/server material,
  or approved external Secret references;
- requested mode, verified central mode/revision, local emergency-disable latch
  with actor/timestamp, disclosure digest, leader/epoch, health state,
  last verified/connected/rotated timestamps;
- onboarding package ID, package digest, unique consumption state
  `validated|secrets_pending|secrets_written|consumed|active|failed`, deterministic
  Secret names/hashes, last error, and reconciliation timestamps;
- no central runtime product-client/product-agent token columns.

#### `charlie_sessions` — ownership and correlation only

- local ID, Charlie session ID, stable client session ID;
- owner user ID nullable for automation;
- source `user|event`, visibility `private|incident`;
- intent, resource-scope summary, management-component IDs and downstream-agent
  connection-record IDs where applicable;
- state, last event ID, central revision, created/updated/completed timestamps;
- no prompt, response, evidence, reasoning, tool arguments, or tool results.

Store affected-resource identities in a constrained child table keyed by session
and finding, rather than only in an opaque summary, so every list/get/history
request can reauthorize every resource efficiently.

#### `charlie_delegations`

- hash-only opaque authorization reference;
- session ID, principal type `user|service`, principal ID;
- issue/expiry/revoked timestamps and non-secret prefix;
- no RBAC snapshot: every action uses live bindings.

#### `charlie_action_receipts`

- Charlie action ID unique, session/turn IDs, capability, effect, argument
  digest, fencing epoch, product idempotency key;
- lease owner/expiry and attempt, state including `ambiguous`, result
  digest/status only, audit correlation ID, timestamps;
- no raw arguments or response body.

Every write adapter must pass its receipt operation ID into an idempotent
underlying operation. A crash after dispatch but before receipt completion is
reconciled from the postcondition and operation ID; an ambiguous write is never
blindly retried.

#### `charlie_trigger_rules` and `charlie_trigger_events`

- rule type/category, enabled, minimum severity, management-component or
  agent-metadata selectors, thresholds/windows, cooldown, service identity, mode
  ceiling, timestamps;
- event source/type/resource/fingerprint, redacted summary metadata, state,
  session ID, attempt/backoff/dead-letter fields;
- unique active fingerprint constraint for durable deduplication.

#### `charlie_findings` — bounded product notification state

- Charlie finding ID unique, session/investigation and affected-resource
  correlation, severity, status, source, effective mode, and execution-block code;
- redacted bounded title/summary, recommended action label, risk/impact,
  verification summary, expiry, repeat count, acknowledged/dismissed/resolved
  actor/timestamps, and existing Astronomer alert/event correlation;
- exact arguments, complete evidence, citations, and detailed recommendation are
  fetched from Charlie through the bridge after live authorization rather than
  duplicated locally;
- no prompt, model reasoning, raw tool result, credential, Secret value, or
  unredacted log content.

### 4.2 Browser-facing Astronomer APIs

Add the following to `docs/openapi.yaml`, generate the Go SDK, and use the
existing error envelope and route conventions.

#### Administration — `charlie:manage`

- `GET /api/v1/admin/charlie/status/`
- `POST /api/v1/admin/charlie/onboarding/validate/`
- `POST /api/v1/admin/charlie/onboarding/consume/`
- `POST /api/v1/admin/charlie/agent/install/`
- `POST /api/v1/admin/charlie/agent/upgrade/`
- `POST /api/v1/admin/charlie/agent/rollback/`
- `POST /api/v1/admin/charlie/agent/rotate/`
- `POST /api/v1/admin/charlie/agent/uninstall/`
- `POST /api/v1/admin/charlie/disconnect/`
- `GET|POST /api/v1/admin/charlie/trigger-rules/`
- `PATCH|DELETE /api/v1/admin/charlie/trigger-rules/{id}/`
- `PATCH /api/v1/admin/charlie/mode/`
- `GET|PUT /api/v1/admin/charlie/access/`
- `POST /api/v1/admin/charlie/diagnostics/run/`

#### User runtime — normal session plus Charlie RBAC

- `GET|POST /api/v1/charlie/sessions/`
- `GET /api/v1/charlie/sessions/{id}/`
- `POST /api/v1/charlie/sessions/{id}/messages/`
- `GET /api/v1/charlie/sessions/{id}/events/`
- `GET /api/v1/charlie/sessions/{id}/history/`
- `POST /api/v1/charlie/sessions/{id}/abort/`
- `GET /api/v1/charlie/context/search/`
- `GET /api/v1/charlie/operations/{operation_id}`
- `GET /api/v1/charlie/approvals/`
- `POST /api/v1/charlie/approvals/{id}/decision/`
- `GET /api/v1/charlie/findings/`
- `GET /api/v1/charlie/findings/{id}/`
- `POST /api/v1/charlie/findings/{id}/acknowledge/`
- `POST /api/v1/charlie/findings/{id}/dismiss/`
- `POST /api/v1/charlie/findings/{id}/resolve/`

The browser never receives a central URL credential, local agent certificate,
MCP certificate, enrollment secret, registry password, or raw authorization
reference.

### 4.3 Private interfaces

- Product Bridge:
  `https://charlie-agent-bridge.<charlie-namespace>.svc:7443/bridge/v1` using
  Astronomer client mTLS.
- Astronomer MCP:
  `https://astronomer-charlie-mcp.<astronomer-namespace>.svc:7444/mcp` using agent
  client mTLS.
- Neither private service is routed by Ingress/Gateway/HTTPRoute.

## 5. Phased implementation checklist

### 5.1 Audit evidence and remaining v1 gates

The 2026-08-05 source audit used the following evidence rather than inferring
completion from this checklist:

- Contract, fail-closed transport, replay, bounded response, and approval binding:
  `internal/charlie/contract/*_test.go`.
- Onboarding, trust, installation, mode, availability, and feature isolation:
  `internal/charlie/{onboarding_consumer,trust,agent_installation,admin,gate,availability}_test.go`.
- MCP catalog, adapters, live authority, action safety, audit, and concurrent
  replay protection: `internal/charlie/{mcp,catalog,*_capability_adapter,live_authority,action_*}_test.go`.
- Sessions, approvals, triggers, findings, metrics, and operations:
  `internal/charlie/{sessions,session_access,approval_access,trigger_*,finding_*,metrics,observability}_test.go`,
  `internal/handler/charlie_*_test.go`, and
  `internal/worker/tasks/charlie_trigger_test.go`.
- Public route, schema, security, deployment, and generated-client coverage:
  `docs/openapi.yaml`, `docs/routes.json`,
  `docs/security-sensitive-routes.json`, route/security tests, and
  `deploy/charlie_mcp_render_test.go`.

Remaining non-UI/non-live gates are intentionally unchecked below:

1. Run `make sqlc-check` against a committed baseline; its final
   `git diff --exit-code` is not meaningful while this implementation is an
   uncommitted working-tree change. `make check-migrations` and sqlc generation
   already pass.
2. Complete the exhaustive per-capability and per-write scenario matrices.
3. Exercise every operational runbook with a deterministic test or game-day.

### Phase A1 — Contract pin and verification baseline

**Depends on:** Charlie C1.
**Why first:** Astronomer must not implement against an inferred or moving agent
contract.

- [x] **A1-001** Pin the reviewed Charlie Product Bridge schema, onboarding
  schema, agent protocol version, chart version, and minimum central API version.
- [x] **A1-002** Add a contract checksum and CI drift check.
- [x] **A1-003** Add generated Product Bridge client types under one internal
  package; do not hand-maintain duplicate wire structs.
- [x] **A1-004** Document that this client targets only the local agent service.
- [x] **A1-005** Add a fake Product Bridge server supporting deterministic
  health, sessions, SSE replay, approvals, findings, mode, disabled isolation,
  central outage, and failover.
- [x] **A1-006** Add `feature.charlie`, default `false`, to backend and frontend
  feature-gate inventories.
- [x] **A1-007** Add Charlie route entries to generated route/security inventories
  before implementing handlers.
- [x] **A1-008** Make every Charlie handler, worker dispatch, scheduler,
  frontend route/launcher, and internal client conditional on the backend-owned
  feature gate; do not rely on UI hiding. The generic task handler may remain
  inertly registered, but no dispatcher is configured and no task is enqueued or
  claimed while Charlie is inactive.
- [x] **A1-009** Define two states: feature unavailable/disabled and feature
  available but integration not yet activated. Both reject runtime work and
  disclose no product evidence.
- [x] **A1-010** Reconcile feature changes into the installed runtime: disable
  closes/drains write admission, stops the listener/agent workload, removes the
  MCP Service and exact product access policy, and retains only owner-bound
  secrets/resume state plus durable audit/history. Re-enable restores the
  captured runtime in disabled/installing mode without restoring authority.

**Verify**

- [x] `make verify` exits 0 with the pinned contract.
- [x] Generated contract drift intentionally fails CI.
- [x] No package outside the internal bridge client imports a central Charlie
  transport.
- [x] With `feature.charlie=false`, route gating, background worker dispatch,
  database-write guards, listener/workload teardown, and absence of Charlie
  Service/NetworkPolicy ports prove zero Charlie runtime activation. Secure
  status, emergency-disable, uninstall, and disconnect administration remains.

### Phase A2 — Persistence, secret inventory, and RBAC

**Depends on:** A1.

- [x] **A2-001** Create migration 147 tables, checks, indexes, and down migration
  exactly as section 4.1 defines.
- [x] **A2-002** Add sqlc queries for singleton connection CAS, session ownership,
  delegation lookup/revoke, action receipt claim/complete, rules, trigger dedupe,
  findings/lifecycle dedupe, retry, and dead-letter transitions.
- [x] **A2-003** Add all secret-looking columns to
  `docs/secret-column-inventory.md` with encrypted/hash-only classification.
- [x] **A2-004** Encrypt local reusable TLS key material with `auth.Encryptor` or
  persist only an approved external Secret reference.
- [x] **A2-005** Refuse onboarding when the encryptor or configured Secret target
  is unavailable.
- [x] **A2-006** Add canonical RBAC resource `charlie`.
- [x] **A2-007** Add canonical verb `approve`.
- [x] **A2-008** Update built-in role catalogs and docs:
  - viewer/operator: `charlie:create,read` subject to underlying resource access;
  - explicit Charlie approver: `charlie:approve` plus underlying action RBAC;
  - superuser: `charlie:manage` and all other Charlie permissions;
  - no default automation write grant.
- [x] **A2-009** Create one hidden service-user identity for Charlie automation,
  following existing machine-identity conventions.
- [x] **A2-010** Require explicit management-plane capability grants and
  agent-metadata read grants for that identity; reject wildcard default grants
  and every downstream-resource grant in v1.
- [x] **A2-011** Add cache invalidation when Charlie-related bindings change.
- [x] **A2-012** Add audit resource/action vocabulary for connection, certificate,
  agent, mode, session, trigger, approval, MCP decision, and disconnect events.

**Verify**

- [x] Fresh migration, upgrade, down/up, migration safety, sqlc generation, and
  secret-column inventory tests pass.
- [x] RBAC table tests cover superuser, viewer, scoped operator, approver without
  target access, target operator without approve, service identity, and revoked
  bindings.
- [ ] `make check-migrations && make sqlc-check` exits 0.

### Phase A3 — Onboarding verification and local trust

**Depends on:** A2, Charlie C2/C5/C6.

- [x] **A3-001** Accept the onboarding package only through an authenticated,
  CSRF-protected, `charlie:manage` endpoint.
- [x] **A3-002** Enforce content type, maximum upload size, JSON schema, package
  expiry, package ID uniqueness, supported contract versions, and exact product
  slug `astronomer`.
- [x] **A3-003** Verify canonical bytes, Ed25519 signature, signing-key ID, and
  operator-confirmed Charlie signing fingerprint.
- [x] **A3-004** Verify central URL form, CA chain, route/deployment tuple, chart
  digest, image digest, and credential purposes without calling central Charlie
  from the Astronomer backend.
- [x] **A3-005** Reject packages containing a configuration-admin, provider,
  product-client, or product-agent credential.
- [x] **A3-006** Create an installation-local CA using approved algorithms.
- [x] **A3-007** Issue separate 90-day certificates for Astronomer bridge client,
  agent bridge server, Astronomer MCP server, and agent MCP client.
- [x] **A3-008** Use client/server EKUs and exact service DNS SANs; do not issue a
  broad wildcard certificate.
- [x] **A3-009** Encrypt Astronomer-side CA/private keys immediately and write
  agent-side material directly to Kubernetes Secrets.
- [x] **A3-010** Use a 24-hour dual-trust overlap for certificate rotation.
- [x] **A3-011** Never return private key or onboarding secret content from read
  APIs, logs, audit, diagnostics, or support bundles.
- [x] **A3-012** Mark a package consumed only after every DB and Kubernetes Secret
  write succeeds; otherwise roll back and permit safe retry.
- [x] **A3-013** Detect a repeated package ID and return idempotent status without
  recreating identities or Secrets.

**Verify**

- [x] Valid, expired, tampered, wrong-product, wrong-fingerprint, unsupported,
  oversized, replayed, and secret-overprivileged package tests pass.
- [x] PostgreSQL, logs, audit, support bundle, and API serialization scans find no
  fixture plaintext secret/private key.
- [x] Certificate tests verify SANs, EKUs, expiry, rotation overlap, and rejection
  after old trust expires.

### Phase A4 — Argo-managed agent installation

**Depends on:** A3, Charlie C5/C6.

- [x] **A4-001** Add a management-plane Argo repository Secret for Charlie OCI
  using only the deployment artifact-pull credential.
- [x] **A4-002** Add a Kubernetes Docker config Secret for the agent image pull.
- [x] **A4-003** Add the enrollment Secret and distinct bridge/MCP TLS Secrets.
- [x] **A4-004** Label and owner-mark generated Secrets without putting secret
  data in labels, annotations, CRDs, or Argo Application fields.
- [x] **A4-005** Create one Argo Application named from the Astronomer installation
  UUID, targeting the management cluster and a dedicated configurable Charlie
  namespace (default `astronomer-charlie`).
- [x] **A4-006** Source the generic agent chart from Charlie OCI by immutable
  digest/version and configure the image by immutable digest.
- [x] **A4-007** Pass stable logical agent ID, central URL/CA, exact MCP host,
  existing Secret names, proxy, and network destinations as non-secret values.
- [x] **A4-008** Require the signed package's replica count (minimum two),
  anti-affinity, PodDisruptionBudget, no service-account token, and no RBAC
  resources.
- [x] **A4-008a** Create the dedicated namespace and default-deny ingress/egress;
  permit only DNS, Charlie central/OCI/proxy destinations, Astronomer-to-bridge
  mTLS, and agent-to-Astronomer-MCP mTLS.
- [x] **A4-009** Wait for every expected bridge-ready replica, central enrollment,
  leader election, standby visibility, and compatible protocol versions.
- [x] **A4-010** Run Product Bridge status, central health through the bridge, and
  OCI digest diagnostics before declaring installation complete.
- [x] **A4-011** Implement explicit upgrade by new reviewed package/digest; never
  follow a mutable latest tag.
- [x] **A4-012** Implement rollback to the prior retained chart/image digest while
  preserving stable logical agent and local trust identities.
- [x] **A4-013** Rotate enrollment/artifact credentials with Secret checksum
  rollout and verified old-credential revocation.
- [x] **A4-014** Uninstall in order: set Charlie mode disabled through bridge,
  stop trigger dispatch, abort/settle local streams, delete Argo Application,
  remove agent Secrets/repository credentials, retain non-secret audit/session
  metadata.
- [x] **A4-015** Make full disconnect distinct from temporary uninstall and
  require explicit destructive confirmation.

**Verify**

- [x] Helm/Argo render tests find no Charlie central component, Role,
  RoleBinding, service-account token mount, mutable image, or inline secret.
- [x] Fresh install, retry after partial failure, upgrade, rollback, rotation,
  uninstall, and reconnect tests pass.
- [x] Argo drift correction restores agent resources without overwriting
  operator-owned unrelated resources.

### Phase A5 — Product Bridge client and runtime proxy

**Depends on:** A1, A3, A4.

- [x] **A5-001** Build one pooled mTLS client targeting only the private agent
  ClusterIP DNS name.
- [x] **A5-001a** Construct no bridge client and start no runtime proxy, stream,
  trigger, finding consumer, or retry loop until the feature is enabled and the
  connection is explicitly active.
- [x] **A5-002** Verify the agent server certificate chain, EKU, exact DNS SAN,
  and installation identity.
- [x] **A5-003** Use bounded connect/header/body/idle/turn timeouts.
- [x] **A5-004** Add retry only for idempotent calls or calls carrying stable
  idempotency IDs.
- [x] **A5-005** Add circuit breaking that degrades Charlie surfaces without
  degrading core Astronomer readiness.
- [x] **A5-006** Preserve stable client session/message IDs across browser,
  server, worker, agent, and central retries.
- [x] **A5-007** Proxy SSE with `Last-Event-ID`, heartbeats, client cancellation,
  bounded buffering, and cleanup on disconnect.
- [x] **A5-008** Persist the latest acknowledged central event ID only after it is
  safely forwarded/processed.
- [x] **A5-009** Reconnect across agent replica/leader changes without duplicating
  browser-visible events.
- [x] **A5-010** Map bridge errors to stable Astronomer API errors without
  exposing internal URLs, certificate detail, or central bodies.
- [x] **A5-011** Add content-free metrics for calls, latency, failures, circuit
  state, SSE connections, reconnects, and leader changes.
- [x] **A5-012** Prove no code path constructs a direct central Charlie request.

**Verify**

- [x] Fake bridge tests cover success, timeout, cancellation, central outage,
  stale mode, invalid TLS, replica switch, stream replay, malformed frames, and
  oversized responses.
- [x] A network test blocks direct central egress from server/worker while the
  complete runtime proxy suite passes.

### Phase A6 — Private Astronomer MCP server and action guard

**Depends on:** A2, A3, A5, Charlie C3/C3A/C4.

- [x] **A6-001** Add a dedicated private TLS listener on port 7444 and an
  installer-owned ClusterIP Service; do not attach it to public routers or the
  default chart Service/NetworkPolicy scaffolding.
- [x] **A6-001a** Bind/advertise MCP only while the feature and connection are
  active; on emergency disable reject discovery/calls before resolving evidence
  or authorization.
- [x] **A6-002** Require the agent MCP client certificate and exact identity.
- [x] **A6-003** Reject plaintext, public-session cookies, Astronomer API tokens,
  and any certificate not issued for agent MCP client use.
- [x] **A6-004** Implement MCP initialize, initialized notification semantics,
  tools/list, tools/call, deterministic `listChanged: false` disclosure, and
  bounded structured errors. Catalog changes require a reviewed deployment and
  disclosure acknowledgement rather than an in-place authority expansion.
- [x] **A6-005** Generate a deterministic disclosure digest from names,
  descriptions, schemas, effect/risk, auto eligibility, target bounds,
  preconditions, impact, verification, reversibility/rollback, and version.
- [x] **A6-006** Validate Charlie's action signature, deployment/session/turn/
  action correlation, exact argument digest, fencing epoch, and expiry.
- [x] **A6-007** Require `Idempotency-Key` to equal Charlie action ID.
- [x] **A6-008** Atomically claim an action receipt before executing a write.
- [x] **A6-009** Return the prior bounded result/status for an exact replay and
  reject argument-digest conflicts.
- [x] **A6-010** Resolve the opaque authorization reference to a live delegation.
- [x] **A6-011** Load current user/service identity and current RBAC bindings for
  every call; never authorize from session context snapshots.
- [x] **A6-012** Recheck installation, management namespace/component, and named
  agent-connection-record scope; reject any downstream-resource target.
- [x] **A6-013** Require the automation identity's live grant for background work.
- [x] **A6-014** Require both approver permission and target permission for
  approval-mode writes.
- [x] **A6-015** Reject revoked/expired delegation, user, service identity,
  binding, session, action, mode revision, disclosure digest, or fencing epoch.
- [x] **A6-016** Audit proposed, denied, approved, dispatched, succeeded, failed,
  replayed, and fenced actions with content-free digests.
- [x] **A6-017** Evaluate in deny-first order: feature/connection enabled, mode,
  disclosure revision, capability effect/risk, delegation, live RBAC, exact scope,
  approval, automation allowlist, action budget/cooldown, preconditions, circuit
  breaker, fencing, and idempotency.
- [x] **A6-018** Re-read mode, approval, delegation/RBAC, preconditions, and fencing
  immediately before the side effect; a disable/revoke/change while waiting wins.
- [x] **A6-018a** Register the full write pipeline behind a shared fail-closed
  admission/cancellation fence. Feature/emergency disable closes admission,
  cancels cooperative executors, waits boundedly, and returns explicit drain
  state rather than reporting completion while a side effect can newly start.
- [x] **A6-019** Mark destructive/irreversible capabilities non-auto-eligible and
  omit all such v1 operations from discovery.

**Verify**

- [x] MCP protocol and schema conformance tests pass.
- [x] An exhaustive authorization matrix covers user/service identities,
  management components, downstream-agent metadata records, each Charlie mode,
  approval intersection, disclosure drift, expiry, and fencing.
- [x] Concurrent duplicate writes execute the underlying operation exactly once.
- [x] Adapter-interface and registration tests prove MCP discovery and calls
  have no downstream-agent tunnel dependency or capability.

### Phase A7 — Read capability catalog

**Depends on:** A6.
Each tool needs an exact JSON schema, response-size limit, RBAC resource/verb,
scope resolver, redactor, audit classification, timeout, and unit/integration
test. Reuse existing handlers/services; do not call Kubernetes directly from the
agent. Management-cluster tools resolve the singleton `local` cluster server-side
and accept no arbitrary `cluster_id`. Agent-fleet tools may accept a cluster ID
only as the key of an Astronomer-owned connection record and must never forward a
request to that cluster.

#### Installation and management-cluster reads

- [x] **A7-001** `astronomer.installation.summary` — installation UUID,
  Astronomer/chart versions, namespace/release, management Kubernetes
  version/distribution, and redacted component health.
- [x] **A7-002** `astronomer.installation.readiness` — bounded readiness details for
  database, schema, Redis, workers, tunnel hub/locator, and security cache.
- [x] **A7-003** `astronomer.installation.configuration` — allowlisted effective
  settings and feature state with all secrets and sensitive values omitted.
- [x] **A7-004** `astronomer.management.workloads` and `.workload_get` — only
  Astronomer-owned management workloads, rollout state, restarts, and OOM status.
- [x] **A7-005** `astronomer.management.events` — bounded recent events for the
  Astronomer namespace and allowlisted supporting namespaces/components.
- [x] **A7-006** `astronomer.management.pod_logs` — named management pod/container,
  maximum 200 lines and 64 KiB, shared redaction, and no downstream log path.
- [x] **A7-007** `astronomer.management.nodes`, `.storage`, and `.network` — bounded
  management-cluster capacity/pressure, PVC health, ingress/service/network-policy
  status, and no arbitrary resource-kind or GVR input.
- [x] **A7-008** `astronomer.database.health` — connection-pool, replication,
  storage, and failover signals without raw SQL or credentials.
- [x] **A7-009** `astronomer.queue.health` and `.failed_tasks` — bounded Asynq/Redis
  health, backlog, retry, and dead-letter metadata without payloads.
- [x] **A7-010** `astronomer.argocd.self_management_status` — sanitized health and
  sync state for Astronomer-owned management applications only.
- [x] **A7-011** `astronomer.migrations.status`, `astronomer.backups.status`, and
  `astronomer.tls.status` — bounded management-plane operational state without
  storage credentials, private keys, or mutation controls.
- [x] **A7-012** `astronomer.observability.health` — named/query-template metrics
  and logging pipeline health, bounded ranges/series/results, and no arbitrary
  PromQL/LogQL by default.
- [x] **A7-013** `astronomer.alert.list` and `.get` — actor-visible management-plane
  alerts only.
- [x] **A7-014** `astronomer.audit.recent_changes` — narrow management-resource and
  time query with sanitized details and affected-resource authorization.

#### Astronomer cluster-agent connection reads

- [x] **A7-015** `astronomer.agent_fleet.summary` — counts by connection state,
  version, environment, region, ingestion health, and bounded anomaly rollups.
- [x] **A7-016** `astronomer.agent_fleet.list` — actor-visible Astronomer cluster
  ID/display name, environment/region, agent ID/version, current state, last
  heartbeat, and last successful connection. Free-form labels are deliberately
  omitted from v1 because they are operator-controlled and not secret-safe.
- [x] **A7-017** `astronomer.agent_fleet.get` — one connection record including
  reconnect count, flap frequency, structured errors, authentication/registration
  status, protocol compatibility, and credential expiry/revocation metadata.
- [x] **A7-018** `astronomer.agent_fleet.connection_history` — bounded connection,
  disconnection, reconnect, owning-server, and structured-reason timeline.
- [x] **A7-019** `astronomer.agent_fleet.upgrade_status` — installed/desired agent
  version and reported upgrade state without offering an upgrade action.
- [x] **A7-020** `astronomer.agent_fleet.ingestion_health` — audit, metrics, and
  state-ingestion failures; pending/failed/expired Astronomer command metadata;
  and self-reported downstream API reachability already present in heartbeats.
- [x] **A7-021** `astronomer.tunnel.health` — management-plane tunnel hub,
  authentication, registration, and locator health without opening a tunnel.
- [x] **A7-022** `astronomer.tunnel.replica_distribution` — connection counts and
  imbalance by owning Astronomer server replica.
- [x] **A7-023** `astronomer.tunnel.recent_errors` — bounded structured server-side
  connection/lookup errors correlated by opaque connection ID.
- [x] **A7-024** Compute fleet-wide patterns that distinguish one agent, a
  region/environment subset, and a simultaneous fleet event without exposing
  downstream Kubernetes contents.
- [x] **A7-025** Implement every A7-015 through A7-024 tool exclusively from
  Astronomer PostgreSQL, Redis locator state, and server-side telemetry; inject a
  fail-fast tunnel client in tests that fails if any proxy method is called.
- [x] **A7-026** Reject unknown fields, unbounded selectors, raw URLs, raw SQL,
  arbitrary query languages, arbitrary Kubernetes identifiers/GVRs, and path
  traversal.

**Verify**

- [x] Every capability has positive, empty, partial-data, timeout, redaction,
  pagination, size-limit, resource-scope, and forbidden tests.
- [x] Secret sentinel fixtures cannot be found in serialized results, audit, or
  logs.
- [x] List tools return identical or narrower resources than the initiating
  user's corresponding Astronomer UI/API views.
- [x] Downstream pods, workloads, nodes, namespaces, events, logs, Kubernetes API
  responses, and agent credentials are absent from all schemas and fixtures.
- [x] Adapter-interface and catalog tests prove discovery and every read
  capability have no downstream proxy dependency.

### Phase A8 — Bounded write capability catalog

**Depends on:** A6, A7.
Every write must use the same service/handler, validation, maintenance-window,
quota, audit, and task semantics as the normal Astronomer API.

- [x] **A8-001** `astronomer.management.workload_restart` — restart one unhealthy
  Astronomer management-plane pod/workload with approval; auto eligibility, if
  later enabled, requires redundancy, PDB safety, explicit allowlist, and cooldown.
- [x] **A8-002** `astronomer.management.workload_rollout` — roll out an allowlisted
  stateless Astronomer server/worker/frontend deployment with approval and
  pre/post readiness checks.
- [x] **A8-003** `astronomer.management.workload_scale` — scale an allowlisted
  Astronomer stateless component within configured min/max and capacity ceilings.
- [x] **A8-004** `astronomer.argocd.self_management_sync` — reconcile an
  Astronomer-owned management application, including the management ingress when
  it is Argo-owned, with no prune, force, arbitrary revision, repository, or
  downstream Application target.
- [x] **A8-005** `astronomer.queue.retry_task` — retry one allowlisted idempotent
  management-plane task/dead-letter item after payload-free review.
- [x] **A8-006** `astronomer.management.run_job` — rerun one allowlisted diagnostic
  or maintenance Job with fixed inputs and no shell/command override.
- [x] **A8-007** Omit separate backup mutation tools in v1. The product has no
  management-only backup-configuration service: its existing backup records can
  target downstream clusters. The fixed `management-plane-backup` and
  `restore-drill` CronJob templates are available only through
  `astronomer.management.run_job`, with no destination, credential, command, or
  production-restore override.
- [x] **A8-008** `astronomer.tunnel.restart_component` — restart a named stuck
  Astronomer management-plane tunnel component, never a downstream cluster agent.
- [x] **A8-009** Omit `tunnel.retry_connection_task` and
  `tunnel.refresh_locator_state` from v1 discovery. Astronomer has no dedicated
  product-owned operation for either behavior; direct Redis locator mutation or
  task replay could race a live tunnel owner. The bounded
  `tunnel.restart_component` management deployment operation remains available.
- [x] **A8-010** Return durable operation IDs and status links rather than waiting
  indefinitely for asynchronous completion.
- [x] **A8-011** Apply the product maintenance-window evaluator to every write.
  Refuse windows fail closed; defer windows persist only a bounded scheduling
  decision and operation ID. Charlie retains its own action context and may
  retry the same signed action after `deferred_until`, so Astronomer never stores
  prompts, envelopes, or tool arguments in the deferred-operation record.
- [x] **A8-012** Execute one action at a time per incident, enforce durable
  capability/resource cooldowns and explicit auto budgets, gather adapter-owned
  pre/post health, stop the incident after a failed or ambiguous action, and
  never chain authority from model output. Auto remains fail-closed until an
  enabled per-capability product policy exists.
- [x] **A8-012a** Require every write to carry one opaque `resource_id` that
  exactly matches the session's product-disclosed resource table. Re-read that
  table during live evaluation and again immediately before approval/budget
  consumption; session resource IDs are unique across kinds and only `read`
  ProductContext rows can match. Bind approval rows to the one signed manifest resource and key
  automatic cooldown/budget scope by `resource_id`. Adapter target fields remain
  independently schema-validated, so an agent-fleet record may authorize a
  bounded Astronomer management-plane remediation without becoming a downstream
  API or mutable cluster-agent target.
- [x] **A8-013** Explicitly omit and reject shell/exec, arbitrary Kubernetes
  apply/patch/delete/proxy, Secret/ConfigMap data, direct SQL/Redis mutation,
  migration manipulation, PostgreSQL failover, production restore, PV/PVC or
  namespace deletion, RBAC/users/credentials/network-policy changes, and full
  Astronomer upgrade/rollback.
- [x] **A8-014** Explicitly omit and reject every downstream cluster operation,
  including listing/reading resources or logs; installing, upgrading, restarting,
  deleting, or configuring an Astronomer cluster agent; rotating downstream
  credentials; and invoking downstream Helm, Argo, backup, security, or fleet
  workflows.
- [x] **A8-015** Each v1 action is bounded by a fixed adapter: three named
  stateless Deployments with replica/capacity checks; one product-owned Argo
  Application with prune/force/revision omitted; payload-free replay of a fixed
  idempotent management-task allowlist; two fixed CronJob templates with no
  input override; and the named server/worker management Deployments. Backup
  configuration actions, direct locator repair, shell/exec, arbitrary
  Kubernetes/Argo/queue operations, and every downstream action remain out of
  scope because Astronomer has no equivalently narrow product-owned service or
  because their side effects are destructive, credential-bearing, or race live
  ownership.

**Verify**

- [x] Each write has success, replay, validation, maintenance, quota, RBAC,
  approval, read-only denial, auto allowlist, stale epoch, pre/post health, and
  cooldown tests.
- [x] Excluded operations are absent from discovery and fail if invoked by name.
- [x] No write bypasses existing Astronomer audit or operation tracking.
- [x] Adapter-interface and catalog tests prove every write has no downstream
  proxy dependency and cannot mutate an Astronomer cluster-agent record or
  credential.

### Phase A9 — Chat/session API and privacy model

**Depends on:** A5-A8.

- [x] **A9-001** Create a local session record and hash-only user delegation
  before calling the bridge.
- [x] **A9-002** Pass actor ID/type/display label, opaque authorization reference,
  intent, versioned context schema, resources, and bounded evidence.
- [x] **A9-002a** Define `astronomer.sre-context/v1` with installation UUID,
  Astronomer/chart versions, namespace/release, management Kubernetes version and
  distribution, trigger/current UI context, affected management-component or
  agent-connection-record IDs, a redacted health summary, and stable opaque
  authorization/correlation references.
- [x] **A9-002b** Do not eagerly attach logs, metric samples, manifests, audit
  details, configuration bodies, database rows, Kubernetes objects, credentials,
  or secrets; retrieve bounded evidence through MCP after authorization.
- [x] **A9-003** Pass running Astronomer `pkg/version.Version` as
  `product_version` for Charlie documentation release selection.
- [x] **A9-004** Persist the Charlie session ID and central revision only after
  successful creation.
- [x] **A9-005** Enforce owner-only access for user sessions even though the local
  agent credential can access the deployment's central sessions.
- [x] **A9-006** Enforce affected-resource read access for shared incident
  sessions on every list/get/history/event/action request.
- [x] **A9-007** Keep prompts, answers, evidence, actions, and history in Charlie;
  proxy on demand after authorization.
- [x] **A9-008** Revoke delegations on session abort, user deactivation, binding
  loss, connection replacement, or configured expiry.
- [x] **A9-009** Use stable client-message IDs and return replayed accepted turns
  rather than duplicates.
- [x] **A9-010** Resume SSE after browser, server, agent replica, or central
  disconnect.
- [x] **A9-011** Distinguish unavailable, policy denied, waiting approval, failed,
  aborted, and completed states in API/UI contracts.
- [x] **A9-012** Add per-user/session/IP rate and concurrency limits.
- [x] **A9-013** Add content-free audit metadata for session lifecycle and
  visibility decisions.

**Verify**

- [x] Owner A cannot access owner B's private session.
- [x] A resource-authorized operator can access the relevant incident; an
  unrelated operator cannot.
- [x] Revoking a binding during a turn blocks the next MCP call and subsequent
  history read where applicable.
- [x] Persistence scans prove conversation content is not duplicated locally.

### Phase A10 — Global user experience

**Depends on:** A9.

- [x] **A10-001** Add a persistent Charlie launcher to the authenticated shell.
- [x] **A10-002** Add an accessible keyboard shortcut that does not conflict with
  the command palette.
- [x] **A10-003** Render a right-side desktop drawer and full-screen mobile view.
- [x] **A10-004** Reuse Astronomer's design tokens, focus traps, dialogs, toasts,
  empty states, and permission states.
- [x] **A10-005** Add a route-aware context registry with explicit adapters for
  installation health, management components, alerts, monitoring, logging,
  backups, self-management GitOps, audit, Astronomer cluster-agent records, and
  agent-fleet/tunnel health.
- [x] **A10-006** Display every attached context item as a visible removable chip.
- [x] **A10-007** Never attach logs, metrics samples, audit details, or broad
  related resources automatically; attach IDs and bounded summaries, then let
  Charlie request evidence through MCP.
- [x] **A10-008** Allow users to add authorized context through a searchable
  picker.
- [x] **A10-009** Render streaming text with safe Markdown that ignores raw HTML
  and sanitizes links.
- [x] **A10-010** Render retrieval state and citations to Charlie-managed product
  documentation.
- [x] **A10-011** Render tool cards with capability, effect, risk, bounded
  arguments, state, result summary, and audit correlation.
- [x] **A10-012** Render inline approval cards only after server-side eligibility
  is confirmed; never rely on hiding the button as authorization.
- [x] **A10-013** Add reconnect, retry, partial response, central unavailable,
  agent failover, policy denial, and expired-session states.
- [x] **A10-014** Add `/dashboard/charlie` with deep-linked `conversations`,
  `investigations`, `findings`, and `approvals` tabs and session/incident/finding
  IDs in the URL.
- [x] **A10-015** Preserve selected tab/filter/context in TanStack search params.
- [ ] **A10-016** Add responsive, keyboard, screen-reader, reduced-motion, and
  contrast tests.
- [x] **A10-017** Surface open/acknowledged actionable medium-or-higher Charlie
  findings through Astronomer's existing notification/alert system with dedupe,
  severity, affected resource, diagnosis confidence, reason no action ran, and a
  deep link; resolved, dismissed, expired, disabled, info, and low findings do
  not inflate the Topbar alert count.
- [x] **A10-017a** Synchronize only the bounded central finding-summary contract
  during an authorized list request. Require exact central `session_id` linkage,
  map it to the same-deployment local session, atomically inherit that session's
  complete resource scope, and apply current owner/resource authorization before
  rendering. Background sync stores no central detail/content and an outage does
  not hide already-authorized local findings.
- [x] **A10-018** Finding detail fetches authorized central detail on demand and
  shows bounded evidence, actionable operator checks, proposed capability and
  exact bounded target, risk/impact, preconditions, expected result, rollback when
  available, verification steps, mode, and approval eligibility.
- [x] **A10-019** Let an authorized user acknowledge/dismiss/resolve a finding or,
  in approval mode, open the exact approval flow; never turn a recommendation
  click into implicit execution.

**Verify**

- [ ] Frontend unit tests cover every state and permission combination.
- [x] Playwright covers global launch from at least installation health,
  management component, alert, cluster-agent, backup, and self-management GitOps
  pages; context chips match the route resource and never imply downstream access.
- [x] Deep links survive refresh and browser back/forward.
- [ ] Accessibility scan has no serious/critical violations.

### Phase A11 — Administration experience

**Depends on:** A3-A5, A9.

- [x] **A11-001** Add a Charlie card to the Settings hub behind
  `feature.charlie` and `charlie:manage`.
- [x] **A11-002** Add `/dashboard/settings/charlie` with deep-linked tabs:
  `connection`, `agent`, `mode`, `automation`, `access`, and `diagnostics`.
- [x] **A11-003** Connection tab uploads/validates a package and shows only
  non-secret product/deployment/route/version/fingerprint/digest metadata.
- [x] **A11-004** Agent tab shows Argo application state, desired/ready replicas,
  leader/standby, epoch, heartbeats, versions, chart/image digests, and lifecycle
  controls.
- [x] **A11-005** Mode tab changes disabled/read-only/approval/auto through the
  local bridge, reads back Charlie's authoritative value, and explains effects.
- [x] **A11-005a** Provide an emergency Disable control that immediately stops new
  sessions, trigger/finding dispatch, central claims, waiting approvals/actions,
  and MCP calls while preserving audit and health/configuration access.
- [x] **A11-006** Require disclosure rediscovery/acknowledgement when capability,
  allowlist, MCP certificate, or mode changes alter the digest.
- [x] **A11-007** Automation tab manages trigger rules, scopes, cooldowns,
  grace periods, flap windows/counts, fleet thresholds, version policy,
  suppression, retries, dead letters, and service-identity access; defaults are
  displayed and editable rather than hidden in Charlie.
- [x] **A11-008** Access tab shows effective Charlie permissions and automation
  grants without creating a parallel role engine.
- [x] **A11-009** Diagnostics tab independently reports:
  - local database/config;
  - Product Bridge mTLS;
  - both agent replicas;
  - central connection through agent;
  - leader/epoch;
  - route/RAG readiness reported through agent;
  - MCP TLS/discovery digest;
  - Charlie OCI chart/image availability;
  - certificate and credential expiry.
- [x] **A11-010** Never make Charlie health part of core Astronomer readiness;
  degrade only Charlie features.
- [x] **A11-011** Require typed confirmation for uninstall, connection replacement,
  and disconnect.

**Verify**

- [x] All tabs deep-link and refresh correctly.
- [x] Non-admin users cannot call admin APIs directly.
- [x] No read response or DOM text contains onboarding, registry, certificate
  private key, enrollment, or runtime token material.

### Phase A12 — Durable event-triggered investigations

**Depends on:** A5, A9, A11, Charlie C3A.

- [x] **A12-001** Use Astronomer's task-outbox/worker path; do not rely solely on
  ephemeral Redis/SSE events.
- [x] **A12-002** Create default reviewed rules for high/critical alert firing.
- [x] **A12-003** Create an Astronomer cluster-agent disconnected rule with a
  configurable grace period, default five minutes; cancel if it reconnects before
  dispatch.
- [x] **A12-004** Create an agent-flapping rule with configurable thresholds,
  default three disconnects in fifteen minutes.
- [x] **A12-005** Add configurable rules for stale heartbeat, repeated
  authentication/registration failure, self-reported downstream Kubernetes API
  unreachability, unsupported/materially-behind agent version, failed/stalled
  agent upgrade, repeated state/metrics/audit ingestion failure, and pending
  command expiry without acknowledgement.
- [x] **A12-006** Add configurable management-plane rules for tunnel connections
  concentrated on one server replica, cross-replica locator failure, a server
  rollout causing a reconnect spike, and a significant simultaneous fleet
  disconnect.
- [x] **A12-007** Keep grace periods, flap windows/counts, version policy,
  concentration limits, and fleet-percentage thresholds in Astronomer trigger
  configuration; send evaluated events to Charlie rather than hardcoding policy
  in Charlie.
- [x] **A12-008** Create default rules for terminal failures in management-plane
  backup/drill, logging, self-management GitOps, migration, queue, and tunnel
  workflows.
- [x] **A12-009** Let administrators enable/disable and scope defaults during
  onboarding; do not silently enable automatic writes.
- [x] **A12-010** Compute a fingerprint from source type, stable resource ID,
  normalized failure class, and bounded status—not message text alone.
- [x] **A12-011** Maintain one active investigation per fingerprint with a default
  30-minute cooldown.
- [x] **A12-012** Coalesce repeat timestamps/counts into local metadata without
  duplicating Charlie sessions.
- [x] **A12-013** Send only IDs, type, severity, state, timestamp, version,
  environment/region pattern dimensions, and a redacted bounded summary through
  the bridge.
- [x] **A12-014** Use the automation service identity's opaque delegation.
- [x] **A12-015** Let the Charlie product agent gather live evidence only through
  MCP; never use an Astronomer cluster agent or tunnel as an evidence channel.
- [x] **A12-016** Add exponential retry, maximum attempts, dead-letter state,
  operator retry, suppression reason, and idempotent session creation.
- [x] **A12-017** Link investigations back to the originating Astronomer resource
  and audit/event record.
- [x] **A12-018** Publish live UI updates through the existing event bus after
  durable state changes.

**Verify**

- [x] Duplicate event storms create one investigation and accurate repeat count.
- [x] Reconnect-before-threshold creates no agent-disconnect investigation.
- [x] Exactly three disconnects inside the default fifteen-minute window creates
  one flapping investigation; events outside the window do not.
- [x] Fleet and regional synthetic incidents are classified separately from a
  single-agent incident.
- [x] Worker/server restarts do not lose or duplicate trigger work.
- [x] Users without affected-resource access cannot list or open the incident.
- [x] Auto mode still cannot execute a capability absent from both the Charlie
  allowlist and automation service-identity grant.

#### Required troubleshooting workflow for cluster-agent instability

For one flapping Astronomer cluster agent, the Charlie product agent must gather
the minimum authorized evidence and correlate, in order:

1. Connection and heartbeat history.
2. Authentication and registration results.
3. Astronomer cluster-agent and protocol versions.
4. Structured errors already reported by that agent.
5. The Astronomer server replica that owned each connection.
6. Recent Astronomer deployment or configuration changes.
7. Bounded management-plane server logs around the opaque connection ID.
8. Redis and tunnel-locator health.
9. Management-plane load balancer, ingress, and TLS health.
10. Whether agents in the same region/environment or the broader fleet are
    affected.

- [x] **A12-019** Classify fleet-wide likely causes such as Astronomer server
  rollout, Redis/locator failure, ingress timeout, certificate issue, capacity
  exhaustion, or management-plane networking.
- [x] **A12-020** Classify regional-subset likely causes such as external routing,
  load balancer, DNS, or provider connectivity.
- [x] **A12-021** Classify single-agent likely causes such as downstream network,
  local agent crash, unsupported version, expired credential, or downstream API
  failure.
- [x] **A12-022** For a likely downstream-side cause, return the diagnosis,
  confidence/evidence, and exact operator checks without entering the cluster or
  proposing a downstream action.
- [x] **A12-023** Propose only the least disruptive Astronomer-side remediation,
  enforce the selected Charlie mode, execute at most one bounded action, then
  re-read health before continuing.
- [x] **A12-024** When read-only mode finds a material issue, create or update one
  actionable Charlie finding instead of attempting a write.
- [x] **A12-025** When approval mode identifies a write, create a finding linked
  to the exact expiring approval; rejection/expiry updates the finding without
  automatically proposing broader arguments.
- [x] **A12-026** When auto mode is blocked by RBAC, scope, eligibility, allowlist,
  safety budget, precondition, cooldown, circuit breaker, or failed verification,
  create/update a finding with the exact block reason and safe next step.
- [x] **A12-027** Deduplicate findings by installation, affected resource,
  normalized diagnosis, and recommended capability; coalesce repeats, suppress
  alert storms, expire stale recommendations, and reopen on verified recurrence.
- [x] **A12-028** Publish finding lifecycle to the existing Astronomer alert/event
  bus only after durable local correlation is committed.

### Phase A13 — Observability, audit, security, and operations

**Depends on:** A4-A12.

- [x] **A13-001** Add content-free Prometheus metrics for bridge calls/latency,
  agent availability, leader changes, SSE replay, MCP calls/results, approvals,
  triggers/dedup/retries/dead letters, certificate expiry, and action outcomes.
- [x] **A13-002** Bound metric labels to enums/status/capability names; never use
  user IDs, resource names, prompts, errors, URLs, or arguments as labels.
- [x] **A13-003** Add structured logs with request/session/action correlation IDs
  but no content, evidence, authorization reference, or secret.
- [x] **A13-003a** Log every feature/mode/RBAC/scope/approval/allowlist/safety
  decision as bounded structured outcome codes; never log prompts, reasoning, raw
  evidence/tool output, exact sensitive arguments, credentials, or Secret values.
- [x] **A13-004** Add audit events for every onboarding, install, mode, access,
  session, approval, MCP authorization, action, rotation, upgrade, uninstall,
  and trigger lifecycle transition.
- [x] **A13-005** Pass all diagnostic/support-bundle material through shared
  redaction and include only public certificate metadata/fingerprints.
- [x] **A13-006** Add NetworkPolicy for bridge/MCP pod identities and exact ports.
- [x] **A13-007** Add configured Charlie central/OCI CIDRs or egress gateway only
  to the agent; document dynamic-IP handling and proxy requirements.
- [x] **A13-007a** Prove the existing server/worker external-egress CIDRs do not
  include Charlie central/OCI. Because Kubernetes NetworkPolicies are additive,
  reject production values containing `0.0.0.0/0` or an overlapping Charlie
  CIDR unless traffic is forced through an independently enforced egress
  gateway; a Charlie-specific deny policy cannot override an existing allow.
- [x] **A13-008** Persist only their expiration timestamps, publish a fixed-kind
  seconds-to-expiry metric, and add certificate, enrollment, artifact
  credential, and onboarding-package expiry alerts.
- [x] **A13-009** Add runbooks for bridge unavailable, no leader, disclosure
  drift, MCP TLS failure, central unavailable, OCI pull failure, trigger backlog,
  rotation failure, Charlie product-agent failure, one flapping Astronomer
  cluster agent, regional agent instability, and fleet-wide disconnect.
- [x] **A13-010** Update threat model, secret policy/inventory, network policy,
  audit schema, DR, backup/restore, and on-call documentation.
- [x] **A13-011** Ensure DR restores local encrypted trust and metadata but
  requires new onboarding if agent enrollment/registry Secrets were lost.
- [x] **A13-012** Add a security review specifically for prompt injection,
  cross-session access, confused deputy, SSRF, tool schema manipulation,
  idempotency, stale leadership, and content leakage.
- [x] **A13-013** Add action budgets, per-capability/resource cooldowns,
  concurrency limits, repeated-failure circuit breakers, pre/postcondition
  verification, and an incident-wide stop-after-failure rule.
- [x] **A13-014** Add retention/deletion rules for local finding summaries and
  central detail, plus alert dedupe/cardinality and notification-delivery metrics.

**Verify**

- [x] Metrics cardinality tests and log/audit/support-bundle secret scans pass.
- [x] Network tests prove browser/public ingress cannot reach bridge or MCP.
- [x] Server/worker cannot reach central Charlie directly.
- [x] Only agent pods can reach MCP and only Astronomer server/worker pods can
  reach Product Bridge.
- [ ] Every runbook is exercised by a game-day or deterministic test.

### Phase A14 — Connected and air-gap qualification

**Depends on:** all prior phases.

#### Connected qualification against the separate Charlie server

- [ ] **A14-001** Configure the Astronomer product, route, versioned docs, and one
  isolated deployment in Charlie administration.
- [ ] **A14-002** Download the signed onboarding package from Charlie.
- [ ] **A14-003** Import it in Astronomer and install only the generic agent.
- [ ] **A14-004** Pull chart/image exclusively from Charlie OCI.
- [ ] **A14-005** Block direct server/worker access to Charlie central before
  functional testing.
- [ ] **A14-006** Confirm both replicas, one leader, one standby, correct epoch,
  compatible versions, and MCP disclosure acknowledgement.
- [ ] **A14-007** Validate grounded Astronomer-version answers and general
  Kubernetes passthrough answers.
- [ ] **A14-008** Validate visible context chips and authorized on-demand evidence.
- [ ] **A14-009** Validate read-only read success and write denial.
- [ ] **A14-010** Validate approve-once executes once and reject executes never.
- [ ] **A14-011** Validate auto executes only an explicitly allowlisted,
  service-authorized bounded action.
- [ ] **A14-012** Validate user-private and resource-shared visibility.
- [ ] **A14-013** Validate critical-alert trigger deduplication and shared
  investigation creation.
- [ ] **A14-013a** Simulate stale heartbeat, five-minute disconnect, three flaps in
  fifteen minutes, authentication failure, upgrade stall, ingestion failure,
  command expiry, replica concentration, locator failure, rollout reconnect spike,
  and fleet disconnect; validate configurable trigger evaluation and deduplication.
- [ ] **A14-013b** Validate one-agent, region/environment subset, and fleet-wide
  troubleshooting classifications using only database, Redis locator, and
  Astronomer server telemetry.
- [ ] **A14-013c** Instrument every downstream tunnel/proxy entry point and prove
  zero calls during chat, triggered investigations, every MCP read, approval
  actions, and auto-mode actions.
- [ ] **A14-013d** Attempt every prohibited downstream operation by tool name and
  crafted arguments; verify it is absent from discovery, rejected, audited, and
  never reaches an Astronomer cluster agent.
- [ ] **A14-014** Kill the leader at each write boundary and prove no duplicate
  action.
- [ ] **A14-015** Restart Charlie, Astronomer server/worker, Argo, and both agent
  replicas during sessions and verify recovery.
- [ ] **A14-016** Connect a second Astronomer installation and prove credential,
  artifact, session, approval, action, audit, usage, and investigation isolation.
- [ ] **A14-016a** With feature false, enabled-without-activation, central-disabled,
  and emergency-disabled states, prove zero model/RAG/session/work-claim/MCP/
  evidence calls and zero trigger/finding dispatch while core Astronomer remains
  healthy.
- [ ] **A14-016b** Execute the full deny-precedence matrix across read-only,
  approval, and auto with live RBAC/scope changes, disclosure drift, allowlists,
  budgets, preconditions, cooldowns, circuit breakers, and fencing.
- [ ] **A14-016c** Prove every destructive/irreversible operation is absent from
  discovery and cannot run in any mode; simulate model/prompt attempts to bypass
  effect/risk classification and verify denial/audit.
- [ ] **A14-016d** Validate read-only diagnosis, pending/rejected/expired approval,
  blocked auto, failed precondition, and failed post-verification each produce one
  deduplicated actionable finding with correct alert, deep link, lifecycle, and
  no implicit action.

#### Internal-Charlie air gap

- [ ] **A14-017** Install an internal Charlie from its signed transfer kit with
  internet and external DNS blocked.
- [ ] **A14-018** Seed its internal Charlie OCI service from the kit.
- [ ] **A14-019** Import an onboarding package and install the agent without any
  external registry.
- [ ] **A14-020** Repeat chat, RAG, read-only, approval, auto, trigger, failover,
  rotation, upgrade, and rollback qualification.
- [ ] **A14-021** Capture traffic and prove no external connection attempts.

#### Controlled-egress topology

- [ ] **A14-022** Permit only DNS/proxy plus configured Charlie central and OCI
  destinations from agent/artifact pull paths.
- [ ] **A14-023** Block all other outbound traffic.
- [ ] **A14-024** Repeat onboarding, pull, runtime, MCP, rotation, upgrade, and
  failover qualification.

## 6. UI acceptance details

### Global drawer

- [ ] Closed state does not fetch conversation content.
- [ ] Opening restores the user's last private session only after authorization.
- [ ] New-chat clearly displays current mode and attached context.
- [ ] Context can be removed before the first network request.
- [ ] Waiting approval, agent failover, central unavailable, and MCP denial are
  visually distinct.
- [ ] Disabled, read-only finding, approval required, auto blocked, destructive
  denial, failed verification, and emergency-stop states are visually distinct.
- [ ] Tool cards never display redacted/secret arguments.
- [ ] Closing the drawer does not abort a turn; an explicit Abort action does.
- [ ] Mobile layout preserves streaming, context, and approvals without
  horizontal overflow.

### Investigations and approvals

- [ ] Investigation list supports status, severity, cluster, source, and time
  filters represented in URL search parameters; `cluster` means an Astronomer
  cluster-agent connection record, not downstream resource access.
- [ ] Investigation detail links to the originating Astronomer resource.
- [ ] Finding list/detail deep-links preserve status, severity, source, affected
  resource, execution-block reason, and time filters.
- [ ] Repeated events show a count/timeline rather than duplicated incidents.
- [ ] Approval detail shows bounded non-authoritative capability, effect, risk,
  target summary, expiry, eligibility, and required permission. The browser
  never receives signed manifests, signatures, exact arguments, authorization
  references, or authority digests.
- [ ] Approval submission supports a bounded rationale and handles stale or
  conflicting exact-action decisions.
- [ ] Resolved/expired approvals cannot be resubmitted.
- [ ] Finding actions require live authorization; acknowledge/dismiss/resolve are
  idempotent and “Approve” opens a separate exact-action confirmation.

### Administration

- [ ] Package upload is clearly separate from provider/model/RAG administration,
  which remains in Charlie.
- [ ] Agent install cannot be enabled before signature/digest/trust validation.
- [ ] Auto mode cannot be enabled before MCP discovery acknowledgement,
  automation identity configuration, and explicit allowlist review.
- [ ] Auto mode cannot include destructive/non-auto-eligible capabilities and
  requires visible budgets, cooldowns, preconditions, verification, and circuit
  breaker settings.
- [x] Emergency disable remains available during degraded central/agent states and
  the UI distinguishes requested from verified central mode.
- [ ] Diagnostics state exactly which boundary failed and provide a safe next
  action without exposing secrets.

## 7. Verification commands

| Gate | Command | Expected result |
| --- | --- | --- |
| Go tests/races | `make test` | all packages pass, no races |
| Go lint | `make lint` | exit 0 |
| API contract | `make verify` | generated API/route contracts pass |
| Full repository | `make verify-enterprise` | backend, frontend, Helm pass |
| Migrations | `make check-migrations` | no unsafe migration findings |
| sqlc drift | `make sqlc-check` | generated queries clean |
| Error catalog | `make error-codes-check` | docs/catalog synchronized |
| Frontend types | `npm --prefix frontend run type-check` | exit 0 |
| Frontend unit | `npm --prefix frontend test` | all tests pass |
| Frontend lint | `npm --prefix frontend run lint` | exit 0 |
| Frontend build | `npm --prefix frontend run build` | production build succeeds |
| Browser tests | `npm --prefix frontend run test:e2e` | Chromium/mobile suites pass |
| Route smoke | `npm --prefix frontend run test:e2e:smoke` | all registered routes render |

## 8. Cross-repository dependency order

| Order | Charlie prerequisite | Astronomer work unlocked |
| --- | --- | --- |
| 1 | C1 schemas/security | A1 contract pin |
| 2 | C2 enrollment exchange | A3 onboarding validation |
| 3 | C3 Product Bridge | A5 runtime proxy and A9 sessions |
| 4 | C3A authority/safety/findings | A6 enforcement and A12 actionable alerts |
| 5 | C4 leadership/fencing | A4 HA install and A6 action fencing |
| 6 | C5 Charlie OCI | A4 artifact installation |
| 7 | C6 chart hardening | A4 Argo reconciliation |
| 8 | C7 air-gap kit | A14 offline qualification |
| 9 | C8 generic docs/tooling | final integrated release closure |

Do not begin an Astronomer phase against a draft Charlie schema. Pin the exact
reviewed schema/version/digest first.

## 9. Rollout and rollback

- [ ] Ship all Astronomer code behind `feature.charlie=false`.
- [ ] Verify a default installation creates no Charlie namespace, workloads,
  Secrets, clients, worker registrations, triggers, alerts, or network flows.
- [ ] Enable only in the development Astronomer installation first.
- [ ] Start with Charlie `read_only`; run the complete read and isolation suite.
- [ ] Move to `approval`; exercise approve-once/reject and permission intersection.
- [ ] Move to `auto` only for individually reviewed capability/scope pairs.
- [ ] Keep an immediate mode-disable control that travels through the bridge and
  displays verified central state.
- [ ] Enforce a local deny immediately when emergency disable is requested, even
  if central readback is temporarily unavailable; reconcile authoritative central
  state when connectivity returns without resuming work automatically.
- [ ] If the agent is unavailable, stop new sessions/triggers and fail closed;
  do not fall back to direct central calls.
- [ ] Roll back the agent by immutable prior digest through Argo.
- [ ] Roll back Astronomer by disabling the feature and retaining schema/data;
  do not drop audit/session correlations during incident rollback.
- [ ] Full disconnect requires explicit operator confirmation and Charlie-side
  credential revocation verification.

## 10. STOP conditions

Stop and report; do not improvise if:

- any Astronomer runtime feature appears to require direct Charlie central
  access;
- the Charlie agent would require Kubernetes API access or a service-account
  token;
- a capability cannot be implemented through an existing authorized Astronomer
  service/handler without bypassing policy;
- a capability, context adapter, trigger, or remediation would open or proxy an
  Astronomer cluster-agent tunnel, query downstream Kubernetes, or mutate a
  downstream agent/configuration/credential;
- any disabled state still permits a session, trigger/finding dispatch, central
  work claim, model/RAG request, evidence read, MCP discovery/call, or waiting
  action continuation;
- mode, RBAC, scope, approval, allowlist, or safety checks cannot be revalidated
  atomically immediately before a side effect;
- a destructive/irreversible action would be discoverable as auto eligible or
  lack explicit recovery/verification semantics;
- an action cannot be made idempotent or safely fenced;
- a result cannot be bounded/redacted without losing its authorization boundary;
- an onboarding package contains durable product-client/product-agent or
  configuration-admin credentials;
- a certificate/private key/token would enter audit, logs, support bundles, API
  read responses, CRD status, or Argo Application fields;
- the implementation would edit an applied migration rather than add migration
  147 or a later forward migration;
- air-gap validation requires an undeclared external image, registry, DNS name,
  signature service, or package download;
- direct server/worker egress blocking breaks the intended integration;
- an authorization or security gate remains unresolved after two focused
  attempts.

## 11. Definition of done

- [ ] All A1-A14 tasks are checked with machine-readable, secret-free evidence.
- [ ] Charlie central is not installed in Astronomer.
- [ ] The generic Charlie agent is the only Charlie runtime workload installed.
- [ ] All runtime Charlie traffic passes through the local agent bridge.
- [ ] Astronomer server/worker direct central egress is blocked and the full
  integration still passes.
- [ ] The agent has no Kubernetes service-account token or RBAC.
- [ ] Charlie OCI supplies the chart/image without an external registry.
- [ ] User, service, approval, mode, capability, and resource authorization tests
  all fail closed.
- [ ] Charlie diagnoses the Astronomer management plane and downstream-agent
  connection fleet from Astronomer-owned metadata while downstream Kubernetes
  contents remain unreachable.
- [ ] All nine `astronomer.agent_fleet.*`/`astronomer.tunnel.*` read tools are
  bounded, redacted, authorized, and proven not to proxy downstream.
- [ ] Every downstream resource and downstream cluster-agent mutation capability
  is absent from MCP discovery and rejected by the server.
- [ ] Default-disabled and emergency-disabled integrations are inert and isolated
  without affecting Astronomer readiness or non-Charlie functionality.
- [ ] Read-only, approval, and auto obey the complete deny-first authority matrix;
  no prompt, user request, finding, or Charlie role can widen Astronomer RBAC.
- [ ] Destructive actions cannot auto-run, every bounded action has preconditions
  and post-verification, and safety budgets/cooldowns/circuit breakers stop loops.
- [ ] Non-executed material diagnoses create deduplicated actionable alerts with
  safe operator checks or an exact separately approved action.
- [ ] Two agent replicas fail over without duplicate writes.
- [ ] Private chat, shared incidents, global drawer, approvals, admin UI, and
  event automation meet accessibility and deep-link acceptance criteria.
- [ ] Connected, internal air-gap, controlled-egress, multi-deployment,
  rotation, upgrade, rollback, restart, and content-minimization qualification
  all pass.
- [ ] `make test`, `make lint`, `make verify-enterprise`, frontend E2E, Helm/Argo,
  and live qualification pass on the release commit.
