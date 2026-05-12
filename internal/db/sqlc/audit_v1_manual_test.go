package sqlc

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// fakeDBTX captures the SQL string and the args passed to Exec. The Query
// / QueryRow methods are unused for the batch-insert tests but must be
// present to satisfy the DBTX interface.
type fakeDBTX struct {
	lastSQL  string
	lastArgs []any
	execErr  error
}

func (f *fakeDBTX) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	f.lastSQL = sql
	f.lastArgs = args
	return pgconn.CommandTag{}, f.execErr
}

func (f *fakeDBTX) Query(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
	return nil, nil
}

func (f *fakeDBTX) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	return nil
}

func TestBatchInsertAuditLog_BuildsSingleMultiRowInsert(t *testing.T) {
	db := &fakeDBTX{}
	q := New(db)

	const n = 7
	rows := make([]CreateAuditLogV1Params, n)
	for i := 0; i < n; i++ {
		rows[i] = CreateAuditLogV1Params{
			Source: "test",
			Action: "test.action",
			Detail: json.RawMessage(`{}`),
		}
	}

	if err := q.BatchInsertAuditLog(context.Background(), rows); err != nil {
		t.Fatalf("BatchInsertAuditLog: %v", err)
	}

	// One Exec call should have been issued (a single multi-row VALUES list).
	if !strings.Contains(db.lastSQL, "INSERT INTO audit_log") {
		t.Fatalf("SQL missing INSERT prefix:\n%s", db.lastSQL)
	}
	// 7 rows * 16 columns = 112 bind params total.
	if got := strings.Count(db.lastSQL, "$"); got != n*auditLogColumnsPerRow {
		t.Fatalf("bind placeholder count = %d, want %d", got, n*auditLogColumnsPerRow)
	}
	if got := len(db.lastArgs); got != n*auditLogColumnsPerRow {
		t.Fatalf("Exec args = %d, want %d", got, n*auditLogColumnsPerRow)
	}
	// Confirm we emitted exactly n VALUES tuples. The query opens one
	// paren for the column list + one for each tuple, so total open
	// parens = n+1.
	if got := strings.Count(db.lastSQL, "("); got != n+1 {
		t.Fatalf("VALUES tuple count = %d, want %d (n+1 incl. column list)", got, n+1)
	}
}

func TestBatchInsertAuditLog_EmptyIsNoop(t *testing.T) {
	db := &fakeDBTX{}
	q := New(db)
	if err := q.BatchInsertAuditLog(context.Background(), nil); err != nil {
		t.Fatalf("BatchInsertAuditLog(nil) = %v", err)
	}
	if db.lastSQL != "" {
		t.Fatalf("empty batch should not issue Exec; got SQL: %s", db.lastSQL)
	}
}

func TestBatchInsertAuditLog_PreservesRowOrderInArgs(t *testing.T) {
	db := &fakeDBTX{}
	q := New(db)

	rows := []CreateAuditLogV1Params{
		{Source: "row-A", Action: "a"},
		{Source: "row-B", Action: "b"},
		{Source: "row-C", Action: "c"},
	}
	if err := q.BatchInsertAuditLog(context.Background(), rows); err != nil {
		t.Fatalf("BatchInsertAuditLog: %v", err)
	}

	// Source is the first column per row; with 16 cols per row,
	// args[0] = row0.Source, args[16] = row1.Source, args[32] = row2.Source.
	wantSources := []string{"row-A", "row-B", "row-C"}
	for i, want := range wantSources {
		gotIdx := i * auditLogColumnsPerRow
		got, ok := db.lastArgs[gotIdx].(string)
		if !ok {
			t.Fatalf("args[%d] = %T, want string", gotIdx, db.lastArgs[gotIdx])
		}
		if got != want {
			t.Fatalf("args[%d] (source for row %d) = %q, want %q", gotIdx, i, got, want)
		}
	}
}
