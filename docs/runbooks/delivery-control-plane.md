# Flux-native delivery control-plane operations

This runbook covers the Astronomer-owned delivery API, durable rollout engine,
cluster-agent protocol, and the exact signed Flux distribution. Treat
PostgreSQL as the control-plane authority and the agent-reported Flux inventory
as downstream evidence. Never repair state by editing Flux resources, rollout
rows, generation counters, or agent checkpoints by hand.

Before any intervention, record the release manifest digest, rollout and target
IDs, frozen plan digest, affected cluster IDs, current generation/fence,
controller inventory, recent bounded event codes, and the relevant metric
window. Pause the rollout when continued cohort release could enlarge impact.
Do not include source URLs, rendered values, credentials, Secrets, or raw
manifests in tickets or support bundles.

## Stuck rollout

1. Inspect the rollout timeline and distinguish approval, maintenance-window,
   worker/outbox, assignment acknowledgment, and downstream readiness waits.
2. Confirm a healthy worker owns or can reclaim the expired lease. Check
   `astronomer_task_outbox_*` and `astronomer_delivery_worker_events_total`.
3. Confirm the cluster agent has acknowledged the exact snapshot generation
   and ETag. Follow [delivery assignment lag](delivery-assignment-lag.md) when it
   has not.
4. Confirm source and reconciler objects report the assignment generation and
   digest. Resume or retry through the rollout API with its current ETag; never
   modify a cohort or lease directly.

## Failed rollout or failure budget

The frozen strategy and failure budget are immutable. Group failures by the
bounded reason code, correct the source, compatibility, admission, quota, or
workload problem, then use `retry-failed`. If policy selected rollback, let the
same rollout drive the exact previous bundle assignment. Creating a second
overlapping rollout is not recovery.

## Rollback failure

Pause other rollouts to the affected targets. Confirm the previous bundle
version and source revision still exist in the release/air-gap mirror and that
their verification identity is valid. Inspect admission-policy and immutable
field failures. Retry the failed rollback assignment through the API. If the
previous artifact is unavailable or its signature no longer verifies, stop and
restore the verified mirror or backup; do not substitute a rebuilt artifact
under the old digest.

## Source authentication, signature, or render failure

Use the source resolution’s stable error code to choose the path:

- authentication: rotate only the referenced credential, then verify;
- network policy: validate the explicit hostname plus CIDR allowlist, DNS
  answers, optional HTTPS proxy, and custom CA;
- signature: verify the exact Git object or OCI/chart digest against the
  operator-mounted key or keyless issuer/identity;
- digest mismatch: quarantine the mutable upstream reference and investigate;
- limit exceeded: inspect the artifact rather than raising limits blindly;
- render/health: reproduce with the immutable revision and bounded renderer
  contract, never with a moving branch or tag.

Resolver errors deliberately omit upstream output and secret-bearing URLs.
Inspect sensitive upstream diagnostics only in the source system.

## Helm remediation and Kustomize health timeout

For Helm, inspect the normalized HelmRelease conditions and bounded failure
code, then correct chart inputs or cluster prerequisites in a new immutable
bundle version. Let Helm remediation use the frozen retry/rollback policy. For
Kustomize, identify the unhealthy inventory entries and correct health checks,
dependencies, or admission failures in source. Do not force a reconcile with a
different revision or patch generated objects in place.

## Drift loop or stuck deletion

Repeated drift normally means another actor owns the same field or resource.
Use managed fields and the assignment inventory to identify that actor, then
remove the ownership conflict or narrow the bundle. During deletion, the agent
prunes only objects in the accepted checkpoint after the replacement apply has
succeeded. Never remove finalizers broadly. If a finalizer owner is gone,
follow the component’s recovery procedure and record every exact object before
a narrowly scoped finalizer change.

## Flux distribution or controller outage

Confirm all and only the pinned source, kustomize, and helm controllers are
present with the signed distribution digest and supported API versions. Check
resource pressure, leader election, admission, DNS, and registry reachability.
Use a canaried system-release rollout to repair or upgrade the distribution.
Do not install Flux CLI bootstrap output or a fourth controller alongside the
managed distribution.

## Agent disconnect or stale status

Follow [delivery status stale](delivery-status-stale.md). Verify tunnel session
fencing, clock, protocol version, snapshot acknowledgment, queue/coalescing
counters, and full-resync behavior. Reconnecting must retain the accepted
checkpoint and must not prune workloads while assignments are unavailable.

## Credential rotation and revocation

Write the replacement credential through the source API, verify the source,
and confirm its credential epoch advances. Existing assignments receive only
the new encrypted projection and snapshot epoch; their immutable intent digest
does not change. Revocation fails closed for new resolution. Never copy a
credential between database rows or place it in a rollout/event payload.

## Air-gap mirror drift

Run the release mirror verifier against the signed release manifest. Every
chart, image, Flux distribution, and built-in bundle must retain its exact
digest and provenance reference. Restore missing blobs from the release kit.
Do not retag or rebuild content to satisfy an existing digest.

## Database restore and fresh reinstall

Use the management-plane backup/restore runbook and verify the single v1 schema,
assignment generations, rollout fences, audit chain, and release manifest
before reopening traffic. A v0.3.x database is intentionally rejected without
mutation; greenfield v1 requires a fresh install, with only explicitly exported
configuration and credentials re-entered through supported APIs.
