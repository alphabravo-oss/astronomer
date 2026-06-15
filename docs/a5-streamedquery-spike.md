# A5 Spike — `experimental_streamedQuery` for live-events SSE

**Date:** 2026-06-15
**Scope:** `frontend/src/lib/live-events.ts`
**Recommendation:** **Do not adopt `experimental_streamedQuery`. Keep the hand-rolled SSE bus as-is.** A small, optional devtools-visibility win is described at the end but is itself deferred (not clearly low-risk).

---

## 1. How `live-events.ts` works today

`live-events.ts` is **not** a query — it is a **multiplexed event bus** that owns a single shared `EventSource` and fans frames out to many subscribers, most of which **patch other queries' caches**.

Concretely:

- **Single shared transport (`conn` singleton).** One module-level `ConnectionState` holds the `EventSource`, an `EventTarget` re-emitter, a `refCount`, a `retryCount`, a pending `reconnectTimer`, the ticket it was opened with, and a `status` of `'idle' | 'connecting' | 'open' | 'closed'`.
- **Auth handshake.** `EventSource` cannot send custom headers, so `openSource` first calls `createStreamTicket('events')` (authenticated XHR) and then opens `…/events/stream/?ticket=<one-use>`.
- **Refcounted lifetime.** `useLiveEvents`, `useLiveQueryInvalidation`, and `useLiveClusterMetricsMerger` each bump/decrement `refCount`; the connection opens on first mount and closes when the last consumer unmounts. ~74-ish dashboard pages mount these hooks; only **one** socket is ever open.
- **Reconnect logic.** `scheduleReconnect` does exponential backoff (1s→30s cap) keyed on `retryCount`, only while `refCount > 0`. `onopen` resets `retryCount`. A `visibilitychange` listener re-opens after laptop sleep.
- **Dispatch / fan-out.** `dispatch` JSON-parses each frame, un-nests the `{id,type,time,data}` wire shape into a `LiveEvent`, and dispatches a `CustomEvent` keyed by event type **plus** a `'*'` wildcard, so subscribers see only what they asked for.

The three consumer hooks do fundamentally different things with those events:

1. **`useLiveEvents()`** — holds the connection open and exposes `subscribe(types, handler)` + `status()`. Used by the dashboard layout (to keep the socket alive) and by the registration wizard (`connect/page.tsx`, `registration-timeline.tsx`) which subscribe to `cluster.registration.*` step/phase events and drive UI directly. **No query involved.**
2. **`useLiveQueryInvalidation(eventTypes, queryKeys)`** — on matching events, calls `queryClient.invalidateQueries({ queryKey })` (prefix match) for **N arbitrary, caller-supplied query keys**. One event fans out to many unrelated caches.
3. **`useLiveClusterMetricsMerger()`** — on `cluster.metrics` / `cluster.status_changed`, **patches existing caches in place** via `setQueriesData(clusters.listAll, …)` and `setQueryData(clusters.detail(id), …)` (the `mergeCluster*` helpers), deliberately avoiding refetches every 10s. `status_changed` additionally fires one `invalidateQueries`.

**Key property:** an incoming frame is a *command that mutates 0..N other queries' caches*. The data does not live in any single query keyed to the stream.

## 2. What `experimental_streamedQuery` offers

`experimental_streamedQuery` (present here: `@tanstack/react-query@^5.32`, re-exported as `experimental_streamedQuery`; backing `streamedQuery` in `@tanstack/query-core`) is a **`queryFn` factory**. Signature:

```ts
streamedQuery<TQueryFnData, TData, TQueryKey>({
  streamFn: (ctx) => AsyncIterable<TQueryFnData> | Promise<AsyncIterable<TQueryFnData>>,
  refetchMode?: 'append' | 'reset' | 'replace',
  reducer?, initialValue?,   // optional accumulation
}): QueryFunction<TData, TQueryKey>
```

It consumes an **async iterable** and accumulates each yielded chunk into **one query's `data`** (default `TData = Array<TQueryFnData>`, or a custom `reducer`). It models **"one stream → one growing query result"**: incremental LLM tokens, a paginated firehose, a log tail. The stream is tied 1:1 to a `queryKey`; lifecycle (loading/error/refetch/cancel via `ctx.signal`) is managed by the query.

## 3. Why it does NOT fit here

| Need in `live-events.ts` | `streamedQuery` model |
| --- | --- |
| One stream **patches many** existing caches (`clusters list`, `clusters detail`, plus arbitrary caller keys) | One stream **owns one** query's data |
| Consumers want **side effects** (invalidate / `setQueryData` merges, optimistic status flips), not an accumulated array | Consumers read the **accumulated array** as that query's data |
| Wildcard `'*'` + per-type fan-out to N subscribers | No multiplex; one subscriber = one query |
| Registration wizard drives **UI directly** off events (no cache) | Would force routing UI events through a query result |
| Custom **ticket handshake + refcount + visibility-driven reconnect + capped backoff** already tuned | Reconnect/refetch semantics are query-shaped (`refetchMode`, `staleTime`), not a refcounted shared socket |
| `EventSource` (named SSE events, auto-reconnect) | `streamFn` must return an `AsyncIterable`; SSE would need an adapter wrapping `EventSource` into an async generator |

Adopting it would mean inventing a single synthetic "all events" query whose `data` is an ever-growing array of frames, then *re-deriving* every existing side effect from that array — strictly more code, a new unbounded-growth concern, and a behavioral rewrite of cache patching that today is precise and refetch-free. It is a **mismatch of shape**: a bus that issues cache-mutating commands vs. a query that accumulates stream chunks. `experimental_` status is a secondary concern; the structural mismatch is the disqualifier.

**Verdict: keep the hand-rolled bus. No `streamedQuery` adoption.**

## 4. Smaller win considered — expose SSE status as a tiny query

The one genuinely appealing idea is surfacing the connection `status` (`idle/connecting/open/closed` + `retryCount`) into the React Query cache (e.g. key `['live-events','connection']`) so it shows up in React Query Devtools and so status pills can subscribe reactively instead of polling `live.status()`.

**Assessment — worthwhile but NOT "clearly low-risk", so deferred (not implemented in this spike):**

- Today `status` is a **plain mutable field** read synchronously via `live.status()`; there are **no reactive subscribers**. Making it a query requires calling `queryClient.setQueryData(['live-events','connection'], …)` at **every** transition (`connecting` in `openSource`, `open` in `onopen`, `closed` in `onerror`/`catch`/teardown, plus `retryCount` in `scheduleReconnect`) — ~8 sites.
- The singleton `conn` has **no access to a `QueryClient`** (it's module-global, created outside React). Wiring one in means threading a client reference through `ensureConnection`, which touches the shared transport that all ~74 call sites depend on. That is exactly the "default render path must stay behaviorally identical" surface we were told to protect.
- It also adds a new always-present entry to every consumer's cache and new re-render triggers — small, but a behavior change with no failing test to catch a regression (there are currently **no tests over `live-events.ts`**).

Because the payoff is purely devtools/diagnostics polish while the change perturbs the shared transport with no test safety net, it does not meet the "clearly low-risk **and** net win" bar this spike requires. Recommend deferring it to a dedicated, test-backed change if status visibility is later prioritized.

## 5. Recommendation

1. **Do not** replace the SSE bus with `experimental_streamedQuery` — structural mismatch (multiplexed cache-patching bus vs. single stream-backed query), compounded by its experimental status.
2. **Keep `live-events.ts` as-is.** No code change made; tsc / lint / jest unaffected.
3. If devtools visibility into SSE health is later wanted, do the status-as-query change (Section 4) on its own branch **with tests**, threading a `QueryClient` into the singleton and emitting `setQueryData` at each transition — not as part of this low-risk spike.
