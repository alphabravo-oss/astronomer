package tasks

import (
	"context"
	"testing"
)

type fakeAuditPartitionQuerier struct {
	called bool
}

func (f *fakeAuditPartitionQuerier) EnsureAuditLogPartitions(_ context.Context) error {
	f.called = true
	return nil
}

func TestHandleEnsureAuditLogPartitions(t *testing.T) {
	q := &fakeAuditPartitionQuerier{}
	if err := ensureAuditLogPartitions(context.Background(), q); err != nil {
		t.Fatalf("ensureAuditLogPartitions: %v", err)
	}
	if !q.called {
		t.Fatal("expected EnsureAuditLogPartitions to be called")
	}
}

func TestHandleEnsureAuditLogPartitions_NoRuntime(t *testing.T) {
	defer resetRuntime()
	if err := HandleEnsureAuditLogPartitions(context.Background(), nil); err != nil {
		t.Fatalf("HandleEnsureAuditLogPartitions: %v", err)
	}
}
