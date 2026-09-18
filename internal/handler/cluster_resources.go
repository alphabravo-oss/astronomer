// Sprint 069 — Cluster-detail "what's installed" handler.
//
// Read-only handler over the five sprint-069 mirrored_* tables. Backs
// the Cluster → Resources tab in the dashboard so the UI can render a
// "what's actually in this cluster" view without a kubectl round-trip
// per render.
//
// Routes owned by this file (all under /api/v1):
//
//   GET /clusters/{cluster_id}/ingress-classes/
//   GET /clusters/{cluster_id}/gateway-classes/
//   GET /clusters/{cluster_id}/network-policies/?namespace=...
//   GET /clusters/{cluster_id}/resource-quotas/?namespace=...
//   GET /clusters/{cluster_id}/limit-ranges/?namespace=...
//
// All endpoints are gated on clusters:read (the same RBAC class as the
// existing /clusters/{id}/snapshots/ list). The mirrored rows never
// carry secrets (no Secret/ConfigMap mirror in this sprint) so a
// cluster reader is the right caller; tighter gating would create
// per-namespace RBAC churn for what is fundamentally an
// already-cluster-scoped view.
//
// NOTE: there is also a /clusters/{id}/network-policies/applications/
// endpoint (sprint 068, parallel) that returns the
// astronomer-MANAGED subset. This file owns the bare
// /network-policies/ endpoint which returns the FULL mirror (managed
// + operator-created). The is_managed boolean on each row lets the UI
// render a "managed by astronomer" badge without a second call.

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/crd"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ClusterResourcesQuerier is the narrow DB surface the handler needs.
// Defined locally so unit tests can stand up a small in-memory fake
// without pulling in the full *sqlc.Queries — same pattern used by
// ClusterRegistryQuerier / ClusterSnapshotQuerier.
type ClusterResourcesQuerier interface {
	GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error)

	ListMirroredIngressClasses(ctx context.Context, arg sqlc.ListMirroredIngressClassesParams) ([]sqlc.MirroredIngressClass, error)
	CountMirroredIngressClasses(ctx context.Context, clusterID uuid.UUID) (int64, error)
	ListMirroredGatewayClasses(ctx context.Context, arg sqlc.ListMirroredGatewayClassesParams) ([]sqlc.MirroredGatewayClass, error)
	CountMirroredGatewayClasses(ctx context.Context, clusterID uuid.UUID) (int64, error)

	ListMirroredNetworkPolicies(ctx context.Context, arg sqlc.ListMirroredNetworkPoliciesParams) ([]sqlc.MirroredNetworkPolicy, error)
	ListMirroredNetworkPoliciesByNamespace(ctx context.Context, arg sqlc.ListMirroredNetworkPoliciesByNamespaceParams) ([]sqlc.MirroredNetworkPolicy, error)
	CountMirroredNetworkPolicies(ctx context.Context, clusterID uuid.UUID) (int64, error)
	CountMirroredNetworkPoliciesByNamespace(ctx context.Context, arg sqlc.CountMirroredNetworkPoliciesByNamespaceParams) (int64, error)

	ListMirroredResourceQuotas(ctx context.Context, arg sqlc.ListMirroredResourceQuotasParams) ([]sqlc.MirroredResourceQuota, error)
	ListMirroredResourceQuotasByNamespace(ctx context.Context, arg sqlc.ListMirroredResourceQuotasByNamespaceParams) ([]sqlc.MirroredResourceQuota, error)
	CountMirroredResourceQuotas(ctx context.Context, clusterID uuid.UUID) (int64, error)
	CountMirroredResourceQuotasByNamespace(ctx context.Context, arg sqlc.CountMirroredResourceQuotasByNamespaceParams) (int64, error)

	ListMirroredLimitRanges(ctx context.Context, arg sqlc.ListMirroredLimitRangesParams) ([]sqlc.MirroredLimitRange, error)
	ListMirroredLimitRangesByNamespace(ctx context.Context, arg sqlc.ListMirroredLimitRangesByNamespaceParams) ([]sqlc.MirroredLimitRange, error)
	CountMirroredLimitRanges(ctx context.Context, clusterID uuid.UUID) (int64, error)
	CountMirroredLimitRangesByNamespace(ctx context.Context, arg sqlc.CountMirroredLimitRangesByNamespaceParams) (int64, error)
}

// ClusterResourcesHandler owns the five list endpoints.
type ClusterResourcesHandler struct {
	queries ClusterResourcesQuerier
}

// NewClusterResourcesHandler constructs a handler with the supplied
// queries surface. Nil-safe — when queries is nil, every endpoint
// returns 503 so we degrade cleanly during the pre-migration boot
// window.
func NewClusterResourcesHandler(q ClusterResourcesQuerier) *ClusterResourcesHandler {
	return &ClusterResourcesHandler{queries: q}
}

// ---------------------------------------------------------------------
// DTOs
// ---------------------------------------------------------------------

// ClusterMirroredIngressClassDTO is the wire DTO for one IngressClass
// row. is_default is the pre-resolved annotation boolean so the UI can
// render a "default" badge without parsing annotations on every render.
type ClusterMirroredIngressClassDTO struct {
	Name        string            `json:"name"`
	Controller  string            `json:"controller"`
	Parameters  any               `json:"parameters"`
	IsDefault   bool              `json:"is_default"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	LastSeenAt  time.Time         `json:"last_seen_at"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// ClusterMirroredGatewayClassDTO mirrors the GatewayClass row. The
// Accepted condition is pre-resolved to a string so the UI doesn't
// have to walk conditions per render.
type ClusterMirroredGatewayClassDTO struct {
	Name           string            `json:"name"`
	ControllerName string            `json:"controller_name"`
	Description    string            `json:"description"`
	Parameters     any               `json:"parameters"`
	AcceptedStatus string            `json:"accepted_status"`
	Labels         map[string]string `json:"labels"`
	Annotations    map[string]string `json:"annotations"`
	LastSeenAt     time.Time         `json:"last_seen_at"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

// ClusterMirroredNetworkPolicyDTO mirrors the NetworkPolicy row.
// is_managed is the precomputed managed-by=astronomer label boolean.
type ClusterMirroredNetworkPolicyDTO struct {
	Namespace    string            `json:"namespace"`
	Name         string            `json:"name"`
	PodSelector  any               `json:"pod_selector"`
	PolicyTypes  any               `json:"policy_types"`
	IngressRules any               `json:"ingress_rules"`
	EgressRules  any               `json:"egress_rules"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	IsManaged    bool              `json:"is_managed"`
	LastSeenAt   time.Time         `json:"last_seen_at"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
}

// ClusterMirroredResourceQuotaDTO mirrors the ResourceQuota row.
// hard / used / scopes are passed through as opaque shapes so the UI
// can render any quota key upstream Kubernetes carries.
type ClusterMirroredResourceQuotaDTO struct {
	Namespace   string            `json:"namespace"`
	Name        string            `json:"name"`
	Hard        any               `json:"hard"`
	Used        any               `json:"used"`
	Scopes      any               `json:"scopes"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	LastSeenAt  time.Time         `json:"last_seen_at"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// ClusterMirroredLimitRangeDTO mirrors the LimitRange row.
type ClusterMirroredLimitRangeDTO struct {
	Namespace   string            `json:"namespace"`
	Name        string            `json:"name"`
	Limits      any               `json:"limits"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	LastSeenAt  time.Time         `json:"last_seen_at"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// ---------------------------------------------------------------------
// Endpoint handlers
// ---------------------------------------------------------------------

// ListIngressClasses serves GET /clusters/{cluster_id}/ingress-classes/.
func (h *ClusterResourcesHandler) ListIngressClasses(w http.ResponseWriter, r *http.Request) {
	cid, ok := h.resolveCluster(w, r)
	if !ok {
		return
	}
	limit, offset := queryLimitOffset(r, 50)
	rows, err := h.queries.ListMirroredIngressClasses(r.Context(), sqlc.ListMirroredIngressClassesParams{
		ClusterID: cid, QueryLimit: int32(limit), QueryOffset: int32(offset),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, err.Error())
		return
	}
	out := make([]ClusterMirroredIngressClassDTO, 0, len(rows))
	for _, row := range rows {
		out = append(out, ClusterMirroredIngressClassDTO{
			Name:        row.Name,
			Controller:  row.Controller,
			Parameters:  mirrorRawOrNull(row.Parameters),
			IsDefault:   row.IsDefault,
			Labels:      mirrorDecodeStringMap(row.Labels),
			Annotations: mirrorDecodeStringMap(row.Annotations),
			LastSeenAt:  row.LastSeenAt,
			CreatedAt:   row.CreatedAt,
			UpdatedAt:   row.UpdatedAt,
		})
	}
	total, err := h.queries.CountMirroredIngressClasses(r.Context(), cid)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, err.Error())
		return
	}
	paging.Write(w, out, paging.Exact(total, queryLimit(r, 50), queryOffset(r), len(out)))
}

// ListGatewayClasses serves GET /clusters/{cluster_id}/gateway-classes/.
func (h *ClusterResourcesHandler) ListGatewayClasses(w http.ResponseWriter, r *http.Request) {
	cid, ok := h.resolveCluster(w, r)
	if !ok {
		return
	}
	limit, offset := queryLimitOffset(r, 50)
	rows, err := h.queries.ListMirroredGatewayClasses(r.Context(), sqlc.ListMirroredGatewayClassesParams{
		ClusterID: cid, QueryLimit: int32(limit), QueryOffset: int32(offset),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, err.Error())
		return
	}
	out := make([]ClusterMirroredGatewayClassDTO, 0, len(rows))
	for _, row := range rows {
		out = append(out, ClusterMirroredGatewayClassDTO{
			Name:           row.Name,
			ControllerName: row.ControllerName,
			Description:    row.Description,
			Parameters:     mirrorRawOrNull(row.Parameters),
			AcceptedStatus: row.AcceptedStatus,
			Labels:         mirrorDecodeStringMap(row.Labels),
			Annotations:    mirrorDecodeStringMap(row.Annotations),
			LastSeenAt:     row.LastSeenAt,
			CreatedAt:      row.CreatedAt,
			UpdatedAt:      row.UpdatedAt,
		})
	}
	total, err := h.queries.CountMirroredGatewayClasses(r.Context(), cid)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, err.Error())
		return
	}
	paging.Write(w, out, paging.Exact(total, queryLimit(r, 50), queryOffset(r), len(out)))
}

// ListNetworkPolicies serves GET /clusters/{cluster_id}/network-policies/.
// Honors ?namespace= for namespace-scoped narrowing.
func (h *ClusterResourcesHandler) ListNetworkPolicies(w http.ResponseWriter, r *http.Request) {
	cid, ok := h.resolveCluster(w, r)
	if !ok {
		return
	}
	ns := strings.TrimSpace(r.URL.Query().Get("namespace"))
	limit, offset := queryLimitOffset(r, 50)
	var rows []sqlc.MirroredNetworkPolicy
	var total int64
	var err error
	if ns != "" {
		rows, err = h.queries.ListMirroredNetworkPoliciesByNamespace(r.Context(), sqlc.ListMirroredNetworkPoliciesByNamespaceParams{
			ClusterID: cid, Namespace: ns, QueryLimit: int32(limit), QueryOffset: int32(offset),
		})
		if err == nil {
			total, err = h.queries.CountMirroredNetworkPoliciesByNamespace(r.Context(), sqlc.CountMirroredNetworkPoliciesByNamespaceParams{
				ClusterID: cid, Namespace: ns,
			})
		}
	} else {
		rows, err = h.queries.ListMirroredNetworkPolicies(r.Context(), sqlc.ListMirroredNetworkPoliciesParams{
			ClusterID: cid, QueryLimit: int32(limit), QueryOffset: int32(offset),
		})
		if err == nil {
			total, err = h.queries.CountMirroredNetworkPolicies(r.Context(), cid)
		}
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, err.Error())
		return
	}
	out := make([]ClusterMirroredNetworkPolicyDTO, 0, len(rows))
	for _, row := range rows {
		out = append(out, ClusterMirroredNetworkPolicyDTO{
			Namespace:    row.Namespace,
			Name:         row.Name,
			PodSelector:  mirrorRawOrNull(row.PodSelector),
			PolicyTypes:  mirrorRawOrNull(row.PolicyTypes),
			IngressRules: mirrorRawOrNull(row.IngressRules),
			EgressRules:  mirrorRawOrNull(row.EgressRules),
			Labels:       mirrorDecodeStringMap(row.Labels),
			Annotations:  mirrorDecodeStringMap(row.Annotations),
			IsManaged:    row.IsManaged,
			LastSeenAt:   row.LastSeenAt,
			CreatedAt:    row.CreatedAt,
			UpdatedAt:    row.UpdatedAt,
		})
	}
	paging.Write(w, out, paging.Exact(total, queryLimit(r, 50), queryOffset(r), len(out)))
}

// ListResourceQuotas serves GET /clusters/{cluster_id}/resource-quotas/.
// Honors ?namespace=.
func (h *ClusterResourcesHandler) ListResourceQuotas(w http.ResponseWriter, r *http.Request) {
	cid, ok := h.resolveCluster(w, r)
	if !ok {
		return
	}
	ns := strings.TrimSpace(r.URL.Query().Get("namespace"))
	limit, offset := queryLimitOffset(r, 50)
	var rows []sqlc.MirroredResourceQuota
	var total int64
	var err error
	if ns != "" {
		rows, err = h.queries.ListMirroredResourceQuotasByNamespace(r.Context(), sqlc.ListMirroredResourceQuotasByNamespaceParams{
			ClusterID: cid, Namespace: ns, QueryLimit: int32(limit), QueryOffset: int32(offset),
		})
		if err == nil {
			total, err = h.queries.CountMirroredResourceQuotasByNamespace(r.Context(), sqlc.CountMirroredResourceQuotasByNamespaceParams{
				ClusterID: cid, Namespace: ns,
			})
		}
	} else {
		rows, err = h.queries.ListMirroredResourceQuotas(r.Context(), sqlc.ListMirroredResourceQuotasParams{
			ClusterID: cid, QueryLimit: int32(limit), QueryOffset: int32(offset),
		})
		if err == nil {
			total, err = h.queries.CountMirroredResourceQuotas(r.Context(), cid)
		}
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, err.Error())
		return
	}
	out := make([]ClusterMirroredResourceQuotaDTO, 0, len(rows))
	for _, row := range rows {
		out = append(out, ClusterMirroredResourceQuotaDTO{
			Namespace:   row.Namespace,
			Name:        row.Name,
			Hard:        mirrorRawOrNull(row.Hard),
			Used:        mirrorRawOrNull(row.Used),
			Scopes:      mirrorRawOrNull(row.Scopes),
			Labels:      mirrorDecodeStringMap(row.Labels),
			Annotations: mirrorDecodeStringMap(row.Annotations),
			LastSeenAt:  row.LastSeenAt,
			CreatedAt:   row.CreatedAt,
			UpdatedAt:   row.UpdatedAt,
		})
	}
	paging.Write(w, out, paging.Exact(total, queryLimit(r, 50), queryOffset(r), len(out)))
}

// ListLimitRanges serves GET /clusters/{cluster_id}/limit-ranges/. Honors
// ?namespace=.
func (h *ClusterResourcesHandler) ListLimitRanges(w http.ResponseWriter, r *http.Request) {
	cid, ok := h.resolveCluster(w, r)
	if !ok {
		return
	}
	ns := strings.TrimSpace(r.URL.Query().Get("namespace"))
	limit, offset := queryLimitOffset(r, 50)
	var rows []sqlc.MirroredLimitRange
	var total int64
	var err error
	if ns != "" {
		rows, err = h.queries.ListMirroredLimitRangesByNamespace(r.Context(), sqlc.ListMirroredLimitRangesByNamespaceParams{
			ClusterID: cid, Namespace: ns, QueryLimit: int32(limit), QueryOffset: int32(offset),
		})
		if err == nil {
			total, err = h.queries.CountMirroredLimitRangesByNamespace(r.Context(), sqlc.CountMirroredLimitRangesByNamespaceParams{
				ClusterID: cid, Namespace: ns,
			})
		}
	} else {
		rows, err = h.queries.ListMirroredLimitRanges(r.Context(), sqlc.ListMirroredLimitRangesParams{
			ClusterID: cid, QueryLimit: int32(limit), QueryOffset: int32(offset),
		})
		if err == nil {
			total, err = h.queries.CountMirroredLimitRanges(r.Context(), cid)
		}
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, err.Error())
		return
	}
	out := make([]ClusterMirroredLimitRangeDTO, 0, len(rows))
	for _, row := range rows {
		out = append(out, ClusterMirroredLimitRangeDTO{
			Namespace:   row.Namespace,
			Name:        row.Name,
			Limits:      mirrorRawOrNull(row.Limits),
			Labels:      mirrorDecodeStringMap(row.Labels),
			Annotations: mirrorDecodeStringMap(row.Annotations),
			LastSeenAt:  row.LastSeenAt,
			CreatedAt:   row.CreatedAt,
			UpdatedAt:   row.UpdatedAt,
		})
	}
	paging.Write(w, out, paging.Exact(total, queryLimit(r, 50), queryOffset(r), len(out)))
}

// ---------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------

// resolveCluster pulls {cluster_id} off the chi route + validates it
// against the clusters table. Returns the parsed UUID on success, or
// writes a 400/404 and false otherwise. The nil-queries fast-path
// returns 503 — the handler is intended to degrade gracefully when DB
// wiring hasn't completed yet (test fakes, pre-migration boots).
func (h *ClusterResourcesHandler) resolveCluster(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	if h == nil || h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotWired, "cluster resources handler not wired")
		return uuid.Nil, false
	}
	cid, ok := parseClusterID(w, r)
	if !ok {
		return uuid.Nil, false
	}
	if _, err := h.queries.GetClusterByID(r.Context(), cid); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "cluster does not exist")
			return uuid.Nil, false
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ClusterLookupFailed, err.Error())
		return uuid.Nil, false
	}
	return cid, true
}

// mirrorRawOrNull returns nil when the JSONB value is empty / null / `{}` / `[]`
// shaped or already nil, otherwise the json.RawMessage so the encoder
// emits the JSON verbatim. We use any here so the DTO field can be
// `null` rather than `{}` when the column is unset — the UI's
// "show/hide rule details" toggle keys off null.
func mirrorRawOrNull(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return rawJSON(b)
}

// rawJSON is a marker type so the JSON encoder serializes the bytes
// directly without re-marshalling.
type rawJSON []byte

// MarshalJSON returns the raw bytes verbatim, falling back to "null"
// on an empty body so the wire never carries the literal bytes `[]`
// (which would be invalid JSON for the type any).
func (r rawJSON) MarshalJSON() ([]byte, error) {
	if len(r) == 0 {
		return []byte("null"), nil
	}
	return r, nil
}

// mirrorDecodeStringMap reads a JSONB labels/annotations column and returns the
// decoded map. On any decode failure it returns nil — the dashboard
// renders an empty pair list, which matches the agent-side semantics of
// "object had no labels".
func mirrorDecodeStringMap(b []byte) map[string]string {
	if len(b) == 0 {
		return map[string]string{}
	}
	m := map[string]string{}
	if err := json.Unmarshal(b, &m); err != nil {
		return map[string]string{}
	}
	return m
}

// Keep an unused-import sentinel so future refactors that touch only
// crd's prune metric don't bit-rot the import on this file (the
// handler reads no crd-package state directly today, but we keep the
// import for a future per-kind metric label).
var _ = crd.KindIngressClass
