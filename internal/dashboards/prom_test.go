package dashboards

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

// TestMain disables the SSRF dial guard so httptest loopback servers stay
// reachable. Production uses SafeClientAllowPrivate (SEC-R04).
func TestMain(m *testing.M) {
	restore := httpclient.DisableGuardForTest()
	code := m.Run()
	restore()
	os.Exit(code)
}

// TestPromQueryRange_CachedSecondHit verifies the 30s cache: two
// QueryRange calls inside the TTL must produce ONE upstream HTTP
// request. This is the load-shedding contract the handler relies on
// when N concurrent dashboard polls land on the same widget.
func TestPromQueryRange_CachedSecondHit(t *testing.T) {
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		if !strings.HasSuffix(r.URL.Path, "/api/v1/query_range") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[1700000000,"1"],[1700000060,"2"],[1700000120,"3"]]}]}}`))
	}))
	defer srv.Close()

	cache := NewCache(30 * time.Second)
	ds := Datasource{ID: "ds1", URL: srv.URL}
	ctx := context.Background()
	now := time.Unix(1700000200, 0)

	m1, err := QueryRange(ctx, cache, ds, `up`, "5m", "60s", now)
	if err != nil {
		t.Fatalf("first QueryRange: %v", err)
	}
	if len(m1.Samples) != 3 {
		t.Fatalf("expected 3 samples, got %d", len(m1.Samples))
	}
	m2, err := QueryRange(ctx, cache, ds, `up`, "5m", "60s", now)
	if err != nil {
		t.Fatalf("second QueryRange: %v", err)
	}
	if len(m2.Samples) != 3 {
		t.Fatalf("cached miss: expected 3, got %d", len(m2.Samples))
	}
	if h := atomic.LoadInt64(&hits); h != 1 {
		t.Fatalf("expected exactly 1 upstream hit, got %d", h)
	}

	// Distinct query → distinct cache key → second upstream hit.
	if _, err := QueryRange(ctx, cache, ds, `down`, "5m", "60s", now); err != nil {
		t.Fatalf("third QueryRange: %v", err)
	}
	if h := atomic.LoadInt64(&hits); h != 2 {
		t.Fatalf("expected 2 hits after distinct query, got %d", h)
	}
}

// TestPromEvalStat_RoundTrip exercises the instant-query path,
// including the (value, ok=false) "empty result" semantics.
func TestPromEvalStat_RoundTrip(t *testing.T) {
	var idx int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/api/v1/query") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		idx++
		switch idx {
		case 1:
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1700000000,"42.5"]}]}}`))
		default:
			// Empty result — "ok=false" path.
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
		}
	}))
	defer srv.Close()
	cache := NewCache(30 * time.Second)
	ds := Datasource{ID: "ds2", URL: srv.URL}
	ctx := context.Background()
	v, ok, err := EvalStat(ctx, cache, ds, `foo`)
	if err != nil {
		t.Fatalf("EvalStat: %v", err)
	}
	if !ok || v != 42.5 {
		t.Fatalf("expected (42.5, true), got (%v, %v)", v, ok)
	}
	// Different query, fresh upstream → empty result.
	_, ok2, err := EvalStat(ctx, cache, ds, `bar`)
	if err != nil {
		t.Fatalf("EvalStat empty: %v", err)
	}
	if ok2 {
		t.Fatalf("expected ok=false for empty result")
	}
}

// TestPromQueryRange_AuthHeader verifies Bearer + Basic auth get
// applied per-datasource. We don't separately test "no auth" because
// the success path in TestPromQueryRange_CachedSecondHit already
// exercises it.
func TestPromQueryRange_AuthHeader(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[]}}`))
	}))
	defer srv.Close()
	cache := NewCache(30 * time.Second)
	ctx := context.Background()
	now := time.Unix(1700000200, 0)

	// Bearer wins when both are set.
	_, _ = QueryRange(ctx, cache, Datasource{ID: "ds3", URL: srv.URL, BearerToken: "T0K", BasicAuthUser: "u", BasicAuthPass: "p"}, `up`, "1m", "60s", now)
	if got != "Bearer T0K" {
		t.Fatalf("expected Bearer T0K, got %q", got)
	}
	// Basic-only path. Use a different datasource id so the cache
	// doesn't short-circuit before we see the upstream header.
	_, _ = QueryRange(ctx, cache, Datasource{ID: "ds4", URL: srv.URL, BasicAuthUser: "alice", BasicAuthPass: "secret"}, `up`, "1m", "60s", now)
	if !strings.HasPrefix(got, "Basic ") {
		t.Fatalf("expected Basic auth header, got %q", got)
	}
}

// TestPromQueryRange_ErrorPropagation checks that a non-2xx upstream
// produces an error rather than caching an empty matrix.
func TestPromQueryRange_ErrorPropagation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprintln(w, "boom")
	}))
	defer srv.Close()
	_, err := QueryRange(context.Background(), NewCache(30*time.Second), Datasource{ID: "ds5", URL: srv.URL}, `up`, "1m", "60s", time.Unix(1700000000, 0))
	if err == nil {
		t.Fatalf("expected error on 500 upstream")
	}
}
