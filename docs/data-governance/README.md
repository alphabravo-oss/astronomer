# Durable data governance

Astronomer treats durable JSON and destructive deletion as explicit contracts.
Migration 049 creates `durable_json_schemas`, registers every non-partition
JSONB column at schema version 1, and installs a write trigger on every owning
table. The trigger rejects the wrong top-level JSON type, missing required
keys, and documents larger than the registered byte ceiling. Nullable fields
remain nullable. Compatibility is recorded as `additive`, `exact`, or `opaque`;
raising a schema version requires an expand/migrate/contract change and a
reader compatibility test.

The database view `durable_json_schema_coverage` is the authoritative runtime
inventory. Startup fails closed when any JSONB column lacks either a registry
entry or its validation trigger. Inspect the current contracts with:

```sql
SELECT table_name, column_name, schema_version, json_type, max_bytes,
       compatibility_mode, owner
FROM durable_json_schemas
ORDER BY table_name, column_name;
```

[`deletion-policies.json`](./deletion-policies.json) classifies every table
targeted by canonical SQL as retention data, derived/reconciled state, an
explicit audited purge, or tombstone-before-retention. `clusters` remain the
tombstone case: audit identity is archived before the final retention purge.
User, project, and RBAC deletion is an explicit audited purge because the audit
contract stores immutable actor/resource identity rather than foreign keys to
the deleted row. Adding a new `DELETE FROM` target without policy ownership is
rejected by `scripts/check-data-governance.py`.

Neither inventory authorizes ad-hoc deletion. Domain handlers and services
still enforce authorization, tenant scope, transactional audit, and retention
cutoffs. The inventories define compatibility and ownership; they do not
weaken those controls.
