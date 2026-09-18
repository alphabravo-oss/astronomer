package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
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

	e, first, conflict := store.begin("k", "digest")
	if !first {
		t.Fatal("first begin must own the entry")
	}
	if conflict {
		t.Fatal("first begin reported a conflict")
	}
	// complete it
	e.storedAt = clock()
	close(e.done)

	now = now.Add(2 * time.Minute)
	if n := store.evictExpired(); n != 1 {
		t.Fatalf("evicted %d, want 1", n)
	}
	if _, first, _ := store.begin("k", "digest"); !first {
		t.Fatal("post-eviction begin must run fresh")
	}
}

func TestIdempotency_BindsKeyToBodyAndCanonicalQuery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var calls atomic.Int32
	h := idempotencyWith(ctx, time.Minute, time.Now)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read replayed request body: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(body)
	}))

	do := func(target, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
		req.Header.Set("Idempotency-Key", "bound")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	first := do("/api/v1/items?label=b&label=a&sort=name", `{"name":"first"}`)
	if first.Code != http.StatusCreated || first.Body.String() != `{"name":"first"}` {
		t.Fatalf("first response = %d %q", first.Code, first.Body.String())
	}
	equivalent := do("/api/v1/items?sort=name&label=a&label=b", `{"name":"first"}`)
	if equivalent.Code != http.StatusCreated || equivalent.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("canonical query replay = %d headers=%v", equivalent.Code, equivalent.Header())
	}
	conflict := do("/api/v1/items?sort=name&label=a&label=b", `{"name":"second"}`)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("different body status = %d, want 409", conflict.Code)
	}
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(conflict.Body.Bytes(), &envelope); err != nil || envelope.Error.Code != "idempotency_conflict" {
		t.Fatalf("conflict body = %q error=%v", conflict.Body.String(), err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("handler calls = %d, want 1", got)
	}
}

func TestIdempotency_DistinctQueryValuesConflict(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := idempotencyWith(ctx, time.Minute, time.Now)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	for index, target := range []string{"/api/v1/items?page=1", "/api/v1/items?page=2"} {
		req := httptest.NewRequest(http.MethodDelete, target, nil)
		req.Header.Set("Idempotency-Key", "same")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		want := http.StatusNoContent
		if index == 1 {
			want = http.StatusConflict
		}
		if rec.Code != want {
			t.Fatalf("request %d status = %d, want %d", index, rec.Code, want)
		}
	}
}

func TestIdempotency_OversizedBodyFailsBeforeHandler(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var called atomic.Bool
	h := idempotencyWith(ctx, time.Minute, time.Now)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called.Store(true)
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/items", bytes.NewReader(make([]byte, idempotencyMaxBodyBytes+1)))
	req.Header.Set("Idempotency-Key", "large")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge || called.Load() {
		t.Fatalf("oversized response = %d called=%t", rec.Code, called.Load())
	}
}

func TestIdempotency_IsolatesAuthenticatedUsers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	h := idempotencyWith(ctx, time.Minute, time.Now)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, userID := range []string{"11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/items", strings.NewReader("same"))
		req = req.WithContext(reqctx.WithUser(req.Context(), &reqctx.User{ID: userID}))
		req.Header.Set("Idempotency-Key", "shared")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("user %s status = %d", userID, rec.Code)
		}
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("handler calls = %d, want one per user", got)
	}
}
