package handler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

type operationRunnerTestRow struct {
	id        uuid.UUID
	target    string
	status    string
	startedAt time.Time
}

func TestClaimLatestOperations(t *testing.T) {
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	old := operationRunnerTestRow{id: uuid.New(), target: "cluster-1:argocd", status: OpStatusPending}
	newer := operationRunnerTestRow{id: uuid.New(), target: "cluster-1:argocd", status: OpStatusPending}
	freshRunning := operationRunnerTestRow{id: uuid.New(), target: "cluster-2:argocd", status: OpStatusRunning, startedAt: now.Add(-30 * time.Second)}
	staleRunning := operationRunnerTestRow{id: uuid.New(), target: "cluster-3:argocd", status: OpStatusRunning, startedAt: now.Add(-2 * time.Minute)}
	markErr := operationRunnerTestRow{id: uuid.New(), target: "cluster-4:argocd", status: OpStatusPending}
	conditionalFreshOlder := operationRunnerTestRow{id: uuid.New(), target: "cluster-5:argocd", status: OpStatusRunning, startedAt: now.Add(-30 * time.Second)}
	conditionalNewest := operationRunnerTestRow{id: uuid.New(), target: "cluster-5:argocd", status: OpStatusPending}

	tests := []struct {
		name            string
		rows            []operationRunnerTestRow
		wantSuperseded  []uuid.UUID
		wantMarked      []uuid.UUID
		wantClaimed     []uuid.UUID
		failMarkRunning uuid.UUID
		shouldSupersede func(operationRunnerTestRow, time.Time) bool
	}{
		{
			name:            "supersedes older target and skips fresh running",
			rows:            []operationRunnerTestRow{old, newer, freshRunning, staleRunning, markErr},
			wantSuperseded:  []uuid.UUID{old.id},
			wantMarked:      []uuid.UUID{newer.id, staleRunning.id},
			wantClaimed:     []uuid.UUID{newer.id, staleRunning.id},
			failMarkRunning: markErr.id,
		},
		{
			name:        "empty batch",
			rows:        nil,
			wantClaimed: nil,
		},
		{
			name:        "conditional supersede can leave fresh running older row alone",
			rows:        []operationRunnerTestRow{conditionalFreshOlder, conditionalNewest},
			wantMarked:  []uuid.UUID{conditionalNewest.id},
			wantClaimed: []uuid.UUID{conditionalNewest.id},
			shouldSupersede: func(row operationRunnerTestRow, at time.Time) bool {
				return row.status == OpStatusPending || !row.startedAt.IsZero() && at.Sub(row.startedAt) >= time.Minute
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var superseded []uuid.UUID
			var marked []uuid.UUID
			claimed := claimLatestOperations(context.Background(), tt.rows, operationRunnerConfig[operationRunnerTestRow]{
				ID:              func(row operationRunnerTestRow) uuid.UUID { return row.id },
				TargetKey:       func(row operationRunnerTestRow) string { return row.target },
				Status:          func(row operationRunnerTestRow) string { return row.status },
				ShouldSupersede: tt.shouldSupersede,
				IsFreshRunning: func(row operationRunnerTestRow, at time.Time) bool {
					return !row.startedAt.IsZero() && at.Sub(row.startedAt) < time.Minute
				},
				Supersede: func(_ context.Context, row operationRunnerTestRow) {
					superseded = append(superseded, row.id)
				},
				MarkRunning: func(_ context.Context, row operationRunnerTestRow) (operationRunnerTestRow, error) {
					if row.id == tt.failMarkRunning {
						return operationRunnerTestRow{}, errors.New("claim failed")
					}
					marked = append(marked, row.id)
					row.status = OpStatusRunning
					return row, nil
				},
				Claimed: func(row operationRunnerTestRow) claimedOp {
					return claimedOp{ID: row.id}
				},
				Now: func() time.Time { return now },
			})

			if !sameUUIDs(superseded, tt.wantSuperseded) {
				t.Fatalf("superseded = %v, want %v", superseded, tt.wantSuperseded)
			}
			if !sameUUIDs(marked, tt.wantMarked) {
				t.Fatalf("marked = %v, want %v", marked, tt.wantMarked)
			}
			gotClaimed := make([]uuid.UUID, 0, len(claimed))
			for _, op := range claimed {
				gotClaimed = append(gotClaimed, op.ID)
			}
			if !sameUUIDs(gotClaimed, tt.wantClaimed) {
				t.Fatalf("claimed = %v, want %v", gotClaimed, tt.wantClaimed)
			}
		})
	}
}

func TestSummarizeOperations(t *testing.T) {
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	oldFailure := operationRunnerTestRow{id: uuid.New(), target: "old-failure", status: OpStatusFailed, startedAt: now.Add(-2 * time.Hour)}
	rows := []operationRunnerTestRow{
		{id: uuid.New(), target: "pending", status: OpStatusPending},
		{id: uuid.New(), target: "running-stale", status: OpStatusRunning, startedAt: now.Add(-2 * time.Minute)},
		{id: uuid.New(), target: "running-fresh", status: OpStatusRunning, startedAt: now.Add(-15 * time.Second)},
		{id: uuid.New(), target: "failed-recent", status: OpStatusFailed, startedAt: now.Add(-5 * time.Minute)},
		oldFailure,
		{id: uuid.New(), target: "filtered", status: OpStatusPending},
	}

	summary := summarizeOperations(context.Background(), rows, operationStatusSummaryConfig[operationRunnerTestRow]{
		Status:    func(row operationRunnerTestRow) string { return row.status },
		CreatedAt: func(row operationRunnerTestRow) time.Time { return row.startedAt },
		IsStaleRunning: func(row operationRunnerTestRow, at time.Time) bool {
			return !row.startedAt.IsZero() && at.Sub(row.startedAt) > time.Minute
		},
		Include: func(_ context.Context, row operationRunnerTestRow) bool {
			return row.target != "filtered"
		},
		Preview: func(_ context.Context, row operationRunnerTestRow) map[string]any {
			return map[string]any{"target": row.target, "status": row.status}
		},
		Now:                   func() time.Time { return now },
		RecentLimit:           3,
		RecentFailureWindow:   30 * time.Minute,
		StaleThresholdSeconds: 60,
	})

	if summary.QueueDepth != 3 {
		t.Fatalf("queue depth = %d, want 3", summary.QueueDepth)
	}
	if summary.StaleRunning != 1 {
		t.Fatalf("stale running = %d, want 1", summary.StaleRunning)
	}
	if summary.RecentFailures != 1 {
		t.Fatalf("recent failures = %d, want 1", summary.RecentFailures)
	}
	if summary.LatestFailure == nil || summary.LatestFailure["target"] != "failed-recent" {
		t.Fatalf("latest failure = %+v, want failed-recent", summary.LatestFailure)
	}
	if len(summary.Recent) != 3 {
		t.Fatalf("recent len = %d, want 3", len(summary.Recent))
	}
	if summary.Counts[OpStatusPending] != 1 || summary.Counts[OpStatusRunning] != 2 || summary.Counts[OpStatusFailed] != 2 {
		t.Fatalf("counts = %+v", summary.Counts)
	}
	if summary.reconcilerMap()["staleThresholdSecond"] != 60 {
		t.Fatalf("reconciler map = %+v", summary.reconcilerMap())
	}
}

func TestIsRetryableOperationStatus(t *testing.T) {
	tests := []struct {
		status string
		want   bool
	}{
		{status: OpStatusFailed, want: true},
		{status: OpStatusSuperseded, want: true},
		{status: OpStatusPending, want: false},
		{status: OpStatusRunning, want: false},
		{status: OpStatusCompleted, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			if got := isRetryableOperationStatus(tt.status); got != tt.want {
				t.Fatalf("isRetryableOperationStatus(%q) = %t, want %t", tt.status, got, tt.want)
			}
		})
	}
}

func sameUUIDs(got, want []uuid.UUID) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
