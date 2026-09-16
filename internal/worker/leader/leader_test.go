package leader

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDedicatedConfigIsIsolatedAndBounded(t *testing.T) {
	source, err := pgxpool.ParseConfig("postgres://operator:secret@db.example/astronomer")
	if err != nil {
		t.Fatalf("parse source config: %v", err)
	}
	source.MinConns = 5
	source.MaxConns = 25

	dedicated := dedicatedConfig(source)
	if dedicated == source {
		t.Fatal("dedicated config aliases source")
	}
	if dedicated.MinConns != 0 {
		t.Fatalf("MinConns = %d, want 0", dedicated.MinConns)
	}
	if dedicated.MaxConns != dedicatedPoolMaxConns {
		t.Fatalf("MaxConns = %d, want %d", dedicated.MaxConns, dedicatedPoolMaxConns)
	}
	if source.MinConns != 5 || source.MaxConns != 25 {
		t.Fatalf("source config mutated: min=%d max=%d", source.MinConns, source.MaxConns)
	}
}

func TestElectorCloseDoesNotCloseBorrowedPool(t *testing.T) {
	config, err := pgxpool.ParseConfig("postgres://operator:secret@db.example/astronomer")
	if err != nil {
		t.Fatalf("parse pool config: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatalf("create lazy pool: %v", err)
	}
	defer pool.Close()

	elector := New(pool, nil)
	elector.Close()
	if pool.Stat().MaxConns() == 0 {
		t.Fatal("borrowed pool was closed")
	}
}
