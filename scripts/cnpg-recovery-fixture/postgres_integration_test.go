//go:build integration

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"github.com/alphabravocompany/astronomer-go/internal/db"
	"github.com/jackc/pgx/v5/pgconn"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This test requires an empty dedicated, fully migrated disposable database.
// The runtime qualification invokes it explicitly; unavailable prerequisites
// are a failure rather than a skipped qualification case.
func TestPostgresRecoveryFixtureProductionReaders(t *testing.T) {
	ref := os.Getenv("ASTRONOMER_RECOVERY_TEST_DATABASE_REF")
	if ref == "" {
		t.Fatal("owned database reference is required for this integration case")
	}
	url, name, err := databaseURL(ref)
	if err != nil {
		t.Fatal("reject database reference")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	connection, err := db.ConnectWithConfig(ctx, url, db.PoolConfig{MaxConns: 2, MinConns: 1})
	if err != nil {
		t.Fatal("connect owned database")
	}
	defer connection.Close()
	var actual string
	if connection.Pool().QueryRow(ctx, "SELECT current_database()").Scan(&actual) != nil || actual != name {
		t.Fatal("owned database identity differs")
	}
	if err := connection.SchemaHealth(ctx); err != nil {
		t.Fatal("full migration prerequisite missing")
	}
	path := filepath.Join(t.TempDir(), "protected-keys.json")
	m, err := seed(ctx, connection.Pool(), path, strings.Repeat("a", 40))
	if err != nil {
		var pgerr *pgconn.PgError
		if errors.As(err, &pgerr) {
			t.Fatalf("seed rejected SQLSTATE=%s table=%s constraint=%s", pgerr.Code, pgerr.TableName, pgerr.ConstraintName)
		}
		t.Fatal("seed production fixture failed")
	}
	serialized, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal("serialize public recovery manifest")
	}
	var decoded manifest
	if err := decodeClosed(serialized, &decoded); err != nil {
		t.Fatal("public recovery manifest cannot round trip")
	}
	m = decoded
	manifestPath := filepath.Join(t.TempDir(), "public-manifest.json")
	if os.WriteFile(manifestPath, serialized, 0o600) != nil {
		t.Fatal("write public manifest")
	}
	previousArgs, previousFlags := os.Args, flag.CommandLine
	defer func() { os.Args, flag.CommandLine = previousArgs, previousFlags }()
	os.Args = []string{"recovery-fixture", "--mode", "verify", "--database-ref", ref, "--key-ref", path, "--manifest", manifestPath, "--source-commit", m.SourceCommit}
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	if err := run(); err != nil {
		t.Fatalf("CLI verification failed: %s", err.Error())
	}
	if err := verify(ctx, connection.Pool(), path, "", m); err != nil {
		t.Fatalf("verify failed: %s", err.Error())
	}
	for _, negative := range []string{"missing-old", "wrong"} {
		if verify(ctx, connection.Pool(), path, negative, m) == nil {
			t.Fatal("negative credential restoration accepted")
		}
	}
	if _, err := connection.Pool().Exec(ctx, "UPDATE cluster_deployments SET observed_generation=7 WHERE id=$1", m.Tenants[0].Deployment); err != nil {
		t.Fatal("mutate owned generation fixture")
	}
	if verify(ctx, connection.Pool(), path, "", m) == nil {
		t.Fatal("changed restored generation accepted")
	}
}
