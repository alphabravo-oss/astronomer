# Charlie Integration Operations and Runbooks

Charlie is an optional, separately deployed SRE assistant. Astronomer installs
only the generic Charlie product agent in `astronomer-charlie`; Astronomer
server and worker communicate only with that agent over the private Product
Bridge and MCP interfaces. Charlie Central, its databases, models, RAG, and OCI
service never run in the Astronomer release.

## Safety contract

- `feature.charlie=false` is the default. A never-onboarded installation creates
  no Charlie runtime resources. Disabling an existing installation closes write
  admission, cancels/drains in-flight writes, stops the private listener and
  agent workload, and removes the MCP Service plus exact access NetworkPolicy.
  It deliberately retains the namespace, installation Secrets, owner-bound
  resume state, metadata, and audit/history for a safe re-enable.
- Onboarding installs the two-replica product agent as a control-only runtime:
  `runtime.enabled=true` with product-owned `runtime.modeCeiling=disabled`.
  Configuration with `runtime.enabled=false` renders no workload or listener.
  The agent has no Kubernetes service-account token or RBAC. Either product or
  Charlie deployment disablement wins immediately.
- Effective authority is the least privilege of current product feature state,
  connection state, emergency latch, Charlie-verified mode, current Astronomer
  RBAC, affected-resource scope, disclosure digest, action classification,
  approval/allowlist, budgets, cooldowns, preconditions, and fencing epoch.
- `read_only` permits disclosed reads only. `approval` requires one exact,
  expiring approval by an eligible user. `auto` permits only individually
  allowlisted, non-destructive, auto-eligible actions within current budgets.
- Destructive or irreversible operations are not disclosed in v1 and cannot be
  enabled by mode, approval, administrator role, model output, or prompt text.
- Charlie never opens an Astronomer downstream-cluster tunnel. Downstream-agent
  connection metadata is Astronomer management-plane state and is readable;
  downstream Kubernetes objects and all downstream mutations are prohibited.
- Optional management-cluster Kubernetes diagnostics are configured separately
  under **Settings → Charlie → Kubernetes** and execute only inside Astronomer's
  existing Product MCP adapter. The generic product agent still has no service
  account. See [Charlie Kubernetes visibility](charlie-kubernetes-visibility.md).

Emergency response always starts with **Settings → Charlie → Mode → Emergency
Disable**. The local latch commits first, stops new sessions/triggers/MCP calls,
and tears down bridge transports even when the agent or Charlie Central is
unavailable. Admin status, emergency mode, uninstall, and disconnect endpoints
remain available while the feature is off. Non-emergency mode changes remain
feature-gated in the handler. If the API
reports a write drain still in progress, the latch remains closed and disable
is not complete; investigate the non-cooperative executor. Do not clear the
latch until the agent independently reports `disabled`.

## Content-safe observability

Prometheus and structured logs use fixed labels only: operation, disclosed
capability, effect, mode, lifecycle state, and stable denial code. They never
contain prompts, responses, evidence, arguments, resource IDs, user IDs,
authorization references, URLs, upstream error bodies, or credentials.

The support bundle includes `charlie-status.json` only when local Charlie
metadata exists. It contains mode/health/version/fencing state, reviewed trigger
configuration, and finding counts. It excludes central URLs, trust material,
Secret names/HMACs, package contents, finding prose, session content, and opaque
central identifiers. Conversation and evidence content remains in Charlie.

Audit records are required before a write can dispatch. They record proposed,
denied, approved/committed, dispatched, verified, succeeded, failed, ambiguous,
and replayed lifecycle metadata without arguments or content. If the durable
audit or receipt cannot be committed, the action fails closed.

## Management-plane diagnostic coverage

Charlie discovery separates broad orientation from narrow evidence:

1. `astronomer.platform.inventory` reports bounded counts for configured
   management-plane domains. It cannot return record contents.
2. Domain health tools cover delivery, logging, monitoring, identity,
   authentication, RBAC, live key rotation, external credential integrations,
   governance, controller policy, templates, configuration, tenancy,
   registration, fleet-operation orchestration, catalog ingestion,
   reconciliation bookkeeping, GitOps, extensions, alerting, dashboards, and
   the product-local Charlie runtime.
3. Work-pipeline tools expose durable task-outbox, scheduler, controller-alert,
   and controller-operation lifecycle state. Payload field names and byte counts
   may be returned; values and raw errors are never returned.
4. Runtime tools cover owned management Kubernetes resources, PostgreSQL,
   Redis, aggregate HTTP metrics, and the Astronomer process. They accept no
   generic SQL, Redis command, metric query, Kubernetes kind/GVR, namespace, or
   selector.
5. `astronomer.audit.search` is exact-filtered, lookback-bounded, and paginated.
   It excludes actors, paths, IP addresses, user agents, and detail JSON.

These reads still require live product RBAC and exact session scope. Registered
cluster and cluster-agent connection metadata remains management-plane state;
no tool can convert that metadata into a managed-cluster Kubernetes request.
Cross-domain summaries require wildcard read RBAC (or superuser); a narrow
settings, monitoring, or project grant cannot authorize them.

Full management-plane visibility is not database export access. Charlie does
not receive user identities, tokens, secret/configuration documents, support
bundles, shell-command history, raw SQL, arbitrary PromQL, or raw HTTP data.
It also does not receive managed-cluster pods/workloads/nodes/events/logs,
API-server audit events, security findings, vulnerability/image inventories,
snapshots, manifests, or policy bodies. It may receive bounded Astronomer-owned
workflow state about registration, delivery, reconciliation, or fleet work so
it can diagnose the management service without entering a managed cluster.

The only remediation added with this coverage is
`astronomer.task_outbox.retry_delivery`. It is approval-only and accepts one
UUID from the outbox diagnostic. The adapter refuses delivered, pending, and
in-flight records, requeues only failed/dead delivery state, and verifies the
same row is pending with its delivery error/lock cleared. It cannot change the
task payload or become auto-eligible.

## Runbooks

### Product Bridge unavailable or circuit open

1. Confirm Astronomer core readiness is healthy; Charlie degradation must not
   make the main readiness probe fail.
2. Open Charlie Diagnostics and distinguish feature, connection, local mTLS,
   Service/endpoint, bridge readiness, enrollment, leader, and central health.
3. Check every expected `charlie-agent` StatefulSet replica and the private bridge
   Service. Do not exec into a pod or print mounted Secrets.
4. Verify NetworkPolicy admits Astronomer server/worker to TCP 7443 and that the
   exact SPIFFE/DNS identities and certificate validity match.
5. If the agent is healthy but central is unavailable, leave the circuit open;
   do not bypass the agent with a direct central client. Existing Astronomer
   functionality remains available.
6. If repeated failures continue, emergency-disable and investigate from the
   content-free support bundle and metrics.

### No leader, stale epoch, or replica disagreement

1. Confirm the package's expected replica count, unique ordinals, distinct
   persisted identity volumes, one leader, and standby replicas.
2. Compare leader ID, epoch, and lease expiry reported by each replica against
   Charlie Central database time. Do not use pod wall clocks as authority.
3. If a write is `ambiguous`, do not retry it manually. Reconcile its durable
   receipt and product operation status first.
4. A stale-epoch result must remain denied. Restore central connectivity and let
   lease takeover requeue only unfinished durable commands.
5. Emergency-disable if more than one replica claims leadership or a succeeded
   action is proposed again.

### Disclosure digest changed

1. Leave the effective mode at `disabled`; a connector scope change closes the
   write fence, clears prior acknowledgements, and triggers signed catalog
   rediscovery through the local Product Bridge.
2. Confirm the Kubernetes tab distinguishes rediscovery, Charlie central review,
   and Astronomer acknowledgement instead of treating them as one state.
3. Leave the new, safer product profile in force if rediscovery is unavailable;
   save the unchanged profile to retry after bridge health is restored.
4. Review the exact added, removed, and changed capability schemas, effects,
   connector provenance, target bounds, preconditions, verification, and auto
   eligibility in Charlie.
5. Confirm no shell, exec, attach, port-forward, arbitrary HTTP/SQL/Kubernetes
   proxy, Secret read, downstream operation, destructive action, or open-ended
   selector appears.
6. Acknowledge the candidate in Charlie, wait for Astronomer to import the exact
   active digest, then acknowledge the same digest in Astronomer. Rebuild
   automation allowlists rather than carrying unknown entries forward.

### Requested and verified mode drift

Mode drift always reduces authority through `EffectiveMode`; it never adopts
the more permissive value. Before contacting the Product Bridge, Astronomer
persists the desired ceiling, updates only the owner-bound Argo Application's
`runtime.modeCeiling`, forces a non-pruning self-healing sync, waits for the
StatefulSet rollout, and verifies both ready pods contain the exact immutable
`CHARLIE_MODE` value. Upward transitions leave central at the lower prior mode
until that readback succeeds. Downward and emergency transitions close and
drain write admission first and retain the lower durable product ceiling even
when Kubernetes, Argo, either replica, the bridge, or central is unavailable.
Check the UI's product-agent ceiling readback, Argo health, StatefulSet revision,
both pod readiness states, agent/central reachability, and the last mode
revision. Retry the same transition idempotently only after the same connection,
signing trust, and disclosure digest are confirmed. Never enable pruning to
force a mode rollout. For unexplained drift, emergency-disable and rotate local
trust.

### Enrollment, certificate, or credential rotation failure

1. Treat onboarding packages and enrollment/artifact credentials as secrets.
   Never paste them into logs, tickets, CLI arguments, or browser storage.
2. Verify package signature, confirmed public-key fingerprint, deployment and
   route binding, expiration, immutable chart/image digests, replica count, and
   one unique enrollment slot per ordinal.
3. Replace the signed package when enrollment identity, trust, artifact pins, or
   package scope changes. Routine artifact-pull credentials use renewable
   leases: Astronomer persists a request ID, claims one pending generation
   through the local mTLS product bridge, writes and reads back only the exact
   image-pull and Argo repository Secrets, then acknowledges their digest.
   Charlie activates the new generation, retains the prior token for a bounded
   24-hour overlap, and scrubs the pending secret. No credential is stored in
   Astronomer's database.
4. An installation whose pre-lease artifact token already expired needs one
   fresh signed replacement package to install a lease-aware agent. Do not
   reuse the expired package or patch a registry password by hand.
5. Replayed or ordinal-mismatched enrollment is an incident. Revoke/replace the
   package and inspect Charlie's enrollment audit without exposing token data.

### Agent image or chart pull failure

Confirm the onboarding package points to Charlie OCI by immutable digest, the
artifact credential is unexpired and repository-scoped, and the registry/Blob
store health is green. Do not fall back to `latest`, an unreviewed public image,
or an inline registry password. Roll back to the previously verified digest if
the new artifact cannot be read back and verified. Check
`charlie_artifact_credential_state` for the content-free generation, pending
state, expiry, acknowledgement time, and stable error code. Replay the same
request; never generate a second credential while one is pending.

### Trigger dead-letter or event storm

1. Inspect the durable `charlie_trigger_events` state and task-outbox record;
   Redis/asynq is not the source of truth.
2. Confirm the reviewed rule is enabled and its severity, selectors, threshold,
   grace/window, cooldown, service identity, and read-only mode ceiling are
   correct.
3. Repeated events must coalesce by stable fingerprint and increment the repeat
   count. Do not create one investigation per event.
4. A reconnect inside the disconnect grace period suppresses undispatched work.
5. After correcting the cause, use the authenticated admin retry API with the
   immutable dead event ID plus a fresh UUID request ID. Astronomer retains the
   dead source and fingerprint, creates at most one active retry attempt, and
   uses that request ID as the new event/session idempotency boundary. Never
   edit or revive the dead row or its terminal/ambiguous session in place.
6. `queue_terminal_failure` is emitted by Asynq's production terminal error
   callback, not by an operator or a database insert. The closed task catalog
   decides whether a terminal task is eligible. Payload bytes and raw worker
   errors are never copied into the event. Keep the rule enabled at a
   `read_only` ceiling for findings; raising its ceiling also requires the exact
   product-local action policy, Charlie's independent allowlist, current
   disclosure acknowledgement, budgets, cooldown, and closed circuit.

### Finding projection cursor is stalled

1. Inspect `charlie_finding_projection_cursors.sequence` and the stable
   `last_error_code`; do not edit the cursor or copy a central finding locally.
2. A finding is projectable only when its central session maps to the active
   connection and its resource digest exactly matches that local session's
   management-plane scope. Historical sessions from a replaced installation and
   findings for resources outside that exact scope are non-applicable and are
   skipped while the cursor advances.
3. Valid records are upserted by Charlie finding ID through the existing local
   finding/alert path. Restart and replay must retain one local row, one dedupe
   identity, and the latest repeat count.
4. Workflow, status, block, severity, and repeat count come from Charlie's
   authoritative finding record. The encrypted evidence envelope supplies only
   scoped presentation fields and must never override newer workflow state.

### Actionable finding requires operator attention

High and critical Charlie findings appear in Astronomer notifications and the
Charlie Investigations view. A finding must state the affected management-plane
resource, observed symptoms, likely cause, verification steps, risk/impact, and
the least disruptive next action. In `read_only`, it is advisory. In `approval`,
the exact proposed action deep-links to an approval; reject/expiry executes
nothing. In `auto`, policy/RBAC/safety/budget/cooldown blocks create or update a
finding instead of bypassing the control. Acknowledge, dismiss, and resolve
require live affected-resource authorization and are audited.

### Flapping Astronomer downstream agent

Correlate only Astronomer-owned connection/heartbeat history, auth results,
agent/protocol versions, structured agent errors, tunnel owner replica, recent
Astronomer rollouts, server/Redis/locator/ingress/TLS health, and fleet/region
patterns. Charlie may remediate only an allowlisted Astronomer-side component.
For a likely downstream-side network, agent, credential, or Kubernetes API
cause, return exact operator checks; do not enter or mutate that cluster.

## Upgrade, rollback, disconnect, and disaster recovery

- Upgrade only immutable reviewed chart/image digests. Keep one ready replica,
  verify standby/leader and disclosure, then retire prior artifacts.
- Rollback selects an already reviewed digest and never downgrades the pinned
  bridge/agent protocol outside its declared compatibility range.
- Feature disable performs a reversible suspension: disable locally/remotely,
  stop triggers, settle streams, snapshot the owner-bound Argo desired state,
  and remove the agent workload and private network surface. Re-enable restores
  those runtime objects in disabled/installing mode; it never restores prior
  write authority. Explicit uninstall still removes all owned agent resources.
- Permanent disconnect requires typed confirmation and Charlie-side revocation.
  It never deletes Charlie Central or any downstream resource.
- Backups retain encrypted local trust and metadata but not onboarding packages,
  plaintext credentials, or conversation content. After restore, rotate trust,
  verify the same installation binding, and re-enroll if credential state is
  unavailable.

## Escalation stop conditions

Stop and emergency-disable on signature/digest mismatch, unknown protocol,
replica identity reuse, multiple leaders, stale fencing accepted, missing audit
or receipt, disclosure of a destructive/downstream/open-ended tool, direct
server/worker egress to Charlie Central, credential/content leakage, cross-user
or cross-deployment visibility, or a write whose result cannot be reconciled.
