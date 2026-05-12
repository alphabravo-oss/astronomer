// Migration 057 — maintenance window + deferred operations models,
// hand-authored sqlc shim.
//
// Same pattern as cluster_snapshots_models.go: the two row types live in
// this dedicated file (rather than appended to models.go) so a future
// `sqlc generate` run against the canonical queries/migrations files
// does not clobber them — sqlc's output target is models.go, and the
// CLI is happy to leave unrelated files alone.

package sqlc

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// MaintenanceWindow mirrors one row in maintenance_windows: an operator-
// declared time window that gates destructive ops. Mode picks blackout
// (refuse during window) vs permitted (refuse outside window).
// cluster_selector + operation_types narrow the scope; on_block picks
// the 409 (refuse) vs 202 (defer) response. The DB columns map 1:1 to
// the JSON wire shape the handler emits.
type MaintenanceWindow struct {
	ID              uuid.UUID       `json:"id"`
	Name            string          `json:"name"`
	Description     string          `json:"description"`
	Mode            string          `json:"mode"`
	CronOpen        string          `json:"cron_open"`
	DurationMinutes int32           `json:"duration_minutes"`
	Timezone        string          `json:"timezone"`
	ClusterSelector json.RawMessage `json:"cluster_selector"`
	OperationTypes  json.RawMessage `json:"operation_types"`
	OnBlock         string          `json:"on_block"`
	Enabled         bool            `json:"enabled"`
	CreatedBy       pgtype.UUID     `json:"created_by"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

// DeferredOperation is the row inserted when a mutation handler decides
// to queue rather than refuse. The dispatcher worker drains rows whose
// deferred_until has elapsed and re-fires the original operation. Status
// transitions: pending → dispatched (success) | expired (TTL exceeded) |
// cancelled (operator-cancelled via DELETE).
type DeferredOperation struct {
	ID              uuid.UUID          `json:"id"`
	WindowID        uuid.UUID          `json:"window_id"`
	OperationType   string             `json:"operation_type"`
	OperationSpec   json.RawMessage    `json:"operation_spec"`
	TargetClusterID pgtype.UUID        `json:"target_cluster_id"`
	TargetProjectID pgtype.UUID        `json:"target_project_id"`
	Status          string             `json:"status"`
	DeferredUntil   pgtype.Timestamptz `json:"deferred_until"`
	ExpiresAt       pgtype.Timestamptz `json:"expires_at"`
	RequestedBy     pgtype.UUID        `json:"requested_by"`
	LastError       string             `json:"last_error"`
	DispatchedAt    pgtype.Timestamptz `json:"dispatched_at"`
	CreatedAt       time.Time          `json:"created_at"`
	UpdatedAt       time.Time          `json:"updated_at"`
}
