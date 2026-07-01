// Package middleware — in-memory idempotency guard.
//
// This is a LIGHTWEIGHT retry guard, distinct from the DB-backed
// operation-idempotency flow at the handler layer (internal/handler/
// operation_idempotency.go, migration 097). That one is the durable,
// cross-replica record of a completed mutating operation; this one is a
// short-TTL, single-process dedup that replays the cached status+body when a
// client retries the exact same request before the first response landed
// (flaky network, double-click, impatient client re-POST).
//
// It deliberately reuses the same `Idempotency-Key` request header so a
// caller sets one key and gets covered by whichever layer applies. Routes
// that already go through the DB-backed path keep that as the source of
// truth; for everything else this is a cheap second line of defence.
//
// ── Ceiling (ponytail) ───────────────────────────────────────────────────
// In-memory is fine here BY DESIGN — the guard only needs to cover the
// seconds between a client's first request and its retry. Consequences of
// the in-memory choice, accepted on purpose:
//   - Not shared across replicas: a retry that lands on a different pod is
//     not deduped here (the DB-backed layer covers the operations that need
//     cross-replica guarantees).
//   - Lost on restart: a process bounce drops the cache; worst case the next
//     retry re-runs the handler, which is exactly the pre-existing behaviour.
//   - Bounded only by TTL + janitor: keys evict after the TTL, so the map
//     cannot grow without bound under a fixed key churn rate.
//
// If you need durable, multi-replica idempotency, wire the handler-layer
// DB-backed path — do not grow this into a distributed cache.
package middleware

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"sync"
	"time"
)

// idempotencyTTL is the default lifetime of a cached response. Long enough
// to absorb a client's automatic retry / a double-click, short enough that
// the map stays tiny. Not a durability guarantee — see the package doc.
const idempotencyTTL = 60 * time.Second

// idempotencyMaxBodyBytes caps the cached body so a large streaming response
// can't be buffered into the guard. Past this we record only status+headers
// and stop buffering (the retry still gets the right status; the body just
// isn't replayed). Mutating API responses are small JSON envelopes, so this
// is generous.
const idempotencyMaxBodyBytes = 1 << 20 // 1 MiB

// idempotencyEntry is a completed-or-in-flight cached response. While
// done==nil the first request is still running and concurrent retries wait
// on it; once closed, status/header/body are safe to replay.
type idempotencyEntry struct {
	done      chan struct{}
	status    int
	header    http.Header
	body      []byte
	truncated bool
	storedAt  time.Time
}

// idempotencyStore is the keyed in-memory cache with a mutex + janitor,
// mirroring the rate-limiter stores in this package.
type idempotencyStore struct {
	mu      sync.Mutex
	entries map[string]*idempotencyEntry
	ttl     time.Duration
	now     func() time.Time
}

func newIdempotencyStore(ttl time.Duration, now func() time.Time) *idempotencyStore {
	if ttl <= 0 {
		ttl = idempotencyTTL
	}
	if now == nil {
		now = time.Now
	}
	return &idempotencyStore{
		entries: make(map[string]*idempotencyEntry),
		ttl:     ttl,
		now:     now,
	}
}

// begin returns the entry for key. The bool reports whether the caller is
// the first (and therefore responsible for running the handler and filling
// the entry); a false bool means an existing fresh entry was found and
// should be waited on / replayed.
func (s *idempotencyStore) begin(key string) (*idempotencyEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.entries[key]; ok {
		// Expired entries are treated as absent so a stale key doesn't pin a
		// response forever; the janitor also sweeps these.
		if s.now().Sub(e.storedAt) < s.ttl || isInFlight(e) {
			return e, false
		}
	}
	e := &idempotencyEntry{done: make(chan struct{}), storedAt: s.now()}
	s.entries[key] = e
	return e, true
}

func isInFlight(e *idempotencyEntry) bool {
	select {
	case <-e.done:
		return false
	default:
		return true
	}
}

// abandon drops an in-flight entry whose handler never completed (panic
// recovered upstream, or a non-cacheable outcome) so a later retry can run
// fresh instead of waiting on a channel that will never close.
func (s *idempotencyStore) abandon(key string, e *idempotencyEntry) {
	s.mu.Lock()
	if cur, ok := s.entries[key]; ok && cur == e {
		delete(s.entries, key)
	}
	s.mu.Unlock()
}

// complete finalizes an in-flight entry with the captured response and marks
// the storedAt timestamp, then closes done so waiters can replay. The field
// writes happen under the store mutex so they don't race the janitor /
// begin, which read storedAt while holding the same lock. closing done after
// the unlock establishes the happens-before edge that lets waiters read the
// response fields safely without the lock.
func (s *idempotencyStore) complete(e *idempotencyEntry, header http.Header, status int, body []byte, truncated bool) {
	s.mu.Lock()
	e.header = header
	e.status = status
	e.body = body
	e.truncated = truncated
	e.storedAt = s.now()
	s.mu.Unlock()
	close(e.done)
}

func (s *idempotencyStore) evictExpired() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := s.now().Add(-s.ttl)
	n := 0
	for k, e := range s.entries {
		if isInFlight(e) {
			continue
		}
		if e.storedAt.Before(cutoff) {
			delete(s.entries, k)
			n++
		}
	}
	return n
}

func (s *idempotencyStore) startJanitor(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = s.ttl
	}
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.evictExpired()
			}
		}
	}()
}

// idempotencyRecorder buffers the handler's response so it can be both
// written to the client and cached for replay. Buffering stops past
// idempotencyMaxBodyBytes (status/headers still cached; body marked
// truncated), but the bytes are always passed through to the real writer so
// the live client sees the full response regardless.
type idempotencyRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
	buf         bytes.Buffer
	truncated   bool
}

func (rec *idempotencyRecorder) WriteHeader(code int) {
	if rec.wroteHeader {
		return
	}
	rec.status = code
	rec.wroteHeader = true
	rec.ResponseWriter.WriteHeader(code)
}

func (rec *idempotencyRecorder) Write(p []byte) (int, error) {
	if !rec.wroteHeader {
		rec.WriteHeader(http.StatusOK)
	}
	if rec.buf.Len()+len(p) <= idempotencyMaxBodyBytes {
		rec.buf.Write(p)
	} else {
		rec.truncated = true
	}
	return rec.ResponseWriter.Write(p)
}

// Idempotency returns middleware that dedupes retried mutating requests
// carrying an `Idempotency-Key` header within a short in-memory TTL,
// replaying the cached status+body on retry. Only 2xx and deterministic 4xx
// outcomes are cached; a 5xx is left uncached so a client retry re-executes
// the handler instead of being handed a stale transient error. It self-skips reads
// (GET/HEAD/OPTIONS) and requests without the header, so it is safe to apply
// to a whole route group without per-route POST/PUT/PATCH tagging — the same
// method-gating contract as RequireWriteScopeForMutations.
//
// The janitor that evicts expired entries lives for the lifetime of ctx.
func Idempotency(ctx context.Context) func(http.Handler) http.Handler {
	return idempotencyWith(ctx, idempotencyTTL, time.Now)
}

func idempotencyWith(ctx context.Context, ttl time.Duration, now func() time.Time) func(http.Handler) http.Handler {
	store := newIdempotencyStore(ttl, now)
	store.startJanitor(ctx, 0)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := idempotencyKey(r)
			if key == "" {
				// Read method, or no Idempotency-Key: nothing to dedupe.
				next.ServeHTTP(w, r)
				return
			}

			entry, first := store.begin(key)
			if !first {
				// A concurrent first request may still be running; wait for
				// it (bounded — the handler is under the per-route timeout),
				// then replay its cached response.
				<-entry.done
				replayIdempotent(w, entry)
				return
			}

			rec := &idempotencyRecorder{ResponseWriter: w, status: http.StatusOK}
			completed := false
			defer func() {
				if !completed {
					// Handler panicked (Recoverer is upstream of us): drop the
					// entry so retries aren't blocked on a never-closed channel.
					store.abandon(key, entry)
					close(entry.done)
				}
			}()
			next.ServeHTTP(rec, r)

			store.complete(
				entry,
				cloneIdempotentHeaders(rec.Header()),
				rec.status,
				append([]byte(nil), rec.buf.Bytes()...),
				rec.truncated,
			)
			completed = true

			// 5xx responses are transient: caching one would replay a server
			// error for the full TTL and defeat the client's own retry. The
			// entry is finalized above so any concurrent duplicate in flight
			// still sees this exact response, but it is immediately dropped
			// from the cache so the next retry re-executes the handler fresh.
			// 2xx and deterministic 4xx client errors stay cached.
			if rec.status >= 500 {
				store.abandon(key, entry)
			}
		})
	}
}

// replayIdempotent writes a cached entry to a fresh response. A truncated
// body (handler exceeded the buffer cap) replays status+headers only.
func replayIdempotent(w http.ResponseWriter, e *idempotencyEntry) {
	for k, vs := range e.header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.Header().Set("Idempotent-Replayed", "true")
	status := e.status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	if !e.truncated && len(e.body) > 0 {
		_, _ = w.Write(e.body)
	}
}

// cloneIdempotentHeaders copies the response headers worth replaying. The
// hop-by-hop / per-response headers that must not be replayed verbatim
// (Date, connection control) are dropped.
func cloneIdempotentHeaders(h http.Header) http.Header {
	out := make(http.Header, len(h))
	for k, vs := range h {
		switch http.CanonicalHeaderKey(k) {
		case "Date", "Connection", "Transfer-Encoding":
			continue
		}
		out[k] = append([]string(nil), vs...)
	}
	return out
}

// idempotencyKey derives the dedup key for a request, or "" when the request
// is not a dedup candidate (read method, or no header). The key is scoped by
// authenticated user + method + path so one client's key can never collide
// with another's, mirroring the domain:user:method:path scope the DB-backed
// handler-layer flow uses.
func idempotencyKey(r *http.Request) string {
	if r == nil || !isMutatingScopeMethod(r.Method) {
		return ""
	}
	hdr := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if hdr == "" || len(hdr) > 255 {
		return ""
	}
	userScope := "anonymous"
	if u, ok := GetAuthenticatedUser(r.Context()); ok && u != nil {
		userScope = "u:" + u.ID
	}
	return strings.Join([]string{userScope, r.Method, r.URL.EscapedPath(), hdr}, ":")
}
