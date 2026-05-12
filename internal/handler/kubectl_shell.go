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
// Provisioning + RBAC mirroring lives in internal/kubectl; this file
// just owns the HTTP envelope, scope checks, and audit recording.

package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/kubectl"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// KubectlShellQuerier is the slice of *sqlc.Queries the handler needs.
type KubectlShellQuerier interface {
	kubectl.SessionQuerier
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
	GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error)
}

// KubectlBindingsQuerier resolves the caller's RBAC bindings so the
// handler can map them to an EffectiveVerbs bundle without re-running
// the middleware (which already enforced clusters:update). Same shape
// as middleware.RBACQuerier.
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

// effectiveVerbsFor derives the EffectiveVerbs bundle for the operator
// against this cluster. Uses the rbac.Engine with bindings looked up
// from the bindings querier. Falls back to read-only on lookup failure
// (the middleware already proved clusters:update, so we know at least
// Update is true; the bindings call here is for the more permissive
// Delete bit).
func (h *KubectlShellHandler) effectiveVerbsFor(r *http.Request, userID string, clusterID uuid.UUID) kubectl.EffectiveVerbs {
	v := kubectl.EffectiveVerbs{Read: true, Update: true}
	if h.Bindings == nil || h.RBACEngine == nil {
		return v
	}
	bindings, err := h.Bindings.GetUserBindings(r.Context(), userID)
	if err != nil {
		return v
	}
	if h.RBACEngine.CheckSuperuser(bindings) {
		v.Superuser = true
		return v
	}
	if h.RBACEngine.CheckPermission(bindings, rbac.ResourceClusters, rbac.VerbDelete, clusterID, uuid.Nil) {
		v.Delete = true
	}
	return v
}

// Open handles POST /clusters/{cluster_id}/shell/sessions/.
func (h *KubectlShellHandler) Open(w http.ResponseWriter, r *http.Request) {
	if h.Queries == nil || h.Deps.Requester == nil {
		RespondError(w, http.StatusServiceUnavailable, "shell_unavailable", "Kubectl shell is not configured")
		return
	}
	clusterID, ok := parseClusterID(r)
	if !ok {
		RespondError(w, http.StatusBadRequest, "invalid_cluster_id", "Invalid cluster_id")
		return
	}
	userID, ok := callerUUID(r)
	if !ok {
		RespondError(w, http.StatusUnauthorized, "authentication_required", "Authentication required")
		return
	}
	if _, err := h.Queries.GetClusterByID(r.Context(), clusterID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondError(w, http.StatusNotFound, "cluster_not_found", "Cluster not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}

	verbs := h.effectiveVerbsFor(r, userID.String(), clusterID)

	info, err := kubectl.Open(r.Context(), h.Deps, kubectl.OpenRequest{
		UserID:    userID,
		ClusterID: clusterID,
		Verbs:     verbs,
		ClientIP:  parseClientIP(r),
		UserAgent: r.UserAgent(),
	})
	if err != nil {
		RespondError(w, http.StatusBadGateway, "shell_open_failed", err.Error())
		return
	}
	recordAudit(r, h.Queries, "kubectl.session.opened", "cluster", clusterID.String(), "", map[string]any{
		"session_id":    info.ID.String(),
		"superuser":     verbs.Superuser,
		"verbs":         verbs.Verbs(),
	})
	RespondJSON(w, http.StatusCreated, info)
}

// List handles GET /clusters/{cluster_id}/shell/sessions/.
func (h *KubectlShellHandler) List(w http.ResponseWriter, r *http.Request) {
	if h.Queries == nil {
		RespondError(w, http.StatusServiceUnavailable, "shell_unavailable", "Kubectl shell is not configured")
		return
	}
	clusterID, ok := parseClusterID(r)
	if !ok {
		RespondError(w, http.StatusBadRequest, "invalid_cluster_id", "Invalid cluster_id")
		return
	}
	rows, err := h.Queries.ListActiveKubectlSessionsByCluster(r.Context(), clusterID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	out := make([]kubectl.SessionInfo, 0, len(rows))
	for _, row := range rows {
		count, _ := h.Queries.CountKubectlSessionCommands(r.Context(), row.ID)
		out = append(out, kubectl.ToSessionInfo(row, count, h.idleTimeout()))
	}
	RespondJSON(w, http.StatusOK, out)
}

// Get handles GET /clusters/{cluster_id}/shell/sessions/{id}/.
func (h *KubectlShellHandler) Get(w http.ResponseWriter, r *http.Request) {
	if h.Queries == nil {
		RespondError(w, http.StatusServiceUnavailable, "shell_unavailable", "Kubectl shell is not configured")
		return
	}
	row, ok := h.loadSessionForCluster(w, r)
	if !ok {
		return
	}
	count, _ := h.Queries.CountKubectlSessionCommands(r.Context(), row.ID)
	RespondJSON(w, http.StatusOK, kubectl.ToSessionInfo(row, count, h.idleTimeout()))
}

// Close handles POST /clusters/{cluster_id}/shell/sessions/{id}/close/.
func (h *KubectlShellHandler) Close(w http.ResponseWriter, r *http.Request) {
	if h.Queries == nil {
		RespondError(w, http.StatusServiceUnavailable, "shell_unavailable", "Kubectl shell is not configured")
		return
	}
	row, ok := h.loadSessionForCluster(w, r)
	if !ok {
		return
	}
	if err := kubectl.Close(r.Context(), h.Deps, row.ID); err != nil {
		RespondError(w, http.StatusBadGateway, "shell_close_failed", err.Error())
		return
	}
	recordAudit(r, h.Queries, "kubectl.session.closed", "cluster", row.ClusterID.String(), "", map[string]any{
		"session_id": row.ID.String(),
		"duration_seconds": int64(time.Since(row.StartedAt).Seconds()),
	})
	RespondJSON(w, http.StatusOK, map[string]string{"status": "closed"})
}

// Commands handles GET /clusters/{cluster_id}/shell/sessions/{id}/commands/.
func (h *KubectlShellHandler) Commands(w http.ResponseWriter, r *http.Request) {
	if h.Queries == nil {
		RespondError(w, http.StatusServiceUnavailable, "shell_unavailable", "Kubectl shell is not configured")
		return
	}
	row, ok := h.loadSessionForCluster(w, r)
	if !ok {
		return
	}
	limit := queryInt(r, "limit", 100)
	offset := queryInt(r, "offset", 0)
	if limit < 1 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := h.Queries.ListKubectlSessionCommands(r.Context(), sqlc.ListKubectlSessionCommandsParams{
		SessionID: row.ID,
		Limit:     int32(limit),
		Offset:    int32(offset),
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	total, _ := h.Queries.CountKubectlSessionCommands(r.Context(), row.ID)
	type wireCommand struct {
		CommandAt   time.Time `json:"command_at"`
		CommandLine string    `json:"command_line"`
	}
	out := make([]wireCommand, 0, len(rows))
	for _, c := range rows {
		out = append(out, wireCommand{CommandAt: c.CommandAt, CommandLine: c.CommandLine})
	}
	RespondPaginated(w, r, out, total)
}

// AdminListAll handles GET /admin/shell-sessions/.
func (h *KubectlShellHandler) AdminListAll(w http.ResponseWriter, r *http.Request) {
	if !h.gateSuperuser(w, r) {
		return
	}
	rows, err := h.Queries.ListAllActiveKubectlSessions(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	out := make([]kubectl.SessionInfo, 0, len(rows))
	for _, row := range rows {
		count, _ := h.Queries.CountKubectlSessionCommands(r.Context(), row.ID)
		out = append(out, kubectl.ToSessionInfo(row, count, h.idleTimeout()))
	}
	RespondJSON(w, http.StatusOK, out)
}

// AdminCommands handles GET /admin/shell-sessions/{id}/commands/.
// Superuser sees commands for ANY session, regardless of owner.
func (h *KubectlShellHandler) AdminCommands(w http.ResponseWriter, r *http.Request) {
	if !h.gateSuperuser(w, r) {
		return
	}
	idStr := chi.URLParam(r, "id")
	sessionID, err := uuid.Parse(idStr)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_session_id", "Invalid session id")
		return
	}
	row, err := h.Queries.GetKubectlSessionByID(r.Context(), sessionID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondError(w, http.StatusNotFound, "session_not_found", "Session not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	rows, err := h.Queries.ListKubectlSessionCommands(r.Context(), sqlc.ListKubectlSessionCommandsParams{
		SessionID: row.ID, Limit: 1000, Offset: 0,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	total, _ := h.Queries.CountKubectlSessionCommands(r.Context(), row.ID)
	type wireCommand struct {
		CommandAt   time.Time `json:"command_at"`
		CommandLine string    `json:"command_line"`
	}
	out := make([]wireCommand, 0, len(rows))
	for _, c := range rows {
		out = append(out, wireCommand{CommandAt: c.CommandAt, CommandLine: c.CommandLine})
	}
	RespondPaginated(w, r, out, total)
}

// HandleWS handles GET /ws/clusters/{cluster_id}/shell/sessions/{id}/.
// In v1 we don't multiplex the WS protocol ourselves — we redirect the
// browser at the existing /api/v1/ws/exec/{cluster_id}/{ns}/{pod}/{container}/
// endpoint. The session record carries the pod name we created in Open
// and the WS handler validates the caller owns this row.
//
// This kept the wire surface narrow: the only NEW WS endpoint is this
// session-aware redirect — the actual relay reuses the proven sprint-14
// ExecConsumer.
func (h *KubectlShellHandler) HandleWS(w http.ResponseWriter, r *http.Request) {
	if h.Queries == nil {
		RespondError(w, http.StatusServiceUnavailable, "shell_unavailable", "Kubectl shell is not configured")
		return
	}
	row, ok := h.loadSessionForCluster(w, r)
	if !ok {
		return
	}
	if row.Status != "active" {
		RespondError(w, http.StatusConflict, "session_not_active", "Session is not active")
		return
	}
	// Stamp last_input_at — even just opening the WS counts as "engaged".
	_ = h.Queries.TouchKubectlSessionInput(r.Context(), row.ID)
	// Build the redirect target. Preserve the caller's ?token=... query
	// arg so the browser can pass auth via query param (WS can't set
	// Authorization headers).
	target := "/api/v1/ws/exec/" + row.ClusterID.String() + "/" + row.PodNamespace + "/" + row.PodName + "/" + kubectl.ContainerName + "/"
	if q := r.URL.RawQuery; q != "" {
		target += "?" + q
	}
	// 307 preserves the method (WS handshakes are GETs anyway). Browsers
	// follow this transparently before the Upgrade handshake.
	http.Redirect(w, r, target, http.StatusTemporaryRedirect)
}

// loadSessionForCluster pulls the {cluster_id}/{id} pair from chi and
// returns the row only when it exists, belongs to this cluster, AND
// (for non-superuser callers) belongs to the caller. Writes the
// appropriate error response on failure.
func (h *KubectlShellHandler) loadSessionForCluster(w http.ResponseWriter, r *http.Request) (sqlc.KubectlSession, bool) {
	clusterID, ok := parseClusterID(r)
	if !ok {
		RespondError(w, http.StatusBadRequest, "invalid_cluster_id", "Invalid cluster_id")
		return sqlc.KubectlSession{}, false
	}
	idStr := chi.URLParam(r, "id")
	sessionID, err := uuid.Parse(idStr)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_session_id", "Invalid session id")
		return sqlc.KubectlSession{}, false
	}
	row, err := h.Queries.GetKubectlSessionByID(r.Context(), sessionID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondError(w, http.StatusNotFound, "session_not_found", "Session not found")
			return sqlc.KubectlSession{}, false
		}
		RespondError(w, http.StatusInternalServerError, "db_error", err.Error())
		return sqlc.KubectlSession{}, false
	}
	if row.ClusterID != clusterID {
		RespondError(w, http.StatusNotFound, "session_not_found", "Session not found in this cluster")
		return sqlc.KubectlSession{}, false
	}
	callerID, ok := callerUUID(r)
	if !ok {
		RespondError(w, http.StatusUnauthorized, "authentication_required", "Authentication required")
		return sqlc.KubectlSession{}, false
	}
	if row.UserID != callerID && !h.callerIsSuperuser(r, callerID) {
		RespondError(w, http.StatusForbidden, "session_not_owned", "Session belongs to another operator")
		return sqlc.KubectlSession{}, false
	}
	return row, true
}

func (h *KubectlShellHandler) callerIsSuperuser(r *http.Request, callerID uuid.UUID) bool {
	if h.Queries == nil {
		return false
	}
	u, err := h.Queries.GetUserByID(r.Context(), callerID)
	if err != nil {
		return false
	}
	return u.IsSuperuser
}

func (h *KubectlShellHandler) gateSuperuser(w http.ResponseWriter, r *http.Request) bool {
	callerID, ok := callerUUID(r)
	if !ok {
		RespondError(w, http.StatusUnauthorized, "authentication_required", "Authentication required")
		return false
	}
	if h.Queries == nil {
		RespondError(w, http.StatusServiceUnavailable, "shell_unavailable", "Kubectl shell is not configured")
		return false
	}
	user, err := h.Queries.GetUserByID(r.Context(), callerID)
	if err != nil {
		RespondError(w, http.StatusForbidden, "forbidden", "Caller not found")
		return false
	}
	if !user.IsSuperuser {
		RespondError(w, http.StatusForbidden, "forbidden", "Kubectl shell admin views require superuser privileges")
		return false
	}
	return true
}

func (h *KubectlShellHandler) idleTimeout() time.Duration {
	if h.Deps.IdleTimeout > 0 {
		return h.Deps.IdleTimeout
	}
	return 30 * time.Minute
}

// parseClusterID extracts and parses the {cluster_id} URL param.
func parseClusterID(r *http.Request) (uuid.UUID, bool) {
	idStr := chi.URLParam(r, "cluster_id")
	if idStr == "" {
		return uuid.UUID{}, false
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return uuid.UUID{}, false
	}
	return id, true
}

// callerUUID resolves the authenticated user's UUID from the request
// context. Returns (uuid.Nil, false) on any failure.
func callerUUID(r *http.Request) (uuid.UUID, bool) {
	u, ok := middleware.GetAuthenticatedUser(r.Context())
	if !ok || u == nil {
		return uuid.UUID{}, false
	}
	parsed, err := uuid.Parse(u.ID)
	if err != nil {
		return uuid.UUID{}, false
	}
	return parsed, true
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

// parseClientIP best-effort extracts the client IP. RemoteAddr is the
// closest hop; if a real-IP middleware ran upstream it will have
// overwritten this with the original client. Splits the trailing
// :port off before parsing. Returns nil on parse failure (the column
// is nullable).
func parseClientIP(r *http.Request) *netip.Addr {
	addr := r.RemoteAddr
	if i := strings.LastIndex(addr, ":"); i > 0 {
		// Handle [::1]:1234
		if strings.HasPrefix(addr, "[") {
			if j := strings.Index(addr, "]"); j > 0 {
				addr = addr[1:j]
			}
		} else {
			addr = addr[:i]
		}
	}
	a, err := netip.ParseAddr(addr)
	if err != nil {
		return nil
	}
	return &a
}
