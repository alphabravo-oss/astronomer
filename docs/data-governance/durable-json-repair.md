# Durable JSON inventory and reviewed repair

Migration 072 is an expand step. Seven `NOT VALID` constraints check the stable
wire shapes of new inhibition, task, label, placement and rollout records. They
leave historical rows unchanged and unvalidated. PostgreSQL also checks the
constraint when an old row is updated, even if the update targets another field.
A later contract migration must not validate a constraint while incompatible
records remain. The down migration removes only these seven constraints and
helper functions; it does not restore or transform customer documents.

Before applying the expand step, a database operator runs the read-only tool:

```sh
go run ./tools/json-governance --rows > invalid-rows.json
```

Supply the connection through `ASTRONOMER_JSON_INVENTORY_DATABASE_URL` using the
existing secret-management mechanism. Keep the receipt in restricted operator
storage. Do not put credentials in a command argument, log or ticket. The tool
verifies `transaction_read_only=on`, records the database schema and PostgreSQL
major, uses a repeatable-read snapshot, and streams every row in eight explicit
stable-schema tables. It records counts plus at most 1,000 row IDs and safe
categories; `diagnostics_truncated` does not mean remaining rows were skipped.
The command has a ten-minute bound and returns an error if it cannot finish.

The validators are the application readers for inhibition, task envelopes,
audit detail, label maps, placement, frozen plans and system rollout strategy.
The scope is deliberately visible in `rowSpecs`. It does not decrypt credential
slots, infer schemas for external provider objects, interpret arbitrary
Kubernetes resources, or claim to inventory every opaque field for nested
correctness. The source inventory classifies those fields and identifies their
owners separately. An absent stable schema is not permission to coerce data.

An invalid inhibition is skipped during alert evaluation while valid rules
continue. Its bounded rule-ID/category diagnostic and deduplicated integrity
audit intent use the existing audit outbox. No rule is automatically disabled,
deleted or rewritten. Invalid task/audit outbox envelopes follow the existing
retry/dead lifecycle and do not reach enqueue/delivery. Handler integrity errors
retain the existing typed internal-error envelope.

## Repair policy

A repair requires the domain owner and tenant-authorized operator to establish
what the record was intended to mean. Empty arrays, empty matcher values and
legacy zero-byte tasks already have explicit v1 adapters. JSON null, a wrong
nested type, or a missing required matcher value must not be guessed into an
empty rule or object. Keep the original document in restricted incident storage
when policy permits; diagnostics and repair audit detail contain identifiers,
categories and revision references, never matcher values or credentials.

Use the ordinary authorized product update path when it can express the reviewed
replacement. A one-off database repair requires an approved, reviewed transaction
with a matching domain validator and a mandatory audit intent in that same
transaction. This document does not provide an executable bulk repair command.
The transaction must:

1. Lock the exact tenant-owned row (`SELECT ... FOR UPDATE`) and recheck the
   authorization scope. Read its current domain fields, status and revision.
2. Refuse any claimed or leased outbox/rollout row. For task/audit outboxes require
   `locked_until IS NULL` and an explicitly repairable pending/retry state; for
   rollouts require an empty lease owner, no lease expiry, an approved inactive
   state and the exact fencing generation. An expired lease is not permission to
   modify a record a dispatcher may already have validated. Do not clear a lease,
   change delivery status or increment a fence to force a repair through.
3. Validate the complete proposed record using the current canonical reader and
   domain validator. Preserve supported v1 additional properties and opaque
   producer-owned data. Stop if the replacement requires guessing customer intent.
4. Compare-and-swap against the exact inspected revision: row ID, tenant scope,
   `updated_at`/resource version or fencing generation, status/attempt count, and
   every JSON/bytea field being replaced. JSONB comparisons use the stored value;
   task payloads compare exact bytes. Require the claim/lease predicates again in
   the update, even after taking the row lock. A zero-row update is a conflict and
   must roll back; do not retry using a new revision automatically.
5. Record the reviewed repair through the existing mandatory transactional audit
   outbox. Failure to record the audit intent rolls back the repair. Commit only
   after both operations succeed, and re-run the read-only inventory.

This preserves the dispatcher boundary: a replacement cannot occur between a
claimed row's schema validation and its delivery. Dedupe keys must not be reused
to overwrite another pending audit intent's detail, and delivered audit history
is not rewritten. Repairing a historical audit envelope requires its audit owner
and retention policy; an object envelope alone is not evidence of the lost event.

## Contract and rolling compatibility

The expand step retains the source baseline's empty inhibition arrays (which
match no targets), omitted is_regex=false, empty string matcher values, nullable
optional placement collections and additive stored-record properties. The task
zero-byte adapter is retained without rewriting rows. New typed writers already
marshal canonical model values. SQL functions supplement primitive shape; Go
continues to own regex syntax, UUID/key validation, strategy enums, durations,
duplicate selectors and frozen-plan digest/identity checks.

Source baseline readers and new readers must remain compatible with valid v1
records during rolling deployment. Target schema advances to 72 through the
canonical compatibility generator; the reversible floor remains 66. No old
release manifest or historical migration is rewritten. Operators inventory their
own data before expand and repair only reviewed rows during migrate. A separately
reviewed contract migration can `VALIDATE CONSTRAINT` after both compatible
writers/readers and a clean completed inventory are established. There is no
automatic contract step in 072.
