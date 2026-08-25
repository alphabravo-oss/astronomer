package server

import (
	"log/slog"
	"os"
	"strconv"

	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/hibiken/asynq"
)

func (c *productionComposition) composeManagementRouterDependencies(cfg *config.Config, logger *slog.Logger, deps *RouterDependencies) {
	database := c.database
	queries := c.queries
	rbacQuerier := c.rbacQuerier

	deps.ManagementLogs = func() *handler.ManagementLogsHandler {
		if c.localK8s == nil || c.localNamespace == "" {
			return nil
		}
		h := handler.NewManagementLogsHandler(queries, c.localK8s, c.localNamespace, os.Getenv("RELEASE_NAME"))
		maxLines, _ := strconv.Atoi(os.Getenv("MANAGEMENT_LOGS_MAX_LINES"))
		maxBytes, _ := strconv.Atoi(os.Getenv("MANAGEMENT_LOGS_MAX_BYTES"))
		h.SetCaps(maxLines, maxBytes)
		return h
	}()
	deps.SMTP = c.smtpHandler
	deps.EmailEnqueuer = c.emailEnqueuer
	deps.Webhooks = c.webhookHandler
	deps.SIEMForwarders = c.siemHandler
	deps.NotificationTemplates = func() *handler.NotificationTemplateHandler {
		h := handler.NewNotificationTemplateHandler(queries, logger)
		h.SetAuditWriter(queries)
		h.SetRunTx(sqlcMutationTxRunner[handler.NotificationTemplateMutationTx](database))
		return h
	}()
	deps.GroupMappings = func() *handler.GroupMappingsHandler {
		h := handler.NewGroupMappingsHandler(queries)
		h.SetRunTx(sqlcMutationTxRunner[handler.GroupMappingsMutationTx](database))
		return h
	}()
	deps.SCIM = func() *handler.SCIMHandler {
		h := handler.NewSCIMHandler(queries)
		h.SetJWTManager(c.jwtManager)
		if cache := rbacQuerier.Cache(); cache != nil {
			h.SetRBACCacheInvalidator(cache)
		}
		return h
	}()
	deps.SCIMTokenAdmin = handler.NewSCIMTokenAdminHandler(queries)
	deps.SupportBundle = func() *handler.SupportBundleHandler {
		h := handler.NewSupportBundleHandler(queries, c.localK8s, c.localNamespace)
		h.SetAsynqInspector(asynq.NewInspector(c.redisOpt))
		h.SetDBPool(database.Pool())
		return h
	}()
	deps.Compliance = handler.NewComplianceHandler(queries, c.queue)
	deps.CompliancePosture = handler.NewCompliancePostureHandler(queries, cfg.AuditLogRetentionMonths)
	deps.License = handler.NewLicenseHandler()
	deps.PlatformSettings = func() *handler.PlatformSettingsHandler {
		h := handler.NewPlatformSettingsHandler(queries)
		h.SetRunTx(sqlcMutationTxRunner[handler.PlatformSettingsMutationTx](database))
		return h
	}()
	deps.SettingsCache = c.settingsCache
}
