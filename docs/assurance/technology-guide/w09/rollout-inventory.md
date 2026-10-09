# Separate rollout strategy inventory receipt

The original row inventory checked only `delivery_rollouts.frozen_plan`, although
migration 072 also constrains its separate `strategy` column. The regression uses
a complete v1 frozen plan emitted by the canonical planner and accepted by
`ReadFrozenRolloutV1`, beside an invalid separate `{}` strategy. The original
inventory missed that defect; the fixed inventory uses `ReadRolloutStrategyV1`
before validating the frozen plan.

`go test -p=2 ./tools/json-governance` passes all seven tests, including six AST
fixtures and the separate strategy regression. The before-fix unit failure and
real database receipt are retained as `rollout-inventory-baseline.log` and
`rollout-inventory-before-fix.json`. The fixture contains synthetic identifiers
and was generated through the existing canonical planner test fixture, without
hand-building a document that bypasses frozen-plan validation.

The owned PostgreSQL 16 database contained six inspected rows: a valid legacy
empty inhibition, three invalid inhibition/task rows, a valid placement, and a
rollout with a valid frozen plan but corrupt separate strategy. The original
inventory reported three invalid rows. The fixed read-only inventory reports
four, including the rollout, at schema 71, schema 72, and after rollback/reapply.
The three `invalid-rows-*.json` receipts retain schema/major and read-only
transaction identities. Exact stored document comparisons across the cycle
confirmed that the migration did not rewrite these rows. Selected PostgreSQL
17/18 qualification remains a separate frozen-source gate.
