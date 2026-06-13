// Per-cluster apiserver allow-list handler (migration 070).
//
// Routes owned by this handler (all under /api/v1):
//
//   GET    /clusters/{cluster_id}/apiserver-allowlist/             — current desired + effective + sync_status
//   PUT    /clusters/{cluster_id}/apiserver-allowlist/             — update operator CIDR list + mode
//   POST   /clusters/{cluster_id}/apiserver-allowlist/reconcile/   — fire reconciler on-demand
//   GET    /clusters/{cluster_id}/apiserver-allowlist/snapshots/   — paginated history
//   GET    /clusters/{cluster_id}/apiserver-allowlist/preview/     — render desired without writing
//
// RBAC: list/get/preview gated on clusters:read; mutating endpoints
// (PUT + POST /reconcile/) on clusters:update. We don't introduce a
// separate "allowlist" RBAC resource — operators who can update a
// cluster also own its access posture.
//
// Audit:
//   - cluster.apiserver_allowlist.updated       on every PUT
//   - cluster.apiserver_allowlist.reconciled    on every on-demand /reconcile/
//   - cluster.apiserver_allowlist.drift_detected/applied are stamped by the
//     reconciler worker (NOT by this handler) so the audit log reflects
//     the actual provider response.
//   - All audit entries carry a HASH of the CIDR list, never the
//     cleartext list — CIDRs are operator network topology, treated
//     as sensitive.
//
// Enforce-upgrade safety:
//   - Switching mode monitor→enforce requires force_apply=true on the
//     PUT body when there's an outstanding drift. The handler returns
//     409 mode_change_requires_force otherwise so a misclick can't
//     instantly close the apiserver to the operator's own bastion.

package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"github.com/alphabravocompany/astronomer-go/internal/apisvr/allowlist"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// ApiserverAllowlistQuerier is the DB surface ApiserverAllowlistHandler
// needs. Defined narrow so tests can stand up a fake.
type ApiserverAllowlistQuerier interface {
	GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error)
	GetApiserverAllowlistByClusterID(ctx context.Context, clusterID uuid.UUID) (sqlc.ApiserverAllowlist, error)
	UpsertApiserverAllowlist(ctx context.Context, arg sqlc.UpsertApiserverAllowlistParams) (sqlc.ApiserverAllowlist, error)
	ListApiserverAllowlistSnapshots(ctx context.Context, arg sqlc.ListApiserverAllowlistSnapshotsParams) ([]sqlc.ApiserverAllowlistSnapshot, error)
}

// ApiserverAllowlistEnqueuer fires on-demand reconcile tasks. *asynq.Client
// satisfies this; tests pass a stub. Nil-safe — when unwired the periodic
// 15m sweep is the safety net.
type ApiserverAllowlistEnqueuer interface {
	Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error)
}

// ApiserverAllowlistReconcileFunc is the in-process invocation hook the
// /reconcile/ endpoint uses when no asynq client is wired. The reconciler
// worker exposes ReconcileApiserverAllowlistOnce; the routes file passes
// it in.
type ApiserverAllowlistReconcileFunc func(ctx context.Context, clusterID uuid.UUID) error

// ApiserverAllowlistTaskBuilder returns the asynq task envelope for one
// cluster reconcile. Decoupled so the handler doesn't import the worker
// package and create a cycle.
type ApiserverAllowlistTaskBuilder func(clusterID uuid.UUID) (*asynq.Task, error)

// ApiserverAllowlistHandler owns the per-cluster allow-list REST surface.
type ApiserverAllowlistHandler struct {
	queries     ApiserverAllowlistQuerier
	enqueuer    ApiserverAllowlistEnqueuer
	auditor     any // auditWriterV1 surface — recordAudit type-asserts internally
	reconciler  ApiserverAllowlistReconcileFunc
	taskBuilder ApiserverAllowlistTaskBuilder
	// AstronomerEgress is the runtime-known tunnel egress CIDR list,
	// used by the /preview/ endpoint. Defaults to empty (the renderer
	// falls back to AstronomerEgressFromEnv on render).
	AstronomerEgress []string
	// EmergencyAccess is the global emergency-access CIDR list.
	EmergencyAccess []string
}

// NewApiserverAllowlistHandler wires the handler. The reconciler hook +
// asynq client are optional; without them the on-demand /reconcile/
// endpoint falls back to no-op (the periodic sweep still runs).
func NewApiserverAllowlistHandler(queries ApiserverAllowlistQuerier) *ApiserverAllowlistHandler {
	return &ApiserverAllowlistHandler{queries: queries}
}

// SetAuditor attaches the audit writer.
func (h *ApiserverAllowlistHandler) SetAuditor(a any) { h.auditor = a }

// SetEnqueuer wires the asynq client.
func (h *ApiserverAllowlistHandler) SetEnqueuer(e ApiserverAllowlistEnqueuer) { h.enqueuer = e }

// SetReconciler wires the in-process reconcile hook (used by the
// /reconcile/ endpoint when no asynq client is wired and as the
// post-PUT immediate-trigger path in unit tests).
func (h *ApiserverAllowlistHandler) SetReconciler(f ApiserverAllowlistReconcileFunc) {
	h.reconciler = f
}

// SetTaskBuilder wires the asynq task factory.
func (h *ApiserverAllowlistHandler) SetTaskBuilder(b ApiserverAllowlistTaskBuilder) {
	h.taskBuilder = b
}

// SetAstronomerEgress sets the egress CIDR list used by the /preview/
// renderer. The handler's stored value takes precedence; if empty the
// renderer falls back to AstronomerEgressFromEnv.
func (h *ApiserverAllowlistHandler) SetAstronomerEgress(c []string) { h.AstronomerEgress = c }

// SetEmergencyAccess sets the emergency-access CIDR list.
func (h *ApiserverAllowlistHandler) SetEmergencyAccess(c []string) { h.EmergencyAccess = c }

// ----------------------------------------------------------------------
// Wire DTOs
// ----------------------------------------------------------------------

// AllowlistResponse is the GET / preview response shape.
type AllowlistResponse struct {
	ClusterID        uuid.UUID  `json:"cluster_id"`
	OperatorCIDRs    []string   `json:"operator_cidrs"`
	AstronomerEgress []string   `json:"astronomer_egress"`
	Emergency        []string   `json:"emergency"`
	Desired          []string   `json:"desired"`
	Effective        []string   `json:"effective"`
	Mode             string     `json:"mode"`
	DetectedProvider string     `json:"detected_provider"`
	SyncStatus       string     `json:"sync_status"`
	LastError        string     `json:"last_error,omitempty"`
	LastReconciledAt *time.Time `json:"last_reconciled_at,omitempty"`
	Drift            bool       `json:"drift"`
}

// AllowlistUpdateRequest is the PUT body.
type AllowlistUpdateRequest struct {
	CIDRs []string `json:"cidrs"`
	Mode  string   `json:"mode"`
	// ForceApply, when true, allows a monitor → enforce mode change to
	// succeed even when the row currently has outstanding drift. Without
	// it the handler returns 409 mode_change_requires_force.
	ForceApply bool `json:"force_apply,omitempty"`
}

// SnapshotResponseEntry is the wire DTO for a single allowlist-snapshot row.
type SnapshotResponseEntry struct {
	ID             int64     `json:"id"`
	ClusterID      uuid.UUID `json:"cluster_id"`
	CapturedAt     time.Time `json:"captured_at"`
	EffectiveCIDRs []string  `json:"effective_cidrs"`
	DesiredCIDRs   []string  `json:"desired_cidrs"`
	Drift          bool      `json:"drift"`
}

// ----------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------

var validModes = map[string]struct{}{
	"monitor":  {},
	"enforce":  {},
	"disabled": {},
}

// hashCIDRs returns a short hex digest of the (sorted) CIDR list. Used
// in audit entries so we can prove "this is the same list" without
// recording the cleartext CIDRs (which are sensitive).
func hashCIDRs(cidrs []string) string {
	canonical, _ := allowlist.ValidateCIDRs(cidrs)
	joined := ""
	for _, c := range canonical {
		joined += c + "\n"
	}
	sum := sha256.Sum256([]byte(joined))
	return hex.EncodeToString(sum[:8])
}

func decodeCIDRSlice(raw []byte) []string {
	if len(raw) == 0 {
		return []string{}
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return []string{}
	}
	return out
}

func parseAllowlistClusterID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return uuid.Nil, false
	}
	return id, true
}

// emptyAllowlistResponseFor returns the GET / preview body for a cluster
// that has no apiserver_allowlists row yet — defaults equivalent to a
// fresh insert without any operator CIDRs.
func (h *ApiserverAllowlistHandler) emptyAllowlistResponseFor(clusterID uuid.UUID) AllowlistResponse {
	egress := h.AstronomerEgress
	if len(egress) == 0 {
		egress = allowlist.AstronomerEgressFromEnv()
	}
	desired := allowlist.Render(nil, egress, h.EmergencyAccess)
	return AllowlistResponse{
		ClusterID:        clusterID,
		OperatorCIDRs:    []string{},
		AstronomerEgress: egress,
		Emergency:        h.EmergencyAccess,
		Desired:          desired,
		Effective:        []string{},
		Mode:             "monitor",
		DetectedProvider: "unknown",
		SyncStatus:       "pending",
	}
}

func (h *ApiserverAllowlistHandler) rowToResponse(row sqlc.ApiserverAllowlist) AllowlistResponse {
	operator := decodeCIDRSlice(row.Cidrs)
	effective := decodeCIDRSlice(row.EffectiveCidrs)
	egress := h.AstronomerEgress
	if len(egress) == 0 {
		egress = allowlist.AstronomerEgressFromEnv()
	}
	desired := allowlist.Render(operator, egress, h.EmergencyAccess)
	resp := AllowlistResponse{
		ClusterID:        row.ClusterID,
		OperatorCIDRs:    operator,
		AstronomerEgress: egress,
		Emergency:        h.EmergencyAccess,
		Desired:          desired,
		Effective:        effective,
		Mode:             row.Mode,
		DetectedProvider: row.DetectedProvider,
		SyncStatus:       row.SyncStatus,
		LastError:        row.LastError,
		Drift:            row.SyncStatus == "drifting",
	}
	if row.LastReconciledAt.Valid {
		t := row.LastReconciledAt.Time
		resp.LastReconciledAt = &t
	}
	return resp
}

// ----------------------------------------------------------------------
// Handlers
// ----------------------------------------------------------------------

// Get handles GET /clusters/{cluster_id}/apiserver-allowlist/.
func (h *ApiserverAllowlistHandler) Get(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := parseAllowlistClusterID(w, r)
	if !ok {
		return
	}
	if _, err := h.queries.GetClusterByID(r.Context(), clusterID); err != nil {
		RespondRequestError(w, r, http.StatusNotFound, "not_found", "Cluster not found")
		return
	}
	row, err := h.queries.GetApiserverAllowlistByClusterID(r.Context(), clusterID)
	if err != nil {
		// Treat "no row yet" as the empty-default response so the UI can
		// render the page on a brand-new cluster without seeding a row
		// first. Operators see "no policy yet" and can PUT to create.
		RespondJSON(w, http.StatusOK, h.emptyAllowlistResponseFor(clusterID))
		return
	}
	RespondJSON(w, http.StatusOK, h.rowToResponse(row))
}

// Update handles PUT /clusters/{cluster_id}/apiserver-allowlist/.
func (h *ApiserverAllowlistHandler) Update(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := parseAllowlistClusterID(w, r)
	if !ok {
		return
	}
	if _, err := h.queries.GetClusterByID(r.Context(), clusterID); err != nil {
		RespondRequestError(w, r, http.StatusNotFound, "not_found", "Cluster not found")
		return
	}
	var req AllowlistUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	if req.Mode == "" {
		req.Mode = "monitor"
	}
	if _, ok := validModes[req.Mode]; !ok {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_mode", fmt.Sprintf("mode must be one of monitor|enforce|disabled, got %q", req.Mode))
		return
	}
	canonical, err := allowlist.ValidateCIDRs(req.CIDRs)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_cidr", err.Error())
		return
	}

	// Enforce-upgrade safety: monitor → enforce with outstanding drift
	// requires explicit force_apply.
	existing, existsErr := h.queries.GetApiserverAllowlistByClusterID(r.Context(), clusterID)
	isUpgrade := existsErr == nil && existing.Mode == "monitor" && req.Mode == "enforce"
	if isUpgrade && existing.SyncStatus == "drifting" && !req.ForceApply {
		RespondRequestError(w, r, http.StatusConflict, "mode_change_requires_force",
			"Switching to enforce while drift exists requires force_apply=true; the apiserver allow-list would change on the next reconcile.")
		return
	}

	cidrsJSON, _ := json.Marshal(canonical)
	row, err := h.queries.UpsertApiserverAllowlist(r.Context(), sqlc.UpsertApiserverAllowlistParams{
		ClusterID: clusterID,
		Cidrs:     cidrsJSON,
		Mode:      req.Mode,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "update_error", "Failed to update apiserver allow-list")
		return
	}
	h.audit(r, "cluster.apiserver_allowlist.updated", clusterID, map[string]any{
		"mode":        req.Mode,
		"cidrs_hash":  hashCIDRs(canonical),
		"cidrs_count": len(canonical),
		"force_apply": req.ForceApply,
		"prev_mode":   existing.Mode,
		"prev_status": existing.SyncStatus,
	})
	// Fire an immediate reconcile (best-effort).
	h.fireReconcile(r.Context(), clusterID)
	RespondJSON(w, http.StatusOK, h.rowToResponse(row))
}

// Reconcile handles POST /clusters/{cluster_id}/apiserver-allowlist/reconcile/.
func (h *ApiserverAllowlistHandler) Reconcile(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := parseAllowlistClusterID(w, r)
	if !ok {
		return
	}
	if _, err := h.queries.GetClusterByID(r.Context(), clusterID); err != nil {
		RespondRequestError(w, r, http.StatusNotFound, "not_found", "Cluster not found")
		return
	}
	if _, err := h.queries.GetApiserverAllowlistByClusterID(r.Context(), clusterID); err != nil {
		RespondRequestError(w, r, http.StatusNotFound, "not_found", "No allow-list policy on this cluster")
		return
	}
	h.fireReconcile(r.Context(), clusterID)
	h.audit(r, "cluster.apiserver_allowlist.reconciled", clusterID, nil)
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write([]byte(`{"status":"accepted"}`))
}

// Snapshots handles GET /clusters/{cluster_id}/apiserver-allowlist/snapshots/.
func (h *ApiserverAllowlistHandler) Snapshots(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := parseAllowlistClusterID(w, r)
	if !ok {
		return
	}
	limit := int32(20)
	offset := int32(0)
	if s := r.URL.Query().Get("limit"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 && v <= 200 {
			limit = int32(v)
		}
	}
	if s := r.URL.Query().Get("offset"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v >= 0 {
			offset = int32(v)
		}
	}
	rows, err := h.queries.ListApiserverAllowlistSnapshots(r.Context(), sqlc.ListApiserverAllowlistSnapshotsParams{
		ClusterID: clusterID,
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "list_error", "Failed to list snapshots")
		return
	}
	out := make([]SnapshotResponseEntry, 0, len(rows))
	for _, row := range rows {
		out = append(out, SnapshotResponseEntry{
			ID:             row.ID,
			ClusterID:      row.ClusterID,
			CapturedAt:     row.CapturedAt,
			EffectiveCIDRs: decodeCIDRSlice(row.EffectiveCidrs),
			DesiredCIDRs:   decodeCIDRSlice(row.DesiredCidrs),
			Drift:          row.Drift,
		})
	}
	RespondJSON(w, http.StatusOK, map[string]any{"items": out})
}

// Preview handles GET /clusters/{cluster_id}/apiserver-allowlist/preview/.
// Returns the rendered desired state without persisting or hitting the
// cloud provider.
func (h *ApiserverAllowlistHandler) Preview(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := parseAllowlistClusterID(w, r)
	if !ok {
		return
	}
	if _, err := h.queries.GetClusterByID(r.Context(), clusterID); err != nil {
		RespondRequestError(w, r, http.StatusNotFound, "not_found", "Cluster not found")
		return
	}
	row, err := h.queries.GetApiserverAllowlistByClusterID(r.Context(), clusterID)
	if err != nil {
		RespondJSON(w, http.StatusOK, h.emptyAllowlistResponseFor(clusterID))
		return
	}
	RespondJSON(w, http.StatusOK, h.rowToResponse(row))
}

// fireReconcile fans an on-demand reconcile through whichever path is
// wired. asynq.Enqueue wins; the in-process hook is the fallback for
// tests + dev laptops. Best-effort — failures are logged via the worker
// path, not surfaced into the HTTP response (the handler already
// recorded the audit + state row).
func (h *ApiserverAllowlistHandler) fireReconcile(ctx context.Context, clusterID uuid.UUID) {
	if h.enqueuer != nil && h.taskBuilder != nil {
		task, err := h.taskBuilder(clusterID)
		if err == nil {
			_, _ = h.enqueuer.Enqueue(task)
			return
		}
	}
	if h.reconciler != nil {
		_ = h.reconciler(ctx, clusterID)
	}
}

// audit emits the cluster.apiserver_allowlist.* row through the shared
// audit pipeline. Detail is the optional extra fields; cidrs_hash is
// added by the caller so the audit row records "did this list change?"
// without recording the CIDRs themselves.
func (h *ApiserverAllowlistHandler) audit(r *http.Request, action string, clusterID uuid.UUID, detail map[string]any) {
	recordAudit(r, h.auditor, action, "cluster", clusterID.String(), "", detail)
}
