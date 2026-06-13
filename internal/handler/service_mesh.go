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
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"sigs.k8s.io/yaml"

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

// ServiceMeshInventoryResponse lists first-class mesh policy resources
// surfaced through the tunnel-backed Kubernetes API. Argo-owned resources are
// flagged read-only so the UI can steer users into a GitOps edit flow instead
// of silently overwriting declarative ownership.
type ServiceMeshInventoryResponse struct {
	ClusterID  uuid.UUID                      `json:"cluster_id"`
	Mesh       string                         `json:"mesh"`
	Resources  []ServiceMeshInventoryResource `json:"resources"`
	TotalCount int                            `json:"total_count"`
	Notice     string                         `json:"notice,omitempty"`
}

type ServiceMeshInventoryResource struct {
	Kind       string                     `json:"kind"`
	APIVersion string                     `json:"api_version"`
	Plural     string                     `json:"plural"`
	Count      int                        `json:"count"`
	Items      []ServiceMeshInventoryItem `json:"items"`
	Notice     string                     `json:"notice,omitempty"`
}

type ServiceMeshInventoryItem struct {
	Name        string            `json:"name"`
	Namespace   string            `json:"namespace,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
	ManagedBy   string            `json:"managed_by,omitempty"`
	ReadOnly    bool              `json:"read_only"`
	Reason      string            `json:"reason,omitempty"`
	CreatedAt   *time.Time        `json:"created_at,omitempty"`
}

type ServiceMeshPolicyValidationRequest struct {
	Object json.RawMessage `json:"object,omitempty"`
	YAML   string          `json:"yaml,omitempty"`
}

type ServiceMeshPolicyValidationResponse struct {
	ClusterID    uuid.UUID                            `json:"cluster_id"`
	Valid        bool                                 `json:"valid"`
	APIVersion   string                               `json:"api_version,omitempty"`
	Kind         string                               `json:"kind,omitempty"`
	Name         string                               `json:"name,omitempty"`
	Namespace    string                               `json:"namespace,omitempty"`
	ManagedBy    string                               `json:"managed_by,omitempty"`
	ReadOnly     bool                                 `json:"read_only"`
	ApplyAllowed bool                                 `json:"apply_allowed"`
	Warnings     []ServiceMeshPolicyValidationFinding `json:"warnings"`
	Errors       []ServiceMeshPolicyValidationFinding `json:"errors"`
}

type ServiceMeshPolicyValidationFinding struct {
	Field    string `json:"field,omitempty"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

type serviceMeshResourceDef struct {
	Kind       string
	APIVersion string
	Plural     string
	Path       string
}

var istioInventoryResources = []serviceMeshResourceDef{
	{Kind: "Gateway", APIVersion: "networking.istio.io/v1beta1", Plural: "gateways", Path: "/apis/networking.istio.io/v1beta1/gateways"},
	{Kind: "VirtualService", APIVersion: "networking.istio.io/v1beta1", Plural: "virtualservices", Path: "/apis/networking.istio.io/v1beta1/virtualservices"},
	{Kind: "DestinationRule", APIVersion: "networking.istio.io/v1beta1", Plural: "destinationrules", Path: "/apis/networking.istio.io/v1beta1/destinationrules"},
	{Kind: "AuthorizationPolicy", APIVersion: "security.istio.io/v1beta1", Plural: "authorizationpolicies", Path: "/apis/security.istio.io/v1beta1/authorizationpolicies"},
	{Kind: "PeerAuthentication", APIVersion: "security.istio.io/v1beta1", Plural: "peerauthentications", Path: "/apis/security.istio.io/v1beta1/peerauthentications"},
	{Kind: "Sidecar", APIVersion: "networking.istio.io/v1beta1", Plural: "sidecars", Path: "/apis/networking.istio.io/v1beta1/sidecars"},
	{Kind: "ServiceEntry", APIVersion: "networking.istio.io/v1beta1", Plural: "serviceentries", Path: "/apis/networking.istio.io/v1beta1/serviceentries"},
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

// Inventory handles GET /api/v1/clusters/{cluster_id}/service-mesh/inventory/.
func (h *ServiceMeshHandler) Inventory(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := h.requireClusterResource(w, r, rbac.ResourceServiceMesh, rbac.VerbRead)
	if !ok {
		return
	}
	row, err := h.queries.GetClusterServiceMesh(r.Context(), clusterID)
	if errors.Is(err, pgx.ErrNoRows) {
		row = sqlc.ClusterServiceMesh{ClusterID: clusterID, DetectedMesh: mesh.MeshUnknown}
	} else if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "inventory_error", "Failed to read service mesh detection")
		return
	}

	out := ServiceMeshInventoryResponse{
		ClusterID: clusterID,
		Mesh:      row.DetectedMesh,
		Resources: make([]ServiceMeshInventoryResource, 0, len(istioInventoryResources)),
	}
	if h.requester == nil {
		out.Resources = aggregateInventoryResources(row)
		out.TotalCount = inventoryTotal(out.Resources)
		out.Notice = "Kubernetes tunnel inventory is not configured; showing aggregate detection counts only"
		RespondJSON(w, http.StatusOK, out)
		return
	}

	for _, def := range istioInventoryResources {
		resource := ServiceMeshInventoryResource{
			Kind:       def.Kind,
			APIVersion: def.APIVersion,
			Plural:     def.Plural,
			Items:      []ServiceMeshInventoryItem{},
		}
		items, notice := h.listMeshInventoryItems(r.Context(), clusterID, def)
		resource.Items = items
		resource.Count = len(items)
		resource.Notice = notice
		out.Resources = append(out.Resources, resource)
		out.TotalCount += resource.Count
	}
	RespondJSON(w, http.StatusOK, out)
}

// ValidatePolicy handles POST /api/v1/clusters/{cluster_id}/service-mesh/validate/.
func (h *ServiceMeshHandler) ValidatePolicy(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := h.requireClusterResource(w, r, rbac.ResourceServiceMesh, rbac.VerbUpdate)
	if !ok {
		return
	}
	obj, err := parseServiceMeshPolicyPayload(w, r)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_policy", err.Error())
		return
	}
	out := validateServiceMeshPolicyObject(obj)
	out.ClusterID = clusterID
	if out.Kind != "" {
		if def, ok := serviceMeshResourceDefFor(out.Kind); ok && h.requester != nil {
			if notice := h.checkMeshCRDAvailable(r.Context(), clusterID, def); notice != "" {
				out.Warnings = append(out.Warnings, ServiceMeshPolicyValidationFinding{
					Field:    "kind",
					Severity: "warning",
					Message:  notice,
				})
			}
		}
	}
	out.Valid = len(out.Errors) == 0
	out.ApplyAllowed = out.Valid && !out.ReadOnly
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
	return h.requireClusterResource(w, r, rbac.ResourceClusters, verb)
}

func (h *ServiceMeshHandler) requireClusterResource(w http.ResponseWriter, r *http.Request, resource rbac.Resource, verb rbac.Verb) (uuid.UUID, bool) {
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return uuid.Nil, false
	}
	if _, err := h.queries.GetClusterByID(r.Context(), clusterID); err != nil {
		RespondRequestError(w, r, http.StatusNotFound, "not_found", "Cluster not found")
		return uuid.Nil, false
	}
	if !h.authz.authorizeClusterAction(w, r, clusterID, resource, verb) {
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

func aggregateInventoryResources(row sqlc.ClusterServiceMesh) []ServiceMeshInventoryResource {
	counts := map[string]int{
		"Gateway":            int(row.GatewayCount),
		"VirtualService":     int(row.VirtualServiceCount),
		"DestinationRule":    int(row.DestinationRuleCount),
		"PeerAuthentication": int(row.PeerAuthenticationCount),
	}
	out := make([]ServiceMeshInventoryResource, 0, len(istioInventoryResources))
	for _, def := range istioInventoryResources {
		out = append(out, ServiceMeshInventoryResource{
			Kind:       def.Kind,
			APIVersion: def.APIVersion,
			Plural:     def.Plural,
			Count:      counts[def.Kind],
			Items:      []ServiceMeshInventoryItem{},
			Notice:     "item list unavailable; aggregate count only",
		})
	}
	return out
}

func inventoryTotal(resources []ServiceMeshInventoryResource) int {
	total := 0
	for _, r := range resources {
		total += r.Count
	}
	return total
}

func (h *ServiceMeshHandler) listMeshInventoryItems(ctx context.Context, clusterID uuid.UUID, def serviceMeshResourceDef) ([]ServiceMeshInventoryItem, string) {
	resp, err := h.requester.Do(ctx, clusterID.String(), http.MethodGet, def.Path, nil, requestHeaders(""))
	if err != nil || resp == nil {
		return []ServiceMeshInventoryItem{}, "resource list unavailable through the agent tunnel"
	}
	if resp.StatusCode == http.StatusNotFound {
		return []ServiceMeshInventoryItem{}, "CRD not installed"
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return []ServiceMeshInventoryItem{}, fmt.Sprintf("list failed with Kubernetes status %d", resp.StatusCode)
	}
	body, err := decodeResponseBody(resp)
	if err != nil {
		return []ServiceMeshInventoryItem{}, "resource list response was not decodable"
	}
	var envelope struct {
		Items []struct {
			Metadata struct {
				Name              string            `json:"name"`
				Namespace         string            `json:"namespace"`
				Labels            map[string]string `json:"labels"`
				Annotations       map[string]string `json:"annotations"`
				CreationTimestamp *time.Time        `json:"creationTimestamp"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return []ServiceMeshInventoryItem{}, "resource list payload was not parseable"
	}
	items := make([]ServiceMeshInventoryItem, 0, len(envelope.Items))
	for _, it := range envelope.Items {
		managedBy, readOnly, reason := meshOwnership(it.Metadata.Labels, it.Metadata.Annotations)
		items = append(items, ServiceMeshInventoryItem{
			Name:        it.Metadata.Name,
			Namespace:   it.Metadata.Namespace,
			Labels:      it.Metadata.Labels,
			Annotations: it.Metadata.Annotations,
			ManagedBy:   managedBy,
			ReadOnly:    readOnly,
			Reason:      reason,
			CreatedAt:   it.Metadata.CreationTimestamp,
		})
	}
	sort.SliceStable(items, func(i, j int) bool {
		left := items[i].Namespace + "/" + items[i].Name
		right := items[j].Namespace + "/" + items[j].Name
		return left < right
	})
	return items, ""
}

func parseServiceMeshPolicyPayload(w http.ResponseWriter, r *http.Request) (map[string]any, error) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("policy payload could not be read")
	}
	raw = []byte(strings.TrimSpace(string(raw)))
	if len(raw) == 0 {
		return nil, fmt.Errorf("policy payload is required")
	}

	var wrapped ServiceMeshPolicyValidationRequest
	if json.Unmarshal(raw, &wrapped) == nil {
		if strings.TrimSpace(wrapped.YAML) != "" {
			return parsePolicyDocument([]byte(wrapped.YAML))
		}
		if len(wrapped.Object) > 0 && string(wrapped.Object) != "null" {
			return parsePolicyDocument(wrapped.Object)
		}
	}
	return parsePolicyDocument(raw)
}

func parsePolicyDocument(raw []byte) (map[string]any, error) {
	var obj map[string]any
	if json.Unmarshal(raw, &obj) == nil && len(obj) > 0 {
		return obj, nil
	}
	jsonBytes, err := yaml.YAMLToJSON(raw)
	if err != nil {
		return nil, fmt.Errorf("policy must be valid JSON or YAML")
	}
	if err := json.Unmarshal(jsonBytes, &obj); err != nil {
		return nil, fmt.Errorf("policy must decode to a Kubernetes object")
	}
	if len(obj) == 0 {
		return nil, fmt.Errorf("policy must decode to a Kubernetes object")
	}
	return obj, nil
}

func validateServiceMeshPolicyObject(obj map[string]any) ServiceMeshPolicyValidationResponse {
	out := ServiceMeshPolicyValidationResponse{
		Warnings: []ServiceMeshPolicyValidationFinding{},
		Errors:   []ServiceMeshPolicyValidationFinding{},
	}
	out.APIVersion = meshPolicyStringField(obj, "apiVersion")
	out.Kind = meshPolicyStringField(obj, "kind")
	metadata := meshPolicyMapField(obj, "metadata")
	out.Name = meshPolicyStringField(metadata, "name")
	out.Namespace = meshPolicyStringField(metadata, "namespace")
	labels := meshPolicyStringMapField(metadata, "labels")
	annotations := meshPolicyStringMapField(metadata, "annotations")
	out.ManagedBy, out.ReadOnly, _ = meshOwnership(labels, annotations)

	if out.APIVersion == "" {
		out.Errors = append(out.Errors, validationError("apiVersion", "apiVersion is required"))
	}
	if out.Kind == "" {
		out.Errors = append(out.Errors, validationError("kind", "kind is required"))
	}
	if out.Name == "" {
		out.Errors = append(out.Errors, validationError("metadata.name", "metadata.name is required"))
	}
	if out.Kind != "" {
		if _, ok := serviceMeshResourceDefFor(out.Kind); !ok {
			out.Errors = append(out.Errors, validationError("kind", "unsupported service mesh policy kind "+out.Kind))
		}
	}
	if out.APIVersion != "" && !isSupportedServiceMeshAPIVersion(out.APIVersion) {
		out.Errors = append(out.Errors, validationError("apiVersion", "apiVersion must be an Istio networking or security API"))
	} else if out.APIVersion != "" && !strings.HasSuffix(out.APIVersion, "/v1beta1") {
		out.Warnings = append(out.Warnings, validationWarning("apiVersion", "non-v1beta1 Istio API version; confirm the target cluster still serves it"))
	}
	if out.ReadOnly {
		out.Warnings = append(out.Warnings, validationWarning("metadata", "resource appears to be ArgoCD-owned; edit through GitOps or explicitly migrate ownership before direct apply"))
	}

	spec := meshPolicyMapField(obj, "spec")
	switch out.Kind {
	case "VirtualService":
		validateVirtualService(spec, &out)
	case "DestinationRule":
		if meshPolicyStringField(spec, "host") == "" {
			out.Errors = append(out.Errors, validationError("spec.host", "DestinationRule spec.host is required"))
		}
	case "Gateway":
		if len(meshPolicySliceField(spec, "servers")) == 0 {
			out.Errors = append(out.Errors, validationError("spec.servers", "Gateway spec.servers must include at least one server"))
		}
	case "PeerAuthentication":
		mode := strings.ToUpper(meshPolicyStringField(meshPolicyMapField(spec, "mtls"), "mode"))
		if mode == "DISABLE" {
			out.Warnings = append(out.Warnings, validationWarning("spec.mtls.mode", "DISABLE turns off mTLS for the target scope"))
		}
	}
	return out
}

func validateVirtualService(spec map[string]any, out *ServiceMeshPolicyValidationResponse) {
	if len(meshPolicySliceField(spec, "hosts")) == 0 {
		out.Errors = append(out.Errors, validationError("spec.hosts", "VirtualService spec.hosts must include at least one host"))
	}
	httpRoutes := meshPolicySliceField(spec, "http")
	for i, httpRoute := range httpRoutes {
		routeMap, ok := httpRoute.(map[string]any)
		if !ok {
			continue
		}
		routes := meshPolicySliceField(routeMap, "route")
		if len(routes) == 0 {
			continue
		}
		sum := 0
		weighted := false
		missing := 0
		for _, route := range routes {
			routeObj, ok := route.(map[string]any)
			if !ok {
				continue
			}
			weight, ok := meshPolicyIntField(routeObj, "weight")
			if !ok {
				missing++
				continue
			}
			weighted = true
			sum += weight
		}
		field := fmt.Sprintf("spec.http[%d].route", i)
		if weighted && missing > 0 {
			out.Warnings = append(out.Warnings, validationWarning(field, "some route destinations omit weight; set all weights explicitly for safe canary behavior"))
		}
		if weighted && sum != 100 {
			out.Errors = append(out.Errors, validationError(field, fmt.Sprintf("route weights total %d; expected 100", sum)))
		}
		if !weighted && len(routes) > 1 {
			out.Warnings = append(out.Warnings, validationWarning(field, "multiple destinations without weights rely on Istio defaults; canary splits should total 100"))
		}
	}
}

func (h *ServiceMeshHandler) checkMeshCRDAvailable(ctx context.Context, clusterID uuid.UUID, def serviceMeshResourceDef) string {
	resp, err := h.requester.Do(ctx, clusterID.String(), http.MethodGet, def.Path+"?limit=1", nil, requestHeaders(""))
	if err != nil || resp == nil {
		return "could not verify target CRD through the agent tunnel"
	}
	if resp.StatusCode == http.StatusNotFound {
		return def.Kind + " CRD is not installed on this cluster"
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Sprintf("target CRD check returned Kubernetes status %d", resp.StatusCode)
	}
	return ""
}

func meshOwnership(labels, annotations map[string]string) (string, bool, string) {
	if annotations != nil {
		if v := strings.TrimSpace(annotations["argocd.argoproj.io/instance"]); v != "" {
			return "argocd", true, "ArgoCD application " + v
		}
		if v := strings.TrimSpace(annotations["argocd.argoproj.io/tracking-id"]); v != "" {
			return "argocd", true, "ArgoCD tracking id present"
		}
	}
	if labels != nil {
		if v := strings.TrimSpace(labels["app.kubernetes.io/managed-by"]); v != "" {
			if strings.EqualFold(v, "argocd") || strings.EqualFold(v, "argo-cd") {
				return "argocd", true, "app.kubernetes.io/managed-by=" + v
			}
			return v, false, ""
		}
	}
	return "", false, ""
}

func serviceMeshResourceDefFor(kind string) (serviceMeshResourceDef, bool) {
	for _, def := range istioInventoryResources {
		if def.Kind == kind {
			return def, true
		}
	}
	return serviceMeshResourceDef{}, false
}

func isSupportedServiceMeshAPIVersion(apiVersion string) bool {
	return strings.HasPrefix(apiVersion, "networking.istio.io/") ||
		strings.HasPrefix(apiVersion, "security.istio.io/")
}

func validationError(field, message string) ServiceMeshPolicyValidationFinding {
	return ServiceMeshPolicyValidationFinding{Field: field, Severity: "error", Message: message}
}

func validationWarning(field, message string) ServiceMeshPolicyValidationFinding {
	return ServiceMeshPolicyValidationFinding{Field: field, Severity: "warning", Message: message}
}

func meshPolicyStringField(obj map[string]any, key string) string {
	if obj == nil {
		return ""
	}
	v, ok := obj[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	default:
		return strings.TrimSpace(fmt.Sprint(t))
	}
}

func meshPolicyMapField(obj map[string]any, key string) map[string]any {
	if obj == nil {
		return map[string]any{}
	}
	v, ok := obj[key].(map[string]any)
	if !ok || v == nil {
		return map[string]any{}
	}
	return v
}

func meshPolicySliceField(obj map[string]any, key string) []any {
	if obj == nil {
		return nil
	}
	v, ok := obj[key].([]any)
	if !ok {
		return nil
	}
	return v
}

func meshPolicyStringMapField(obj map[string]any, key string) map[string]string {
	v, ok := obj[key].(map[string]any)
	if !ok || len(v) == 0 {
		return nil
	}
	out := make(map[string]string, len(v))
	for k, raw := range v {
		if s, ok := raw.(string); ok {
			out[k] = s
		}
	}
	return out
}

func meshPolicyIntField(obj map[string]any, key string) (int, bool) {
	v, ok := obj[key]
	if !ok || v == nil {
		return 0, false
	}
	switch t := v.(type) {
	case float64:
		return int(t), true
	case int:
		return t, true
	case json.Number:
		i, err := strconv.Atoi(t.String())
		return i, err == nil
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(t))
		return i, err == nil
	default:
		return 0, false
	}
}
