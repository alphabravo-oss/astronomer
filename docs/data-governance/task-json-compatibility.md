# Durable task JSON v1

The task outbox stores JSON in a `bytea` column, so the JSONB registry alone
cannot cover it. Its stable envelope is an object bounded to 16 MiB. Reserved
`_correlation_id`, `_traceparent`, and `_tracestate` members, when present, must
be non-null strings. Unknown task-specific properties are retained: their
semantics belong to the existing typed task handlers, rather than a second
central task-type registry.

The explicit v1 adapter maps historically payload-less tasks (zero bytes) to
`{}` before a new write or dispatch. Whitespace, JSON `null`, arrays, malformed
JSON, and trailing documents are rejected. A reader does not rewrite the stored
row. Task enrichment is checked both before and after adding observability
metadata, so it cannot turn a corrupt null payload into an apparently valid
object or grow a document beyond the durable bound.

`EnqueueTaskOutbox` rejects an invalid envelope before calling the database.
The dispatcher records the safe schema category in the existing retry/dead
lifecycle, skips enqueueing that row, and continues valid rows in the batch.
It does not delete, repair, or disable a task. Operator repair requires the task
owner to reconstruct the intended payload; an object shape alone does not prove
that a task's business fields are valid.

Rollout planning already owns a typed frozen-plan validator and strict durable
reader. The PostgreSQL `InsertRollout` boundary now invokes that validator before
any SQL call, including nested strategy, membership, indexed identity and
immutable digest checks. This check supplements the planner's validation and
does not grant tenant authorization.

Audit JSON v1 uses a bounded object envelope containing sanitized additive
domain values. `before`/`after` snapshots and historically domain-owned `tags`
values remain opaque. The reader preserves arbitrary JSON numbers instead of
converting them to `float64`. A marshal failure now rejects the audit intent
before a write, rather than replacing its detail with `{}`. The dispatcher
validates detail before atomic delivery, retries corrupt rows with a bounded
category, and continues valid rows. The publisher also refuses corrupt detail
instead of emitting an apparently valid empty event.
