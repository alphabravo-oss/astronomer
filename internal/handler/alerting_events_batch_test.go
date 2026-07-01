package handler

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// countingAlertRefLoader records how many times each batch loader is invoked so
// the test can prove the event-list path resolves rule/cluster metadata in a
// fixed number of queries regardless of page size (no per-row N+1).
type countingAlertRefLoader struct {
	rules        []sqlc.AlertRule
	clusters     []sqlc.Cluster
	rulesCalls   int
	clusterCalls int
	lastRuleIDs  []uuid.UUID
}

func (c *countingAlertRefLoader) ListAlertRulesByIDs(_ context.Context, ids []uuid.UUID) ([]sqlc.AlertRule, error) {
	c.rulesCalls++
	c.lastRuleIDs = ids
	return c.rules, nil
}

func (c *countingAlertRefLoader) ListClustersByIDs(_ context.Context, _ []uuid.UUID) ([]sqlc.Cluster, error) {
	c.clusterCalls++
	return c.clusters, nil
}

// A page of 20 events sharing one rule + one cluster must resolve names with a
// single ListAlertRulesByIDs + single ListClustersByIDs — not 20 of each. Before
// the fix, ListEvents called GetAlertRuleByID + GetClusterByID per row.
func TestAlertEventResponsesBatched_NoPerRowLookups(t *testing.T) {
	ruleID := uuid.New()
	clusterID := uuid.New()
	loader := &countingAlertRefLoader{
		rules:    []sqlc.AlertRule{{ID: ruleID, Name: "cpu-high", Severity: "critical"}},
		clusters: []sqlc.Cluster{{ID: clusterID, Name: "prod", DisplayName: "Prod East"}},
	}

	events := make([]sqlc.AlertEvent, 0, 20)
	for i := 0; i < 20; i++ {
		events = append(events, sqlc.AlertEvent{
			ID:        uuid.New(),
			RuleID:    ruleID,
			ClusterID: pgtype.UUID{Bytes: clusterID, Valid: true},
			Status:    "firing",
		})
	}

	items := alertEventResponsesBatched(context.Background(), loader, events)
	if len(items) != 20 {
		t.Fatalf("expected 20 items, got %d", len(items))
	}
	if loader.rulesCalls != 1 {
		t.Errorf("expected exactly 1 ListAlertRulesByIDs call, got %d", loader.rulesCalls)
	}
	if loader.clusterCalls != 1 {
		t.Errorf("expected exactly 1 ListClustersByIDs call, got %d", loader.clusterCalls)
	}
	// The 20 events share one rule/cluster, so only one distinct ID is loaded.
	if len(loader.lastRuleIDs) != 1 {
		t.Errorf("expected 1 distinct rule id collected, got %d", len(loader.lastRuleIDs))
	}
	if items[0]["ruleName"] != "cpu-high" || items[0]["severity"] != "critical" {
		t.Errorf("rule fields not resolved from batch: %+v", items[0])
	}
	if items[0]["clusterName"] != "Prod East" {
		t.Errorf("cluster display name not resolved from batch: %+v", items[0])
	}
}

// An event whose rule/cluster aren't found must fall back to the same defaults
// the single-row path used (empty rule name, "warning" severity, nil cluster).
func TestAlertEventResponsesBatched_MissingRefsUseDefaults(t *testing.T) {
	loader := &countingAlertRefLoader{} // returns no rules/clusters
	events := []sqlc.AlertEvent{{
		ID:        uuid.New(),
		RuleID:    uuid.New(),
		ClusterID: pgtype.UUID{Bytes: uuid.New(), Valid: true},
		Status:    "firing",
	}}

	items := alertEventResponsesBatched(context.Background(), loader, events)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0]["ruleName"] != "" {
		t.Errorf("expected empty ruleName default, got %v", items[0]["ruleName"])
	}
	if items[0]["severity"] != "warning" {
		t.Errorf("expected warning severity default, got %v", items[0]["severity"])
	}
	if items[0]["clusterName"] != nil {
		t.Errorf("expected nil clusterName default, got %v", items[0]["clusterName"])
	}
}
