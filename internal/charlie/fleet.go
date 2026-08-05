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
	maxFleetPageSize    = 100
	maxFleetHistoryRows = 100
)

// FleetTelemetryReader is deliberately database-shaped. It has no tunnel,
// Kubernetes requester, proxy, or generic transport method, making it
// impossible for these capabilities to cross into a downstream cluster.
type FleetTelemetryReader interface {
	CharlieAgentFleetSummary(context.Context, int32) (sqlc.CharlieAgentFleetSummaryRow, error)
	CharlieAgentFleetList(context.Context, sqlc.CharlieAgentFleetListParams) ([]sqlc.CharlieAgentFleetListRow, error)
	CharlieAgentFleetGet(context.Context, uuid.UUID) (sqlc.CharlieAgentFleetGetRow, error)
	CharlieAgentConnectionHistory(context.Context, sqlc.CharlieAgentConnectionHistoryParams) ([]sqlc.AgentConnectionEvent, error)
	CharlieAgentReconnectStats(context.Context, sqlc.CharlieAgentReconnectStatsParams) (sqlc.CharlieAgentReconnectStatsRow, error)
	CharlieTunnelReplicaDistribution(context.Context) ([]sqlc.CharlieTunnelReplicaDistributionRow, error)
	CharlieTunnelRecentErrors(context.Context, sqlc.CharlieTunnelRecentErrorsParams) ([]sqlc.TunnelLocatorEvent, error)
	CharlieTunnelHealth(context.Context, time.Time) (sqlc.CharlieTunnelHealthRow, error)
}

type FleetService struct {
	reader FleetTelemetryReader
}

func NewFleetService(reader FleetTelemetryReader) *FleetService {
	if reader == nil {
		return nil
	}
	return &FleetService{reader: reader}
}

func (s *FleetService) Summary(ctx context.Context, staleAfter time.Duration) (sqlc.CharlieAgentFleetSummaryRow, error) {
	if s == nil || s.reader == nil {
		return sqlc.CharlieAgentFleetSummaryRow{}, fmt.Errorf("agent fleet telemetry is unavailable")
	}
	if staleAfter < time.Minute || staleAfter > 24*time.Hour {
		return sqlc.CharlieAgentFleetSummaryRow{}, fmt.Errorf("stale heartbeat threshold must be between one minute and 24 hours")
	}
	return s.reader.CharlieAgentFleetSummary(ctx, int32(staleAfter/time.Second))
}

type FleetListFilter struct {
	Environment     string
	Region          string
	ConnectionState string
	Page            int
	PageSize        int
}

func (s *FleetService) List(ctx context.Context, filter FleetListFilter) ([]sqlc.CharlieAgentFleetListRow, error) {
	if s == nil || s.reader == nil {
		return nil, fmt.Errorf("agent fleet telemetry is unavailable")
	}
	if filter.Page < 0 || filter.PageSize < 1 || filter.PageSize > maxFleetPageSize {
		return nil, fmt.Errorf("agent fleet pagination is out of bounds")
	}
	if filter.ConnectionState != "" && filter.ConnectionState != "connected" && filter.ConnectionState != "disconnected" && filter.ConnectionState != "never" {
		return nil, fmt.Errorf("unsupported agent connection state")
	}
	return s.reader.CharlieAgentFleetList(ctx, sqlc.CharlieAgentFleetListParams{
		Environment: optionalText(filter.Environment), Region: optionalText(filter.Region),
		ConnectionState: optionalText(filter.ConnectionState),
		PageOffset:      int32(filter.Page * filter.PageSize), PageLimit: int32(filter.PageSize),
	})
}

func (s *FleetService) Get(ctx context.Context, clusterID uuid.UUID) (sqlc.CharlieAgentFleetGetRow, error) {
	if s == nil || s.reader == nil || clusterID == uuid.Nil {
		return sqlc.CharlieAgentFleetGetRow{}, fmt.Errorf("valid Astronomer cluster connection record is required")
	}
	return s.reader.CharlieAgentFleetGet(ctx, clusterID)
}

type FleetConnectionDetail struct {
	Events []sqlc.AgentConnectionEvent
	Stats  sqlc.CharlieAgentReconnectStatsRow
}

func (s *FleetService) ConnectionHistory(ctx context.Context, clusterID uuid.UUID, since time.Time, limit int) (FleetConnectionDetail, error) {
	if s == nil || s.reader == nil || clusterID == uuid.Nil {
		return FleetConnectionDetail{}, fmt.Errorf("valid Astronomer cluster connection record is required")
	}
	if since.IsZero() || since.Before(time.Now().Add(-30*24*time.Hour)) || limit < 1 || limit > maxFleetHistoryRows {
		return FleetConnectionDetail{}, fmt.Errorf("agent connection history bounds are invalid")
	}
	events, err := s.reader.CharlieAgentConnectionHistory(ctx, sqlc.CharlieAgentConnectionHistoryParams{ClusterID: clusterID, Since: since, RowLimit: int32(limit)})
	if err != nil {
		return FleetConnectionDetail{}, err
	}
	stats, err := s.reader.CharlieAgentReconnectStats(ctx, sqlc.CharlieAgentReconnectStatsParams{ClusterID: clusterID, Since: since})
	if err != nil {
		return FleetConnectionDetail{}, err
	}
	return FleetConnectionDetail{Events: events, Stats: stats}, nil
}

type FleetPattern string

const (
	PatternHealthy   FleetPattern = "healthy"
	PatternSingle    FleetPattern = "single_agent"
	PatternSubset    FleetPattern = "regional_or_environment_subset"
	PatternFleetWide FleetPattern = "fleet_wide"
	PatternScattered FleetPattern = "scattered"
)

// ClassifyFleetPattern distinguishes a single agent, a coherent subset, and a
// simultaneous fleet event using only Astronomer-owned status rows.
func ClassifyFleetPattern(rows []sqlc.CharlieAgentFleetListRow) FleetPattern {
	if len(rows) == 0 {
		return PatternHealthy
	}
	type groupKey struct{ environment, region string }
	groups := map[groupKey]int{}
	affected := 0
	for _, row := range rows {
		unhealthy := row.ConnectionState != "connected" || row.AuthenticationState != "ok" || row.RegistrationState != "registered" ||
			row.AuditIngestionState == "failed" || row.MetricsIngestionState == "failed" || row.StateIngestionState == "failed" ||
			(row.DownstreamApiReachable.Valid && !row.DownstreamApiReachable.Bool)
		if !unhealthy {
			continue
		}
		affected++
		groups[groupKey{environment: row.Environment, region: row.Region}]++
	}
	if affected == 0 {
		return PatternHealthy
	}
	if affected == 1 {
		return PatternSingle
	}
	if affected*100 >= len(rows)*30 {
		return PatternFleetWide
	}
	for _, count := range groups {
		if count >= 2 && count == affected {
			return PatternSubset
		}
	}
	return PatternScattered
}

func optionalText(value string) pgtype.Text {
	if value == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: value, Valid: true}
}
