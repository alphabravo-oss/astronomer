# Checked SQL pagination type boundary

The native `927151be` collection reported 88 direct pagination casts across 37
handler modules. Each cast was already bounded by the shared parser. This change
provides typed SQL helpers with explicit bounds before narrowing; it does not
claim a newly demonstrated vulnerability or accept a scanner exception.

The 88 replacements comprise 43 offset calls, 42 ordinary limit calls, and three
caller-selected limit calls. Matching each finding's expression column prevents
misclassifying lines containing both a limit and an offset. Alias-derived casts
and other numeric findings remain outside this slice.

The existing `int` limit helpers and response metadata remain unchanged. The
typed helpers preserve their `Atoi` behavior: a representable large integer such
as 2147483648 clamps to the configured limit, while an integer parsing overflow
retains the old default. The offset metadata wrapper uses the same typed
`ParseUint` parser: malformed and negative values, including negative overflow,
remain zero; positive overflow remains bounded to 10000. This refactor preserves
existing endpoint validation of unsupported offsets and cursors.

Before production changes, clean `5bb3be2a` passed three parent tests and 27 leaf
cases covering exact parser expectations, invalid/default ceiling edges, and
actual ToolHandler SQL window agreement with emitted paging JSON. Clean
`c89b8c13` then passed four focused parents and 28 leaves under the race detector,
with zero failures or skips. Handler vet passed.

The full handler race command passed after 221 seconds: 1,576 parents, 2,422
leaves, and 2,620 terminal test events, comprising 2,602 passes and 18 skips. The
packet records every skip name and exact emitted reason: one manual live OCI
test, six pagination PostgreSQL fixture events, three table-view PostgreSQL
fixture events, and eight retired synchronous Dex/SSO contracts. Those skipped
paths are not qualified by this run. All initial and maintained capacity guards
passed, and both source freezes remained clean and unchanged.

[The packet](packet.json) binds original command logs, receipts, runners, source
maps, and the byte-identical external review proposal. No native security rescan
ran in this compiler lease. The unrelated mechanical change in audit.go makes
the prior G109 whole-file digest stale; this packet does not renew it. Current
source governance metadata and security findings require separate exact review
and a fresh combined-source collection. Whole W02 and W07 remain in progress.
