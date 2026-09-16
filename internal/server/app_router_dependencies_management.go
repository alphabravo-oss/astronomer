package server

import (
	"log/slog"

	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/hibiken/asynq"
)

func (c *productionComposition) composeManagementRouterDependencies(cfg *config.Config, logger *slog.Logger, deps *RouterDependencies) {
	database := c.database
	queries := c.queries
	rbacQuerier := c.rbacQuerier

	deps.AdminPlatform.ManagementLogs = func() *handler.ManagementLogsHandler {
		if c.localK8s == nil || c.localNamespace == "" {
			return nil
		}
		h := handler.NewManagementLogsHandler(queries, c.localK8s, c.localNamespace, cfg.ReleaseName)
		h.SetCaps(cfg.ManagementLogsMaxLines, cfg.ManagementLogsMaxBytes)
		return h
	}()
	deps.AdminPlatform.SMTP = c.smtpHandler
	deps.CoreAuth.EmailEnqueuer = c.emailEnqueuer
	deps.AdminPlatform.Webhooks = c.webhookHandler
	deps.AdminPlatform.SIEMForwarders = c.siemHandler
	deps.AdminPlatform.NotificationTemplates = func() *handler.NotificationTemplateHandler {
		h := handler.NewNotificationTemplateHandler(queries, logger)
		h.SetAuditWriter(queries)
		h.SetRunTx(sqlcMutationTxRunner[handler.NotificationTemplateMutationTx](database))
		return h
	}()
	deps.AdminPlatform.GroupMappings = func() *handler.GroupMappingsHandler {
		h := handler.NewGroupMappingsHandler(queries)
		h.SetRunTx(sqlcMutationTxRunner[handler.GroupMappingsMutationTx](database))
		return h
	}()
	deps.CoreAuth.SCIM = func() *handler.SCIMHandler {
		h := handler.NewSCIMHandler(queries)
		h.SetJWTManager(c.jwtManager)
		h.SetRunTx(sqlcMutationTxRunner[handler.SCIMMutationTx](database))
		if cache := rbacQuerier.Cache(); cache != nil {
			h.SetRBACCacheInvalidator(cache)
		}
		return h
	}()
	deps.CoreAuth.SCIMTokenAdmin = handler.NewSCIMTokenAdminHandler(queries)
	deps.AdminPlatform.SupportBundle = func() *handler.SupportBundleHandler {
		h := handler.NewSupportBundleHandler(queries, queries, c.localK8s, c.localNamespace)
		h.SetAsynqInspector(asynq.NewInspector(c.redisOpt))
		h.SetDBPool(database.Pool())
		h.SetRunTx(sqlcMutationTxRunner[handler.SupportBundleMutationTx](database))
		return h
	}()
	deps.AdminPlatform.Compliance = handler.NewComplianceHandler(queries, c.queue)
	deps.AdminPlatform.CompliancePosture = handler.NewCompliancePostureHandler(queries, cfg.AuditLogRetentionMonths)
	deps.AdminPlatform.License = handler.NewLicenseHandler()
	deps.AdminPlatform.PlatformSettings = func() *handler.PlatformSettingsHandler {
		h := handler.NewPlatformSettingsHandler(queries)
		h.SetRunTx(sqlcMutationTxRunner[handler.PlatformSettingsMutationTx](database))
		return h
	}()
	deps.CoreAuth.SettingsCache = c.settingsCache
}
