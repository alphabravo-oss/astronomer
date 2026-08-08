package charlie

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type fleetCapabilityQueries interface {
	CharlieAgentFleetSummary(context.Context, int32) (sqlc.CharlieAgentFleetSummaryRow, error)
	CharlieAgentFleetList(context.Context, sqlc.CharlieAgentFleetListParams) ([]sqlc.CharlieAgentFleetListRow, error)
	CharlieAgentFleetGet(context.Context, uuid.UUID) (sqlc.CharlieAgentFleetGetRow, error)
	CharlieAgentConnectionHistory(context.Context, sqlc.CharlieAgentConnectionHistoryParams) ([]sqlc.AgentConnectionEvent, error)
	CharlieAgentReconnectStats(context.Context, sqlc.CharlieAgentReconnectStatsParams) (sqlc.CharlieAgentReconnectStatsRow, error)
	CharlieTunnelHealth(context.Context, time.Time) (sqlc.CharlieTunnelHealthRow, error)
	CharlieTunnelReplicaDistribution(context.Context) ([]sqlc.CharlieTunnelReplicaDistributionRow, error)
	CharlieTunnelRecentErrors(context.Context, sqlc.CharlieTunnelRecentErrorsParams) ([]sqlc.TunnelLocatorEvent, error)
}

// FleetCapabilityAdapter reads only Astronomer-owned PostgreSQL operational
// metadata. It has no tunnel, Kubernetes, bridge, or credential dependency.
type FleetCapabilityAdapter struct{ queries fleetCapabilityQueries }

func NewFleetCapabilityAdapter(queries fleetCapabilityQueries) (*FleetCapabilityAdapter, error) {
	if queries == nil {
		return nil, fmt.Errorf("Charlie fleet capability store is unavailable")
	}
	return &FleetCapabilityAdapter{queries: queries}, nil
}

func (a *FleetCapabilityAdapter) Execute(ctx context.Context, capability CapabilityDescriptor, arguments map[string]json.RawMessage) (json.RawMessage, error) {
	switch capability.Name {
	case "astronomer.agent_fleet.summary":
		stale := int64Argument(arguments, "stale_after_seconds", 300)
		row, err := a.queries.CharlieAgentFleetSummary(ctx, int32(stale))
		if err != nil {
			return nil, err
		}
		pattern := "healthy"
		if row.DisconnectedClusters == 1 || row.StaleHeartbeats == 1 {
			pattern = "single_agent"
		}
		if row.DisconnectedClusters > 1 || row.StaleHeartbeats > 1 {
			pattern = "fleet_event"
			fleet, listErr := a.queries.CharlieAgentFleetList(ctx, sqlc.CharlieAgentFleetListParams{PageLimit: 100})
			if listErr == nil {
				environments, regions, affected := map[string]struct{}{}, map[string]struct{}{}, int64(0)
				for _, item := range fleet {
					if item.ConnectionState == "connected" {
						continue
					}
					affected++
					environments[item.Environment], regions[item.Region] = struct{}{}, struct{}{}
				}
				if affected == row.DisconnectedClusters {
					switch {
					case len(environments) == 1 && len(regions) > 1:
						pattern = "environment_subset"
					case len(regions) == 1:
						pattern = "region_subset"
					}
				}
			}
		}
		return marshalBounded(map[string]any{"summary": row, "pattern": pattern}, capability.MaxResponseBytes)
	case "astronomer.agent_fleet.list":
		page, pageSize := pagination(arguments, 50)
		rows, err := a.queries.CharlieAgentFleetList(ctx, sqlc.CharlieAgentFleetListParams{
			Environment: optionalPGText(arguments, "environment"), Region: optionalPGText(arguments, "region"),
			ConnectionState: optionalPGText(arguments, "state"), PageOffset: int32((page - 1) * pageSize), PageLimit: int32(pageSize),
		})
		if err != nil {
			return nil, err
		}
		items := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			items = append(items, map[string]any{
				"cluster_id": row.ClusterID, "display_name": row.DisplayName, "environment": row.Environment, "region": row.Region,
				"agent_id": row.AgentID, "installed_agent_version": row.InstalledAgentVersion, "connection_state": row.ConnectionState,
				"last_heartbeat": nullableTime(row.LastHeartbeat), "last_successful_connection_at": nullableTime(row.LastSuccessfulConnectionAt),
				"authentication_state": row.AuthenticationState, "registration_state": row.RegistrationState,
				"protocol_version": row.ProtocolVersion, "protocol_compatible": nullableBool(row.ProtocolCompatible),
				"owning_server_replica": row.OwningServerReplica, "audit_ingestion_state": row.AuditIngestionState,
				"metrics_ingestion_state": row.MetricsIngestionState, "state_ingestion_state": row.StateIngestionState,
				"reported_api_reachable": nullableBool(row.DownstreamApiReachable),
			})
		}
		return marshalBounded(map[string]any{"items": items, "page": page, "page_size": pageSize}, capability.MaxResponseBytes)
	case "astronomer.agent_fleet.get", "astronomer.agent_fleet.upgrade_status", "astronomer.agent_fleet.ingestion_health":
		clusterID, err := uuidArgument(arguments, "cluster_id")
		if err != nil {
			return nil, err
		}
		row, err := a.queries.CharlieAgentFleetGet(ctx, clusterID)
		if err != nil {
			return nil, err
		}
		if capability.Name == "astronomer.agent_fleet.upgrade_status" {
			return marshalBounded(map[string]any{"cluster_id": row.ClusterID, "installed_version": row.InstalledAgentVersion, "desired_version": nullableText(row.DesiredAgentVersion), "upgrade_state": nullableText(row.UpgradeState), "last_status_at": nullableTime(row.LastStatusAt)}, capability.MaxResponseBytes)
		}
		if capability.Name == "astronomer.agent_fleet.ingestion_health" {
			return marshalBounded(map[string]any{"cluster_id": row.ClusterID, "audit": nullableText(row.AuditIngestionState), "metrics": nullableText(row.MetricsIngestionState), "state": nullableText(row.StateIngestionState), "pending_commands": nullableInt(row.PendingCommandCount), "failed_commands": nullableInt(row.FailedCommandCount), "expired_commands": nullableInt(row.ExpiredCommandCount), "reported_api_reachable": nullableBool(row.DownstreamApiReachable), "reported_at": nullableTime(row.DownstreamApiReportedAt)}, capability.MaxResponseBytes)
		}
		stats, _ := a.queries.CharlieAgentReconnectStats(ctx, sqlc.CharlieAgentReconnectStatsParams{ClusterID: clusterID, Since: time.Now().UTC().Add(-24 * time.Hour)})
		return marshalBounded(map[string]any{
			"cluster_id": row.ClusterID, "display_name": row.DisplayName, "environment": row.Environment, "region": row.Region,
			"agent_id": row.AgentID, "installed_agent_version": row.InstalledAgentVersion, "connection_state": row.ConnectionState,
			"last_heartbeat": nullableTime(row.LastHeartbeat), "protocol_version": nullableText(row.ProtocolVersion), "protocol_compatible": nullableBool(row.ProtocolCompatible),
			"authentication_state": nullableText(row.AuthenticationState), "registration_state": nullableText(row.RegistrationState),
			"credential_state": nullableText(row.CredentialState), "credential_expires_at": nullableTime(row.CredentialExpiresAt),
			"owning_server_replica": nullableText(row.OwningServerReplica), "last_successful_connection_at": nullableTime(row.LastSuccessfulConnectionAt),
			"reconnect_count_24h": stats.ReconnectCount, "disconnect_count_24h": stats.DisconnectCount, "flap_events_24h": stats.FlapEventCount,
		}, capability.MaxResponseBytes)
	case "astronomer.agent_fleet.connection_history":
		clusterID, err := uuidArgument(arguments, "cluster_id")
		if err != nil {
			return nil, err
		}
		rows, err := a.queries.CharlieAgentConnectionHistory(ctx, sqlc.CharlieAgentConnectionHistoryParams{ClusterID: clusterID, Since: sinceArgument(arguments, 24*time.Hour), RowLimit: int32(int64Argument(arguments, "limit", 100))})
		if err != nil {
			return nil, err
		}
		items := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			items = append(items, map[string]any{"event_type": row.EventType, "reason_code": row.ReasonCode, "agent_id": row.AgentID, "agent_version": row.AgentVersion, "protocol_version": row.ProtocolVersion, "server_replica": row.ServerReplica, "occurred_at": row.OccurredAt})
		}
		return marshalBounded(map[string]any{"cluster_id": clusterID, "items": items}, capability.MaxResponseBytes)
	case "astronomer.tunnel.health":
		row, err := a.queries.CharlieTunnelHealth(ctx, time.Now().UTC().Add(-15*time.Minute))
		if err != nil {
			return nil, err
		}
		return marshalBounded(row, capability.MaxResponseBytes)
	case "astronomer.tunnel.replica_distribution":
		rows, err := a.queries.CharlieTunnelReplicaDistribution(ctx)
		if err != nil {
			return nil, err
		}
		return marshalBounded(map[string]any{"replicas": rows}, capability.MaxResponseBytes)
	case "astronomer.tunnel.recent_errors":
		rows, err := a.queries.CharlieTunnelRecentErrors(ctx, sqlc.CharlieTunnelRecentErrorsParams{Since: sinceArgument(arguments, time.Hour), ConnectionID: optionalPGText(arguments, "connection_id"), RowLimit: int32(int64Argument(arguments, "limit", 100))})
		if err != nil {
			return nil, err
		}
		items := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			items = append(items, map[string]any{"connection_id": row.ConnectionID, "event_type": row.EventType, "reason_code": row.ReasonCode, "server_replica": row.ServerReplica, "occurred_at": row.OccurredAt})
		}
		return marshalBounded(map[string]any{"items": items}, capability.MaxResponseBytes)
	default:
		return nil, fmt.Errorf("unsupported fleet capability")
	}
}

func (a *FleetCapabilityAdapter) Verify(context.Context, CapabilityDescriptor, map[string]json.RawMessage, json.RawMessage) (bool, error) {
	return true, nil
}

func pagination(args map[string]json.RawMessage, defaultSize int64) (int64, int64) {
	return int64Argument(args, "page", 1), int64Argument(args, "page_size", defaultSize)
}
func int64Argument(args map[string]json.RawMessage, name string, fallback int64) int64 {
	var value int64
	if raw, ok := args[name]; ok && json.Unmarshal(raw, &value) == nil {
		return value
	}
	return fallback
}
func optionalPGText(args map[string]json.RawMessage, name string) pgtype.Text {
	var value string
	raw, ok := args[name]
	if !ok || json.Unmarshal(raw, &value) != nil || value == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: value, Valid: true}
}
func uuidArgument(args map[string]json.RawMessage, name string) (uuid.UUID, error) {
	var value string
	if json.Unmarshal(args[name], &value) != nil {
		return uuid.Nil, fmt.Errorf("invalid identifier")
	}
	return uuid.Parse(value)
}
func sinceArgument(args map[string]json.RawMessage, fallback time.Duration) time.Time {
	var value string
	if raw, ok := args["since"]; ok && json.Unmarshal(raw, &value) == nil {
		if duration, err := time.ParseDuration(value); err == nil {
			return time.Now().UTC().Add(-duration)
		}
	}
	return time.Now().UTC().Add(-fallback)
}
func nullableTime(v pgtype.Timestamptz) any {
	if !v.Valid {
		return nil
	}
	return v.Time.UTC()
}
func nullableText(v pgtype.Text) any {
	if !v.Valid {
		return nil
	}
	return v.String
}
func nullableBool(v pgtype.Bool) any {
	if !v.Valid {
		return nil
	}
	return v.Bool
}
func nullableInt(v pgtype.Int4) any {
	if !v.Valid {
		return nil
	}
	return v.Int32
}
func marshalBounded(value any, max int) (json.RawMessage, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(encoded) > max {
		return nil, fmt.Errorf("capability result exceeds bound")
	}
	return encoded, nil
}
