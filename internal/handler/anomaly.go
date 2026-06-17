package handler

// Sprint 072 — read-only inspection of anomaly_baselines.
//
// Operators need a way to introspect what the recompute worker has
// observed for tuning purposes ("the cpu baseline for cluster X
// looks like mean=72 stddev=4 — that explains why the 3σ rule keeps
// firing"). The write path is the worker; this handler is read-only
// by design.

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
)

// AnomalyBaselineQuerier is the narrow interface the handler needs.
// Kept distinct from AlertingQuerier so the dependency graph stays
// explicit per-feature.
type AnomalyBaselineQuerier interface {
	ListAnomalyBaselines(ctx context.Context, arg sqlc.ListAnomalyBaselinesParams) ([]sqlc.AnomalyBaseline, error)
	GetAnomalyBaselineByID(ctx context.Context, id uuid.UUID) (sqlc.AnomalyBaseline, error)
	ListAnomalyBaselinesByCluster(ctx context.Context, clusterID uuid.UUID) ([]sqlc.AnomalyBaseline, error)
	CountAnomalyBaselines(ctx context.Context) (int64, error)
}

// AnomalyHandler serves the /api/v1/anomaly-baselines/* read-only
// endpoints.
type AnomalyHandler struct {
	queries AnomalyBaselineQuerier
}

// NewAnomalyHandler builds an AnomalyHandler.
func NewAnomalyHandler(queries AnomalyBaselineQuerier) *AnomalyHandler {
	return &AnomalyHandler{queries: queries}
}

// List handles GET /api/v1/anomaly-baselines/.
//
// Supports ?clusterId= to scope the listing to one cluster. The
// default is all baselines, sorted by most-recently-updated.
func (h *AnomalyHandler) List(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.Unavailable, "anomaly baseline querier not configured")
		return
	}
	if clusterIDRaw := r.URL.Query().Get("clusterId"); clusterIDRaw != "" {
		clusterID, err := uuid.Parse(clusterIDRaw)
		if err != nil {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidClusterID, "Invalid clusterId")
			return
		}
		rows, err := h.queries.ListAnomalyBaselinesByCluster(r.Context(), clusterID)
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list anomaly baselines")
			return
		}
		items := make([]map[string]any, 0, len(rows))
		for _, b := range rows {
			items = append(items, anomalyBaselineResponse(b))
		}
		// Per-cluster listing has no COUNT query and returns the full
		// (cluster-scoped) set unpaginated, so Total is the page length.
		// TODO(total)
		limit, offset := queryLimitOffset(r, 50)
		RespondList(w, items, NewPagination(len(items), limit, offset, len(items)))
		return
	}
	limit := int32(queryInt(r, "limit", 50))
	offset := int32(queryInt(r, "offset", 0))
	rows, err := h.queries.ListAnomalyBaselines(r.Context(), sqlc.ListAnomalyBaselinesParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list anomaly baselines")
		return
	}
	items := make([]map[string]any, 0, len(rows))
	for _, b := range rows {
		items = append(items, anomalyBaselineResponse(b))
	}
	total, _ := h.queries.CountAnomalyBaselines(r.Context())
	RespondList(w, items, NewPagination(int(total), int(limit), int(offset), len(items)))
}

// Get handles GET /api/v1/anomaly-baselines/{id}/.
func (h *AnomalyHandler) Get(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.Unavailable, "anomaly baseline querier not configured")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid baseline ID")
		return
	}
	row, err := h.queries.GetAnomalyBaselineByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Anomaly baseline not found")
		return
	}
	RespondJSON(w, http.StatusOK, anomalyBaselineResponse(row))
}

// anomalyBaselineResponse marshals a baseline row into the JSON
// shape the frontend's baseline-inspection page expects.
//
// Two notable choices:
//   - The recent_samples ring buffer is NOT exposed. It's an
//     implementation detail of the recompute worker; surfacing it
//     would tempt UI authors to render every datapoint, blowing
//     up render time for clusters with active baselines.
//   - lastValueAt is RFC3339, not Unix. Matches every other
//     timestamped response in the alerting package.
func anomalyBaselineResponse(b sqlc.AnomalyBaseline) map[string]any {
	resp := map[string]any{
		"id":            b.ID.String(),
		"clusterId":     b.ClusterID.String(),
		"metric":        b.MetricName,
		"windowSeconds": b.WindowSeconds,
		"sampleCount":   b.SampleCount,
		"mean":          b.Mean,
		"stddev":        b.Stddev,
		"min":           b.MinValue,
		"max":           b.MaxValue,
		"p50":           b.P50,
		"p95":           b.P95,
		"p99":           b.P99,
		"lastValue":     b.LastValue,
		"updatedAt":     b.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if b.LastValueAt.Valid {
		resp["lastValueAt"] = b.LastValueAt.Time.UTC().Format(time.RFC3339)
	} else {
		resp["lastValueAt"] = nil
	}
	// Surface the configured retention as a hint for the UI even
	// though the operator's anomaly_min_samples lives on the
	// rule, not the baseline. Useful diagnostic on the
	// per-baseline page.
	resp["recentSampleBytes"] = len(b.RecentSamples)
	return resp
}
