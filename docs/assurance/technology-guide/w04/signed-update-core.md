# Signed database update core

This source slice implements a closed signed update reader and a pure freshness
evaluator. It has not executed its tests. It does not publish a feed, change an
immutable release parent, project a database assignment, or establish actual
scan-used provenance. W04 remains in progress.

## Signed update and child descriptors

`internal/delivery/distribution/database_update_contract.go` defines
`application/vnd.astronomer.vulnerability-db-update.v1+json`, bounded to one MiB.
The shared JSON contract rejects duplicate keys at every depth, unknown fields,
excess depth, trailing documents and invalid numeric representations. Constructor
expectations bind the base release, distribution digest, catalog digest, candidate
source commit, built-in profile, exact scanner image/version and main/Java schema.
There is one main database and at most one Java database. Every unchanged child
binds its original supplier subject, published subject, manifest/config/layer
digest, exact size and media type. Existing two GiB layer bounds remain intact.

The update has a positive generation, ordered finite UTC clocks and an expiry.
UTC means zero-offset RFC3339 representations; nonzero offsets are rejected.
Upstream metadata has its own digest, bounded size, schema, UpdatedAt and
NextUpdate. An elapsed NextUpdate is rejected when reading a new update. A future
NextUpdate cannot extend the owned maximum age. These signed metadata fields
remain claims until the entire corresponding payload is hashed and its actual
metadata is inspected.

`database_update_loader.go` uses the existing canonical OCI verifier before
reading feed bytes and before reading child descriptors/configuration. Trust,
the network policy and the HTTP client come from constructor composition;
document data cannot select them. The network policy is copied at construction.
No database layer is fetched by this metadata reader. Its opaque authenticated
result returns copies and cannot be forged through a public bool or zero value.
Cancellation preserves `context.Canceled` and `context.DeadlineExceeded`.
Descriptor authentication does not authorize mutation or prove payload
availability, freshness, revocation checks or actual scan use.

The producer must sign the unchanged supplier subject and the update under the
existing publisher policy. This is an Astronomer distribution endorsement;
there is no assertion that the supplier provides a Cosign signature. Production
OIDC identity stays separate from constructor-only ephemeral keyed QA fixtures.

## Pure freshness evaluation

`internal/scanner/database_freshness.go` adds internal types only. Released Go
SDK structs and existing public contracts are unchanged. The evaluator receives
the current desired image/assignment generation separately from the measured
report observation. An image or assignment mismatch returns unknown and requires
a rescan. The measured scanner digest must also equal the accepted scanner
digest; a changed scanner returns stale and requires a rescan.

Main and Java freshness use the measured DB subject/layer/metadata identities,
schema and upstream UpdatedAt/NextUpdate. Missing or mismatched provenance cannot
qualify a clean report. Java not-needed requires explicit measured applicability;
absence of Java findings does not establish it. Feed expiry, revocation, mirror
outage, report expiry, pending/failed scan and DB unknown/stale remain distinct.
Known findings on a completed report stay visible when that report's database
becomes stale. Real composition must also preserve previously known findings
while a newer job is pending or failed.

The finite proposed policy is a six-hour report TTL, 30-hour main maximum age,
48-hour Java maximum age, 48-hour feed lifetime and at most five minutes of clock
skew. These helper defaults are not yet deployed configuration or a selected
payload measurement. The main upstream feed documents a six-hour build cadence
and a 24-hour default metadata update interval; the Java feed documents daily
builds. See the [main DB cadence](https://github.com/aquasecurity/trivy-db#update-interval)
and [Java DB cadence](https://github.com/aquasecurity/trivy-java-db#update-interval).
Actual selected supplier metadata and cadence still require qualification.

Scan completion and local DownloadedAt cannot refresh upstream age. A report
six hours old, or a report using the previous accepted DB, requires a rescan.
The evaluator is deterministic data logic, not proof that a caller authenticated
its inputs. A measured owned job/report integration and atomic accepted-state
projection must precede any API/UI clean-posture decision.

## Pending verification and composition

The prepared tests cover valid bindings, finite clock boundaries, recursive JSON
ambiguity, invalid constructors, missing feed/child signatures, wrong key, wrong
base, expired feed, changed layer descriptors, undeclared child destinations,
snapshot isolation and cancellation. Registry counters assert that failed feed
authentication cannot reach the feed payload and that descriptor verification
does not read database layers. A separate selected-Cosign fixture signs the
feed and children using an ephemeral QA key; an observed proxy negative control
distinguishes deliberate public attempts from zero unexpected trust requests.
These tests are source only until the selected toolchain executes them.

Remaining work includes whole supplier payload hash/metadata inspection; signed
producer/offline update composition; explicit typed internal repository policy;
durable monotonic generation/replay checks with actual release/catalog revocation
and assignment ownership; owned measured job/report provenance; new typed API/UI
status; rescan scheduling; and fresh-cache disconnected runtime qualification.
W07 has potential measured index migration leases 076–078. W04 migration numbers
079 or later require root allocation before any DDL. No schema, public API,
runtime configuration, database, shared cluster or external settings were changed
by this source slice.
