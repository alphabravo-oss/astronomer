package server

import (
	"context"
	"log/slog"

	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/internal/quota"
	"github.com/alphabravocompany/astronomer-go/internal/scanner"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
)

func (c *productionComposition) configureRouterPolicies(ctx context.Context, logger *slog.Logger, deps *RouterDependencies) {
	database := c.database
	queries := c.queries

	deps.Alerting.SetEnqueuer(c.queue)
	deps.Alerting.SetEventBus(c.bus)
	c.apiserverAllowlistHandler.SetEnqueuer(c.queue)
	c.apiserverAllowlistHandler.SetTaskBuilder(tasks.NewApiserverAllowlistReconcileTask)
	if deps.SettingsCache != nil {
		deps.Alerting.SetSettingsCache(deps.SettingsCache)
		if deps.Auth != nil {
			deps.Auth.SetSettingsCache(deps.SettingsCache)
		}
		if deps.Resources != nil {
			deps.Resources.SetSettingsCache(deps.SettingsCache)
		}
	}
	if queries != nil {
		readAuditEvaluator := appmiddleware.NewPolicyEvaluator(queries)
		readAuditHandler := handler.NewReadAuditPolicyHandler(queries, logger)
		readAuditHandler.SetAuditWriter(queries)
		readAuditHandler.SetRunTx(sqlcMutationTxRunner[handler.ReadAuditPolicyMutationTx](database))
		readAuditHandler.SetCacheInvalidator(readAuditEvaluator)
		deps.ReadAuditEvaluator = readAuditEvaluator
		deps.ReadAuditPolicies = readAuditHandler
	}
	if deps.PlatformSettings != nil && deps.SettingsCache != nil {
		deps.PlatformSettings.SetCache(deps.SettingsCache)
	}
	if deps.Dashboards != nil && deps.SettingsCache != nil {
		deps.Dashboards.SetSettingsCache(deps.SettingsCache)
	}

	quotaEnforcer := quota.New(queries, logger)
	quota.MustRegister()
	quota.StartReporter(ctx, queries, logger)
	scanner.MustRegisterMetrics()
	if deps.Clusters != nil {
		deps.Clusters.SetQuotaEnforcer(quotaEnforcer)
	}
	if deps.Auth != nil {
		deps.Auth.SetQuotaEnforcer(quotaEnforcer)
	}
	if deps.RBAC != nil {
		deps.RBAC.SetQuotaEnforcer(quotaEnforcer)
	}

	maintenanceGate := handler.NewMaintenanceGate(c.maintenanceEvaluator, queries, c.encryptor)
	maintenanceGate.SetRunTx(sqlcMutationTxRunner[handler.MaintenanceGateMutationTx](database))
	if deps.Clusters != nil {
		deps.Clusters.SetMaintenanceGate(maintenanceGate)
	}
	if deps.Projects != nil {
		deps.Projects.SetMaintenanceGate(maintenanceGate)
	}
	if deps.Tools != nil {
		deps.Tools.SetMaintenanceGate(maintenanceGate)
	}
	if deps.Catalog != nil {
		deps.Catalog.SetMaintenanceGate(maintenanceGate)
	}
	if deps.ClusterTemplates != nil {
		deps.ClusterTemplates.SetMaintenanceGate(maintenanceGate)
	}
}
