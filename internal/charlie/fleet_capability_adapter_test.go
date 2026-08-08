package charlie

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
)

type fleetQueriesFake struct {
	summary    sqlc.CharlieAgentFleetSummaryRow
	list       []sqlc.CharlieAgentFleetListRow
	listParams sqlc.CharlieAgentFleetListParams
	get        sqlc.CharlieAgentFleetGetRow
}

func (f *fleetQueriesFake) CharlieAgentFleetSummary(context.Context, int32) (sqlc.CharlieAgentFleetSummaryRow, error) {
	return f.summary, nil
}
func (f *fleetQueriesFake) CharlieAgentFleetList(_ context.Context, params sqlc.CharlieAgentFleetListParams) ([]sqlc.CharlieAgentFleetListRow, error) {
	f.listParams = params
	return f.list, nil
}
func (f *fleetQueriesFake) CharlieAgentFleetGet(context.Context, uuid.UUID) (sqlc.CharlieAgentFleetGetRow, error) {
	return f.get, nil
}
func (f *fleetQueriesFake) CharlieAgentConnectionHistory(context.Context, sqlc.CharlieAgentConnectionHistoryParams) ([]sqlc.AgentConnectionEvent, error) {
	return nil, nil
}
func (f *fleetQueriesFake) CharlieAgentReconnectStats(context.Context, sqlc.CharlieAgentReconnectStatsParams) (sqlc.CharlieAgentReconnectStatsRow, error) {
	return sqlc.CharlieAgentReconnectStatsRow{}, nil
}
func (f *fleetQueriesFake) CharlieTunnelHealth(context.Context, time.Time) (sqlc.CharlieTunnelHealthRow, error) {
	return sqlc.CharlieTunnelHealthRow{}, nil
}
func (f *fleetQueriesFake) CharlieTunnelReplicaDistribution(context.Context) ([]sqlc.CharlieTunnelReplicaDistributionRow, error) {
	return nil, nil
}
func (f *fleetQueriesFake) CharlieTunnelRecentErrors(context.Context, sqlc.CharlieTunnelRecentErrorsParams) ([]sqlc.TunnelLocatorEvent, error) {
	return nil, nil
}

func TestFleetCapabilityListIsPaginatedAndOmitsLabelsAndClusterInternals(t *testing.T) {
	clusterID := uuid.New()
	queries := &fleetQueriesFake{list: []sqlc.CharlieAgentFleetListRow{{
		ClusterID: clusterID, ClusterName: "internal-SENTINEL", DisplayName: "production",
		Environment: "prod", Region: "us-east", Labels: json.RawMessage(`{"api_key":"SENTINEL"}`),
		AgentID: "agent-a", InstalledAgentVersion: "1.0.0", ConnectionState: "connected",
	}}}
	adapter, _ := NewFleetCapabilityAdapter(queries)
	descriptor, _ := capabilityByName("astronomer.agent_fleet.list")
	arguments := rawArguments(t, map[string]any{"page": 3, "page_size": 20, "environment": "prod"})
	result, err := adapter.Execute(context.Background(), descriptor, arguments)
	if err != nil {
		t.Fatal(err)
	}
	if queries.listParams.PageOffset != 40 || queries.listParams.PageLimit != 20 || !queries.listParams.Environment.Valid {
		t.Fatalf("pagination/filter not pushed to SQL: %+v", queries.listParams)
	}
	if stringsContainsAny(string(result), "SENTINEL", "internal-SENTINEL", "labels", "api_key") {
		t.Fatalf("fleet list leaked internal metadata: %s", result)
	}
	if !strings.Contains(string(result), clusterID.String()) || !strings.Contains(string(result), "production") {
		t.Fatalf("fleet list omitted safe identity fields: %s", result)
	}
}

func TestFleetCapabilitySummaryClassifiesBoundedAnomalyPattern(t *testing.T) {
	queries := &fleetQueriesFake{summary: sqlc.CharlieAgentFleetSummaryRow{TotalClusters: 10, ConnectedClusters: 7, DisconnectedClusters: 3}, list: []sqlc.CharlieAgentFleetListRow{
		{Environment: "prod", Region: "us-east", ConnectionState: "disconnected"},
		{Environment: "prod", Region: "us-west", ConnectionState: "disconnected"},
		{Environment: "prod", Region: "eu-west", ConnectionState: "disconnected"},
	}}
	adapter, _ := NewFleetCapabilityAdapter(queries)
	descriptor, _ := capabilityByName("astronomer.agent_fleet.summary")
	result, err := adapter.Execute(context.Background(), descriptor, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result), `"pattern":"environment_subset"`) {
		t.Fatalf("fleet pattern=%s", result)
	}
}

func TestFleetCapabilityRejectsInvalidClusterIdentifierBeforeQuery(t *testing.T) {
	queries := &fleetQueriesFake{}
	adapter, _ := NewFleetCapabilityAdapter(queries)
	descriptor, _ := capabilityByName("astronomer.agent_fleet.get")
	if _, err := adapter.Execute(context.Background(), descriptor, rawArguments(t, map[string]any{"cluster_id": "../downstream"})); err == nil {
		t.Fatal("invalid cluster record identifier was accepted")
	}
}
