package quota

import (
	"context"
	"log/slog"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

// ReporterQuerier is the read-only DB surface used by the periodic
// usage refresher. *sqlc.Queries satisfies it.
type ReporterQuerier interface {
	ListProjectQuotaSnapshots(ctx context.Context, arg sqlc.ListProjectQuotaSnapshotsParams) ([]sqlc.ProjectQuotaSnapshotRow, error)
	ListUserQuotaSnapshots(ctx context.Context, arg sqlc.ListUserQuotaSnapshotsParams) ([]sqlc.UserQuotaSnapshotRow, error)
	CountTotalClusters(ctx context.Context) (int64, error)
	CountTotalActiveUsers(ctx context.Context) (int64, error)
	GetQuotaPlan(ctx context.Context, name string) (sqlc.QuotaPlan, error)
}

// defaultReporterInterval is the cadence at which the gauge series is
// refreshed for tenants that aren't otherwise mutating. Five minutes is
// the sweet spot: long enough that the Postgres scan-and-aggregate is a
// trivial portion of the server's load budget, short enough that the
// "you're at 80% of cap" dashboard banner is responsive.
const defaultReporterInterval = 5 * time.Minute

// reporterPageSize is the per-call batch when paging through the
// snapshot queries. 200 keeps Postgres happy and stays under the wire-
// payload budget on installations with thousands of tenants.
const reporterPageSize int32 = 200

// StartReporter spawns a background goroutine that walks
// (projects, users, global) and refreshes the per-tenant usage gauge.
// Returns immediately; cancel ctx to stop. Safe to call once at boot.
func StartReporter(ctx context.Context, queries ReporterQuerier, log *slog.Logger) {
	if queries == nil {
		return
	}
	if log == nil {
		log = slog.Default()
	}
	MustRegister()

	tick := time.NewTicker(defaultReporterInterval)

	go func() {
		defer tick.Stop()
		// Run once immediately so the gauge series populates on first
		// scrape rather than waiting for the first tick.
		runOnce(ctx, queries, log)
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				runOnce(ctx, queries, log)
			}
		}
	}()
	log.Debug("started quota usage reporter", "interval", defaultReporterInterval)
}

func runOnce(ctx context.Context, queries ReporterQuerier, log *slog.Logger) {
	offset := int32(0)
	for {
		rows, err := queries.ListProjectQuotaSnapshots(ctx, sqlc.ListProjectQuotaSnapshotsParams{
			Limit:  reporterPageSize,
			Offset: offset,
		})
		if err != nil {
			log.Warn("quota reporter: list project snapshots failed", "error", err)
			break
		}
		for _, r := range rows {
			reportProjectRow(r)
		}
		if int32(len(rows)) < reporterPageSize {
			break
		}
		offset += reporterPageSize
	}

	offset = 0
	for {
		rows, err := queries.ListUserQuotaSnapshots(ctx, sqlc.ListUserQuotaSnapshotsParams{
			Limit:  reporterPageSize,
			Offset: offset,
		})
		if err != nil {
			log.Warn("quota reporter: list user snapshots failed", "error", err)
			break
		}
		for _, r := range rows {
			reportUserRow(r)
		}
		if int32(len(rows)) < reporterPageSize {
			break
		}
		offset += reporterPageSize
	}

	plan, err := queries.GetQuotaPlan(ctx, "global")
	if err == nil {
		if c, err := queries.CountTotalClusters(ctx); err == nil {
			setUsage("global", "max_total_clusters", c, plan.MaxTotalClusters)
		}
		if c, err := queries.CountTotalActiveUsers(ctx); err == nil {
			setUsage("global", "max_total_users", c, plan.MaxTotalUsers)
		}
	}
}

func reportProjectRow(r sqlc.ProjectQuotaSnapshotRow) {
	subj := "project:" + r.ProjectID.String()
	setUsage(subj, "max_clusters_per_project", r.ClustersInProject, r.MaxClustersPerProject)
	setUsage(subj, "max_namespaces_per_project", r.NamespacesInProject, r.MaxNamespacesPerProject)
	setUsage(subj, "max_members_per_project", r.MembersInProject, r.MaxMembersPerProject)
}

func reportUserRow(r sqlc.UserQuotaSnapshotRow) {
	subj := "user:" + r.UserID.String()
	setUsage(subj, "max_projects_per_user", r.ProjectsForUser, r.MaxProjectsPerUser)
	setUsage(subj, "max_tokens_per_user", r.TokensForUser, r.MaxTokensPerUser)
}

// setUsage updates the gauge with a percentage. 0 max = unlimited; we
// publish 0% so the series is present but obviously "no cap".
func setUsage(subject, limit string, current int64, max int32) {
	if max <= 0 {
		quotaUsagePct.WithLabelValues(observability.MetricValues(subject, limit)...).Set(0)
		return
	}
	pct := float64(current) / float64(max) * 100
	quotaUsagePct.WithLabelValues(observability.MetricValues(subject, limit)...).Set(pct)
}
