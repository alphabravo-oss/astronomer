package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
)

// respondClusterAccessError maps tunnel/circuit-breaker failures to stable
// API error codes so UI and agents can distinguish "agent offline" from a
// generic proxy fault. Real user flows hit this whenever a disconnected
// cluster is selected; the previous proxy_error message was opaque.
func respondClusterAccessError(w http.ResponseWriter, r *http.Request, err error) {
	if err == nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "unexpected empty cluster access error")
		return
	}
	switch {
	case errors.Is(err, ErrCircuitOpen):
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.TunnelUnavailable,
			"Cluster agent is temporarily unavailable (circuit breaker open). Reconnect the agent or wait for automatic recovery.")
	case strings.Contains(err.Error(), "cluster agent not connected"):
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.AgentConnectionError,
			"Cluster agent is not connected. Install or reconnect the agent, then retry.")
	default:
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.ProxyError, err.Error())
	}
}
