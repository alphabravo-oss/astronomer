package server

import (
	"context"
	"log/slog"

	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/internal/platformsettings"
	"github.com/alphabravocompany/astronomer-go/internal/quota"
	"github.com/alphabravocompany/astronomer-go/internal/scanner"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
)

func (c *productionComposition) configureRouterPolicies(ctx context.Context, logger *slog.Logger, deps *RouterDependencies) {
	database := c.database
	queries := c.queries

	deps.AdminPlatform.Alerting.SetEventBus(c.bus)
	c.apiserverAllowlistHandler.SetTaskBuilder(tasks.NewApiserverAllowlistReconcileTask)
	if deps.CoreAuth.SettingsCache != nil {
		deps.AdminPlatform.Alerting.SetSettingsCache(deps.CoreAuth.SettingsCache)
		if deps.CoreAuth.Auth != nil {
			deps.CoreAuth.Auth.SetSettingsCache(deps.CoreAuth.SettingsCache)
		}
		if deps.ClusterResources.Resources != nil {
			deps.ClusterResources.Resources.SetSettingsCache(deps.CoreAuth.SettingsCache)
		}
	}
	if queries != nil {
		readAuditEvaluator := appmiddleware.NewPolicyEvaluator(queries)
		readAuditEvaluator.SetReadTierReader(func(ctx context.Context) string {
			tier, err := deps.CoreAuth.SettingsCache.StringValueWithError(ctx, platformsettings.ReadAuditTierKey, "standard")
			if err != nil || (tier != "standard" && tier != "diagnostic" && tier != "incident") {
				// Unavailable or corrupted settings must not silently weaken read coverage.
				return "incident"
			}
			return tier
		})
		readAuditHandler := handler.NewReadAuditPolicyHandler(queries, logger)
		readAuditHandler.SetAuditWriter(queries)
		readAuditHandler.SetRunTx(sqlcMutationTxRunner[handler.ReadAuditPolicyMutationTx](database))
		readAuditHandler.SetCacheInvalidator(readAuditEvaluator)
		deps.CoreAuth.ReadAuditEvaluator = readAuditEvaluator
		deps.AdminPlatform.ReadAuditPolicies = readAuditHandler
	}
	if deps.AdminPlatform.PlatformSettings != nil && deps.CoreAuth.SettingsCache != nil {
		deps.AdminPlatform.PlatformSettings.SetCache(deps.CoreAuth.SettingsCache)
	}
	if deps.AdminPlatform.Dashboards != nil && deps.CoreAuth.SettingsCache != nil {
		deps.AdminPlatform.Dashboards.SetSettingsCache(deps.CoreAuth.SettingsCache)
	}

	quotaEnforcer := quota.New(queries, logger)
	quota.MustRegister()
	quota.StartReporter(ctx, queries, logger)
	scanner.MustRegisterMetrics()
	if deps.ClusterResources.Clusters != nil {
		deps.ClusterResources.Clusters.SetQuotaEnforcer(quotaEnforcer)
	}
	if deps.CoreAuth.Auth != nil {
		deps.CoreAuth.Auth.SetQuotaEnforcer(quotaEnforcer)
	}
	if deps.CoreAuth.RBAC != nil {
		deps.CoreAuth.RBAC.SetQuotaEnforcer(quotaEnforcer)
	}

	maintenanceGate := handler.NewMaintenanceGate(c.maintenanceEvaluator, queries, c.encryptor)
	maintenanceGate.SetRunTx(sqlcMutationTxRunner[handler.MaintenanceGateMutationTx](database))
	if deps.ClusterResources.Clusters != nil {
		deps.ClusterResources.Clusters.SetMaintenanceGate(maintenanceGate)
	}
	if deps.ClusterResources.Projects != nil {
		deps.ClusterResources.Projects.SetMaintenanceGate(maintenanceGate)
	}
	if deps.ClusterResources.Tools != nil {
		deps.ClusterResources.Tools.SetMaintenanceGate(maintenanceGate)
	}
	if deps.AdminPlatform.Catalog != nil {
		deps.AdminPlatform.Catalog.SetMaintenanceGate(maintenanceGate)
	}
	if deps.ClusterResources.ClusterTemplates != nil {
		deps.ClusterResources.ClusterTemplates.SetMaintenanceGate(maintenanceGate)
	}
}
