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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/maintenance"
)

// GatedOpQuerier is the slice of sqlc.Queries the gate uses for its
// DEFER path. *sqlc.Queries satisfies this naturally.
type GatedOpQuerier interface {
	CreateDeferredOperation(ctx context.Context, arg sqlc.CreateDeferredOperationParams) (sqlc.DeferredOperation, error)
}

type MaintenanceGateMutationTx interface {
	GatedOpQuerier
	audit.OutboxQuerier
}

type maintenanceGateRunTxFunc func(context.Context, func(MaintenanceGateMutationTx) error) error

// MaintenanceGate bundles the evaluator + querier the per-mutation
// hook needs. nil-safe — when the gate or its evaluator is nil, every
// call returns "not blocked" so a partially-wired test harness can
// still exercise the underlying handler.
type MaintenanceGate struct {
	Evaluator *maintenance.Evaluator
	Queries   GatedOpQuerier
	Cipher    DeferredSpecCipher
	runTx     maintenanceGateRunTxFunc
}

// DeferredSpecCipher keeps deferred mutation bodies encrypted at rest. The
// production Fernet encryptor satisfies this interface.
type DeferredSpecCipher interface {
	Encrypt(string) (string, error)
}

// NewMaintenanceGate builds a gate. evaluator may be nil to disable the
// feature without removing the call sites. A cipher is required before defer
// mode can accept work; absent encryption degrades safely to refuse mode.
func NewMaintenanceGate(evaluator *maintenance.Evaluator, queries GatedOpQuerier, cipher ...DeferredSpecCipher) *MaintenanceGate {
	gate := &MaintenanceGate{Evaluator: evaluator, Queries: queries}
	if len(cipher) > 0 {
		gate.Cipher = cipher[0]
	}
	return gate
}

func (g *MaintenanceGate) SetRunTx(runTx maintenanceGateRunTxFunc) {
	if g != nil {
		g.runTx = runTx
	}
}

func (g *MaintenanceGate) TransactionalAuditWired() bool { return g != nil && g.runTx != nil }

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

// EncryptedDeferredOpSpec is the only operation_spec shape written for new
// deferred mutations. Keeping the envelope versioned allows online key
// rotation and a bounded compatibility reader for pre-encryption rows.
type EncryptedDeferredOpSpec struct {
	SchemaVersion int    `json:"schema_version"`
	Ciphertext    string `json:"ciphertext"`
}

const maxDeferredOperationBodyBytes = 1 << 20

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

	// Defer path. Durable storage and at-rest encryption are both mandatory.
	// Falling back to refuse is safer than accepting an intent we cannot replay
	// or persisting credentials/values YAML in cleartext JSONB.
	if gate.Queries == nil || gate.Cipher == nil {
		respondMaintenanceRefuse(w, *win, now)
		recordAudit(r, queriesForAudit(gate), "operation.blocked_by_window", "maintenance_window", win.ID.String(), win.Name, map[string]any{
			"op_type":  opType,
			"mode":     win.Mode,
			"degraded": "defer_storage_or_encryption_unavailable",
		})
		return true
	}

	deferredUntil := maintenance.NextOpen(*win, now)
	expiresAt := deferredUntil.Add(24 * time.Hour)
	spec, specErr := buildDeferredSpec(r)
	if specErr != nil {
		respondMaintenanceRefuse(w, *win, now)
		recordAudit(r, queriesForAudit(gate), "operation.blocked_by_window", "maintenance_window", win.ID.String(), win.Name, map[string]any{
			"op_type": opType, "mode": win.Mode, "degraded": "defer_request_capture_failed",
		})
		return true
	}
	plainSpec, err := json.Marshal(spec)
	if err != nil {
		respondMaintenanceRefuse(w, *win, now)
		return true
	}
	ciphertext, err := gate.Cipher.Encrypt(string(plainSpec))
	clear(plainSpec)
	if err != nil {
		respondMaintenanceRefuse(w, *win, now)
		recordAudit(r, queriesForAudit(gate), "operation.blocked_by_window", "maintenance_window", win.ID.String(), win.Name, map[string]any{
			"op_type": opType, "mode": win.Mode, "degraded": "defer_encrypt_failed",
		})
		return true
	}
	specBytes, err := json.Marshal(EncryptedDeferredOpSpec{SchemaVersion: 1, Ciphertext: ciphertext})
	if err != nil {
		respondMaintenanceRefuse(w, *win, now)
		return true
	}
	params := sqlc.CreateDeferredOperationParams{
		WindowID:        win.ID,
		OperationType:   opType,
		OperationSpec:   specBytes,
		TargetClusterID: clusterID,
		TargetProjectID: projectID,
		DeferredUntil:   pgtype.Timestamptz{Time: deferredUntil, Valid: !deferredUntil.IsZero()},
		ExpiresAt:       pgtype.Timestamptz{Time: expiresAt, Valid: !deferredUntil.IsZero()},
		RequestedBy:     currentUserUUID(r),
	}
	mutationContext := withOperationIdempotency(r, "deferred")
	var row sqlc.DeferredOperation
	if gate.runTx != nil {
		err = gate.runTx(r.Context(), func(q MaintenanceGateMutationTx) error {
			var createErr error
			row, createErr = createDeferredOperation(mutationContext, q, params)
			if createErr != nil {
				return createErr
			}
			return recordAuditOutbox(r, q, "operation.deferred", "deferred_operation", row.ID.String(), opType, http.StatusAccepted, map[string]any{
				"window_id": win.ID.String(),
				"next_open": deferredUntil.Format(time.RFC3339),
			})
		})
	} else {
		row, err = createDeferredOperation(mutationContext, gate.Queries, params)
		if err == nil {
			recordAudit(r, queriesForAudit(gate), "operation.deferred", "deferred_operation", row.ID.String(), opType, map[string]any{
				"window_id": win.ID.String(),
				"next_open": deferredUntil.Format(time.RFC3339),
			})
		}
	}
	if err != nil {
		// If we can't queue, refuse rather than silently letting the
		// op through.
		respondMaintenanceRefuse(w, *win, now)
		recordAudit(r, queriesForAudit(gate), "operation.blocked_by_window", "maintenance_window", win.ID.String(), win.Name, map[string]any{
			"op_type":  opType,
			"mode":     win.Mode,
			"degraded": "defer_persistence_failed",
		})
		return true
	}

	maintenance.RecordDeferred(opType)

	RespondJSON(w, http.StatusAccepted, map[string]any{
		"deferred_id": row.ID.String(),
		"window_id":   win.ID.String(),
		"window_name": win.Name,
		"next_open":   deferredUntil.Format(time.RFC3339),
		"expires_at":  expiresAt.Format(time.RFC3339),
		"on_block":    win.OnBlock,
		"message":     "Operation deferred until the next maintenance window open",
	})
	return true
}

func createDeferredOperation(ctx context.Context, q GatedOpQuerier, params sqlc.CreateDeferredOperationParams) (sqlc.DeferredOperation, error) {
	if idem, ok := operationIdempotencyFromContext(ctx); ok {
		if creator, ok := q.(interface {
			CreateDeferredOperationIdempotent(context.Context, sqlc.CreateDeferredOperationIdempotentParams) (sqlc.DeferredOperation, error)
		}); ok {
			return creator.CreateDeferredOperationIdempotent(ctx, sqlc.CreateDeferredOperationIdempotentParams{
				Scope:           idem.scope,
				IdempotencyKey:  idem.key,
				WindowID:        params.WindowID,
				OperationType:   params.OperationType,
				OperationSpec:   params.OperationSpec,
				TargetClusterID: params.TargetClusterID,
				TargetProjectID: params.TargetProjectID,
				DeferredUntil:   params.DeferredUntil,
				ExpiresAt:       params.ExpiresAt,
				RequestedBy:     params.RequestedBy,
			})
		}
	}
	return q.CreateDeferredOperation(ctx, params)
}

// respondMaintenanceRefuse writes the 409 body with the window
// identifier + next-open hint so the operator/client can decide
// whether to retry later.
func respondMaintenanceRefuse(w http.ResponseWriter, win maintenance.Window, now time.Time) {
	next := maintenance.NextOpen(win, now)
	body := map[string]any{
		"error": map[string]any{
			"code":        "maintenance_window_active",
			"message":     "Operation blocked by maintenance window: " + win.Name,
			"window_id":   win.ID.String(),
			"window_name": win.Name,
			"mode":        win.Mode,
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
func buildDeferredSpec(r *http.Request) (DeferredOpSpec, error) {
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
	if r.Body != nil {
		body, err := io.ReadAll(io.LimitReader(r.Body, maxDeferredOperationBodyBytes+1))
		if err != nil {
			return DeferredOpSpec{}, fmt.Errorf("read deferred operation body: %w", err)
		}
		if len(body) > maxDeferredOperationBodyBytes {
			clear(body)
			return DeferredOpSpec{}, fmt.Errorf("deferred operation body exceeds %d bytes", maxDeferredOperationBodyBytes)
		}
		r.Body = io.NopCloser(bytes.NewReader(append([]byte(nil), body...)))
		if len(body) > 0 {
			if !json.Valid(body) {
				clear(body)
				return DeferredOpSpec{}, fmt.Errorf("deferred operation body is not valid JSON")
			}
			spec.Body = append(json.RawMessage(nil), body...)
			clear(body)
		}
	}
	return spec, nil
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
