// Package handler — platform health summary.
//
// A single GET endpoint that aggregates the signals
// an operator needs to answer "is anything broken right now?" without
// drilling cluster-by-cluster. The numbers fall into three buckets:
//
//  1. Cluster state — total / degraded / disconnected.
//     "Degraded" means at least one cluster_condition is recorded with
//     status not equal to "True" for a positive-sense condition
//     (AgentReachable, Connected). "Disconnected" is the narrower case
//     where the agent itself isn't currently in agent_connections
//     with status='connected'.
//
//  2. Worker queue — recent error count + DLQ depth. Both come from the
//     asynq inspector since they're live-process state, not DB state.
//
//  3. Cache freshness — when this snapshot was computed. The handler is
//     intentionally not cached server-side; the data is cheap to gather
//     and operators want it live when they're triaging.
//
// Auth: standard authenticated user. No superuser gate — every operator
// who can see the dashboard should be able to see the rollup.
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
)

// PlatformHealthPooler is the slice of *pgxpool.Pool the handler needs.
// Tests can supply a fake; production wires the live pool.
type PlatformHealthPooler interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// PlatformHealthInspector is the slice of asynq.Inspector for the queue
// portion of the rollup. nil disables those fields.
type PlatformHealthInspector interface {
	Queues() ([]string, error)
	GetQueueInfo(qname string) (*asynq.QueueInfo, error)
}

// PlatformHealthHandler wraps GET /api/v1/platform/health-summary/.
type PlatformHealthHandler struct {
	db        PlatformHealthPooler
	inspector PlatformHealthInspector
}

// NewPlatformHealthHandler builds a handler. db is required; inspector is
// optional (when nil, queue fields are zero/absent).
func NewPlatformHealthHandler(db PlatformHealthPooler) *PlatformHealthHandler {
	return &PlatformHealthHandler{db: db}
}

// SetAsynqInspector wires the asynq inspector. Without it, worker_dlq_depth
// stays at 0 and worker_error_count is absent from the response.
func (h *PlatformHealthHandler) SetAsynqInspector(insp PlatformHealthInspector) {
	if h == nil {
		return
	}
	h.inspector = insp
}

// PlatformHealthResponse is the wire shape. All counts are non-negative
// integers; computing one of them failing surfaces as a zero with a
// warning in the `errors` field (operators see a rollup, partial-failure
// doesn't kill the whole response).
type PlatformHealthResponse struct {
	ClustersTotal        int       `json:"clusters_total"`
	ClustersDegraded     int       `json:"clusters_degraded"`
	ClustersDisconnected int       `json:"clusters_disconnected"`
	WorkerDLQDepth       int       `json:"worker_dlq_depth"`
	WorkerRetryDepth     int       `json:"worker_retry_depth"`
	AsOf                 time.Time `json:"as_of"`
	Errors               []string  `json:"errors,omitempty"`
}

// Summary is the chi-mounted handler.
func (h *PlatformHealthHandler) Summary(w http.ResponseWriter, r *http.Request) {
	resp := PlatformHealthResponse{AsOf: time.Now().UTC()}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	if h.db != nil {
		// Single round-trip aggregation. Joins clusters → cluster_conditions
		// to count rows where ANY condition is not status='True' (excluding
		// soft-deleted "decommissioned" clusters from the denominator).
		// Disconnected is the narrower case: no active agent_connections row.
		const q = `
WITH base AS (
  SELECT id FROM clusters WHERE decommissioned_at IS NULL
),
degraded AS (
  SELECT DISTINCT b.id
  FROM base b
  JOIN cluster_conditions c ON c.cluster_id = b.id
  WHERE c.status <> 'True'
    AND c.type IN ('AgentReachable', 'Connected')
),
disconnected AS (
  SELECT b.id
  FROM base b
  LEFT JOIN agent_connections a
    ON a.cluster_id = b.id AND a.status = 'connected' AND a.disconnected_at IS NULL
  WHERE a.id IS NULL
)
SELECT
  (SELECT COUNT(*) FROM base),
  (SELECT COUNT(*) FROM degraded),
  (SELECT COUNT(*) FROM disconnected);
`
		var total, degraded, disconnected int
		err := h.db.QueryRow(ctx, q).Scan(&total, &degraded, &disconnected)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			resp.Errors = append(resp.Errors, "cluster_state: "+err.Error())
		} else {
			resp.ClustersTotal = total
			resp.ClustersDegraded = degraded
			resp.ClustersDisconnected = disconnected
		}
	} else {
		resp.Errors = append(resp.Errors, "cluster_state: db pool not wired")
	}

	if h.inspector != nil {
		queues, err := h.inspector.Queues()
		if err != nil {
			resp.Errors = append(resp.Errors, "queues: "+err.Error())
		} else {
			for _, qn := range queues {
				info, qerr := h.inspector.GetQueueInfo(qn)
				if qerr != nil {
					resp.Errors = append(resp.Errors, "queue "+qn+": "+qerr.Error())
					continue
				}
				resp.WorkerDLQDepth += info.Archived
				resp.WorkerRetryDepth += info.Retry
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": resp})
}
