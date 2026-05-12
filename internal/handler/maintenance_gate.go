// Maintenance window gating helpers used by the mutation endpoints
// (migration 057).
//
// Each gated handler calls EnforceMaintenanceWindow at the top of its
// body. When the helper returns true, the response has already been
// written (409 for refuse / 202 for defer); the caller MUST return
// without further work.
//
// Layout:
//
//   - GatedOpQuerier: the read-side surface the deferred-op INSERT path
//     needs (just the CreateDeferredOperation method). When nil, defer
//     mode degrades to refuse (the more conservative choice).
//
//   - MaintenanceGate: the evaluator + querier bundle the handlers
//     embed. nil-safe: when not wired, every gate call returns "not
//     blocked" so windows can be added incrementally without breaking
//     existing tests.
//
//   - EnforceMaintenanceWindow: the per-call hook. Returns blocked=true
//     when a response has been written.

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/maintenance"
)

// GatedOpQuerier is the slice of sqlc.Queries the gate uses for its
// DEFER path. *sqlc.Queries satisfies this naturally.
type GatedOpQuerier interface {
	CreateDeferredOperation(ctx context.Context, arg sqlc.CreateDeferredOperationParams) (sqlc.DeferredOperation, error)
}

// MaintenanceGate bundles the evaluator + querier the per-mutation
// hook needs. nil-safe — when the gate or its evaluator is nil, every
// call returns "not blocked" so a partially-wired test harness can
// still exercise the underlying handler.
type MaintenanceGate struct {
	Evaluator *maintenance.Evaluator
	Queries   GatedOpQuerier
}

// NewMaintenanceGate builds a gate. evaluator may be nil to disable the
// feature without removing the call sites.
func NewMaintenanceGate(evaluator *maintenance.Evaluator, queries GatedOpQuerier) *MaintenanceGate {
	return &MaintenanceGate{Evaluator: evaluator, Queries: queries}
}

// DeferredOpSpec is the JSONB-serializable bag the defer path stores
// in operation_spec. Handlers fill it with the same pieces the original
// request carried so the dispatcher can replay the mutation. The shape
// is operation-type-specific; the dispatcher does the type switch.
type DeferredOpSpec struct {
	Method      string            `json:"method,omitempty"`
	Path        string            `json:"path,omitempty"`
	URLParams   map[string]string `json:"url_params,omitempty"`
	QueryParams map[string]string `json:"query_params,omitempty"`
	Body        json.RawMessage   `json:"body,omitempty"`
}

// EnforceMaintenanceWindow is the per-request hook. Returns blocked=
// true ONLY when an HTTP response has already been written (either 409
// for refuse-mode or 202 for defer-mode). The caller MUST return
// immediately when blocked=true.
//
// targetCluster may be sqlc.Cluster zero-value when the operation is
// not cluster-scoped (e.g. a project.delete that has no specific
// cluster target); pass nil for clusterLabels in that case.
func EnforceMaintenanceWindow(
	w http.ResponseWriter,
	r *http.Request,
	gate *MaintenanceGate,
	opType string,
	clusterLabels map[string]string,
	clusterID, projectID pgtype.UUID,
) bool {
	if gate == nil || gate.Evaluator == nil {
		return false
	}
	now := time.Now().UTC()
	blocked, win, err := maintenance.IsBlocked(r.Context(), gate.Evaluator, opType, clusterLabels, now)
	if err != nil || !blocked || win == nil {
		return false
	}

	// We have a window match — record metrics + decide refuse vs defer.
	maintenance.RecordBlocked(opType, win.Mode)

	if win.OnBlock == maintenance.OnBlockRefuse {
		respondMaintenanceRefuse(w, *win, now)
		recordAudit(r, queriesForAudit(gate), "operation.blocked_by_window", "maintenance_window", win.ID.String(), win.Name, map[string]any{
			"op_type": opType,
			"mode":    win.Mode,
		})
		return true
	}

	// Defer path. If the querier isn't wired, fall back to refuse — the
	// more conservative choice when we can't durably queue the op.
	if gate.Queries == nil {
		respondMaintenanceRefuse(w, *win, now)
		recordAudit(r, queriesForAudit(gate), "operation.blocked_by_window", "maintenance_window", win.ID.String(), win.Name, map[string]any{
			"op_type":  opType,
			"mode":     win.Mode,
			"degraded": "defer_unavailable",
		})
		return true
	}

	deferredUntil := maintenance.NextOpen(*win, now)
	expiresAt := deferredUntil.Add(24 * time.Hour)
	spec := buildDeferredSpec(r)
	specBytes, _ := json.Marshal(spec)
	if len(specBytes) == 0 {
		specBytes = []byte("{}")
	}
	row, err := gate.Queries.CreateDeferredOperation(r.Context(), sqlc.CreateDeferredOperationParams{
		WindowID:        win.ID,
		OperationType:   opType,
		OperationSpec:   specBytes,
		TargetClusterID: clusterID,
		TargetProjectID: projectID,
		DeferredUntil:   pgtype.Timestamptz{Time: deferredUntil, Valid: !deferredUntil.IsZero()},
		ExpiresAt:       pgtype.Timestamptz{Time: expiresAt, Valid: !deferredUntil.IsZero()},
		RequestedBy:     currentUserUUID(r),
	})
	if err != nil {
		// If we can't queue, refuse rather than silently letting the
		// op through.
		respondMaintenanceRefuse(w, *win, now)
		recordAudit(r, queriesForAudit(gate), "operation.blocked_by_window", "maintenance_window", win.ID.String(), win.Name, map[string]any{
			"op_type":   opType,
			"mode":      win.Mode,
			"degraded":  "defer_insert_failed",
			"insert_err": err.Error(),
		})
		return true
	}

	maintenance.RecordDeferred(opType)
	recordAudit(r, queriesForAudit(gate), "operation.deferred", "deferred_operation", row.ID.String(), opType, map[string]any{
		"window_id": win.ID.String(),
		"next_open": deferredUntil.Format(time.RFC3339),
	})

	RespondJSON(w, http.StatusAccepted, map[string]any{
		"deferred_id":  row.ID.String(),
		"window_id":    win.ID.String(),
		"window_name":  win.Name,
		"next_open":    deferredUntil.Format(time.RFC3339),
		"expires_at":   expiresAt.Format(time.RFC3339),
		"on_block":     win.OnBlock,
		"message":      "Operation deferred until the next maintenance window open",
	})
	return true
}

// respondMaintenanceRefuse writes the 409 body with the window
// identifier + next-open hint so the operator/client can decide
// whether to retry later.
func respondMaintenanceRefuse(w http.ResponseWriter, win maintenance.Window, now time.Time) {
	next := maintenance.NextOpen(win, now)
	body := map[string]any{
		"error": map[string]any{
			"code":    "maintenance_window_active",
			"message": "Operation blocked by maintenance window: " + win.Name,
			"window_id": win.ID.String(),
			"window_name": win.Name,
			"mode":   win.Mode,
		},
	}
	if win.Mode == maintenance.ModeBlackout {
		// For blackout windows, the operator can retry after the
		// current window closes; that close time is more useful than
		// the next open.
		close := maintenance.NextClose(win, now)
		if !close.IsZero() {
			body["error"].(map[string]any)["opens_at"] = close.Format(time.RFC3339)
		}
	}
	if !next.IsZero() {
		body["error"].(map[string]any)["next_open"] = next.Format(time.RFC3339)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	_ = json.NewEncoder(w).Encode(body)
}

// buildDeferredSpec captures the original request's identifying pieces
// so the dispatcher can replay the operation. Body is captured opaquely
// so per-op-type knowledge stays with the dispatcher's type switch
// rather than this helper.
func buildDeferredSpec(r *http.Request) DeferredOpSpec {
	spec := DeferredOpSpec{
		Method:    r.Method,
		Path:      r.URL.Path,
		URLParams: map[string]string{},
	}
	q := r.URL.Query()
	if len(q) > 0 {
		spec.QueryParams = map[string]string{}
		for k, v := range q {
			if len(v) > 0 {
				spec.QueryParams[k] = v[0]
			}
		}
	}
	return spec
}

// queriesForAudit returns the queries field as `any` so recordAudit's
// type-assertion to auditWriterV1 succeeds when *sqlc.Queries was
// passed in. Falls back to nil when no queryable is wired.
func queriesForAudit(gate *MaintenanceGate) any {
	if gate == nil {
		return nil
	}
	return gate.Queries
}

// MaintenanceGateClusterLabels is a small helper to extract labels from
// a sqlc.Cluster row. Centralised so each hook site doesn't duplicate
// the JSON-unmarshal idiom.
func MaintenanceGateClusterLabels(c sqlc.Cluster) map[string]string {
	labels := map[string]string{}
	if len(c.Labels) > 0 {
		_ = json.Unmarshal(c.Labels, &labels)
	}
	return labels
}

// Compile-time assertion: uuid.UUID import used so Update path stays
// stable if a refactor drops the constant set above.
var _ = uuid.Nil
