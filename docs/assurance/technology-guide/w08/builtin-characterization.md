# Builtin provisioner SQL characterization

The original provisioner uses a checked-out physical connection for its session
advisory lock. It acquires that lock before beginning Serializable, avoiding a
snapshot fixed before a queued reconciler obtains ownership. Transaction queries
run on that connection's transaction. Deferred cleanup uses a fresh bounded
context and either releases the matching session lock or hijacks/closes the
connection; it must not return a connection holding that lock to the pool.

The existing complete package passes with the owned PostgreSQL16 environment.
Both database parents execute: multi-source deterministic assets/retry stability,
source conflict rollback, authenticated mirror ownership/history and concurrent
retries; automatic scanning enrollment/opt-out/inventory/registration states
remain characterized. These fixtures use isolated domain DDL rather than full
migration constraints, and are not described as a selected-major qualification.

The new cancellation case holds a trigger-stage transaction lock, observes the
provisioner actually blocked after obtaining its cluster session lock, cancels
the request, then acquires that same session key from another physical connection
within the existing five-second cleanup budget. It checks that no dependent rows
committed. The first instantaneous try-lock expectation failed because pgx5.11
marks canceled connections closed before its asynchronous cancellation/network
cleanup completes; that result is retained as a fixture expectation correction,
not a proven persistent product lock leak. The bounded replacement acquisition
passes on original production source without a source fix or retries.

Retained `builtin-before.log` and `builtin-session-characterization.log` record
actual database behavior before canonical SQL movement. The earlier
`builtin-session-before.log` retains the overly strong instantaneous expectation.
The new parent is TestPostgresBuiltinCanceledTransactionReleasesSessionLock,
using BUILTIN_PROVISIONER_TEST_DATABASE_URL. It adds cancellation/session cleanup
coverage to the existing real concurrent/idempotency/rollback cases.
