// Package handler — sprint 17 / migration 065 in-browser kubectl shell.
//
// REST surface (cluster-scoped):
//
//	POST   /api/v1/clusters/{cluster_id}/shell/sessions/
//	GET    /api/v1/clusters/{cluster_id}/shell/sessions/
//	GET    /api/v1/clusters/{cluster_id}/shell/sessions/{id}/
//	POST   /api/v1/clusters/{cluster_id}/shell/sessions/{id}/close/
//	GET    /api/v1/clusters/{cluster_id}/shell/sessions/{id}/commands/
//	GET    /api/v1/ws/clusters/{cluster_id}/shell/sessions/{id}/
//
// REST surface (admin):
//
//	GET    /api/v1/admin/shell-sessions/
//	GET    /api/v1/admin/shell-sessions/{id}/commands/
//
// All cluster-scoped routes are gated on clusters:update — opening a
// privileged shell isn't a read action. The WS endpoint additionally
// validates that the session row belongs to the caller (operators
// can't hijack each other's sessions). Admin routes are superuser-only
// inside the handler (mirrors admin_drill.go).
//
// Provisioning + RBAC mirroring lives in internal/kubectl; these handlers
// own the HTTP envelope, scope checks, and audit recording.

package handler

import (
	"context"
	"log/slog"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/kubectl"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/google/uuid"
)

// KubectlShellQuerier is the slice of *sqlc.Queries the handler needs.
type KubectlShellQuerier interface {
	kubectl.SessionQuerier
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
	GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error)
	CountKubectlSessionCommandsForSessions(ctx context.Context, sessionIDs []uuid.UUID) ([]sqlc.CountKubectlSessionCommandsForSessionsRow, error)
	ListAllActiveKubectlSessionsPage(ctx context.Context, arg sqlc.ListAllActiveKubectlSessionsPageParams) ([]sqlc.KubectlSession, error)
	CountAllActiveKubectlSessions(ctx context.Context) (int64, error)
}

// KubectlBindingsQuerier resolves the caller's RBAC bindings so the
// handler can map them to an EffectiveVerbs bundle without re-running
// the middleware (which already enforced clusters:update). Same shape
// as rbac.BindingQuerier.
type KubectlBindingsQuerier interface {
	GetUserBindings(ctx context.Context, userID string) ([]rbac.RoleBinding, error)
}

// KubectlShellHandler owns the REST + WS surface.
type KubectlShellHandler struct {
	Queries    KubectlShellQuerier
	Bindings   KubectlBindingsQuerier
	RBACEngine *rbac.Engine
	Deps       kubectl.Deps
	Log        *slog.Logger
	// JWT is the manager used by HandleWS to authenticate the WebSocket
	// upgrade request via ticket/query/header auth. Browsers cannot set
	// Authorization headers on WS handshakes, so the SPA passes the JWT
	// in the URL. Without this the route 401s — see the sibling
	// /api/v1/ws/exec/ + /api/v1/ws/logs/ routes which solve the same
	// problem the same way (via auth.AuthorizeStreamRequest).
	JWT *auth.JWTManager
	// TokenQueries lets AuthorizeStreamRequest verify api_token-style
	// bearer tokens (astro_*) against the DB. Nil-safe: JWT-only mode
	// works without it.
	TokenQueries  auth.TokenQuerier
	StreamTickets *auth.StreamTicketStore
	// Exec is the cluster-agent exec relay HandleWS bridges onto after
	// upgrading the inbound WebSocket. When nil the WS endpoint 503s —
	// we still register the route so the SPA gets a stable error rather
	// than a 404. Wired by the server boot path; tests can leave it nil
	// (the non-WS endpoints don't need it).
	Exec ExecProxy
	// crossPodWSForwarder lets HandleWS hand off the WS upgrade to a
	// sibling pod when the cluster's tunnel is held by a sibling
	// replica. Mirrors the HTTP-side forwardToOwnerPod fallback in
	// internal/tunnel/proxy.go. nil-safe: when unset the handler stays
	// on the local-only path (single-pod deployments).
	crossPodWSForwarder CrossPodWSForwarder
}

// NewKubectlShellHandler builds a wired handler. Any nil dep degrades
// the relevant endpoint to 503; the route is still registered so the
// frontend gets a stable 503 instead of a 404.
func NewKubectlShellHandler(queries KubectlShellQuerier, bindings KubectlBindingsQuerier, engine *rbac.Engine, deps kubectl.Deps) *KubectlShellHandler {
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	return &KubectlShellHandler{
		Queries:    queries,
		Bindings:   bindings,
		RBACEngine: engine,
		Deps:       deps,
		Log:        deps.Log,
	}
}

// logger returns the handler's logger, falling back to slog.Default()
// when the handler was constructed without one. HandleWS reaches for
// this on the WS-accept failure path; the rest of the file uses h.Log
// directly because those paths are guarded by NewKubectlShellHandler's
// default.
func (h *KubectlShellHandler) logger() *slog.Logger {
	if h != nil && h.Log != nil {
		return h.Log
	}
	return slog.Default()
}

// KubectlK8sRequesterAdapter wraps a handler-side K8sRequester in the
// shape kubectl.K8sRequester expects. Used by NewApp wiring so the
// in-cluster shell objects flow through the same tunnel circuit-
// breaker as every other tunnel mutation.
type kubectlRequesterAdapter struct{ r K8sRequester }

// Do implements kubectl.K8sRequester.
func (a kubectlRequesterAdapter) Do(ctx context.Context, clusterID, method, path string, body []byte, headers map[string]string) (*kubectl.K8sResponse, error) {
	resp, err := a.r.Do(ctx, clusterID, method, path, body, headers)
	if err != nil {
		return nil, err
	}
	b, _ := decodeResponseBody(resp)
	return &kubectl.K8sResponse{StatusCode: resp.StatusCode, Body: b}, nil
}

// KubectlK8sRequesterFromHandlerRequester adapts a handler.K8sRequester
// into the surface kubectl.Open / Close / Reap take. Mirrors
// ProjectK8sRequesterFromHandlerRequester. Returns nil when given nil
// so server.NewApp can pass through a missing tunnel hub cleanly.
func KubectlK8sRequesterFromHandlerRequester(r K8sRequester) kubectl.K8sRequester {
	if r == nil {
		return nil
	}
	return kubectlRequesterAdapter{r: r}
}
