# W09 JSON governance receipt

Source classification is in docs/data-governance/json-contracts.json; scope and
compatibility qualifications are in json-contract-inventory.md. The inventory
covers canonical sqlc JSON-capable fields, narrow binary/scalar exclusions,
import-bound JSON reader/writer sites including canonical extension templates,
and bound HTTP decoders. It records concrete legacy adapters/defaults instead
of claiming every opaque field acquired a strict nested product schema.

Commands on this scoped source:

- `go test -p=2 ./tools/json-governance`: six AST fixtures pass, including import
  aliases, request/response binding, generic helpers, byte/encrypted storage,
  and shadowed alias/nested request negatives.
- `python3 scripts/check-json-governance-test.py`: ten negative fixtures pass,
  including storage representation drift and a new serializer in an already
  owned module requiring a schema/reader owner.
- `make data-governance-check`: passes with 189 request owners, 205 classified
  storage candidates, 1,125 discovered JSON call sites, and existing deletion
  governance retained. Helpers and inherited partition paths have source-bound
  annotations; canonical helper functions are checked for continued existence.
- `node scripts/check-docs.mjs`: passes.

The initial gate uses unique function/operation/argument bindings (1,102), not raw site
count as correctness proof. Read-only inventory verifies transaction mode and
schema identity before reading every row of its explicitly stable-schema scope.
Physical constraints and reader/worker tests provide independent behavior proof.
The manifest records selector-name/query candidates' limitations: it is not a
whole-program callgraph or arbitrary SQL type-resolution proof.

The final refresh imports reviewed main `4798bcdd` (including W03) and removes
the retired URL-bearing catalog diagnostic serializer. Five new modules and
nine precise calls have explicit owner/schema/reader behavior; one old site is
retired. It now discovers 1,134 calls and binds 1,110 unique sites in 388 modules.
The critical row inventory also checks separate rollout strategy alongside its
frozen plan, with a complete canonical-plan regression and actual database
before/after receipts. Seven Go fixtures and ten Python negatives pass, and the
combined data governance gate passes. `final-data-governance.log` and
`final-governance-fixtures.log` retain the outputs. These additions preserve the
same syntax/dataflow limitations and do not claim unrelated ignored errors were
silently corrected.
