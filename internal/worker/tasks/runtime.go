package tasks

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// K8sRequester is the local mirror of handler.K8sRequester used by tasks
// that need to round-trip CRDs through the tunnel (Phase B2 Velero, Phase
// B5 cis-operator). Defined locally to avoid an import cycle between the
// worker/tasks package and the handler package.
type K8sRequester interface {
	Do(ctx context.Context, clusterID, method, path string, body []byte, headers map[string]string) (*protocol.K8sResponsePayload, error)
}

type LeaderElector interface {
	TryLeader(ctx context.Context, jobName string) (release func(), held bool, err error)
}

type RuntimeQuerier interface {
	// Cluster registration token cleanup.
	DeleteExpiredRegistrationTokens(ctx context.Context) (int64, error)
	// Alert event retention cleanup.
	DeleteAlertEventsOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
	// Velero Backup CR identity tracking (Phase B2).
	UpdateBackupVeleroIdentity(ctx context.Context, arg sqlc.UpdateBackupVeleroIdentityParams) error
	TouchBackupPolling(ctx context.Context, id uuid.UUID) error
	// Backup schedule evaluation.
	GetActiveSchedules(ctx context.Context) ([]sqlc.BackupSchedule, error)
	UpdateBackupScheduleLastBackup(ctx context.Context, arg sqlc.UpdateBackupScheduleLastBackupParams) error
	CreateBackup(ctx context.Context, arg sqlc.CreateBackupParams) (sqlc.Backup, error)
	// Backup retention enforcement.
	ListBackups(ctx context.Context, arg sqlc.ListBackupsParams) ([]sqlc.Backup, error)
	ListBackupsByStorage(ctx context.Context, arg sqlc.ListBackupsByStorageParams) ([]sqlc.Backup, error)
	DeleteBackup(ctx context.Context, id uuid.UUID) error
	// Restore execution.
	GetRestoreOperationByID(ctx context.Context, id uuid.UUID) (sqlc.RestoreOperation, error)
	UpdateRestoreOperationStarted(ctx context.Context, id uuid.UUID) error
	UpdateRestoreOperationCompleted(ctx context.Context, id uuid.UUID) error
	UpdateRestoreOperationFailed(ctx context.Context, arg sqlc.UpdateRestoreOperationFailedParams) error

	ListClusters(ctx context.Context, arg sqlc.ListClustersParams) ([]sqlc.Cluster, error)
	GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error)
	UpdateClusterStatus(ctx context.Context, arg sqlc.UpdateClusterStatusParams) error
	UpsertClusterHealthStatus(ctx context.Context, arg sqlc.UpsertClusterHealthStatusParams) (sqlc.ClusterHealthStatus, error)
	GetClusterHealthStatus(ctx context.Context, clusterID uuid.UUID) (sqlc.ClusterHealthStatus, error)
	GetDefaultMonitoringBackend(ctx context.Context) (sqlc.MonitoringBackend, error)
	UpsertDefaultMonitoringBackend(ctx context.Context, arg sqlc.UpsertDefaultMonitoringBackendParams) (sqlc.MonitoringBackend, error)
	GetClusterMonitoringConfig(ctx context.Context, clusterID uuid.UUID) (sqlc.ClusterMonitoringConfig, error)
	GetClusterMonitoringContext(ctx context.Context, clusterID uuid.UUID) (sqlc.GetClusterMonitoringContextRow, error)
	UpsertClusterMonitoringConfig(ctx context.Context, arg sqlc.UpsertClusterMonitoringConfigParams) (sqlc.ClusterMonitoringConfig, error)
	ListAlertRules(ctx context.Context, arg sqlc.ListAlertRulesParams) ([]sqlc.AlertRule, error)
	ListAlertRulesByCluster(ctx context.Context, arg sqlc.ListAlertRulesByClusterParams) ([]sqlc.AlertRule, error)
	ListAlertSilences(ctx context.Context, arg sqlc.ListAlertSilencesParams) ([]sqlc.AlertSilence, error)
	ListChannelsForAlertRule(ctx context.Context, alertRuleID uuid.UUID) ([]sqlc.NotificationChannel, error)
	CreateAlertEvent(ctx context.Context, arg sqlc.CreateAlertEventParams) (sqlc.AlertEvent, error)
	ListAlertEventsByRule(ctx context.Context, arg sqlc.ListAlertEventsByRuleParams) ([]sqlc.AlertEvent, error)
	UpdateAlertEventStatus(ctx context.Context, arg sqlc.UpdateAlertEventStatusParams) error
	GetBackupByID(ctx context.Context, id uuid.UUID) (sqlc.Backup, error)
	GetBackupStorageConfigByID(ctx context.Context, id uuid.UUID) (sqlc.BackupStorageConfig, error)
	UpdateBackupStarted(ctx context.Context, id uuid.UUID) error
	UpdateBackupCompleted(ctx context.Context, arg sqlc.UpdateBackupCompletedParams) error
	UpdateBackupFailed(ctx context.Context, arg sqlc.UpdateBackupFailedParams) error
	CreateSecurityScanResult(ctx context.Context, arg sqlc.CreateSecurityScanResultParams) (sqlc.SecurityScanResult, error)
	ListEnabledHelmRepositories(ctx context.Context) ([]sqlc.HelmRepository, error)
	UpdateHelmRepositoryLastSynced(ctx context.Context, id uuid.UUID) error
	GetHelmChartByRepoAndName(ctx context.Context, arg sqlc.GetHelmChartByRepoAndNameParams) (sqlc.HelmChart, error)
	CreateHelmChart(ctx context.Context, arg sqlc.CreateHelmChartParams) (sqlc.HelmChart, error)
	ListChartsByRepository(ctx context.Context, arg sqlc.ListChartsByRepositoryParams) ([]sqlc.HelmChart, error)
	DeleteHelmChart(ctx context.Context, id uuid.UUID) error
	GetHelmChartVersion(ctx context.Context, arg sqlc.GetHelmChartVersionParams) (sqlc.HelmChartVersion, error)
	CreateHelmChartVersion(ctx context.Context, arg sqlc.CreateHelmChartVersionParams) (sqlc.HelmChartVersion, error)
	ListChartVersions(ctx context.Context, arg sqlc.ListChartVersionsParams) ([]sqlc.HelmChartVersion, error)
	DeleteHelmChartVersion(ctx context.Context, id uuid.UUID) error
}

type RuntimeDependencies struct {
	Queries                 RuntimeQuerier
	HTTPClient              *http.Client
	Log                     *slog.Logger
	AgentImageRepo          string
	AgentImageTag           string
	PlatformName            string
	ServerURL               string
	AuditLogRetentionMonths int
	Leader                  LeaderElector
	// K8s is the tunnel-backed Kubernetes API requester used by B2 (Velero)
	// and B5 (cis-operator) for CR round-trips. Optional — when nil, those
	// tasks degrade gracefully (e.g. mark the row failed with a clear
	// message).
	K8s K8sRequester
}

var runtimeDeps RuntimeDependencies

var workerLeaderHeld = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: "astronomer",
		Name:      "worker_leader_held",
		Help:      "Whether the current Astronomer process holds the advisory lock for a periodic worker job.",
	},
	observability.MetricLabels("job"),
)

func init() {
	prometheus.MustRegister(workerLeaderHeld)
}

func ConfigureRuntime(deps RuntimeDependencies) {
	runtimeDeps = deps
	if runtimeDeps.HTTPClient == nil {
		runtimeDeps.HTTPClient = http.DefaultClient
	}
	if runtimeDeps.Log == nil {
		runtimeDeps.Log = slog.Default()
	}
	if runtimeDeps.AgentImageRepo == "" {
		runtimeDeps.AgentImageRepo = "ghcr.io/alphabravocompany/astronomer-go-agent"
	}
	if runtimeDeps.AgentImageTag == "" {
		runtimeDeps.AgentImageTag = "latest"
	}
	if runtimeDeps.PlatformName == "" {
		runtimeDeps.PlatformName = "Astronomer"
	}
	if runtimeDeps.AuditLogRetentionMonths <= 0 {
		runtimeDeps.AuditLogRetentionMonths = 13
	}
}

func resetRuntime() {
	runtimeDeps = RuntimeDependencies{}
}

func runtimeLogger() *slog.Logger {
	if runtimeDeps.Log != nil {
		return runtimeDeps.Log
	}
	return slog.Default()
}

func runPeriodicTaskWithLeader(ctx context.Context, jobName string, fn func() error) error {
	if runtimeDeps.Leader == nil {
		workerLeaderHeld.WithLabelValues(observability.MetricValues(jobName)...).Set(1)
		defer workerLeaderHeld.WithLabelValues(observability.MetricValues(jobName)...).Set(0)
		return fn()
	}
	release, held, err := runtimeDeps.Leader.TryLeader(ctx, jobName)
	if err != nil {
		workerLeaderHeld.WithLabelValues(observability.MetricValues(jobName)...).Set(0)
		return fmt.Errorf("acquire leader lock for %s: %w", jobName, err)
	}
	if !held {
		workerLeaderHeld.WithLabelValues(observability.MetricValues(jobName)...).Set(0)
		runtimeLogger().DebugContext(ctx, "periodic task skipped on non-leader replica", "job", jobName)
		return nil
	}
	workerLeaderHeld.WithLabelValues(observability.MetricValues(jobName)...).Set(1)
	defer workerLeaderHeld.WithLabelValues(observability.MetricValues(jobName)...).Set(0)
	defer release()
	return fn()
}

func emptyUUID() pgtype.UUID {
	return pgtype.UUID{}
}
