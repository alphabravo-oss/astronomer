# Frozen supplier capture and signed update baseline

The first actual Go qualification on source `0fdc71cb4ae3dd2f08c403fbac80309af5d25191`
failed. The full distribution and resolver race command used Go 1.27.2,
GOMAXPROCS=4, GOFLAGS=-p=2 and the retained selected Cosign 3.1.3 binary.
[Raw test events](supplier-capture-race.jsonl) and the
[source-bound gate receipt](supplier-capture-gate-results.json) preserve this
failure. All tracked Go inputs and module files stayed byte-identical before and
after execution. These local results do not grant release authority.

Resolver passed in 4.623 seconds. Distribution failed in 3.618 seconds, with two
failed parent/leaf pairs: the signed-update constructor accepted a `:latest`
repository, and the oversized-member negative fixture asked the tar writer to
encode size 8589934593 using USTAR. That format cannot encode this size, so the
fixture failed before reaching the inspector. There were 288 passed cases, four
failed case events, 73 passed parents and zero skips. This is not a passing
qualification and the oversized-member rejection had no executed coverage yet.

`TestSelectedCosignDatabaseUpdateToCanonicalLoader` did execute and passed in
1.01 seconds. Narrow vet passed in 4.418 seconds; capacity guards passed before
race and vet and at closure. Every compiler/runtime process exited before the
window was handed back. New supplier capture/inspection tests ran, but their
passes do not make the failed full package gate green. Scanner freshness tests
were outside this two-package invocation and remain unexecuted here.

Corrections must reject a tag-bearing repository even when the following subject
digest is valid, and encode the deliberately oversized negative using GNU tar so
the actual bounded inspector performs the rejection. A corrected source freeze
and independently retained rerun are required. Whole supplier network transfer,
payload cadence, production OIDC publication, durable transitions, actual
scan-used provenance and fresh-cache/no-egress remain pending.
