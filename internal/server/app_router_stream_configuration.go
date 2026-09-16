package server

import (
	"log/slog"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/tunnel"
)

// configureRouterStreamAdapters installs optional cross-domain adapters. Core
// stream authentication, authorization, tickets, and audit are constructor
// requirements and are deliberately absent from this phase.
func (c *productionComposition) configureRouterStreamAdapters(logger *slog.Logger, deps *RouterDependencies) {
	if cache := c.rbacQuerier.Cache(); cache != nil && deps.AdminPlatform.GroupMappings != nil {
		deps.AdminPlatform.GroupMappings.SetRBACCacheInvalidator(cache)
	}
	if deps.StreamingInternal.KubectlShell == nil {
		return
	}

	deps.StreamingInternal.KubectlShell.SetStreamAuth(c.jwtManager, c.queries)
	deps.StreamingInternal.KubectlShell.SetStreamTickets(deps.StreamingInternal.StreamTicketStore)
	if deps.StreamingInternal.Exec != nil {
		deps.StreamingInternal.KubectlShell.SetExecProxy(deps.StreamingInternal.Exec)
	}
	deps.StreamingInternal.KubectlShell.SetCrossPodWSForwarder(func(w http.ResponseWriter, r *http.Request, clusterID string) bool {
		return tunnel.ForwardWSToOwnerPod(c.hub, logger, w, r, clusterID)
	})
}
