---
status: "accepted"
date: 2026-10-20
deciders: ["AlphaBravoCompany"]
title: "Redis (Valkey-compatible) + asynq as the durable task queue"
tags: [data, runtime]
---

# Redis (Valkey-compatible) + asynq as the durable task queue

## Context

The guide's data chapter lists PostgreSQL as the only stateful dependency and allows LISTEN/NOTIFY for eventing. Astronomer runs a separate `worker` binary that executes long-running, retried, scheduled tasks (cluster adoption, catalog sync, scans, delivery reconciliation, notification dispatch). Those tasks are enqueued through `hibiken/asynq` on Redis, with transactional outboxes in PostgreSQL (`task_outbox`, `audit_outbox`) so an enqueue is never lost between the database commit and the queue.

## Decision Drivers

- At-least-once delivery with retries, scheduling, uniqueness and dead-letter handling.
- Horizontal worker scaling and leader election independent of the API process.
- Outage recovery: the outbox replays into Redis after a queue outage.
- Production posture already supports Sentinel and external Redis; the chart ships it.

## Considered Options

- Keep Redis/asynq with PostgreSQL outboxes.
- Replace with a PostgreSQL-only queue (LISTEN/NOTIFY + SKIP LOCKED).

## Decision Outcome

Chosen option: "Keep Redis/asynq", because the product already relies on asynq's scheduling and retry semantics and the outbox pattern gives durability; rewriting the queue is a large change with no conformance value. Redis is therefore a declared stateful dependency of Astronomer.

### Positive Consequences

- No change to worker semantics; outage-recovery behaviour is preserved.

### Negative Consequences

- One more stateful service to operate; the dev stack and tests need a real Redis (testcontainers in Phase 1).

## Consequences

Tests MUST use a real Redis (testcontainers module), not `miniredis`, as the Phase 1 testing substrate lands. The dev compose stack runs `redis:8.x-alpine`.

## Pros and Cons of the Options

### Keep Redis/asynq

- Good, because scheduling, retries and uniqueness are proven in production.
- Bad, because it is a second stateful dependency.

### PostgreSQL-only queue

- Good, because it removes a dependency.
- Bad, because it re-implements scheduling/retry semantics the worker already depends on.

## Links

- `internal/worker`, `internal/redisconn`, migrations `task_outbox`/`audit_outbox`.
