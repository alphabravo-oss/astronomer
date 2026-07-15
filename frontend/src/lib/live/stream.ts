/**
 * Live-event SSE transport for the Astronomer dashboard.
 *
 * One EventSource for the lifetime of the dashboard layout; incoming frames
 * fan out via a tiny EventTarget so any number of pages / hooks can
 * subscribe without opening their own connection. Ported from the proven
 * `lib/live-events.ts` singleton: module ConnectionState, refcount,
 * 1s→30s exponential backoff, mint-then-connect with single-use tickets
 * (never EventSource auto-reconnect — the ticket is one-use, so the
 * browser's built-in retry would always 401).
 *
 * Auth contract (also documented in `internal/handler/events_stream.go`):
 * EventSource cannot send custom headers, so we first mint a short-lived
 * stream ticket over a normal authenticated XHR, then pass only that
 * one-use ticket in the stream URL.
 *
 * Frames are default-message SSE (`onmessage` only — no `event:` line);
 * the JSON envelope carries `type` and we dispatch on it. A 75s watchdog
 * (3 missed 25s `sys.ping`s) force-closes half-open connections into the
 * normal backoff/re-mint loop.
 */

import type { QueryClient } from '@tanstack/react-query';
import { createStreamTicket } from '@/lib/api';
import { API_BASE } from '@/lib/env';
import { dispatchLiveFrame } from './dispatch';
import { clearPacedInvalidations } from './paced-invalidate';
import { setLiveStatus, type LiveStatus } from './status-store';

/** 3 missed 25s sys.ping heartbeats = half-open connection. */
const WATCHDOG_MS = 75_000;

interface ConnectionState {
  source: EventSource | null;
  /** EventTarget that re-emits each backend event by envelope `type`. */
  target: EventTarget;
  /** Number of consecutive failed connect attempts (for backoff). */
  retryCount: number;
  /** Pending reconnect timer so we can cancel during teardown. */
  reconnectTimer: ReturnType<typeof setTimeout> | null;
  /** Heartbeat watchdog — reset on ANY frame, fires after WATCHDOG_MS. */
  watchdogTimer: ReturnType<typeof setTimeout> | null;
  /** Ticket this connection was opened with. */
  openedWithTicket: string | null;
  /** Reference count: how many live hooks currently hold the connection. */
  refCount: number;
  /** Most recent status the SSE client sees. */
  status: LiveStatus;
  /** True once the stream has opened at least once this session. */
  everOpened: boolean;
}

let conn: ConnectionState | null = null;

/**
 * QueryClient used for the transition catch-up invalidations. Registered by
 * the live hooks on mount (they run inside the provider); transitions before
 * registration are no-ops, which is correct — nothing is cached yet.
 */
let liveQueryClient: QueryClient | null = null;

export function registerLiveQueryClient(qc: QueryClient): void {
  liveQueryClient = qc;
}

function ensureConnection(): ConnectionState {
  if (!conn) {
    conn = {
      source: null,
      target: new EventTarget(),
      retryCount: 0,
      reconnectTimer: null,
      watchdogTimer: null,
      openedWithTicket: null,
      refCount: 0,
      status: 'idle',
      everOpened: false,
    };
  }
  return conn;
}

/** The shared fan-out target live hooks attach their listeners to. */
export function liveTarget(): EventTarget {
  return ensureConnection().target;
}

/**
 * Status transitions carry the resume story (no Last-Event-ID replay — the
 * bus has no history):
 *  - re-open after a drop ⇒ bulk-invalidate active queries so every mounted
 *    view catches up on whatever events were missed while disconnected;
 *  - open→closed ⇒ the same bulk invalidate, so every mounted query's
 *    `refetchInterval` function re-evaluates and fallback polling actually
 *    restarts (React Query only re-evaluates intervals after a fetch).
 * The FIRST open never invalidates — queries are fetching fresh already.
 */
function setStatus(state: ConnectionState, next: LiveStatus): void {
  const prev = state.status;
  if (prev === next) return;
  state.status = next;
  setLiveStatus(next);
  if (next === 'closed') {
    // Pending trailing invalidations are pointless with the event source
    // gone — the open→closed bulk invalidate below kicks every active query.
    clearPacedInvalidations();
  }
  if (next === 'open') {
    if (state.everOpened) {
      liveQueryClient?.invalidateQueries({ refetchType: 'active' });
    }
    state.everOpened = true;
  } else if (prev === 'open' && next === 'closed') {
    liveQueryClient?.invalidateQueries({ refetchType: 'active' });
  }
}

/** Build the stream URL with a one-use stream ticket. */
function streamURL(ticket: string): string {
  // Trailing slash matches the backend route registration.
  const url = `${API_BASE}/events/stream/`;
  return `${url}?ticket=${encodeURIComponent(ticket)}`;
}

function clearWatchdog(state: ConnectionState): void {
  if (state.watchdogTimer) {
    clearTimeout(state.watchdogTimer);
    state.watchdogTimer = null;
  }
}

/** (Re)arm the heartbeat watchdog; expiry force-closes into the backoff loop. */
function armWatchdog(state: ConnectionState): void {
  clearWatchdog(state);
  state.watchdogTimer = setTimeout(() => {
    state.watchdogTimer = null;
    // Half-open connection: the socket looks open but nothing (not even
    // sys.ping) arrived for WATCHDOG_MS. Force-close and re-mint.
    try {
      state.source?.close();
    } catch {
      /* ignore */
    }
    state.source = null;
    setStatus(state, 'closed');
    scheduleReconnect(state);
  }, WATCHDOG_MS);
}

/**
 * Open (or re-open) the underlying EventSource. Reconnects on close with an
 * exponential backoff capped at 30s; every (re)connect mints a fresh
 * single-use ticket. Each incoming frame is re-emitted on `state.target`
 * keyed by the envelope's `type` plus a `*` wildcard.
 */
function openSource(state: ConnectionState): void {
  if (state.source) {
    if (state.status !== 'closed') return;
    try {
      state.source.close();
    } catch {
      /* ignore */
    }
    state.source = null;
  }
  if (state.reconnectTimer) {
    clearTimeout(state.reconnectTimer);
    state.reconnectTimer = null;
  }
  setStatus(state, 'connecting');
  createStreamTicket('events')
    .then(({ ticket }) => {
      if (state.refCount === 0 || state.source) return;
      state.openedWithTicket = ticket;
      let es: EventSource;
      try {
        es = new EventSource(streamURL(ticket), { withCredentials: false });
      } catch {
        scheduleReconnect(state);
        return;
      }
      state.source = es;

      es.onopen = () => {
        setStatus(state, 'open');
        state.retryCount = 0;
        armWatchdog(state);
      };

      // Default-message dispatch: the envelope's `type` field is the routing
      // key. There is no addEventListener-per-type registration (the old
      // KNOWN_EVENT_TYPES footgun) — new backend types just work. The
      // dispatcher fans the frame out on `state.target` and routes it into
      // the paced query invalidator (see lib/live/dispatch.ts).
      es.onmessage = (ev) => {
        armWatchdog(state);
        dispatchLiveFrame(ev.data, state.target, liveQueryClient);
      };

      es.onerror = () => {
        clearWatchdog(state);
        try {
          es.close();
        } catch {
          /* ignore */
        }
        state.source = null;
        setStatus(state, 'closed');
        scheduleReconnect(state);
      };
    })
    .catch(() => {
      setStatus(state, 'closed');
      scheduleReconnect(state);
    });
}

function scheduleReconnect(state: ConnectionState): void {
  if (state.refCount === 0) return; // nothing's listening
  state.retryCount += 1;
  // Exponential backoff: 1s, 2s, 4s, 8s, ... capped at 30s.
  const delay = Math.min(30000, 1000 * 2 ** Math.min(state.retryCount - 1, 5));
  state.reconnectTimer = setTimeout(() => {
    state.reconnectTimer = null;
    openSource(state);
  }, delay);
}

/**
 * Take a refcounted hold on the shared connection, opening it if needed.
 * Every acquire must be paired with exactly one `releaseLiveStream()`.
 */
export function acquireLiveStream(): void {
  const state = ensureConnection();
  state.refCount += 1;
  if (!state.source || state.status === 'closed') {
    openSource(state);
  }
}

/** Drop one hold; the last release tears the connection down. */
export function releaseLiveStream(): void {
  const state = ensureConnection();
  state.refCount -= 1;
  if (state.refCount <= 0) {
    if (state.reconnectTimer) {
      clearTimeout(state.reconnectTimer);
      state.reconnectTimer = null;
    }
    clearWatchdog(state);
    try {
      state.source?.close();
    } catch {
      /* ignore */
    }
    state.source = null;
    setStatus(state, 'closed');
    state.refCount = 0;
  }
}

/**
 * Re-open after laptop-sleep / tab-throttle scenarios where the OS reset the
 * TCP connection — wired to `visibilitychange` by the mount hook.
 */
export function reopenIfClosed(): void {
  const state = conn;
  if (state && state.refCount > 0 && state.status === 'closed') {
    openSource(state);
  }
}
