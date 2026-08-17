package charlie

import (
	"context"
	"go/parser"
	"go/token"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type clusterAgentHealthFake struct {
	rows []sqlc.CharlieClusterAgentListRow
}

func (f clusterAgentHealthFake) CharlieClusterAgentSummary(context.Context, int32) (sqlc.CharlieClusterAgentSummaryRow, error) {
	return sqlc.CharlieClusterAgentSummaryRow{TotalClusters: int64(len(f.rows))}, nil
}
func (f clusterAgentHealthFake) CharlieClusterAgentList(context.Context, sqlc.CharlieClusterAgentListParams) ([]sqlc.CharlieClusterAgentListRow, error) {
	return f.rows, nil
}
func (clusterAgentHealthFake) CharlieClusterAgentGet(context.Context, uuid.UUID) (sqlc.CharlieClusterAgentGetRow, error) {
	return sqlc.CharlieClusterAgentGetRow{}, nil
}
func (clusterAgentHealthFake) CharlieAgentConnectionHistory(context.Context, sqlc.CharlieAgentConnectionHistoryParams) ([]sqlc.AgentConnectionEvent, error) {
	return []sqlc.AgentConnectionEvent{}, nil
}
func (clusterAgentHealthFake) CharlieAgentReconnectStats(context.Context, sqlc.CharlieAgentReconnectStatsParams) (sqlc.CharlieAgentReconnectStatsRow, error) {
	return sqlc.CharlieAgentReconnectStatsRow{}, nil
}
func (clusterAgentHealthFake) CharlieTunnelReplicaDistribution(context.Context) ([]sqlc.CharlieTunnelReplicaDistributionRow, error) {
	return nil, nil
}
func (clusterAgentHealthFake) CharlieTunnelRecentErrors(context.Context, sqlc.CharlieTunnelRecentErrorsParams) ([]sqlc.TunnelLocatorEvent, error) {
	return nil, nil
}
func (clusterAgentHealthFake) CharlieTunnelHealth(context.Context, time.Time) (sqlc.CharlieTunnelHealthRow, error) {
	return sqlc.CharlieTunnelHealthRow{}, nil
}

func healthyClusterAgentRow(environment, region string) sqlc.CharlieClusterAgentListRow {
	return sqlc.CharlieClusterAgentListRow{
		ClusterID: uuid.New(), ConnectionState: "connected", Environment: environment, Region: region,
		AuthenticationState: "ok", RegistrationState: "registered",
		AuditIngestionState: "healthy", MetricsIngestionState: "healthy", StateIngestionState: "healthy",
		DownstreamApiReachable: pgtype.Bool{Bool: true, Valid: true},
	}
}

func TestClassifyClusterAgentPatterns(t *testing.T) {
	healthy := []sqlc.CharlieClusterAgentListRow{healthyClusterAgentRow("prod", "east"), healthyClusterAgentRow("prod", "west"), healthyClusterAgentRow("dev", "west")}
	if got := ClassifyClusterAgentPattern(healthy); got != PatternHealthy {
		t.Fatalf("healthy pattern=%s", got)
	}
	one := append([]sqlc.CharlieClusterAgentListRow(nil), healthy...)
	one[0].ConnectionState = "disconnected"
	if got := ClassifyClusterAgentPattern(one); got != PatternSingle {
		t.Fatalf("single pattern=%s", got)
	}

	rows := make([]sqlc.CharlieClusterAgentListRow, 10)
	for i := range rows {
		rows[i] = healthyClusterAgentRow("prod", "west")
	}
	rows[0].ConnectionState, rows[1].ConnectionState = "disconnected", "disconnected"
	if got := ClassifyClusterAgentPattern(rows); got != PatternSubset {
		t.Fatalf("subset pattern=%s", got)
	}
	rows[2].ConnectionState = "disconnected"
	if got := ClassifyClusterAgentPattern(rows); got != PatternMultiCluster {
		t.Fatalf("multi-cluster pattern=%s", got)
	}
}

func TestClusterAgentHealthServiceRejectsUnboundedQueries(t *testing.T) {
	service := NewClusterAgentHealthService(clusterAgentHealthFake{})
	if _, err := service.Summary(context.Background(), time.Second); err == nil {
		t.Fatal("unbounded stale threshold accepted")
	}
	if _, err := service.List(context.Background(), ClusterAgentListFilter{PageSize: 101}); err == nil {
		t.Fatal("unbounded page accepted")
	}
	if _, err := service.Get(context.Background(), uuid.Nil); err == nil {
		t.Fatal("empty connection record ID accepted")
	}
	if _, err := service.ConnectionHistory(context.Background(), uuid.New(), time.Now().Add(-time.Hour), 101); err == nil {
		t.Fatal("unbounded history accepted")
	}
}

func TestClusterAgentHealthCannotImportDownstreamTunnel(t *testing.T) {
	parsed, err := parser.ParseFile(token.NewFileSet(), "cluster_agent_health.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range parsed.Imports {
		path := strings.Trim(spec.Path.Value, `"`)
		if strings.Contains(path, "/tunnel") || strings.Contains(path, "kubernetes") {
			t.Fatalf("cluster-agent health telemetry imported prohibited transport %q", path)
		}
	}
}
