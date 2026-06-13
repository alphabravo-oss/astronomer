// Per-cluster service-mesh tile handler (migration 071).
//
// Routes owned by this handler (all under /api/v1):
//
//   GET    /clusters/{cluster_id}/service-mesh/         — current detection
//   POST   /clusters/{cluster_id}/service-mesh/detect/  — fire detection inline
//   GET    /clusters/{cluster_id}/service-mesh/mtls/    — per-namespace mTLS breakdown
//
// All three are gated on clusters:read. The /detect/ POST runs the
// inline detection synchronously so the operator gets immediate
// feedback when they click "Re-detect" — the periodic worker still
// runs on its 5m cadence, this just lets the UI shortcut the wait.
//
// The handler is read-side only: it never installs or modifies the
// mesh. The "Install" button on the frontend deep-links to the
// catalog filtered to ?tag=service-mesh; this handler exposes no
// install verbs.

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/mesh"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// ServiceMeshQuerier is the narrow DB surface this handler uses.
// Tests pass an in-memory fake; production wires *sqlc.Queries.
type ServiceMeshQuerier interface {
	GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error)
	GetClusterServiceMesh(ctx context.Context, clusterID uuid.UUID) (sqlc.ClusterServiceMesh, error)
	UpsertClusterServiceMesh(ctx context.Context, arg sqlc.UpsertClusterServiceMeshParams) (sqlc.ClusterServiceMesh, error)
}

// MeshDetector is the side-effect entry-point invoked by POST .../detect/.
// In production this is a thin closure around tasks.DetectAndUpsert
// — the worker package owns the canonical detection wiring. Tests
// pass a hand-rolled stub that records the call.
type MeshDetector interface {
	DetectAndUpsert(ctx context.Context, clusterID uuid.UUID) error
}

// MeshDetectorFunc adapts a plain function to the MeshDetector
// interface so server-startup can hand the handler a closure over
// tasks.DetectAndUpsert without spawning a one-method wrapper type.
type MeshDetectorFunc func(ctx context.Context, clusterID uuid.UUID) error

// DetectAndUpsert satisfies MeshDetector.
func (f MeshDetectorFunc) DetectAndUpsert(ctx context.Context, clusterID uuid.UUID) error {
	return f(ctx, clusterID)
}

// ServiceMeshHandler owns the /clusters/{cluster_id}/service-mesh/*
// routes.
type ServiceMeshHandler struct {
	queries   ServiceMeshQuerier
	detector  MeshDetector
	requester K8sRequester
	auditor   any // recordAudit type-asserts to auditWriterV1 internally
	authz     authorizationSupport
}

// NewServiceMeshHandler wires the handler with the queries surface
// only; SetDetector / SetRequester / SetAuditor / SetAuthorization
// attach the runtime collaborators.
func NewServiceMeshHandler(queries ServiceMeshQuerier) *ServiceMeshHandler {
	return &ServiceMeshHandler{queries: queries}
}

// SetDetector wires the on-demand detector for POST .../detect/.
func (h *ServiceMeshHandler) SetDetector(d MeshDetector) {
	if h == nil {
		return
	}
	h.detector = d
}

// SetRequester wires the tunnel-backed K8sRequester used by the
// /mtls/ endpoint to fall back to a direct list when PeerAuthentication
// CRs weren't mirrored by sprint 069.
func (h *ServiceMeshHandler) SetRequester(r K8sRequester) {
	if h == nil {
		return
	}
	h.requester = r
}

// SetAuditor wires the audit recorder. Optional — the handler degrades
// to a silent no-op when nil.
func (h *ServiceMeshHandler) SetAuditor(a any) {
	if h == nil {
		return
	}
	h.auditor = a
}

// SetAuthorization wires the RBAC engine. When nil the handler
// trusts the caller's auth context (legacy behaviour); production
// always supplies a real engine.
func (h *ServiceMeshHandler) SetAuthorization(engine *rbac.Engine, querier middleware.RBACQuerier) {
	if h == nil {
		return
	}
	h.authz.SetAuthorization(engine, querier)
}

// ----------------------------------------------------------------------
// DTOs
// ----------------------------------------------------------------------

// ServiceMeshDetectionResponse is the wire DTO returned by GET and POST.
type ServiceMeshDetectionResponse struct {
	ClusterID               uuid.UUID  `json:"cluster_id"`
	DetectedMesh            string     `json:"detected_mesh"`
	DetectedVersion         string     `json:"detected_version"`
	ControlPlaneNamespace   string     `json:"control_plane_namespace"`
	GatewayCount            int32      `json:"gateway_count"`
	VirtualServiceCount     int32      `json:"virtual_service_count"`
	DestinationRuleCount    int32      `json:"destination_rule_count"`
	PeerAuthenticationCount int32      `json:"peer_authentication_count"`
	ServiceProfileCount     int32      `json:"service_profile_count"`
	ServerAuthCount         int32      `json:"server_auth_count"`
	MTLSCoveragePct         int32      `json:"mtls_coverage_pct"`
	LastDetectedAt          *time.Time `json:"last_detected_at,omitempty"`
	LastError               string     `json:"last_error,omitempty"`
}

// MTLSBreakdownResponse is the wire DTO for GET .../mtls/. When the
// detector hasn't mirrored PeerAuthentication / Server CRs, the
// handler degrades to an aggregate-only response — rows is empty
// and notice carries the "scaffolding-only" message.
type MTLSBreakdownResponse struct {
	ClusterID  uuid.UUID         `json:"cluster_id"`
	Mesh       string            `json:"mesh"`
	Coverage   int32             `json:"mtls_coverage_pct"`
	TotalCount int32             `json:"total_count"`
	Rows       []MTLSBreakdownNS `json:"rows"`
	Notice     string            `json:"notice,omitempty"`
}

// MTLSBreakdownNS is one row in the per-namespace mTLS breakdown.
type MTLSBreakdownNS struct {
	Namespace string `json:"namespace"`
	Mode      string `json:"mode"`
	Rules     int    `json:"rules"`
}

// ----------------------------------------------------------------------
// Handlers
// ----------------------------------------------------------------------

// Get handles GET /api/v1/clusters/{cluster_id}/service-mesh/.
func (h *ServiceMeshHandler) Get(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := h.requireCluster(w, r, rbac.VerbRead)
	if !ok {
		return
	}
	row, err := h.queries.GetClusterServiceMesh(r.Context(), clusterID)
	if errors.Is(err, pgx.ErrNoRows) {
		// No detection yet — return the empty/unknown response so
		// the UI can render a stable "no data yet" tile rather than
		// a 404 the user can't act on.
		RespondJSON(w, http.StatusOK, ServiceMeshDetectionResponse{
			ClusterID:    clusterID,
			DetectedMesh: mesh.MeshUnknown,
		})
		return
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "get_error", "Failed to read service mesh detection")
		return
	}
	RespondJSON(w, http.StatusOK, rowToResponse(row))
}

// Detect handles POST /api/v1/clusters/{cluster_id}/service-mesh/detect/.
// Runs the detector inline so the operator-clicked "Re-detect" button
// gets immediate feedback.
func (h *ServiceMeshHandler) Detect(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := h.requireCluster(w, r, rbac.VerbRead)
	if !ok {
		return
	}
	if h.detector == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, "detector_unwired", "Service mesh detector is not configured")
		return
	}
	if err := h.detector.DetectAndUpsert(r.Context(), clusterID); err != nil {
		RespondRequestError(w, r, http.StatusBadGateway, "detect_failed", "Service mesh detection failed: "+err.Error())
		return
	}
	row, err := h.queries.GetClusterServiceMesh(r.Context(), clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "post_detect_read", "Detection finished but the row could not be read back")
		return
	}
	recordAudit(r, h.auditor, "cluster.service_mesh.detected", "cluster", clusterID.String(), "", map[string]any{
		"detected_mesh":    row.DetectedMesh,
		"detected_version": row.DetectedVersion,
	})
	RespondJSON(w, http.StatusOK, rowToResponse(row))
}

// MTLS handles GET /api/v1/clusters/{cluster_id}/service-mesh/mtls/.
//
// Returns a per-namespace breakdown of PeerAuthentication / Server
// rules. When the underlying mirror isn't available the handler
// degrades to the aggregate-only view (rows is empty, notice
// explains why).
func (h *ServiceMeshHandler) MTLS(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := h.requireCluster(w, r, rbac.VerbRead)
	if !ok {
		return
	}
	row, err := h.queries.GetClusterServiceMesh(r.Context(), clusterID)
	if errors.Is(err, pgx.ErrNoRows) {
		RespondJSON(w, http.StatusOK, MTLSBreakdownResponse{
			ClusterID: clusterID,
			Mesh:      mesh.MeshUnknown,
			Notice:    "no detection has run yet; click Re-detect to populate",
		})
		return
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "mtls_error", "Failed to read service mesh detection")
		return
	}
	out := MTLSBreakdownResponse{
		ClusterID: clusterID,
		Mesh:      row.DetectedMesh,
		Coverage:  row.MtlsCoveragePct,
	}
	switch row.DetectedMesh {
	case mesh.MeshIstio:
		out.TotalCount = row.PeerAuthenticationCount
		out.Rows, out.Notice = h.istioMTLSRows(r.Context(), clusterID)
	case mesh.MeshLinkerd:
		out.TotalCount = row.ServerAuthCount
		out.Rows = []MTLSBreakdownNS{}
		// Linkerd's mTLS is workload-level (proxy auth), not
		// namespace-level. The aggregate count is meaningful but the
		// breakdown isn't; document that and leave rows empty.
		out.Notice = "Linkerd reports mTLS coverage at the Server level; per-namespace breakdown not surfaced"
	default:
		out.Rows = []MTLSBreakdownNS{}
		out.Notice = "no mTLS-aware mesh detected"
	}
	RespondJSON(w, http.StatusOK, out)
}

// istioMTLSRows fetches PeerAuthentication CRs through the tunnel
// requester and groups them by namespace. Returns (rows, notice) —
// notice is non-empty when the requester isn't wired or the listing
// failed so the caller can still surface the aggregate count.
func (h *ServiceMeshHandler) istioMTLSRows(ctx context.Context, clusterID uuid.UUID) ([]MTLSBreakdownNS, string) {
	if h.requester == nil {
		return []MTLSBreakdownNS{}, "PeerAuthentication mirror is not wired; aggregate-only view"
	}
	resp, err := h.requester.Do(ctx, clusterID.String(), http.MethodGet,
		"/apis/security.istio.io/v1beta1/peerauthentications", nil, requestHeaders(""))
	if err != nil || resp == nil || resp.StatusCode == http.StatusNotFound {
		return []MTLSBreakdownNS{}, "PeerAuthentication CRD not installed or unreachable; aggregate-only view"
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return []MTLSBreakdownNS{}, "PeerAuthentication list failed; aggregate-only view"
	}
	body, err := decodeResponseBody(resp)
	if err != nil {
		return []MTLSBreakdownNS{}, "PeerAuthentication response was not decodable; aggregate-only view"
	}
	var envelope struct {
		Items []struct {
			Metadata struct {
				Namespace string `json:"namespace"`
			} `json:"metadata"`
			Spec struct {
				MTLS struct {
					Mode string `json:"mode"`
				} `json:"mtls"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return []MTLSBreakdownNS{}, "PeerAuthentication payload was not parseable; aggregate-only view"
	}
	// Group by namespace; pick the strictest mode per namespace.
	byNS := map[string]*MTLSBreakdownNS{}
	for _, it := range envelope.Items {
		ns := it.Metadata.Namespace
		mode := it.Spec.MTLS.Mode
		if mode == "" {
			mode = "UNSET"
		}
		row, ok := byNS[ns]
		if !ok {
			row = &MTLSBreakdownNS{Namespace: ns, Mode: mode}
			byNS[ns] = row
		}
		row.Rules++
		if modeStrength(mode) > modeStrength(row.Mode) {
			row.Mode = mode
		}
	}
	rows := make([]MTLSBreakdownNS, 0, len(byNS))
	for _, v := range byNS {
		rows = append(rows, *v)
	}
	return rows, ""
}

// modeStrength orders mTLS modes from least-to-most strict. Used so
// a namespace with both UNSET and STRICT rules surfaces as STRICT in
// the per-namespace summary.
func modeStrength(mode string) int {
	switch mode {
	case "STRICT":
		return 3
	case "PERMISSIVE":
		return 2
	case "DISABLE":
		return 1
	default:
		return 0
	}
}

// ----------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------

// requireCluster parses the cluster_id URL param, verifies the cluster
// exists, and runs the RBAC check. Returns (clusterID, true) on
// success or sends the HTTP error and returns (uuid.Nil, false).
func (h *ServiceMeshHandler) requireCluster(w http.ResponseWriter, r *http.Request, verb rbac.Verb) (uuid.UUID, bool) {
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return uuid.Nil, false
	}
	if _, err := h.queries.GetClusterByID(r.Context(), clusterID); err != nil {
		RespondRequestError(w, r, http.StatusNotFound, "not_found", "Cluster not found")
		return uuid.Nil, false
	}
	if !h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceClusters, verb) {
		return uuid.Nil, false
	}
	return clusterID, true
}

// rowToResponse projects the sqlc row into the wire DTO.
func rowToResponse(row sqlc.ClusterServiceMesh) ServiceMeshDetectionResponse {
	out := ServiceMeshDetectionResponse{
		ClusterID:               row.ClusterID,
		DetectedMesh:            row.DetectedMesh,
		DetectedVersion:         row.DetectedVersion,
		ControlPlaneNamespace:   row.ControlPlaneNamespace,
		GatewayCount:            row.GatewayCount,
		VirtualServiceCount:     row.VirtualServiceCount,
		DestinationRuleCount:    row.DestinationRuleCount,
		PeerAuthenticationCount: row.PeerAuthenticationCount,
		ServiceProfileCount:     row.ServiceProfileCount,
		ServerAuthCount:         row.ServerAuthCount,
		MTLSCoveragePct:         row.MtlsCoveragePct,
		LastError:               row.LastError,
	}
	if row.LastDetectedAt.Valid {
		t := row.LastDetectedAt.Time
		out.LastDetectedAt = &t
	}
	return out
}
