package charlie

import (
	"context"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type triggerSweepFake struct {
	triggerIngestFake
	connection    sqlc.CharlieConnection
	rules         []sqlc.CharlieTriggerRule
	clusterAgents []sqlc.CharlieClusterAgentListRow
	distribution  []sqlc.CharlieTunnelReplicaDistributionRow
	tunnel        sqlc.CharlieTunnelHealthRow
}

func (f *triggerSweepFake) GetActiveCharlieConnection(context.Context) (sqlc.CharlieConnection, error) {
	return f.connection, nil
}
func (f *triggerSweepFake) ListEnabledCharlieTriggerRules(context.Context, uuid.UUID) ([]sqlc.CharlieTriggerRule, error) {
	return f.rules, nil
}
func (f *triggerSweepFake) CharlieClusterAgentList(_ context.Context, params sqlc.CharlieClusterAgentListParams) ([]sqlc.CharlieClusterAgentListRow, error) {
	start := int(params.PageOffset)
	if start >= len(f.clusterAgents) {
		return nil, nil
	}
	end := min(start+int(params.PageLimit), len(f.clusterAgents))
	return f.clusterAgents[start:end], nil
}
func (f *triggerSweepFake) CharlieTunnelReplicaDistribution(context.Context) ([]sqlc.CharlieTunnelReplicaDistributionRow, error) {
	return f.distribution, nil
}
func (f *triggerSweepFake) CharlieTunnelHealth(context.Context, time.Time) (sqlc.CharlieTunnelHealthRow, error) {
	return f.tunnel, nil
}

func TestTriggerSweeperEvaluatesPersistentProductOwnedState(t *testing.T) {
	now := time.Unix(80000, 0).UTC()
	ruleNames := []string{
		"agent_heartbeat_stale", "agent_downstream_api_unreachable_reported", "agent_version_unsupported",
		"agent_credential_invalid", "agent_upgrade_failed_or_stalled", "agent_ingestion_failed", "agent_command_expired",
		"cluster_agents_simultaneous_disconnect", "tunnel_replica_concentration", "tunnel_locator_failure",
	}
	rules := make([]sqlc.CharlieTriggerRule, 0, len(ruleNames))
	for _, name := range ruleNames {
		rules = append(rules, triggerRule(name, 5*time.Minute))
	}
	clusterA, clusterB := uuid.New(), uuid.New()
	store := &triggerSweepFake{
		connection: readySessionConnection(), rules: rules,
		clusterAgents: []sqlc.CharlieClusterAgentListRow{
			{ClusterID: clusterA, Environment: "prod", Region: "east", ConnectionState: "disconnected", LastHeartbeat: pgtype.Timestamptz{Time: now.Add(-10 * time.Minute), Valid: true}, ProtocolCompatible: pgtype.Bool{Bool: false, Valid: true}, CredentialState: "revoked", UpgradeState: "stalled", ExpiredCommandCount: 1, DownstreamApiReachable: pgtype.Bool{Bool: false, Valid: true}, AuditIngestionState: "failed"},
			{ClusterID: clusterB, Environment: "prod", Region: "east", ConnectionState: "disconnected", LastHeartbeat: pgtype.Timestamptz{Time: now, Valid: true}, ProtocolCompatible: pgtype.Bool{Bool: true, Valid: true}, DownstreamApiReachable: pgtype.Bool{Bool: true, Valid: true}},
		},
		distribution: []sqlc.CharlieTunnelReplicaDistributionRow{{ServerReplica: "server-a", ConnectionCount: 2}},
		tunnel:       sqlc.CharlieTunnelHealthRow{LookupFailures: 1},
	}
	ingestor, _ := NewTriggerIngestor(store, func() bool { return true })
	ingestor.now = func() time.Time { return now }
	sweeper := NewTriggerSweeper(store, ingestor, func() bool { return true })
	sweeper.now = func() time.Time { return now }
	if err := sweeper.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{}
	for _, name := range ruleNames {
		want[name] = true
	}
	for _, created := range store.created {
		delete(want, created.EventType)
		if created.ResourceType == "agent_connection_record" && created.ResourceID != clusterA.String() {
			t.Fatalf("healthy agent received persistent-state trigger: %#v", created)
		}
	}
	if len(want) != 0 {
		t.Fatalf("persistent triggers not evaluated: missing=%v created=%#v", want, store.created)
	}
}

func TestTriggerSweeperIsInactiveAndBounded(t *testing.T) {
	store := &triggerSweepFake{connection: readySessionConnection()}
	ingestor, _ := NewTriggerIngestor(store, func() bool { return false })
	sweeper := NewTriggerSweeper(store, ingestor, func() bool { return false })
	if err := sweeper.Sweep(context.Background()); err != nil || len(store.created) != 0 {
		t.Fatalf("inactive sweep performed work: err=%v created=%d", err, len(store.created))
	}
	if simultaneousClusterAgentDisconnectThreshold([]sqlc.CharlieClusterAgentListRow{{ConnectionState: "disconnected"}}) {
		t.Fatal("single downstream agent was classified as a multi-cluster incident")
	}
	if concentratedTunnel([]sqlc.CharlieTunnelReplicaDistributionRow{{ServerReplica: "server-a", ConnectionCount: 1}}) {
		t.Fatal("single connection was classified as tunnel concentration")
	}
}
