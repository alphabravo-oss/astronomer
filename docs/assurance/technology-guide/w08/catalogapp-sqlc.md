# Catalog application canonical SQL slice

Seventeen fixed statements move from catalogapp service/status readers into
`internal/db/queries/delivery_catalog_application.sql`, generated with sqlc1.31.1.
They retain statement predicates, values, ordering and isolation. Explicit boolean
casts give sqlc the actual PostgreSQL parameter type for revision/generation
CASE expressions; qualified nested IDs avoid ambiguous sqlc analysis. Generated
row aliases close former unnamed result fields; no handwritten generated output.

Asset creation, rollback staging and uninstall each retain their original
Serializable BeginTx, deferred rollback and single commit. Every generated
mutation query is bound to sqlc.New(tx). Uninstall's SELECT FOR UPDATE, existing
project/resource-version deletion CAS, deployment updates and compatibility
status update remain one transaction. Rollback commits staging before preview
and planner.Create, carrying the same actor, tenant and mandatory audit intent
into the planner's separate durable transaction. Ordinary status/project reads
remain pool reads; exact rollout status never borrows a newer target rollout.
The status reader interface exposes only the two named typed query methods.
Positive generation is checked before signed-to-unsigned conversion and before
commit; database generation constraints and monotonic increments remain intact.

Actual database characterization includes curated immutable version ledger
identity and retries, the uncurated INSERT SELECT zero-row behavior, late YAML
validation rollback, install retry versus explicit upgrade revision ownership,
exact status/foreign target handling, immutable revision ordering, and late
uninstall/rollback installation-trigger failures. The latter prove earlier
fenced target/deployment writes roll back together. A separate pool session
observes committed rollback staging before preview. Planner callbacks assert
actor/tenant/generation/audit handoff; they do not claim to execute the planner's
outbox transaction. That transaction remains owned by the existing rollout
adapter and its real PostgreSQL transaction/fencing integration case.

The additional atomic service tests were run on original source91b05392 in an
isolated worktree, then on the canonical implementation. Both pass. The fixtures
clone actual migrated column/check/index/identity definitions into random owned
schemas, excluding foreign-key and registry-trigger recreation. The existing
operation-status fixture runs against the actual migrated public schema inside
a rollback-only transaction. No live product cluster is used.

Retained commands/results:

- `go test -p2 -count=1 -v ./internal/delivery/catalogapp`, with both owned database
  environments set: all11 parent cases PASS, zero skips (`catalogapp-after.log`).
- `go test -race -p1 -count=1 -v ./internal/delivery/catalogapp`, GOMAXPROCS2 and
  the same database environments: complete package PASS (`catalogapp-race.log`).
- Original-source named atomic tests: all3 parents PASS
  (`catalogapp-atomic-before.log`).
- `make sqlc-generate` and `make sqlc-check`: PASS, canonical generator1.31.1.
- Go vet, golangci-lint2.14 full package: PASS, zero lint findings.
- Dependency boundary and complexity gates: PASS without increased ceilings.

These local database receipts use owned PostgreSQL16. Selected17/18 qualification
and the expanded strict mandatory-case harness are coordinated with root/W12;
no selected-major result is inferred from these local tests. This is the second
bounded service slice; remaining W08 services and expanded SQL/delete governance
are still in progress.
