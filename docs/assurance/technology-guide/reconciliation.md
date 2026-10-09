# Current findings and prior-plan reconciliation

All anchors were re-opened at input commit `6c3da188`. No E01–E24 finding is closed by W00. Open denotes observed source behavior or an evidence gap, not an exploit or its frequency. Earlier plan statuses/logs are historical.

| ID | Current observation / source | Disposition / owner | Prior work reused |
| --- | --- | --- | --- |
| E01 | `go.mod:3`1.26.6 repeated in Docker Go builders | Open authority/freshness, W01 | 016 tooling and Go ADR |
| E02 | Dependabot ignores major updates in five ecosystems | Open, W01 | Existing updater/lock generation |
| E03 | security-analysis gosec retains `-no-fail`, no later severity enforcement | Open, W02 | 016 security evidence/waiver validators |
| E04 | Main protection still HTTP404; rulesets `[]` | Open external policy, W02 | 016 gate identities; no settings changed |
| E05 | builtin provisioner line 107 loads embedded catalog; Source/Values persist those inputs | Open consumed wiring, W03 | 026/030 built-in and Flux lifecycle |
| E06 | manifest mirror mapping rewrites agent/Flux/bundle, not consumed catalog/values | Open, W03 | Existing strict mirror/trust tests |
| E07 | catalog selects public GHCR Trivy DB/Java DB | Open offline supply/freshness, W04 | 026 automatic scanner behavior |
| E08 | catalog-airgap script line 101 extracts before archive-provided checksum validation | Open authenticity/order, W03 | Hardened kit extraction/signature fixtures |
| E09 | airgap runbook lines 140–141 requires fresh_only/six; validator requires fresh_and_upgrade/seven for 1.2.x | Open docs drift, W03 | Existing release fixture validators |
| E10 | OpenAPI rollout state string; Go cluster set omits DB-allowed aborted/rollback_failed | Open parity, W06 | Model/scheduler tests; no assertion these values occur in DB |
| E11 | Shared offset caps 10,000; catalog uses it; page metadata advertises continuation above ceiling | Open traversal regression, W07 | 027 scoped K8s pagination/cursor tests |
| E12 | transport line 116 standalone refresh POST lacks deadline despite ordinary 30-second transport | Open shared browser lifetime, W05 | Cookie/CSRF/singleflight; server rotation already exists |
| E13 | Cancellation gate fixed module list; tool queryFns drop signals | Open adoption/inventory, W05 | 027/031 generated Query ownership |
| E14 | SIEM/alert-channel catch comments defer to toasts; dash-prefixed components lack broad adoption | Open form/error adoption, W05 | 031 form kit/primitives already implemented |
| E15 | ResourceHandler spans domains;340 top-level production handler Go files | Open ownership, W13 | 016 boundaries, 026 fixes,030 caching; no blanket handler defect |
| E16 | systemrollout/catalogapp fixed SQL; governance scans canonical SQL only | Open query/discovery boundary, W08 | Tx/audit/operation tests; no injection allegation |
| E17 | Durable JSON broad shape; inhibition worker/handler ignore three unmarshal errors | Open reader schemas, W09 | 016 governance/inhibition fixtures; valid empty rules preserved |
| E18 | Strict decoder exists; user create uses permissive decoder | Open client/endpoint inventory, W09 | Existing bounded validation/error envelopes |
| E19 | Browser support Firefox/Safari; Playwright scripts/projects select Chromium | Open attributable coverage, W12 | 027/031 historical Chromium/axe; WebKit is not all-Safari certification |
| E20 | Integration script uses pgvector pg 16; production CNPG17.11 | Open version fidelity, W12 | 016 real DB isolation; physical CNPG recovery distinct |
| E21 | Telemetry Compose old versions/Promtail 3.4.2 | Open collector renewal, W10 | 030 metrics/query-tracing/cache work retained |
| E22 |65 tracked recursive `scripts/**/*.sh` files,18 over 200 lines; live-browser 884 lines | Open typed lifecycle/characterization, W14 | Existing 016/028/029/030 safety/evidence fixtures |
| E23 | Catalog Python 3.12, floating Actions, two direct pins/no complete lock | Open graph/gates, W15 | Catalog unittest/schema generator; signed pair W03/W15 |
| E24 | FIPS ADR expressly excludes qualified artifacts | Accepted unsupported capability; W16 decision track | 016 capability ADR; no false-certification allegation |

Rejected assumptions: JWT refresh rotation/replay-family revocation exist in SQL/handler/auth code and `internal/db/sqlc/refresh_sessions_integration_test.go`. Key rotation/historical decryption exist in `internal/auth/crypto_test.go` and `cmd/keyrotate`. Structured slog, OTLP/HTTP, real DB tests, signed kits, generated consumers and local Flux exist. Their presence is not fresh whole-product qualification.

| Prior plan | Reuse / residual boundary |
| --- | --- |
|016 production/GA | Auth/key/audit/DR/release-policy tests; external claims remain bound to actual release exceptions |
|026 platform/workflow | Historical Local CI 25/25 and workflow/agent fixes retained; current E03/E04/E07/E17 still need closure |
|027 navigation | Marked DONE historically; scope/pagination/form/query patterns reused; E11/E13 distinct boundaries |
|028 all offerings | Offering denominator, runner and estate preflight; no current all-offerings claim |
|029 independent estate | IaC statically implemented; provider/candidate/functional-run prerequisites still open |
|030 efficiency | Integrated historical instrumentation/cache/backup; performance/soak/recovery remain open; W10 builds on it |
|031 UI | Existing primitives/migration retained; backend filter/table and hand-QA residuals not duplicated |
