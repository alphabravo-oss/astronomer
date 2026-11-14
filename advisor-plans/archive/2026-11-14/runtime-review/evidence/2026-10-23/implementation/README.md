> **Archived 2026-11-14. Historical record only.** See [the sole current working plan](../../../../../../CURRENT.md). This document does not authorize new work or certify the current candidate.

# Plan 032 R01–R03 implementation evidence

Source: `/root/work/astronomer-fixes`, branch `fix/2.1-runtime-verification`, baseline `e222313603` (see the exact full SHA in `source-manifest.json`). Changes are local and undeployed. The existing [master plan](../../../../032-technology-guide-alignment-and-latest-stable-master-plan.md:1426) owns remaining tasks and qualification.

Implemented ordinary REST/stream access-JWT enforcement, shared API-token CIDR enforcement, request-context stream validation, and canonical client-IP propagation through the existing cross-pod proxy. Dedicated refresh/TOTP endpoints, one-use tickets, RBAC and configured proxy trust remain in place. No schema, generated API or frontend source changes.

| Evidence | Command / result |
| --- | --- |
| `auth-before.log` | New JWT-purpose and REST/direct-stream/sibling CIDR regression matrix against baseline: expected failures, exit 1. |
| `auth-after.log` | Same targeted matrix after fixes: exit 0. |
| `forwarding-before.log` | Go overlay restores only the old `ws_owner_proxy.go`; trusted-proxy → trusted-sibling case fails 401 instead of 204, exit 1. Confirms forwarding change is required independently of CIDR enforcement. |
| `affected-packages.log` | First extended run found an over-specific new cancellation test assertion: canceled context is rejected before the fake lookup is called. Removed that internal-call assertion and retained the observable rejection regression. Original failing log preserved. |
| `affected-packages-final.log` | `go test ./internal/auth ./internal/server/middleware ./internal/tunnel ./internal/handler -count=1`: all four pass, exit 0. Includes TOTP/refresh and HA ticket regressions. |
| `security.log` | `GOFLAGS='-p=4' make security`: exit 0. Configured gosec gate passed; govulncheck has zero reachable findings (one module-only advisory); gitleaks scanned 2304 commits, no leaks. |
| `make-check.log` | Full repository `make check`, with `GOFLAGS='-p=4'` and a private root-filesystem TMPDIR: **exit 0**. Lint: 0 issues; vet/format/build passed; race-enabled suite: 15,638 total, 15,606 passed, 32 skipped, 0 failed/errors (417.726 seconds for tests). No production capacity guard disabled. |
| `source-manifest.json`, `source.patch` | SHA-256 for all seven changed Go files, baseline identity, and complete source/test patch. Verify hashes before attributing results to another checkout. |

The new tests cover access/refresh/two TOTP purposes; REST bearer/session-cookie transports; exec/shell/logs/events; rejection before exec upgrade; canceled validation; IPv4/IPv6/mapped IPs; malformed/empty CIDRs; unknown client address; spoofed/untrusted and trusted proxy chains; real HTTP sibling forwarding. The original security findings remain applicable to deployed v2.1.1 until a new candidate passes R16. No live attack or deployment was performed for this slice.

The 32 skips cover unavailable promtool/upstream chart, selected actual signer/private repository/proxy fixtures, a live OCI registry case, and explicitly retired synchronous Dex contracts. They remain qualification gaps under R14/R16; no new authentication regression was skipped. `verification-summary.json` retains their exact names/reasons. All seven source hashes still matched after the final gates.
