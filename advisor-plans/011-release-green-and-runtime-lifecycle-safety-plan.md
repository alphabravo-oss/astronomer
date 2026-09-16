# Release-green and runtime lifecycle safety plan

**Status:** IN PROGRESS — priority 1 of 5

**Planned at:** `32100314e081e635e89f43fcf14673a25449ef63` on 2026-09-16, against the current dirty working tree

**Branch:** `advisor/011-release-green-lifecycle`

**Scope boundary:** adopted clusters only; Flux is the only delivery engine; do not add Fleet, cluster provisioning, Cluster API, or Crossplane.

## Implementation progress (2026-09-16)

- Repaired the worker capability fixture and rollout-route test regression.
- Routed runtime listener failures through graceful shutdown, normalized normal
  HTTP closure, and added ordered audit/telemetry drains before resource-pool
  closure.
- Made connection-pool saturation state instance-local, bound idempotency keys
  to canonical request digests, excluded tombstoned clusters from active reads,
  and replaced persisted cluster JSON panics with bounded controlled errors.
- Remaining lifecycle/readiness work and the complete release gate are still
  required before this plan can be marked complete.

## Objective

Restore a trustworthy release baseline and close the ten highest-leverage correctness failures found in the current tree. Completion means the ordinary Go and frontend suites are green, shutdown drains durable work in dependency order, dead critical workers make the process unready, and requests cannot act on tombstoned clusters or reuse an idempotency key for different input.

## Why this plan is first

The repository currently builds and type-checks, but `go test ./... -count=1` has four failures and `npm test` has five. More importantly, a normal HTTP shutdown can be treated as fatal, the database is closed before the audit writer drains, and background loops can outlive their dependencies. Do not start feature/parity work while those invariants are false.

## Findings covered (global rank 1–10)

| Rank | ID | Finding | Primary evidence |
|---:|---|---|---|
| 1 | GREEN-01 | Four tunnel-worker tests omit the newly required Kubernetes capability checker | `internal/worker/worker_test.go:488-499,731,740,812,855`; `internal/worker/tasks/runtime_validation.go:44-52` |
| 2 | GREEN-02 | Five rollout-route tests mount a TanStack hook without router context | `frontend/src/routes/dashboard/delivery/rollouts/$rolloutId/index.test.tsx`; route `index.tsx` |
| 3 | LIFE-01 | Normal `http.ErrServerClosed` can reach `os.Exit(1)` and bypass deferred cleanup | `internal/server/server.go:527-541`; `cmd/server/main.go:202-206` |
| 4 | LIFE-02 | Server shutdown closes PostgreSQL before the audit writer drains | `internal/server/server.go:576-585`; `cmd/server/main.go:226-238`; `internal/audit/writer.go:287-310` |
| 5 | LIFE-03 | Background reconcilers are canceled but not joined before dependencies close | `internal/server/app_runtime_foundation.go:37-65`; `internal/server/server.go:576-588` |
| 6 | LIFE-04 | Tunnel-worker death is only logged; readiness can remain healthy | `internal/server/server.go:533-538`; `internal/server/readiness.go:174-180` |
| 7 | DB-01 | Readiness mutates a package-global counter without synchronization | `internal/db/db.go:239-249`; `internal/server/readiness.go:124` |
| 8 | API-01 | Idempotency is bound only to route/key, not request query/body identity | `internal/server/middleware/idempotency.go:319-336`; `internal/handler/operation_idempotency.go:34-59,88-108` |
| 9 | API-02 | Tombstoned clusters remain addressable and actionable by ID | `internal/db/queries/clusters.sql:1-2,53-58`; `internal/handler/clusters_inventory.go:412-440` |
| 10 | DATA-01 | Corrupt persisted JSON can panic cluster response construction | `internal/handler/clusters_inventory.go:611-619`; `internal/handler/clusters_response.go:101-106` |

## Out of scope

- New provisioning workflows or lifecycle ownership of Kubernetes clusters.
- Fleet compatibility, Fleet migrations, or legacy Argo paths.
- Large handler/package decomposition; that is Plan 014.
- Authentication, backup, supply-chain, and frontend scale work; those are Plans 012–015.

## Preflight and drift check

1. Create the branch without cleaning or resetting the working tree.
2. Record `git status --short`, `git rev-parse HEAD`, and `git diff --stat`. The tree was heavily modified when this plan was written; preserve every unrelated change.
3. Re-open every evidence location above. If a cited defect is already fixed, add a focused regression test and mark the item satisfied; do not reimplement it.
4. Run the baseline commands below. Expected at planning time: build/vet/type-check/lint pass; the four named Go tests and five rollout tests fail.

```bash
go vet ./internal/... ./cmd/...
go build ./...
go test ./... -count=1
cd frontend && npm run type-check && npm run lint && npm test
```

Stop if the failing set has materially changed in a way that indicates concurrent edits in the same files. Rebase the plan against the current code rather than overwriting those edits.

## Implementation steps

### 1. Restore the two red test groups

- Extend the test-only `testTunnelRuntime` in `internal/worker/worker_test.go` with a deterministic fake implementing the exact capability-checker interface now required by `ValidateTunnelRuntime`. Keep production validation strict; do not make the dependency optional to placate tests.
- Add a negative validation test proving a nil capability checker still fails closed.
- In the rollout route test, mount the route with a memory history/router or mock `useParams` consistently with the real TanStack Router API. Prefer a reusable `renderWithRouter` helper if another delivery test already has one.
- Preserve the real parameter parsing behavior; do not replace it with a hard-coded production fallback.

Acceptance:

```bash
go test ./internal/worker -run 'Test(RegisterTunnelHandlers|NewTunnelWorkerBindsExplicitRuntimeHandlers|NewTunnelWorkerCRDOwnershipFollowsFeature|TunnelWorkerConcurrencyConfigurable)$' -count=1
cd frontend && npx vitest run 'src/routes/dashboard/delivery/rollouts/$rolloutId/index.test.tsx'
```

Both commands exit zero, and the new negative Go test proves production construction still rejects a missing capability checker.

### 2. Make normal HTTP shutdown non-fatal

- At the server boundary, normalize `http.ErrServerClosed` to `nil`, or explicitly classify it in `cmd/server/main.go` before deciding to exit.
- Remove shutdown-path `os.Exit` behavior that can skip defers. Return an exit code from a testable `run` function and let `main` call `os.Exit` only after cleanup has completed.
- Add tests for normal shutdown, unexpected listener/serve failure, and context cancellation. The unexpected error must still produce a non-zero outcome.

Acceptance: a normal signal-driven shutdown returns success and executes cleanup; an injected unexpected `Serve` failure returns an error.

### 3. Establish one explicit shutdown dependency graph

Use this order and encode it in one owner rather than split between `Server.Shutdown` and `main`:

1. stop accepting HTTP/tunnel work;
2. cancel producers and reconcilers;
3. wait for every registered goroutine with a bounded deadline;
4. stop/drain task consumers that can emit audit records;
5. flush and close the audit writer;
6. close Redis and PostgreSQL;
7. shut down telemetry last.

- Give reconcilers and the tunnel worker a lifecycle group (`errgroup`, explicit `WaitGroup`, or an equivalent owned abstraction).
- Do not fire untracked goroutines from the shutdown path.
- If the deadline expires, report which component failed to drain without logging secrets.
- Add a dependency-order test with fakes recording close/drain calls. Include an audit write emitted during worker drain and prove it commits before the database closes.

### 4. Couple critical worker health to readiness

- Store terminal tunnel-worker error state in the server lifecycle owner.
- Make readiness fail with a stable, non-sensitive reason when the worker exits unexpectedly.
- An intentional shutdown cancellation must not be reported as a runtime failure.
- Add a metric and structured error log for unexpected worker exit, and test the ready → failed transition.

### 5. Remove readiness shared mutable global state

- Move `lastEmptyAcquireCount` into a per-pool or per-checker instance guarded by a mutex/atomic, or calculate the signal from immutable pool statistics without retained global state.
- Ensure two server instances in one process cannot influence one another.
- Add a concurrent test and run it under the race detector.

Acceptance:

```bash
go test -race ./internal/db ./internal/server -count=1
```

### 6. Bind idempotency to canonical request identity

- Compute a versioned digest over method, canonical route, canonicalized query parameters, and the exact bounded request body bytes. Preserve/replay the body for the handler after hashing.
- Store the digest with the idempotency record. A repeated key with the same digest returns the recorded result; the same key with a different digest returns a deterministic conflict response.
- Reuse the digest-aware operation-idempotency path rather than maintaining two incompatible models.
- Exclude authentication headers and secrets from stored material; store only the digest and required metadata.
- Add tests for identical replay, different body, reordered equivalent query parameters, distinct query values, concurrent first writers, oversized bodies, and tenant isolation.

### 7. Make tombstones non-actionable by default

- Split raw historical lookup from active-cluster lookup. Public GET/action handlers must use the active form (`decommissioned_at IS NULL`).
- Keep a narrowly named internal lookup only where audit/history/retention truly needs a tombstone.
- Inventory every `GetClusterByID` caller, including shell, network policy, snapshot, delivery, proxy, and tunnel paths. Do not rely on individual handlers to remember a second check.
- Return the repository's ordinary not-found response to callers without leaking tombstone existence.
- Add handler and store tests showing every mutation/action class rejects a tombstone while audit/history can still render its retained name.

### 8. Replace JSON response panics with validated boundaries

- Replace `panic`/must-unmarshal behavior in cluster response construction with an error-returning decoder.
- Validate the persisted JSON shape when writing and again defensively when reading. A corrupt row must become a controlled 500 plus an operator-visible metric/log containing record identity but not JSON contents.
- Add database constraints/migration only after auditing existing rows; provide a preflight query and remediation path for invalid historical values.
- Test malformed JSON, valid empty object, unknown forward-compatible fields, and oversized input.

## Verification

Run focused tests after each step, then the complete gate:

```bash
go test -race ./internal/db ./internal/server ./internal/worker ./internal/handler -count=1
go vet ./internal/... ./cmd/...
go build ./...
go test ./... -count=1
cd frontend
npm run type-check
npm run lint
npm test
```

Expected result: every command exits zero. No test may be skipped merely to obtain green status. Run the repository's broader `make verify`/`make verify-all` gate if its prerequisites are available and attach the result.

## Commit sequence

Use small conventional commits and do not mix unrelated dirty-tree changes:

1. `test(worker): restore capability runtime fixtures`
2. `test(ui): mount rollout route with router context`
3. `fix(server): make shutdown ordered and graceful`
4. `fix(server): fail readiness on critical worker death`
5. `fix(db): isolate readiness state`
6. `fix(api): bind idempotency to request digest`
7. `fix(clusters): reject tombstoned targets`
8. `fix(clusters): handle invalid persisted metadata`

## Done criteria

- All five validation commands are green from a clean checkout plus the intended commits.
- Normal shutdown exits successfully and drains audit before closing PostgreSQL.
- Every owned background loop is canceled and joined under a bounded timeout.
- Unexpected tunnel-worker death makes readiness fail.
- Race tests find no readiness-state race.
- Idempotency conflicts on same key/different input.
- Public reads and actions cannot access a decommissioned cluster.
- Corrupt JSONB cannot panic an HTTP request.
- No Fleet, provisioning, or legacy delivery code was added.

## Stop conditions

- Stop and request an architecture decision if completing shutdown order requires dropping audit events or extending the process past the configured termination grace period.
- Stop before a schema constraint if the preflight finds historical invalid data without a deterministic repair rule.
- Stop if a concurrent change owns any cited lifecycle/authentication file; reconcile rather than overwrite.
