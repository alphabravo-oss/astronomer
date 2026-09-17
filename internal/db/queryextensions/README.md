# Isolated sqlc extensions

This directory is the canonical source for the small set of query adapters
that sqlc cannot currently express, including dynamic authorized filters and
multi-statement compare-and-swap/outbox operations. Templates are not compiled
as Go. `scripts/generate-sqlc-extensions.py` copies them into
`internal/db/sqlc` with a generated-file header.

New ordinary queries belong in `internal/db/queries/*.sql`, never here. An
extension is acceptable only when its transaction or dynamic-query shape is
not representable by the pinned sqlc version. `sqlc-check` regenerates these
files and rejects every ungenerated production `.go` file in the generated
directory, so handwritten source can no longer silently accumulate there.
