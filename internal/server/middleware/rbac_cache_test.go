package middleware

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

// fakeUserBindingsQuerier implements userBindingsQuerier and counts DB calls
// per method, so tests can assert "exactly one DB round-trip on a cold cache,
// zero on a hit" without spinning up a real Postgres.
type fakeUserBindingsQuerier struct {
	mu              sync.Mutex
	listCalls       int64
	getUserCalls    int64
	rowsByUser      map[string][]sqlc.ListUserBindingsWithRolesRow
	superuserByUser map[string]bool
}

func newFakeUserBindingsQuerier() *fakeUserBindingsQuerier {
	return &fakeUserBindingsQuerier{
		rowsByUser:      map[string][]sqlc.ListUserBindingsWithRolesRow{},
		superuserByUser: map[string]bool{},
	}
}

func (f *fakeUserBindingsQuerier) GetUserByID(_ context.Context, id uuid.UUID) (sqlc.User, error) {
	atomic.AddInt64(&f.getUserCalls, 1)
	return sqlc.User{ID: id, IsSuperuser: f.superuserByUser[id.String()]}, nil
}

func (f *fakeUserBindingsQuerier) ListUserBindingsWithRoles(_ context.Context, userID pgtype.UUID) ([]sqlc.ListUserBindingsWithRolesRow, error) {
	atomic.AddInt64(&f.listCalls, 1)
	if !userID.Valid {
		return nil, nil
	}
	key := uuid.UUID(userID.Bytes).String()
	f.mu.Lock()
	defer f.mu.Unlock()
	rows := f.rowsByUser[key]
	out := make([]sqlc.ListUserBindingsWithRolesRow, len(rows))
	copy(out, rows)
	return out, nil
}

func (f *fakeUserBindingsQuerier) setRows(userID string, rows []sqlc.ListUserBindingsWithRolesRow) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rowsByUser[userID] = rows
}

func (f *fakeUserBindingsQuerier) listCount() int64 {
	return atomic.LoadInt64(&f.listCalls)
}

func mkRow(scope, name string) sqlc.ListUserBindingsWithRolesRow {
	return sqlc.ListUserBindingsWithRolesRow{
		Scope:     scope,
		BindingID: uuid.New(),
		RoleID:    uuid.New(),
		RoleName:  name,
		RoleRules: []byte(`[{"resource":"clusters","verbs":["read"]}]`),
	}
}

// TestRBACCache_HitWithinTTL: a second lookup inside the TTL window must NOT
// hit the DB. Asserted by checking the fake querier's ListUserBindingsWithRoles
// counter is 1 after two Get calls.
func TestRBACCache_HitWithinTTL(t *testing.T) {
	t.Parallel()
	fake := newFakeUserBindingsQuerier()
	userID := uuid.New().String()
	fake.setRows(userID, []sqlc.ListUserBindingsWithRolesRow{mkRow("global", "viewer")})

	cache := NewRBACCacheWithOptions(15*time.Second, 100)
	q := NewSQLCRBACQuerierWithCache(fake, cache)

	if _, err := q.GetUserBindings(context.Background(), userID); err != nil {
		t.Fatalf("first GetUserBindings: %v", err)
	}
	if got := fake.listCount(); got != 1 {
		t.Fatalf("after first call: list count = %d, want 1", got)
	}
	if _, err := q.GetUserBindings(context.Background(), userID); err != nil {
		t.Fatalf("second GetUserBindings: %v", err)
	}
	if got := fake.listCount(); got != 1 {
		t.Fatalf("after second call: list count = %d, want 1 (cache hit expected)", got)
	}
}

// TestRBACCache_MissAfterTTL: once we fast-forward past the TTL the next read
// must re-query. Uses an injectable `now` so the test is deterministic and
// doesn't sleep.
func TestRBACCache_MissAfterTTL(t *testing.T) {
	t.Parallel()
	fake := newFakeUserBindingsQuerier()
	userID := uuid.New().String()
	fake.setRows(userID, []sqlc.ListUserBindingsWithRolesRow{mkRow("global", "viewer")})

	cache := NewRBACCacheWithOptions(15*time.Second, 100)
	// Fast-forwardable clock: starts at t0, jumps via the closure.
	t0 := time.Now()
	currentTime := t0
	var clockMu sync.Mutex
	cache.now = func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return currentTime
	}

	q := NewSQLCRBACQuerierWithCache(fake, cache)

	if _, err := q.GetUserBindings(context.Background(), userID); err != nil {
		t.Fatalf("first GetUserBindings: %v", err)
	}
	if got := fake.listCount(); got != 1 {
		t.Fatalf("after first call: list count = %d, want 1", got)
	}

	// Advance past TTL.
	clockMu.Lock()
	currentTime = t0.Add(16 * time.Second)
	clockMu.Unlock()

	if _, err := q.GetUserBindings(context.Background(), userID); err != nil {
		t.Fatalf("post-TTL GetUserBindings: %v", err)
	}
	if got := fake.listCount(); got != 2 {
		t.Fatalf("after TTL expiry: list count = %d, want 2 (cache miss expected)", got)
	}
}

// TestRBACCache_Invalidate: calling Invalidate forces the next read to refetch
// from the DB, regardless of the TTL window.
func TestRBACCache_Invalidate(t *testing.T) {
	t.Parallel()
	fake := newFakeUserBindingsQuerier()
	userID := uuid.New().String()
	fake.setRows(userID, []sqlc.ListUserBindingsWithRolesRow{mkRow("global", "viewer")})

	cache := NewRBACCacheWithOptions(15*time.Second, 100)
	q := NewSQLCRBACQuerierWithCache(fake, cache)

	if _, err := q.GetUserBindings(context.Background(), userID); err != nil {
		t.Fatalf("populate: %v", err)
	}
	if got := fake.listCount(); got != 1 {
		t.Fatalf("populate count = %d, want 1", got)
	}

	q.Invalidate(userID)

	if _, err := q.GetUserBindings(context.Background(), userID); err != nil {
		t.Fatalf("post-invalidate: %v", err)
	}
	if got := fake.listCount(); got != 2 {
		t.Fatalf("after invalidate: list count = %d, want 2", got)
	}
}

// TestRBACCache_LRUBounded: fill the cache to capacity, then add one more and
// confirm the oldest entry is evicted (the next access on it must re-query).
func TestRBACCache_LRUBounded(t *testing.T) {
	t.Parallel()
	fake := newFakeUserBindingsQuerier()
	const capacity = 4
	cache := NewRBACCacheWithOptions(15*time.Second, capacity)
	q := NewSQLCRBACQuerierWithCache(fake, cache)

	users := make([]string, capacity+1)
	for i := range users {
		users[i] = uuid.New().String()
		fake.setRows(users[i], []sqlc.ListUserBindingsWithRolesRow{
			mkRow("global", fmt.Sprintf("role-%d", i)),
		})
	}

	// Populate the first `capacity` users. Each is a cache miss exactly once.
	for i := 0; i < capacity; i++ {
		if _, err := q.GetUserBindings(context.Background(), users[i]); err != nil {
			t.Fatalf("populate %d: %v", i, err)
		}
	}
	if got := cache.Len(); got != capacity {
		t.Fatalf("after populate: cache size = %d, want %d", got, capacity)
	}
	baseline := fake.listCount()
	if baseline != int64(capacity) {
		t.Fatalf("baseline list count = %d, want %d", baseline, capacity)
	}

	// Insert one more — should evict users[0] (the oldest, never re-touched).
	if _, err := q.GetUserBindings(context.Background(), users[capacity]); err != nil {
		t.Fatalf("populate overflow: %v", err)
	}
	if got := cache.Len(); got != capacity {
		t.Fatalf("after overflow: cache size = %d, want %d (LRU should evict)", got, capacity)
	}

	// users[0] should now miss; the youngest retained entries (the ones
	// promoted to the front by the overflow insert) should still hit.
	// users[capacity-1] was the most-recently-populated before overflow and
	// users[capacity] was added during overflow — both must still be cached.
	listBefore := fake.listCount()
	if _, err := q.GetUserBindings(context.Background(), users[0]); err != nil {
		t.Fatalf("get evicted: %v", err)
	}
	if got := fake.listCount(); got != listBefore+1 {
		t.Fatalf("get evicted user: list count = %d, want %d (re-fetch expected)", got, listBefore+1)
	}
	// users[capacity] was the most recent before users[0] re-fetch, and the
	// LRU policy keeps it. (users[0] re-fetch evicted users[1], which was the
	// new tail after the first overflow.)
	listBefore = fake.listCount()
	if _, err := q.GetUserBindings(context.Background(), users[capacity]); err != nil {
		t.Fatalf("get retained: %v", err)
	}
	if got := fake.listCount(); got != listBefore {
		t.Fatalf("get retained user[capacity]: list count = %d, want %d (cache hit expected)", got, listBefore)
	}
}

// TestRBACCache_InvalidateAll: dumps the whole cache; every subsequent read
// for any user must re-query.
func TestRBACCache_InvalidateAll(t *testing.T) {
	t.Parallel()
	fake := newFakeUserBindingsQuerier()
	cache := NewRBACCacheWithOptions(15*time.Second, 100)
	q := NewSQLCRBACQuerierWithCache(fake, cache)

	users := []string{uuid.New().String(), uuid.New().String(), uuid.New().String()}
	for _, u := range users {
		fake.setRows(u, []sqlc.ListUserBindingsWithRolesRow{mkRow("global", "viewer")})
		if _, err := q.GetUserBindings(context.Background(), u); err != nil {
			t.Fatalf("populate %s: %v", u, err)
		}
	}
	if cache.Len() != 3 {
		t.Fatalf("cache size = %d, want 3", cache.Len())
	}

	q.InvalidateAll()
	if cache.Len() != 0 {
		t.Fatalf("after InvalidateAll: cache size = %d, want 0", cache.Len())
	}

	pre := fake.listCount()
	for _, u := range users {
		if _, err := q.GetUserBindings(context.Background(), u); err != nil {
			t.Fatalf("refetch %s: %v", u, err)
		}
	}
	if got := fake.listCount(); got != pre+int64(len(users)) {
		t.Fatalf("after InvalidateAll refetch: list count delta = %d, want %d", got-pre, len(users))
	}
}

// TestRBACCache_SuperuserShortCircuit: a superuser must yield exactly one
// synthetic IsSuperuser binding without a ListUserBindingsWithRoles call.
// (GetUserByID is used to determine the flag, then the result is cached.)
func TestRBACCache_SuperuserShortCircuit(t *testing.T) {
	t.Parallel()
	fake := newFakeUserBindingsQuerier()
	userID := uuid.New().String()
	fake.superuserByUser[userID] = true

	cache := NewRBACCacheWithOptions(15*time.Second, 100)
	q := NewSQLCRBACQuerierWithCache(fake, cache)

	bindings, err := q.GetUserBindings(context.Background(), userID)
	if err != nil {
		t.Fatalf("GetUserBindings: %v", err)
	}
	if len(bindings) != 1 || !bindings[0].IsSuperuser {
		t.Fatalf("superuser short-circuit: got %#v, want one binding with IsSuperuser=true", bindings)
	}
	if fake.listCount() != 0 {
		t.Fatalf("superuser path called ListUserBindingsWithRoles %d times, want 0", fake.listCount())
	}

	// Second call: cache hit, neither GetUserByID nor List should fire again.
	before := atomic.LoadInt64(&fake.getUserCalls)
	if _, err := q.GetUserBindings(context.Background(), userID); err != nil {
		t.Fatalf("second GetUserBindings: %v", err)
	}
	if got := atomic.LoadInt64(&fake.getUserCalls); got != before {
		t.Fatalf("superuser second call: GetUserByID count = %d, want %d (cache hit)", got, before)
	}
}

// TestRBACCache_AnonymousUserSkipped: empty userID must not populate any
// cache entry.
func TestRBACCache_AnonymousUserSkipped(t *testing.T) {
	t.Parallel()
	fake := newFakeUserBindingsQuerier()
	cache := NewRBACCacheWithOptions(15*time.Second, 100)
	q := NewSQLCRBACQuerierWithCache(fake, cache)

	bindings, err := q.GetUserBindings(context.Background(), "")
	if err != nil {
		t.Fatalf("GetUserBindings(\"\"): %v", err)
	}
	if bindings != nil {
		t.Fatalf("anonymous bindings = %#v, want nil", bindings)
	}
	if cache.Len() != 0 {
		t.Fatalf("cache populated for anonymous user: size = %d", cache.Len())
	}
}

// BenchmarkRBACCache_Hit measures the steady-state hit cost: the cache should
// be lock-cheap so the RBAC middleware can sit in the hot path on every
// authenticated request without adding latency.
func BenchmarkRBACCache_Hit(b *testing.B) {
	fake := newFakeUserBindingsQuerier()
	userID := uuid.New().String()
	fake.setRows(userID, []sqlc.ListUserBindingsWithRolesRow{
		mkRow("global", "viewer"),
		mkRow("cluster", "operator"),
		mkRow("project", "editor"),
	})
	cache := NewRBACCacheWithOptions(15*time.Second, 100)
	q := NewSQLCRBACQuerierWithCache(fake, cache)
	if _, err := q.GetUserBindings(context.Background(), userID); err != nil {
		b.Fatalf("warm: %v", err)
	}
	preBench := fake.listCount()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := q.GetUserBindings(context.Background(), userID); err != nil {
			b.Fatalf("iter %d: %v", i, err)
		}
	}
	b.StopTimer()
	// Verification: the benchmark must not have triggered any extra DB calls.
	if got := fake.listCount(); got != preBench {
		b.Fatalf("benchmark hit list-count = %d, want %d (no DB calls expected)", got, preBench)
	}
	var rbinding rbac.RoleBinding
	_ = rbinding // silence unused import warnings in some toolchains
}

// TestRBACCache_ClusterBindingNamespaceMapped verifies the persisted namespace
// scope on a cluster role binding flows from the DB row into the in-memory
// rbac.RoleBinding.Namespace so the engine can fail closed on namespace
// mismatch. Global/project rows must stay namespace-less.
func TestRBACCache_ClusterBindingNamespaceMapped(t *testing.T) {
	t.Parallel()
	fake := newFakeUserBindingsQuerier()
	userID := uuid.New().String()
	clusterID := uuid.New()

	clusterRow := mkRow("cluster", "ns-operator")
	clusterRow.ClusterID = pgtype.UUID{Bytes: clusterID, Valid: true}
	clusterRow.Namespace = "team-a"
	globalRow := mkRow("global", "viewer")

	fake.setRows(userID, []sqlc.ListUserBindingsWithRolesRow{clusterRow, globalRow})

	cache := NewRBACCacheWithOptions(15*time.Second, 100)
	q := NewSQLCRBACQuerierWithCache(fake, cache)

	bindings, err := q.GetUserBindings(context.Background(), userID)
	if err != nil {
		t.Fatalf("GetUserBindings: %v", err)
	}

	var cluster, global *rbac.RoleBinding
	for i := range bindings {
		switch bindings[i].Scope {
		case "cluster":
			cluster = &bindings[i]
		case "global":
			global = &bindings[i]
		}
	}
	if cluster == nil || global == nil {
		t.Fatalf("expected one cluster and one global binding, got %+v", bindings)
	}
	if cluster.Namespace != "team-a" {
		t.Fatalf("cluster binding namespace = %q, want %q", cluster.Namespace, "team-a")
	}
	if cluster.ClusterID != clusterID.String() {
		t.Fatalf("cluster binding cluster_id = %q, want %q", cluster.ClusterID, clusterID.String())
	}
	if global.Namespace != "" {
		t.Fatalf("global binding namespace = %q, want empty", global.Namespace)
	}
}
