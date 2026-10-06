# Delivery observation freshness

Astronomer distinguishes the time Kubernetes state was observed from the time a
tunnel message arrived. This extension prepares shared observation caches; the
current runtime still uses direct Kubernetes probes. Cache-backed producers are
separate follow-up work. The component UI consumes source freshness before those
producers can be enabled.

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
timestamp. Optional kinds that the direct probe could not read are currently
omitted; per-kind availability reporting belongs to the subsequent snapshot work.
Successful observation and healthy reconciliation are separate: a current
observation can correctly report an unhealthy workload.

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

The existing five-minute status heartbeat floor equals the fleet stale threshold.
Component UI age handling is implemented below. The cache rollout still needs
refresh/heartbeat headroom and end-to-end integration verification; receipt
restamping must not be used to conceal this boundary.

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
