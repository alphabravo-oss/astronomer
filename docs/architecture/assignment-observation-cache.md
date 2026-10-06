# Assignment observation cache

Plan 030 E06 introduces a standalone read-only source for accepted Flux
assignments. Runtime selection and event-driven status wiring are a separate
integration step. This source does not apply, delete, acknowledge or persist
assignments, and does not change mutation, audit or tombstone handling.

## Scope and lifecycle

`delivery.AssignmentCache` uses the supplied dynamic client and existing
credentials. Five shared informers watch the supported GitRepository v1,
OCIRepository v1, HelmRepository v1, Kustomization v1 and HelmRelease v2 GVRs.
Every LIST and WATCH has `app.kubernetes.io/managed-by=astronomer-agent`.
The existing enrollment ClusterRole already grants these cluster-wide verbs;
no permissions are added. Restricted credentials produce an explicit denied
source. There is no automatic direct-GET fallback and no informer per assignment
or project namespace. Informer storage scales with owned Flux objects, while
assignment subscriptions and dirty work are capped by the protocol's 10,000
assignment limit (or a smaller caller limit).

Accepted identities must use their deterministic project control namespace and
source/reconciler names and supported API versions. The projected ownership labels
and spec digest remain available to `NormalizeAcceptedObservation`; lookup does
not bypass its ownership fence. The retained projection excludes specs, source
URLs, arbitrary annotations, values and credentials. It preserves sanitized
condition fields, artifact revision/digest and valid inventory references; invalid
inventory slots retain their contribution to the total count. Copies cannot
mutate the informer store or subscription metadata.

`Run(ctx)` belongs to the agent/runtime lifecycle, not a management-tunnel session.
It starts at most five informer workers, uses shared bounded LIST/WATCH freshness
tracking, joins workers on cancellation, and fences retained observations as
disconnected. A terminal source is single-use. Normal watch recovery, resource
version expiry, permissions becoming available, and CRDs installed after startup
are handled by reflector retries, without reconstructing an informer per object.

## Reads and notifications

`ReplaceAssignments` atomically validates and copies the accepted set, updates
its reverse index, removes evicted dirty IDs, and marks only new or changed
subscriptions dirty. Unchanged subscriptions retain pending work without
creating more. `SnapshotAssignments` takes one snapshot per GVR per batch and
returns source/reconciler observations keyed by deployment ID; there are no
recurring direct GETs. Current missing objects become `absent` using the source's
verified observation time. Unsynced, unavailable, denied, stale and disconnected
states stay distinct; lookups never replace timestamps with their current time.
Consumers must not normalize retained noncurrent objects as healthy.

The single-slot `Wake` channel and bounded dirty-ID set coalesce events through
an object-to-assignment reverse index. Lifecycle changes dirty affected subscribed
kinds. `ConsumeDirty` drains this status-only notification state. A later runtime
consumer must send status without treating every Flux event as a desired-state
reconcile request. Periodic status resync remains necessary for quiet-source age
expiry and snapshot/application barriers. Notification state does not own or
modify the accepted checkpoint, so removing an observation subscription is not
an instruction to delete its objects.

Legacy direct observation remains an explicit runtime selection for integration;
this standalone source never silently changes to direct reads when cache access
fails. Existing metrics callbacks accept the five fixed Flux kind labels in
addition to the six typed observation kinds; integration must wire those callbacks
through the existing telemetry path. No public endpoint is added.

## Verification and remaining qualification

```sh
go test -race ./internal/agent/delivery ./internal/agent/observation
go vet ./internal/agent/delivery ./internal/agent/observation
```

Counting-fake tests establish a 2N direct-GET baseline for 1, 10 and 100 accepted
assignments. Ten cached batches at each tier perform five initial LISTs and five
WATCHs, with zero GETs. Other tests cover normalization equivalence for all four
source variants, updates, deletes, source-time absence, immutable snapshots,
missing CRDs, initial/revoked permissions, 410 expiry, interrupted watch recovery,
concurrent subscription/read activity, bounded notification eviction and canceled
startup/shutdown. These are local source tests, not proof of real-agent assignment
tiers, live Kubernetes watch behavior, end-to-end freshness or production scale.
Runtime negotiation/selection, dirty-status scheduling, checkpoint fences and
live p95 freshness qualification remain E06 integration work.
