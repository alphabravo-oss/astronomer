# Delivery observation freshness

Astronomer distinguishes the time Kubernetes state was observed from the time a
tunnel message arrived. Negotiated delivery uses shared typed observation stores and bounded discovery/operator projections. Legacy sessions retain direct probes. Both the remote agent and embedded local agent share their existing StateSubscriber with delivery and health reporting. The component UI interprets source freshness independently of receipt time.

## Session negotiation and mixed versions

Agents advertise `delivery-observation-v1` in existing CONNECT `capabilities`.
A supporting server returns the supported intersection in optional CONNECT_ACK
`capabilities`. ACK decoding already tolerates unknown fields, so old agents
continue to accept the ACK. Delivery request/status decoding remains strict.

A new agent talking to an old server receives no acknowledgement and continues
**direct-observation legacy mode**, omitting every `observation` field entirely.
It must not merely strip metadata from a cached response. Negotiation resets on
disconnect and before every handshake attempt, including failed handshakes.
Support never transfers from one server session to another.

Queued delivery observations from a supporting session can outlive that session.
Before writing to a legacy peer, the tunnel discards extended status snapshots
and state requests, increments the bounded `observation_contract_changed` drop
reason, and wakes delivery to re-observe directly. A waiting state request is
released immediately and semantic-status suppression is reset. Mutation replies,
audit frames, and legacy-shaped observations are not discarded by this guard.

## Source observation contract

Controller inventory and each reported system component may carry:

```json
{
  "observation": {
    "state": "current",
    "observed_at": "2026-10-06T04:00:00Z"
  }
}
```

The known states are `current`, `stale`, `unsynced`, `denied`, `absent`,
`disconnected`, and `unavailable`. `current`, `stale`, and `absent` require a
nonzero source timestamp. Other states may omit the timestamp when no source
observation exists. Omission is distinct from an observation at receipt time.

Timestamps more than 30 seconds into the future are rejected. `current` timestamps
older than five minutes are rejected; consumers must also age stored current
observations into stale state rather than assuming a historical label stays
current forever. Source watchers may impose stricter deadlines. Noncurrent
controller inventory cannot report `ready=true`; noncurrent components cannot
report `health=healthy`. The nested object is optional for legacy payloads, but
negotiated controller/component metadata must be consistently present.

Direct probes timestamp successfully obtained projections. Failed discovery or
controller reads produce `unavailable`, without inventing a successful source
timestamp. Optional kinds that the legacy direct probe could not read remain omitted. Negotiated shared inventory instead reports per-kind availability explicitly.
Successful observation and healthy reconciliation are separate: a current
observation can correctly report an unhealthy workload.

## Shared typed source

`StateSubscriber` owns one informer per Deployment, StatefulSet, DaemonSet, Pod,
PersistentVolumeClaim, and StorageClass. PVC and StorageClass typed projections
replace their metadata watchers; they do not add duplicate watches. Existing live
invalidation/replay and health inventory consumers reuse the same stores.
Projections retain required metadata, controller identity/hardening switches,
readiness, images, resource requests/limits, and storage facts. They omit workload
environment values, arbitrary command arguments/annotations, storage parameters,
and secret bodies. Returned snapshots are deep copies of projected objects.

A compact namespace/name/UID/resource-version fingerprint set tracks completed
paginated LISTs and subsequent watch changes. A snapshot is current only after
its informer store matches that set and the individual kind has synchronized.
Initial `HasSynced` alone never certifies a replacement LIST after an expired
watch. Bookmarks are explicitly requested; WatchList initial-event semantics are
not used by these tracked informers. A successful watch-open or snapshot read
never renews source time. Successful LIST completion, watch events, and bookmarks
supply candidate times, certified only when the application barrier matches.

LIST requests have a 15-second context bound and watches roll over after 75
seconds. Source age is bounded to four minutes, below the protocol's five-minute
ceiling. Quiet servers that do not provide bookmarks schedule a repair LIST when
another full watch plus LIST/grace budget would cross that age limit: with the
defaults, after 150 seconds without progress. Planned repair retains still-fresh,
matching evidence for at most 20 seconds while the reflector relists; it cannot
revive a disconnected source whose existing grace has expired. A replacement
revision must pass the store application barrier before it becomes current.
Normal timeout/clean EOF gets five seconds of reconnect grace with its **old**
time; a resumed stream must show progress to recover after clean-EOF grace.
Explicit watch errors, 403 permission loss, and 410 expiry immediately invalidate
current state. Cancellation fences retained stores even during reflector backoff.
Each kind fails independently; a denied PVC store does not stall healthy workloads.

Steady snapshot reads make zero Kubernetes calls. Bookmarkless repair adds one
LIST per kind per 150 seconds, an 80% lower steady-state rate than one per 30
seconds (90% lower than one per 15 seconds). These rates exclude startup, failure
recovery, discovery, and dynamic operator inventory, and are not live benchmark
acceptance. The local fake-client baseline measured ten direct platform probes
at 60 typed LISTs plus 30 controller GETs; six tracked kinds bootstrap with six
LISTs/six WATCHs, then 600 snapshot reads add zero API calls.

`astronomer_agent_observation_requests_total{astronomer_instance_id,kind,verb,outcome}` counts
actual LIST/WATCH call results. Separate
`astronomer_agent_observation_watch_events_total{astronomer_instance_id,kind,outcome}` counts
stream closure/errors, rollover, and synthetic repair. Never sum lifecycle events
as API requests. Kinds are the six fixed types; verbs are `list`/`watch`; outcomes
are fixed `success`, `error`, `denied`, `expired`, `canceled` for API results and
`closed`, `rollover`, `repair`, `error`, `denied`, `expired`, `canceled` for lifecycle
events. No resource names, namespaces, error strings, or tenant labels are added.

Negotiated delivery takes its controller deployments and platform workloads,
Pods, PVCs and StorageClasses from these stores. Legacy peers explicitly use the
original direct path. A denied, disconnected, stale, or unsynced source never
triggers a per-tick direct fallback. Optional unavailable kinds have explicit
availability rows; truly absent resources remain distinct. Optional storage or
operator permissions do not disable independently verified Flux readiness.
Controller identity, pinned images, hardening flags, and warm-standby readiness
use the same evaluator in both paths. Source times are combined conservatively
across fields in an aggregate row. Typed snapshots are sampled after bounded
refresh I/O, so revocation during that work cannot be hidden by earlier data.

Discovery refreshes fixed version, Flux API, and API-group endpoints through
context-aware REST requests. Production never wraps uncancellable discovery
methods in detached goroutines. Discovery and five optional dynamic projections
have separate coalesced two-minute refreshes and 15-second context bounds; waiting
callers can cancel without starting duplicate work. Refresh deadlines start
before work, so request latency cannot extend certificate health beyond public
`notAfter`. Dynamic caches retain bounded public component projections and
snapshot counts, not complete custom-resource bodies. Truncated detail slices are copied into bounded backing arrays; aggregate totals remain intact. The snapshot-count map scales with distinct claim identities and is replaced on refresh, so total memory is not a fixed constant independent of cluster size. Errors replace healthy
cached evidence with explicit unavailable/denied rows at the next refresh.
Permission changes without a watch signal can therefore take up to the bounded
refresh interval to be detected; receipt time never extends that interval.

Controller identity/generation/image changes and relevant CRD metadata revisions
invalidate these refreshes. The CRD definition watcher uses the existing metadata
factory and existing credentials, with no RBAC changes. If CRD metadata watch
permission is denied or metadata-client construction fails, periodic discovery
still detects API changes, with its two-minute bound. Discovery failures remain
unavailable rather than proving an API is absent.

`astronomer_agent_delivery_observation_refresh_duration_seconds` records actual
coalesced refresh work with fixed `source` (`discovery`, `dynamic`) and `outcome`
(`success`, `unavailable`). Fixed-source `delivery_observation_source_age_seconds`
and `delivery_observation_source_available` gauges are sampled diagnostics.
`delivery_observation_sampled_at_timestamp_seconds` exposes producer sampling
Unix time; `delivery_observation_observed_at_timestamp_seconds` exposes preserved
source Unix time, or zero when unknown. These metrics use the existing
`astronomer_agent_` prefix and `astronomer_instance_id` label. Dashboards must gate
on producer sampling recency and derive source age from observation time; a live
server scrape does not prove its embedded delivery producer is still running.
No public endpoint or additional transport is introduced. Remote-agent metric
exposure remains a deployment/transport concern, not implied by registration.

Local integration tests measure ten modern probes with zero recurring typed
LIST/GET calls after informer synchronization, one coalesced discovery refresh,
and five initial dynamic LISTs. Controller changes and CRD revisions invalidate
only the bounded refreshes. These tests do not establish live Kubernetes watch
behavior, production request reduction, or live soak acceptance.

## Persistence, API, and coalescing

The existing controller `observed_at` database column receives the validated
source timestamp. Never-observed inventories store SQL NULL. Legacy agents retain
the existing receipt-time semantics. Component observation objects persist in the
existing `system_components` JSON; no database migration is required.

The public inventory API already exposes nullable controller `observed_at`.
Noncurrent controller availability appears through `ready=false` and a bounded
`observation_<state>` error code. Known distribution/version incompatibilities
take precedence; missing identity does not establish incompatibility. Component
`observation` is described in source OpenAPI and generated client types. Its
optional timestamp is omitted when unknown, rather than emitted as JSON null.

Semantic digests exclude source timestamps and retain observation-state changes.
Canonicalization copies nested metadata without mutating caller-owned snapshots.
Higher-sequence, semantically coalesced messages still persist their supplied
source time. Retransmitting an unchanged snapshot does not renew its age.
Session and sequence fences remain authoritative for duplicate/replayed messages.

The status heartbeat floor is one minute, below the five-minute fleet/UI stale threshold. The shared typed source has a four-minute hard age ceiling and proactively repairs quiet watches after 150 seconds. Heartbeat tests preserve unchanged source times across reports; receipt restamping never conceals source age. Live timing/soak acceptance remains outstanding.

## Component presentation

The system-component list and detail pages use each component's source state,
not controller receipt time, to interpret health. Current observations older than
five minutes display as stale. Missing, invalid, or excessively future current
timestamps display as unavailable. Explicit stale, unsynced, denied, absent,
disconnected, and unavailable states replace cached health; the same state gates
nested resource health and PVC phase badges. List counts, sorting, and health
filters use that same effective state. Legacy payloads retain their reported
health; detail labels source time as unavailable rather than inferring it.

One existing 30-second UI clock per page advances age even when query data stays
unchanged. It performs no network requests and is cleaned up on unmount. This
bounds display expiry lag to one clock tick while preserving live-stream polling
suppression. Detail source times are shown only when valid.

## Verification

```sh
go test ./pkg/protocol ./internal/delivery/status ./internal/delivery/compatibility ./internal/agent/delivery
go test -race ./internal/agent/observation
go test -race ./internal/agent -run 'Test(StateSubscriber|SharedObservation|SharedDeliveryReal|DiscoveryRevision|DeliveryComposition)'
go test ./internal/agent -run 'Test(ObservationNegotiation|QueuedExtended)'
npm --prefix frontend test -- src/lib/system-component-freshness.test.ts 'src/routes/dashboard/clusters/$id/delivery/system-components/-freshness.test.tsx'
```

Mixed-version tests use a frozen legacy inventory shape with strict decoding,
exercise actual queued-frame rejection before WebSocket access, and verify retry,
capability reset, stale/future rejection, immutable digests, and source-time
preservation on coalesced persistence. These tests do not establish live
multi-version deployment, real watch freshness, or live browser acceptance.
Frontend fake-clock tests cover unchanged-query expiry, consistent counts and
badges, nested evidence, legacy behavior, and clock cleanup without additional
API requests.

## Assignment source observations

The unreleased `delivery-observation-v1` contract also covers optional top-level
`observation` on each `DeliveryDeploymentStatusV2`. The assignment source uses
five ownership-filtered Flux informers and one batch snapshot per status pass;
see [Assignment observation cache](assignment-observation-cache.md).
The inventory observation marker selects the same contract for the whole pass.
Modern consumers never fall back to direct assignment GETs when a source is
unsynced, denied or otherwise unavailable. Legacy passes keep direct reads and
omit assignment observation fields. The final queued-write gate recognizes both
inventory and deployment extensions and retries obsolete observation frames after
a legacy reconnect, without discarding mutation/audit frames.

For a current pair, the source timestamp is the earlier verified timestamp of
source and reconciler. A known missing object is `absent`. If either dependency
has never been verified, no combined source time exists: `current`, `absent` and
`stale` without that required time become `unsynced`. Noncurrent assignments
report `phase=unknown`, `observation_<state>` and no cached conditions, revision
or inventory health. Ownership or checkpoint identity mismatches fail closed.
Nested timestamps are excluded from semantic digests; state transitions remain.

The mandatory legacy wire `observed_at` remains an assessment timestamp. For
modern reports it is never used as database source freshness. Modern
`cluster_deployments.last_observed_at` receives nested source time or SQL NULL.
The public deployment `inventory.observation` preserves the same optional
state/time object in the existing JSON column. Modern local apply/prune/deletion
failure decisions and deleting/removed tombstone statuses are an explicit
exception: they retain existing phase/error semantics, omit source observation,
and store NULL source time. They must not carry cached ready conditions or
revision data. Legacy rows retain their previous timestamp semantics.

Modern transition/rollout decision events use server receipt time, separately
from source time. A higher-sequence semantically coalesced report may refresh
source metadata through the existing desired-generation/spec-digest/session/
sequence fences, while preserving semantic deployment fields. That refresh emits
no deployment transition, deletion-finalization, rollout, event, outbox or snapshot
ack effects. The existing independent post-commit readiness-repair callback still
runs so its documented periodic retry behavior is preserved. No migration or
hand-edited SQLC output is involved.

The runtime owns the cache under its lifetime context and joins it on shutdown;
a management-tunnel reconnect does not recreate watches. It synchronizes
subscriptions from loaded and updated accepted checkpoints and evicts completed
removals. Dirty IDs schedule status only, with one non-resetting 250 ms debounce
and an attempt interval capped at 15 seconds; periodic status resync remains.
The same scheduler is serviced while waiting for a desired-state response, using
that response wait's deadline for any status inspection. Mutation passes remain
single-owner and serialized. Local tests establish bounded scheduling and zero
recurring assignment GETs, not the live end-to-end p95 target; long mutation passes
and deployment qualification still need measurement.

The frontend deployment view consumes `inventory.observation`; source absence
must not be replaced with `updated_at`, event time or the compatibility assessment
field. Reported-phase filters/counts remain reported-state views, not claims of
fresh source health. UI expiry/presentation work is tracked separately from this
backend integration.

### Attributable Kubernetes request measurements

The agent shared Kubernetes client family now installs a copied-config transport
wrapper at both K8sProxy constructors and embedded-agent client initialization.
Subsequent clients built from the returned RESTConfig inherit it, including the
browser member fixture. Existing authentication/token-file rotation, prior
transport-wrapper order and rate settings are retained. A dedicated discovery
client supplies the inventory consumer for legacy methods that discard caller
context; it reuses the typed client's discovery limiter and HTTP timeout policy.
No additional typed/dynamic clientset, informer or metrics endpoint is introduced.

`astronomer_agent_kubernetes_requests_total` has the existing instance label plus
fixed `consumer`, `operation`, `resource`, and `outcome` labels:

- Consumers: `shared_observation`, `delivery_inventory`,
  `delivery_assignment_observation`, `other`.
- Operations: `list`, `get`, `watch`, `discovery`, `other`.
- Resources: the fixed API-group/resource allowlist in
  `internal/agent/kuberequests/classify.go`; unknown resources map to `other`.
- Outcomes: `1xx` through `5xx`, `transport_error`, or `other`.

Counts describe observable RoundTrip attempts, including client-go retries,
pagination requests and retries made by pre-existing transport wrappers. Watch
establishment counts once; watch frames, TCP connection attempts and retries
internal to Go's underlying http.Transport are not additional counts. Nested
instrumentation on copied configs records only the innermost attempt, using
request-local spans without mutating the caller's request or context. Mutations,
subresources and unknown paths remain `other`; no namespace, object name, URL,
query, resource version or credential becomes a label.

Consumer tags are applied inside the six typed informer LIST/WATCH callbacks and
five assignment-cache callbacks. This is necessary because factory.Start does
not propagate context values. Direct legacy assignment reads are tagged only in
Runtime.observe; other Executor.Get callers remain `other`. Both direct and
shared controller/system probes tag their own reads and discovery/dynamic work.
Health, checkpoint, mutation and other shared-client traffic is counted but remains
`other` unless it enters an explicitly tagged observation callback. Other process
clients, external tools, independent Helm/client construction and upgraded stream
paths that bypass these transports are outside the stated client-family coverage.

`astronomer_agent_kubernetes_request_instrumentation_info{schema="v1"}=1` appears
when an instrumented transport is installed, with the existing instance label.
It proves installed transport-family instrumentation, not all-process coverage,
member identity, completeness across process restarts, or successful requests.
Missing request series alone are not proof of zero activity. Future collectors
must verify this schema sentinel, metric-target/member identity, sample coverage,
process start/reset behavior and candidate provenance before interpreting an
absent bounded counter combination. Source freshness and completed work remain
independent acceptance conditions.

Historical uninstrumented baselines cannot be compared to these counters as if
they reported zero calls. Backport the same transport instrumentation, consumer
boundaries and label schema to the historical baseline, then run matched workload
windows. Keep delivery-induced reads, shared-store maintenance, other traffic and
repair/retry load separately attributable. This instrumentation increment does
not change collector parsers or dashboard queries and proves no live reduction.
