package tasks

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

type fakeAuditRetentionQuerier struct {
	partitions []string
	dropped    []string
	dropErr    error
}

type fakeAuditRetentionWithOutbox struct {
	fakeAuditRetentionQuerier
	cutoff      pgtype.Timestamptz
	purged      int64
	purgeCalled int
}

func (f *fakeAuditRetentionWithOutbox) DeleteDeliveredAuditOutboxBefore(_ context.Context, cutoff pgtype.Timestamptz) (int64, error) {
	f.purgeCalled++
	f.cutoff = cutoff
	return f.purged, nil
}

func (f *fakeAuditRetentionQuerier) ListAuditLogPartitions(_ context.Context) ([]string, error) {
	return append([]string(nil), f.partitions...), nil
}

func (f *fakeAuditRetentionQuerier) DropAuditLogPartition(_ context.Context, name string) error {
	if f.dropErr != nil {
		return f.dropErr
	}
	f.dropped = append(f.dropped, name)
	return nil
}

func TestAuditLogRetentionCutoff(t *testing.T) {
	now := time.Date(2026, time.May, 9, 12, 0, 0, 0, time.UTC)
	got := auditLogRetentionCutoff(now, 13)
	want := time.Date(2025, time.May, 1, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("cutoff = %s, want %s", got, want)
	}
}

func TestAuditLogPartitionsToDrop(t *testing.T) {
	q := &fakeAuditRetentionQuerier{
		partitions: []string{
			"audit_log_2025_04",
			"audit_log_2025_05",
			"audit_log_2026_04",
			"audit_log_2026_05",
			"audit_log_2026_06",
			"audit_log_default",
			"something_else",
		},
	}
	now := time.Date(2026, time.May, 9, 12, 0, 0, 0, time.UTC)

	got, err := auditLogPartitionsToDrop(context.Background(), q, now, 13)
	if err != nil {
		t.Fatalf("auditLogPartitionsToDrop: %v", err)
	}
	want := []string{"audit_log_2025_04"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("partitions to drop = %#v, want %#v", got, want)
	}
}

func TestEnforceAuditLogRetentionDropsExpiredPartitions(t *testing.T) {
	q := &fakeAuditRetentionQuerier{
		partitions: []string{
			"audit_log_2025_03",
			"audit_log_2025_04",
			"audit_log_2025_05",
			"audit_log_2026_05",
		},
	}
	now := time.Date(2026, time.May, 9, 12, 0, 0, 0, time.UTC)

	if err := enforceAuditLogRetention(context.Background(), q, now, 13); err != nil {
		t.Fatalf("enforceAuditLogRetention: %v", err)
	}
	want := []string{"audit_log_2025_03", "audit_log_2025_04"}
	if !reflect.DeepEqual(q.dropped, want) {
		t.Fatalf("dropped = %#v, want %#v", q.dropped, want)
	}
}

func TestEnforceAuditLogRetentionPropagatesDropError(t *testing.T) {
	q := &fakeAuditRetentionQuerier{
		partitions: []string{"audit_log_2025_04"},
		dropErr:    errors.New("boom"),
	}
	now := time.Date(2026, time.May, 9, 12, 0, 0, 0, time.UTC)

	err := enforceAuditLogRetention(context.Background(), q, now, 13)
	if err == nil || err.Error() == "" {
		t.Fatal("expected error")
	}
}

func TestEnforceAuditLogRetentionPurgesOnlyOldDeliveredOutboxReceipts(t *testing.T) {
	now := time.Date(2026, time.May, 9, 12, 0, 0, 0, time.UTC)
	q := &fakeAuditRetentionWithOutbox{purged: 7}

	if err := enforceAuditLogRetention(context.Background(), q, now, 13); err != nil {
		t.Fatalf("enforceAuditLogRetention: %v", err)
	}
	if q.purgeCalled != 1 || !q.cutoff.Valid {
		t.Fatalf("outbox purge calls=%d cutoff=%#v", q.purgeCalled, q.cutoff)
	}
	want := now.Add(-deliveredAuditOutboxRetention)
	if !q.cutoff.Time.Equal(want) {
		t.Fatalf("outbox cutoff = %s, want %s", q.cutoff.Time, want)
	}
}

func TestHandleEnforceAuditLogRetentionNoRuntime(t *testing.T) {
	if err := HandleEnforceAuditLogRetention(testRuntimeContext(RuntimeDependencies{}), nil); err == nil {
		t.Fatal("HandleEnforceAuditLogRetention returned nil, want an unconfigured-runtime error")
	}
}
