package charlie

import (
	"context"
	"fmt"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	triggerSweepPageSize = 100
	triggerSweepMaxRows  = 1000
)

type triggerSweepQueries interface {
	GetActiveCharlieConnection(context.Context) (sqlc.CharlieConnection, error)
	ListEnabledCharlieTriggerRules(context.Context, uuid.UUID) ([]sqlc.CharlieTriggerRule, error)
	CharlieAgentFleetList(context.Context, sqlc.CharlieAgentFleetListParams) ([]sqlc.CharlieAgentFleetListRow, error)
	CharlieTunnelReplicaDistribution(context.Context) ([]sqlc.CharlieTunnelReplicaDistributionRow, error)
	CharlieTunnelHealth(context.Context, time.Time) (sqlc.CharlieTunnelHealthRow, error)
}

// TriggerSweeper evaluates persistent Astronomer-owned state that does not
// have a reliable edge event (stale heartbeats, reported operational health,
// and tunnel topology). It reads PostgreSQL only and has no downstream tunnel,
// Kubernetes, bridge, or generic transport dependency.
type TriggerSweeper struct {
	queries  triggerSweepQueries
	ingestor *TriggerIngestor
	active   func() bool
	now      func() time.Time
}

func NewTriggerSweeper(queries triggerSweepQueries, ingestor *TriggerIngestor, active func() bool) *TriggerSweeper {
	if queries == nil || ingestor == nil || active == nil {
		return nil
	}
	return &TriggerSweeper{queries: queries, ingestor: ingestor, active: active, now: time.Now}
}

func (s *TriggerSweeper) Sweep(ctx context.Context) error {
	if s == nil || !s.active() {
		return nil
	}
	connection, err := s.queries.GetActiveCharlieConnection(ctx)
	if err != nil {
		return nil
	}
	rules, err := s.queries.ListEnabledCharlieTriggerRules(ctx, connection.ID)
	if err != nil {
		return fmt.Errorf("load Charlie sweep rules: %w", err)
	}
	byName := make(map[string]sqlc.CharlieTriggerRule, len(rules))
	for _, rule := range rules {
		byName[rule.Name] = rule
	}
	rows, err := s.loadFleet(ctx)
	if err != nil {
		return err
	}
	now := s.now().UTC()
	for _, row := range rows {
		checks := []struct {
			name, state, failure, severity string
			eligible                       bool
		}{
			{"agent_heartbeat_stale", "stale", "heartbeat_stale", "warning", staleForRule(row.LastHeartbeat, byName["agent_heartbeat_stale"], now)},
			{"agent_downstream_api_unreachable_reported", "unreachable", "downstream_api_unreachable_reported", "warning", row.DownstreamApiReachable.Valid && !row.DownstreamApiReachable.Bool},
			{"agent_version_unsupported", "unsupported", "protocol_or_version_incompatible", "warning", row.ProtocolCompatible.Valid && !row.ProtocolCompatible.Bool},
			{"agent_credential_invalid", "invalid", "credential_expired_or_revoked", "critical", credentialInvalid(row, now)},
			{"agent_upgrade_failed_or_stalled", "failed", "agent_upgrade_failed_or_stalled", "warning", upgradeFailedOrStalled(row, byName["agent_upgrade_failed_or_stalled"], now)},
			{"agent_ingestion_failed", "failed", "agent_ingestion_failed", "warning", anyFailedState(row.AuditIngestionState, row.MetricsIngestionState, row.StateIngestionState)},
			{"agent_command_expired", "expired", "agent_command_expired", "warning", row.ExpiredCommandCount > 0},
		}
		for _, check := range checks {
			rule, enabled := byName[check.name]
			if !enabled || !check.eligible {
				continue
			}
			signal := TriggerSignal{
				Source: "astronomer", EventType: rule.Name, ResourceType: "agent_connection_record", ResourceID: row.ClusterID.String(),
				FailureClass: check.failure, Severity: check.severity, State: check.state, Timestamp: now,
				ProductVersion: currentProductDocumentationVersion(), Environment: row.Environment, Region: row.Region,
				Summary: "Astronomer cluster-agent operational state requires investigation",
			}
			_, _, _ = s.ingestor.Ingest(ctx, rule, TriggerObservation{Signal: signal, OriginResourceRef: "agent-connection:" + row.ClusterID.String(), OriginEventRef: "agent-state-sweep:" + now.Format(time.RFC3339)})
		}
	}

	if rule, ok := byName["fleet_simultaneous_disconnect"]; ok && fleetDisconnectThreshold(rows) {
		_ = s.ingestAggregate(ctx, rule, "agent_fleet", "agent-fleet", "fleet_disconnect", "simultaneous_disconnect", "critical", now)
	}
	if rule, ok := byName["tunnel_replica_concentration"]; ok {
		distribution, distributionErr := s.queries.CharlieTunnelReplicaDistribution(ctx)
		if distributionErr == nil && concentratedTunnel(distribution) {
			_ = s.ingestAggregate(ctx, rule, "tunnel", "replica-distribution", "concentrated", "single_replica_concentration", "warning", now)
		}
	}
	if rule, ok := byName["tunnel_locator_failure"]; ok {
		window := time.Duration(rule.WindowSeconds) * time.Second
		health, healthErr := s.queries.CharlieTunnelHealth(ctx, now.Add(-window))
		if healthErr == nil && (health.LookupFailures > 0 || health.OwnerUnreachable > 0) {
			_ = s.ingestAggregate(ctx, rule, "tunnel", "cross-replica-locator", "failed", "tunnel_locator_failure", "critical", now)
		}
	}
	return nil
}

func (s *TriggerSweeper) loadFleet(ctx context.Context) ([]sqlc.CharlieAgentFleetListRow, error) {
	rows := make([]sqlc.CharlieAgentFleetListRow, 0, triggerSweepPageSize)
	for offset := 0; offset < triggerSweepMaxRows; offset += triggerSweepPageSize {
		page, err := s.queries.CharlieAgentFleetList(ctx, sqlc.CharlieAgentFleetListParams{
			Environment: pgtype.Text{}, Region: pgtype.Text{}, ConnectionState: pgtype.Text{},
			PageOffset: int32(offset), PageLimit: triggerSweepPageSize,
		})
		if err != nil {
			return nil, fmt.Errorf("load Charlie fleet sweep: %w", err)
		}
		rows = append(rows, page...)
		if len(page) < triggerSweepPageSize {
			return rows, nil
		}
	}
	return nil, fmt.Errorf("Charlie fleet sweep exceeds the reviewed row bound")
}

func (s *TriggerSweeper) ingestAggregate(ctx context.Context, rule sqlc.CharlieTriggerRule, resourceType, resourceID, state, failure, severity string, now time.Time) error {
	_, _, err := s.ingestor.Ingest(ctx, rule, TriggerObservation{Signal: TriggerSignal{
		Source: "astronomer", EventType: rule.Name, ResourceType: resourceType, ResourceID: resourceID,
		FailureClass: failure, Severity: severity, State: state, Timestamp: now,
		ProductVersion: currentProductDocumentationVersion(), Summary: "Astronomer management-plane fleet or tunnel state requires investigation",
	}, OriginResourceRef: resourceType + ":" + resourceID, OriginEventRef: "state-sweep:" + now.Format(time.RFC3339)})
	return err
}

func staleForRule(value pgtype.Timestamptz, rule sqlc.CharlieTriggerRule, now time.Time) bool {
	return value.Valid && validTriggerRule(rule) && !value.Time.UTC().Add(time.Duration(rule.WindowSeconds)*time.Second).After(now)
}

func anyFailedState(values ...string) bool {
	for _, value := range values {
		if value == "failed" {
			return true
		}
	}
	return false
}

func credentialInvalid(row sqlc.CharlieAgentFleetListRow, now time.Time) bool {
	if row.CredentialState == "expired" || row.CredentialState == "revoked" || row.CredentialState == "failed" {
		return true
	}
	return row.CredentialExpiresAt.Valid && !row.CredentialExpiresAt.Time.UTC().After(now)
}

func upgradeFailedOrStalled(row sqlc.CharlieAgentFleetListRow, rule sqlc.CharlieTriggerRule, now time.Time) bool {
	if row.UpgradeState == "failed" || row.UpgradeState == "stalled" {
		return true
	}
	if row.UpgradeState != "pending" && row.UpgradeState != "running" {
		return false
	}
	return row.LastStatusAt.Valid && staleForRule(row.LastStatusAt, rule, now)
}

func fleetDisconnectThreshold(rows []sqlc.CharlieAgentFleetListRow) bool {
	if len(rows) < 2 {
		return false
	}
	disconnected := 0
	for _, row := range rows {
		if row.ConnectionState != "connected" {
			disconnected++
		}
	}
	return disconnected >= 2 && disconnected*100 >= len(rows)*30
}

func concentratedTunnel(rows []sqlc.CharlieTunnelReplicaDistributionRow) bool {
	return len(rows) == 1 && rows[0].ServerReplica != "" && rows[0].ConnectionCount >= 2
}
