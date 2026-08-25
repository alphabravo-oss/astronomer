package server

import (
	"log/slog"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/tunnel"
)

func (c *productionComposition) configureRouterStreams(cfg *config.Config, logger *slog.Logger, deps *RouterDependencies) {
	jwtManager := c.jwtManager
	queries := c.queries
	rbacEngine := c.rbacEngine
	rbacQuerier := c.rbacQuerier

	deps.EventStream.SetAuth(jwtManager, queries)
	deps.EventStream.SetStreamTickets(deps.StreamTicketStore)
	deps.EventStream.SetAuthorization(rbacEngine, rbacQuerier)
	deps.EventStream.SetCharlieFindingAuthorization(c.charlieFindingEvents)
	if cache := rbacQuerier.Cache(); cache != nil && deps.GroupMappings != nil {
		deps.GroupMappings.SetRBACCacheInvalidator(cache)
	}

	deps.Exec.SetAuth(jwtManager, queries)
	deps.Exec.SetStreamTickets(deps.StreamTicketStore)
	deps.Exec.SetAuditWriter(queries)
	deps.Exec.SetAuthorization(rbacEngine, rbacQuerier)
	if deps.Logs != nil {
		deps.Logs.SetAuth(jwtManager, queries)
		deps.Logs.SetStreamTickets(deps.StreamTicketStore)
		deps.Logs.SetAuditWriter(queries)
		deps.Logs.SetAuthorization(rbacEngine, rbacQuerier)
	}
	if deps.KubectlShell == nil {
		return
	}

	deps.KubectlShell.SetStreamAuth(jwtManager, queries)
	deps.KubectlShell.SetStreamTickets(deps.StreamTicketStore)
	deps.KubectlShell.SetNamespaceScopedRBAC(cfg.NamespaceScopedRBACEnabled)
	if deps.SettingsCache != nil {
		deps.KubectlShell.SetFeatureFlags(deps.SettingsCache)
	}
	if deps.Exec != nil {
		deps.KubectlShell.SetExecProxy(deps.Exec)
	}
	deps.KubectlShell.SetCrossPodWSForwarder(func(w http.ResponseWriter, r *http.Request, clusterID string) bool {
		return tunnel.ForwardWSToOwnerPod(c.hub, logger, w, r, clusterID)
	})
}
