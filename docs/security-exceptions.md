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

## Image scan: upstream kubectl in `astronomer-shell`

`release.yaml` scans every image with Trivy (HIGH/CRITICAL, fixed only) and
fails the release on a finding. The shell image ships the upstream signed
`kubectl` release binary (v1.35.9, the latest 1.35 patch on 2026-10-20). Every
current upstream kubectl (v1.35.9, v1.36.5, v1.37.1) is built with go1.26.8,
so Trivy reports stdlib CVE-2026-78667 / CVE-2026-78669 / CVE-2026-97031,
golang.org/x/net CVE-2026-78669 and golang.org/x/text CVE-2026-56851 against
it. They are server-side HTTP/2 / Range / ECH / text-profile denial-of-service
issues in a CLI that runs short-lived inside the shell pod; there is no fixed
upstream binary to pin. The scan skips `/usr/local/bin/kubectl` in that one
image only (`trivy_skip_files` in the release matrix); our own binaries are
built with Go 1.27.3 and current x/net / x/text and stay scanned.

| Scope | Reason | Expiry |
| --- | --- | --- |
| Trivy `skip-files: /usr/local/bin/kubectl`, image `astronomer-shell` | no upstream kubectl built with a fixed Go toolchain yet | the first kubectl patch built with go ≥ 1.26.9 / 1.27.2 — bump `KUBECTL_VERSION` and drop the skip |
