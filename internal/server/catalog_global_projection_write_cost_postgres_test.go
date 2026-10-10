package server

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
)

// These actual write plans measure the read/write tradeoff; they are not a
// bounded-write claim and do not stand in for timing canonical migration080.
func TestCatalogGlobalProjectionPostgresBulkOwnerWriteCost(t *testing.T) {
	ctx, database, guard := complianceSnapshotOwnedDatabase(t)
	prefix := "w07-projection-write-" + uuid.NewString()
	cluster, project, repository := uuid.New(), uuid.New(), uuid.New()
	t.Cleanup(func() {
		if !guard.mayCleanup() {
			t.Error("unjoined projection write owner: exact rows retained")
			return
		}
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		commands := []struct {
			sql  string
			args []any
		}{
			{`DELETE FROM helm_charts WHERE repository_id=$1 AND name LIKE $2`, []any{repository, prefix + "-%"}},
			{`DELETE FROM helm_repositories WHERE id=$1 AND name=$2`, []any{repository, prefix}},
			{`DELETE FROM projects WHERE id=$1 AND cluster_id=$2 AND name=$3`, []any{project, cluster, prefix}},
			{`DELETE FROM clusters WHERE id=$1 AND name=$2`, []any{cluster, prefix}},
		}
		for _, command := range commands {
			if _, err := database.Pool().Exec(cleanup, command.sql, command.args...); err != nil {
				t.Error("exact projection write fixture cleanup failed")
			}
		}
	})
	for _, command := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO clusters(id,name,display_name) VALUES($1,$2,$2)`, []any{cluster, prefix}},
		{`INSERT INTO projects(id,name,display_name,cluster_id) VALUES($1,$2,$2,$3)`, []any{project, prefix, cluster}},
		{`INSERT INTO helm_repositories(id,name,url) VALUES($1,$2,'https://example.invalid/owned-write-cost')`, []any{repository, prefix}},
	} {
		if _, err := database.Pool().Exec(ctx, command.sql, command.args...); err != nil {
			t.Fatal("owned projection write fixture setup failed")
		}
	}
	ids := make([]uuid.UUID, 100000)
	for i := range ids {
		ids[i] = uuid.New()
	}
	measure := func(t *testing.T, phase, statement string, args ...any) {
		t.Helper()
		started := time.Now()
		var plan json.RawMessage
		if err := database.Pool().QueryRow(ctx, "EXPLAIN (ANALYZE, BUFFERS, WAL, FORMAT JSON) "+statement, args...).Scan(&plan); err != nil {
			t.Fatal("owned actual projection write plan failed", phase, err)
		}
		var nodes []struct {
			Plan struct {
				ActualRows  float64 `json:"Actual Rows"`
				ActualLoops float64 `json:"Actual Loops"`
			} `json:"Plan"`
			ExecutionTime *float64 `json:"Execution Time"`
			Triggers      []struct {
				Name  string  `json:"Trigger Name"`
				Time  float64 `json:"Time"`
				Calls float64 `json:"Calls"`
			} `json:"Triggers"`
		}
		if err := json.Unmarshal(plan, &nodes); err != nil || len(nodes) != 1 || nodes[0].ExecutionTime == nil || *nodes[0].ExecutionTime < 0 || len(nodes[0].Triggers) == 0 {
			t.Fatal("actual projection write plan lacks execution/trigger measurement")
		}
		expected := map[string]float64{"catalog_global_chart_key_after_owner": 1}
		if phase == "initial-trigger-insert" {
			expected = map[string]float64{"catalog_global_chart_key_lock_parents": 100000, "catalog_global_chart_key_after_chart": 100000}
		}
		seen := make(map[string]bool)
		for _, trigger := range nodes[0].Triggers {
			if calls, ok := expected[trigger.Name]; ok {
				if seen[trigger.Name] || trigger.Calls != calls || trigger.Time < 0 {
					t.Fatal("actual projection trigger count/time mismatch", phase, trigger.Name)
				}
				seen[trigger.Name] = true
			}
		}
		if len(seen) != len(expected) {
			t.Fatal("actual projection write plan omitted maintained trigger", phase)
		}
		t.Logf("owned projection write phase=%s population=100000 wall_ns=%d actual plan=%s", phase, time.Since(started).Nanoseconds(), plan)
	}
	t.Run("initial-trigger-insert", func(t *testing.T) {
		measure(t, "initial-trigger-insert", `INSERT INTO helm_charts(id,repository_id,name) SELECT chart_id,$1,$3||'-'||lpad(ordinality::text,6,'0') FROM unnest($2::uuid[]) WITH ORDINALITY AS source(chart_id,ordinality)`, repository, ids, prefix)
		var charts, keys int
		if err := database.Pool().QueryRow(ctx, `SELECT (SELECT count(*) FROM helm_charts WHERE repository_id=$1),(SELECT count(*) FROM catalog_global_chart_keys WHERE repository_id=$1)`, repository).Scan(&charts, &keys); err != nil || charts != 100000 || keys != 100000 {
			t.Fatalf("owned initial projection cardinality charts=%d keys=%d", charts, keys)
		}
	})
	if t.Failed() {
		return
	}
	t.Run("global-to-private", func(t *testing.T) {
		measure(t, "global-to-private", `UPDATE helm_repositories SET owner_project_id=$2 WHERE id=$1`, repository, project)
		var charts, keys int
		if err := database.Pool().QueryRow(ctx, `SELECT (SELECT count(*) FROM helm_charts WHERE repository_id=$1),(SELECT count(*) FROM catalog_global_chart_keys WHERE repository_id=$1)`, repository).Scan(&charts, &keys); err != nil || charts != 100000 || keys != 0 {
			t.Fatalf("owned private projection cardinality charts=%d keys=%d", charts, keys)
		}
	})
	if t.Failed() {
		return
	}
	t.Run("private-to-global", func(t *testing.T) {
		measure(t, "private-to-global", `UPDATE helm_repositories SET owner_project_id=NULL WHERE id=$1`, repository)
		var charts, keys, private, missing, stale int
		err := database.Pool().QueryRow(ctx, `SELECT
(SELECT count(*) FROM helm_charts WHERE repository_id=$1),
(SELECT count(*) FROM catalog_global_chart_keys WHERE repository_id=$1),
(SELECT count(*) FROM catalog_global_chart_keys k JOIN helm_repositories r ON r.id=k.repository_id WHERE k.repository_id=$1 AND r.owner_project_id IS NOT NULL),
(SELECT count(*) FROM helm_charts c WHERE c.repository_id=$1 AND NOT EXISTS(SELECT 1 FROM catalog_global_chart_keys k WHERE k.chart_id=c.id AND k.repository_id=c.repository_id AND k.name=c.name)),
(SELECT count(*) FROM catalog_global_chart_keys k LEFT JOIN helm_charts c ON c.id=k.chart_id WHERE k.repository_id=$1 AND (c.id IS NULL OR c.repository_id<>k.repository_id OR c.name<>k.name))`, repository).Scan(&charts, &keys, &private, &missing, &stale)
		if err != nil || charts != 100000 || keys != 100000 || private != 0 || missing != 0 || stale != 0 {
			t.Fatal(fmt.Sprintf("owned bulk backfill cardinality charts=%d keys=%d private=%d missing=%d stale=%d", charts, keys, private, missing, stale))
		}
	})
}
