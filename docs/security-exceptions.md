# Static-analysis exceptions

Written exceptions for the `make security` gate (Technology Selection Guide
ch.06: suppressions MUST cite the rule id and the reasoning; blanket
suppressions MUST NOT happen). Site-level suppressions live in the code as
`// #nosec <rule> -- <reason>`; this file records the one rule-level exception.

| Rule | Scope | Reason | Expiry |
| --- | --- | --- | --- |

Site-level `#nosec` annotations as of 2026-10-20: G704 x8 (internal forward
services and registered agent endpoints, fixed provider endpoints), G709 x2
(typed decoding of provider responses), G703 x3 (operator-supplied paths on the
developer load-test CLI), G118 x2 (process-owned listener goroutines), G122 x1
(read-only embedded contract directory), G404 x6 (reconnect/load-test jitter).
Each annotation carries its reason inline; `grep -rn "#nosec" --include='*.go'`
lists them.

## Resolved: G115 (integer overflow conversion)

The rule-level exclusion was removed in Phase 1. The 157 findings are now
bounded conversions through `pkg/safeconv` (saturating `Int32/Int64/Int/
Uint32/Uint64/Byte`, unit-tested at every boundary); the only `#nosec G115`
annotations are the seven inside that package, each next to the range check
that makes it safe. `make gosec` runs with G115 enabled.

## Resolved: upstream kubectl in `astronomer-shell`

v2.1.0-rc.1 failed the Trivy gate on the upstream dl.k8s.io kubectl (built
with go1.26.8). The shell image now compiles kubectl from k8s.io/kubectl
(`tools/imagebins`, version pinned there) with the pinned Go 1.27.3 builder,
so the scan needs no skip. `scripts/check-image-go.sh` (release workflow and
`make docker-build`) fails any image that contains a Go binary built with a
Go older than go.mod's `go` line.
