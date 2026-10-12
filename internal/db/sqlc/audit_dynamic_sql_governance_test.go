package sqlc

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"reflect"
	"strings"
	"testing"
)

type governanceCaptureDB struct {
	query string
	args  []any
	calls int
}

func (d *governanceCaptureDB) Exec(_ context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	d.query = query
	d.args = args
	d.calls++
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}
func (d *governanceCaptureDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	panic("unused")
}
func (d *governanceCaptureDB) QueryRow(context.Context, string, ...any) pgx.Row { panic("unused") }

func TestAuditSQLDynamicFilterBindsOperatorValues(t *testing.T) {
	attack := "fixture'; DROP TABLE audit_log; --"
	arg := AuditLogFilterParams{Actor: attack, Q: attack, ResourceType: attack, ResourceID: attack, ResourceName: attack, Target: attack, Action: attack, ActionClass: attack, Source: attack, CorrelationID: attack, RequestID: attack, ClusterID: attack, ProjectID: attack, Audience: attack, Result: attack, Limit: 7, Offset: 2}
	query, args, limit := buildAuditLogV1PageQuery(arg)
	if limit != 7 || strings.Contains(query, "fixture") || strings.Contains(query, "DROP TABLE") || strings.Contains(query, "$%d") {
		t.Fatal("operator value or unexpanded placeholder entered SQL")
	}
	seen := 0
	for _, value := range args {
		if text, ok := value.(string); ok && strings.Contains(text, "fixture") {
			seen++
		}
	}
	if seen != 13 {
		t.Fatalf("operator values not retained in bound args: %d", seen)
	}
	if !reflect.DeepEqual(args[len(args)-2:], []any{int32(8), int32(2)}) {
		t.Fatal("bounded continuation parameters changed")
	}
}
func TestAuditSQLBatchBindsValuesAndPartitionDDLRejectsIdentifiers(t *testing.T) {
	capture := &governanceCaptureDB{}
	q := New(capture)
	attack := "fixture'); DROP TABLE users; --"
	rows := []CreateAuditLogV1Params{{Action: attack, ResourceType: "fixture"}, {Action: "second", ResourceType: "fixture"}}
	if err := q.execBatchInsertAuditLog(t.Context(), rows); err != nil {
		t.Fatal(err)
	}
	if capture.calls != 1 || strings.Contains(capture.query, "fixture") || !strings.Contains(capture.query, "$34") || len(capture.args) != 34 {
		t.Fatal("batch construction interpolated values or changed parameter width")
	}
	found := false
	for _, arg := range capture.args {
		found = found || arg == attack
	}
	if !found {
		t.Fatal("batch caller value was dropped")
	}
	before := capture.calls
	for _, name := range []string{`audit_log_2026_10;DROP TABLE users`, `audit_log_2026_10"`, `public.audit_log_2026_10`, `audit_log_2026_10_extra`} {
		if err := q.DropAuditLogPartition(t.Context(), name); err == nil || capture.calls != before {
			t.Fatal("unapproved identifier reached DDL")
		}
	}
	if err := q.DropAuditLogPartition(t.Context(), "audit_log_2026_10"); err != nil {
		t.Fatal(err)
	}
	if capture.query != `DROP TABLE IF EXISTS "audit_log_2026_10"` || len(capture.args) != 0 {
		t.Fatal("approved identifier was not quoted as one identifier")
	}
}
