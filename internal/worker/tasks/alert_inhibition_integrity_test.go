package tasks

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type inhibitionAuditQueries struct {
	RuntimeQuerier
	rows     []sqlc.AlertInhibition
	audits   map[string]sqlc.UpsertAuditOutboxParams
	auditErr error
}

func (q *inhibitionAuditQueries) ListEnabledAlertInhibitions(context.Context) ([]sqlc.AlertInhibition, error) {
	return q.rows, nil
}

func (q *inhibitionAuditQueries) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	if q.auditErr != nil {
		return sqlc.AuditOutbox{}, q.auditErr
	}
	q.audits[arg.DedupeKey] = arg
	return sqlc.AuditOutbox{ID: arg.ID}, nil
}

func TestInhibitionIntegrityAuditDedupeRevisionAndRedaction(t *testing.T) {
	valid := inhibitionRule(t, []inhibitionMatcher{{Label: "severity", Value: "critical"}}, []inhibitionMatcher{{Label: "severity", Value: "warning"}}, nil)
	valid.ID = uuid.New()
	corrupt := valid
	corrupt.ID = uuid.New()
	corrupt.Name = "sensitive-fixture"
	corrupt.EqualLabels = []byte(`{"password":"sensitive-fixture"}`)
	q := &inhibitionAuditQueries{rows: []sqlc.AlertInhibition{corrupt, valid}, audits: map[string]sqlc.UpsertAuditOutboxParams{}}
	var logs bytes.Buffer
	ctx := testRuntimeContext(RuntimeDependencies{Queries: q, Log: slog.New(slog.NewJSONHandler(&logs, nil))})
	for range 2 {
		rows, auditErr, err := listEnabledInhibitions(ctx)
		if err != nil || auditErr != nil || len(rows) != 1 || rows[0].ID != valid.ID {
			t.Fatalf("valid-rule continuation failed: rows=%d auditErr=%v err=%v", len(rows), auditErr, err)
		}
	}
	if len(q.audits) != 1 {
		t.Fatalf("same corrupt revision produced %d audit intents", len(q.audits))
	}
	for _, row := range q.audits {
		if row.ResourceID != corrupt.ID.String() || row.Action != "alert.inhibition.integrity_failure" || row.Source != "worker" || len(row.Detail) > 200 || strings.Contains(string(row.Detail), "sensitive-fixture") || row.ResourceName != "" {
			t.Fatalf("unbounded or incorrect integrity envelope: action=%s resource=%s", row.Action, row.ResourceID)
		}
	}
	if strings.Contains(logs.String(), "sensitive-fixture") || !strings.Contains(logs.String(), corrupt.ID.String()) || !strings.Contains(logs.String(), "equal_labels:invalid_shape") {
		t.Fatal("diagnostic must contain only bounded rule ID and category")
	}
	q.rows[0].UpdatedAt = time.Now().UTC()
	_, _, _ = listEnabledInhibitions(ctx)
	q.rows[0].EqualLabels = []byte(`{"token":"changed-fixture"}`)
	_, _, _ = listEnabledInhibitions(ctx)
	if len(q.audits) != 3 {
		t.Fatalf("timestamp/payload revision changes must produce distinct intents; got %d", len(q.audits))
	}
}

type inhibitionSweepQueries struct {
	RuntimeQuerier
	store    *inhibitionAuditQueries
	fleet    *countingFleetQuerier
	dispatch *alertDispatchQuerier
	rule     sqlc.AlertRule
}

// Resolve the embedded runtime surfaces explicitly: an accidental extra query
// remains a test failure through the other nil interface methods.
func (q *inhibitionSweepQueries) ListAlertRules(context.Context, sqlc.ListAlertRulesParams) ([]sqlc.AlertRule, error) {
	return []sqlc.AlertRule{q.rule}, nil
}

func (q *inhibitionSweepQueries) ListAlertSilences(context.Context, sqlc.ListAlertSilencesParams) ([]sqlc.AlertSilence, error) {
	return nil, nil
}

func (q *inhibitionSweepQueries) ListAlertEventsByRule(context.Context, sqlc.ListAlertEventsByRuleParams) ([]sqlc.AlertEvent, error) {
	return nil, nil
}

func TestInhibitionAuditFailureStillEvaluatesValidAlerts(t *testing.T) {
	clusterID := uuid.New()
	corrupt := inhibitionRule(t, []inhibitionMatcher{{Label: "severity", Value: "critical"}}, []inhibitionMatcher{{Label: "severity", Value: "warning"}}, nil)
	corrupt.ID = uuid.New()
	corrupt.EqualLabels = []byte(`{"password":"sensitive-fixture"}`)
	q := &inhibitionSweepQueries{
		store:    &inhibitionAuditQueries{rows: []sqlc.AlertInhibition{corrupt}, auditErr: errors.New("driver included sensitive-fixture")},
		fleet:    &countingFleetQuerier{clusters: []sqlc.Cluster{{ID: clusterID, Name: "disconnected", Status: "disconnected"}}},
		dispatch: &alertDispatchQuerier{},
		rule:     sqlc.AlertRule{ID: uuid.New(), Name: "lost heartbeat", RuleType: "absence", Enabled: true, Severity: "warning", ClusterID: pgtype.UUID{Bytes: clusterID, Valid: true}, Configuration: []byte(`{}`)},
	}
	ctx := testRuntimeContext(RuntimeDependencies{Queries: q, AlertNotificationRunTx: func(ctx context.Context, fn func(AlertNotificationMutationTx) error) error {
		return fn(q.dispatch)
	}})
	task, err := NewAlertEvaluationTask(AlertEvaluationPayload{})
	if err != nil {
		t.Fatal(err)
	}
	err = HandleAlertEvaluation(ctx, task)
	if err == nil || strings.Contains(err.Error(), "sensitive-fixture") || len(q.dispatch.events) != 1 {
		t.Fatalf("audit failure must be retriable after valid alert commits: err=%v events=%d", err, len(q.dispatch.events))
	}
}

func (q *inhibitionSweepQueries) ListEnabledAlertInhibitions(ctx context.Context) ([]sqlc.AlertInhibition, error) {
	return q.store.ListEnabledAlertInhibitions(ctx)
}
func (q *inhibitionSweepQueries) UpsertAuditOutbox(ctx context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	return q.store.UpsertAuditOutbox(ctx, arg)
}
func (q *inhibitionSweepQueries) ListClustersByIDs(ctx context.Context, ids []uuid.UUID) ([]sqlc.Cluster, error) {
	return q.fleet.ListClustersByIDs(ctx, ids)
}
func (q *inhibitionSweepQueries) ListClusterHealthStatusesForClusters(ctx context.Context, ids []uuid.UUID) ([]sqlc.ClusterHealthStatus, error) {
	return q.fleet.ListClusterHealthStatusesForClusters(ctx, ids)
}
func (q *inhibitionSweepQueries) ListClusterLivenessForClusters(ctx context.Context, ids []uuid.UUID) ([]sqlc.ClusterLiveness, error) {
	return q.fleet.ListClusterLivenessForClusters(ctx, ids)
}
