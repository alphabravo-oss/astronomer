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

type fleetFake struct {
	rows []sqlc.CharlieAgentFleetListRow
}

func (f fleetFake) CharlieAgentFleetSummary(context.Context, int32) (sqlc.CharlieAgentFleetSummaryRow, error) {
	return sqlc.CharlieAgentFleetSummaryRow{TotalClusters: int64(len(f.rows))}, nil
}
func (f fleetFake) CharlieAgentFleetList(context.Context, sqlc.CharlieAgentFleetListParams) ([]sqlc.CharlieAgentFleetListRow, error) {
	return f.rows, nil
}
func (fleetFake) CharlieAgentFleetGet(context.Context, uuid.UUID) (sqlc.CharlieAgentFleetGetRow, error) {
	return sqlc.CharlieAgentFleetGetRow{}, nil
}
func (fleetFake) CharlieAgentConnectionHistory(context.Context, sqlc.CharlieAgentConnectionHistoryParams) ([]sqlc.AgentConnectionEvent, error) {
	return []sqlc.AgentConnectionEvent{}, nil
}
func (fleetFake) CharlieAgentReconnectStats(context.Context, sqlc.CharlieAgentReconnectStatsParams) (sqlc.CharlieAgentReconnectStatsRow, error) {
	return sqlc.CharlieAgentReconnectStatsRow{}, nil
}
func (fleetFake) CharlieTunnelReplicaDistribution(context.Context) ([]sqlc.CharlieTunnelReplicaDistributionRow, error) {
	return nil, nil
}
func (fleetFake) CharlieTunnelRecentErrors(context.Context, sqlc.CharlieTunnelRecentErrorsParams) ([]sqlc.TunnelLocatorEvent, error) {
	return nil, nil
}
func (fleetFake) CharlieTunnelHealth(context.Context, time.Time) (sqlc.CharlieTunnelHealthRow, error) {
	return sqlc.CharlieTunnelHealthRow{}, nil
}

func healthyFleetRow(environment, region string) sqlc.CharlieAgentFleetListRow {
	return sqlc.CharlieAgentFleetListRow{
		ClusterID: uuid.New(), ConnectionState: "connected", Environment: environment, Region: region,
		AuthenticationState: "ok", RegistrationState: "registered",
		AuditIngestionState: "healthy", MetricsIngestionState: "healthy", StateIngestionState: "healthy",
		DownstreamApiReachable: pgtype.Bool{Bool: true, Valid: true},
	}
}

func TestClassifyFleetPatterns(t *testing.T) {
	healthy := []sqlc.CharlieAgentFleetListRow{healthyFleetRow("prod", "east"), healthyFleetRow("prod", "west"), healthyFleetRow("dev", "west")}
	if got := ClassifyFleetPattern(healthy); got != PatternHealthy {
		t.Fatalf("healthy pattern=%s", got)
	}
	one := append([]sqlc.CharlieAgentFleetListRow(nil), healthy...)
	one[0].ConnectionState = "disconnected"
	if got := ClassifyFleetPattern(one); got != PatternSingle {
		t.Fatalf("single pattern=%s", got)
	}

	rows := make([]sqlc.CharlieAgentFleetListRow, 10)
	for i := range rows {
		rows[i] = healthyFleetRow("prod", "west")
	}
	rows[0].ConnectionState, rows[1].ConnectionState = "disconnected", "disconnected"
	if got := ClassifyFleetPattern(rows); got != PatternSubset {
		t.Fatalf("subset pattern=%s", got)
	}
	rows[2].ConnectionState = "disconnected"
	if got := ClassifyFleetPattern(rows); got != PatternFleetWide {
		t.Fatalf("fleet-wide pattern=%s", got)
	}
}

func TestFleetServiceRejectsUnboundedQueries(t *testing.T) {
	service := NewFleetService(fleetFake{})
	if _, err := service.Summary(context.Background(), time.Second); err == nil {
		t.Fatal("unbounded stale threshold accepted")
	}
	if _, err := service.List(context.Background(), FleetListFilter{PageSize: 101}); err == nil {
		t.Fatal("unbounded page accepted")
	}
	if _, err := service.Get(context.Background(), uuid.Nil); err == nil {
		t.Fatal("empty connection record ID accepted")
	}
	if _, err := service.ConnectionHistory(context.Background(), uuid.New(), time.Now().Add(-time.Hour), 101); err == nil {
		t.Fatal("unbounded history accepted")
	}
}

func TestFleetPackageCannotImportDownstreamTunnel(t *testing.T) {
	parsed, err := parser.ParseFile(token.NewFileSet(), "fleet.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range parsed.Imports {
		path := strings.Trim(spec.Path.Value, `"`)
		if strings.Contains(path, "/tunnel") || strings.Contains(path, "kubernetes") {
			t.Fatalf("fleet telemetry imported prohibited transport %q", path)
		}
	}
}
