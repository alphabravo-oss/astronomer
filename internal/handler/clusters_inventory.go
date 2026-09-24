package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	agenttemplate "github.com/alphabravocompany/astronomer-go/deploy/agent"
	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/alphabravocompany/astronomer-go/internal/quota"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/registration"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// clusterScopeQuerier is the OPTIONAL capability a ClusterQuerier may provide
// to serve a scope-filtered list page. Kept off ClusterQuerier on purpose: only
// the list path needs it, so the dozen existing test fakes that implement the
// full interface stay untouched. A scope-restricted caller whose querier does
// NOT implement it gets a 500, never an unfiltered fleet.
type clusterScopeQuerier interface {
	ListClustersForScopes(ctx context.Context, arg sqlc.ListClustersForScopesParams) ([]sqlc.Cluster, error)
	CountClustersForScopes(ctx context.Context, clusterIds []uuid.UUID) (int64, error)
}

type clusterFilteredQuerier interface {
	ListClustersFiltered(ctx context.Context, arg sqlc.ListClustersFilteredParams) ([]sqlc.Cluster, error)
	CountClustersFiltered(ctx context.Context, arg sqlc.CountClustersFilteredParams) (int64, error)
	ListClustersFilteredForScopes(ctx context.Context, arg sqlc.ListClustersFilteredForScopesParams) ([]sqlc.Cluster, error)
	CountClustersFilteredForScopes(ctx context.Context, arg sqlc.CountClustersFilteredForScopesParams) (int64, error)
}

type clusterCursorQuerier interface {
	ListClustersAfter(ctx context.Context, arg sqlc.ListClustersAfterParams) ([]sqlc.Cluster, error)
	ListClustersFilteredAfter(ctx context.Context, arg sqlc.ListClustersFilteredAfterParams) ([]sqlc.Cluster, error)
	ListClustersForScopesAfter(ctx context.Context, arg sqlc.ListClustersForScopesAfterParams) ([]sqlc.Cluster, error)
	ListClustersFilteredForScopesAfter(ctx context.Context, arg sqlc.ListClustersFilteredForScopesAfterParams) ([]sqlc.Cluster, error)
}

type clusterLivenessQuerier interface {
	GetClusterLiveness(ctx context.Context, clusterID uuid.UUID) (sqlc.ClusterLiveness, error)
	ListClusterLivenessForClusters(ctx context.Context, clusterIds []uuid.UUID) ([]sqlc.ClusterLiveness, error)
}

// clusterEstateSummaryQuerier is optional so focused handler fakes need only
// implement it when exercising the overview endpoint. Production's generated
// sqlc store always provides both methods. The handler fails closed if that
// capability is absent; it never approximates an estate total from a page.
type clusterEstateSummaryQuerier interface {
	GetClusterEstateSummary(ctx context.Context) (sqlc.GetClusterEstateSummaryRow, error)
	GetClusterEstateSummaryForScopes(ctx context.Context, clusterIds []uuid.UUID) (sqlc.GetClusterEstateSummaryForScopesRow, error)
}

// --- Request / Response types ---

// CreateClusterRequest represents the request body for creating a cluster.
// openapi:request CreateClusterRequest
type CreateClusterRequest struct {
	Name         string          `json:"name" validate:"required,rfc1123"`
	DisplayName  string          `json:"display_name"`
	Description  string          `json:"description"`
	Environment  string          `json:"environment"`
	Region       string          `json:"region"`
	Provider     string          `json:"provider"`
	Distribution string          `json:"distribution"`
	Labels       json.RawMessage `json:"labels"`
	// Annotations carry agent settings at adoption time, notably
	// astronomer.io/image-scanning for the default-on scanner opt-out.
	Annotations json.RawMessage `json:"annotations"`
	// ApiServerUrl and CaCertificate are optional direct-access coordinates.
	// They never contain a credential. The download endpoint validates HTTPS,
	// TLS identity and reachability again before minting a short-lived token.
	ApiServerUrl   string                       `json:"api_server_url"`
	CaCertificate  string                       `json:"ca_certificate"`
	BadgeText      string                       `json:"badge_text"`
	BadgeColor     string                       `json:"badge_color"`
	AgentOverrides agenttemplate.AgentOverrides `json:"agent_overrides"`
}

// UpdateClusterRequest represents the request body for updating a cluster.
// openapi:request UpdateClusterRequest
type UpdateClusterRequest struct {
	DisplayName    *string                       `json:"display_name,omitempty"`
	Description    *string                       `json:"description,omitempty"`
	Environment    *string                       `json:"environment,omitempty"`
	Region         *string                       `json:"region,omitempty"`
	Labels         *json.RawMessage              `json:"labels,omitempty"`
	Annotations    *json.RawMessage              `json:"annotations,omitempty"`
	ApiServerUrl   *string                       `json:"api_server_url,omitempty"`
	CaCertificate  *string                       `json:"ca_certificate,omitempty"`
	BadgeText      *string                       `json:"badge_text,omitempty"`
	BadgeColor     *string                       `json:"badge_color,omitempty"`
	AgentOverrides *agenttemplate.AgentOverrides `json:"agent_overrides,omitempty"`
}

// UnmarshalJSON retains an explicitly supplied JSON null for free-form JSONB
// fields. A pointer alone cannot distinguish null from omission, but the API's
// partial-update contract must preserve omission while retaining the existing
// accepted explicit-null behavior.
func (r *UpdateClusterRequest) UnmarshalJSON(data []byte) error {
	type updateClusterRequestAlias UpdateClusterRequest
	var decoded updateClusterRequestAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if raw, ok := fields["labels"]; ok {
		decoded.Labels = &raw
	}
	if raw, ok := fields["annotations"]; ok {
		decoded.Annotations = &raw
	}
	if raw, ok := fields["agent_overrides"]; ok {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("agent_overrides must be an object; use {} to clear it")
		}
		var overrides agenttemplate.AgentOverrides
		if err := json.Unmarshal(raw, &overrides); err != nil {
			return err
		}
		decoded.AgentOverrides = &overrides
	}
	*r = UpdateClusterRequest(decoded)
	return nil
}

// --- Endpoints ---

// ClusterEstateSummaryResponse is the authorization-scoped source of truth for
// the platform overview. Counts and capacity describe the entire visible
// estate, never merely the current inventory page.
type ClusterEstateSummaryResponse struct {
	ClustersTotal        int64     `json:"clusters_total"`
	ClustersActive       int64     `json:"clusters_active"`
	ClustersWarning      int64     `json:"clusters_warning"`
	ClustersDisconnected int64     `json:"clusters_disconnected"`
	NodesTotal           int64     `json:"nodes_total"`
	PodsTotal            int64     `json:"pods_total"`
	AsOf                 time.Time `json:"as_of"`
}

// Summary handles GET /api/v1/clusters/summary/.
func (h *ClusterHandler) Summary(w http.ResponseWriter, r *http.Request) {
	all, clusterIDs, _, err := h.authz.authorizedScopeIDs(r.Context(), rbac.ResourceClusters, rbac.VerbList, rbac.NarrowedClustersWiden)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Failed to retrieve user permissions")
		return
	}

	store, ok := h.queries.(clusterEstateSummaryQuerier)
	if !ok {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.StoreUnavailable, "Cluster summary store is not available")
		return
	}

	response := ClusterEstateSummaryResponse{AsOf: time.Now().UTC()}
	if all {
		row, queryErr := store.GetClusterEstateSummary(r.Context())
		if queryErr != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to summarize clusters")
			return
		}
		response.ClustersTotal = row.ClustersTotal
		response.ClustersActive = row.ClustersActive
		response.ClustersWarning = row.ClustersWarning
		response.ClustersDisconnected = row.ClustersDisconnected
		response.NodesTotal = row.NodesTotal
		response.PodsTotal = row.PodsTotal
	} else {
		row, queryErr := store.GetClusterEstateSummaryForScopes(r.Context(), clusterIDs)
		if queryErr != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to summarize clusters")
			return
		}
		response.ClustersTotal = row.ClustersTotal
		response.ClustersActive = row.ClustersActive
		response.ClustersWarning = row.ClustersWarning
		response.ClustersDisconnected = row.ClustersDisconnected
		response.NodesTotal = row.NodesTotal
		response.PodsTotal = row.PodsTotal
	}

	RespondJSON(w, http.StatusOK, response)
}

// List handles GET /api/v1/clusters/.
func (h *ClusterHandler) List(w http.ResponseWriter, r *http.Request) {
	// Clamp limit/offset via the shared helper: limit → [1,200] (default 20),
	// offset → >=0. Previously an int32(queryInt(...)) with no upper bound, so
	// e.g. limit=3e9 overflowed the int32 param to a negative value.
	limitInt, offsetInt := queryLimitOffset(r, 20)
	limit := int32(limitInt)
	offset := int32(offsetInt)
	filterStatus := strings.TrimSpace(r.URL.Query().Get("status"))
	filterProvider := strings.TrimSpace(r.URL.Query().Get("provider"))
	filterEnvironment := strings.TrimSpace(r.URL.Query().Get("environment"))
	filterSearch := strings.TrimSpace(r.URL.Query().Get("search"))
	for name, value := range map[string]string{
		"status": filterStatus, "provider": filterProvider,
		"environment": filterEnvironment, "search": filterSearch,
	} {
		if len(value) > 128 {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, name+" must be at most 128 bytes")
			return
		}
	}
	hasFilters := filterStatus != "" || filterProvider != "" || filterEnvironment != "" || filterSearch != ""

	// Scope filter. The collection gate (RequireCollectionPermission) admits a
	// caller whose only grant is a cluster_role_bindings row, so the page must
	// be narrowed to the clusters they may see. A platform-wide grant (and a
	// superuser) returns all==true and takes the original unfiltered path
	// below, byte-identical to before.
	all, clusterIDs, _, err := h.authz.authorizedScopeIDs(r.Context(), rbac.ResourceClusters, rbac.VerbList, rbac.NarrowedClustersWiden)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Failed to retrieve user permissions")
		return
	}

	cursorValue, cursorProvided := r.URL.Query()["cursor"]
	_, offsetProvided := r.URL.Query()["offset"]
	if cursorProvided && offsetProvided {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "cursor and offset cannot be combined")
		return
	}
	cursorMode := !offsetProvided
	cursorBinding := paging.Binding(
		"clusters:v1", "created_at:desc,id:desc", filterStatus, filterProvider,
		filterEnvironment, filterSearch, fmt.Sprintf("all=%t", all), paging.SortedUUIDs(clusterIDs),
	)
	var cursor paging.Cursor
	if cursorProvided {
		if len(cursorValue) != 1 {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "cursor must be one opaque value")
			return
		}
		cursor, err = paging.DecodeCursor(cursorValue[0], cursorBinding)
		if err != nil {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Invalid pagination cursor")
			return
		}
	}
	cursorQueries, cursorCapable := h.queries.(clusterCursorQuerier)
	if cursorMode && !cursorCapable {
		if cursorProvided {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.StoreUnavailable, "Cursor cluster listing is not available")
			return
		}
		// Compatibility for injected stores that predate cursor support. The
		// production sqlc store always implements clusterCursorQuerier.
		cursorMode = false
	}
	cursorLimit := limit + 1
	hasCursor := cursorProvided

	var clusters []sqlc.Cluster
	var total int64
	if all && hasFilters {
		filtered, ok := h.queries.(clusterFilteredQuerier)
		if !ok {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.StoreUnavailable, "Filtered cluster listing is not available")
			return
		}
		if cursorMode {
			clusters, err = cursorQueries.ListClustersFilteredAfter(r.Context(), sqlc.ListClustersFilteredAfterParams{
				FilterStatus: filterStatus, FilterProvider: filterProvider,
				FilterEnvironment: filterEnvironment, FilterSearch: filterSearch,
				HasCursor: hasCursor, AfterCreatedAt: cursor.Time, AfterID: cursor.ID, QueryLimit: cursorLimit,
			})
		} else {
			clusters, err = filtered.ListClustersFiltered(r.Context(), sqlc.ListClustersFilteredParams{
				FilterStatus: filterStatus, FilterProvider: filterProvider,
				FilterEnvironment: filterEnvironment, FilterSearch: filterSearch,
				QueryLimit: limit, QueryOffset: offset,
			})
		}
		if err == nil {
			total, err = filtered.CountClustersFiltered(r.Context(), sqlc.CountClustersFilteredParams{
				FilterStatus: filterStatus, FilterProvider: filterProvider,
				FilterEnvironment: filterEnvironment, FilterSearch: filterSearch,
			})
		}
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list filtered clusters")
			return
		}
	} else if all {
		if cursorMode {
			clusters, err = cursorQueries.ListClustersAfter(r.Context(), sqlc.ListClustersAfterParams{
				HasCursor: hasCursor, AfterCreatedAt: cursor.Time, AfterID: cursor.ID, QueryLimit: cursorLimit,
			})
		} else {
			clusters, err = h.queries.ListClusters(r.Context(), sqlc.ListClustersParams{Limit: limit, Offset: offset})
		}
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list clusters")
			return
		}
		total, err = h.queries.CountClusters(r.Context())
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count clusters")
			return
		}
	} else if hasFilters {
		filtered, ok := h.queries.(clusterFilteredQuerier)
		if !ok {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.StoreUnavailable, "Scoped filtered cluster listing is not available")
			return
		}
		if cursorMode {
			clusters, err = cursorQueries.ListClustersFilteredForScopesAfter(r.Context(), sqlc.ListClustersFilteredForScopesAfterParams{
				ClusterIds: clusterIDs, FilterStatus: filterStatus, FilterProvider: filterProvider,
				FilterEnvironment: filterEnvironment, FilterSearch: filterSearch,
				HasCursor: hasCursor, AfterCreatedAt: cursor.Time, AfterID: cursor.ID, QueryLimit: cursorLimit,
			})
		} else {
			clusters, err = filtered.ListClustersFilteredForScopes(r.Context(), sqlc.ListClustersFilteredForScopesParams{
				ClusterIds: clusterIDs, FilterStatus: filterStatus, FilterProvider: filterProvider,
				FilterEnvironment: filterEnvironment, FilterSearch: filterSearch,
				QueryLimit: limit, QueryOffset: offset,
			})
		}
		if err == nil {
			total, err = filtered.CountClustersFilteredForScopes(r.Context(), sqlc.CountClustersFilteredForScopesParams{
				ClusterIds: clusterIDs, FilterStatus: filterStatus, FilterProvider: filterProvider,
				FilterEnvironment: filterEnvironment, FilterSearch: filterSearch,
			})
		}
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list scoped filtered clusters")
			return
		}
	} else {
		scoped, ok := h.queries.(clusterScopeQuerier)
		if !ok {
			// Authorization is wired but the query surface cannot filter. Fail
			// closed rather than serve the whole fleet to a scoped caller.
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Scoped cluster listing is not available")
			return
		}
		if cursorMode {
			clusters, err = cursorQueries.ListClustersForScopesAfter(r.Context(), sqlc.ListClustersForScopesAfterParams{
				ClusterIds: clusterIDs, HasCursor: hasCursor,
				AfterCreatedAt: cursor.Time, AfterID: cursor.ID, QueryLimit: cursorLimit,
			})
		} else {
			clusters, err = scoped.ListClustersForScopes(r.Context(), sqlc.ListClustersForScopesParams{
				ClusterIds: clusterIDs, QueryLimit: limit, QueryOffset: offset,
			})
		}
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list clusters")
			return
		}
		// Count over the SAME predicate: an unfiltered total under a filtered
		// page would both leak the fleet size and break the pager.
		total, err = scoped.CountClustersForScopes(r.Context(), clusterIDs)
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count clusters")
			return
		}
	}

	nextCursor := ""
	seen := offsetInt
	if cursorMode {
		seen = cursor.Seen
		if len(clusters) > limitInt {
			clusters = clusters[:limitInt]
			last := clusters[len(clusters)-1]
			nextCursor, err = paging.EncodeCursor(paging.NewCursor(last.CreatedAt, last.ID, cursorBinding, seen+len(clusters)))
			if err != nil {
				RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Failed to encode pagination cursor")
				return
			}
		}
	}

	// One query for all in-flight decommissions, scoped to this page's cluster
	// IDs → mark the matching rows Decommissioning (avoids an N+1 per cluster).
	// Best-effort: on error we just don't flag anything.
	pageIDs := make([]uuid.UUID, len(clusters))
	for i, c := range clusters {
		pageIDs[i] = c.ID
	}
	decommissioning := h.inFlightDecommissionSet(r.Context(), pageIDs)
	livenessStore, ok := h.queries.(clusterLivenessQuerier)
	if !ok {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.StoreUnavailable, "Cluster liveness store is not available")
		return
	}
	livenessRows, err := livenessStore.ListClusterLivenessForClusters(r.Context(), pageIDs)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list cluster liveness")
		return
	}
	livenessByCluster := make(map[uuid.UUID]pgtype.Timestamptz, len(livenessRows))
	for _, liveness := range livenessRows {
		livenessByCluster[liveness.ClusterID] = liveness.LastHeartbeat
	}

	enriched := make([]ClusterResponse, 0, len(clusters))
	for _, c := range clusters {
		resp, err := h.enrichClusterFromCache(r.Context(), c)
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Failed to render cluster metadata")
			return
		}
		setClusterResponseHeartbeat(&resp, livenessByCluster[c.ID])
		resp.Decommissioning = decommissioning[c.ID]
		enriched = append(enriched, resp)
	}
	metadata := paging.Exact(total, limitInt, offsetInt, len(enriched))
	if cursorMode {
		metadata = paging.CursorPage(total, limitInt, seen, len(enriched), nextCursor)
	}
	paging.Write(w, enriched, metadata)
}

// inFlightDecommissionSet returns which of the given cluster IDs have a
// pending/running decommission. Best-effort — returns an empty set on any
// error. The query receives only the already-authorized page IDs, so work is
// bounded by the page size and no estate-wide decommission state is read.
func (h *ClusterHandler) inFlightDecommissionSet(ctx context.Context, ids []uuid.UUID) map[uuid.UUID]bool {
	set := map[uuid.UUID]bool{}
	if len(ids) == 0 {
		return set
	}
	rows, err := h.queries.ListPendingClusterDecommissionsForClusters(ctx, ids)
	if err != nil {
		return set
	}
	for _, row := range rows {
		set[row.ClusterID] = true
	}
	return set
}

// Create handles POST /api/v1/clusters/.
func (h *ClusterHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req CreateClusterRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}

	// Estate-wide cap (migration 051). The 'global' quota plan's
	// max_total_clusters caps how many clusters the platform will
	// hold. Soft enforcement is logged + metric'd; hard returns a 429.
	if h.enforcer != nil {
		if err := h.enforcer.CheckGlobalClusterCreate(r.Context()); err != nil {
			if qe, ok := quota.IsQuotaExceeded(err); ok {
				WriteQuotaExceeded(w, qe)
				return
			}
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.QuotaCheckError, "Failed to evaluate cluster quota")
			return
		}
	}

	labels := req.Labels
	if labels == nil {
		labels = json.RawMessage(`{}`)
	}
	annotations := req.Annotations
	if err := validateDirectAccessConfig(req.ApiServerUrl, req.CaCertificate); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, err.Error())
		return
	}
	badgeText, badgeColor, err := normalizeClusterBadge(req.BadgeText, req.BadgeColor)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, err.Error())
		return
	}
	// Downstream-impersonation mode: superuser-only, validated, default off.
	// uuid.Nil as the cluster id is correct here — the row does not exist yet,
	// so there is nothing stored to preserve and no agent that could have
	// advertised the capability, which makes `enforce` unreachable at create
	// time by construction.
	annotations, ok := guardDownstreamImpersonationAnnotation(w, r, h.queries, uuid.Nil, nil, annotations)
	if !ok {
		return
	}
	annotations = fullManagementAnnotations(annotations)
	agentOverrides, err := marshalAgentOverrides(req.AgentOverrides)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, err.Error())
		return
	}
	params := sqlc.CreateClusterParams{
		Name:           req.Name,
		DisplayName:    req.DisplayName,
		Description:    req.Description,
		Environment:    req.Environment,
		Region:         req.Region,
		Provider:       req.Provider,
		Distribution:   req.Distribution,
		Labels:         labels,
		Annotations:    annotations,
		ApiServerUrl:   strings.TrimSpace(req.ApiServerUrl),
		CaCertificate:  strings.TrimSpace(req.CaCertificate),
		CreatedByID:    currentUserUUID(r),
		BadgeText:      badgeText,
		BadgeColor:     badgeColor,
		AgentOverrides: agentOverrides,
	}
	cluster, err := executeMutation(r, h.runTx,
		func(q ClusterMutationTx) (sqlc.Cluster, error) { return q.CreateCluster(r.Context(), params) },
		func(cluster sqlc.Cluster) mutationAuditEvent {
			return mutationAuditEvent{
				action: "cluster.create", resourceType: "cluster", resourceID: cluster.ID.String(), resourceName: cluster.Name,
				status: http.StatusCreated,
				detail: map[string]any{
					"environment": req.Environment, "region": req.Region,
					"provider": req.Provider, "distribution": req.Distribution,
				},
			}
		})
	if err != nil {
		if isUniqueViolation(err) {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, fmt.Sprintf("A cluster named %q already exists", req.Name))
			return
		}
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.CreateError, "Failed to create cluster")
		return
	}

	h.publishEvent("cluster.created", map[string]any{
		"cluster_id":   cluster.ID.String(),
		"name":         cluster.Name,
		"display_name": cluster.DisplayName,
		"status":       cluster.Status,
	})

	// Wizard step rows. Best-effort: when the registration service
	// isn't wired (legacy test harness), we just skip the timeline
	// rows — the API still returns the cluster body.
	if h.registration != nil {
		_, _ = h.registration.WriteStep(r.Context(), cluster.ID, registration.StepInput{
			StepName: "cluster_created",
			Status:   "success",
			Detail: map[string]any{
				"name":         cluster.Name,
				"display_name": cluster.DisplayName,
			},
		})
		_, _ = h.registration.WriteStep(r.Context(), cluster.ID, registration.StepInput{
			StepName: "manifest_generated",
			Status:   "success",
		})
	}

	h.triggerGrafanaFolders()

	w.Header().Set("Location", "/api/v1/clusters/"+cluster.ID.String()+"/")
	response, err := clusterToResponse(cluster)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Failed to render cluster metadata")
		return
	}
	RespondJSON(w, http.StatusCreated, response)
}

// Get handles GET /api/v1/clusters/{id}/.
func (h *ClusterHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, ok := parseClusterIDParam(w, r, "id")
	if !ok {
		return
	}

	cluster, err := h.queries.GetClusterByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}

	out, err := h.enrichClusterFresh(r.Context(), cluster)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Failed to render cluster metadata")
		return
	}
	livenessStore, ok := h.queries.(clusterLivenessQuerier)
	if !ok {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.StoreUnavailable, "Cluster liveness store is not available")
		return
	}
	if liveness, livenessErr := livenessStore.GetClusterLiveness(r.Context(), id); livenessErr == nil {
		setClusterResponseHeartbeat(&out, liveness.LastHeartbeat)
	} else if !errors.Is(livenessErr, pgx.ErrNoRows) {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, "Failed to load cluster liveness")
		return
	}
	if latest, derr := h.queries.GetLatestClusterDecommissionByCluster(r.Context(), id); derr == nil {
		out.Decommissioning = latest.Status == "pending" || latest.Status == "running"
	}
	RespondJSON(w, http.StatusOK, out)
}

// Update handles PUT /api/v1/clusters/{id}/.
func (h *ClusterHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, ok := parseClusterIDParam(w, r, "id")
	if !ok {
		return
	}

	var req UpdateClusterRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}

	if blocked, err := clusterUpdateBlockedByOwnership(r.Context(), h.queries, id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, "Failed to check cluster ownership")
		return
	} else if blocked != "" {
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, blocked)
		return
	}

	cluster, err := executeMutation(r, h.runTx,
		func(q ClusterMutationTx) (sqlc.Cluster, error) {
			// Lock before merging so two concurrent partial updates cannot restore
			// stale values for fields the second request omitted.
			existing, lockErr := q.GetClusterByIDForUpdate(r.Context(), id)
			if lockErr != nil {
				return sqlc.Cluster{}, lockErr
			}
			params, mergeErr := mergeClusterUpdate(id, existing, req)
			if mergeErr != nil {
				return sqlc.Cluster{}, mergeErr
			}
			annotations, ok := guardDownstreamImpersonationAnnotation(w, r, q, id, existing.Annotations, params.Annotations)
			if !ok {
				return sqlc.Cluster{}, errClusterUpdateResponseWritten
			}
			params.Annotations = annotations
			return q.UpdateCluster(r.Context(), params)
		},
		func(cluster sqlc.Cluster) mutationAuditEvent {
			return mutationAuditEvent{
				action: "cluster.update", resourceType: "cluster", resourceID: cluster.ID.String(), resourceName: cluster.Name,
				status: http.StatusOK,
				detail: map[string]any{
					"display_name": cluster.DisplayName, "description": cluster.Description,
					"environment": cluster.Environment, "region": cluster.Region,
				},
			}
		})
	if err != nil {
		if errors.Is(err, errClusterUpdateResponseWritten) {
			return
		}
		var validationErr *clusterUpdateValidationError
		if errors.As(err, &validationErr) {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, validationErr.Error())
			return
		}
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		} else if errors.Is(err, audit.ErrOutboxUnavailable) {
			respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.DBError, "Failed to update cluster")
		} else {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, "Failed to update cluster")
		}
		return
	}

	h.publishEvent("cluster.updated", map[string]any{
		"cluster_id":   cluster.ID.String(),
		"name":         cluster.Name,
		"display_name": cluster.DisplayName,
		"status":       cluster.Status,
	})

	h.triggerGrafanaFolders()
	response, err := clusterToResponse(cluster)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Failed to render cluster metadata")
		return
	}
	RespondJSON(w, http.StatusOK, response)
}

var errClusterUpdateResponseWritten = errors.New("cluster update response already written")

type clusterUpdateValidationError struct{ err error }

func (e *clusterUpdateValidationError) Error() string { return e.err.Error() }

func (e *clusterUpdateValidationError) Unwrap() error { return e.err }

// mergeClusterUpdate implements lossless PATCH semantics over the generated
// request shape. Explicit empty values clear a field; omitted values preserve
// the row locked by the caller.
func mergeClusterUpdate(id uuid.UUID, existing sqlc.Cluster, req UpdateClusterRequest) (sqlc.UpdateClusterParams, error) {
	displayName, description := existing.DisplayName, existing.Description
	environment, region := existing.Environment, existing.Region
	labels, annotations := existing.Labels, existing.Annotations
	if req.DisplayName != nil {
		displayName = *req.DisplayName
	}
	if req.Description != nil {
		description = *req.Description
	}
	if req.Environment != nil {
		environment = *req.Environment
	}
	if req.Region != nil {
		region = *req.Region
	}
	if req.Labels != nil {
		labels = *req.Labels
	}
	if req.Annotations != nil {
		annotations = *req.Annotations
	}
	directURL, directCA := existing.ApiServerUrl, existing.CaCertificate
	if req.ApiServerUrl != nil {
		directURL = strings.TrimSpace(*req.ApiServerUrl)
	}
	if req.CaCertificate != nil {
		directCA = strings.TrimSpace(*req.CaCertificate)
	}
	badgeText, badgeColor := existing.BadgeText, existing.BadgeColor
	agentOverrides := existing.AgentOverrides
	if req.AgentOverrides != nil {
		if err := req.AgentOverrides.Validate(); err != nil {
			return sqlc.UpdateClusterParams{}, &clusterUpdateValidationError{err: err}
		}
		var err error
		agentOverrides, err = marshalAgentOverrides(*req.AgentOverrides)
		if err != nil {
			return sqlc.UpdateClusterParams{}, &clusterUpdateValidationError{err: err}
		}
	}
	if req.BadgeText != nil {
		badgeText = *req.BadgeText
		if strings.TrimSpace(badgeText) == "" && req.BadgeColor == nil {
			badgeColor = ""
		}
	}
	if req.BadgeColor != nil {
		badgeColor = *req.BadgeColor
	}
	badgeText, badgeColor, badgeErr := normalizeClusterBadge(badgeText, badgeColor)
	if badgeErr != nil {
		return sqlc.UpdateClusterParams{}, &clusterUpdateValidationError{err: badgeErr}
	}
	if req.ApiServerUrl != nil || req.CaCertificate != nil {
		if err := validateDirectAccessConfig(directURL, directCA); err != nil {
			return sqlc.UpdateClusterParams{}, &clusterUpdateValidationError{err: err}
		}
	}
	return sqlc.UpdateClusterParams{
		ID: id, DisplayName: displayName, Description: description,
		Environment: environment, Region: region, Labels: labels, Annotations: annotations,
		ApiServerUrl:   pgtype.Text{String: directURL, Valid: true},
		CaCertificate:  pgtype.Text{String: directCA, Valid: true},
		BadgeText:      pgtype.Text{String: badgeText, Valid: true},
		BadgeColor:     pgtype.Text{String: badgeColor, Valid: true},
		AgentOverrides: agentOverrides,
	}, nil
}

func marshalAgentOverrides(overrides agenttemplate.AgentOverrides) (json.RawMessage, error) {
	encoded, err := overrides.CanonicalJSON()
	if err != nil {
		return nil, fmt.Errorf("invalid agent_overrides: %w", err)
	}
	return encoded, nil
}

func persistedAgentOverrides(raw json.RawMessage) (agenttemplate.AgentOverrides, error) {
	if len(raw) == 0 {
		return agenttemplate.AgentOverrides{}, nil
	}
	if len(raw) > 32<<10 {
		return agenttemplate.AgentOverrides{}, fmt.Errorf("invalid persisted agent_overrides: exceeds 32768 bytes")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		if err == nil {
			err = errors.New("value must be an object")
		}
		return agenttemplate.AgentOverrides{}, fmt.Errorf("invalid persisted agent_overrides: %w", err)
	}
	// The persisted reader deliberately tolerates unknown top-level keys so a
	// newer writer can be rolled back without making older servers panic. New
	// writes still pass AgentOverrides.CanonicalJSON, whose strict validation
	// rejects unsupported fields at the API boundary.
	type persistedAgentOverridesWire agenttemplate.AgentOverrides
	var wire persistedAgentOverridesWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return agenttemplate.AgentOverrides{}, fmt.Errorf("invalid persisted agent_overrides: %w", err)
	}
	overrides := agenttemplate.AgentOverrides(wire)
	if err := overrides.Validate(); err != nil {
		return agenttemplate.AgentOverrides{}, fmt.Errorf("invalid persisted agent_overrides: %w", err)
	}
	return overrides, nil
}

var clusterBadgeColors = map[string]struct{}{
	"slate": {}, "blue": {}, "green": {}, "amber": {},
	"red": {}, "purple": {},
}

func normalizeClusterBadge(text, color string) (string, string, error) {
	text = strings.TrimSpace(text)
	color = strings.ToLower(strings.TrimSpace(color))
	if utf8.RuneCountInString(text) > 24 {
		return "", "", fmt.Errorf("badge_text must be at most 24 characters")
	}
	if text == "" {
		if color != "" {
			return "", "", fmt.Errorf("badge_color requires badge_text")
		}
		return "", "", nil
	}
	if color == "" {
		color = "slate"
	}
	if _, ok := clusterBadgeColors[color]; !ok {
		return "", "", fmt.Errorf("badge_color must be one of slate, blue, green, amber, red, or purple")
	}
	return text, color, nil
}
