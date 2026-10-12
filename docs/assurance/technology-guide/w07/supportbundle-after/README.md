# Finite support-bundle continuation qualification

The corrected `898d9408` freeze passed the five focused parent tests and eleven
leaf cases under the race detector. Actual typed Kubernetes clients contacted
HTTP fixtures, followed the opaque continuation unchanged, collected 501 items,
and found the referenced TLS certificate on the second page. Expired, repeated,
changed-snapshot, missing-snapshot, oversized, and exhausted continuations emit
an explicit incomplete section without partial projections. Caller cancellation
stops the collector and the actual second-page HTTP request.

The collector uses the existing shared request deadline, a 500-item page size,
ten-page limit, and 5,000-item limit for Events, NetworkPolicies, Ingresses, and
Secrets. It compares response resource versions without adding unsupported
resource-version options to continuation requests. Certificate private keys
remain excluded. This qualification covers these four streams; whole W07
remains in progress.

The first `c3cb51c2` focused run failed because two new HTTP fixture handlers
omitted the JSON content type. The Kubernetes client rejected those responses
before continuation. Its raw failure is retained unchanged. The test-only
`898d9408` correction supplies the content type and requires the exact incomplete
category, preventing unrelated decoder errors from satisfying the negatives.
Production collector bytes did not change between these runs. The original
[behavioral baseline](../supportbundle-before/README.md) remains separate.

The corrected full handler race command exited zero after 221 seconds: 1,572
parents, 2,394 leaves, and 2,589 terminal test events, with no failures and 18
skipped events. These include one manually run live OCI test, six events requiring
the pagination PostgreSQL fixture, three requiring the table-view PostgreSQL
fixture, and eight retired synchronous Dex/SSO tests superseded by durable worker
contracts. Every exact skip name and emitted reason appears in [the packet](packet.json)
and the original command log. These skips do not qualify those integration paths.
Handler vet also exited zero.

The packet retains the original receipts, command logs, runner scripts, source
maps, failed events, exact commands, and artifact hashes. Both freezes remained
clean and unchanged during execution. All fatal initial and maintained capacity
checks passed. The compiler window closed before this documentation was written.
