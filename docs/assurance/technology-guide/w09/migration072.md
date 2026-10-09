# Migration 072 expand receipt

Migration 072 installs seven `NOT VALID` nested wire-shape constraints for
inhibitions, task payloads, label maps, placement and indexed/system rollout
strategies. It contains no customer-row updates, repairs, deletes or disables.
The down migration removes only its own constraints/functions. Existing rows
remain unvalidated; updates to those rows must satisfy the new constraint.
Operators must complete the read-only inventory and reviewed repair policy
before expand and any separately reviewed future contract validation.

The SQL helpers preserve legacy empty inhibition arrays, optional matcher regex
omission, empty string values, additive stored properties, nullable optional
placement collections and the zero-byte task adapter. Task JSON uses PostgreSQL
`json` to preserve opaque numbers outside PostgreSQL numeric's range. Go remains
authoritative for regex, enums, durations, keys, UUIDs, duplicate selectors and
frozen-plan identity/digests. Cluster annotations remain additive objects because
released product paths support numeric metadata; they are not constrained to a
string map.

Commands and actual results on the scoped source:

- `make check-migrations`: 72 migration pairs and policy fixtures pass.
- `make sqlc-generate` and `make sqlc-check`: pass with no sqlc source drift.
- `python3 scripts/compatibility-contract.py generate` and `check`: pass. The
  canonical target schema and generated Helm/docs advance to 72; the reversible
  floor remains 66 and old released manifests are unchanged.
- `GOMAXPROCS=4 make test-postgres-integration`: 26/26 named cases execute and pass
  on a fresh owned PostgreSQL 16 database, with no skipped cases. Five new cases
  cover valid/invalid inhibition writes, task Go/SQL adapters, historical corrupt
  rows retained through expand, nested placement/strategy shapes, and actual
  audit outbox deduplication. Existing catalog operation fixtures now store a
  valid canonical strategy instead of `{}`; assertions retain their original
  operation and concurrency behavior.
- A separately owned schema-71 database was inventoried before migration and
  after schema 72 and rollback/reapply. All six rows were inspected each time,
  all four invalid rows remained present, and exact inspected documents were
  unchanged. These receipts are retained with the separate strategy inventory
  regression.

The logs in this directory retain actual command output. PostgreSQL 17/18 gates
must run against this frozen migration before selected-major acceptance. Initial
`make verify` reached and failed the expected generated OpenAPI freshness check
while W06 paired generation was pending (`api-contract-initial.log`); that run is
not a successful complete verification receipt.
