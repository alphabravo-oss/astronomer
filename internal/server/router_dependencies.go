package server

import (
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/email"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	deliveryhandler "github.com/alphabravocompany/astronomer-go/internal/handler/delivery"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/internal/tunnel"
)

// RouterDependencies is the route composition boundary. Dependencies are
// grouped by ownership so adding a route cannot grow another flat service
// locator. NewRouter accepts partial groups for focused tests; production must
// use NewProductionRouter, which validates security-critical wiring first.
type RouterDependencies struct {
	CoreAuth          CoreAuthDependencies
	ClusterResources  ClusterResourceDependencies
	Delivery          DeliveryDependencies
	AdminPlatform     AdminPlatformDependencies
	StreamingInternal StreamingInternalDependencies
}

// CoreAuthDependencies contains cross-cutting identity, authorization,
// persistence, documentation, and readiness dependencies.
type CoreAuthDependencies struct {
	JWT                *auth.JWTManager
	Encryptor          *auth.Encryptor
	AuthQueries        auth.TokenUserQuerier
	AuditWriter        audit.Querier
	EmailEnqueuer      *email.Enqueuer
	Auth               *handler.AuthHandler
	TOTP               *handler.TOTPHandler
	SSO                *handler.SSOHandler
	Docs               *handler.DocsHandler
	SSOPresets         *handler.SSOPresetsHandler
	RBAC               *handler.RBACHandler
	Principals         *handler.PrincipalHandler
	RBACQueries        rbac.BindingQuerier
	RBACEngine         *rbac.Engine
	Queries            *sqlc.Queries
	SettingsCache      *handler.SettingsCache
	ReadAuditEvaluator *appmiddleware.PolicyEvaluator
	SCIM               *handler.SCIMHandler
	SCIMTokenAdmin     *handler.SCIMTokenAdminHandler
	Readyz             http.Handler
}

// ClusterResourceDependencies owns adopted-cluster, project, Kubernetes
// resource, observability, policy, and operator-tool routes.
type ClusterResourceDependencies struct {
	Clusters              *handler.ClusterHandler
	ClusterTemplates      *handler.ClusterTemplateHandler
	ClusterRegistration   *handler.ClusterRegistrationHandler
	ClusterRegistries     *handler.ClusterRegistriesHandler
	ClusterSnapshots      *handler.ClusterSnapshotsHandler
	ControlPlaneSnapshots *handler.ControlPlaneSnapshotHandler
	NativeAuthz           nativeAuthorizer
	NativeRBAC            *handler.NativeRBACHandler
	NamespaceScopedRBAC   bool
	NetworkPolicies       *handler.NetworkPolicyHandler
	Gatekeeper            *handler.GatekeeperConstraintsHandler
	Projects              *handler.ProjectHandler
	Tools                 *handler.ToolHandler
	Backups               *handler.BackupHandler
	Logging               *handler.LoggingHandler
	Monitoring            *handler.MonitoringHandler
	ControlPlane          *handler.ControlPlaneHandler
	Resources             *handler.ResourceHandler
	ServiceProxy          *handler.ServiceProxyHandler
	Workloads             *handler.WorkloadHandler
	ResourcesSearch       *handler.ResourcesSearchHandler
	ClusterAgent          *handler.ClusterAgentHandler
	ApiserverAudit        *handler.ApiserverAuditHandler
	ApiserverAllowlist    *handler.ApiserverAllowlistHandler
	ImageVulns            *handler.ImageVulnHandler
	ClusterGroups         *handler.ClusterGroupHandler
	ClusterResources      *handler.ClusterResourcesHandler
	ServiceMesh           *handler.ServiceMeshHandler
	CloudCredentials      *handler.CloudCredentialHandler
	ProjectCatalogs       *handler.ProjectCatalogHandler
	Vault                 *handler.VaultHandler
}

// DeliveryDependencies owns the Flux-native desired-state and rollout API.
type DeliveryDependencies struct {
	Sources                *deliveryhandler.SourceHandler
	Bundles                *deliveryhandler.BundleHandler
	Targets                *deliveryhandler.TargetHandler
	Rollouts               *deliveryhandler.RolloutHandler
	Deployments            *deliveryhandler.DeploymentHandler
	Inventory              *deliveryhandler.InventoryHandler
	System                 *deliveryhandler.SystemRolloutHandler
	ConfigurationTemplates *deliveryhandler.ConfigurationTemplateHandler
	OverrideSets           *deliveryhandler.OverrideSetHandler
	GitOps                 *handler.GitOpsHandler
}

// AdminPlatformDependencies owns management-plane and platform-wide product
// surfaces. Individual feature handlers remain optional and are only mounted
// when their feature is wired.
type AdminPlatformDependencies struct {
	PlatformHealth           *handler.PlatformHealthHandler
	AdminQueues              *handler.AdminQueuesHandler
	AdminTaskOutbox          *handler.AdminTaskOutboxHandler
	AdminDrill               *handler.AdminDrillHandler
	ManagementLogs           *handler.ManagementLogsHandler
	GroupMappings            *handler.GroupMappingsHandler
	SMTP                     *handler.SMTPHandler
	Webhooks                 *handler.WebhookHandler
	SIEMForwarders           *handler.SIEMHandler
	NotificationTemplates    *handler.NotificationTemplateHandler
	Audit                    *handler.AuditHandler
	Alerting                 *handler.AlertingHandler
	Anomaly                  *handler.AnomalyHandler
	Catalog                  *handler.CatalogHandler
	ChartRatings             *handler.ChartRatingsHandler
	PlatformCharts           *handler.PlatformChartRepoHandler
	Security                 *handler.SecurityHandler
	DexConfig                *handler.DexHandler
	SupportBundle            *handler.SupportBundleHandler
	Compliance               *handler.ComplianceHandler
	CompliancePosture        *handler.CompliancePostureHandler
	License                  *handler.LicenseHandler
	PlatformSettings         *handler.PlatformSettingsHandler
	Extensions               *handler.ExtensionHandler
	PlatformDefaultTemplate  *handler.PlatformDefaultTemplateHandler
	PlatformBaselineCoverage *handler.PlatformBaselineCoverageHandler
	CharlieOnboarding        *handler.CharlieOnboardingHandler
	CharlieAdmin             *handler.CharlieAdminHandler
	CharlieSessions          *handler.CharlieSessionHandler
	CharlieThreads           *handler.CharlieThreadHandler
	CharlieApprovals         *handler.CharlieApprovalHandler
	CharlieContext           *handler.CharlieContextHandler
	CharlieFindings          *handler.CharlieFindingHandler
	CharlieOperations        *handler.CharlieOperationHandler
	Quotas                   *handler.QuotaHandler
	Maintenance              *handler.MaintenanceHandler
	Dashboards               *handler.DashboardHandler
	ReadAuditPolicies        *handler.ReadAuditPolicyHandler
	ComplianceBaselines      *handler.ComplianceBaselinesHandler
}

// StreamingInternalDependencies owns long-lived browser streams, the agent
// tunnel, and authenticated cross-pod forwarding. Nil consumers remain
// fail-closed and optional routes remain absent.
type StreamingInternalDependencies struct {
	Hub               *tunnel.Hub
	Proxy             *tunnel.ProxyHandler
	InternalK8s       *tunnel.InternalK8sHandler
	InternalHelm      *tunnel.InternalHelmHandler
	Exec              *tunnel.ExecConsumer
	Logs              *tunnel.LogsConsumer
	EventStream       *handler.EventStreamHandler
	StreamTickets     *handler.StreamTicketHandler
	StreamTicketStore *auth.StreamTicketStore
	KubectlShell      *handler.KubectlShellHandler
}
