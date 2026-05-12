// Migration 068 — network policy templates + applications.
//
// Hand-authored sqlc shim (sqlc CLI was broken when this migration
// landed; the pattern matches the cluster_snapshots / cluster_registry
// hand-written shims in this directory). The table-row types mirror
// what `sqlc generate` would emit. Kept in a dedicated *_models.go file
// so a future regeneration doesn't clobber.

package sqlc

import (
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// NetworkPolicyTemplate mirrors one row in network_policy_templates. A
// template is a Go text/template-rendered NetworkPolicy YAML body
// indexed by a stable slug. Builtin rows are seeded by the migration
// and are read-only at the handler layer.
type NetworkPolicyTemplate struct {
	ID            uuid.UUID   `json:"id"`
	Slug          string      `json:"slug"`
	Name          string      `json:"name"`
	Description   string      `json:"description"`
	Kind          string      `json:"kind"`
	SpecTemplate  string      `json:"spec_template"`
	Enabled       bool        `json:"enabled"`
	CreatedBy     pgtype.UUID `json:"created_by"`
	CreatedAt     time.Time   `json:"created_at"`
	UpdatedAt     time.Time   `json:"updated_at"`
}

// NetworkPolicyApplication tracks "which template is applied to which
// (cluster, namespace), and how did the last apply go?". The reconciler
// walks pending/failed/drifting rows on a 5m tick and applies the
// rendered NetworkPolicy via the tunnel K8sRequester.
type NetworkPolicyApplication struct {
	ID            uuid.UUID          `json:"id"`
	TemplateID    uuid.UUID          `json:"template_id"`
	ClusterID     uuid.UUID          `json:"cluster_id"`
	Namespace     string             `json:"namespace"`
	PolicyName    string             `json:"policy_name"`
	Status        string             `json:"status"`
	LastAppliedAt pgtype.Timestamptz `json:"last_applied_at"`
	LastError     string             `json:"last_error"`
	AppliedBy     pgtype.UUID        `json:"applied_by"`
	CreatedAt     time.Time          `json:"created_at"`
	UpdatedAt     time.Time          `json:"updated_at"`
}
