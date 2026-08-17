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
	maxClusterAgentPageSize    = 100
	maxClusterAgentHistoryRows = 100
)

// ClusterAgentHealthReader is deliberately database-shaped. It has no tunnel,
// Kubernetes requester, proxy, or generic transport method, making it
// impossible for these capabilities to cross into a downstream cluster.
type ClusterAgentHealthReader interface {
	CharlieClusterAgentSummary(context.Context, int32) (sqlc.CharlieClusterAgentSummaryRow, error)
	CharlieClusterAgentList(context.Context, sqlc.CharlieClusterAgentListParams) ([]sqlc.CharlieClusterAgentListRow, error)
	CharlieClusterAgentGet(context.Context, uuid.UUID) (sqlc.CharlieClusterAgentGetRow, error)
	CharlieAgentConnectionHistory(context.Context, sqlc.CharlieAgentConnectionHistoryParams) ([]sqlc.AgentConnectionEvent, error)
	CharlieAgentReconnectStats(context.Context, sqlc.CharlieAgentReconnectStatsParams) (sqlc.CharlieAgentReconnectStatsRow, error)
	CharlieTunnelReplicaDistribution(context.Context) ([]sqlc.CharlieTunnelReplicaDistributionRow, error)
	CharlieTunnelRecentErrors(context.Context, sqlc.CharlieTunnelRecentErrorsParams) ([]sqlc.TunnelLocatorEvent, error)
	CharlieTunnelHealth(context.Context, time.Time) (sqlc.CharlieTunnelHealthRow, error)
}

type ClusterAgentHealthService struct {
	reader ClusterAgentHealthReader
}

func NewClusterAgentHealthService(reader ClusterAgentHealthReader) *ClusterAgentHealthService {
	if reader == nil {
		return nil
	}
	return &ClusterAgentHealthService{reader: reader}
}

func (s *ClusterAgentHealthService) Summary(ctx context.Context, staleAfter time.Duration) (sqlc.CharlieClusterAgentSummaryRow, error) {
	if s == nil || s.reader == nil {
		return sqlc.CharlieClusterAgentSummaryRow{}, fmt.Errorf("cluster-agent health telemetry is unavailable")
	}
	if staleAfter < time.Minute || staleAfter > 24*time.Hour {
		return sqlc.CharlieClusterAgentSummaryRow{}, fmt.Errorf("stale heartbeat threshold must be between one minute and 24 hours")
	}
	return s.reader.CharlieClusterAgentSummary(ctx, int32(staleAfter/time.Second))
}

type ClusterAgentListFilter struct {
	Environment     string
	Region          string
	ConnectionState string
	Page            int
	PageSize        int
}

func (s *ClusterAgentHealthService) List(ctx context.Context, filter ClusterAgentListFilter) ([]sqlc.CharlieClusterAgentListRow, error) {
	if s == nil || s.reader == nil {
		return nil, fmt.Errorf("cluster-agent health telemetry is unavailable")
	}
	if filter.Page < 0 || filter.PageSize < 1 || filter.PageSize > maxClusterAgentPageSize {
		return nil, fmt.Errorf("cluster-agent pagination is out of bounds")
	}
	if filter.ConnectionState != "" && filter.ConnectionState != "connected" && filter.ConnectionState != "disconnected" && filter.ConnectionState != "never" {
		return nil, fmt.Errorf("unsupported agent connection state")
	}
	return s.reader.CharlieClusterAgentList(ctx, sqlc.CharlieClusterAgentListParams{
		Environment: optionalText(filter.Environment), Region: optionalText(filter.Region),
		ConnectionState: optionalText(filter.ConnectionState),
		PageOffset:      int32(filter.Page * filter.PageSize), PageLimit: int32(filter.PageSize),
	})
}

func (s *ClusterAgentHealthService) Get(ctx context.Context, clusterID uuid.UUID) (sqlc.CharlieClusterAgentGetRow, error) {
	if s == nil || s.reader == nil || clusterID == uuid.Nil {
		return sqlc.CharlieClusterAgentGetRow{}, fmt.Errorf("valid Astronomer cluster connection record is required")
	}
	return s.reader.CharlieClusterAgentGet(ctx, clusterID)
}

type ClusterAgentConnectionDetail struct {
	Events []sqlc.AgentConnectionEvent
	Stats  sqlc.CharlieAgentReconnectStatsRow
}

func (s *ClusterAgentHealthService) ConnectionHistory(ctx context.Context, clusterID uuid.UUID, since time.Time, limit int) (ClusterAgentConnectionDetail, error) {
	if s == nil || s.reader == nil || clusterID == uuid.Nil {
		return ClusterAgentConnectionDetail{}, fmt.Errorf("valid Astronomer cluster connection record is required")
	}
	if since.IsZero() || since.Before(time.Now().Add(-30*24*time.Hour)) || limit < 1 || limit > maxClusterAgentHistoryRows {
		return ClusterAgentConnectionDetail{}, fmt.Errorf("agent connection history bounds are invalid")
	}
	events, err := s.reader.CharlieAgentConnectionHistory(ctx, sqlc.CharlieAgentConnectionHistoryParams{ClusterID: clusterID, Since: since, RowLimit: int32(limit)})
	if err != nil {
		return ClusterAgentConnectionDetail{}, err
	}
	stats, err := s.reader.CharlieAgentReconnectStats(ctx, sqlc.CharlieAgentReconnectStatsParams{ClusterID: clusterID, Since: since})
	if err != nil {
		return ClusterAgentConnectionDetail{}, err
	}
	return ClusterAgentConnectionDetail{Events: events, Stats: stats}, nil
}

type ClusterAgentPattern string

const (
	PatternHealthy      ClusterAgentPattern = "healthy"
	PatternSingle       ClusterAgentPattern = "single_agent"
	PatternSubset       ClusterAgentPattern = "regional_or_environment_subset"
	PatternMultiCluster ClusterAgentPattern = "multi_cluster"
	PatternScattered    ClusterAgentPattern = "scattered"
)

// ClassifyClusterAgentPattern distinguishes a single agent, a coherent subset,
// and a simultaneous multi-cluster event using only Astronomer-owned status rows.
func ClassifyClusterAgentPattern(rows []sqlc.CharlieClusterAgentListRow) ClusterAgentPattern {
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
		return PatternMultiCluster
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
