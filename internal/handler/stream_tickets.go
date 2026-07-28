package handler

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

type StreamTicketHandler struct {
	store *auth.StreamTicketStore
	authz authorizationSupport
}

type StreamTicketRequest struct {
	StreamType string `json:"stream_type"`
	ClusterID  string `json:"cluster_id,omitempty"`
}

type StreamTicketResponse struct {
	Ticket    string `json:"ticket"`
	ExpiresAt string `json:"expires_at"`
}

func NewStreamTicketHandler(store *auth.StreamTicketStore) *StreamTicketHandler {
	return &StreamTicketHandler{store: store}
}

func (h *StreamTicketHandler) SetAuthorization(engine *rbac.Engine, querier middleware.RBACQuerier) {
	if h == nil {
		return
	}
	h.authz.SetAuthorization(engine, querier)
}

func (h *StreamTicketHandler) Create(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.store == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.StreamTicketsUnavailable, "Stream tickets are not configured")
		return
	}
	user, ok := middleware.GetAuthenticatedUser(r.Context())
	if !ok || user == nil || user.ID == "" {
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Authentication required")
		return
	}
	userID, err := uuid.Parse(user.ID)
	if err != nil {
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Invalid authenticated user")
		return
	}
	var req StreamTicketRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	kind := auth.NormalizeStreamKind(req.StreamType)
	if kind == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "stream_type must be one of events, registration, logs, exec, shell")
		return
	}
	var clusterID uuid.UUID
	if kind != auth.StreamKindEvents {
		clusterID, err = uuid.Parse(req.ClusterID)
		if err != nil {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "cluster_id is required for this stream type")
			return
		}
		// Issuance MUST match the WEAKEST redeemer of the kind, or a
		// ticket is mintable but not redeemable (or the reverse). Each
		// kind maps to the gate(s) its relays apply:
		//
		//   logs  → pods:logs OR pods:read — this kind has TWO redeemers.
		//           /api/v1/ws/logs/ enforces pods:logs
		//           (internal/tunnel/logs_consumer.go), while the pod SSE
		//           watch GET /clusters/{id}/pods/watch/ redeems the same
		//           kind behind pods:read (internal/server/routes.go, and
		//           openPodsWatch in frontend/src/lib/api/k8s-watch.ts
		//           mints a 'logs' ticket for exactly that stream). Minting
		//           on pods:read alone grants nothing extra: ws/logs
		//           re-checks pods:logs at redemption. Requiring pods:logs
		//           here would silently drop every pods:read-only role
		//           ('Node Operator', 'Storage Manager') onto the UI's
		//           polling fallback.
		//   exec  → pods:exec        (internal/tunnel/exec_consumer.go)
		//   shell → clusters:update  (KubectlShellHandler.Open's route gate,
		//                             internal/server/routes_cluster_addons.go)
		//
		// exec and logs used to ride on clusters:update / clusters:read,
		// which disagreed with the HTTP k8s-proxy path in both directions.
		// The kubectl shell deliberately stays on clusters:update: it is a
		// break-glass kubectl session against the whole cluster, not exec
		// into one pod, and its session-open route gates on that verb.
		perms := []clusterPermission{{rbac.ResourcePods, rbac.VerbLogs}, {rbac.ResourcePods, rbac.VerbRead}}
		switch kind {
		case auth.StreamKindExec:
			perms = []clusterPermission{{rbac.ResourcePods, rbac.VerbExec}}
		case auth.StreamKindShell:
			perms = []clusterPermission{{rbac.ResourceClusters, rbac.VerbUpdate}}
		}
		if kind == auth.StreamKindExec || kind == auth.StreamKindShell {
			// H2 backstop: an exec/shell ticket is a cluster write /
			// RCE-equivalent credential. A read-scoped API token must
			// not be able to mint one even when the owning user holds
			// the RBAC verb checked below. JWT sessions and legacy
			// empty-scope tokens pass through (see requireTokenScope).
			// Logs tickets stay read-eligible.
			if !requireTokenScope(r, auth.ScopeWriteClusters) {
				RespondRequestError(w, r, http.StatusForbidden, apierror.ScopeDenied, "Token is missing the required scope: "+auth.ScopeWriteClusters)
				return
			}
		}
		if !h.authz.authorizeClusterActionAny(w, r, clusterID, perms...) {
			return
		}
	}
	token, ticket, err := h.store.Issue(userID, kind, clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.TicketError, "Failed to issue stream ticket")
		return
	}
	RespondJSON(w, http.StatusCreated, StreamTicketResponse{
		Ticket:    token,
		ExpiresAt: ticket.ExpiresAt.UTC().Format(time.RFC3339),
	})
}

// requireTokenScope reports whether the request is allowed to perform an
// action requiring `required` from an API-token scope perspective. It
// mirrors middleware.APITokenScopeEnforce's contract inline so a handler
// that issues an exec/shell credential can enforce the same backstop the
// route middleware applies to direct mutations:
//
//   - JWT (dashboard) sessions bypass — session RBAC is their gate.
//   - When the token row isn't in context (tests / unconfigured deploy)
//     we can't enforce scopes; preserve prior behaviour and allow — RBAC
//     remains in the chain.
//   - Pre-044 / empty-scope legacy tokens pass through (opt-in rollout).
//   - Any other API token must carry `required` (or admin / *).
func requireTokenScope(r *http.Request, required string) bool {
	user, _ := middleware.GetAuthenticatedUser(r.Context())
	if user == nil || user.AuthMethod != "api_token" {
		return true
	}
	tok, ok := middleware.GetAuthenticatedAPIToken(r.Context())
	if !ok || tok == nil {
		return true
	}
	scopes, err := auth.ParseTokenScopes(tok.Scopes)
	if err != nil {
		return false
	}
	return auth.ScopeAllowsRequest(scopes, required)
}
