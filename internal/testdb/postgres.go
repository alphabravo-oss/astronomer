//go:build integration

package testdb

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log/slog"
	"io"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver pgtestdb connects with
	"github.com/peterldowns/pgtestdb"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/alphabravocompany/astronomer-go/internal/db/migrations"
)

// PostgresImage matches the production CloudNativePG minor (guide ch.07:
// "MUST match the production Postgres version to the minor").
const PostgresImage = "postgres:17.11-alpine"

const (
	user     = "astronomer"
	password = "astronomer"
	database = "astronomer"
)

var (
	once   sync.Once
	server pgtestdb.Config
	errRun error
)

// container starts the per-process Postgres container on first use. The
// container is reaped by testcontainers (Ryuk) when the test process exits.
func container(t testing.TB) pgtestdb.Config {
	t.Helper()
	once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		c, err := postgres.Run(ctx, PostgresImage,
			postgres.WithDatabase(database),
			postgres.WithUsername(user),
			postgres.WithPassword(password),
			postgres.BasicWaitStrategies(),
			// Throwaway data: trade durability for speed.
			testcontainers.WithCmdArgs("-c", "fsync=off", "-c", "synchronous_commit=off", "-c", "full_page_writes=off", "-c", "max_connections=400"),
		)
		if err != nil {
			errRun = fmt.Errorf("testdb: start %s: %w", PostgresImage, err)
			return
		}
		host, err := c.Host(ctx)
		if err != nil {
			errRun = fmt.Errorf("testdb: container host: %w", err)
			return
		}
		port, err := c.MappedPort(ctx, "5432/tcp")
		if err != nil {
			errRun = fmt.Errorf("testdb: container port: %w", err)
			return
		}
		server = pgtestdb.Config{
			DriverName:                "pgx",
			Host:                      host,
			Port:                      port.Port(),
			User:                      user,
			Password:                  password,
			Database:                  database,
			Options:                   "sslmode=disable",
			ForceTerminateConnections: true,
		}
	})
	if errRun != nil {
		t.Fatal(errRun)
	}
	return server
}

// migrator runs the production goose migrator (internal/db/migrations.Up)
// against the template database. pgtestdb keys templates on Hash, so the
// template is rebuilt only when the embedded migration set changes.
type migrator struct{}

func (migrator) Hash() (string, error) {
	h := sha256.New()
	entries, err := fs.ReadDir(migrations.FS(), ".")
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		body, err := fs.ReadFile(migrations.FS(), name)
		if err != nil {
			return "", err
		}
		_, _ = h.Write([]byte(name))
		_, _ = h.Write(body)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (migrator) Migrate(ctx context.Context, db *sql.DB, _ pgtestdb.Config) error {
	_, err := migrations.Up(ctx, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return err
}

// Config returns connection details for a fresh database cloned from the
// migrated template. The database is dropped when the test finishes.
func Config(t testing.TB) *pgtestdb.Config {
	t.Helper()
	return pgtestdb.Custom(t, container(t), migrator{})
}

// DSN returns the URL of a fresh, fully migrated database owned by the test.
func DSN(t testing.TB) string {
	t.Helper()
	return Config(t).URL()
}

// Pool opens a pgx pool on a fresh, fully migrated database. The pool is
// closed before the database is dropped.
func Pool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	return open(t, DSN(t))
}

// EmptyDSN returns the URL of a fresh database with NO migrations applied —
// for tests that exercise the migrator itself.
func EmptyDSN(t testing.TB) string {
	t.Helper()
	return pgtestdb.Custom(t, container(t), pgtestdb.NoopMigrator{}).URL()
}

func open(t testing.TB, dsn string) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("testdb: parse dsn: %v", err)
	}
	cfg.MaxConns = 8
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("testdb: open pool: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("testdb: ping: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}
