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

1. Leave the effective mode at `read_only` or `disabled`; changed disclosure
   invalidates prior automation acknowledgement.
2. Review the exact added, removed, and changed capability schemas, effects,
   target bounds, preconditions, verification, and auto eligibility.
3. Confirm no shell, exec, arbitrary HTTP/SQL/Kubernetes operation, Secret read,
   downstream operation, destructive action, or open-ended selector appears.
4. Acknowledge the new digest explicitly. Rebuild automation allowlists rather
   than carrying unknown entries forward.

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
3. Charlie v1 rotates enrollment and artifact credentials only by issuing a
   signed replacement package. Charlie atomically revokes the prior generation;
   Astronomer validates and installs the replacement, verifies bridge/MCP
   identity and readiness, then prunes superseded owner-bound material. There
   is no separate Astronomer-side credential-rotation RPC.
4. Replayed or ordinal-mismatched enrollment is an incident. Revoke/replace the
   package and inspect Charlie's enrollment audit without exposing token data.

### Agent image or chart pull failure

Confirm the onboarding package points to Charlie OCI by immutable digest, the
artifact credential is unexpired and repository-scoped, and the registry/Blob
store health is green. Do not fall back to `latest`, an unreviewed public image,
or an inline registry password. Roll back to the previously verified digest if
the new artifact cannot be read back and verified.

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
