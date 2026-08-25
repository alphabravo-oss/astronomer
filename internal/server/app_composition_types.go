package server

import (
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/cacheinvalidate"
	"github.com/alphabravocompany/astronomer-go/internal/charlie"
	"github.com/alphabravocompany/astronomer-go/internal/db"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	deliverydeployment "github.com/alphabravocompany/astronomer-go/internal/delivery/deployment"
	deliveryrollout "github.com/alphabravocompany/astronomer-go/internal/delivery/rollout"
	deliverystatus "github.com/alphabravocompany/astronomer-go/internal/delivery/status"
	"github.com/alphabravocompany/astronomer-go/internal/email"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	deliveryhandler "github.com/alphabravocompany/astronomer-go/internal/handler/delivery"
	"github.com/alphabravocompany/astronomer-go/internal/maintenance"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/internal/siem"
	"github.com/alphabravocompany/astronomer-go/internal/tunnel"
	"github.com/alphabravocompany/astronomer-go/internal/tunnel2"
	"github.com/alphabravocompany/astronomer-go/internal/vault"
	"github.com/alphabravocompany/astronomer-go/internal/webhook"
	"github.com/alphabravocompany/astronomer-go/internal/worker/leader"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	metricsv "k8s.io/metrics/pkg/client/clientset/versioned"
)

// productionComposition is the explicit hand-off between dependency
// construction, HTTP route composition, and background-runtime startup.
// Keeping the hand-off typed makes the production composition root reviewable
// without hiding dependencies in globals or an untyped service locator.
type productionComposition struct {
	database                     *db.DB
	queries                      *sqlc.Queries
	jwtManager                   *auth.JWTManager
	encryptor                    *auth.Encryptor
	ssoManager                   *auth.SSOManager
	bus                          *events.Bus
	hub                          *tunnel.Hub
	deliveryStatusIngester       *deliverystatus.Ingester
	locatorReadinessErr          string
	connLimiter                  *tunnel.ConnectFailureLimiter
	remoteServer                 *tunnel2.RemoteServer
	requester                    *handler.TunnelK8sRequester
	helmRequester                *handler.TunnelHelmRequester
	monitoringHandler            *handler.MonitoringHandler
	alertingHandler              *handler.AlertingHandler
	toolHandler                  *handler.ToolHandler
	catalogHandler               *handler.CatalogHandler
	backupHandler                *handler.BackupHandler
	loggingHandler               *handler.LoggingHandler
	securityHandler              *handler.SecurityHandler
	apiserverAllowlistHandler    *handler.ApiserverAllowlistHandler
	workloadHandler              *handler.WorkloadHandler
	anomalyHandler               *handler.AnomalyHandler
	rbacEngine                   *rbac.Engine
	rbacQuerier                  *appmiddleware.SQLCRBACQuerier
	redisOpt                     asynq.RedisConnOpt
	queue                        *asynq.Client
	taskLeader                   *leader.Elector
	securityCacheCoordinator     *cacheinvalidate.Coordinator
	runtimeRedisClient           redis.UniversalClient
	securityIngestRuntime        tasks.SecurityIngestRuntime
	projectHandler               *handler.ProjectHandler
	clusterTemplateHandler       *handler.ClusterTemplateHandler
	clusterRegistriesHandler     *handler.ClusterRegistriesHandler
	networkPoliciesHandler       *handler.NetworkPolicyHandler
	networkPolicyRuntime         tasks.NetworkPolicyRuntime
	cloudCredentialsHandler      *handler.CloudCredentialHandler
	dashboardsHandler            *handler.DashboardHandler
	projectCatalogsHandler       *handler.ProjectCatalogHandler
	clusterGroupsHandler         *handler.ClusterGroupHandler
	vaultResolver                *vault.Resolver
	vaultHandler                 *handler.VaultHandler
	clusterSnapshotsHandler      *handler.ClusterSnapshotsHandler
	controlPlaneSnapshotHandler  *handler.ControlPlaneSnapshotHandler
	controlPlaneSnapshotRuntime  tasks.ControlPlaneSnapshotRuntime
	nativeRBACAuthz              *nativeRBACAuthorizer
	nativeRBACHandler            *handler.NativeRBACHandler
	clusterSnapshotRuntime       tasks.ClusterSnapshotRuntime
	meshRuntime                  tasks.MeshRuntime
	clusterResourcesHandler      *handler.ClusterResourcesHandler
	apiserverAuditHandler        *handler.ApiserverAuditHandler
	controlPlaneHandler          *handler.ControlPlaneHandler
	authHandler                  *handler.AuthHandler
	totpHandler                  *handler.TOTPHandler
	ssoHandler                   *handler.SSOHandler
	dexHandler                   *handler.DexHandler
	clusterHandler               *handler.ClusterHandler
	clusterRegistrationHandler   *handler.ClusterRegistrationHandler
	localK8s                     kubernetes.Interface
	localMetrics                 metricsv.Interface
	localDynamic                 dynamic.Interface
	localNamespace               string
	localReleaseName             string
	localChartVersion            string
	charlieFeatures              charlieLiveFeatures
	charlieBindings              charlieLiveBindings
	charlieOnboardingHandler     *handler.CharlieOnboardingHandler
	charlieAgentRuntime          *charlie.KubernetesRuntimeActivator
	charlieHelm                  charlie.HelmReleaser
	resourceHandler              *handler.ResourceHandler
	platformCharts               *handler.PlatformChartRepoHandler
	smtpHandler                  *handler.SMTPHandler
	emailEnqueuer                *email.Enqueuer
	webhookHandler               *handler.WebhookHandler
	webhookTap                   *webhook.Tap
	siemHandler                  *handler.SIEMHandler
	siemTap                      *siem.BusTap
	maintenanceEvaluator         *maintenance.Evaluator
	streamTickets                *auth.StreamTicketStore
	streamTicketHandler          *handler.StreamTicketHandler
	settingsCache                *handler.SettingsCache
	charlieSessionsHandler       *handler.CharlieSessionHandler
	charlieThreadsHandler        *handler.CharlieThreadHandler
	charlieApprovalsHandler      *handler.CharlieApprovalHandler
	charlieContextHandler        *handler.CharlieContextHandler
	charlieFindingsHandler       *handler.CharlieFindingHandler
	charlieOperationsHandler     *handler.CharlieOperationHandler
	charlieAdminHandler          *handler.CharlieAdminHandler
	charlieAdminService          *charlie.AdminService
	managedCharlieBridge         *charlie.ManagedBridge
	charlieInventory             *charlie.ManagementPlatformInventory
	charlieFindingProjection     *charlie.FindingProjection
	charlieFindingEvents         handler.CharlieFindingEventAuthorizer
	charlieWriteFence            *charlie.WriteFence
	charlieTriggerRuntime        *tasks.CharlieTriggerRuntime
	deliveryTargetHandler        *deliveryhandler.TargetHandler
	deliveryPlanningStore        *deliveryrollout.PostgresPlanningStore
	deliveryRolloutController    *deliveryrollout.PostgresController
	deliverySourceHandler        *deliveryhandler.SourceHandler
	deliveryBundleHandler        *deliveryhandler.BundleHandler
	deliveryRolloutHandler       *deliveryhandler.RolloutHandler
	deliveryDeploymentController *deliverydeployment.PostgresController
	deliverySystemRolloutHandler *deliveryhandler.SystemRolloutHandler
	kubectlShell                 *handler.KubectlShellHandler
	kubectlSessionReapRuntime    tasks.KubectlSessionReapRuntime
}
