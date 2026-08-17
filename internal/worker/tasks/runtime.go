package tasks

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/catalog"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	imonitoring "github.com/alphabravocompany/astronomer-go/internal/monitoring"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

const defaultWorkerHTTPTimeout = httpclient.DefaultExternalTimeout

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
	UpdateRestoreOperationStarted(ctx context.Context, id uuid.UUID) (int64, error)
	UpdateRestoreOperationCompleted(ctx context.Context, id uuid.UUID) error
	UpdateRestoreOperationFailed(ctx context.Context, arg sqlc.UpdateRestoreOperationFailedParams) error

	ListClusters(ctx context.Context, arg sqlc.ListClustersParams) ([]sqlc.Cluster, error)
	GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error)
	UpdateClusterStatus(ctx context.Context, arg sqlc.UpdateClusterStatusParams) error
	UpdateClusterStatusOnHeartbeat(ctx context.Context, arg sqlc.UpdateClusterStatusOnHeartbeatParams) (int64, error)
	UpsertClusterHealthStatus(ctx context.Context, arg sqlc.UpsertClusterHealthStatusParams) (sqlc.ClusterHealthStatus, error)
	GetClusterHealthStatus(ctx context.Context, clusterID uuid.UUID) (sqlc.ClusterHealthStatus, error)
	UpsertClusterCondition(ctx context.Context, arg sqlc.UpsertClusterConditionParams) (sqlc.ClusterCondition, error)
	ListClusterConditions(ctx context.Context, clusterID uuid.UUID) ([]sqlc.ClusterCondition, error)
	// Sprint 086 — remediation reconciler. Reads False conditions
	// fleet-wide, writes attempt rows, and re-issues registration
	// tokens for the Connected=False remedy path.
	ListClusterConditionsByStatus(ctx context.Context, status string) ([]sqlc.ClusterCondition, error)
	GetLatestClusterConditionRemediation(ctx context.Context, arg sqlc.GetLatestClusterConditionRemediationParams) (sqlc.ClusterConditionRemediationAttempt, error)
	GetLatestNonSkipClusterConditionRemediation(ctx context.Context, arg sqlc.GetLatestNonSkipClusterConditionRemediationParams) (sqlc.ClusterConditionRemediationAttempt, error)
	InsertClusterConditionRemediation(ctx context.Context, arg sqlc.InsertClusterConditionRemediationParams) (sqlc.ClusterConditionRemediationAttempt, error)
	CountClusterConditionRemediationSinceForType(ctx context.Context, arg sqlc.CountClusterConditionRemediationSinceForTypeParams) (int64, error)
	CreateClusterRegistrationToken(ctx context.Context, arg sqlc.CreateClusterRegistrationTokenParams) (sqlc.ClusterRegistrationToken, error)
	// T8.4 — TemplateApplyStuck remediation. Reads the cluster's
	// template-application row to confirm it is still stuck before
	// resetting to 'failed'; the drift-sweep recovery sweep then
	// re-enqueues.
	GetClusterTemplateApplication(ctx context.Context, clusterID uuid.UUID) (sqlc.ClusterTemplateApplication, error)
	MarkClusterTemplateApplicationStatus(ctx context.Context, arg sqlc.MarkClusterTemplateApplicationStatusParams) (sqlc.ClusterTemplateApplication, error)
	CreateAuditLogV1(ctx context.Context, arg sqlc.CreateAuditLogV1Params) error
	GetDefaultMonitoringBackend(ctx context.Context) (sqlc.MonitoringBackend, error)
	UpsertDefaultMonitoringBackend(ctx context.Context, arg sqlc.UpsertDefaultMonitoringBackendParams) (sqlc.MonitoringBackend, error)
	GetClusterMonitoringConfig(ctx context.Context, clusterID uuid.UUID) (sqlc.ClusterMonitoringConfig, error)
	GetClusterMonitoringContext(ctx context.Context, clusterID uuid.UUID) (sqlc.GetClusterMonitoringContextRow, error)
	UpsertClusterMonitoringConfig(ctx context.Context, arg sqlc.UpsertClusterMonitoringConfigParams) (sqlc.ClusterMonitoringConfig, error)
	ListAlertRules(ctx context.Context, arg sqlc.ListAlertRulesParams) ([]sqlc.AlertRule, error)
	ListAlertRulesByCluster(ctx context.Context, arg sqlc.ListAlertRulesByClusterParams) ([]sqlc.AlertRule, error)
	ListAlertSilences(ctx context.Context, arg sqlc.ListAlertSilencesParams) ([]sqlc.AlertSilence, error)
	ListEnabledAlertInhibitions(ctx context.Context) ([]sqlc.AlertInhibition, error)
	ListChannelsForAlertRule(ctx context.Context, alertRuleID uuid.UUID) ([]sqlc.NotificationChannel, error)
	CreateAlertEvent(ctx context.Context, arg sqlc.CreateAlertEventParams) (sqlc.AlertEvent, error)
	ListAlertEventsByRule(ctx context.Context, arg sqlc.ListAlertEventsByRuleParams) ([]sqlc.AlertEvent, error)
	UpdateAlertEventStatus(ctx context.Context, arg sqlc.UpdateAlertEventStatusParams) error
	GetBackupByID(ctx context.Context, id uuid.UUID) (sqlc.Backup, error)
	GetBackupStorageConfigByID(ctx context.Context, id uuid.UUID) (sqlc.BackupStorageConfig, error)
	UpdateBackupStarted(ctx context.Context, id uuid.UUID) (int64, error)
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

	// Migration 046: telemetry sender + global settings hub.
	GetPlatformSetting(ctx context.Context, key string) (sqlc.PlatformSetting, error)
	CountClusters(ctx context.Context) (int64, error)
	CountUsers(ctx context.Context) (int64, error)
	CountProjects(ctx context.Context) (int64, error)

	// Migration 055: chart ratings + recommendations. The nightly
	// chart_recommendations:recompute task uses these to rebuild the
	// per-chart aggregate and the co-installation matrix.
	ListChartRatingsByChart(ctx context.Context, arg sqlc.ListChartRatingsByChartParams) ([]sqlc.ChartRating, error)
	CountChartRatingsByChart(ctx context.Context, chartID uuid.UUID) (int64, error)
	UpsertChartRatingAggregate(ctx context.Context, arg sqlc.UpsertChartRatingAggregateParams) (sqlc.ChartRatingAggregate, error)
	GetChartRatingAggregate(ctx context.Context, chartID uuid.UUID) (sqlc.ChartRatingAggregate, error)
	ListTopChartsByBayesian(ctx context.Context, arg sqlc.ListTopChartsByBayesianParams) ([]sqlc.ChartRatingAggregate, error)
	ListChartCoInstallationsFor(ctx context.Context, arg sqlc.ListChartCoInstallationsForParams) ([]sqlc.ChartCoInstallation, error)
	TruncateChartCoInstallation(ctx context.Context) error
	UpsertChartCoInstallation(ctx context.Context, arg sqlc.UpsertChartCoInstallationParams) error
	ListInstalledChartChartPairs(ctx context.Context) ([]sqlc.InstalledChartChartPair, error)
	ListDistinctRatedChartIDs(ctx context.Context) ([]uuid.UUID, error)
}

type RuntimeDependencies struct {
	Queries                 RuntimeQuerier
	HTTPClient              *http.Client
	Log                     *slog.Logger
	AgentImageRepo          string
	AgentImageTag           string
	SystemArtifactURL       string
	SystemArtifactDigest    string
	SystemOIDCIssuer        string
	SystemOIDCIdentity      string
	PlatformName            string
	ServerURL               string
	AuditLogRetentionMonths int
	// ClusterTombstoneRetentionDays is how long decommissioned cluster rows
	// (tombstones) are kept before the retention sweep hard-deletes them.
	// Mirrors cfg.ClusterTombstoneRetentionDays; defaults to 90 when unset.
	ClusterTombstoneRetentionDays int
	// RegistrationTokenTTLHours (task A3) is the TTL the Connected=False
	// remediation reissue stamps on the registration token it mints. Mirrors
	// cfg.RegistrationTokenTTLHours; defaults to 1 when unset.
	RegistrationTokenTTLHours int
	Leader                    LeaderElector
	// K8s is the tunnel-backed Kubernetes API requester used by B2 (Velero)
	// and B5 (cis-operator) for CR round-trips. Optional — when nil, those
	// tasks degrade gracefully (e.g. mark the row failed with a clear
	// message).
	K8s K8sRequester
	// Enqueuer hands follow-up tasks to the asynq queue — e.g. the alert
	// evaluator enqueues a notification:send task per fired (rule, channel).
	// Optional — when nil, tasks that would fan out a follow-up log and skip
	// it rather than crash. *asynq.Client satisfies it.
	Enqueuer Enqueuer
	// Bus is the SSE events bus (P4.9). In the dedicated worker process it
	// is a Redis-attached bus with no local subscribers — publishes fan out
	// to the server pods' relays; in the server process it is the shared
	// in-memory bus. Optional and nil-safe: publishers are fire-and-forget.
	Bus *events.Bus
	// CatalogDecryptor unwraps helm_repositories.auth_config_encrypted
	// (migration 145) for the unattended catalog:sync sweep. The interactive
	// handler and this sweep are SEPARATE readers of the same credential, and
	// wiring only one of them is how an encryption-at-rest change ships half
	// done: the operator clicks Sync and it works, then every scheduled sweep
	// 401s. Nil-safe — a nil decryptor reads pre-145 (unsealed) rows fine and
	// declines to authenticate sealed ones (see catalog.ResolveAuthConfig).
	CatalogDecryptor catalog.Decryptor
	// MonitoringCipher seals and unseals monitoring_backends.auth_config_encrypted
	// (migration 146). Unlike CatalogDecryptor this needs BOTH halves: the
	// monitoring reconcile tick does a read-modify-write on the column to
	// stamp the shared-Thanos status, so it must re-seal what it resolved.
	//
	// Nil-safe — a nil cipher reads pre-146 (unsealed) rows fine, declines to
	// authenticate sealed ones, and writes back the pre-146 row shape.
	MonitoringCipher MonitoringCipher
}

// MonitoringCipher is the narrow both-directions surface the monitoring
// credential envelope needs. *auth.Encryptor satisfies it.
type MonitoringCipher interface {
	Encrypt(plaintext string) (string, error)
	Decrypt(token string) (string, error)
}

// MonitoringCipherFor adapts a possibly-nil *auth.Encryptor, for the same
// reason CatalogDecryptorFor does: a nil *auth.Encryptor assigned straight
// into an interface field is a NON-nil interface holding a nil pointer, so
// every `== nil` guard downstream passes and the first call panics.
func MonitoringCipherFor(enc *auth.Encryptor) MonitoringCipher {
	if enc == nil {
		return nil
	}
	return enc
}

// monitoringDecryptor / monitoringEncryptor narrow the configured cipher to
// one direction, preserving the genuinely-nil interface when none is wired.
func monitoringDecryptor() imonitoring.Decryptor {
	if runtimeDeps.MonitoringCipher == nil {
		return nil
	}
	return runtimeDeps.MonitoringCipher
}

func monitoringEncryptor() imonitoring.Encryptor {
	if runtimeDeps.MonitoringCipher == nil {
		return nil
	}
	return runtimeDeps.MonitoringCipher
}

// CatalogDecryptorFor adapts a possibly-nil *auth.Encryptor to the narrow
// interface the catalog credential resolver takes.
//
// The explicit nil is the point: assigning a nil *auth.Encryptor straight into
// an interface field yields a NON-nil interface holding a nil pointer, so
// every `dec == nil` check downstream would pass and the first Decrypt would
// panic on a nil receiver. Both processes that wire this (internal/server and
// cmd/worker) can legitimately have no encryptor in development, so this is a
// live path, not a theoretical one.
func CatalogDecryptorFor(enc *auth.Encryptor) catalog.Decryptor {
	if enc == nil {
		return nil
	}
	return enc
}

// Enqueuer is the narrow asynq surface a task uses to enqueue a follow-up
// task. *asynq.Client satisfies it; tests inject a recording fake.
type Enqueuer interface {
	Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error)
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
		runtimeDeps.HTTPClient = httpclient.New(defaultWorkerHTTPTimeout)
	}
	if runtimeDeps.Log == nil {
		runtimeDeps.Log = slog.Default()
	}
	if runtimeDeps.AgentImageRepo == "" {
		runtimeDeps.AgentImageRepo = "ghcr.io/alphabravo-oss/astronomer-go-agent"
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
	if runtimeDeps.ClusterTombstoneRetentionDays <= 0 {
		runtimeDeps.ClusterTombstoneRetentionDays = defaultClusterTombstoneRetentionDays
	}
	if runtimeDeps.RegistrationTokenTTLHours <= 0 {
		runtimeDeps.RegistrationTokenTTLHours = 1
	}
}

func runtimeHTTPClient() *http.Client {
	if runtimeDeps.HTTPClient != nil {
		return runtimeDeps.HTTPClient
	}
	return httpclient.DefaultExternal()
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
	// Every periodic task emits reconciler_runs_total
	// + last_success_timestamp_seconds + duration_seconds. The start time
	// is captured up front so duration includes the leader-lease acquire
	// plus fn() — that's the full wall-clock time the reconciler "owns"
	// the lane, which is what the stalled-reconciler alert cares about.
	start := time.Now()

	if runtimeDeps.Leader == nil {
		workerLeaderHeld.WithLabelValues(observability.MetricValues(jobName)...).Set(1)
		defer workerLeaderHeld.WithLabelValues(observability.MetricValues(jobName)...).Set(0)
		err := fn()
		observability.RecordReconcilerRun(jobName, reconcilerStatusFor(err), start)
		return err
	}
	release, held, err := runtimeDeps.Leader.TryLeader(ctx, jobName)
	if err != nil {
		workerLeaderHeld.WithLabelValues(observability.MetricValues(jobName)...).Set(0)
		observability.RecordReconcilerRun(jobName, observability.ReconcilerStatusErrored, start)
		return fmt.Errorf("acquire leader lock for %s: %w", jobName, err)
	}
	if !held {
		workerLeaderHeld.WithLabelValues(observability.MetricValues(jobName)...).Set(0)
		runtimeLogger().DebugContext(ctx, "periodic task skipped on non-leader replica", "job", jobName)
		observability.RecordReconcilerRun(jobName, observability.ReconcilerStatusSkipped, start)
		return nil
	}
	workerLeaderHeld.WithLabelValues(observability.MetricValues(jobName)...).Set(1)
	defer workerLeaderHeld.WithLabelValues(observability.MetricValues(jobName)...).Set(0)
	defer release()
	taskErr := fn()
	observability.RecordReconcilerRun(jobName, reconcilerStatusFor(taskErr), start)
	return taskErr
}

// reconcilerStatusFor maps an fn() return to a metric label. nil error =
// succeeded; non-nil = failed. The skipped + errored statuses are reserved
// for the lease-acquisition outcomes and not produced from here.
func reconcilerStatusFor(err error) string {
	if err == nil {
		return observability.ReconcilerStatusSucceeded
	}
	return observability.ReconcilerStatusFailed
}

func emptyUUID() pgtype.UUID {
	return pgtype.UUID{}
}
