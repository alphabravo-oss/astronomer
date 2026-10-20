//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/migrations"
	"github.com/alphabravocompany/astronomer-go/internal/testdb"
)

// TestUpSerializesConcurrentBoots is the item-2 proof: two server processes
// booting at once against an empty database must serialize on the advisory
// lock so that exactly one of them applies the set and both observe Latest.
func TestUpSerializesConcurrentBoots(t *testing.T) {
	t.Parallel()
	dsn := testdb.EmptyDSN(t)
	const boots = 2
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	results := make([]migrations.Result, boots)
	errs := make([]error, boots)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range boots {
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			results[i], errs[i] = migrations.Up(ctx, db, logger)
		}()
	}
	close(start)
	wg.Wait()

	applied := 0
	for i := range boots {
		if errs[i] != nil {
			t.Fatalf("boot %d: %v", i, errs[i])
		}
		if results[i].Version != migrations.Latest() {
			t.Fatalf("boot %d: version %d, want %d", i, results[i].Version, migrations.Latest())
		}
		if results[i].Applied > 0 {
			applied++
			if results[i].Applied != len(migrations.Versions()) {
				t.Fatalf("boot %d applied %d migrations, want all %d", i, results[i].Applied, len(migrations.Versions()))
			}
		}
	}
	if applied != 1 {
		t.Fatalf("%d boots applied migrations, want exactly 1", applied)
	}
}

// TestUpIsIdempotent: a second run on a migrated database applies nothing.
func TestUpIsIdempotent(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("pgx", testdb.DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	res, err := migrations.Up(context.Background(), db, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Applied != 0 || res.Version != migrations.Latest() {
		t.Fatalf("second Up = %+v, want applied=0 version=%d", res, migrations.Latest())
	}
	version, err := migrations.Version(context.Background(), db)
	if err != nil || version != migrations.Latest() {
		t.Fatalf("Version = %d, %v; want %d", version, err, migrations.Latest())
	}
}
