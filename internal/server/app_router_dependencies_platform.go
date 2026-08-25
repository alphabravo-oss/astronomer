package server

import (
	"log/slog"
	"os"
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

	deps.CharlieOnboarding = c.charlieOnboardingHandler
	deps.CharlieAdmin = c.charlieAdminHandler
	deps.CharlieSessions = c.charlieSessionsHandler
	deps.CharlieThreads = c.charlieThreadsHandler
	deps.CharlieApprovals = c.charlieApprovalsHandler
	deps.CharlieContext = c.charlieContextHandler
	deps.CharlieFindings = c.charlieFindingsHandler
	deps.CharlieOperations = c.charlieOperationsHandler
	deps.Extensions = func() *handler.ExtensionHandler {
		h := handler.NewExtensionHandler(queries)
		h.SetRunTx(sqlcMutationTxRunner[handler.ExtensionMutationTx](database))
		h.SetAuditWriter(queries)
		h.SetRBAC(rbacEngine, rbacQuerier)
		extensionTickets := auth.NewExtensionTicketStore(time.Minute)
		h.SetExtensionTickets(extensionTickets)
		h.SetExtensionTicketIssuer(extensionTickets)
		if err := h.SetTrustedBundleKey(os.Getenv("EXTENSION_BUNDLE_TRUSTED_KEY")); err != nil {
			logger.Warn("invalid EXTENSION_BUNDLE_TRUSTED_KEY; bundle verification will fail closed", "error", err)
		}
		return h
	}()
	deps.PlatformDefaultTemplate = func() *handler.PlatformDefaultTemplateHandler {
		h := handler.NewPlatformDefaultTemplateHandler(queries)
		h.SetRunTx(sqlcMutationTxRunner[handler.PlatformDefaultTemplateMutationTx](database))
		h.SetApplyQueue(queue)
		h.SetTaskOutbox(queries)
		return h
	}()
	deps.PlatformBaselineCoverage = handler.NewPlatformBaselineCoverageHandler(queries)
	deps.Quotas = func() *handler.QuotaHandler {
		h := handler.NewQuotaHandler(queries)
		h.SetRunTx(sqlcMutationTxRunner[handler.QuotaMutationTx](database))
		return h
	}()
	deps.CloudCredentials = c.cloudCredentialsHandler
	deps.Maintenance = func() *handler.MaintenanceHandler {
		h := handler.NewMaintenanceHandler(queries, c.maintenanceEvaluator)
		h.SetRunTx(sqlcMutationTxRunner[handler.MaintenanceMutationTx](database))
		return h
	}()
	deps.Dashboards = c.dashboardsHandler
	deps.GitOps = func() *handler.GitOpsHandler {
		gitopsRuntime := tasks.GitOpsRuntime{Deps: tasks.GitOpsDeps{
			Queries: queries, Enqueuer: queue, TaskOutbox: queries,
			Decryptor: c.encryptor, Log: logger,
		}}
		h := handler.NewGitOpsHandler(queries, handler.DefaultGitOpsSyncRunner(gitopsRuntime), logger)
		h.SetRunTx(sqlcMutationTxRunner[handler.GitOpsMutationTx](database))
		h.SetTaskOutbox(queries)
		h.SetAuditWriter(queries)
		h.SetEncryptor(c.encryptor)
		h.SetWebhookSecret(cfg.GitopsWebhookSecret)
		return h
	}()
	deps.ProjectCatalogs = c.projectCatalogsHandler
	deps.ComplianceBaselines = handler.NewComplianceBaselinesHandlerFromPool(database.Pool(), logger)
	deps.KubectlShell = c.kubectlShell
	deps.ClusterGroups = c.clusterGroupsHandler
	deps.Vault = c.vaultHandler
	deps.ClusterResources = c.clusterResourcesHandler
	deps.ServiceMesh = func() *handler.ServiceMeshHandler {
		h := handler.NewServiceMeshHandler(queries)
		h.SetRequester(c.requester)
		h.SetAuditor(queries)
		h.SetAuthorization(rbacEngine, rbacQuerier)
		h.SetDetector(handler.MeshDetectorFunc(c.meshRuntime.DetectAndUpsert))
		return h
	}()
}
