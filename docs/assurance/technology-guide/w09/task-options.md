# Task outbox parameter bounds

The new actual writer regressions reproduced 13 writes reaching the SQL boundary:
six invalid generic option cases and seven invalid transactional template task
cases. Oversized int retries/attempts and rounded duration seconds narrowed to
int32 without a guard. The atomic template path also accepted null/trailing task
payloads because it copied parameters independently of the generic task writer.

PrepareTaskOutbox now constructs canonical validated parameters without writing
SQL. Both the generic writer and the transactional template writer use it; the
latter still calls its original single application/task SQL statement inside the
same domain/audit transaction. It retains existing envelope enrichment, v1
zero-byte task adaptation and opaque task business fields.

The builder preserves existing generic negative-retry-to-zero and nonpositive
attempt-count-to-20 defaults, queue/time defaults and Round(time.Second) behavior.
It rejects values outside the nonnegative int32 storage range before SQL. The
atomic path retains its previous rejection of negative retries (formerly through
the SQL constraint) rather than adopting the generic default. Inclusive MaxInt32
values and subsecond rounding/default fixtures pass; no large value is clamped.

Focused actual generic and transactional helper tests, existing template mutation
commit/rollback cases, request/inhibition regressions, narrow vet and pinned
v2.14.0 golangci-lint on worker/tasks and handler pass. The scoped linter also
identified two W09 cleanup issues: infallible hash-prefix formatting now explicitly
discards its return, and the unused duplicate ensureJSONEOF helper was removed.
The canonical bounded one-document reader remains the only EOF implementation
used by converted HTTP decoders. No ignore or linter rule change was added.
