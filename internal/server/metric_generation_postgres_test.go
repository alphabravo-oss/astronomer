package server

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type generationDomainFixture struct{ table, family, insert, ordinaryUpdate string }

var generationDomains = []generationDomainFixture{
	{"sso_configurations", "sso_provider", "INSERT INTO sso_configurations(id,provider) VALUES($1,'owned-oidc')", "UPDATE sso_configurations SET display_name='renamed',is_enabled=false WHERE id=$1"},
	{"siem_forwarders", "siem_forwarder", "INSERT INTO siem_forwarders(id,name,transport,endpoint) VALUES($1,'owned-siem','ndjson_https','https://receiver.invalid')", "UPDATE siem_forwarders SET name='renamed',enabled=false WHERE id=$1"},
	{"cluster_groups", "cluster_group", "INSERT INTO cluster_groups(id,name,slug) VALUES($1,'owned-group','owned-group')", "UPDATE cluster_groups SET name='renamed',slug='renamed',enabled=false WHERE id=$1"},
	{"clusters", "cluster", "INSERT INTO clusters(id,name,display_name) VALUES($1,'owned-cluster','owned-cluster')", "UPDATE clusters SET name='renamed',display_name='renamed',status='disconnected' WHERE id=$1"},
}

func generationPostgresFixture(t *testing.T) *metricsPostgresFixture {
	t.Helper()
	f := ownedMetricsPostgresFixture(t, false)
	var schema string
	if f.writer.QueryRow(f.ctx, "SELECT current_schema()").Scan(&schema) != nil || !strings.HasPrefix(schema, "plan032_w10_metrics_") {
		t.Fatal("owned fixture schema binding required")
	}
	quoted := pgx.Identifier{schema}.Sanitize()
	for _, table := range []string{"sso_configurations", "siem_forwarders", "cluster_groups"} {
		ident := pgx.Identifier{table}.Sanitize()
		if _, err := f.writer.Exec(f.ctx, "CREATE TABLE "+quoted+"."+ident+" (LIKE public."+ident+" INCLUDING ALL)"); err != nil {
			t.Fatal("clone only owned canonical generation table")
		}
	}
	// Seed before hooks/backfill so this tests migration backfill as well as
	// subsequent old-style domain writers, without writing public application rows.
	for _, domain := range generationDomains {
		if _, err := f.writer.Exec(f.ctx, domain.insert, metricsFixtureID(1)); err != nil {
			t.Fatal("seed only owned pre-migration rows")
		}
	}
	raw, err := os.ReadFile("../db/migrations/078_metric_owner_generations.up.sql")
	if err != nil {
		t.Fatal("read canonical migration078")
	}
	// Exact canonical SQL with only its schema qualification replaced by this
	// uniquely owned namespace. This is trigger behavior, not full deployment DDL.
	statement := strings.ReplaceAll(string(raw), "public.", quoted+".")
	if _, err := f.writer.Exec(f.ctx, statement); err != nil {
		t.Fatal("install exact generation migration in owned schema")
	}
	return f
}

func TestPostgresMetricGenerationLifecycleAndTransactionRollback(t *testing.T) {
	f := generationPostgresFixture(t)
	queries := sqlc.New(f.writer)
	for _, domain := range generationDomains {
		t.Run(domain.family, func(t *testing.T) {
			id := metricsFixtureID(1)
			get := func() uuid.UUID {
				t.Helper()
				value, err := queries.GetMetricOwnerGeneration(f.ctx, sqlc.GetMetricOwnerGenerationParams{Family: domain.family, OwnerID: id})
				if err != nil || value == uuid.Nil {
					t.Fatal("current owner generation missing")
				}
				return value
			}
			original := get()
			if _, err := f.writer.Exec(f.ctx, domain.ordinaryUpdate, id); err != nil || get() != original {
				t.Fatal("rename/disable changed generation")
			}
			tx, err := f.writer.Begin(f.ctx)
			if err != nil {
				t.Fatal("begin owned rollback fixture")
			}
			defer func() {
				cleanup, stop := context.WithTimeout(context.WithoutCancel(f.ctx), 2*time.Second)
				defer stop()
				_ = tx.Rollback(cleanup)
			}()
			if _, err := tx.Exec(f.ctx, "DELETE FROM "+pgx.Identifier{domain.table}.Sanitize()+" WHERE id=$1", id); err != nil {
				t.Fatal("delete owned domain row")
			}
			if _, err := tx.Exec(f.ctx, domain.insert, id); err != nil {
				t.Fatal("recreate owned ID")
			}
			changed, err := sqlc.New(tx).GetMetricOwnerGeneration(f.ctx, sqlc.GetMetricOwnerGenerationParams{Family: domain.family, OwnerID: id})
			if err != nil || changed == original || changed == uuid.Nil {
				t.Fatal("recreated ID did not obtain a new generation")
			}
			if err := tx.Rollback(f.ctx); err != nil || get() != original {
				t.Fatal("rolled-back domain mutation changed ownership")
			}
			if domain.family == "cluster" {
				if _, err := f.writer.Exec(f.ctx, "UPDATE clusters SET decommissioned_at=now() WHERE id=$1", id); err != nil {
					t.Fatal("decommission owned fixture row")
				}
				if _, err := queries.GetMetricOwnerGeneration(f.ctx, sqlc.GetMetricOwnerGenerationParams{Family: domain.family, OwnerID: id}); err == nil {
					t.Fatal("tombstone remained current metric owner")
				}
				if _, err := f.writer.Exec(f.ctx, "UPDATE clusters SET decommissioned_at=NULL WHERE id=$1", id); err != nil || get() != original {
					t.Fatal("ordinary state edit rotated the durable token")
				}
			}
			if _, err := f.writer.Exec(f.ctx, "UPDATE "+pgx.Identifier{domain.table}.Sanitize()+" SET id=$2 WHERE id=$1", id, metricsFixtureID(2)); err != nil {
				t.Fatal("owned ID change failed")
			}
			if _, err := queries.GetMetricOwnerGeneration(f.ctx, sqlc.GetMetricOwnerGenerationParams{Family: domain.family, OwnerID: id}); err == nil {
				t.Fatal("ID change retained previous registry owner")
			}
			child := pgx.Identifier{"owned_child_" + domain.family}.Sanitize()
			table := pgx.Identifier{domain.table}.Sanitize()
			if _, err := f.writer.Exec(f.ctx, "CREATE TABLE "+child+"(parent uuid REFERENCES "+table+"(id))"); err != nil {
				t.Fatal("create owned cascading fixture")
			}
			if _, err := f.writer.Exec(f.ctx, "INSERT INTO "+child+"(parent) VALUES($1)", metricsFixtureID(2)); err != nil {
				t.Fatal("insert owned cascading fixture")
			}
			if _, err := f.writer.Exec(f.ctx, "TRUNCATE "+table+" CASCADE"); err != nil {
				t.Fatal("truncate only owned fixture table")
			}
			if _, err := queries.GetMetricOwnerGeneration(f.ctx, sqlc.GetMetricOwnerGenerationParams{Family: domain.family, OwnerID: metricsFixtureID(2)}); err == nil {
				t.Fatal("truncate left historical metric owners")
			}
			var children int64
			if f.writer.QueryRow(f.ctx, "SELECT count(*) FROM "+child).Scan(&children) != nil || children != 0 {
				t.Fatal("owned TRUNCATE CASCADE did not clear dependent rows")
			}
		})
	}
}

func TestPostgresMetricGenerationCapturedCallbackRejectsRecreatedID(t *testing.T) {
	f := generationPostgresFixture(t)
	previous := observability.InstanceID()
	observability.SetInstanceID(t.Name())
	t.Cleanup(func() { observability.SetInstanceID(previous) })
	queries := sqlc.New(f.reader)
	id := metricsFixtureID(1)
	old := observability.CaptureMetricGeneration(f.ctx, queries, observability.MetricSIEMForwarder, id)
	calls := 0
	if !old.Record(f.ctx, func(string, string) { calls++ }) {
		t.Fatal("current actual database callback failed")
	}
	if _, err := f.writer.Exec(f.ctx, "DELETE FROM siem_forwarders WHERE id=$1", id); err != nil {
		t.Fatal("delete owned fixture owner")
	}
	if _, err := f.writer.Exec(f.ctx, generationDomains[1].insert, id); err != nil {
		t.Fatal("recreate owned fixture owner")
	}
	if old.Record(f.ctx, func(string, string) { calls++ }) {
		t.Fatal("old actual database generation resurrected")
	}
	fresh := observability.CaptureMetricGeneration(f.ctx, queries, observability.MetricSIEMForwarder, id)
	if !fresh.Record(f.ctx, func(string, string) { calls++ }) || calls != 2 {
		t.Fatal("fresh generation failed exact callback count")
	}
	if !observability.BeginMetricGenerationLookup(observability.MetricSIEMForwarder).Publish(nil, time.Now()) {
		t.Fatal("owned test telemetry cleanup failed")
	}
}

func TestPostgresMetricGenerationRepeatableReadAcrossPages(t *testing.T) {
	f := generationPostgresFixture(t)
	// Add500 rows to the one backfilled cluster owner for an actual501-row page boundary.
	for i := 2; i <= 501; i++ {
		if _, err := f.writer.Exec(f.ctx, "INSERT INTO clusters(id,name,display_name) VALUES($1,$2,$2)", metricsFixtureID(i), "owned-generation-"+metricsFixtureID(i).String()); err != nil {
			t.Fatal("seed owned generation boundary")
		}
	}
	mutations := 0
	begin := func(ctx context.Context, options pgx.TxOptions) (pgx.Tx, error) {
		tx, err := f.reader.BeginTx(ctx, options)
		if err != nil {
			return nil, err
		}
		var isolation, readOnly string
		if tx.QueryRow(ctx, "SELECT current_setting('transaction_isolation'),current_setting('transaction_read_only')").Scan(&isolation, &readOnly) != nil || isolation != "repeatable read" || readOnly != "on" {
			_ = tx.Rollback(ctx)
			t.Fatal("actual snapshot transaction settings differ")
		}
		return &metricsInterleavingTx{Tx: tx, onSecond: func(ctx context.Context) error {
			mutations++
			_, err := f.writer.Exec(ctx, "DELETE FROM clusters WHERE id=$1", metricsFixtureID(501))
			return err
		}}, nil
	}
	rows, err := observability.CollectMetricGenerationSnapshot(f.ctx, begin, observability.MetricCluster)
	if err != nil || len(rows) != 501 || mutations != 1 || rows[500].ID != metricsFixtureID(501) {
		t.Fatal("generation observation mixed actual committed pages")
	}
	rows, err = observability.CollectMetricGenerationSnapshot(f.ctx, f.reader.BeginTx, observability.MetricCluster)
	if err != nil || len(rows) != 500 {
		t.Fatal("subsequent generation observation did not see committed deletion")
	}
}
