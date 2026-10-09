# Release bootstrap canonical query receipt

All five fixed bootstrap statements now live in
`internal/db/queries/delivery_system_bootstrap.sql`. `sqlc` 1.31.1 generated the
methods and global interface from that source; no generated method was edited.
The narrow domain interface is injected with `sqlc.New(tx)` only. The original
BeginTx isolation, deferred rollback, commit/error ownership, statement ordering
and parameter values remain intact. The promotion argument explicitly casts to
text to preserve the old textual UUID comparison and avoid sqlc inferring a
different public parameter representation. Count returns int64 directly without
narrowing; no saturation or default behavior is introduced.

The five actual PostgreSQL behavior cases from the before-move receipt still
pass after movement, together with all six existing release-builder unit cases.
The same complete package passes with `-race -p=1` under GOMAXPROCS2. The fixture
environment was set and every database case executed; no skipped case is
represented as database qualification. This scoped receipt is PostgreSQL16,
while selected17/18 acceptance remains a separate frozen-source root gate.

Commands and retained outputs:

- `make sqlc-generate`, `make sqlc-check`: pass with selected generator1.31.1.
- `go test -p=2 ./internal/delivery/systemrelease -count=1 -v`: pass with the
  owned database environment (`systemrelease-after.log`).
- `go test -race -p=1 ./internal/delivery/systemrelease -count=1 -v`: pass with
  the same owned database environment (`systemrelease-race.log`).
- Scoped Go vet and golangci-lint2.14.0: pass, zero lint issues.
- Dependency boundary and complexity budget gates: pass, no waiver or ceiling
  increase.

This is the first bounded W08 service slice. It does not claim the other three
services or runtime SQL/delete governance complete. Their inventory and
transaction characterization continue before each subsequent query movement.
