package worker

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	allowlistproviders "github.com/alphabravocompany/astronomer-go/internal/apisvr/allowlist/providers"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/charlie"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/email"
	"github.com/alphabravocompany/astronomer-go/internal/webhook"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgtype"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

type workerDeliveryStub struct{}

func (*workerDeliveryStub) ResolveOne(context.Context, uuid.UUID) error   { return nil }
func (*workerDeliveryStub) ReconcileOne(context.Context, uuid.UUID) error { return nil }
func (*workerDeliveryStub) Sweep(context.Context, int) error              { return nil }

type workerDispatchQueries struct{}

type workerAdminQueueInspector struct{}

func (workerAdminQueueInspector) GetTaskInfo(string, string) (*asynq.TaskInfo, error) {
	return &asynq.TaskInfo{State: asynq.TaskStatePending}, nil
}
func (workerAdminQueueInspector) RunTask(string, string) error    { return nil }
func (workerAdminQueueInspector) DeleteTask(string, string) error { return nil }

func (*workerDispatchQueries) ListQueuedEmails(context.Context, int32) ([]sqlc.EmailMessage, error) {
	return nil, nil
}
func (*workerDispatchQueries) MarkEmailSent(context.Context, sqlc.MarkEmailSentParams) error {
	return nil
}
func (*workerDispatchQueries) MarkEmailFailed(context.Context, sqlc.MarkEmailFailedParams) error {
	return nil
}
func (*workerDispatchQueries) MarkEmailSkipped(context.Context, sqlc.MarkEmailSkippedParams) error {
	return nil
}
func (*workerDispatchQueries) DeleteEmailsOlderThan(context.Context, time.Time) (int64, error) {
	return 0, nil
}
func (*workerDispatchQueries) DeleteExpiredPasswordResetTokens(context.Context, time.Time) (int64, error) {
	return 0, nil
}
func (*workerDispatchQueries) ListPendingWebhookDeliveries(context.Context, sqlc.ListPendingWebhookDeliveriesParams) ([]sqlc.WebhookDelivery, error) {
	return nil, nil
}
func (*workerDispatchQueries) GetWebhookSubscription(context.Context, uuid.UUID) (sqlc.WebhookSubscription, error) {
	return sqlc.WebhookSubscription{}, nil
}
func (*workerDispatchQueries) MarkWebhookDeliveryDelivered(context.Context, sqlc.MarkWebhookDeliveryDeliveredParams) error {
	return nil
}
func (*workerDispatchQueries) MarkWebhookDeliveryFailed(context.Context, sqlc.MarkWebhookDeliveryFailedParams) error {
	return nil
}
func (*workerDispatchQueries) MarkWebhookDeliveryDropped(context.Context, sqlc.MarkWebhookDeliveryDroppedParams) error {
	return nil
}
func (*workerDispatchQueries) DeleteWebhookDeliveriesOlderThan(context.Context, time.Time) (int64, error) {
	return 0, nil
}
func (*workerDispatchQueries) ListEnabledSIEMForwarders(context.Context) ([]sqlc.SiemForwarder, error) {
	return nil, nil
}
func (*workerDispatchQueries) ListSIEMQueueBatch(context.Context, sqlc.ListSIEMQueueBatchParams) ([]sqlc.SiemForwardQueue, error) {
	return nil, nil
}
func (*workerDispatchQueries) ListSIEMQueueExhausted(context.Context, sqlc.ListSIEMQueueExhaustedParams) ([]sqlc.SiemForwardQueue, error) {
	return nil, nil
}
func (*workerDispatchQueries) DeleteSIEMQueueByIDs(context.Context, []int64) error { return nil }
func (*workerDispatchQueries) MarkSIEMTestOperationsSucceededByQueueIDs(context.Context, []int64) error {
	return nil
}
func (*workerDispatchQueries) MarkSIEMTestOperationsFailedByQueueIDs(context.Context, []int64, string) error {
	return nil
}
func (*workerDispatchQueries) IncrementSIEMQueueAttempts(context.Context, []int64) error {
	return nil
}
func (*workerDispatchQueries) CountSIEMQueueByForwarder(context.Context, uuid.UUID) (int64, error) {
	return 0, nil
}
func (*workerDispatchQueries) UpsertSIEMForwarderStatus(context.Context, sqlc.UpsertSIEMForwarderStatusParams) error {
	return nil
}
func (*workerDispatchQueries) DeleteSIEMQueueOlderThan(context.Context, time.Time) (int64, error) {
	return 0, nil
}
func (*workerDispatchQueries) ClaimDueTaskOutbox(context.Context, sqlc.ClaimDueTaskOutboxParams) ([]sqlc.TaskOutbox, error) {
	return nil, nil
}
func (*workerDispatchQueries) MarkTaskOutboxDelivered(context.Context, sqlc.MarkTaskOutboxDeliveredParams) error {
	return nil
}
func (*workerDispatchQueries) MarkTaskOutboxFailed(context.Context, sqlc.MarkTaskOutboxFailedParams) error {
	return nil
}
func (*workerDispatchQueries) ResetExpiredAuditOutboxLeases(context.Context, time.Time) (int64, error) {
	return 0, nil
}
func (*workerDispatchQueries) ClaimDueAuditOutbox(context.Context, sqlc.ClaimDueAuditOutboxParams) ([]sqlc.AuditOutbox, error) {
	return nil, nil
}

func (*workerDispatchQueries) ClaimAdminQueueOperation(context.Context, sqlc.ClaimAdminQueueOperationParams) (sqlc.AdminQueueOperation, error) {
	return sqlc.AdminQueueOperation{ID: uuid.New(), Action: "retry", Status: "running"}, nil
}
func (*workerDispatchQueries) GetAdminQueueOperation(context.Context, uuid.UUID) (sqlc.AdminQueueOperation, error) {
	return sqlc.AdminQueueOperation{ID: uuid.New(), Action: "retry", Status: "succeeded"}, nil
}
func (*workerDispatchQueries) MarkAdminQueueOperationEffectStarted(context.Context, uuid.UUID) (sqlc.AdminQueueOperation, error) {
	return sqlc.AdminQueueOperation{ID: uuid.New(), Action: "retry", Status: "running"}, nil
}
func (*workerDispatchQueries) MarkAdminQueueOperationSucceeded(context.Context, uuid.UUID) (sqlc.AdminQueueOperation, error) {
	return sqlc.AdminQueueOperation{ID: uuid.New(), Action: "retry", Status: "succeeded"}, nil
}
func (*workerDispatchQueries) MarkAdminQueueOperationFailed(context.Context, sqlc.MarkAdminQueueOperationFailedParams) (sqlc.AdminQueueOperation, error) {
	return sqlc.AdminQueueOperation{ID: uuid.New(), Action: "retry", Status: "failed"}, nil
}
func (*workerDispatchQueries) MarkAdminQueueOperationRetrying(context.Context, sqlc.MarkAdminQueueOperationRetryingParams) (sqlc.AdminQueueOperation, error) {
	return sqlc.AdminQueueOperation{ID: uuid.New(), Action: "retry", Status: "retrying"}, nil
}
func (*workerDispatchQueries) DeliverAuditOutbox(context.Context, sqlc.DeliverAuditOutboxParams) (sqlc.DeliverAuditOutboxRow, error) {
	return sqlc.DeliverAuditOutboxRow{}, nil
}
func (*workerDispatchQueries) MarkAuditOutboxFailed(context.Context, sqlc.MarkAuditOutboxFailedParams) (sqlc.AuditOutbox, error) {
	return sqlc.AuditOutbox{}, nil
}
func (*workerDispatchQueries) ClaimCharlieAlertDelivery(context.Context, uuid.UUID) (sqlc.CharlieAlertDelivery, error) {
	return sqlc.CharlieAlertDelivery{}, nil
}
func (*workerDispatchQueries) GetCharlieAlertDelivery(context.Context, uuid.UUID) (sqlc.CharlieAlertDelivery, error) {
	return sqlc.CharlieAlertDelivery{}, nil
}
func (*workerDispatchQueries) CharlieAlertDeliveryAllowed(context.Context, uuid.UUID) (bool, error) {
	return true, nil
}
func (*workerDispatchQueries) GetCharlieFinding(context.Context, uuid.UUID) (sqlc.CharlieFinding, error) {
	return sqlc.CharlieFinding{}, nil
}
func (*workerDispatchQueries) GetNotificationChannelByID(context.Context, uuid.UUID) (sqlc.NotificationChannel, error) {
	return sqlc.NotificationChannel{}, nil
}
func (*workerDispatchQueries) MarkCharlieAlertDeliveryDelivered(context.Context, uuid.UUID) error {
	return nil
}
func (*workerDispatchQueries) MarkCharlieAlertDeliveryRetry(context.Context, sqlc.MarkCharlieAlertDeliveryRetryParams) error {
	return nil
}
func (*workerDispatchQueries) SuppressCharlieAlertDelivery(context.Context, sqlc.SuppressCharlieAlertDeliveryParams) error {
	return nil
}
func (*workerDispatchQueries) ListClustersDueForAgentTokenRotation(context.Context, int32) ([]sqlc.ListClustersDueForAgentTokenRotationRow, error) {
	return nil, nil
}
func (*workerDispatchQueries) SetClusterAgentTokenRotationPending(context.Context, uuid.UUID) (int64, error) {
	return 0, nil
}
func (*workerDispatchQueries) ClearExpiredAgentTokenRotationGrace(context.Context, int32) (int64, error) {
	return 0, nil
}
func (*workerDispatchQueries) ListBackupStorageConfigs(context.Context, sqlc.ListBackupStorageConfigsParams) ([]sqlc.BackupStorageConfig, error) {
	return nil, nil
}
func (*workerDispatchQueries) UpdateBackupStorageConfig(context.Context, sqlc.UpdateBackupStorageConfigParams) (sqlc.BackupStorageConfig, error) {
	return sqlc.BackupStorageConfig{}, nil
}
func (*workerDispatchQueries) ListAllClusterRegistryConfigs(context.Context) ([]sqlc.ClusterRegistryConfig, error) {
	return nil, nil
}
func (*workerDispatchQueries) UpdateClusterRegistryConfig(context.Context, sqlc.UpdateClusterRegistryConfigParams) (sqlc.ClusterRegistryConfig, error) {
	return sqlc.ClusterRegistryConfig{}, nil
}
func (*workerDispatchQueries) ListHelmRepositoriesWithLegacyAuthConfig(context.Context, sqlc.ListHelmRepositoriesWithLegacyAuthConfigParams) ([]sqlc.HelmRepository, error) {
	return nil, nil
}
func (*workerDispatchQueries) SealHelmRepositoryAuthConfig(context.Context, sqlc.SealHelmRepositoryAuthConfigParams) error {
	return nil
}
func (*workerDispatchQueries) ListMonitoringBackendsWithLegacyAuthConfig(context.Context, sqlc.ListMonitoringBackendsWithLegacyAuthConfigParams) ([]sqlc.MonitoringBackend, error) {
	return nil, nil
}
func (*workerDispatchQueries) SealMonitoringBackendAuthConfig(context.Context, sqlc.SealMonitoringBackendAuthConfigParams) error {
	return nil
}
func (*workerDispatchQueries) ListCRDOwnedClusters(context.Context, int32) ([]sqlc.FleetOwnership, error) {
	return nil, nil
}
func (*workerDispatchQueries) UpsertClusterCondition(context.Context, sqlc.UpsertClusterConditionParams) (sqlc.ClusterCondition, error) {
	return sqlc.ClusterCondition{}, nil
}
func (*workerDispatchQueries) GetApiserverAllowlistByClusterID(context.Context, uuid.UUID) (sqlc.ApiserverAllowlist, error) {
	return sqlc.ApiserverAllowlist{}, nil
}
func (*workerDispatchQueries) ListActiveApiserverAllowlists(context.Context) ([]sqlc.ApiserverAllowlist, error) {
	return nil, nil
}
func (*workerDispatchQueries) UpdateApiserverAllowlistReconcileState(context.Context, sqlc.UpdateApiserverAllowlistReconcileStateParams) error {
	return nil
}
func (*workerDispatchQueries) InsertApiserverAllowlistSnapshot(context.Context, sqlc.InsertApiserverAllowlistSnapshotParams) (sqlc.ApiserverAllowlistSnapshot, error) {
	return sqlc.ApiserverAllowlistSnapshot{}, nil
}
func (*workerDispatchQueries) DeleteApiserverAllowlistSnapshotsOlderThan(context.Context, time.Time) error {
	return nil
}
func (*workerDispatchQueries) GetClusterByID(context.Context, uuid.UUID) (sqlc.Cluster, error) {
	return sqlc.Cluster{}, nil
}
func (*workerDispatchQueries) GetClusterByName(context.Context, string) (sqlc.Cluster, error) {
	return sqlc.Cluster{}, nil
}
func (*workerDispatchQueries) CreateCluster(context.Context, sqlc.CreateClusterParams) (sqlc.Cluster, error) {
	return sqlc.Cluster{}, nil
}
func (*workerDispatchQueries) UpdateCluster(context.Context, sqlc.UpdateClusterParams) (sqlc.Cluster, error) {
	return sqlc.Cluster{}, nil
}
func (*workerDispatchQueries) GetClusterTemplateByName(context.Context, string) (sqlc.ClusterTemplate, error) {
	return sqlc.ClusterTemplate{}, nil
}
func (*workerDispatchQueries) UpsertClusterTemplateApplication(context.Context, sqlc.UpsertClusterTemplateApplicationParams) (sqlc.ClusterTemplateApplication, error) {
	return sqlc.ClusterTemplateApplication{}, nil
}
func (*workerDispatchQueries) UpsertGitOpsRegisteredCluster(context.Context, sqlc.UpsertGitOpsRegisteredClusterParams) (sqlc.GitopsRegisteredCluster, error) {
	return sqlc.GitopsRegisteredCluster{}, nil
}
func (*workerDispatchQueries) ListEnabledGitOpsSources(context.Context) ([]sqlc.GitopsRegistrationSource, error) {
	return nil, nil
}
func (*workerDispatchQueries) GetGitOpsSource(context.Context, uuid.UUID) (sqlc.GitopsRegistrationSource, error) {
	return sqlc.GitopsRegistrationSource{}, nil
}
func (*workerDispatchQueries) ListGitOpsRegisteredClustersBySource(context.Context, uuid.UUID) ([]sqlc.GitopsRegisteredCluster, error) {
	return nil, nil
}
func (*workerDispatchQueries) TombstoneGitOpsRegisteredCluster(context.Context, sqlc.TombstoneGitOpsRegisteredClusterParams) error {
	return nil
}
func (*workerDispatchQueries) DeleteGitOpsRegisteredCluster(context.Context, uuid.UUID) error {
	return nil
}
func (*workerDispatchQueries) StampGitOpsSourceSync(context.Context, sqlc.StampGitOpsSourceSyncParams) error {
	return nil
}
func (*workerDispatchQueries) StampGitOpsSourceError(context.Context, sqlc.StampGitOpsSourceErrorParams) error {
	return nil
}
func (*workerDispatchQueries) ConsumeGitOpsMassDecommissionOverride(context.Context, uuid.UUID) error {
	return nil
}
func (*workerDispatchQueries) ListExpiredTombstones(context.Context, pgtype.Timestamptz) ([]sqlc.GitopsRegisteredCluster, error) {
	return nil, nil
}
func (*workerDispatchQueries) CountGitOpsRegisteredClustersBySource(context.Context, uuid.UUID) (int64, error) {
	return 0, nil
}
func (*workerDispatchQueries) CountGitOpsTombstonedBySource(context.Context, uuid.UUID) (int64, error) {
	return 0, nil
}
func (*workerDispatchQueries) CreateClusterDecommission(context.Context, sqlc.CreateClusterDecommissionParams) (sqlc.ClusterDecommission, error) {
	return sqlc.ClusterDecommission{}, nil
}
func (*workerDispatchQueries) CreateAuditLogV1(context.Context, sqlc.CreateAuditLogV1Params) error {
	return nil
}
func (*workerDispatchQueries) UpsertTaskOutbox(context.Context, sqlc.UpsertTaskOutboxParams) (sqlc.TaskOutbox, error) {
	return sqlc.TaskOutbox{}, nil
}

type workerAlertReconciler struct{}

func (workerAlertReconciler) Reconcile(context.Context) error { return nil }

type workerEmailSender struct{}

func (workerEmailSender) Send(context.Context, email.Message) error { return nil }

type workerEmailProvider struct{}

func (workerEmailProvider) Provide(context.Context) (email.Settings, error) {
	return email.Settings{Enabled: true}, nil
}

type workerWebhookSender struct{}

func (workerWebhookSender) Send(context.Context, webhook.Subscription, webhook.Event) (webhook.Outcome, int, error) {
	return webhook.Outcome{Status: http.StatusOK}, 0, nil
}

type workerTaskEnqueuer struct{}

func (workerTaskEnqueuer) EnqueueContext(context.Context, *asynq.Task, ...asynq.Option) (*asynq.TaskInfo, error) {
	return &asynq.TaskInfo{ID: "test"}, nil
}
func (workerTaskEnqueuer) Enqueue(*asynq.Task, ...asynq.Option) (*asynq.TaskInfo, error) {
	return &asynq.TaskInfo{ID: "test"}, nil
}

type workerToolDriftQueries struct{}

func (workerToolDriftQueries) ListInstalledChartsForDriftSweep(context.Context, int32) ([]sqlc.InstalledChart, error) {
	return nil, nil
}
func (workerToolDriftQueries) MarkInstalledChartDrift(context.Context, sqlc.MarkInstalledChartDriftParams) error {
	return nil
}

type workerHelmStatusProber struct{}

func (workerHelmStatusProber) Status(context.Context, string, string, string) (*protocol.HelmResultPayload, error) {
	return &protocol.HelmResultPayload{Status: "deployed"}, nil
}

type workerClusterTemplateQueries struct {
	tasks.ClusterTemplateApplyQuerier
}

func (workerClusterTemplateQueries) ListClusterTemplateApplicationsByStatus(context.Context, sqlc.ListClusterTemplateApplicationsByStatusParams) ([]sqlc.ClusterTemplateApplication, error) {
	return nil, nil
}

type workerToolInstaller struct{}

func (workerToolInstaller) EnsureInstalled(context.Context, uuid.UUID, string, string, string, string) (sqlc.InstalledChart, error) {
	return sqlc.InstalledChart{}, nil
}

type workerK8sRequester struct{ tasks.K8sRequester }

type workerNetworkPolicyQueries struct{ tasks.NetworkPolicyQuerier }

func (workerNetworkPolicyQueries) ListPendingNetworkPolicyApplications(context.Context, int32) ([]sqlc.NetworkPolicyApplication, error) {
	return nil, nil
}
func (workerNetworkPolicyQueries) ListAppliedNetworkPolicyApplications(context.Context, int32) ([]sqlc.NetworkPolicyApplication, error) {
	return nil, nil
}

type workerMeshQueries struct{ tasks.MeshDetectQuerier }

func (workerMeshQueries) ListClusters(context.Context, sqlc.ListClustersParams) ([]sqlc.Cluster, error) {
	return nil, nil
}

type workerProjectRequester struct{ tasks.ProjectK8sRequester }

type workerCloudCredentialQueries struct{ tasks.CloudCredentialQuerier }

func (workerCloudCredentialQueries) ListAllPendingCloudCredentialMaterializations(context.Context) ([]sqlc.CloudCredentialMaterialization, error) {
	return nil, nil
}

type workerClusterRegistryQueries struct {
	tasks.ClusterRegistryApplyQuerier
}

func (workerClusterRegistryQueries) ListAllClusterRegistryConfigs(context.Context) ([]sqlc.ClusterRegistryConfig, error) {
	return nil, nil
}

type workerProjectQueries struct{ tasks.ProjectReconcileQuerier }

func (workerProjectQueries) ListAllProjectNamespaces(context.Context) ([]sqlc.ProjectNamespace, error) {
	return nil, nil
}

type workerClusterSnapshotQueries struct {
	tasks.ClusterSnapshotPollQuerier
}

func (workerClusterSnapshotQueries) ListPendingClusterSnapshots(context.Context, int32) ([]sqlc.ClusterSnapshot, error) {
	return nil, nil
}
func (workerClusterSnapshotQueries) ListPendingClusterRestores(context.Context, int32) ([]sqlc.ClusterRestore, error) {
	return nil, nil
}
func (workerClusterSnapshotQueries) ListEnabledSnapshotSchedules(context.Context) ([]sqlc.ClusterSnapshotSchedule, error) {
	return nil, nil
}
func (workerClusterSnapshotQueries) ListExpiredTerminalSnapshots(context.Context, int32) ([]sqlc.ClusterSnapshot, error) {
	return nil, nil
}

type workerVeleroDriver struct{ tasks.VeleroSnapshotDriver }

type workerDecommissionQueries struct {
	tasks.ClusterDecommissionQuerier
}

func (workerDecommissionQueries) ListPendingClusterDecommissions(context.Context, int32) ([]sqlc.ClusterDecommission, error) {
	return nil, nil
}

type workerDecommissionTunnel struct{ tasks.DecommissionTunnel }

type workerRBACCache struct{}

func (workerRBACCache) InvalidateAll() {}

type workerDeferredQueries struct{ tasks.DeferredDispatchQuerier }

func (workerDeferredQueries) ListPendingDeferredOperations(context.Context, sqlc.ListPendingDeferredOperationsParams) ([]sqlc.DeferredOperation, error) {
	return nil, nil
}

func workerDeferredReplayers() map[string]tasks.DeferredReplayer {
	replayers := make(map[string]tasks.DeferredReplayer)
	for _, operationType := range []string{
		"cluster.delete", "project.delete", "tool.install", "tool.upgrade",
		"tool.uninstall", "helm.install", "helm.uninstall", "cluster_template.apply",
	} {
		replayers[operationType] = func(context.Context, sqlc.DeferredOperation) error { return nil }
	}
	return replayers
}

type workerSecurityQueries struct{ tasks.SecurityIngestQuerier }

func (workerSecurityQueries) ListRecoverableSecurityScans(context.Context, sqlc.ListRecoverableSecurityScansParams) ([]sqlc.SecurityScanResult, error) {
	return nil, nil
}

type workerSecurityFetcher struct{ tasks.SecurityIngestK8sFetcher }
type workerSecurityOutbox struct{ tasks.TaskOutboxWriter }

type workerLeaderElector struct{}

func (workerLeaderElector) TryLeader(context.Context, string) (func(), bool, error) {
	return func() {}, true, nil
}

type workerRuntimeQueries struct{ tasks.RuntimeQuerier }
type workerDexQueries struct{ tasks.DexOperationQuerier }
type workerDexExecutor struct{ tasks.DexOperationExecutor }

func (workerDexQueries) RecoverDexOperationOutbox(context.Context, time.Time) (int64, error) {
	return 0, nil
}

type workerManagementBackupExecutor struct{}

func (workerManagementBackupExecutor) ManagementBackupReady() bool { return true }

func (workerManagementBackupExecutor) ReconcileManagementBackup(context.Context, uuid.UUID, int64) error {
	return nil
}
func (workerManagementBackupExecutor) ExecuteManagementBackupOperation(context.Context, uuid.UUID) error {
	return nil
}

func testTunnelRuntime() TunnelRuntime {
	key, err := auth.GenerateKey()
	if err != nil {
		panic(err)
	}
	encryptor, err := auth.NewEncryptor(key)
	if err != nil {
		panic(err)
	}
	return TunnelRuntime{Features: tasks.TunnelRuntimeFeatures{CRDOwnership: true}, Core: tasks.CoreRuntime{Deps: tasks.RuntimeDependencies{
		Queries: workerRuntimeQueries{}, Leader: workerLeaderElector{}, K8s: workerK8sRequester{}, ResourceDecryptor: encryptor, Log: testLogger(), ManagementBackup: workerManagementBackupExecutor{},
	}}, ToolDrift: tasks.ToolDriftRuntime{Deps: tasks.ToolDriftSweepDeps{
		Queries: workerToolDriftQueries{}, Helm: workerHelmStatusProber{},
	}}, CharlieTrigger: &tasks.CharlieTriggerRuntime{}, ClusterTemplate: tasks.ClusterTemplateRuntime{
		Deps: tasks.ClusterTemplateApplyDeps{
			Queries: workerClusterTemplateQueries{}, Installer: workerToolInstaller{},
		},
		RecoveryEnqueuer: workerTaskEnqueuer{},
	}, NetworkPolicy: tasks.NetworkPolicyRuntime{Deps: tasks.NetworkPolicyApplyDeps{
		Queries: workerNetworkPolicyQueries{}, Requester: workerK8sRequester{},
	}}, Mesh: tasks.MeshRuntime{Deps: tasks.MeshDetectDeps{
		Queries: workerMeshQueries{}, Requester: workerK8sRequester{},
	}}, CloudCredential: tasks.CloudCredentialRuntime{Deps: tasks.CloudCredentialMaterializeDeps{
		Queries: workerCloudCredentialQueries{}, Requester: workerProjectRequester{}, Decryptor: encryptor,
	}}, ClusterRegistry: tasks.ClusterRegistryRuntime{Deps: tasks.ClusterRegistryApplyDeps{
		Queries: workerClusterRegistryQueries{}, Requester: workerProjectRequester{}, Encryptor: encryptor,
	}}, Project: tasks.ProjectRuntime{Deps: tasks.ProjectReconcileDeps{
		Queries: workerProjectQueries{}, Requester: workerProjectRequester{}, Encryptor: encryptor,
	}}, ClusterSnapshot: tasks.ClusterSnapshotRuntime{Deps: tasks.ClusterSnapshotDeps{
		Queries: workerClusterSnapshotQueries{}, Driver: workerVeleroDriver{}, Log: testLogger(),
	}}, ClusterDecommission: tasks.ClusterDecommissionRuntime{Deps: tasks.ClusterDecommissionDeps{
		Queries: workerDecommissionQueries{}, Tunnel: workerDecommissionTunnel{}, RBACCache: workerRBACCache{},
	}}, ControlPlaneSnapshot: tasks.ControlPlaneSnapshotRuntime{}, Deferred: tasks.DeferredRuntime{Deps: tasks.DeferredDispatchDeps{
		Queries: workerDeferredQueries{}, Replayers: workerDeferredReplayers(),
	}}, KubectlSessionReap: tasks.KubectlSessionReapRuntime{}, SecurityIngest: tasks.SecurityIngestRuntime{
		Deps: tasks.SecurityIngestDeps{
			Queries: workerSecurityQueries{}, K8s: workerSecurityFetcher{}, Outbox: workerSecurityOutbox{}, Log: testLogger(),
		},
		Leader: workerLeaderElector{},
	}, CRDOwnership: tasks.CRDOwnershipRuntime{Deps: tasks.CRDOwnershipDriftDeps{
		Queries: &workerDispatchQueries{}, Dynamic: dynamicfake.NewSimpleDynamicClient(k8sruntime.NewScheme()),
	}}, Dex: tasks.DexOperationRuntime{Queries: workerDexQueries{}, Executor: workerDexExecutor{}}}
}

func testStandaloneRuntime() StandaloneRuntime {
	stub := &workerDeliveryStub{}
	queries := &workerDispatchQueries{}
	key, err := auth.GenerateKey()
	if err != nil {
		panic(err)
	}
	encryptor, err := auth.NewEncryptor(key)
	if err != nil {
		panic(err)
	}
	allowlistRegistry := allowlistproviders.NewRegistry()
	allowlistRegistry.Register(allowlistproviders.NewSelfManagedProvider())
	return StandaloneRuntime{Features: tasks.StandaloneRuntimeFeatures{ManagementBackup: true}, Core: tasks.CoreRuntime{Deps: tasks.RuntimeDependencies{
		Queries: workerRuntimeQueries{}, Leader: workerLeaderElector{}, Enqueuer: workerTaskEnqueuer{}, Log: testLogger(),
		CatalogDecryptor: encryptor, MonitoringCipher: encryptor, ManagementBackup: workerManagementBackupExecutor{},
	}}, Delivery: tasks.DeliveryRuntime{
		SourceResolver:          stub,
		RolloutReconciler:       stub,
		SystemRolloutReconciler: stub,
	}, Dispatch: tasks.DispatchRuntime{
		Email:       tasks.EmailDeps{Queries: queries, Sender: workerEmailSender{}, Provider: workerEmailProvider{}},
		Webhook:     tasks.WebhookDeps{Queries: queries, Sender: workerWebhookSender{}, Encryptor: encryptor},
		SIEM:        tasks.SIEMDeps{Queries: queries, Encryptor: encryptor},
		TaskOutbox:  tasks.TaskOutboxDispatchDeps{Queries: queries, Enqueuer: workerTaskEnqueuer{}},
		AuditOutbox: tasks.AuditOutboxDispatchDeps{Queries: queries},
		AdminQueue:  tasks.AdminQueueOperationDeps{Queries: queries, Inspector: workerAdminQueueInspector{}},
	}, Alerts: tasks.CharlieAlertRuntime{
		Queries: queries, WriteFence: charlie.NewWriteFence(), Reconciler: workerAlertReconciler{},
	}, Maintenance: tasks.MaintenanceRuntime{
		AgentTokens:          tasks.AgentTokenRotateDeps{Queries: queries},
		PlaintextCredentials: tasks.PlaintextCredentialMigrationDeps{Queries: queries, Encryptor: encryptor},
	}, Allowlists: tasks.ApiserverAllowlistRuntime{Deps: tasks.ApiserverAllowlistReconcileDeps{
		Queries: queries, Registry: allowlistRegistry,
		ClusterShaper: allowlistproviders.ClusterFromSQLC, AuditWriter: queries,
	}}, GitOps: tasks.GitOpsRuntime{Deps: tasks.GitOpsDeps{
		Queries: queries, Enqueuer: workerTaskEnqueuer{}, TaskOutbox: queries,
		Decryptor: encryptor, Log: testLogger(),
	}}}
}

func TestNewWorker(t *testing.T) {
	w, err := NewWorker("redis://localhost:6379/0", testLogger(), testStandaloneRuntime())
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	if w == nil {
		t.Fatal("expected non-nil Worker")
	}
	if w.server == nil {
		t.Fatal("expected non-nil asynq.Server")
	}
	if w.mux == nil {
		t.Fatal("expected non-nil asynq.ServeMux")
	}
}

func TestNewWorkerManagementBackupOwnershipFollowsFeature(t *testing.T) {
	runtime := testStandaloneRuntime()
	runtime.Features.ManagementBackup = false
	runtime.Core.Deps.ManagementBackup = nil
	w, err := NewWorker("redis://localhost:6379/0", testLogger(), runtime)
	if err != nil {
		t.Fatalf("disabled management backup rejected worker startup: %v", err)
	}
	for _, descriptor := range w.descriptors {
		if descriptor.Type == tasks.ManagementBackupReconcileType || descriptor.Type == tasks.ManagementBackupOperationType {
			t.Fatalf("disabled worker owns management-backup descriptor %q", descriptor.Type)
		}
	}

	runtime = testStandaloneRuntime()
	w, err = NewWorker("redis://localhost:6379/0", testLogger(), runtime)
	if err != nil {
		t.Fatalf("enabled ready management backup rejected worker startup: %v", err)
	}
	owned := map[string]bool{
		tasks.ManagementBackupReconcileType: false,
		tasks.ManagementBackupOperationType: false,
	}
	for _, descriptor := range w.descriptors {
		if _, ok := owned[descriptor.Type]; ok {
			owned[descriptor.Type] = true
		}
	}
	for taskType, present := range owned {
		if !present {
			t.Fatalf("enabled worker does not own management-backup descriptor %q", taskType)
		}
	}

	runtime = testStandaloneRuntime()
	runtime.Core.Deps.ManagementBackup = notReadyWorkerManagementBackup{}
	if w, err = NewWorker("redis://localhost:6379/0", testLogger(), runtime); err == nil || w != nil || !strings.Contains(err.Error(), "management_backup") {
		t.Fatalf("enabled unready management backup startup = (%v, %v), want fail-closed", w, err)
	}
}

type notReadyWorkerManagementBackup struct{ workerManagementBackupExecutor }

func (notReadyWorkerManagementBackup) ManagementBackupReady() bool { return false }

func TestNewWorkerRejectsIncompleteExplicitRuntime(t *testing.T) {
	w, err := NewWorker("redis://localhost:6379/0", testLogger(), StandaloneRuntime{})
	if err == nil {
		t.Fatal("expected incomplete runtime error")
	}
	if w != nil {
		t.Fatal("incomplete runtime returned a worker")
	}
	for _, dependency := range []string{"source_resolver", "rollout_reconciler", "system_rollout_reconciler"} {
		if !strings.Contains(err.Error(), dependency) {
			t.Errorf("error %q does not report %s", err, dependency)
		}
	}
}

func TestNewWorkerBindsRuntimeHandlersIntoCompositionGraph(t *testing.T) {
	w, err := NewWorker("redis://localhost:6379/0", testLogger(), testStandaloneRuntime())
	if err != nil {
		t.Fatal(err)
	}
	required := map[string]bool{
		tasks.DeliverySourceResolutionType:           false,
		tasks.DeliveryRolloutReconcileType:           false,
		tasks.DeliverySystemRolloutReconcileType:     false,
		tasks.EmailDispatchType:                      false,
		tasks.EmailCleanupOldType:                    false,
		tasks.WebhookDispatchType:                    false,
		tasks.WebhookCleanupOldType:                  false,
		tasks.SIEMDispatchType:                       false,
		tasks.SIEMCleanupOldType:                     false,
		tasks.TaskOutboxDispatchType:                 false,
		tasks.AuditOutboxDispatchType:                false,
		tasks.CharlieAlertDispatchTaskType:           false,
		tasks.CharlieAlertReconcileType:              false,
		tasks.AgentTokenRotateSweepType:              false,
		tasks.PlaintextCredentialMigrationType:       false,
		tasks.ApiserverAllowlistReconcileType:        false,
		tasks.ApiserverAllowlistReconcileAllType:     false,
		tasks.ApiserverAllowlistCleanupSnapshotsType: false,
		tasks.GitOpsSyncType:                         false,
	}
	for _, descriptor := range w.descriptors {
		if _, ok := required[descriptor.Type]; !ok {
			continue
		}
		if descriptor.Handler == nil {
			t.Fatalf("runtime-bound handler %q is nil", descriptor.Type)
		}
		if err := descriptor.Handler(context.Background(), asynq.NewTask(descriptor.Type, nil)); err != nil && strings.Contains(err.Error(), "requires an explicit runtime binding") {
			t.Fatalf("runtime-bound handler %q used an unbound fallback: %v", descriptor.Type, err)
		}
		required[descriptor.Type] = true
	}
	for taskType, bound := range required {
		if !bound {
			t.Errorf("runtime-bound task %q absent from worker graph", taskType)
		}
	}
}

// Invalid REDIS_URL must be fail-fast (error returned,
// nil Worker), NOT silently fall back to localhost. The previous behavior
// was a production footgun.
func TestNewWorkerInvalidRedis(t *testing.T) {
	w, err := NewWorker("not-a-valid-url", testLogger(), testStandaloneRuntime())
	if err == nil {
		t.Fatal("expected error for invalid REDIS_URL, got nil")
	}
	if w != nil {
		t.Fatal("expected nil Worker on parse error")
	}
}

func TestRegisterHandlers(t *testing.T) {
	w, err := NewWorker("redis://localhost:6379/0", testLogger(), testStandaloneRuntime())
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	// Should not panic.
	w.RegisterHandlers()
}

func TestRegisterTunnelHandlers(t *testing.T) {
	w, err := NewTunnelWorker("redis://localhost:6379/0", 0, testLogger(), testTunnelRuntime())
	if err != nil {
		t.Fatalf("NewTunnelWorker: %v", err)
	}
	// Should not panic.
	w.RegisterTunnelHandlers()
}

func TestNewTunnelWorkerBindsExplicitRuntimeHandlers(t *testing.T) {
	w, err := NewTunnelWorker("redis://localhost:6379/0", 0, testLogger(), testTunnelRuntime())
	if err != nil {
		t.Fatal(err)
	}
	required := map[string]bool{
		tasks.ToolDriftSweepType:                   false,
		tasks.CharlieTriggerDispatchType:           false,
		tasks.ClusterTemplateApplyType:             false,
		tasks.ClusterTemplateDriftCheckType:        false,
		tasks.NetworkPolicyApplyType:               false,
		tasks.NetworkPolicyDriftCheckType:          false,
		tasks.MeshDetectType:                       false,
		tasks.CloudCredentialMaterializeType:       false,
		tasks.CloudCredentialDriftReconcileType:    false,
		tasks.ClusterApplyRegistrySecretType:       false,
		tasks.ClusterRegistryDriftReconcileType:    false,
		tasks.ProjectReconcileType:                 false,
		tasks.ProjectReconcileAllType:              false,
		tasks.ClusterSnapshotPollType:              false,
		tasks.DexOperationType:                     false,
		tasks.DexOperationRecoveryType:             false,
		tasks.ClusterSnapshotDispatchScheduledType: false,
		tasks.ClusterSnapshotCleanupExpiredType:    false,
		tasks.ClusterDecommissionType:              false,
		tasks.ClusterDecommissionAllType:           false,
		tasks.ControlPlaneSnapshotSweepType:        false,
		tasks.DispatchDeferredType:                 false,
		tasks.KubectlSessionReapType:               false,
		tasks.SecurityIngestType:                   false,
		tasks.SecurityIngestRecoveryType:           false,
		tasks.CRDOwnershipDriftCheckType:           false,
	}
	for _, descriptor := range w.descriptors {
		if _, ok := required[descriptor.Type]; !ok {
			continue
		}
		if descriptor.Handler == nil {
			t.Fatalf("runtime-bound handler %q is nil", descriptor.Type)
		}
		err := descriptor.Handler(context.Background(), asynq.NewTask(descriptor.Type, nil))
		if err != nil && strings.Contains(err.Error(), "requires an explicit runtime binding") {
			t.Fatalf("runtime-bound handler %q used an unbound fallback: %v", descriptor.Type, err)
		}
		required[descriptor.Type] = true
	}
	for taskType, bound := range required {
		if !bound {
			t.Errorf("runtime-bound task %q absent from tunnel graph", taskType)
		}
	}
}

func TestNewTunnelWorkerRejectsIncompleteRuntime(t *testing.T) {
	w, err := NewTunnelWorker("redis://localhost:6379/0", 0, testLogger(), TunnelRuntime{})
	if err == nil || w != nil {
		t.Fatalf("incomplete tunnel runtime returned worker=%v error=%v", w, err)
	}
}

func TestNewTunnelWorkerFailsClosedWithoutResourceDecryptor(t *testing.T) {
	runtime := testTunnelRuntime()
	runtime.Core.Deps.ResourceDecryptor = nil
	w, err := NewTunnelWorker("redis://localhost:6379/0", 0, testLogger(), runtime)
	if err == nil || w != nil || !strings.Contains(err.Error(), "resource_decryptor") {
		t.Fatalf("missing resource decryptor returned worker=%v error=%v", w, err)
	}
}

func TestNewTunnelWorkerCRDOwnershipFollowsFeature(t *testing.T) {
	runtime := testTunnelRuntime()
	runtime.Features.CRDOwnership = false
	runtime.CRDOwnership.Deps.Dynamic = nil
	w, err := NewTunnelWorker("redis://localhost:6379/0", 0, testLogger(), runtime)
	if err != nil {
		t.Fatalf("disabled CRD ownership rejected tunnel startup: %v", err)
	}
	for _, descriptor := range w.descriptors {
		if descriptor.Type == tasks.CRDOwnershipDriftCheckType {
			t.Fatal("disabled tunnel owns CRD ownership descriptor")
		}
	}

	runtime = testTunnelRuntime()
	runtime.CRDOwnership.Deps.Dynamic = nil
	if w, err = NewTunnelWorker("redis://localhost:6379/0", 0, testLogger(), runtime); err == nil || w != nil || !strings.Contains(err.Error(), "dynamic_client") {
		t.Fatalf("enabled CRD ownership startup = (%v, %v), want fail-closed", w, err)
	}
}

func TestCRDOwnershipScheduleFollowsFeature(t *testing.T) {
	for _, test := range []struct {
		enabled bool
		want    bool
	}{
		{enabled: false, want: false},
		{enabled: true, want: true},
	} {
		found := false
		for _, spec := range scheduledTaskSpecsForFeatures(SchedulerFeatures{CRDOwnership: test.enabled}) {
			found = found || spec.TaskType == tasks.CRDOwnershipDriftCheckType
		}
		if found != test.want {
			t.Fatalf("CRD enabled=%t schedule present=%t, want %t", test.enabled, found, test.want)
		}
	}
}

// TestTunnelWorkerConcurrencyConfigurable (M11): NewTunnelWorker accepts an
// explicit concurrency and a non-positive value falls back to the default
// (which is higher than the old hardcoded 2 so long installs don't starve RPCs).
func TestTunnelWorkerConcurrencyConfigurable(t *testing.T) {
	if defaultTunnelWorkerConcurrency <= 2 {
		t.Fatalf("default tunnel concurrency = %d, must exceed the old hardcoded 2", defaultTunnelWorkerConcurrency)
	}
	for _, c := range []int{0, -1, 16} {
		w, err := NewTunnelWorker("redis://localhost:6379/0", c, testLogger(), testTunnelRuntime())
		if err != nil {
			t.Fatalf("NewTunnelWorker(concurrency=%d): %v", c, err)
		}
		if w == nil || w.server == nil {
			t.Fatalf("NewTunnelWorker(concurrency=%d) returned nil server", c)
		}
	}
}

func TestNewScheduler(t *testing.T) {
	s, err := NewScheduler("redis://localhost:6379/0", testLogger())
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}
	if s == nil {
		t.Fatal("expected non-nil Scheduler")
	}
	if s.scheduler == nil {
		t.Fatal("expected non-nil asynq.Scheduler")
	}
}

// Same fail-fast contract for the scheduler.
func TestNewSchedulerInvalidRedis(t *testing.T) {
	s, err := NewScheduler("not-a-valid-url", testLogger())
	if err == nil {
		t.Fatal("expected error for invalid REDIS_URL, got nil")
	}
	if s != nil {
		t.Fatal("expected nil Scheduler on parse error")
	}
}

func TestTaskConstants(t *testing.T) {
	expected := map[string]string{
		"TypeHealthCheck":              "cluster:health_check",
		"TypeAlertEvaluation":          "alert:evaluate",
		"TypeCatalogSync":              "catalog:sync",
		"TypeMetricsAggregation":       "metrics:aggregate",
		"TypeMonitoringReconcile":      "monitoring:reconcile",
		"TypeBackupExecution":          "backup:execute",
		"TypeSecurityScan":             "security:scan",
		"TypeNotificationSend":         "notification:send",
		"TypeAgentManifest":            "agent:generate_manifest",
		"TypeEnsureAuditLogPartitions": "audit_log:ensure_partitions",
		"TypeEnforceAuditLogRetention": "audit_log:enforce_retention",
	}

	actual := map[string]string{
		"TypeHealthCheck":              TypeHealthCheck,
		"TypeAlertEvaluation":          TypeAlertEvaluation,
		"TypeCatalogSync":              TypeCatalogSync,
		"TypeMetricsAggregation":       TypeMetricsAggregation,
		"TypeMonitoringReconcile":      TypeMonitoringReconcile,
		"TypeBackupExecution":          TypeBackupExecution,
		"TypeSecurityScan":             TypeSecurityScan,
		"TypeNotificationSend":         TypeNotificationSend,
		"TypeAgentManifest":            TypeAgentManifest,
		"TypeEnsureAuditLogPartitions": TypeEnsureAuditLogPartitions,
		"TypeEnforceAuditLogRetention": TypeEnforceAuditLogRetention,
	}

	for name, want := range expected {
		got := actual[name]
		if got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

// --- Payload encoding/decoding tests ---

func TestHealthCheckPayloadRoundTrip(t *testing.T) {
	p := tasks.HealthCheckPayload{ClusterID: "cluster-123"}
	task, err := tasks.NewHealthCheckTask(p)
	if err != nil {
		t.Fatalf("NewHealthCheckTask: %v", err)
	}

	var decoded tasks.HealthCheckPayload
	if err := json.Unmarshal(task.Payload(), &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.ClusterID != p.ClusterID {
		t.Errorf("ClusterID = %q, want %q", decoded.ClusterID, p.ClusterID)
	}
}

func TestBackupExecutionPayloadRoundTrip(t *testing.T) {
	p := tasks.BackupExecutionPayload{ClusterID: "c1", BackupID: "b1"}
	task, err := tasks.NewBackupExecutionTask(p)
	if err != nil {
		t.Fatalf("NewBackupExecutionTask: %v", err)
	}

	var decoded tasks.BackupExecutionPayload
	if err := json.Unmarshal(task.Payload(), &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.ClusterID != "c1" || decoded.BackupID != "b1" {
		t.Errorf("got %+v, want cluster_id=c1, backup_id=b1", decoded)
	}
}

func TestNotificationPayloadRoundTrip(t *testing.T) {
	p := tasks.NotificationSendPayload{
		Channel:    "slack",
		Subject:    "Alert",
		Body:       "Something happened",
		Recipients: []string{"#ops", "#alerts"},
	}
	task, err := tasks.NewNotificationSendTask(p)
	if err != nil {
		t.Fatalf("NewNotificationSendTask: %v", err)
	}

	var decoded tasks.NotificationSendPayload
	if err := json.Unmarshal(task.Payload(), &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Channel != "slack" || len(decoded.Recipients) != 2 {
		t.Errorf("got %+v, want channel=slack, 2 recipients", decoded)
	}
}

func TestAgentManifestQueueProducerIsRetired(t *testing.T) {
	p := tasks.AgentManifestPayload{
		ClusterID:       "c1",
		AgentToken:      "tok-abc",
		ImageRepository: "ghcr.io/test",
		ImageTag:        "v1.0.0",
	}
	task, err := tasks.NewAgentManifestTask(p)
	if err == nil || task != nil {
		t.Fatalf("NewAgentManifestTask() = (%v, %v), want retired error and no credential-bearing task", task, err)
	}
}

func TestSecurityScanQueueProducerIsRetired(t *testing.T) {
	task, err := tasks.NewSecurityScanTask(tasks.SecurityScanPayload{ClusterID: "11111111-1111-1111-1111-111111111111"})
	if err == nil || task != nil {
		t.Fatalf("NewSecurityScanTask() = (%v, %v), want retired error and no synthetic scan task", task, err)
	}
}

// --- Handler tests (with nil/empty payloads and no configured runtime) ---

func TestHandleHealthCheckEmptyPayload(t *testing.T) {
	task := asynq.NewTask(TypeHealthCheck, nil)
	if err := tasks.HandleHealthCheck(context.Background(), task); err == nil {
		t.Fatal("HandleHealthCheck returned nil, want an unconfigured-runtime error")
	}
}

func TestHandleAlertEvaluationEmptyPayload(t *testing.T) {
	task := asynq.NewTask(TypeAlertEvaluation, nil)
	if err := tasks.HandleAlertEvaluation(context.Background(), task); err == nil {
		t.Fatal("HandleAlertEvaluation returned nil, want an unconfigured-runtime error")
	}
}

func TestHandleCatalogSyncEmptyPayload(t *testing.T) {
	task := asynq.NewTask(TypeCatalogSync, nil)
	if err := tasks.HandleCatalogSync(context.Background(), task); err == nil {
		t.Fatal("HandleCatalogSync returned nil, want an unconfigured-runtime error")
	}
}

func TestHandleMetricsAggregationEmptyPayload(t *testing.T) {
	task := asynq.NewTask(TypeMetricsAggregation, nil)
	if err := tasks.HandleMetricsAggregation(context.Background(), task); err != nil {
		t.Fatalf("HandleMetricsAggregation: %v", err)
	}
}

func TestHandleMonitoringReconcileEmptyPayload(t *testing.T) {
	task := asynq.NewTask(TypeMonitoringReconcile, nil)
	if err := tasks.HandleMonitoringReconcile(context.Background(), task); err == nil {
		t.Fatal("HandleMonitoringReconcile returned nil, want an unconfigured-runtime error")
	}
}

func TestHandleBackupExecutionMissingFields(t *testing.T) {
	// Empty payload should fail (requires cluster_id and backup_id).
	task := asynq.NewTask(TypeBackupExecution, []byte(`{}`))
	err := tasks.HandleBackupExecution(context.Background(), task)
	if err == nil {
		t.Fatal("expected error for missing fields")
	}
}

func TestHandleSecurityScanMissingCluster(t *testing.T) {
	task := asynq.NewTask(TypeSecurityScan, []byte(`{}`))
	err := tasks.HandleSecurityScan(context.Background(), task)
	if err == nil {
		t.Fatal("expected error for missing cluster_id")
	}
}

func TestHandleNotificationMissingChannel(t *testing.T) {
	task := asynq.NewTask(TypeNotificationSend, []byte(`{"recipients":["a"]}`))
	err := tasks.HandleNotificationSend(context.Background(), task)
	if err == nil {
		t.Fatal("expected error for missing channel")
	}
}

func TestHandleAgentManifestMissingCluster(t *testing.T) {
	task := asynq.NewTask(TypeAgentManifest, []byte(`{}`))
	err := tasks.HandleAgentManifest(context.Background(), task)
	if err == nil {
		t.Fatal("expected error for missing cluster_id")
	}
}

func TestRetiredHandlersAreNonRetryable(t *testing.T) {
	securityTask := asynq.NewTask(TypeSecurityScan, []byte(`{"cluster_id":"11111111-1111-1111-1111-111111111111"}`))
	if err := tasks.HandleSecurityScan(context.Background(), securityTask); !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("HandleSecurityScan error = %v, want SkipRetry", err)
	}
	agentTask := asynq.NewTask(TypeAgentManifest, []byte(`{"cluster_id":"cluster-1"}`))
	if err := tasks.HandleAgentManifest(context.Background(), agentTask); !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("HandleAgentManifest error = %v, want SkipRetry", err)
	}
}
