package server

import (
	"log/slog"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
)

func (c *productionComposition) composePlatformRouterDependencies(cfg *config.Config, logger *slog.Logger, deps *RouterDependencies) {
	database := c.database
	queries := c.queries
	rbacEngine := c.rbacEngine
	rbacQuerier := c.rbacQuerier
	queue := c.queue

	deps.AdminPlatform.CharlieOnboarding = c.charlieOnboardingHandler
	deps.AdminPlatform.CharlieAdmin = c.charlieAdminHandler
	deps.AdminPlatform.CharlieSessions = c.charlieSessionsHandler
	deps.AdminPlatform.CharlieThreads = c.charlieThreadsHandler
	deps.AdminPlatform.CharlieApprovals = c.charlieApprovalsHandler
	deps.AdminPlatform.CharlieContext = c.charlieContextHandler
	deps.AdminPlatform.CharlieFindings = c.charlieFindingsHandler
	deps.AdminPlatform.CharlieOperations = c.charlieOperationsHandler
	deps.AdminPlatform.Extensions = func() *handler.ExtensionHandler {
		h := handler.NewExtensionHandler(queries)
		h.SetRunTx(sqlcMutationTxRunner[handler.ExtensionMutationTx](database))
		h.SetAuditWriter(queries)
		h.SetRBAC(rbacEngine, rbacQuerier)
		extensionTickets := auth.NewExtensionTicketStore(time.Minute)
		h.SetExtensionTickets(extensionTickets)
		h.SetExtensionTicketIssuer(extensionTickets)
		if err := h.SetTrustedBundleKey(cfg.ExtensionBundleTrustedKey); err != nil {
			logger.Warn("invalid EXTENSION_BUNDLE_TRUSTED_KEY; bundle verification will fail closed", "error", err)
		}
		return h
	}()
	deps.AdminPlatform.PlatformDefaultTemplate = func() *handler.PlatformDefaultTemplateHandler {
		h := handler.NewPlatformDefaultTemplateHandler(queries)
		h.SetRunTx(sqlcMutationTxRunner[handler.PlatformDefaultTemplateMutationTx](database))
		return h
	}()
	deps.AdminPlatform.PlatformBaselineCoverage = handler.NewPlatformBaselineCoverageHandler(queries)
	deps.AdminPlatform.Quotas = func() *handler.QuotaHandler {
		h := handler.NewQuotaHandler(queries)
		h.SetRunTx(sqlcMutationTxRunner[handler.QuotaMutationTx](database))
		return h
	}()
	deps.ClusterResources.CloudCredentials = c.cloudCredentialsHandler
	deps.AdminPlatform.Maintenance = func() *handler.MaintenanceHandler {
		h := handler.NewMaintenanceHandler(queries, c.maintenanceEvaluator)
		h.SetRunTx(sqlcMutationTxRunner[handler.MaintenanceMutationTx](database))
		return h
	}()
	deps.AdminPlatform.Dashboards = c.dashboardsHandler
	deps.Delivery.GitOps = func() *handler.GitOpsHandler {
		gitopsRuntime := tasks.GitOpsRuntime{Deps: tasks.GitOpsDeps{
			Queries: queries, Enqueuer: queue, TaskOutbox: queries,
			Decryptor: c.encryptor, Log: logger,
		}}
		h := handler.NewGitOpsHandler(queries, handler.DefaultGitOpsSyncRunner(gitopsRuntime), logger)
		h.SetRunTx(sqlcMutationTxRunner[handler.GitOpsMutationTx](database))
		h.SetTaskOutbox(queries)
		h.SetAuditWriter(queries)
		h.SetEncryptor(c.encryptor)
		return h
	}()
	deps.ClusterResources.ProjectCatalogs = c.projectCatalogsHandler
	deps.AdminPlatform.ComplianceBaselines = handler.NewComplianceBaselinesHandlerFromPool(database.Pool(), logger)
	deps.StreamingInternal.KubectlShell = c.kubectlShell
	deps.ClusterResources.ClusterGroups = c.clusterGroupsHandler
	deps.ClusterResources.Vault = c.vaultHandler
	deps.ClusterResources.ClusterResources = c.clusterResourcesHandler
	deps.ClusterResources.ServiceMesh = func() *handler.ServiceMeshHandler {
		h := handler.NewServiceMeshHandler(queries)
		h.SetRequester(c.requester)
		h.SetAuditor(queries)
		h.SetAuthorization(rbacEngine, rbacQuerier)
		h.SetDetector(handler.MeshDetectorFunc(c.meshRuntime.DetectAndUpsert))
		return h
	}()
}
