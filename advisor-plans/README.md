# Advisor plans

Plans produced by deep-dive advisory audits. Product/feature plans remain under `../plans/`.

| Plan | Status | Notes |
|---|---|---|
| [000-enterprise-quality-rancher-parity-master-plan.md](./000-enterprise-quality-rancher-parity-master-plan.md) | **SUPERSEDED residual** | Full 2026-07-08 multiagent assessment. Many Wave A–C items landed on disk (often uncommitted). Do not re-open §4 closed items without new evidence. |
| [001-residual-enterprise-ha-ssrf-parity-master-plan.md](./001-residual-enterprise-ha-ssrf-parity-master-plan.md) | **REVIEW** | **Active residual program (2026-07-09).** All open P0–P3 findings, waves A–D, tasks, testing, validation, child-plan split. Against `2991f9d` + residual tree. |

## Recommended execution order (from 001)

1. **Wave A** — CORR-R01 atomic claims → CORR-R03 backup claim-first → SEC-R02/R03/R08 SafeClient → AUTH-R01 password settings  
2. **Wave B** — SEC-R01 tunnel2 auth → SEC-R04/R05/R06 operator-URL SSRF → AUTH-R02/SCIM-R01/DR-R01  
3. **Wave C** — CORR-R02 Redis event bus → SEC-R07 SSE RBAC → paging/leader/F7-b/scale/CI  
4. **Wave D** — tunnel cutover, explorer forms, git catalog, docs, product ns-RBAC default decision  

Child executor plans (`002-…`) are written only after master-plan **001** approval (see §10 of 001 for suggested split).

## Waivers

| ID | Owner | Date | Reason |
|---|---|---|---|
| *(none yet)* | | | |
