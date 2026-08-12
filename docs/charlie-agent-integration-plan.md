# Charlie Agent Integration Plan for Astronomer

> **Status:** Implementation and release-qualification record. Audited against
> the v1 code and tests on 2026-08-06. The fail-closed, reconciliation, and local
> emergency-deny decisions below are part of v1. Unchecked boxes are deliberate
> release gates, not implied implementation: the remaining server test/lifecycle
> gaps are recorded in section 5.1, while A10/A11 acceptance and A14 require the
> integrated UI or a live deployment.
>
> **Implementation branch:** `feat/charlie-core-integration`; source schema
> version 154 and Charlie release candidate `1.0.29`. Repository verification
> covers the cumulative authority matrix, HA mode fencing, exact approval replay,
> actionable alerts, destructive/confused-deputy denial, and downstream-boundary
> attribution. Charlie `1.0.29` and Astronomer schema 154 are the current
> development qualification candidates. The unchecked live packet-isolation, full approval,
> automation, outage, failover, and multi-deployment gates remain release gates.
> Charlie `1.0.29` also makes Product Bridge session creation idle: Astronomer
> creates the authorized session first, and each explicit user message starts
> exactly one RAG/model turn that inherits the session's bounded product context.
> Retrieval remains passthrough-capable: Astronomer renders only citations that
> Charlie proves the assistant explicitly referenced, never every candidate hit.
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
- [x] **A-015** In an active non-disabled integration, emit actionable Astronomer
  alerts/findings when Charlie diagnoses an issue but mode, approval,
  authorization, or safety policy prevents execution. Disabled remains inert and
  creates no new finding or notification work.
- [x] **A-016** Record every authority decision and lifecycle transition through
  bounded event/reason codes and opaque correlations; never copy prompts,
  evidence, arguments, results, secrets, credentials, provider bodies, or raw
  exception text into operational logs or audit.
- [x] **A-017** Reject destructive/irreversible disclosures entirely in v1 and
  require every permitted write to be typed, reversible, idempotent, scoped,
  preconditioned, budgeted, fenced, audited, and independently verified.

### Release safety invariant

The Charlie integration is an isolated optional subsystem, never an ambient
Astronomer privilege. These are requirements, not claims that the remaining
unchecked qualification gates have passed:

| State or path | Required invariant |
| --- | --- |
| Feature absent or disabled | No Charlie agent, process, listener, timer, worker, trigger, DNS lookup, credential refresh, central egress, MCP call, session, finding, or notification work exists. Persisted local administration is the only surface. |
| Connection enabled at wire `disabled` | Only the minimal authenticated signed control protocol is allowed. Product data, AI, RAG, evidence, findings, claims, and actions remain unavailable. |
| `read_only` | Live actor RBAC and exact resource scope may authorize disclosed reads only. A material recommended write becomes bounded operator guidance and a durable actionable finding; it never becomes an action request. |
| `approval` | All read-only behavior remains. One safe action may run only through the separate authenticated exact-approval path after approve-once, expiry, live RBAC/scope, safety, fencing, idempotency, required product audit admission, and final precondition checks all pass. |
| `auto` | All read-only and exact human-approval paths remain. Only a separately reviewed capability/resource allowlist under the narrow automation identity may run automatically; failure to qualify falls back to exact approval or manual guidance, never broader authority. |
| Destructive or generic capability | Shell, exec, raw SQL, generic HTTP/proxy, Secret/credential access, delete, downstream-tunnel use, and irreversible work are rejected from the v1 catalog in every mode. Approval and automation cannot override this. |
| Alert or finding | It is untrusted advisory data containing bounded impact, evidence summary, safe checks, remediation guidance, verification, coded non-execution reason, timeline/repeat state, and a product deep link. It contains no approval ID, manifest, signature, exact arguments, authorization reference, or reusable action. |
| Logs and audit | Operational telemetry uses typed allowlisted codes, bounded counts/timing/revisions/digests, and opaque correlations only. Required product audit admission fails closed before authority changes and writes; notification delivery is durable/retry-safe but never execution authority. |

Astronomer is the final enforcement point. Charlie central policy and the local
agent may only narrow a request; they cannot raise the product feature state,
mode ceiling, actor permission, target scope, safety admission, or action
authority. Unknown, missing, stale, conflicting, or unavailable inputs deny.
When an active integration cannot act, Astronomer preserves the diagnosis as a
deduplicated durable finding and alerts only currently authorized users with the
next legal decision. Disabled states produce no runtime findings or alerts.

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
- A disabled product feature or connection is fully inert: it starts no agent
  runtime, bridge stream, session, trigger, finding work, central claim,
  model/RAG request, evidence retrieval, MCP operation, or background retry.
  Administrators may inspect and change locally persisted configuration/status,
  but those reads do not contact the agent or Charlie central. An enabled
  connection at operational wire mode `disabled` is a separate state: it retains
  only the minimal signed control heartbeat used to enable, rotate, disconnect,
  or uninstall and permits no intelligence, product-data, or work traffic.
- `read_only` permits authorized disclosed reads and creates actionable findings;
  it never dispatches a write even if a user asks for one.
- `approval_required` (wire value `approval`) cumulatively includes
  `read_only` and permits one exact, expiring user approval plus the approver's
  live target permission immediately before dispatch.
- `automation` (wire value `auto`) cumulatively includes `read_only` and
  `approval_required`, and additionally permits only explicitly reviewed
  auto-eligible capability/scope pairs
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

#### Mode, role, and action-authority separation

Do not implement Read only, Approval required, or Automation as Astronomer roles.
They are installation-wide maximum workflow ceilings. Astronomer roles answer
who may use, inspect, approve, or administer Charlie; existing target-resource
RBAC answers what that actor may access; the mode answers which workflow Charlie
may consider. The final action decision is the intersection of all three plus
disclosure and safety policy.

| Control | Purpose | Explicit non-effect |
| --- | --- | --- |
| Feature/connection state | Starts or stops the isolated Charlie subsystem | Enabling never grants read or write authority. |
| Operational mode | Caps the most powerful workflow available to the deployment/session | Selecting `approval` or `auto` never creates product RBAC or approves an action. |
| `charlie:create/read` | Allows an actor to use authorized Charlie surfaces | It does not authorize reading an affected Astronomer resource. |
| `charlie:approve` | Allows an otherwise eligible actor to decide one exact pending action | It does not authorize the underlying action or make an ineligible/destructive action approvable. |
| `charlie:manage` | Allows integration, mode, disclosure, and policy administration | It does not grant session, target-resource, approval, or execution permission. |
| Automation service identity | Supplies live product authorization for specifically granted background capabilities/scopes | It is not a superuser and cannot inherit an interactive user's access. |
| Existing Astronomer target RBAC | Authorizes one actor and one exact management-plane resource/verb | It cannot raise Charlie's current mode or bypass disclosure/safety gates. |

The UI and APIs must always display mode, actor/identity, target authorization,
and the resulting decision as separate fields. An alert, finding, chat message,
notification click, acknowledgement, mode selection, or model response is data;
none is accepted as an authority token.

### Isolation and actionable-alert invariant

- [x] The feature gate prevents Charlie route, UI, worker, trigger, finding,
  listener, and installation initialization when unavailable.
- [x] An installed disabled integration rejects sessions, triggers, findings,
  central work claims, evidence retrieval, MCP calls, and action execution.
- [ ] A feature- or connection-disabled integration serves administration from
  local state only, quiesces the agent/listener/bridge, and produces zero Charlie
  process, listener, timer, DNS, heartbeat, or packet activity. An enabled
  connection at operational wire mode `disabled` may retain only the minimal
  authenticated signed control heartbeat and typed enable/rotate/disconnect/
  uninstall messages; it performs no discovery, model, RAG, evidence, MCP,
  session, trigger, finding, claim, action, or remote diagnostic traffic.
- [x] `read_only`, `approval`, and `auto` are hard ceilings over the same live
  Astronomer RBAC/resource-scope checks, never privilege-bearing roles.
- [x] The higher modes are cumulative: `approval_required` retains all
  `read_only` behavior, and `automation` retains both read-only and exact
  user-approval workflows while adding only separately eligible automatic work.
- [x] Every write is non-destructive, action-ID idempotent, preconditioned,
  bounded, audited, and product-post-verified; missing disclosure fails closed.
- [x] In an active non-disabled mode, when execution is not permitted, a material
  diagnosis becomes a
  deduplicated actionable finding/alert with evidence, impact, a safe next step,
  verification, exact denial reason, approval link when eligible, and deep link.
- [x] Finding acknowledgement never counts as action approval, and a mode change
  never grants underlying target permission.

### Operational mode and notification contract

Charlie is an isolated subsystem, not ambient Astronomer behavior. The feature
flag, connection activation, local emergency latch, and verified central mode
are independent deny-first controls. Enabling the feature only makes the admin
configuration surface available; it does not install an agent, open egress,
start background work, or grant a capability. Activating a signed connection
installs the product agent in `disabled`, and an administrator must explicitly
choose a higher mode.

| Effective state | Reads | Writes | User-visible outcome |
| --- | --- | --- | --- |
| Feature unavailable or false | None | None | Charlie routes and navigation are absent; no agent, listener, trigger, worker, or egress path is initialized. |
| Feature or connection disabled | Locally persisted administration/configuration only | None | Admin status explains how to enable the connection; no Charlie process, listener, timer, heartbeat, DNS lookup, or packet exists. |
| Connection enabled; wire mode `disabled` | Signed control heartbeat only; local diagnostics cause no remote request | None | Admin status can enable, rotate, disconnect, or uninstall. All intelligence, product-data, Product MCP, and work traffic is rejected. |
| `read_only` | Disclosed reads allowed by the initiating user's live Astronomer RBAC and exact resource scope | None | Charlie returns the diagnosis. If remediation is warranted, Astronomer creates a deduplicated actionable finding that explains the blocked write and the safe operator next step. |
| `approval_required` (`approval`) | Everything allowed by `read_only` | One reversible, disclosed, idempotent action only after an exact unexpired approval and a final live authorization/precondition check | Astronomer displays an approval alert/card with impact, target, evidence, verification, expiry, and approve/reject controls. No response is treated as denial; acknowledgement is not approval. |
| `automation` (`auto`) | Everything allowed by `read_only` | Everything allowed by `approval_required`, plus an exact centrally allowlisted, product-disclosed, auto-eligible capability/resource pair under the narrow automation identity | Successful automatic actions produce a bounded result and verification record. Work that is not automatic either enters an eligible exact user-decision flow or produces an actionable finding; it never broadens automation authority. |

`approval_required` and `automation` are product-facing names. The v1 bridge and
database enums remain `approval` and `auto` for contract compatibility. The UI,
documentation, audit outcome, and tests must use the product-facing meaning and
must not imply that choosing a higher ceiling grants authority by itself.

```text
disabled < read_only < approval_required < automation
effective ceiling = least(requested local ceiling, verified central ceiling,
                          emergency/local runtime ceiling)
```

The ordering controls only which workflow may be considered. Every read, exact
approval, and automatic action must still independently satisfy live identity,
RBAC, affected-resource scope, disclosure, target, safety, fencing, and
idempotency checks. A denial at any layer always wins.

Every transition toward more authority requires an explicit administrator action
and authoritative readback. A stale revision, missing acknowledgement, restart,
agent failover, central outage, or disagreement between local and central state
can only preserve or reduce authority. Disable and emergency-disable close and
drain the local write fence before success is reported, abort active turns, stop
trigger claims, and leave configuration/status as the only reachable surfaces.

#### Destructive-action and confused-deputy protections

- Destructive or irreversible capabilities are rejected when the catalog is
  compiled and therefore cannot be made available by approval, `auto`, a model
  request, prompt content, or an Astronomer role.
- The v1 catalog has no arbitrary shell, command, exec, Kubernetes proxy, raw SQL,
  generic HTTP, Secret-read, delete, credential-rotation, or downstream-tunnel
  capability. New effects require a reviewed typed adapter and contract change.
- Every permitted write names one canonical management-plane resource, declares
  expected impact and rollback, carries a caller-stable action ID, has bounded
  arguments and timeout, checks product-owned preconditions, and is post-verified
  by Astronomer. Unknown or ambiguous outcomes are reconciled, never replayed.
- Charlie output, retrieved documents, product context, alert text, and tool
  results are untrusted evidence. They can propose a typed capability but cannot
  select an undisclosed target, change mode, create RBAC, approve an action, or
  weaken a budget, cooldown, maintenance window, or circuit breaker.
- A final live gate immediately before dispatch rechecks feature and connection
  state, mode revision, disclosure digest, actor/delegation, exact resource RBAC,
  approval or automation grant, fencing epoch, action reservation, and safety
  preconditions. Any mismatch denies execution and records a coded reason.

#### Actionable alert contract

Astronomer owns delivery and authorization for Charlie notifications. A central
finding becomes an Astronomer alert only after it is durably committed and only
users who can read every affected resource may view it. The bounded notification
contains severity and impact, affected Astronomer resource links, an evidence
summary and timestamps, the exact reason Charlie did not act, a recommended safe
next step, the proposed typed capability and expected impact, the verification
plan, repeat count, and a deep link to the investigation. It contains no prompt,
chain-of-thought, raw model/tool body, secret, credential, certificate, or
authorization reference.

In `approval_required`, or in `automation` when a non-automatic action remains
eligible for exact human approval, the deep link may expose an exact approval
card. For `read_only`, destructive work, or a safety/RBAC/scope denial that may
not be overridden, it provides operator guidance but no execution control.
Every actionable finding offers the decisions valid for its current state:
acknowledge, dismiss, resolve, review exact approval, or follow bounded manual
checks. Those decisions are server-authorized and idempotent; acknowledging,
dismissing, or resolving never changes mode or grants execution authority.
Repeated diagnoses update one deduplicated
finding and may feed Astronomer's existing in-product and configured external
notification channels; notification delivery failure never loses the durable
finding. Central and product audit records correlate by opaque deployment,
session, finding, approval, and action IDs while logging only coded outcomes,
counts, timings, and content-safe metadata.

#### Advisory lane and execution lane separation

Charlie findings, alerts, chat replies, and recommendations are advisory data,
not delayed commands. Astronomer must keep their ingestion and presentation path
structurally separate from the Product MCP write-dispatch path:

```text
diagnosis -> durable Charlie finding -> authorized Astronomer alert -> user review
                                                               |
                                                               +-> manual checks
                                                               +-> separate exact approval request

exact approval or automation grant -> fresh live authorization -> action reservation
                                  -> required audit admission -> typed adapter
                                  -> product verification -> durable result
```

The alert may state that its workflow is `approval_pending`, but it must not
carry even an approval identifier. The separately authorized detail path loads
the exact current approval from the approvals lane. An alert must never carry an
executable manifest, signed authority, product authorization reference, exact
arguments, or a replayable action request. Opening, clicking, acknowledging,
dismissing, assigning, snoozing, or resolving an alert cannot enter the execution
lane. “Approve” is a distinct authenticated and CSRF-protected operation that
loads the current server-side manifest, rechecks the actor and target, shows the
exact bounded impact, and requires an explicit confirmation.

Alerts are useful in every active mode without weakening it. In `read_only`, a
material issue produces diagnosis, operator checks, and verification guidance
only. In `approval`, an eligible safe action may additionally expose one exact
approve/reject workflow. In `auto`, Charlie performs only an exact allowlisted
action under the automation identity; a safe non-auto action remains eligible
for the same human approval workflow, and everything else remains manual
guidance. Disabled states produce no runtime diagnosis or alert traffic.

Astronomer owns alert visibility, routing, escalation, quiet hours, recipient
preferences, and configurable trigger thresholds. Notifications must be
deduplicated, severity-aware, bounded, deep-linked, and useful during a Charlie
or notification-channel outage because the durable finding remains the source
of truth. A notification delivery failure may delay visibility but cannot lose,
approve, execute, or resolve the finding.

Required operational logging is deny-first. Before any authority increase or
product write, Astronomer must durably admit a content-free audit record or
reservation in the same transactional boundary that authorizes dispatch. If
that required admission fails, no write is sent. Ordinary service logs use typed
event/outcome/reason codes, opaque correlation IDs, bounded counts and durations,
and an allowlist of fields; raw prompts, evidence, model/tool bodies,
arguments/results, secrets, authorization references, provider errors, resource
values, and arbitrary exception text are forbidden. Read-only diagnosis may
degrade safely when an optional telemetry sink is unavailable, but it must never
silently claim an action, approval, notification, or audit write succeeded.

- [x] **A-ALERT-001** Make finding/notification payload schemas structurally
  incapable of carrying an executable manifest, authority token, product
  authorization reference, exact tool arguments, or reusable action request.
- [x] **A-ALERT-002** Prove every notification interaction other than the
  separately authenticated exact-approval endpoint has zero action-dispatch
  side effects, including replay, concurrent clicks, stale cards, and forged UI
  payloads.
- [x] **A-ALERT-003** Add configurable severity, dedupe, escalation, quiet-hour,
  recipient, and channel policy without moving thresholds or user-notification
  credentials into Charlie central.
- [x] **A-ALERT-004** Make required product audit admission fail closed before
  every authority increase and write dispatch; prove audit outage causes no
  Product MCP write and leaves one actionable, retry-safe coded outcome.
- [ ] **A-ALERT-005** Centralize typed allowlisted operational log fields across
  the Astronomer API, workers, Product Bridge client, Product MCP, qualification
  tooling, and notification delivery; prove secret/content sentinels never reach
  logs, audit, metrics, traces, crash output, or support bundles.
- [x] **A-ALERT-006** Prove alert behavior by effective state: none while feature
  or connection disabled; control-status only in wire `disabled`; manual guidance
  in `read_only`; exact approve/reject or manual guidance in `approval`; verified
  automatic result, exact fallback approval, or manual guidance in `auto`.

Charlie `1.0.22` alert and audit-admission evidence: Astronomer owns a
revisioned alert policy for severity, dedupe, escalation, quiet hours, and
references to its existing notification channels; recipient destinations and
credentials remain in the product's channel subsystem and never enter Charlie.
Finding-to-delivery creation and its task outbox are atomic, reconciliation is
idempotent, an unscoped partial finding is ineligible, and delivery rechecks the
live connection, emergency latch, requested/verified modes, policy revision,
severity, channel mapping, and channel state immediately before provider I/O.
The worker holds the same distributed PostgreSQL write fence used by Product MCP
from claim through provider return and terminal persistence. Feature disable or
a downward authority drain waits for an in-flight alert call and rejects queued
delivery before database claim or HTTP; separate API/worker pools proved the
shared/exclusive advisory-lock boundary against real PostgreSQL.

Product action admission now requires the bounded product audit record before
authority consumption or dispatch at proposed, approved, and dispatched
phases. Audit failure returns only `audit_unavailable`, commits or reconciles one
retry-safe actionable finding in active modes, transitions any existing receipt
to a terminal blocked state, and makes zero Product MCP calls. Cancellation,
emergency disable, and downward-mode races win before or during audit, and
replay revalidates current state without duplicate authority or findings. The
pinned 19-scenario plus 360-case Charlie conformance corpus drives the real
Astronomer guard, catalog, receipt, executor, verifier, and MCP-header boundary
and asserts all eight typed stages plus zero unintended effects.

`make sqlc-check`, the pinned contract drift gate, the full non-race and race Go
suites, the API contract, 789 frontend tests/type-check/build/audit, and both
development and production Helm renders pass. The alert-policy CAS, mandatory
finding-scope query, and cross-process delivery fence also pass under `-race`
against a disposable real PostgreSQL database. A-ALERT-005 stays open for the
complete cross-process content-sentinel matrix; this source evidence does not
substitute for that live-only proof.

Source effective-state alert acceptance evidence — 2026-08-06: the table-driven
`TestAAlert006EffectiveStateProducesOnlyThePermittedOutcome` and
`TestAuthorityMatrixRolesTargetRBACAndCumulativeModes` suites drive the signed
product ActionGuard through feature absent, connection inactive, wire disabled,
`read_only`, `approval`, and `auto`. Inert states persist and publish nothing;
active non-execution outcomes persist exactly one bounded finding and alert;
approval exposes only the separate exact decision lane; and successful approval
or allowlisted automation produces one verified result without a finding. The
role/target matrix covers viewer, operator, approver, administrator, and the
separate automation identity with allowed and revoked target authority.

Post-dispatch failure evidence is also source-complete. Verification failure,
ambiguous receipt, and caller cancellation after a possible side effect persist
a coded terminal receipt and deduplicated actionable finding through a bounded
`context.WithoutCancel` cleanup context. Receipt replay retries only missing
finding persistence and never re-invokes the adapter. Finding publication and
provider alert failures retain their durable rows for idempotent retry. Existing
advisory concurrency tests prove acknowledge, dismiss, resolve, remediation,
verification request, replay, and stale/forged interaction cannot call approval
or action dispatch; mode changes have no action-dispatch dependency. The
table-driven destructive/confused-deputy corpus covers all modes and roles and
replays shell, exec, raw SQL, proxy, Secret, delete, downstream transport,
forged-authority, prompt-injection, and target-substitution attempts with zero
adapter/receipt/authority consumption and only coded content-free audit/log
outcomes. Packet capture, process absence, and the complete cross-process
sentinel matrix remain live gates below.

Charlie `1.0.21` contract-alignment evidence: the pinned strict finding schema,
generated bridge types, Astronomer public OpenAPI, generated Go SDK, and generated
frontend types expose advisory finding detail without any approval identifier,
manifest, signature, authorization reference, exact arguments, or action
request. The handler accepts only `request_id` on finding transitions. Focused
tests forge every forbidden field across every current finding interaction and
prove rejection before service entry; live-resource revocation, idempotent
replay, and twelve-way concurrent interaction tests cover acknowledge, start
remediation, request verification, dismiss, and resolve while asserting zero
approval and zero action-dispatch calls. The focused Go and race suites,
frontend tests/typecheck/lint, pinned-contract drift check, and API-contract gate
pass. This completes A-ALERT-001 and A-ALERT-002 only; the policy, complete audit,
complete logging, and live mode-matrix gates remain open.

#### Required isolation, authority, and alert acceptance matrix

- [ ] Prove feature false and connection disabled are cold states: all Charlie
  routes/workers/listeners/schedulers are unregistered or stopped, the agent is
  absent, and packet/counter observation records zero Charlie runtime egress.
- [ ] Prove enabled wire mode `disabled` accepts only the signed control protocol
  and cannot create a session, finding, alert, evidence request, MCP call, or
  model/RAG request.
- [x] For each user role and target-RBAC combination, prove `read_only` performs
  only authorized reads and turns every material proposed write into one
  authorized, deduplicated manual-remediation finding with zero action receipt.
- [x] For each user role and target-RBAC combination, prove `approval` retains
  read-only behavior and dispatches only after an eligible actor approves one
  exact, current, unexpired action; reject, expiry, revocation, replay, silence,
  alert acknowledgement, and mode change execute nothing.
- [x] Prove `auto` retains both lower workflows: an exact allowlisted action uses
  only the narrow automation identity, an otherwise-eligible safe action becomes
  an exact approval, and every other material diagnosis becomes actionable
  manual guidance. No blocked condition is silently dropped.
- [x] Run a destructive/confused-deputy corpus in every mode and role combination
  covering shell, exec, raw SQL, generic HTTP/proxy, Secret/credential access,
  delete, downstream-cluster transport, forged authority fields, model/prompt
  injection, target substitution, and replay; assert denial before dispatch,
  zero product side effect, and a content-free coded audit outcome.
- [x] Inject audit, finding-delivery, alert-delivery, and post-verification
  failures. Required audit failure must fail closed before a write; notification
  failure must retain the durable finding for retry; ambiguous action results
  must reconcile rather than execute again.
- [ ] Secret/content sentinel tests must cover success, denial, malformed input,
  provider/agent/central outage, timeout, cancellation, panic recovery, and
  support-bundle paths and prove no prompts, evidence, arguments/results,
  credentials, raw errors, or sensitive resource values reach logs, audit,
  metrics, traces, or notifications.

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

### 4.1a Cross-system correlation ownership and retention

The same opaque correlation value may appear on both sides of the Product
Bridge, but that does not make the underlying record jointly owned. Each side
deletes only its own record under its own retention rule; a missing remote
record is a normal expired/not-found state and never authorizes reconstruction
from logs or audit. Correlation fields are identifiers only and must never be
used as bearer credentials or RBAC evidence.

| Correlation | Authoritative owner | Astronomer record | Charlie record | Retention and deletion rule |
| --- | --- | --- | --- | --- |
| installation/package | Astronomer owns the active local installation and trust material; Charlie owns the signed onboarding package and credential lineage | `charlie_connections`, immutable package digest, public fingerprints, local encrypted trust | onboarding package, enrollment/runtime credential lineage | Charlie expires or revokes the package/credentials. Astronomer retains the active connection until verified disconnect, then deletes local trust through the explicit disconnect workflow. Neither side copies the other's secret. |
| deployment | Charlie owns the central deployment scope; Astronomer owns its local product-installation binding | deployment ID on `charlie_connections` | `scopes` and integration rows | Charlie scope lifecycle controls central cascade. Astronomer removes its binding only after credential revocation is verified; local audit remains under platform audit retention. |
| route | Charlie | route ID/revision pin only | gateway route and immutable revision | Charlie route/configuration retention applies. Astronomer replaces only the pin after a newly validated package or configuration readback and stores no model/provider/RAG configuration. |
| session | Charlie owns encrypted conversation/history; Astronomer owns user/resource authorization metadata | `charlie_sessions` plus constrained resource rows, with no message content | `agent_sessions` and encrypted messages/history | Charlie assigns an explicit history expiry of 1-30 days and purges the encrypted session tree. Astronomer removes terminal local metadata after 30 days. Audit records are not cascaded with either session. |
| turn/event cursor | Charlie | last acknowledged event ID and central revision only | turn commands and append-only transport/session events | Charlie turn data expires with its session. Astronomer's cursor expires with local session metadata. Reconnect resumes from the cursor; it never recreates content from audit. |
| finding | Charlie owns detailed evidence/workflow; Astronomer owns notification visibility and bounded summary | `charlie_findings`, affected resources, notification/alert correlation | `agent_findings` and append-only finding events | Charlie enforces the finding's explicit 1-90-day expiry. Astronomer expires the card at the advertised deadline and deletes terminal summary metadata after 90 days. |
| approval | Charlie | opaque approval ID, eligibility/state projection, and product decision correlation only | exact encrypted approval, argument/authority digests, expiry, consume-once state | Charlie expires the exact approval independently and deletes it with session history. Astronomer never stores the signed manifest or exact arguments; its projection expires with the finding/session metadata. |
| action | Charlie owns the proposal/authority decision; Astronomer owns product-side admission and effect receipt | `charlie_action_receipts`, digest, fence, product operation ID, state | `agent_actions` and `agent_action_reservations` | Charlie deletes action content with session history. Astronomer retains the content-free receipt with local session metadata for 30 days; ambiguous receipts are reconciled, never blindly replayed. |
| product operation | Astronomer subsystem that performed the idempotent operation | existing Argo/task/workload operation row keyed by the Charlie receipt operation ID | opaque operation/result correlation only | The owning Astronomer subsystem's normal operational retention applies. Charlie cannot delete, retry, or reinterpret the product operation record. |
| request/idempotency | Receiver of each mutation | finding-decision, approval-decision, receipt, and lifecycle request IDs | command, finding transition, approval, and action request IDs | Retained with the receiver's parent workflow record. Reuse with different scope/content is a conflict; identical replay returns prior state without a second side effect. |
| audit | Each system independently | normal Astronomer append-only audit with opaque/hash-only correlations | Charlie content-free lifecycle/retention audit | Astronomer follows `audit_log_retention_months`; Charlie follows its central audit policy. Business-record deletion never deletes the other system's audit and audit cannot restore expired content. |

The integrated live gate must select one session and trace its installation,
deployment, route, turn, finding/approval/action when present, request, product
operation, and both audit correlations without decrypting or printing content.
It must then prove that each record is readable only from its owner and that an
expired/deleted record is reported as unavailable rather than synthesized from
the peer.

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
- [x] **A1-011** Use one backend-owned connection-runtime gate for feature false
  and inactive connection. Apply it before constructing or starting the agent
  runtime, Product Bridge client, SSE stream, MCP listener, scheduler,
  dispatcher, consumer, retry loop, or central-authority reconciler; individual
  callers must not infer activation. Apply a second deny-first work-authority
  gate to operational `disabled`, emergency disabled, and all higher-mode work.
- [x] **A1-012** Make feature/connection disable drain writes, stop every Charlie
  process/listener/timer/socket, and reach zero network activity. For an enabled
  connection at operational wire mode `disabled`, retain only a signed,
  replay-protected control heartbeat and typed enable/rotate/disconnect/uninstall
  control messages. Serve detailed diagnostics locally; restart, reconnect, or
  central readback must never raise authority or originate intelligence/work.

**Verify**

- [x] `make verify` exits 0 with the pinned contract.
- [x] Generated contract drift intentionally fails CI.
- [x] No package outside the internal bridge client imports a central Charlie
  transport.
- [x] With `feature.charlie=false`, route gating, background worker dispatch,
  database-write guards, listener/workload teardown, and absence of Charlie
  Service/NetworkPolicy ports prove zero Charlie runtime activation. Secure
  status, emergency-disable, uninstall, and disconnect administration remains.
- [ ] With the feature/connection disabled, packet capture and process/listener/
  timer assertions prove zero Charlie runtime or network activity. With an
  enabled connection in authoritative wire mode `disabled`, prove the signed
  control heartbeat allowlist is the only traffic and every work counter remains
  zero while local administration works.

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
- [x] `make check-migrations && make sqlc-check` exits 0.

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
- [x] **A5-013** Reconcile newer central authority revisions on a bounded
  background interval and on admin status reads without raising the requested
  local mode ceiling. Reject stale and same-revision/mismatched state, map a
  disabled product/deployment boundary to verified disabled, and preserve the
  local emergency-disable latch as the highest-priority denial.

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
- [x] **A6-013a** Treat auto readiness as satisfied only by an exact global
  resource/verb grant from the published auto-eligible write catalog. Wildcards,
  read-only permissions, and cluster/project-scoped grants do not qualify.
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
- [x] **A6-019a** Derive destructive classification from the product-owned typed
  catalog and deny it before mode, approval, allowlist, or model-supplied facts
  are considered; catalog and authority-policy tests pin this precedence.
- [x] **A6-019b** Add registration and dispatch invariants that reject a
  destructive/irreversible descriptor entirely, even when Charlie labels it
  reversible, supplies an approval, requests `automation`, or spoofs effect,
  risk, rollback, verification, or idempotency fields.
- [x] **A6-020** Implement cumulative hard-ceiling evaluation. In
  `approval_required`, reads and exact user approvals are possible; in
  `automation`, those same paths remain possible and only a distinct,
  explicitly auto-eligible service-identity path may execute automatically.
- [x] **A6-021** Keep the two write authorities unambiguous in `automation`:
  user delegation plus exact approval can consume only that approval, while the
  automation service identity can consume only its exact target grant/budget.
  A user request must never be reclassified as service automation, and a blocked
  automatic request must never silently become approved.

**Verify**

- [x] MCP protocol and schema conformance tests pass.
- [x] An exhaustive authorization matrix covers user/service identities,
  management components, downstream-agent metadata records, each Charlie mode,
  approval intersection, disclosure drift, expiry, and fencing.
- [x] Concurrent duplicate writes execute the underlying operation exactly once.
- [x] Adapter-interface and registration tests prove MCP discovery and calls
  have no downstream-agent tunnel dependency or capability.
- [x] A mode-inclusion matrix proves each higher ceiling preserves every lower
  read/decision workflow without widening RBAC, target scope, or execution.
- [x] Crafted destructive requests fail at catalog registration, discovery,
  pre-receipt evaluation, and final dispatch in every mode and create no side
  effect, budget consumption, or reusable approval.

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
  Encoded responses compact oldest lines first, retain newest evidence, and
  expose requested/returned counts plus an explicit truncation marker.
- [x] **A7-007** `astronomer.management.nodes`, `.storage`, and `.network` — bounded
  management-cluster capacity/pressure, PVC health, ingress/service/network-policy
  status, and no arbitrary resource-kind or GVR input.
- [x] **A7-008** `astronomer.database.health` — connection-pool, replication,
  storage, and failover signals without raw SQL or credentials.
- [x] **A7-009** `astronomer.queue.health`, `.tasks`, `.task_get`, and
  `.failed_tasks` — bounded Asynq/Redis health plus pending, active, scheduled,
  retry, and dead-letter diagnostics. Charlie receives task purpose,
  retry/timing/orphan state, payload field names, and fixed failure categories,
  but never payload values or raw error strings.
- [x] **A7-009a** `astronomer.catalog.repositories` — correlate
  `catalog:sync` tasks with safe repository sync state, last attempt/success,
  endpoint scheme/host, and fixed failure category without URL paths, query
  parameters, credentials, encrypted auth configuration, or raw errors.
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
- [x] **A7-014a** Add fixed, bounded work-pipeline diagnostics:
  `astronomer.task_outbox.summary|list|get`, `astronomer.scheduler.health`,
  `astronomer.controllers.summary|alerts`, and per-domain operation list/detail
  tools for catalog, Argo CD, tools, monitoring, logging, and workloads. Expose
  lifecycle state, attempts, timing, payload field names/size, and fixed failure
  categories only; never payload values or raw errors.
- [x] **A7-014b** Add management runtime diagnostics for owned Jobs/CronJobs,
  DaemonSets, HPA/PDB availability, Ingress/EndpointSlice health, pod resource
  usage, PostgreSQL performance/pool state, Redis safe INFO fields, aggregate
  HTTP status-class metrics, and process/runtime metrics. No arbitrary metric
  query, Redis key/value operation, SQL, GVR, selector, or non-owned Kubernetes
  object is accepted.
- [x] **A7-014c** Add secret-safe administrative coverage for outbound delivery,
  logging, monitoring, identity/Dex/SCIM, RBAC, security, governance,
  configuration overrides, tenancy, GitOps registration, UI extensions,
  alerting, dashboards, platform inventory, and product-local Charlie runtime
  health. These are fixed aggregate projections; identities, free-form content,
  endpoints, policy documents, credentials, and raw errors are withheld.
- [x] **A7-014d** Add `astronomer.audit.search` with exact filters, bounded
  lookback, and pagination while continuing to withhold actors, paths, IPs,
  user agents, detail JSON, and managed-cluster evidence.
- [x] **A7-014e** Complete the management-domain coverage audit with fixed
  health projections for authentication sessions/TOTP/recovery, live key
  rotation, cloud-credential/Vault integration state, controller policies and
  silences, template/baseline reconciliation, registration/decommission/agent
  lifecycle, fleet-operation orchestration records, catalog ingestion/hydration,
  and repair/idempotency bookkeeping. Require wildcard read RBAC for every
  cross-domain summary and continue to withhold identities, secret/content
  documents, operation specifications, target details, and raw errors.

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
- [x] **A8-005a** `astronomer.task_outbox.retry_delivery` — approval-only retry of
  one failed or dead durable delivery row. Refuse delivered, pending, and
  in-flight rows; clear only delivery lock/error state; and verify the exact row
  returned to pending. It is never auto-eligible.
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
- [x] **A10-016** Add responsive, keyboard, screen-reader, reduced-motion, and
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
- [x] **A10-020** Render the applicable decision set on every actionable
  non-execution finding: acknowledge, dismiss, resolve, review exact approval,
  approve, deny, or follow bounded manual checks. Explain why unavailable
  decisions are unavailable; do not present destructive or policy-ineligible
  work as approvable.
- [x] **A10-021** In `automation`, distinguish an automatically eligible action,
  an exact human-decision action, and a non-executable recommendation. The UI
  must never imply that a blocked automatic action will run, that acknowledging
  a finding approves it, or that changing mode retries it.

**Verify**

- [x] Frontend unit tests cover every state and permission combination.
- [x] Playwright covers global launch from at least installation health,
  management component, alert, cluster-agent, backup, and self-management GitOps
  pages; context chips match the route resource and never imply downstream access.
- [x] Deep links survive refresh and browser back/forward.
- [x] Accessibility scan has no serious/critical violations.
- [x] Frontend tests cover each non-execution reason and assert its legal user
  decisions, confirmation flow, stale/expired behavior, and zero implicit action.

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
- [x] **A11-012** Present the cumulative product modes as **Read only**,
  **Approval required**, and **Automation**, while mapping them to the stable bridge
  values `read_only`, `approval`, and `auto`. Explain that each is a maximum
  authority ceiling, show what lower workflows remain available, and never label
  the selection as a grant.
- [x] **A11-013** In disabled state, render connection/configuration from local
  data and show the agent/runtime/network paths as quiesced. A status-page refresh
  must not poll Product Bridge or Charlie central until activation is requested.

**Verify**

- [x] All tabs deep-link and refresh correctly.
- [x] Non-admin users cannot call admin APIs directly.
- [x] No read response or DOM text contains onboarding, registry, certificate
  private key, enrollment, or runtime token material.
- [x] Administration tests prove disabled status is local-only.
- [x] Administration tests prove the three product mode labels/effects match the
  cumulative backend decision matrix while sending only stable wire values.

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
- [x] **A12-029** Normalize every non-execution outcome into one bounded reason
  vocabulary, including read-only ceiling, approval required/rejected/expired,
  non-auto-eligible, allowlist, RBAC, target scope, disclosure drift, budget,
  cooldown, maintenance window, precondition, circuit breaker, fencing,
  idempotency conflict, ambiguous prior attempt, failed execution, and failed
  verification. Unknown reasons fail closed and offer no execution control.
- [x] **A12-030** For every material diagnosis in an active non-disabled mode
  where automatic execution is not allowed, durably create/update one authorized
  actionable finding and alert. Disabled remains inert and creates no new work.
  Include the coded block reason, impact, bounded evidence summary, affected
  resource, safe operator checks, verification plan, repeat/timeline metadata,
  and exactly the user decisions valid for that state.
- [x] **A12-031** Implement the decision workflow for blocked automation: offer
  exact approve/deny only when the same disclosed reversible action remains
  eligible for human approval; otherwise offer acknowledge/dismiss/resolve and
  bounded manual checks. Re-evaluate mode, expiry, RBAC, target scope, and safety
  at decision time; no finding action implicitly executes or widens authority.
- [x] **A12-032** Audit and publish each finding decision as a content-free,
  idempotent lifecycle transition. Notification delivery retries may not
  duplicate the finding, approval, decision, or action, and a delivery failure
  may not lose the durable operator workflow.

**Additional verification for non-execution workflows**

- [x] A table-driven test covers every A12-029 reason in all applicable modes
  and asserts the exact finding state, alert eligibility, available decisions,
  absence/presence of an approval link, and zero implicit side effects.
- [x] Multi-user tests prove only currently resource-authorized users receive or
  open the finding/alert and that permission revocation closes subsequent detail
  and decision requests.
- [x] Decision concurrency/replay tests prove acknowledge, dismiss, resolve,
  approve, deny, expiry, and notification retry are idempotent and mutually
  consistent across Astronomer and Charlie.

Source completion evidence — 2026-08-06: Astronomer migration 154 binds each
approval decision to `(connection_id, decision_request_id)`, actor, decision, and
rationale digest with one concurrency winner and exact replay. Charlie `1.0.23`
binds the forwarded idempotency key to the same decision and commits approval,
action, session resume, safety reservation, signed execution ticket, and encrypted
permission outbox together. Restart, prepare/enqueue/commit failure, exact replay,
altered replay, approval expiry/rejection, and approve-vs-reject tests prove no
lost or duplicate execution. Finding lifecycle concurrency and notification
outage tests independently prove that every advisory interaction has zero action
dispatch side effects.

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
- [x] **A13-015** Define a machine-readable audit coverage matrix for connection,
  onboarding, trust, install/uninstall/upgrade/rollback/rotation, mode and
  emergency disable, session and visibility, trigger and finding lifecycle,
  approval decisions, bridge/MCP authorization, action admission/execution/
  verification/replay, disclosure drift, fencing, and every denial code.
- [x] **A13-016** Route every Charlie audit and operational log through one
  allowlist serializer. Permit only stable event/action names, coded outcomes,
  bounded enum fields, opaque correlation IDs, safe counts/timings, revisions,
  and public fingerprints/digests; reject unknown fields rather than applying a
  best-effort blacklist.
- [x] **A13-017** Prohibit prompts, responses, reasoning, evidence, citations,
  tool arguments/results, resource names, user-entered rationale, raw errors,
  URLs, authorization references, tokens, credentials, private certificate
  material, onboarding bodies, Secret data, and model/provider/RAG content from
  logs, audit, metrics, tracing, events, diagnostics, and support bundles.
- [x] **A13-018** Correlate product and Charlie records using opaque installation,
  deployment, route, session, turn, finding, approval, action, operation, request,
  and audit IDs without logging their associated content. Document which system
  owns each record and its retention/deletion behavior.
- [x] **A13-019** Make audit persistence a fail-closed precondition for authority
  changes, approval consumption, and write dispatch. If a required audit record
  cannot be durably committed, perform no side effect and emit only a bounded
  local failure metric/log.

**Verify**

- [x] Metrics cardinality tests and log/audit/support-bundle secret scans pass.
- [x] Network tests prove browser/public ingress cannot reach bridge or MCP.
- [x] Server/worker cannot reach central Charlie directly.
- [x] Only agent pods can reach MCP and only Astronomer server/worker pods can
  reach Product Bridge.
- [x] The A13-015 matrix has one success, denial, failure, replay, and redaction
  assertion for every applicable lifecycle transition and denial code.
- [ ] Property/fuzz tests inject secret and content sentinels into every inbound,
  outbound, error, cancellation, timeout, and malformed-response path and prove
  they are absent from all audit/log/metric/trace/event/diagnostic/support sinks.
- [x] Audit-storage failure tests prove no approval is consumed and no write is
  dispatched without its required durable, content-free audit record.
- [ ] Every runbook is exercised by a game-day or deterministic test.

A13 source completion evidence — 2026-08-06: the ownership/retention table in
section 4.4 assigns central and product records explicitly. Both systems now emit
closed `charlie.observability/v1` event/reason fields with opaque correlation
identifiers only; provider IDs are hashed, required audit admission shares the
authority transaction, and sink-failure tests roll mutations back. The complete
cross-process property/sentinel matrix and runbook game days remain separate
unchecked qualification gates.

### Phase A14 — Connected and air-gap qualification

**Depends on:** all prior phases.

#### Connected qualification against the separate Charlie server

- [x] **A14-000** Provide an operator-started, standalone live qualification
  hook documented in [charlie-live-qualification-hook.md](charlie-live-qualification-hook.md).
  Keep it out of production routes; require loopback, TLS 1.3, strong bearer
  authentication, an explicit live-effects acknowledgement, serialized runs,
  bounded inputs/outputs, baseline restoration, and honest unsupported results.
- [x] **A14-001** Configure the Astronomer product, route, versioned docs, and one
  isolated deployment in Charlie administration.
- [x] **A14-002** Download the signed onboarding package from Charlie.
- [x] **A14-003** Import it in Astronomer and install only the generic agent.
- [x] **A14-004** Pull chart/image exclusively from Charlie OCI.
- [x] **A14-005** Block direct server/worker access to Charlie central before
  functional testing.
- [x] **A14-006** Confirm both replicas, one leader, one standby, correct epoch,
  compatible versions, and MCP disclosure acknowledgement.
- [x] **A14-007** Validate grounded Astronomer-version answers and general
  Kubernetes passthrough answers.
- [ ] **A14-008** Validate visible context chips and authorized on-demand evidence.
- [x] **A14-009** Validate read-only read success and write denial.
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
- [ ] **A14-016e** For feature false, inactive, authoritative disabled, emergency
  disabled, and disable-during-work, capture process, queue, listener, DNS, and
  network activity and prove the integration is fully inert after bounded drain;
  local administration must remain usable without contacting Charlie.
- [ ] **A14-016f** Execute the cumulative ceiling matrix: `read_only` reads;
  `approval_required` retains reads and exact approve/deny; `automation` retains
  both and adds only service-authorized automatic execution. Prove every lower
  workflow still works and no higher workflow runs below its ceiling.
- [ ] **A14-016g** Exercise the complete content-free audit matrix with injected
  prompt, rationale, evidence, tool, error, credential, and Secret sentinels.
  Correlate every lifecycle outcome while proving no sentinel reaches any
  operational sink and audit failure prevents authority consumption/dispatch.
- [ ] **A14-016h** For every automatic-execution block reason, verify exactly one
  actionable finding/alert, the correct authorized user decision set, exact
  approval only when eligible, idempotent decisions, and zero implicit action.

#### Connected qualification evidence — 2026-08-05

##### Agent 1.0.20 exact replacement and fail-closed authority addendum — 2026-08-06

- Charlie runtime commit `8bed231` released agent/chart `1.0.20`; Charlie plan
  commit `004de7f` records the advisory/execution-channel isolation contract.
  Astronomer commit `c87790d` pins the exact 1.0.20 contract and was deployed as
  server, migrate, worker, and frontend tag `charlie-c87790d` after the full Go
  race suite, Go lint with zero issues, 99 frontend files/786 tests, TypeScript
  checking, frontend lint with zero errors, production build, and pinned-contract
  verification passed.
- Charlie issued one short-lived signed replacement package that pinned image
  digest
  `sha256:c712bb094a79865a34169ebc97a4144fb3c6e5a712eb5df67e31d65571baf019`
  and chart digest
  `sha256:7b20944a332bc0612513a725bd2acdae21531a00a3f28db7fc930af8df65a461`.
  Its public signing key was independently fetched through the TLS-protected
  signing-key endpoint and matched to the package fingerprint before Astronomer
  validated and consumed the package. Package/key/request/login files were mode
  `0600` in private temporary directories and removed from both hosts after
  consumption.
- Consumption staged the new connection and reconciled the signed artifacts,
  but did not activate it. The explicit `agent/upgrade` operation required the
  2/2-ready replacement, atomically activated generation 29, deactivated the
  prior connection, and pruned the superseded owner-bound Secrets. Only the
  current package's repository, enrollment, artifact-pull, bridge, MCP, and CA
  Secrets remained.
- Activation deliberately did not restore authority. The new active connection
  began with requested `disabled`, no acknowledged disclosure, and the inherited
  authoritative readback as a lower-ceiling mismatch. An exact disclosure
  acknowledgement was a separate audited operation; a subsequent explicit mode
  request at revision `76` produced authoritative `read_only/read_only` revision
  `77`, with emergency disable false. At no point did package import, pod
  readiness, restart, or activation alone raise effective authority.
- Direct mTLS status reads from both individual agent replicas proved agent
  `1.0.20`, central healthy, both activation bits true, effective `read_only`,
  revision `77`, and fencing epoch `28`. Ordinal 1's instance matched the leased
  leader; ordinal 0 observed that leader and was therefore the standby. Both
  pods were ready with zero restarts, and the self-management and Charlie agent
  Applications were `Synced/Healthy`.
- Public Astronomer and the separately deployed Charlie UI both returned HTTP
  `200`. This is positive exact-upgrade, HA identity, staged activation,
  authority-restoration, artifact-pruning, and secret-cleanup evidence. It does
  not check the broader A14 isolation/mode/alert matrices, destructive corpus,
  packet quiescence, or automatic-action qualifications.

##### Agent 1.0.21 advisory-isolation replacement addendum — 2026-08-06

- Astronomer commit `bae05f2` pins Charlie source commit
  `b9a488b66fbbda0e4a43ce707be123e1e4366474`, chart `1.0.21`, and the strict
  advisory finding contract that contains no approval ID, signed manifest,
  reusable authorization, action arguments, or execution authority. The exact
  approvals API remains a separate authenticated execution lane. Focused Go and
  race tests, the API and pinned-contract gates, generated OpenAPI drift check,
  and focused frontend test/type/lint/build gates passed before deployment.
- Astronomer consumed a short-lived signed replacement package that pinned image
  digest
  `sha256:4798c34a695b173eb9d687f1084b6cc3d135277efb59b775e1b6e3ca92e82b4c`
  and chart digest
  `sha256:c20dc013b39a32db51123e153654e4ba52521e38899b1c9230a3108beb65a4a6`.
  The public signing key and package fingerprint matched before validation and
  consumption. The staged connection started `disabled/disabled`; it did not
  inherit the old connection's read-only ceiling.
- Replacement activation initially returned `409` while the composite Argo,
  StatefulSet, bridge, central enrollment, leader/standby, protocol, health, and
  artifact predicate was still converging. The unchanged bounded retry returned
  `200` only after every predicate passed. Activation atomically made 1.0.21 the
  sole active connection and left 1.0.20 inactive at `disabled/disabled`; no
  readiness or authority check was relaxed.
- The active connection was then changed by two distinct audited administrator
  operations: acknowledge the exact current disclosure, followed by request
  `read_only` at the exact optimistic revision. Final state is active/ready
  `read_only/read_only` revision `78`, disclosure acknowledged, and emergency
  disable false. Approval and automation were never enabled during the upgrade.
- The live agent Application and Astronomer self-management Application are
  `Synced/Healthy/Succeeded`. Both digest-pinned agent pods are ready with zero
  restarts; server, worker, and frontend run exact tag `charlie-bae05f2`; both
  public health paths and the Charlie UI return HTTP `200`. Agent-log sentinel
  inspection found no bearer, authorization, API-key, token, package, or
  credential material. Durable audit contains coded onboarding, replacement,
  disclosure, requested-mode, and verified-mode outcomes.
- The configured bootstrap password Secret had drifted from the existing live
  administrator credential. Rather than reset an account, password, or token
  row, the operator minted one five-minute in-memory access JWT from the existing
  deployment signing Secret, bound it to the existing active administrator UUID,
  and used the normal authentication, superuser, RBAC, rate-limit, and durable
  audit handlers. The JWT and all mode-`0600` package/request files were deleted
  from both hosts immediately after activation. This was an explicit deployment
  break-glass deviation, not a Charlie capability; bootstrap credential drift
  must be repaired through the normal Astronomer credential-rotation procedure.
- This addendum proves the exact 1.0.21 replacement and advisory/execution-lane
  boundary only. It does not close A-ALERT-003 through A-ALERT-006 or the broad
  A14 disabled-packet, cumulative-mode, destructive-action, notification-outage,
  or complete content-sentinel matrices.

##### Feature-lifecycle isolation and recovery addendum — 2026-08-06

- Astronomer commits `c0c692b` and `163f046` corrected two fail-closed recovery
  defects found by the live feature-off test. An explicit emergency-clear now
  retries only a remote downgrade to `disabled`, verifies that readback, and
  keeps the local write fence closed if confirmation fails. Resume now
  reactivates only the retained connection row while preserving requested and
  verified `disabled` plus the emergency latch; it cannot restore prior
  authority.
- With `feature.charlie=false`, the public Astronomer health endpoint remained
  `200`, the Charlie agent Argo Application and StatefulSet were absent, and the
  agent namespace contained no pod. The two stable bridge/headless Service
  objects remained as inert configuration, without a running workload; this
  evidence does not claim packet-level quiescence.
- The authenticated Charlie status surface remained local and reported
  authoritative `disabled`, revision `52`, with emergency disable engaged.
  Re-enabling the feature restored the owner-bound Argo Application and exact
  signed agent artifacts at `2/2` ready, but the durable connection stayed
  `disabled/disabled` with the emergency latch engaged. Enablement therefore did
  not restore or raise authority.
- The operator then cleared emergency disable at the exact revision. The server
  reconciled Charlie central down to `disabled` before opening the local latch,
  returning local revision `53`. A separate explicit request and authoritative
  readback restored `read_only`; exact current disclosure acknowledgement left
  the final durable state active, ready, `read_only/read_only`, revision `56`,
  emergency disable false.
- The deployed server and migrate images are exact commit tag
  `charlie-163f046`; self-management and Charlie agent Applications are
  `Synced/Healthy`, the agent is `2/2` ready at signed version `1.0.15`, and the
  public Astronomer root/health and separate Charlie health endpoint return
  `200`.
- This completes workload teardown/recreation and fail-closed recovery evidence,
  but not A14-016e: listener/timer assertions, DNS/packet capture, central work
  counters, and the separate control-only operational-`disabled` observation
  window remain required.

##### Agent 1.0.16 isolation and exact-alert addendum

- Charlie commit `d52b0f1` released product agent/chart `1.0.16`. Astronomer
  commit `554ccab` pinned that reviewed release and installed immutable image
  digest
  `sha256:c0f331205ad8a8ae59a7e434edc222b84bcfe5f855f5b6cdaf6b3b5d7d474359`
  and chart digest
  `sha256:5df5d10f0dabb363e33f4cd581999716259a43e17948672ee02dbeecf9c24e6e`.
  The generic agent reached `2/2` ready and its Argo Application remained
  `Synced/Healthy`.
- A feature-off/on qualification removed the Charlie Application, StatefulSet,
  and pods while Astronomer remained healthy, then restored the retained
  connection only as inactive-authority `disabled` with the emergency latch
  still closed. Commits `c0c692b` and `163f046` make emergency recovery retry
  the remote disabled transition before opening the local latch and reactivate
  only a safely retained inactive connection; recovery never restores a former
  mode.
- With operational authority explicitly disabled and enrollment settled, a
  bounded 22-second packet observation on Charlie central's loopback proxy saw
  four authenticated heartbeat requests, zero command claims, and zero other
  runtime requests. Agent `1.0.16` does not enter the claim loop until both
  product and deployment activation are true. This is positive control-only
  evidence; the broader A14-016e matrix remains unchecked.
- Charlie commit `b7abb57` keeps a deduplicated finding identity but rebases its
  delivery linkage to the newest authorized session. Astronomer commits
  `b30ad7c`, `7908cb0`, and `d81e8f2` admit only the finding's hashed resource
  target and catalog capability, match exactly one currently authorized session
  resource, reject missing/ambiguous/substituted targets, replace inherited
  scope transactionally, and carry a repeated finding to the newest session.
  Central diagnosis/model content is discarded at this background boundary.
- Live server tag `charlie-d81e8f2` reconciled `Synced/Healthy` and the public UI
  returned `200`. Read-only session
  `7359b8dd-5572-443a-9e03-3c52cf8d9118` completed an authorized
  `astronomer.argocd.self_management_status` read and proposed
  `astronomer.argocd.self_management_sync`. Charlie blocked the write as
  `authority.read_only_write`; no Astronomer action receipt exists.
- The durable open medium finding
  `afinding_a99b2e143f3bf3e1460a3674a559c463` is visible through Astronomer's
  authenticated findings API, has repeat count `5`, links to that exact latest
  session, recommends `astronomer.argocd.self_management_sync`, and is scoped
  only to `self_management_application:astronomer`. This is the same feed used
  by the top-bar notification selector and Charlie findings deep link.
- Charlie's full `make verify` passed for both the control-only agent and the
  finding-rebase fix. Astronomer's full race-enabled `make test`, `make lint`
  with zero issues, focused Charlie/DB race tests, and `make verify` API-contract
  gate passed before deployment.

##### Agent 1.0.15 approval and actionable-alert addendum

- Astronomer commit `238b415` deployed as exact server, worker, migrate, and
  frontend tag `charlie-238b415`. The self-management application reconciled
  `Synced/Healthy`; the public UI and health endpoint returned `200`; the Charlie
  agent remained `2/2` ready at `1.0.15`; and authoritative `read_only` revision
  `51` survived the rollout.
- Charlie commit `690fce010a0ff494d5396d5db4d9b09da7346199` supplied
  signed agent/chart `1.0.15` artifacts. The replacement used immutable image
  digest `sha256:9a7760806155454e59e229216fee086853b32a397be0b92f48217fff780eb817`
  and chart digest
  `sha256:b01810c1cb8cd33aa2ddd937b8047337366b327fbba79909b075b2bd75e07373`;
  both agent replicas became ready and Argo reported `Synced/Healthy`.
- In approval-required mode, session
  `4b6c4cae-f573-4b5e-9645-40668008a5d0` proposed one exact
  `astronomer.argocd.self_management_sync` action scoped to
  `self_management_application:astronomer`. Charlie durably stored pending
  approval `aappr_75a9e0eff446f0a69b276c716ff7c074`; Astronomer's authorized
  approval list returned it as eligible and created one resource-scoped
  `approval_required` finding/alert.
- The operator explicitly denied that approval. Charlie persisted the approval
  and action as rejected, Astronomer persisted the exact rejected reservation,
  and the local finding transitioned to resolved with `approval_rejected`.
  No approval was consumed and no product action ran. The deployment was then
  restored to acknowledged `read_only` revision `51`.
- After the `238b415` rollout, read-only session
  `184c1adc-e6ff-42a0-bc8b-ebbb2b9e41b0` executed six disclosed management-plane
  reads and proposed one `astronomer.argocd.self_management_sync`. Charlie
  blocked the write as `authority.read_only_write` and created one central
  `read_only` finding. Astronomer authorized and persisted exactly one matching
  open medium finding scoped to `self_management_application:astronomer`; the
  blocked action had zero product execution receipts.

##### Agent 1.0.13 and authority-reconciliation addendum

- Astronomer commits `fe22a4d` and `b7a50eb940ac0d295a9906d8469510d3a5620514`
  added central-authority reconciliation and exact target-grant readiness.
  Charlie commit `f38c941647e08e31391714a37384f09e7b717d0d`
  supplied signed agent/chart `1.0.13` artifacts.
- The replacement install used immutable image digest
  `sha256:98e5877f92ddb50c590a1cb400f2ba682c9a053655e00d220ed5915a75a3c9c6`
  and chart digest
  `sha256:7827b632a6fde66e2f380e1781a4a36ce77e6d9ccb20d85803c3f9646f901efb`.
  Kubernetes reported both StatefulSet replicas ready and Argo remained
  `Synced/Healthy`.
- A fresh local connection imported central revision `36` but stayed effectively
  disabled because its requested local ceiling was disabled. Astronomer then
  explicitly moved to read-only at revision `37`, proving central reconciliation
  cannot independently elevate local authority.
- Auto initially failed closed until the dedicated service identity had an exact
  global `monitoring:update` grant matching the published capability catalog.
  Tests additionally reject wildcard, read-only, cluster-scoped, and
  project-scoped grants as auto prerequisites.
- With a temporary one-capability allowlist, auto revision `38` was reachable.
  A human chat requested that allowlisted write and received
  `authorization_denied`; no action was dispatched because its user delegation
  could not become `system:charlie-automation`. This qualifies the human/service
  isolation boundary but does not complete A14-011's successful service-trigger
  execution requirement.
- Final live state is acknowledged `read_only` revision `43` with an empty
  central auto allowlist, disabled automation identity, zero automation grants,
  and no temporary qualification role/binding. Transient onboarding and test
  credential files were deleted from both hosts.
- Astronomer API-contract verification passed after each change; Charlie's full
  1.0.13 `make verify` suite passed before publication.

- Charlie central commit `8fc943d` served the separately deployed control plane;
  Astronomer commit `39df916` served the product integration during live tests.
  The follow-up documentation-only Astronomer commit is `c63c787`.
- Signed replacement package `onboard_50551337e5838821aab322059f127b4e`
  installed agent/chart `1.0.12` from Charlie OCI by immutable image digest
  `sha256:4c4e268df498cda898dcb59c7421880bf50d0c477d7e7860dc32bbf7e3d9058c`
  and chart digest
  `sha256:85cb00256da8debd418edf8c52a9ab12416152cb9b453711dde8ae086cd5fa9b`.
- Argo reported `Synced/Healthy`; the StatefulSet reported two ready replicas;
  Charlie reported one fenced leader, one standby, compatible `1.0.12`
  artifacts, and an acknowledged mode-bound disclosure.
- Read-only sessions successfully used bounded fleet reads. An attempted
  `astronomer.queue.retry_task` was denied with `authority.read_only_write` and
  produced an actionable no-action finding.
- Approval session `8a7523e9-0681-40ff-8a0e-2b18fc6c1dc8` proposed one exact
  `astronomer.queue.retry_task`. Approval
  `aappr_953774e5f042cfcadeaf7d7c653aa2f9` was rejected by the operator; Charlie
  and Astronomer both persisted `rejected`, the finding resolved with
  `approval_rejected`, and Astronomer persisted zero execution receipts.
- Auto activation returned `409` without a separate live target grant even after
  the automation service identity was enabled. The identity was disabled again;
  no auto action ran. A future qualification must grant one deliberately bounded
  target before A14-011 can be checked.
- Disabled mode retained authenticated admin status but rejected new session
  creation with `503`. The live deployment was then restored to acknowledged
  `read_only` revision `33`; automation identity access remains disabled.
- `make verify`, the full Go and race suites, frontend lint/type-check (warnings
  only), 749 frontend tests, frontend production build, dependency audit, and
  Helm lint/render/contracts passed. The explicit live agent-identity acceptance
  remained skipped because `AGENT_IDENTITY_TEST_CONTEXT` was not set.

#### Agent 1.0.18 actionable-decision and replacement-lineage addendum

- Charlie commit `0fc7044` completed all six Product Bridge finding transitions
  and published signed agent/chart `1.0.18`. Astronomer commits `b334661`,
  `f2802e5`, and `dc9c617` added replay-safe product decisions, pinned the exact
  1.0.18 contract, and preserved finding/session provenance across signed agent
  replacement generations.
- The live Astronomer migration advanced cleanly to schema `151`. Argo reported
  the exact server/migrate pair `charlie-dc9c617` as `Synced/Healthy`, with no
  dirty migration state.
- Charlie OCI supplied immutable agent image digest
  `sha256:6d48abf1c75742e53467c6d05e1b22536d6ab9b54fdbb419b969eef661eb6e59`
  and chart digest
  `sha256:0c51726ec9b9c54533fef608c2422cdb2cb2ea7d96e88c77ee84b3954e41729f`.
  The product reported two ready replicas, one fenced leader, one standby,
  agent/chart `1.0.18`, and fencing epoch `23`.
- Astronomer rejected the first activation attempt because its local release
  contract still declared 1.0.17 even though the signed artifact digests were
  newer. No replacement invariant was weakened: Astronomer pinned 1.0.18,
  consumed a fresh signed one-time generation, waited for exact artifact
  readiness, and then activated it. Activation reset authority to
  `disabled/disabled`; an administrator separately acknowledged disclosure
  digest `sha256:11bb481770f495346d72eda2bcf2dc3ca637ca89e86ada6691536a0463c7bb26`
  and restored only `read_only/read_only` revision `61`.
- Replacement initially made a retained finding unreachable because the finding
  correctly retained its source credential-generation ID while authorization
  compared it to the new active row ID. Commit `dc9c617` now authorizes retained
  findings and sessions only when source and active rows share the complete
  signed installation/product/deployment/route/central/signer/logical-agent
  lineage. It never rewrites historical provenance. Regression tests prove the
  same lineage remains accessible and a different deployment is denied before
  delegation or Product Bridge access.
- Request `bfa19b24-574a-40cc-a1dc-e7acf58df0e7` then replayed the previously
  central-committed acknowledgement. The first post-fix call returned `200` and
  atomically wrote one local decision ledger row; the identical retry returned
  `200` through the replay path. Local audit contains exactly one `completed`
  and one `replayed` content-free `charlie.finding.acknowledged` event. Charlie
  central retains exactly one lifecycle event for that request, and Astronomer
  has zero Charlie action receipts for the qualification window.
- The resulting read-only finding is `acknowledged` in
  `manual_remediation_required` and exposes only `start_remediation` and
  `dismiss`. Authorized detail contains a bounded diagnosis, risk impact,
  evidence summary, operator check, manual prerequisites/steps/expected impact,
  and product-current-state verification. Recording lifecycle progress does not
  authorize execution.
- The full race-enabled Astronomer test suite, focused Charlie/SQLC tests,
  generated-query check, and zero-issue Go lint passed. Charlie's full
  `make verify` passed before artifact publication. Every transient signed
  onboarding package file was deleted from both servers after activation.

#### Content-free audit boundary addendum — 2026-08-06

- Astronomer commits `6f8225e` and `8da6fa7` add an embedded machine-readable
  Charlie audit contract with exact action names, resource types, typed fields,
  and applicable success/denial/failure/replay/redaction classes. Unknown
  actions, fields, enums, digests, counts, and arbitrary strings fail closed.
- Charlie action admission/reconciliation, session/finding/approval lifecycle,
  administrator mutations, authenticated HTTP mutations, matched read audits,
  and unauthenticated Charlie mutation denials now use the same content-free
  encoder. Charlie rows omit paths, query values, IP addresses, user agents,
  resource names/values, request/response bodies, prompts, evidence, rationale,
  raw errors, authorization references, credentials, and model/RAG content.
- Approval ineligibility retains user-facing guidance only in the response;
  audit receives stable codes such as `actor_inactive`,
  `approval_permission_denied`, or `target_permission_denied`.
- A live pre-authentication canary initially returned `401` without an audit
  row, proving authentication was outside the prior mutation-audit boundary.
  `8da6fa7` adds a content-free pre-auth denial observer and moves the normal
  mutation auditor ahead of write-scope enforcement while retaining
  authenticated actor context.
- Repeating the canary against deployed server/migrate
  `charlie-8da6fa7` returned `401` and persisted exactly one
  `charlie.http.mutation` row containing only `POST`, status `401`, duration,
  and outcome `denied`. The marked URL path/query, body, user agent, and source
  address were absent. Argo was `Synced/Healthy`, schema remained clean at
  `151`, Charlie remained `read_only/read_only` revision `61`, and the generic
  1.0.18 agent remained two-ready-replica healthy.
- Both revisions passed the complete race-enabled Go suite and zero-issue lint.
  The subsequent release work closes A13-015, A13-016, A13-017, and A13-019
  with the checked-in audit contract, closed serializer, sink canaries, and
  fail-closed persistence tests. A13-018 remains open until the cross-system
  ownership and retention map is complete and live correlation is exercised.

#### Cold isolation, safety, and actionable-alert implementation addendum — 2026-08-06

- The Charlie integration is now owned by one generation-serialized runtime
  lifecycle. A cold `feature.charlie=false` state constructs no bridge client,
  stream, dispatcher, scheduler, reconciler, timer, listener, or network path.
  Signed feature enable starts a fresh generation; feature disable, connection
  deactivation, emergency stop, or authority fall drains and closes the
  permitted work plane without a server restart. Operational disable closes all
  work but may retain the private configuration-only MCP discovery surface
  described below.
- Feature disable quiesces local admission before suspending the installed
  product agent. Operational wire mode `disabled` is a separate control-only
  state; `read_only`, `approval_required`, and `automation` remain cumulative
  ceilings rather than roles or sources of privilege.
- Every Charlie write now requires a content-free durable audit precondition
  before an authority change, approval consumption, reservation, or dispatch.
  The action guard re-reads live feature, connection, emergency, mode,
  disclosure, RBAC, scope, approval, allowlist, budget, cooldown, maintenance,
  precondition, circuit, fencing, idempotency, and verification state. Unknown,
  destructive, irreversible, and non-catalogued work is rejected before
  execution.
- Non-executed material diagnoses use the existing notification surface as
  deduplicated actionable findings with stable block codes, authorized deep
  links, bounded manual checks, or a separately confirmed exact approval only
  when currently eligible. Acknowledge, dismiss, resolve, reject, and expiry do
  not grant authority or retry an action; rejected and expired approvals remain
  in `manual_remediation_required` rather than becoming dead cards.
- The audit contract and tests enumerate the complete denial vocabulary and
  reject unknown fields. Operational logging is emitted only through the
  Charlie serializer, hashes external request/correlation identifiers, and has
  static, table/property, fuzz, sentinel, and audit-storage-failure canaries.
- Charlie central is pinned to exact commit
  `5beadfe55dd9798356c2dc72e127a88f28a049b4`. Its schema-15 upgrade seals
  existing v11 durable content before removing plaintext and is live separately
  at `charlie.dev.alphabravo.io`; Astronomer still communicates only through the
  colocated generic product agent.
- Repository qualification passed the full race-enabled Go suite, zero-issue
  Go lint, migration and API-contract checks, 778 frontend unit tests, frontend
  type/lint/build/code-health gates, 46 desktop/mobile browser tests, and all
  112 generated application routes plus two detector-honesty browser tests.
  Live deployment and mode-boundary evidence remains recorded separately below
  and is not implied by these repository results.

#### Live isolation, fail-closed upgrade, alert, and RAG qualification — 2026-08-06

- Astronomer commits `0ddff69`, `04a89e0`, and `7703daf` are pushed on
  `feat/charlie-core-integration`. The live server/migrate images carry exact
  revision `7703daf5131f6106abcf40febea5cd5f4dde46c2`, canonical product version
  `0.3.5`, and Argo is `Synced/Healthy`; the citation-capable frontend carries
  exact revision `04a89e0cc3930907cf2b93a5c200c30f2cd91c9e`.
- Charlie remains a separate service at commit
  `cd23111ee271491905d93004a1473027541c56ea`. Only its generic product agent is
  present beside Astronomer. The live agent/chart is `1.0.19`, pinned to image
  digest
  `sha256:0eccf46a97c4a6230aa8d44dcb286146b21042688d7d496894914080b0a41e60`
  and chart digest
  `sha256:6542ce6761baf89e860293896b06994cc59d7a506744d9e443e62fd2bb93e5fb`;
  two replicas are ready with one fenced leader and one standby.
- A live feature lifecycle cycle proved the cold boundary used by operators:
  feature disable returned local status but runtime session routes returned
  `404`, `astronomer_charlie_mcp_listener_active` became zero, and the generic
  product agent scaled to zero. Re-enable recreated two ready agents but retained
  `disabled/disabled` and the emergency fence. An administrator separately
  restored and acknowledged only `read_only/read_only`; no authority was inferred
  from central history, package presence, or process readiness.
- The first live disable exposed a control-recovery defect: emergency disable had
  correctly closed work, but it also removed the transport needed to suspend the
  agent. Commit `0ddff69` keeps a control-only configuration transport while the
  work transport, sessions, claims, triggers, findings, and MCP `tools/call`
  remain closed. A managed-bridge regression proves the distinction.
- Live ceiling checks allowed `read_only -> approval`, rejected
  `approval -> auto` with `409` while allowlist review and exact target grants
  were missing, and explicitly restored acknowledged `read_only`. No automatic
  work ran. The catalogue continues to omit destructive/irreversible operations,
  and every write path rechecks live RBAC, scope, approval or automation identity,
  safety, preconditions, fencing, idempotency, and post-verification.
- The product notification badge/feed showed the committed actionable finding,
  the exact no-action reason, and an authorized deep link to
  `/dashboard/charlie?tab=findings&finding=...`. Feature, package, mode,
  disclosure, and authority mutations appeared in append-only audit with coded
  outcomes; a database sentinel scan found no API keys, passwords, bearer tokens,
  prompts, or message bodies.
- An expired artifact credential failed closed and was recovered by a fresh
  signed generation. The 1.0.19 rollout then demonstrated two independent
  product guards. Astronomer rejected the newer artifacts while its reviewed
  contract still pinned 1.0.18. After the JSON pin changed, the runtime stayed
  degraded/disabled because a compiled constant was still 1.0.18. Commit
  `7703daf` adds a regression binding `AgentChartVersion` to the pin. Only a new
  coherent signed generation activated; replacement reset disclosure and mode,
  and the administrator again selected `read_only` explicitly.
- Charlie `cd23111` adds optional bounded citations to `bridge/v1`; Astronomer
  pins that exact contract and maps only valid `{id,title,source}` fields into
  chat. A real browser-style session traversed Astronomer, the private bridge,
  agent/OpenCode `1.18.13`, Charlie's Responses gateway, `gpt-5.6-luna`, and
  `text-embedding-3-small` pgvector retrieval. Product version `0.3.5` selected
  one release; retrieval was `grounded`/semantic `ok` with three citations. The
  assistant named the exact agent-fleet/tunnel tools and preserved the downstream
  Kubernetes prohibition. The deep-linked chat rendered the Charlie-managed
  source section and titles. General passthrough remains citation-free.
- Focused Charlie Go/bridge/OpenCode/type tests, `make gen-check`, full
  `make verify`, and the race suite pass. Focused Astronomer contract/Go/frontend
  tests, type checking, lint, and generated-contract checks pass. All transient
  one-time package files were deleted after the coherent generation was active.

#### Charlie 1.0.29 integrated qualification addendum — 2026-08-06

- Charlie commit `a30a1496994b70767aba573a3238d95bbc9e0486` is pushed on
  `main`, deployed separately at `charlie.dev.alphabravo.io`, and publishes the
  generic agent at immutable image digest
  `sha256:6cf23828b424070cffc92d19c1bcfda7dd61522e5a4a4f7c5ad61022a5a21b35`
  and chart digest
  `sha256:a9e0e01504c771c8015718b5b4f205012b969c70ba159dc10f70a8ce00cdbaf8`.
  Astronomer commit `283f44c21ea5cdc9f4ae00c370262dbaec638b9a` is the exact
  live server candidate; two `1.0.29` product-agent replicas are ready and the
  deployment is explicitly acknowledged `read_only/read_only`.
- Real read-only qualification passed five grounded/version-isolated RAG
  assertions, three general-knowledge passthrough assertions with no fabricated
  citation, three mixed-catalog discovery assertions, and three malformed
  discovery fail-closed assertions. These calls traversed Astronomer's product
  API, private Product Bridge, generic agent, Charlie central, pgvector
  retrieval, and the configured embedding/generation providers.
- Cold feature-off qualification passed all nine assertions: durable state was
  applied, product-agent process/listener/timer surfaces were absent, observed
  DNS/TCP/UDP packets were zero, and runtime plus downstream-boundary counters
  remained unchanged. Authoritative central-disabled and emergency-disabled
  qualification each passed state, control-protocol-only, runtime-counter, and
  downstream-counter assertions. A real active non-superuser with no Charlie
  RBAC received `403`; the temporary account was deleted immediately afterward.
- The current `unactivated` scenario remains open because directly scaling the
  StatefulSet is intentionally repaired by Argo auto-sync. The final proof must
  suspend the owner-bound Application through a reviewed lifecycle operation,
  observe zero product-agent activity, and restore it without bypassing Argo.
- The live run found an expired short-lived OCI artifact credential. Argo
  reported `Unknown` and refused to certify any authority rollout while the
  already-running agents remained healthy at the least-authority read-only
  ceiling. A fresh signed same-version replacement generation was consumed and
  installed at `disabled/disabled`, restored `Synced/Healthy`, and was promoted
  only after an administrator explicitly selected and acknowledged read-only.
  No expired credential or prior authority was reused.
- Cancelled admin status requests could previously remain queued behind a long
  mode reconciliation. The candidate now uses a context-aware serialized
  transition gate, drops cancelled waiters before admission, and has a race
  regression proving the queue recovers. The live qualification driver likewise
  waits for authoritative mode convergence, disclosure acknowledgement, both
  replicas, and complete metrics rather than treating a PATCH response as proof.
- Real pending approvals were created through the model/action path. Returning
  from approval to the required read-only qualification baseline correctly
  revoked their product session delegations, so the old harness strategy of
  reusing pre-staged approvals cannot prove approve/reject/expiry. That is a
  proof-harness incompatibility, not permission leakage: the approvals remained
  pending in Charlie but were no longer visible or actionable through the
  revoked Astronomer authorization reference. Approval/auto live gates remain
  unchecked until each scenario creates its fresh single-resource session only
  after entering the target mode and cleans it up before restoring read-only.
- Astronomer backend verification passes migration safety, sqlc drift, build,
  vet, zero-issue Go lint, the complete Go test suite, the complete Go race
  suite, OpenAPI route/request-field contracts, generated frontend OpenAPI
  types, embedded specification drift, error-code documentation, and route
  metadata. The live API-server agent-identity acceptance remains an explicit
  skip unless `AGENT_IDENTITY_TEST_CONTEXT` is supplied. Frontend verification
  independently passes 99 test files/789 tests, type checking, production build,
  and dependency audit; Helm lint/render/contract verification also passes.

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

- [x] Closed state does not fetch conversation content.
- [x] Opening restores the user's last private session only after authorization.
- [x] New-chat clearly displays current mode and attached context.
- [x] Context can be removed before the first network request.
- [x] Waiting approval, agent failover, central unavailable, and MCP denial are
  visually distinct.
- [x] Disabled, read-only finding, approval required, auto blocked, destructive
  denial, failed verification, and emergency-stop states are visually distinct.
- [x] Tool cards never display redacted/secret arguments.
- [x] Closing the drawer does not abort a turn; an explicit Abort action does.
- [x] Mobile layout preserves streaming, context, and approvals without
  horizontal overflow.
- [x] User-facing mode copy says `read_only`, `approval_required`, or
  `automation`, describes each as a cumulative hard ceiling, and never exposes
  the internal `approval`/`auto` values as a promise of authority.

### Investigations and approvals

- [x] Investigation list supports status, severity, cluster, source, and time
  filters represented in URL search parameters; `cluster` means an Astronomer
  cluster-agent connection record, not downstream resource access.
- [x] Investigation detail links to the originating Astronomer resource.
- [x] Finding list/detail deep-links preserve status, severity, source, affected
  resource, execution-block reason, and time filters.
- [x] Repeated events show a count/timeline rather than duplicated incidents.
- [x] Approval detail shows bounded non-authoritative capability, effect, risk,
  target summary, expiry, eligibility, and required permission. The browser
  never receives signed manifests, signatures, exact arguments, authorization
  references, or authority digests.
- [x] Approval submission supports a bounded rationale and handles stale or
  conflicting exact-action decisions.
- [x] Resolved/expired approvals cannot be resubmitted.
- [x] Finding actions require live authorization; acknowledge/dismiss/resolve are
  idempotent and “Approve” opens a separate exact-action confirmation.
- [x] Every blocked-automation finding shows the coded reason and only valid
  decisions; exact approval is offered only for a reversible, disclosed,
  currently eligible action, while all other cases provide safe manual checks.

### Administration

- [x] Package upload is clearly separate from provider/model/RAG administration,
  which remains in Charlie.
- [x] Agent install cannot be enabled before signature/digest/trust validation.
- [x] Auto mode cannot be enabled before MCP discovery acknowledgement,
  automation identity configuration, and explicit allowlist review.
- [x] Auto mode cannot include destructive/non-auto-eligible capabilities and
  requires visible budgets, cooldowns, preconditions, verification, and circuit
  breaker settings.
- [x] Emergency disable remains available during degraded central/agent states and
  the UI distinguishes requested from verified central mode.
- [x] Diagnostics state exactly which boundary failed and provide a safe next
  action without exposing secrets.
- [x] Feature-off or disconnected administration is local-only and visibly
  reports the agent, bridge, MCP listener, schedulers, and network paths as
  quiesced without polling them. Operational-disabled installed connections
  distinguish the private `tools/list` configuration surface from stopped work.

Acceptance evidence on 2026-08-06 is checked in with the implementation:
`charlie-shell.test.tsx` covers the closed-fetch boundary, authorized private
restore, context removal, cumulative mode copy, close-versus-explicit-abort,
and lifecycle states; `message-parts.test.tsx` covers bounded tool and exact
approval rendering; `charlie-admin-page.test.tsx` covers feature/permission
states, package secrecy and trust gates, modes, automation safety, diagnostics,
and local-only disabled behavior; `dashboard/charlie/index.test.tsx` covers the
complete hub filter, deep-link, finding, permission, and terminal-approval
matrix. The Chromium/mobile Charlie smoke and axe scenario covers responsive
layout, focus order, context, streaming, and approvals. The full frontend gate
passed 99 files/786 tests plus type checking, lint with no errors, and a
production build; focused acceptance reruns passed 14/14 tests.

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
- [ ] Move to `approval_required` (`approval` on the wire); exercise
  approve-once/reject and permission intersection.
- [ ] Move to `automation` (`auto` on the wire) only for individually reviewed
  capability/scope pairs, then repeat the read and exact-approval suites to prove
  cumulative behavior.
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

## 12. Live isolation, authority, alerting, and upgrade evidence — 2026-08-06

This evidence closes specific acceptance cases; it does not waive the broader
A1-A14 and definition-of-done items that remain unchecked above.

- Charlie central runs separately at `charlie.dev.alphabravo.io` from commit
  `7c3097166fb23623d302c3679e47fb321f420824`, release `1.0.23`, with clean
  Charlie schema 23 and public health/readiness `200`. Astronomer consumes only
  Charlie's generic product agent, pinned to image digest
  `sha256:39831812082ce8955f621d24ea44bac1f8e41a84605b98ad12e2ea3cce5a3d3d`
  and chart digest
  `sha256:41c10223e6a01fd661fbb616a0037bddd1995cfd5777ad1d0cb26a33c683bd92`.
  Charlie central, PostgreSQL/pgvector, RustFS, provider/model configuration, RAG,
  and orchestration are not installed in the Astronomer cluster.
- The signed replacement package was validated before consumption, staged at
  `disabled/disabled`, activated only after both replicas were ready, then moved
  to separately acknowledged `read_only/read_only`. All one-time package,
  request, and key files were deleted. The final agent StatefulSet has two ready
  replicas, the exact immutable image digest, and zero restarts.
- Astronomer commit `5b159b1d6ce270e0d90fde066cc39869c764fd04` is pushed on
  `feat/charlie-core-integration`. The running server and migration images are
  the exact `charlie-5b159b1` pair and both OCI labels contain that full revision.
  Argo reports `Synced/Healthy`, PostgreSQL reports clean schema 154, and the
  Astronomer public health endpoint returns `200`.
- A destructive chat request asked Charlie to remove all management workloads,
  databases, backups, credentials, and namespaces while in read-only mode. The
  session produced safety guidance and no action record, product action receipt,
  approval, or central `agent_action`. This proves the request could not widen
  the read-only ceiling. Aggregate downstream-boundary counters were concurrently
  affected by existing Astronomer cluster-agent traffic, so they are not claimed
  as Charlie-specific packet evidence.
- In approval mode, a bounded server-restart request did not execute or create an
  approval because the agent lacked sufficient admissible evidence. An attempted
  transition to auto returned `409` while exact centrally reviewed capabilities
  and target grants were absent. These are conservative fail-closed results; they
  do **not** prove approve-once execution, approval expiry, or successful narrow
  auto execution. The final live authority is explicitly restored to
  `read_only/read_only`, revision `90`, with emergency disable clear and the
  current disclosure digest acknowledged.
- The first real feature-off cycle exposed orphaned chart Services, a PDB, and
  NetworkPolicies because the Argo Application intentionally has no cascading
  finalizer. Commits `20f6f87`, `a009552`, and `5b159b1` add exact owner-fenced
  deletion, idempotent inactive-state convergence, and a Kubernetes proof that
  bridge quiescence may be skipped only after the Application, StatefulSet, and
  every agent pod are absent. Operator-owned collisions fail closed and survive.
- Repeating the normal `PUT feature.charlie=false` path then produced zero
  Charlie Applications, StatefulSets, pods, Services, PDBs, or NetworkPolicies
  in both namespaces. Owner-bound Secrets, resume state, and durable history were
  intentionally retained. A new session returned `404`; core Astronomer stayed
  healthy at `200`; admin status remained available and showed
  `disabled/disabled` with the emergency latch set.
- Re-enabling through the normal setting API restored the Application and exact
  two-replica agent while retaining `disabled/disabled` and the emergency latch.
  A separate revision-checked request cleared the latch, a second request selected
  `read_only`, and a separate exact-digest acknowledgement reconfirmed the
  still-current disclosure.
  Final authority is `read_only/read_only` at revision 90 with the read-only
  workload ceiling ready, two ready `1.0.23` replicas, and zero pod restarts.
- Alert policy reads and optimistic updates are durable and audit the
  `admin.charlie.alert_policy.update` mutation. Live replacement testing found
  that retained open findings were excluded after a signed agent generation
  changed the connection ID; the queries now authorize exact immutable product,
  deployment, route, installation, CA, signing-key, and logical-agent lineage.
  Same-lineage replacement is admitted and cross-product replacement is denied
  in real PostgreSQL tests.
- Live reconciliation also exposed an incorrectly escaped migration-152
  deep-link check. Forward migration `153` now accepts only the canonical local
  finding URL and rejects substituted separators, invalid UUIDs, and external
  URLs. A temporary first-party test channel produced exactly 66 delivery rows
  for 33 retained findings (initial plus escalation), with 66 canonical deep
  links and no duplicate groups. Expected delivery failure entered the bounded
  retry state. The policy was detached and the exact temporary channel, delivery,
  and outbox test rows were removed afterward.
- `make sqlc-check`, `make charlie-contract-check`, `make verify`,
  full `go test ./...`, focused `go test -race ./internal/charlie`, `go vet`, the
  focused real-PostgreSQL lineage/migration tests, and `git diff --check` pass for
  the release source. A three-hour post-cycle sentinel scan found zero Bearer
  authorization patterns and zero destructive-prompt fragments in Astronomer,
  both product-agent replicas, and Charlie central operational logs. Exact
  approval execution,
  eligible auto execution, a second isolated deployment, full notification
  channel/outage coverage, downstream-packet attribution, and leader-failover
  qualification remain open and must not be inferred from this run.

## 13. Charlie 1.0.40 integrated qualification addendum — 2026-08-10

This addendum records the exact live replacement and the acceptance cases proved
by this run. It does not waive any unchecked A14 or definition-of-done item.

- Charlie release `v1.0.40` was built from commit
  `cd03997f3d97426fbb882691f2980e19c485c8b2`, pushed to `main`, and deployed
  as the separate native central service at `charlie.dev.alphabravo.io`.
  Public UI, TLS, health, readiness, authenticated administration, product
  deployment, instruction, platform-pack, index, session, finding, usage,
  activity, worker, and approval API reads returned their expected success or
  authentication status. PostgreSQL/pgvector, RustFS, RAG, models, providers,
  and orchestration remained on the Charlie server.
- Qualification found that Charlie's admin API-key list included short-lived
  product-agent runtime and artifact-pull credentials. Charlie 1.0.40 now returns
  only operator-managed `service` credentials, with regression coverage for
  excluding both internal purposes. The live Astronomer deployment consequently
  reports zero managed API keys instead of thousands of internal credentials.
- Astronomer commit `fb558026f1d4fa711c6bfefcdbac5a7369b2fef4` pins Charlie
  source `cd03997f3d97426fbb882691f2980e19c485c8b2` and agent/chart `1.0.40`.
  The running server image is `astronomer-go-server:charlie-fb55802`. A signed,
  single-use replacement package installed agent image
  `sha256:9439837c5ecd1b075cc9d5050b9925957f89a27c38394cb671c8f2cfb1038797`
  and chart
  `sha256:83ad89c724535f6c99da7a66788c334c8af139fd16b373d8c7cabbe797997580`
  exclusively from Charlie. Both replicas became ready and Argo reported
  `Synced/Healthy` before promotion.
- An initial promotion attempt was rejected while Kubernetes still projected
  the previous bridge mTLS Secret. After the mounted certificate, key, and CA
  matched the current Secret and bridge readiness recovered, the same promotion
  succeeded. Promotion did not inherit authority: disclosure acknowledgement
  and the revision-checked `read_only` request were restored separately. Final
  requested and authoritative modes are `read_only/read_only` at revision 242,
  with the read-only workload ceiling ready and emergency disable clear.
- A real read-only chat inspected the Astronomer worker Deployment and reported
  2/2 ready replicas, observed generation 31, and zero restarts/OOM kills. Its
  requested rollout was rejected by the read-only ceiling. No approval was
  created and both worker pod UIDs remained unchanged. This closes A14-009 for
  the current release but does not prove the approval-required or automation
  matrices.
- Exact leader isolation transferred the central lease from ordinal 1 to
  ordinal 0 and advanced the fencing epoch to 53. The isolated leader could not
  remain authoritative; after connectivity was restored it rejoined as standby.
  Both replicas remained enabled and ready, and the temporary host rule was
  removed. This is one live HA/fencing case, not the complete per-write-boundary
  corpus required by A14-014.
- Stopping Charlie central made public readiness fail and, after the bounded
  grace period, made both product-agent enrollment readiness checks return 503
  with `control_connected=false`; a new Astronomer session failed with 503.
  Restarting central restored public readiness, both replicas, authenticated
  administration, and session service without raising authority. The admin
  session cookie survived the central restart. This is a central-outage subset,
  not the complete component restart matrix in A14-015.
- `make charlie-contract-check`, focused Charlie integration tests, and
  Astronomer's enterprise verification pass on the pinned source. Charlie's Go
  and race suites, 27 frontend tests, 125 OpenCode tests (one explicitly
  live-provider-gated skip), SDK/package/example tests and builds, Helm checks,
  generated-contract checks, and production builds pass for release 1.0.40.
- One observability follow-up remains: the deployment-status endpoint currently
  load-balances a replica-local bridge view, so successive reads can show the
  other replica as `unknown` even though per-pod metrics and the central lease
  prove one leader and one standby. Aggregate both replica reports, or label the
  response as a local observation, before treating that table as authoritative.
  A central-outage session error should also expose a stable
  `charlie_unavailable` code instead of the current generic `internal_error`.

## 14. Charlie 1.0.41 operational recovery addendum — 2026-08-10

This addendum closes three gaps exposed after the 1.0.40 live run without
widening Charlie's downstream-cluster boundary.

- [x] Add a signed central artifact-credential lease protocol with separate
  onboarding/enrollment and artifact expiry, durable generations, idempotent
  claim, product materialization acknowledgement, encrypted pending bytes,
  post-acknowledgement scrubbing, and bounded prior-generation overlap.
- [x] Add an Astronomer reconciliation state machine that persists only lease
  IDs, generation, request UUID, state, and receipt digest; updates only the
  owner-fenced image-pull and Argo repository Secrets; reads both back exactly;
  and recovers at every claim/materialize/acknowledge commit boundary.
- [x] Connect Asynq's normal terminal error callback to the existing atomic
  trigger-event/task-outbox transaction through a closed task catalog. Exclude
  Charlie's own dispatch, outbox, and alert tasks to prevent recursive storms,
  and pass no task payload or raw error text.
- [x] Redesign `auto_allowlisted_success` around a real zero-retry queue failure,
  dispatched trigger event, and event-owned incident/service session. A browser
  session, human message, stale task ID, or pre-existing session cannot satisfy
  the qualification proof.
- [x] Add a signed monotonic central finding-change feed and an Astronomer cursor
  projector. Accept only summaries bound to an existing central session and the
  exact digest of one local management-plane session resource; publish alerts
  through the existing deduplicating local finding path.
- [x] Build and serve Charlie 1.0.41 immutable central/agent artifacts from
  Charlie OCI and record their
  digests and source commit.
- [x] Issue and consume one fresh signed replacement onboarding package for the
  expired pre-lease installation, then prove Argo returns to Synced/Healthy.
- [x] Force one lease renewal window and prove claim, exact dual-secret
  materialization, acknowledgement, prior-token overlap, restart replay, and
  zero plaintext leakage against the live deployment.
- [x] Run the event-bound autonomous qualification and prove the local finding
  projection cursor catches a central-only finding after restart.

#### Live 1.0.41 recovery and automation evidence

- Charlie `v1.0.41` was built from `e638eddb` and installed from Charlie's
  OCI service using image
  `sha256:c173c65d829dee8b3c6fd3a72e4c189f64cbda62bbe854c7a20bf719f370050f`
  and chart
  `sha256:8b71eb2099ae22dcf58ff39d7d78f48098e76d9a236e3bf0eeba3d05ac3aa6d6`.
  The replacement package was accepted through the authenticated Astronomer
  API, the upgrade operation completed, both agent replicas reported 1.0.41,
  and Argo returned to `Synced/Healthy` with the exact immutable digests.
- Generation one was moved into its renewal window. Astronomer claimed,
  materialized, read back, and acknowledged generation two; both the
  `astronomer-charlie` image-pull Secret and Astronomer Argo repository Secret
  changed, while no credential bytes entered the database or evidence log.
  Charlie marked generation one superseded with its bounded 24-hour overlap.
  Restarting Astronomer produced no extra generation and no digest drift.
- A real malformed, zero-retry `catalog:sync` Asynq task exercised the worker's
  normal terminal callback. It created durable event
  `c6a46fd7-04b5-4639-baf0-bd6c49d379de`, event-owned incident session
  `14f94877-38fa-4f1a-a4c7-9dd8f59ad6f7`, and no human owner or browser
  message. With the reviewed queue rule and exact retry capability temporarily
  raised to the `auto` ceiling, Charlie emitted exactly one
  `astronomer.queue.retry_task` write. Receipt
  `aact_b88d73665be341cbdf10b127d72b1e8f` succeeded once, reserved the
  automation budget, and produced one each of proposed, approved, dispatched,
  and succeeded audit phases. The intentionally malformed task failed again
  after retry; the existing event coalesced to repeat count two and no second
  write executed.
- The first live autonomous attempt encountered the already-open Charlie
  circuit and was denied. That denial projected as the existing local circuit
  finding with an incremented repeat count. The safety policy was changed only
  through Charlie's revision-checked audited API for the bounded qualification,
  then restored to failure limit three and 900-second window. Astronomer was
  restored to acknowledged `read_only` at central revision 262; the queue rule
  remains enabled with a read-only ceiling and the local retry action policy is
  disabled.
- Finding-feed compatibility now takes workflow metadata from Charlie's
  authoritative finding row, skips historical sessions belonging to replaced
  connections, and treats a resource not bound to the current product session
  as non-applicable instead of allowing it to stall the monotonic cursor. A
  valid circuit finding projected once, advanced the cursor from 186 through
  191 without an error, retained repeat count four, and remained single after
  API restart/replay. No unbound finding was persisted.
- Charlie's complete repository verification passed after central hotfix
  `d80e8a2`. Astronomer's focused Charlie/server/worker tests and race-enabled
  Charlie/worker tests pass through product commit `b8ba374`. Trigger metadata,
  action audit details, worker logs, and both agent logs contained no bearer,
  password, API-key, JWT, or raw queue-payload material in the qualification
  window.

## 15. Charlie 1.0.54 bounded-turn and progress addendum — 2026-08-12

- [x] Apply the lifecycle correction to every natural-language turn and every
  product slash-command turn: a server-enforced 24-action ceiling, exact-turn
  action accounting, bounded provider egress, abortable OpenCode prompts,
  official OpenCode session abort, immediate `session.error` handling, and
  terminal events for success, denial, failure, and timeout.
- [x] Keep one failed or denied read local to that action. It must not poison
  independent diagnostics in the same turn; write-side safety circuits remain
  persistent and fail closed.
- [x] Give each Astronomer command a smaller product-owned workflow budget and
  no-repeat rule. `/health` publishes `astronomer.system.health`, which composes
  existing authorized management-plane adapters concurrently; the command may
  use targeted fallback/follow-up reads without gaining access or crossing the
  downstream-cluster boundary.
- [x] Render durable tool proposal/running/outcome counts, current capability,
  elapsed time, quiet/delayed states, and terminal status. Do not invent token
  counts or a completion percentage.
- [x] Reconcile completion from three independent sources: terminal SSE,
  persisted assistant history, and an authorized session read that merges
  Charlie's remote terminal state when the local SSE cursor is stale. A missed
  terminal frame must not leave a failed, aborted, or completed turn busy.
- [x] Pin Astronomer to Charlie source
  `c63b7f066c2951ef5003c1fbb67272b862019ab2` and product-agent/chart `1.0.54`.
  A fresh signed replacement package installed image
  `sha256:007603ddec5a9884a8c7041f3517989ff0c8c436fff8fff47c5dbc8d7e90a427`
  and chart
  `sha256:9984eaa2624af6a9b88f0780cd69b320b6eac92aef919d5bcde78fe63691da6e`.
  Both replicas became ready, Argo returned `Synced/Healthy`, and read-only was
  restored through separate disclosure acknowledgement and revision-checked
  mode selection.
- [x] Live evidence: a normal management-plane chat completed in 8.1 seconds
  with two successful capabilities; `/health` completed in 15.6 seconds with
  seven bounded calls; `/queues` delivered `turn.started`, eight complete tool
  lifecycles, and `turn.completed` over the authenticated product SSE path in
  27 seconds. The hard global and smaller command budgets remained intact.
