# W06 categorical contracts

The canonical value authority is `docs/openapi.yaml#/components/schemas`. The closed registry in `deploy/contracts/categories.json` records ownership, wire bindings, compatibility, domain generation and complete database CHECK parity without duplicating active value lists. Inventory snapshots are evidence, not another value authority.

## Task status

| Task | Implementation and bounded evidence |
| --- | --- |
| W06.1 | Inventory covers delivery, cluster and deployment states, operations, source/renderer kinds, approvals, credentials, registration, backup/restore, policy, scanner and Charlie. 41 named categories, 51 direct bindings, 117 inventoried high-impact fields and 98 initial discrete CHECKs are recorded; ancillary inline/external categories remain explicitly classified. |
| W06.2 | Registry and inventory distinguish strict writes, neutral unknown reads, historical open backup statuses, Kubernetes vocabularies, and separate Charlie central/bridge states. Existing writer actions are characterized separately from scheduler transitions. |
| W06.3 | Stable categories have named closed schemas. External/historical open fields retain checked adapters; unknown labels use neutral rendering. |
| W06.4 | Persisted cluster `aborted` and `rollback_failed` are explicit terminal readers with no outgoing writer transitions, no resumed effects and no successful interpretation. Existing database values are retained. |
| W06.5 | Go delivery states, browser values, compatibility overlay and categorical response graph generate from canonical schemas; complete value sets are checked against 22 named SQL constraints and source/renderer/auth constants. Real schema-71 constraints accept all 152 known values and reject unknown values. |
| W06.6 | Paired generated browser types distinguish strict request arguments from exact read schemas that include a neutral `unsupported` value. Affected delivery adapters use generated types and typed key mapping rather than double assertions narrowing category values. |
| W06.7 | Browser operation controls derive from actual Go `actionTransition`. Unknown states have no actions; actual generated-client-to-rollout-page regression asserts neutral display, absent operational buttons and disabled approval. Malformed required category shapes reject before typed adapters. |
| W06.8 | All paired OpenAPI outputs are refreshed. Full released-v1.2.1 external compile fixture covers 2634 types, 10671 public fields, 2002 functions, 4132 methods and 1230 constants, with negative pointer/signature/type fixtures. Generator-only overlay preserves historical inhibition pointer layout and inline SDK enum identities; pointer construction and marshal roundtrips pass. |

## Validation and remaining integration

Bounded validation passes: category negative/parity tests (10), SDK negative tests (4), generated-schema binding tests (2), model/state/rollout/migrations tests, touched Go vet, toolchain drift and its 36 tests, full legacy SDK compilation and pointer/marshal tests, frontend type check and code health. Final full frontend and API gates are recorded separately after completion; earlier combined W01/W05 qualification is not evidence for this later source.

`database-categories.json` binds the dedicated owned CNPG17.11 database proof to canonical source hashes. It proves baseline schema 71 CHECK parity and a real production planning/HAFencing adapter. It does not claim combined W09 schema-72 migration or PostgreSQL18 qualification. No migration 073 is needed: the retained terminal values already exist in the exact production CHECK sets and no new database enum is introduced. Canonical maximum schema must remain 72 after W09’s genuine migration 072 is integrated; no empty migration should advance it.

The Go-only compatibility overlay preserves existing public SDK source layout while canonical inhibition request schemas describe required writer inputs and nullable compatibility defaults. A prior pointer-to-slice break was caught by the real baseline fixture and fixed through the generator source. Canonical schemas and strict browser writes retain these requirements.

Kubernetes dynamic kinds/phases, provider/distribution names and bridge extension maps retain upstream vocabularies. Backup/restore wire statuses remain open to preserve historical records; known `running` and historical `in_progress` are recognized and missing/future statuses render neutral unknown. Ancillary database CHECKs and pre-existing inline enums are inventoried, not falsely described as rewritten into the focused 41 schemas. Charlie approval `rejected`/`dispatched` differs from bridge `denied`; the schemas do not merge those meanings.
