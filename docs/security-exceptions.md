# Static-analysis exceptions

Written exceptions for the `make security` gate (Technology Selection Guide
ch.06: suppressions MUST cite the rule id and the reasoning; blanket
suppressions MUST NOT happen). Site-level suppressions live in the code as
`// #nosec <rule> -- <reason>`; this file records the one rule-level exception.

| Rule | Scope | Reason | Expiry |
| --- | --- | --- | --- |
| G115 (integer overflow conversion) | whole tree (`-exclude=G115` in `make gosec`) | 159 findings on 2026-10-20, all bounded `int`/`int64`/`uint64` conversions of row counts, byte sizes and replica counts (`internal/handler` 55, `internal/ops` 38, `internal/worker` 18, `internal/charlie` 17, `internal/delivery` 13, `scripts/loadtest` 10, other 8). Fixing each needs a range check or a narrower type and is scheduled as the Phase 1 "bounded integer conversions" sweep; until then the rule would mask real HIGH findings behind noise. | Phase 1 (remove the exclusion when the sweep lands) |

Site-level `#nosec` annotations as of 2026-10-20: G704 x8 (internal forward
services and registered agent endpoints, fixed provider endpoints), G709 x2
(typed decoding of provider responses), G703 x3 (operator-supplied paths on the
developer load-test CLI), G118 x2 (process-owned listener goroutines), G122 x1
(read-only embedded contract directory), G404 x6 (reconnect/load-test jitter).
Each annotation carries its reason inline; `grep -rn "#nosec" --include='*.go'`
lists them.
