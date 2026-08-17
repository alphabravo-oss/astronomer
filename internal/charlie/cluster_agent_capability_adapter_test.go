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

type clusterAgentQueriesFake struct {
	summary    sqlc.CharlieClusterAgentSummaryRow
	list       []sqlc.CharlieClusterAgentListRow
	listParams sqlc.CharlieClusterAgentListParams
	get        sqlc.CharlieClusterAgentGetRow
}

func (f *clusterAgentQueriesFake) CharlieClusterAgentSummary(context.Context, int32) (sqlc.CharlieClusterAgentSummaryRow, error) {
	return f.summary, nil
}
func (f *clusterAgentQueriesFake) CharlieClusterAgentList(_ context.Context, params sqlc.CharlieClusterAgentListParams) ([]sqlc.CharlieClusterAgentListRow, error) {
	f.listParams = params
	return f.list, nil
}
func (f *clusterAgentQueriesFake) CharlieClusterAgentGet(context.Context, uuid.UUID) (sqlc.CharlieClusterAgentGetRow, error) {
	return f.get, nil
}
func (f *clusterAgentQueriesFake) CharlieAgentConnectionHistory(context.Context, sqlc.CharlieAgentConnectionHistoryParams) ([]sqlc.AgentConnectionEvent, error) {
	return nil, nil
}
func (f *clusterAgentQueriesFake) CharlieAgentReconnectStats(context.Context, sqlc.CharlieAgentReconnectStatsParams) (sqlc.CharlieAgentReconnectStatsRow, error) {
	return sqlc.CharlieAgentReconnectStatsRow{}, nil
}
func (f *clusterAgentQueriesFake) CharlieTunnelHealth(context.Context, time.Time) (sqlc.CharlieTunnelHealthRow, error) {
	return sqlc.CharlieTunnelHealthRow{}, nil
}
func (f *clusterAgentQueriesFake) CharlieTunnelReplicaDistribution(context.Context) ([]sqlc.CharlieTunnelReplicaDistributionRow, error) {
	return nil, nil
}
func (f *clusterAgentQueriesFake) CharlieTunnelRecentErrors(context.Context, sqlc.CharlieTunnelRecentErrorsParams) ([]sqlc.TunnelLocatorEvent, error) {
	return nil, nil
}

func TestClusterAgentCapabilityListIsPaginatedAndOmitsLabelsAndClusterInternals(t *testing.T) {
	clusterID := uuid.New()
	queries := &clusterAgentQueriesFake{list: []sqlc.CharlieClusterAgentListRow{{
		ClusterID: clusterID, ClusterName: "internal-SENTINEL", DisplayName: "production",
		Environment: "prod", Region: "us-east", Labels: json.RawMessage(`{"api_key":"SENTINEL"}`),
		AgentID: "agent-a", InstalledAgentVersion: "1.0.0", ConnectionState: "connected",
	}}}
	adapter, _ := NewClusterAgentCapabilityAdapter(queries)
	descriptor, _ := capabilityByName("astronomer.cluster_agents.list")
	arguments := rawArguments(t, map[string]any{"page": 3, "page_size": 20, "environment": "prod"})
	result, err := adapter.Execute(context.Background(), descriptor, arguments)
	if err != nil {
		t.Fatal(err)
	}
	if queries.listParams.PageOffset != 40 || queries.listParams.PageLimit != 20 || !queries.listParams.Environment.Valid {
		t.Fatalf("pagination/filter not pushed to SQL: %+v", queries.listParams)
	}
	if stringsContainsAny(string(result), "SENTINEL", "internal-SENTINEL", "labels", "api_key") {
		t.Fatalf("cluster-agent list leaked internal metadata: %s", result)
	}
	if !strings.Contains(string(result), clusterID.String()) || !strings.Contains(string(result), "production") {
		t.Fatalf("cluster-agent list omitted safe identity fields: %s", result)
	}
}

func TestClusterAgentCapabilitySummaryClassifiesBoundedAnomalyPattern(t *testing.T) {
	queries := &clusterAgentQueriesFake{summary: sqlc.CharlieClusterAgentSummaryRow{TotalClusters: 10, ConnectedClusters: 7, DisconnectedClusters: 3}, list: []sqlc.CharlieClusterAgentListRow{
		{Environment: "prod", Region: "us-east", ConnectionState: "disconnected"},
		{Environment: "prod", Region: "us-west", ConnectionState: "disconnected"},
		{Environment: "prod", Region: "eu-west", ConnectionState: "disconnected"},
	}}
	adapter, _ := NewClusterAgentCapabilityAdapter(queries)
	descriptor, _ := capabilityByName("astronomer.cluster_agents.summary")
	result, err := adapter.Execute(context.Background(), descriptor, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result), `"pattern":"environment_subset"`) {
		t.Fatalf("cluster-agent pattern=%s", result)
	}
}

func TestClusterAgentCapabilityRejectsInvalidClusterIdentifierBeforeQuery(t *testing.T) {
	queries := &clusterAgentQueriesFake{}
	adapter, _ := NewClusterAgentCapabilityAdapter(queries)
	descriptor, _ := capabilityByName("astronomer.cluster_agents.get")
	if _, err := adapter.Execute(context.Background(), descriptor, rawArguments(t, map[string]any{"cluster_id": "../downstream"})); err == nil {
		t.Fatal("invalid cluster record identifier was accepted")
	}
}
