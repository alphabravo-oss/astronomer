package server

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
)

type complianceSnapshotCleanupGuard struct {
	allowed    func() bool
	joinFailed bool
}

func (g *complianceSnapshotCleanupGuard) mayCleanup() bool {
	return !g.joinFailed && (g.allowed == nil || g.allowed())
}

// The qualifier authenticates the private DSN and exact lease metadata before
// injecting these literal expectations; this fixture never discovers a DB.
func complianceSnapshotOwnedDatabase(t *testing.T) (context.Context, *db.DB, *complianceSnapshotCleanupGuard) {
	t.Helper()
	dsn := os.Getenv("PAGINATION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PAGINATION_TEST_DATABASE_URL is not set")
	}
	expectedName := os.Getenv("COMPLIANCE_SNAPSHOT_TEST_DATABASE_NAME")
	major, err := strconv.Atoi(os.Getenv("COMPLIANCE_SNAPSHOT_TEST_DATABASE_MAJOR"))
	oid, oidErr := strconv.ParseUint(os.Getenv("COMPLIANCE_SNAPSHOT_TEST_DATABASE_OID"), 10, 32)
	lease, leaseErr := uuid.Parse(os.Getenv("COMPLIANCE_SNAPSHOT_TEST_DATABASE_LEASE"))
	source := os.Getenv("COMPLIANCE_SNAPSHOT_TEST_SOURCE")
	if err != nil || (major != 17 && major != 18) || oidErr != nil || oid == 0 || leaseErr != nil || lease == uuid.Nil || len(source) != 40 || strings.Trim(source, "0123456789abcdef") != "" || expectedName != fmt.Sprintf("plan032_w13w07_%d_%s", major, strings.ReplaceAll(lease.String(), "-", "")[:12]) {
		t.Fatal("owned compliance snapshot database expectations incomplete")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	database, err := db.ConnectWithConfig(ctx, dsn, db.PoolConfig{MaxConns: 4, MinConns: 1})
	if err != nil {
		t.Fatal("owned compliance snapshot connection failed")
	}
	cleanupGuard := &complianceSnapshotCleanupGuard{}
	t.Cleanup(func() {
		if cleanupGuard.mayCleanup() {
			database.Close()
		} else {
			t.Error("unjoined snapshot mutator: database handle retained without reuse")
		}
	})
	var name, owner, comment string
	var actualOID uint32
	var actualMajor int
	err = database.Pool().QueryRow(ctx, `SELECT current_database(),d.oid::bigint,pg_get_userbyid(d.datdba),shobj_description(d.oid,'pg_database'),current_setting('server_version_num')::int/10000 FROM pg_database d WHERE datname=current_database()`).Scan(&name, &actualOID, &owner, &comment, &actualMajor)
	wantComment := fmt.Sprintf("plan032-20261009 w13-w07 qualification lease %s schema%d source%s", lease.String(), db.ExpectedSchemaVersion, source[:8])
	if err != nil || name != expectedName || actualOID != uint32(oid) || owner != "astronomer" || comment != wantComment || actualMajor != major {
		t.Fatal("owned compliance snapshot database identity mismatch")
	}
	version, dirty, err := database.SchemaVersion(ctx)
	if err != nil || dirty || version != db.ExpectedSchemaVersion {
		t.Fatal("owned compliance snapshot schema mismatch")
	}
	var drills, projects int
	if err := database.Pool().QueryRow(ctx, `SELECT (SELECT count(*) FROM backup_drill_results),(SELECT count(*) FROM projects)`).Scan(&drills, &projects); err != nil || drills != 0 || projects != 0 {
		t.Fatal("owned snapshot fixture requires empty drill/project tables")
	}
	return ctx, database, cleanupGuard
}

func complianceSnapshotMutation(t *testing.T, ctx context.Context, database *db.DB, guard *complianceSnapshotCleanupGuard, change func(context.Context, pgx.Tx) error) (func() error, context.CancelFunc) {
	t.Helper()
	mutator, err := database.Pool().Acquire(ctx)
	if err != nil {
		t.Fatal("second snapshot connection unavailable")
	}
	mutationCtx, cancel := context.WithCancel(ctx)
	release, done := make(chan struct{}), make(chan struct{})
	var once sync.Once
	var result error
	guard.allowed = func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}
	t.Cleanup(func() {
		if guard.mayCleanup() {
			mutator.Release()
		} else {
			t.Error("unjoined snapshot mutator: acquired connection retained without reuse")
		}
	})
	// Install release/cancel/bounded join before launch, ahead of connection and
	// exact row cleanup in LIFO order. Timeout fails; it never claims a join.
	t.Cleanup(func() {
		once.Do(func() { close(release) })
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			guard.joinFailed = true
			t.Error("owned snapshot mutator did not join; row cleanup withheld")
		}
	})
	go func() {
		defer close(done)
		select {
		case <-release:
		case <-mutationCtx.Done():
			result = mutationCtx.Err()
			return
		}
		tx, err := mutator.Begin(mutationCtx)
		if err != nil {
			result = err
			return
		}
		defer func() {
			cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			_ = tx.Rollback(cleanup)
		}()
		if _, err = tx.Exec(mutationCtx, `SET LOCAL statement_timeout='5s'; SET LOCAL lock_timeout='3s'`); err != nil {
			result = err
			return
		}
		if err = change(mutationCtx, tx); err != nil {
			result = err
			return
		}
		result = tx.Commit(mutationCtx)
	}()
	return func() error {
		once.Do(func() { close(release) })
		select {
		case <-done:
			return result
		case <-ctx.Done():
			return ctx.Err()
		}
	}, cancel
}

func TestComplianceReadSnapshotPostgresDrillMutation(t *testing.T) {
	ctx, database, cleanupGuard := complianceSnapshotOwnedDatabase(t)
	prefix := "w07-snapshot-drill-" + uuid.NewString()
	ids := make([]uuid.UUID, 201)
	for i := range ids {
		ids[i] = uuid.New()
	}
	extra := uuid.New()
	all := append(append([]uuid.UUID{}, ids...), extra)
	t.Cleanup(func() {
		if !cleanupGuard.mayCleanup() {
			t.Error("unjoined drill mutator: owned rows retained")
			return
		}
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if _, err := database.Pool().Exec(cleanup, `DELETE FROM backup_drill_results WHERE id=ANY($1::uuid[]) AND backup_key=$2`, all, prefix); err != nil {
			t.Error("owned snapshot drill cleanup failed")
		}
	})
	at := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	if _, err := database.Pool().Exec(ctx, `INSERT INTO backup_drill_results(id,started_at,status,backup_key) SELECT id,$2,'success',$3 FROM unnest($1::uuid[]) AS id`, ids, at, prefix); err != nil {
		t.Fatal("owned snapshot drill insertion failed")
	}
	mutate, cancelMutation := complianceSnapshotMutation(t, ctx, database, cleanupGuard, func(mutationCtx context.Context, tx pgx.Tx) error {
		result, err := tx.Exec(mutationCtx, `DELETE FROM backup_drill_results WHERE id=$1 AND backup_key=$2`, ids[0], prefix)
		if err != nil || result.RowsAffected() != 1 {
			return errors.New("owned drill deletion failed")
		}
		_, err = tx.Exec(mutationCtx, `INSERT INTO backup_drill_results(id,started_at,status,backup_key) VALUES($1,$2,'success',$3)`, extra, at, prefix)
		return err
	})
	defer cancelMutation()
	var output bytes.Buffer
	err := complianceReadSnapshotRunner(database.Pool().BeginTx)(ctx, func(q handler.ComplianceQuerier) error {
		count, err := q.CountBackupDrillResults(ctx)
		if err != nil || count != 201 {
			return errors.New("owned drill initial count failed")
		}
		if err := mutate(); err != nil {
			return err
		}
		count, err = q.CountBackupDrillResults(ctx)
		if err != nil || count != 201 {
			return errors.New("snapshot drill count changed")
		}
		written, err := handler.WriteBackupDrillHistoryCSV(ctx, &output, q)
		if err != nil {
			return err
		}
		if written != 201 {
			return errors.New("snapshot drill traversal incomplete")
		}
		return nil
	})
	if err != nil {
		t.Fatal("owned drill snapshot mutation witness failed", err)
	}
	records, err := csv.NewReader(&output).ReadAll()
	if err != nil || len(records) != 202 {
		t.Fatal("snapshot drill CSV incomplete")
	}
	seen := map[uuid.UUID]bool{}
	for _, record := range records[1:] {
		id, err := uuid.Parse(record[0])
		if err != nil || seen[id] || id == extra {
			t.Fatal("snapshot drill CSV duplicated or included concurrent insertion")
		}
		seen[id] = true
	}
	for _, id := range ids {
		if !seen[id] {
			t.Fatal("snapshot drill CSV omitted concurrently deleted original")
		}
	}
	q := sqlc.New(database.Pool())
	live, err := q.ListBackupDrillResults(ctx, sqlc.ListBackupDrillResultsParams{Limit: 200, Offset: 0})
	if err != nil {
		t.Fatal(err)
	}
	var originalExists int
	if err := database.Pool().QueryRow(ctx, `SELECT count(*) FROM backup_drill_results WHERE id=$1`, ids[0]).Scan(&originalExists); err != nil || originalExists != 0 || len(live) != 200 {
		t.Fatal("second connection mutation was not committed outside snapshot")
	}
}

func TestComplianceReadSnapshotPostgresPolicyMemberMutation(t *testing.T) {
	ctx, database, cleanupGuard := complianceSnapshotOwnedDatabase(t)
	cluster, project, role, user := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	prefix := "w07-snapshot-policy-" + uuid.NewString()
	email := prefix + "@example.invalid"
	beforeName := prefix + "-before"
	afterName := prefix + "-after"
	ids := make([]uuid.UUID, 201)
	for i := range ids {
		ids[i] = uuid.New()
	}
	extra := uuid.New()
	all := append(append([]uuid.UUID{}, ids...), extra)
	t.Cleanup(func() {
		if !cleanupGuard.mayCleanup() {
			t.Error("unjoined policy mutator: owned rows retained")
			return
		}
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		commands := []struct {
			sql  string
			args []any
		}{
			{`DELETE FROM project_role_bindings WHERE id=ANY($1::uuid[]) AND project_id=$2 AND role_id=$3`, []any{all, project, role}},
			{`DELETE FROM projects WHERE id=$1 AND name=$2 AND cluster_id=$3`, []any{project, prefix, cluster}},
			{`DELETE FROM project_roles WHERE id=$1 AND name=$2`, []any{role, prefix}},
			{`DELETE FROM clusters WHERE id=$1 AND name=$2`, []any{cluster, prefix}},
			{`DELETE FROM users WHERE id=$1 AND email=$2 AND username IN($3,$4)`, []any{user, email, beforeName, afterName}},
		}
		for _, command := range commands {
			if _, err := database.Pool().Exec(cleanup, command.sql, command.args...); err != nil {
				t.Error("owned snapshot policy cleanup failed")
			}
		}
	})
	inserts := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO clusters(id,name,display_name) VALUES($1,$2,$2)`, []any{cluster, prefix}},
		{`INSERT INTO projects(id,name,display_name,cluster_id) VALUES($1,$2,$2,$3)`, []any{project, prefix, cluster}},
		{`INSERT INTO project_roles(id,name) VALUES($1,$2)`, []any{role, prefix}},
		{`INSERT INTO users(id,username,email) VALUES($1,$2,$3)`, []any{user, beforeName, email}},
	}
	for _, command := range inserts {
		if _, err := database.Pool().Exec(ctx, command.sql, command.args...); err != nil {
			t.Fatal("owned snapshot policy insertion failed")
		}
	}
	at := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	for i, id := range ids {
		var member any
		group := fmt.Sprintf("%s-%03d", prefix, i)
		if i == 0 {
			member = user
			group = ""
		}
		if _, err := database.Pool().Exec(ctx, `INSERT INTO project_role_bindings(id,project_id,role_id,user_id,"group",created_at) VALUES($1,$2,$3,$4,$5,$6)`, id, project, role, member, group, at); err != nil {
			t.Fatal("owned snapshot member insertion failed")
		}
	}
	mutate, cancelMutation := complianceSnapshotMutation(t, ctx, database, cleanupGuard, func(mutationCtx context.Context, tx pgx.Tx) error {
		result, err := tx.Exec(mutationCtx, `UPDATE users SET username=$2 WHERE id=$1 AND email=$3`, user, afterName, email)
		if err != nil || result.RowsAffected() != 1 {
			return errors.New("owned member rename failed")
		}
		result, err = tx.Exec(mutationCtx, `DELETE FROM project_role_bindings WHERE id=$1 AND project_id=$2 AND role_id=$3`, ids[200], project, role)
		if err != nil || result.RowsAffected() != 1 {
			return errors.New("owned member deletion failed")
		}
		_, err = tx.Exec(mutationCtx, `INSERT INTO project_role_bindings(id,project_id,role_id,"group",created_at) VALUES($1,$2,$3,$4,$5)`, extra, project, role, prefix+"-concurrent", at)
		return err
	})
	defer cancelMutation()
	var output bytes.Buffer
	err := complianceReadSnapshotRunner(database.Pool().BeginTx)(ctx, func(q handler.ComplianceQuerier) error {
		projects, err := q.ListAllProjectsForCompliance(ctx)
		if err != nil || len(projects) != 1 || projects[0].ID != project {
			return errors.New("owned policy initial project snapshot failed")
		}
		if err := mutate(); err != nil {
			return err
		}
		return handler.WritePolicySnapshotJSON(ctx, &output, q, q)
	})
	if err != nil {
		t.Fatal("owned policy snapshot mutation witness failed", err)
	}
	var entries []handler.PolicySnapshotEntry
	if err := json.Unmarshal(output.Bytes(), &entries); err != nil || len(entries) != 1 || len(entries[0].Members) != 201 {
		t.Fatal("owned policy snapshot document incomplete")
	}
	want := beforeName + " (" + email + ")"
	found := false
	seen := map[string]bool{}
	for _, member := range entries[0].Members {
		if seen[member] || strings.Contains(member, afterName) || member == "group:"+prefix+"-concurrent" {
			t.Fatal("snapshot members drifted or duplicated")
		}
		seen[member] = true
		if member == want {
			found = true
		}
	}
	if !found || !seen["group:"+prefix+"-200"] {
		t.Fatal("snapshot lost original user label or concurrently deleted member")
	}
	var liveName string
	if err := database.Pool().QueryRow(ctx, `SELECT username FROM users WHERE id=$1 AND email=$2`, user, email).Scan(&liveName); err != nil || liveName != afterName {
		t.Fatal("second connection member mutation did not commit")
	}
}

func TestComplianceReadSnapshotPostgresCanceledBlockedMutation(t *testing.T) {
	ctx, database, cleanupGuard := complianceSnapshotOwnedDatabase(t)
	id := uuid.New()
	prefix := "w07-snapshot-cancel-" + uuid.NewString()
	t.Cleanup(func() {
		if !cleanupGuard.mayCleanup() {
			t.Error("unjoined canceled mutator: owned row retained")
			return
		}
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if _, err := database.Pool().Exec(cleanup, `DELETE FROM backup_drill_results WHERE id=$1 AND backup_key=$2`, id, prefix); err != nil {
			t.Error("owned cancellation row cleanup failed")
		}
	})
	if _, err := database.Pool().Exec(ctx, `INSERT INTO backup_drill_results(id,started_at,status,backup_key) VALUES($1,$2,'success',$3)`, id, time.Now().UTC(), prefix); err != nil {
		t.Fatal("owned cancellation row insertion failed")
	}
	locker, err := database.Pool().Acquire(ctx)
	if err != nil {
		t.Fatal("owned blocking connection unavailable")
	}
	t.Cleanup(func() {
		if cleanupGuard.mayCleanup() {
			locker.Release()
		} else {
			t.Error("unjoined cancellation worker: blocker connection retained")
		}
	})
	lockTx, err := locker.Begin(ctx)
	if err != nil {
		t.Fatal("owned blocking transaction failed")
	}
	t.Cleanup(func() {
		if !cleanupGuard.mayCleanup() {
			t.Error("unjoined cancellation worker: blocker transaction retained")
			return
		}
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = lockTx.Rollback(cleanup)
	})
	var blockerPID int
	if err := lockTx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&blockerPID); err != nil {
		t.Fatal(err)
	}
	if _, err := lockTx.Exec(ctx, `SELECT id FROM backup_drill_results WHERE id=$1 AND backup_key=$2 FOR UPDATE`, id, prefix); err != nil {
		t.Fatal("owned blocking row lock failed")
	}
	workerPID := make(chan int, 1)
	mutate, cancelMutation := complianceSnapshotMutation(t, ctx, database, cleanupGuard, func(mutationCtx context.Context, tx pgx.Tx) error {
		var pid int
		if err := tx.QueryRow(mutationCtx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
			return err
		}
		workerPID <- pid
		_, err := tx.Exec(mutationCtx, `UPDATE backup_drill_results SET status='failed' WHERE id=$1 AND backup_key=$2`, id, prefix)
		return err
	})
	defer cancelMutation()
	observationCtx, stopObservation := context.WithTimeout(ctx, 5*time.Second)
	observationDone := make(chan struct{})
	var observationError error
	t.Cleanup(func() {
		stopObservation()
		select {
		case <-observationDone:
		case <-time.After(5 * time.Second):
			cleanupGuard.joinFailed = true
			t.Error("owned cancellation observer did not join; connection and row cleanup withheld")
		}
	})
	go func() {
		defer close(observationDone)
		defer cancelMutation()
		var pid int
		select {
		case pid = <-workerPID:
		case <-observationCtx.Done():
			observationError = observationCtx.Err()
			return
		}
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			var blocked bool
			err := database.Pool().QueryRow(observationCtx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND $2=ANY(pg_blocking_pids(pid)) AND query LIKE 'UPDATE backup_drill_results SET status=%')`, pid, blockerPID).Scan(&blocked)
			if err != nil {
				observationError = err
				return
			}
			if blocked {
				return
			}
			select {
			case <-ticker.C:
			case <-observationCtx.Done():
				observationError = observationCtx.Err()
				return
			}
		}
	}()
	err = mutate()
	select {
	case <-observationDone:
	case <-ctx.Done():
		t.Fatal("owned cancellation observer timed out")
	}
	if observationError != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("actual blocked mutation was not canceled and joined", observationError, err)
	}
	var status string
	if err := lockTx.QueryRow(ctx, `SELECT status FROM backup_drill_results WHERE id=$1 AND backup_key=$2`, id, prefix).Scan(&status); err != nil || status != "success" {
		t.Fatal("canceled mutation persisted a change before blocker released")
	}
}
