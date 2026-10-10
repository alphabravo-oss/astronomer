package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	appdb "github.com/alphabravocompany/astronomer-go/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// These fixtures clone migrated table definitions into a unique schema in an
// exclusively leased database. They never write public application rows.
type metricsPostgresFixture struct {
	reader, writer *pgx.Conn
	ctx            context.Context
}

func ownedMetricsPostgresFixture(t *testing.T, vault bool) *metricsPostgresFixture {
	t.Helper()
	path := os.Getenv("ASTRONOMER_METRICS_DATABASE_REF")
	if path == "" {
		t.Skip("run scripts/metrics-postgres-qualification.py with exclusively owned reference")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		t.Fatal("private regular metrics reference required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("read private metrics reference")
	}
	hash := sha256.Sum256(raw)
	if expected := os.Getenv("ASTRONOMER_METRICS_REFERENCE_SHA256"); expected == "" || expected != hex.EncodeToString(hash[:]) {
		t.Fatal("reference differs from immediately preceding live ownership fence")
	}
	major, err := strconv.Atoi(os.Getenv("ASTRONOMER_METRICS_POSTGRES_MAJOR"))
	if err != nil || (major != 17 && major != 18) {
		t.Fatal("explicit PostgreSQL17 or18 lane required")
	}
	var ref struct {
		URL      string `json:"database_url"`
		Database string `json:"database"`
		UID      string `json:"cluster_uid"`
		Primary  string `json:"primary_at_allocation"`
	}
	expectedUID := "ea0f410a-e1ea-4cb4-9fe5-40a35a020d3e"
	if major == 17 {
		expectedUID = "2f83aaff-dbbf-40ef-8880-542225fec0b4"
	}
	if json.Unmarshal(raw, &ref) != nil || ref.Database != fmt.Sprintf("plan032_w10_metrics%d", major) || ref.UID != expectedUID {
		t.Fatal("exclusive metrics lease required")
	}
	u, err := url.Parse(ref.URL)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Hostname() == "" || u.Path != "/"+ref.Database || u.Fragment != "" {
		t.Fatal("owned metrics URL identity required")
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		t.Fatal("invalid owned metrics query")
	}
	for key, values := range query {
		if len(values) != 1 || (key != "sslmode" && key != "sslrootcert" && key != "connect_timeout" && key != "application_name") {
			t.Fatal("owned metrics connection override rejected")
		}
	}
	cfg, err := pgx.ParseConfig(ref.URL)
	if err != nil {
		t.Fatal("parse owned metrics configuration")
	}
	port, err := strconv.ParseUint(u.Port(), 10, 16)
	if err != nil || cfg.Database != ref.Database || cfg.Host != u.Hostname() || cfg.Port != uint16(port) || cfg.RuntimeParams["options"] != "" {
		t.Fatal("parsed metrics connection differs from live-fenced endpoint")
	}
	cfg.RuntimeParams["application_name"] = "plan032-w10-metrics"
	cfg.ConnectTimeout = 5 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	t.Cleanup(cancel)
	reader, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal("connect owned metrics reader")
	}
	t.Cleanup(func() {
		closeCtx, stop := context.WithTimeout(context.Background(), 2*time.Second)
		defer stop()
		_ = reader.Close(closeCtx)
	})
	var name string
	var version, schema int64
	var dirty bool
	if reader.QueryRow(ctx, `SELECT current_database(), current_setting('server_version_num')::bigint`).Scan(&name, &version) != nil || name != ref.Database || version/10000 != int64(major) {
		t.Fatal("actual owned PostgreSQL major required before fixture writes")
	}
	if reader.QueryRow(ctx, `SELECT version, dirty FROM public.schema_migrations ORDER BY version DESC LIMIT 1`).Scan(&schema, &dirty) != nil || dirty || schema != appdb.ExpectedSchemaVersion {
		t.Fatal("exact current migrated schema required before fixture writes")
	}
	writer, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal("connect owned metrics writer")
	}
	t.Cleanup(func() {
		closeCtx, stop := context.WithTimeout(context.Background(), 2*time.Second)
		defer stop()
		_ = writer.Close(closeCtx)
	})
	fixtureSchema := "plan032_w10_metrics_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quoted := pgx.Identifier{fixtureSchema}.Sanitize()
	if _, err := writer.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal("create unique metrics fixture schema")
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if _, err := writer.Exec(cleanup, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error("remove only owned metrics fixture schema")
		}
	})
	tables := []string{"clusters", "agent_connections"}
	if vault {
		tables = []string{"vault_connections"}
	}
	for _, table := range tables {
		ident := pgx.Identifier{table}.Sanitize()
		if _, err := writer.Exec(ctx, "CREATE TABLE "+quoted+"."+ident+" (LIKE public."+ident+" INCLUDING ALL)"); err != nil {
			t.Fatal("clone canonical migrated metrics table definition")
		}
	}
	for _, conn := range []*pgx.Conn{reader, writer} {
		if _, err := conn.Exec(ctx, "SET search_path TO "+quoted+", public"); err != nil {
			t.Fatal("bind only owned fixture tables")
		}
	}
	t.Logf("owned metrics PostgreSQL major=%d server_version_num=%d schema=%d; unique schema, canonical queries", major, version, schema)
	return &metricsPostgresFixture{reader: reader, writer: writer, ctx: ctx}
}

func metricsFixtureID(index int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("00000000-0000-0000-0000-%012d", index))
}

func (f *metricsPostgresFixture) seed(t *testing.T, vault bool) {
	t.Helper()
	for i := 1; i <= 501; i++ {
		var err error
		if vault {
			_, err = f.writer.Exec(f.ctx, `INSERT INTO vault_connections(id,name,addr,auth_method) VALUES($1,$2,'https://vault.example.invalid','token')`, metricsFixtureID(i), fmt.Sprintf("owned-vault-%05d", i))
		} else {
			_, err = f.writer.Exec(f.ctx, `INSERT INTO clusters(id,name,display_name,api_server_url,ca_certificate,created_at,registration_phase) VALUES($1,$2,$2,'https://cluster.example.invalid','',TIMESTAMPTZ '2026-01-01 00:00:00Z','created')`, metricsFixtureID(i), fmt.Sprintf("owned-cluster-%05d", i))
		}
		if err != nil {
			t.Fatal("seed only owned metrics fixture rows")
		}
	}
}

func (f *metricsPostgresFixture) mutate(ctx context.Context, vault bool) error {
	tx, err := f.writer.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 2*time.Second)
		defer stop()
		_ = tx.Rollback(cleanup)
	}()
	if vault {
		if _, err := tx.Exec(ctx, `DELETE FROM vault_connections WHERE id=$1`, metricsFixtureID(501)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE vault_connections SET name=name||'-new'`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO vault_connections(id,name,addr,auth_method) VALUES($1,'owned-vault-00502-new','https://vault.example.invalid','token')`, metricsFixtureID(502)); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec(ctx, `DELETE FROM clusters WHERE id=$1`, metricsFixtureID(501)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE clusters SET name=name||'-new',registration_phase='ready'`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO clusters(id,name,display_name,api_server_url,ca_certificate,created_at,registration_phase) VALUES($1,'owned-cluster-00502-new','owned-new','https://cluster.example.invalid','',TIMESTAMPTZ '2026-01-01 00:00:00Z','ready')`, metricsFixtureID(502)); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

type metricsInterleavingTx struct {
	pgx.Tx
	queries  int
	onSecond func(context.Context) error
}

func (tx *metricsInterleavingTx) Query(ctx context.Context, query string, args ...any) (pgx.Rows, error) {
	tx.queries++
	if tx.queries == 2 && tx.onSecond != nil {
		if err := tx.onSecond(ctx); err != nil {
			return nil, err
		}
	}
	return tx.Tx.Query(ctx, query, args...)
}

func (f *metricsPostgresFixture) begin(t *testing.T, vault bool) (connectionMetricsBegin, *int) {
	t.Helper()
	calls := new(int)
	return func(ctx context.Context, options pgx.TxOptions) (pgx.Tx, error) {
		tx, err := f.reader.BeginTx(ctx, options)
		if err != nil {
			return nil, err
		}
		var isolation, readOnly string
		if tx.QueryRow(ctx, `SELECT current_setting('transaction_isolation'), current_setting('transaction_read_only')`).Scan(&isolation, &readOnly) != nil || isolation != "repeatable read" || readOnly != "on" {
			_ = tx.Rollback(ctx)
			t.Fatal("actual transaction is not read-only repeatable-read")
		}
		wrapped := &metricsInterleavingTx{Tx: tx, onSecond: func(ctx context.Context) error { *calls += 1; return f.mutate(ctx, vault) }}
		return wrapped, nil
	}, calls
}

func assertMetricsSnapshotIdentity(t *testing.T, ids []uuid.UUID, fresh bool) {
	t.Helper()
	if len(ids) != 501 {
		t.Fatalf("complete metrics rows=%d want501", len(ids))
	}
	seen := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			t.Fatal("duplicate metrics identity across pages")
		}
		seen[id] = true
	}
	for i := 1; i <= 502; i++ {
		want := i <= 501
		if fresh {
			want = i != 501
		}
		if seen[metricsFixtureID(i)] != want {
			t.Fatalf("unexpected identity at index%d fresh%t", i, fresh)
		}
	}
}

func TestPostgresConnectionMetricsRepeatableReadAcrossMutation(t *testing.T) {
	f := ownedMetricsPostgresFixture(t, false)
	f.seed(t, false)
	begin, mutations := f.begin(t, false)
	rows, err := collectConnectionMetricsSnapshot(f.ctx, begin)
	if err != nil {
		t.Fatal("collect actual canonical connection snapshot")
	}
	ids := make([]uuid.UUID, len(rows))
	for i, row := range rows {
		ids[i] = row.ClusterID
		if strings.HasSuffix(row.ClusterName, "-new") || row.Status != "never" {
			t.Fatal("connection snapshot mixed committed pages")
		}
	}
	assertMetricsSnapshotIdentity(t, ids, false)
	if *mutations != 1 {
		t.Fatal("concurrent mutation did not execute exactly once between pages")
	}
	rows, err = collectConnectionMetricsSnapshot(f.ctx, f.reader.BeginTx)
	if err != nil {
		t.Fatal("collect subsequent connection snapshot")
	}
	if len(rows) != len(ids) {
		t.Fatal("subsequent snapshot did not return the complete501-row set")
	}
	for i, row := range rows {
		ids[i] = row.ClusterID
		if !strings.HasSuffix(row.ClusterName, "-new") {
			t.Fatal("new snapshot did not observe committed names")
		}
	}
	assertMetricsSnapshotIdentity(t, ids, true)
}

func TestPostgresRegistrationMetricsRepeatableReadAcrossMutation(t *testing.T) {
	f := ownedMetricsPostgresFixture(t, false)
	f.seed(t, false)
	begin, mutations := f.begin(t, false)
	rows, err := collectRegistrationMetricsSnapshot(f.ctx, begin)
	if err != nil {
		t.Fatal("collect actual canonical phase snapshot")
	}
	ids := make([]uuid.UUID, len(rows))
	for i, row := range rows {
		ids[i] = uuid.MustParse(row.ClusterID)
		if row.Phase != "created" {
			t.Fatal("phase snapshot mixed committed pages")
		}
	}
	assertMetricsSnapshotIdentity(t, ids, false)
	if *mutations != 1 {
		t.Fatal("concurrent mutation did not execute exactly once between pages")
	}
	rows, err = collectRegistrationMetricsSnapshot(f.ctx, f.reader.BeginTx)
	if err != nil {
		t.Fatal("collect subsequent phase snapshot")
	}
	if len(rows) != len(ids) {
		t.Fatal("subsequent snapshot did not return the complete501-row set")
	}
	for i, row := range rows {
		ids[i] = uuid.MustParse(row.ClusterID)
		if row.Phase != "ready" {
			t.Fatal("new snapshot did not observe committed phases")
		}
	}
	assertMetricsSnapshotIdentity(t, ids, true)
}

func TestPostgresVaultMetricsRepeatableReadAcrossMutation(t *testing.T) {
	f := ownedMetricsPostgresFixture(t, true)
	f.seed(t, true)
	begin, mutations := f.begin(t, true)
	rows, err := collectVaultMetricsSnapshot(f.ctx, begin)
	if err != nil {
		t.Fatal("collect actual credential-free Vault snapshot")
	}
	ids := make([]uuid.UUID, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
		if strings.HasSuffix(row.Name, "-new") {
			t.Fatal("Vault snapshot mixed committed pages")
		}
	}
	assertMetricsSnapshotIdentity(t, ids, false)
	if *mutations != 1 {
		t.Fatal("concurrent mutation did not execute exactly once between pages")
	}
	rows, err = collectVaultMetricsSnapshot(f.ctx, f.reader.BeginTx)
	if err != nil {
		t.Fatal("collect subsequent Vault snapshot")
	}
	if len(rows) != len(ids) {
		t.Fatal("subsequent snapshot did not return the complete501-row set")
	}
	for i, row := range rows {
		ids[i] = row.ID
		if !strings.HasSuffix(row.Name, "-new") {
			t.Fatal("new snapshot did not observe committed names")
		}
	}
	assertMetricsSnapshotIdentity(t, ids, true)
}
