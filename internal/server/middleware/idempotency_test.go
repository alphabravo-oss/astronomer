package middleware

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A retried POST carrying the same Idempotency-Key replays the cached
// response and runs the handler exactly once.
func TestIdempotency_ReplaysOnRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var calls int32
	mw := idempotencyWith(ctx, time.Minute, time.Now)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"call":`+strconv.Itoa(int(n))+`}`)
	}))

	do := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/c/workloads/x/scale/", nil)
		req.Header.Set("Idempotency-Key", "abc-123")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	first := do()
	if first.Code != http.StatusCreated {
		t.Fatalf("first status = %d, want 201", first.Code)
	}
	if first.Body.String() != `{"call":1}` {
		t.Fatalf("first body = %q", first.Body.String())
	}

	second := do()
	if second.Code != http.StatusCreated {
		t.Fatalf("replay status = %d, want 201", second.Code)
	}
	if second.Body.String() != `{"call":1}` {
		t.Fatalf("replay body = %q, want cached call 1", second.Body.String())
	}
	if second.Header().Get("Idempotent-Replayed") != "true" {
		t.Error("replay must carry Idempotent-Replayed: true")
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("handler ran %d times, want 1", got)
	}
}

// Reads and keyless requests bypass the guard entirely.
func TestIdempotency_SkipsReadsAndKeyless(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var calls int32
	mw := idempotencyWith(ctx, time.Minute, time.Now)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusOK)
	}))

	// GET with a key: not a mutation, must not be deduped.
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/x/", nil)
		req.Header.Set("Idempotency-Key", "k")
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	// POST without a key: must not be deduped.
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/x/", nil)
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	if got := atomic.LoadInt32(&calls); got != 4 {
		t.Fatalf("handler ran %d times, want 4 (no dedup)", got)
	}
}

// Distinct keys (and distinct users for the same key) do not collide.
func TestIdempotency_DistinctKeysDoNotCollide(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var calls int32
	mw := idempotencyWith(ctx, time.Minute, time.Now)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusOK)
	}))

	keys := []string{"k1", "k2", "k3"}
	for _, k := range keys {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/x/", nil)
		req.Header.Set("Idempotency-Key", k)
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("handler ran %d times, want 3 (distinct keys)", got)
	}
}

// Concurrent retries of the same key collapse onto one handler run and all
// receive the same response.
func TestIdempotency_ConcurrentCollapse(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	release := make(chan struct{})
	var calls int32
	mw := idempotencyWith(ctx, time.Minute, time.Now)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		<-release // hold the first request in-flight
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, "ok")
	}))

	const n = 8
	var wg sync.WaitGroup
	codes := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/x/", nil)
			req.Header.Set("Idempotency-Key", "shared")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			codes[i] = rec.Code
		}(i)
	}
	// Give the goroutines a moment to all register on the same in-flight key,
	// then release the first handler.
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("handler ran %d times under concurrency, want 1", got)
	}
	for i, c := range codes {
		if c != http.StatusAccepted {
			t.Fatalf("request %d got %d, want 202", i, c)
		}
	}
}

// Expired entries evict and a later retry runs fresh.
func TestIdempotency_EvictExpired(t *testing.T) {
	now := time.Unix(0, 0)
	clock := func() time.Time { return now }
	store := newIdempotencyStore(time.Minute, clock)

	e, first := store.begin("k")
	if !first {
		t.Fatal("first begin must own the entry")
	}
	// complete it
	e.storedAt = clock()
	close(e.done)

	now = now.Add(2 * time.Minute)
	if n := store.evictExpired(); n != 1 {
		t.Fatalf("evicted %d, want 1", n)
	}
	if _, first := store.begin("k"); !first {
		t.Fatal("post-eviction begin must run fresh")
	}
}
