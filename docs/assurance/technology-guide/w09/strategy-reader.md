# Stored delivery strategy reader

The actual system rollout row reader previously accepted JSON null and a
zero-concurrency strategy, because it only called json.Unmarshal. The named
ScanView regression reproduced both cases before the fix. scanView now invokes
ReadRolloutStrategyV1: a bounded object envelope plus the canonical scheduling
validator. Failures return a content-free integrity category to the existing
caller rather than a usable zero-value strategy. No mutation or tenant lookup
moves into this reader.

A valid v1 strategy with an additive future property still loads. The shared
read-only inventory uses the same parser. Placement inventory uses the canonical
selector validator with the existing omitted/null optional collection adapter;
empty placement still selects nothing. New placement/rollout writes already
marshal typed validated model values, and the expand constraints supplement their
stable primitive wire shapes. PostgreSQL constraints do not independently own
strategy enum values, UUID syntax, Go regexes, duration rules, duplicate selector
semantics, or frozen plan digests.

`go test -p=2 ./internal/delivery/model ./internal/delivery/systemrollout` and
narrow go vet pass. Baseline and fixed output are adjacent to this receipt.
