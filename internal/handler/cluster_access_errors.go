package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/api/apiutil"
)

// respondClusterAccessError maps tunnel/circuit-breaker failures to stable
// API error codes so UI and agents can distinguish "agent offline" from a
// generic proxy fault. Real user flows hit this whenever a disconnected
// cluster is selected; the previous proxy_error message was opaque.
func respondClusterAccessError(w http.ResponseWriter, r *http.Request, err error) {
	if err == nil {
		apiutil.WriteError(w, r, http.StatusInternalServerError, apiutil.InternalError, "unexpected empty cluster access error")
		return
	}
	var upstream *kubernetesResponseError
	switch {
	case errors.As(err, &upstream) && upstream.StatusCode == http.StatusTooManyRequests:
		if retryAfter := upstream.Headers["Retry-After"]; retryAfter != "" {
			w.Header().Set("Retry-After", retryAfter)
		}
		apiutil.WriteError(w, r, http.StatusTooManyRequests, apiutil.AgentOverloaded,
			"The cluster agent is busy. Retry the request shortly.")
	case errors.Is(err, ErrCircuitOpen):
		apiutil.WriteError(w, r, http.StatusServiceUnavailable, apiutil.TunnelUnavailable,
			"Cluster agent is temporarily unavailable (circuit breaker open). Reconnect the agent or wait for automatic recovery.")
	case strings.Contains(err.Error(), "cluster agent not connected"):
		apiutil.WriteError(w, r, http.StatusServiceUnavailable, apiutil.AgentConnectionError,
			"Cluster agent is not connected. Install or reconnect the agent, then retry.")
	default:
		apiutil.WriteError(w, r, http.StatusServiceUnavailable, apiutil.ProxyError, err.Error())
	}
}
