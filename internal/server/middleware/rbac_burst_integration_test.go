//go:build integration

package middleware

import (
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/alphabravocompany/astronomer-go/internal/db"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/testdb"
)

// A real pgx pool + production tracer quantify the cold-cache fan-out. Running
// this same test over the previous querier records the regression baseline.
func TestPostgresRBACColdBurstPoolStats(t *testing.T) {
	t.Parallel()
	database, err := db.ConnectWithConfig(t.Context(), testdb.DSN(t), db.PoolConfig{MaxConns: 12, MinConns: 12})
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	q := sqlc.New(database.Pool())
	user, err := q.CreateUser(t.Context(), sqlc.CreateUserParams{Email: "burst@example.test", Username: "burst", Password: "test-only", IsActive: true, IsSuperuser: true})
	if err != nil {
		t.Fatal(err)
	}
	querier := NewSQLCRBACQuerierWithCache(q, NewRBACCache())
	start := make(chan struct{})
	before := database.Pool().Stat()
	tracedBefore := tracedQueryCount(t)
	started := time.Now()
	var wg sync.WaitGroup
	for range 64 {
		wg.Go(func() {
			<-start
			if _, err := querier.GetUserBindings(t.Context(), user.ID.String()); err != nil {
				t.Error(err)
			}
		})
	}
	close(start)
	wg.Wait()
	after := database.Pool().Stat()
	t.Logf("traced_queries=%d", tracedQueryCount(t)-tracedBefore)
	t.Logf("requests=64 max_conns=12 acquires=%d empty_acquires=%d cancelled_acquires=%d acquire_ms=%.3f elapsed_ms=%.3f", after.AcquireCount()-before.AcquireCount(), after.EmptyAcquireCount()-before.EmptyAcquireCount(), after.CanceledAcquireCount()-before.CanceledAcquireCount(), float64(after.AcquireDuration()-before.AcquireDuration())/float64(time.Millisecond), float64(time.Since(started))/float64(time.Millisecond))
	if n := after.AcquireCount() - before.AcquireCount(); n != 1 {
		t.Fatalf("cold burst used %d queries, want one", n)
	}
}

func tracedQueryCount(t *testing.T) uint64 {
	t.Helper()
	metrics, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	var total uint64
	for _, family := range metrics {
		if family.GetName() != "astronomer_db_query_duration_seconds" {
			continue
		}
		for _, metric := range family.Metric {
			total += metric.GetHistogram().GetSampleCount()
		}
	}
	return total
}
