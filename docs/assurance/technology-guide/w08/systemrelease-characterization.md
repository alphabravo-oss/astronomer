# Release bootstrap before SQL movement

The baseline is W09 `f077054f`, containing reviewed main `4798bcdd`. This is
trusted startup bootstrap of a signed immutable release, not an operator HTTP
mutation. It retains the existing absence of task/audit outbox writes; W08 must
not invent a second mutation authority during a mechanical query move.

Five fixed statements execute through one `pgx.Serializable` transaction:

| Statement                        | Ordering and retained behavior                                                                                                                                                                |
| -------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Transaction-scoped advisory lock | Acquired immediately after BeginTx on the transaction's physical connection; automatically released at commit/rollback. No session lock or pool query substitution.                           |
| Version lookup                   | Parameterized version identity; no row triggers insert, other errors propagate.                                                                                                               |
| Insert immutable release         | Same fourteen bound fields, verification JSON and full spec digest; database supplies identity/default draft state.                                                                           |
| Released count                   | Read after insert/lookup in the same serializable snapshot.                                                                                                                                   |
| First-release promotion          | Runs only for draft with no released release; parameterized textual ID comparison, existing timestamp defaults retained. Later versions stay draft. Commit succeeds before reporting created. |

The actual PostgreSQL 16 baseline executed five named tests before runtime SQL
movement (`systemrelease-before.log`). The fixture clones the migrated release
table's real columns, checks, unique indexes and identity into an owned temporary
schema. It does not duplicate hand-written product DDL or claim to exercise
unrelated foreign keys/JSON registry triggers; the bootstrap never supplies
created_by and always marshals its typed verification object.

Tests prove repeated bootstrap is idempotent, the first release alone promotes,
later releases remain draft, digest mismatch/revoked rows reject, a failure after
insert rolls back that insert, and the advisory lock stays held while a trigger
blocks promotion and becomes available after commit. A forced two-waiter first
insert characterizes the existing serializable snapshot behavior: one creator
commits, one returns exact SQLSTATE40001, and an explicit later retry reads the
same release. This baseline propagates a retriable serialization error; a sqlc
move must preserve it rather than silently change isolation or add hidden retry.
