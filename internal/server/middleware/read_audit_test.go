package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// fakePolicyStore is an in-memory PolicyLister.
type fakePolicyStore struct {
	mu    sync.Mutex
	rows  []sqlc.ReadAuditPolicy
	calls int
}

func (f *fakePolicyStore) ListEnabledReadAuditPolicies(_ context.Context) ([]sqlc.ReadAuditPolicy, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	out := make([]sqlc.ReadAuditPolicy, len(f.rows))
	copy(out, f.rows)
	return out, nil
}

func policy(name, pattern, verbs string, rate float64) sqlc.ReadAuditPolicy {
	return sqlc.ReadAuditPolicy{
		ID:          uuid.New(),
		Name:        name,
		PathPattern: pattern,
		Verbs:       verbs,
		SampleRate:  rate,
		Enabled:     true,
	}
}

func TestPolicyEvaluator_MatchesPathPrefix(t *testing.T) {
	store := &fakePolicyStore{rows: []sqlc.ReadAuditPolicy{
		policy("creds", "/projects/*/cloud-credentials", "GET", 1.0),
	}}
	eval := NewPolicyEvaluator(store)

	cases := []struct {
		route string
		want  bool
	}{
		{"/projects/{id}/cloud-credentials/", true},
		{"/projects/{id}/cloud-credentials", true},
		{"/api/v1/projects/{id}/cloud-credentials/", true},
		{"/projects/{id}/other", false},
		{"/clusters/{id}/registries", false},
	}
	for _, tc := range cases {
		got := eval.Match(context.Background(), tc.route, "GET")
		if (got != nil) != tc.want {
			t.Errorf("Match(%q) gotNil=%v want match=%v", tc.route, got == nil, tc.want)
		}
	}
}

func TestPolicyEvaluator_RespectsVerbFilter(t *testing.T) {
	store := &fakePolicyStore{rows: []sqlc.ReadAuditPolicy{
		policy("creds", "/projects/*/cloud-credentials", "GET", 1.0),
	}}
	eval := NewPolicyEvaluator(store)

	if eval.Match(context.Background(), "/projects/{id}/cloud-credentials", "POST") != nil {
		t.Fatal("expected POST not to match GET-only policy")
	}
	if eval.Match(context.Background(), "/projects/{id}/cloud-credentials", "GET") == nil {
		t.Fatal("expected GET to match GET-only policy")
	}

	// Wildcard verb
	store2 := &fakePolicyStore{rows: []sqlc.ReadAuditPolicy{
		policy("all", "/admin/sso", "*", 1.0),
	}}
	eval2 := NewPolicyEvaluator(store2)
	for _, m := range []string{"GET", "POST", "DELETE", "HEAD"} {
		if eval2.Match(context.Background(), "/admin/sso/config", m) == nil {
			t.Fatalf("expected wildcard to match %s", m)
		}
	}
}

func TestPolicyEvaluator_SampleRateOneAlwaysFires(t *testing.T) {
	p := policy("x", "/x", "GET", 1.0)
	eval := NewPolicyEvaluator(&fakePolicyStore{})
	for i := 0; i < 100; i++ {
		if !eval.Sample(&p) {
			t.Fatal("sample_rate 1.0 must always fire")
		}
	}
}

func TestPolicyEvaluator_SampleRateZeroNeverFires(t *testing.T) {
	p := policy("x", "/x", "GET", 0.0)
	eval := NewPolicyEvaluator(&fakePolicyStore{})
	for i := 0; i < 100; i++ {
		if eval.Sample(&p) {
			t.Fatal("sample_rate 0.0 must never fire")
		}
	}
}

func TestPolicyEvaluator_CacheInvalidatesOnWrite(t *testing.T) {
	store := &fakePolicyStore{rows: []sqlc.ReadAuditPolicy{
		policy("x", "/x", "GET", 1.0),
	}}
	eval := NewPolicyEvaluator(store)
	_ = eval.ListPolicies(context.Background())
	_ = eval.ListPolicies(context.Background())
	if store.calls != 1 {
		t.Fatalf("cache should suppress second fetch; got calls=%d", store.calls)
	}
	eval.Invalidate()
	_ = eval.ListPolicies(context.Background())
	if store.calls != 2 {
		t.Fatalf("invalidate should force refetch; got calls=%d", store.calls)
	}
}

// fakeEnqueuer captures CreateAuditLogV1 calls.
type fakeEnqueuer struct {
	mu   sync.Mutex
	rows []sqlc.CreateAuditLogV1Params
}

func (f *fakeEnqueuer) CreateAuditLogV1(_ context.Context, arg sqlc.CreateAuditLogV1Params) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows = append(f.rows, arg)
	return nil
}

func (f *fakeEnqueuer) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.rows)
}

func waitFor(t *testing.T, cond func() bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("waitFor timed out")
}

func TestReadAuditMiddleware_EmitsForMatchedPath(t *testing.T) {
	store := &fakePolicyStore{rows: []sqlc.ReadAuditPolicy{
		policy("creds", "/projects/*/cloud-credentials", "GET", 1.0),
	}}
	eval := NewPolicyEvaluator(store)
	enq := &fakeEnqueuer{}

	r := chi.NewRouter()
	r.Use(ReadAudit(eval, enq))
	r.Get("/projects/{id}/cloud-credentials/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/projects/" + uuid.NewString() + "/cloud-credentials/")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	waitFor(t, func() bool { return enq.count() == 1 }, time.Second)

	enq.mu.Lock()
	row := enq.rows[0]
	enq.mu.Unlock()
	if row.ActionClass != "read" {
		t.Errorf("ActionClass = %q, want read", row.ActionClass)
	}
	if row.Action == "" || row.Source != "http" {
		t.Errorf("unexpected row: %+v", row)
	}
	var detail map[string]any
	if err := json.Unmarshal(row.Detail, &detail); err != nil {
		t.Fatalf("detail not JSON: %v", err)
	}
	if detail["policy_name"] != "creds" {
		t.Errorf("policy_name = %v, want creds", detail["policy_name"])
	}
	// Bodies must NOT be captured.
	if _, hasBody := detail["body"]; hasBody {
		t.Error("request body must not be in audit row")
	}
}

func TestReadAuditMiddleware_NoOpForUnmatched(t *testing.T) {
	store := &fakePolicyStore{rows: []sqlc.ReadAuditPolicy{
		policy("creds", "/projects/*/cloud-credentials", "GET", 1.0),
	}}
	eval := NewPolicyEvaluator(store)
	enq := &fakeEnqueuer{}

	r := chi.NewRouter()
	r.Use(ReadAudit(eval, enq))
	r.Get("/random/route/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	srv := httptest.NewServer(r)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/random/route/")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	// Give any background goroutine time to (not) emit.
	time.Sleep(50 * time.Millisecond)
	if c := enq.count(); c != 0 {
		t.Fatalf("expected 0 emissions, got %d", c)
	}
}

// blockingEnqueuer hangs in CreateAuditLogV1 until released, letting us
// verify that the HTTP handler does NOT wait on persistence.
type blockingEnqueuer struct {
	gate chan struct{}
	hit  atomic.Int64
}

func (b *blockingEnqueuer) CreateAuditLogV1(ctx context.Context, _ sqlc.CreateAuditLogV1Params) error {
	b.hit.Add(1)
	select {
	case <-b.gate:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestReadAuditMiddleware_NonBlocking(t *testing.T) {
	store := &fakePolicyStore{rows: []sqlc.ReadAuditPolicy{
		policy("creds", "/projects/*/cloud-credentials", "GET", 1.0),
	}}
	eval := NewPolicyEvaluator(store)
	enq := &blockingEnqueuer{gate: make(chan struct{})}

	r := chi.NewRouter()
	r.Use(ReadAudit(eval, enq))
	r.Get("/projects/{id}/cloud-credentials/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	srv := httptest.NewServer(r)
	defer srv.Close()

	start := time.Now()
	resp, err := http.Get(srv.URL + "/projects/" + uuid.NewString() + "/cloud-credentials/")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	elapsed := time.Since(start)

	if elapsed > 250*time.Millisecond {
		t.Fatalf("handler appeared to wait on DB: elapsed=%s", elapsed)
	}
	// Release the worker so the test exits cleanly.
	close(enq.gate)
}
