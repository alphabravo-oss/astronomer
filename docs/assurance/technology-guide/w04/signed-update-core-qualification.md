# Signed update, supplier capture and freshness policy qualification

The corrected frozen source `c5de7e062f9173d4c081b1985604db990a2b4c76` passed the
full distribution and resolver race suites: 75 parent tests, 294 passed cases,
zero failures and zero skips. The actual selected Cosign 3.1.3 update fixture
passed in 0.96 seconds. Narrow vet passed. The constructor now rejects a
tag-bearing repository even when its subject digest is valid. The oversized
GNU archive fixture reaches the actual finite inspector and checks its rejection.
The [initial failed run](supplier-capture-baseline.md) remains immutable.

Separately, four exact reviewed scanner freshness parents passed under race:
48 cases, zero failures/skips. Scanner vet passed. The selector and all four
names are retained; this does not claim execution of the whole scanner suite,
any database/cluster fixture or an actual workload scan job. It verifies the pure
model's image/generation/scanner/database identity comparisons, independent
expired-report state, bounded age, report TTL, explicit unknown/stale states and
preservation of known findings.

The [compact evidence index](signed-update-core-evidence.json) references the
original guarded command records and raw events rather than repeating their
2576 source/module SHA maps. Each invocation captured equal before/after source
snapshots and the same exact Git tree. Selected Go 1.27.2, GOMAXPROCS=4,
GOFLAGS=-p=2 and Cosign bytes are bound. Capacity passed before each gate and at
closure. All subprocesses closed before handing back the shared compiler window.
The checker validates the retained failed baseline and corrected results; local
unsigned JSON cannot approve a release.

The source-only receipts describe their initial freeze time; these later actual
results are a separate qualification packet. Canonical signature-first consumers
still run before the shared strict OCI parser, and signed-feed payload inspection
still compares actual metadata with authenticated claims. New supplier capture
proves reviewed transport/content only, then the same complete two-pass archive
core derives metadata from an owned file. It does not invent an upstream signature.

Whole supplier network layer transfer, current supplier cadence/expansion,
configured publisher endorsement and OIDC execution, monotonic durable projection,
actual scan-used provenance, API/UI composition, fresh-cache/no-public-egress and
combined main integration qualification remain pending. W04 is in progress and no
future tag or release has been approved.
