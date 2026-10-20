//go:build integration

package db_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db"
	"github.com/alphabravocompany/astronomer-go/internal/testdb"
)

// TestMigrateDoesNotLeakSessionStateIntoPool is the regression for the
// boot-time defect found on the live proof: 00001 sets search_path=” for its
// session, and running migrations through the application pool left a pooled
// connection unable to resolve unqualified table names (SQLSTATE 42P01).
func TestMigrateDoesNotLeakSessionStateIntoPool(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	database, err := db.ConnectWithConfig(ctx, testdb.EmptyDSN(t), db.PoolConfig{MaxConns: 4, MinConns: 4})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.Close)
	res, err := database.Migrate(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if res.Applied == 0 || res.Version != db.ExpectedSchemaVersion {
		t.Fatalf("Migrate = %+v, want version %d applied", res, db.ExpectedSchemaVersion)
	}
	// Hold every pooled connection at once and run an unqualified query on each.
	conns := make([]interface{ Release() }, 0, 4)
	for range 4 {
		c, err := database.Pool().Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var n int
		if err := c.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
			t.Fatalf("unqualified query on pooled connection after Migrate: %v", err)
		}
		conns = append(conns, c)
	}
	for _, c := range conns {
		c.Release()
	}
	if err := database.SchemaHealth(ctx); err != nil {
		t.Fatal(err)
	}
}
