// Migration 069 — CRD-mirror v2 models, hand-authored sqlc shim.
//
// Mirrors what `sqlc generate` would emit for the five new tables
// introduced in internal/db/migrations/069_crd_mirror_v2.up.sql. Kept
// in this dedicated file (rather than appended to models.go) so a
// future canonical `sqlc generate` run does not clobber the manual
// additions — sqlc's output target is models.go, the rest of the
// hand-written shims live in *_models.go / *_ext.sql.go files.

package sqlc

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// MirroredIngressClass is one row in mirrored_ingress_classes: a
// cluster-scoped IngressClass discovered by the per-cluster CRD-mirror
// agent (sprint 069). controller is the IngressClass.spec.controller
// string; is_default mirrors the well-known
// ingressclass.kubernetes.io/is-default-class=true annotation so the
// dashboard can put a single-source badge on the row without parsing
// annotations on every render.
type MirroredIngressClass struct {
	ID          uuid.UUID       `json:"id"`
	ClusterID   uuid.UUID       `json:"cluster_id"`
	Name        string          `json:"name"`
	Controller  string          `json:"controller"`
	Parameters  json.RawMessage `json:"parameters"`
	IsDefault   bool            `json:"is_default"`
	Labels      json.RawMessage `json:"labels"`
	Annotations json.RawMessage `json:"annotations"`
	LastSeenAt  time.Time       `json:"last_seen_at"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// MirroredGatewayClass is one row in mirrored_gateway_classes: a
// cluster-scoped GatewayClass. accepted_status carries the value of
// the Accepted condition on .status.conditions ("True" / "False" /
// "Unknown" / "" when unset) — most of the UI signal a gateway-class
// row needs is "is this class actually usable", so we resolve the
// condition at ingest time rather than re-walking conditions on each
// list call.
type MirroredGatewayClass struct {
	ID             uuid.UUID       `json:"id"`
	ClusterID      uuid.UUID       `json:"cluster_id"`
	Name           string          `json:"name"`
	ControllerName string          `json:"controller_name"`
	Description    string          `json:"description"`
	Parameters     json.RawMessage `json:"parameters"`
	AcceptedStatus string          `json:"accepted_status"`
	Labels         json.RawMessage `json:"labels"`
	Annotations    json.RawMessage `json:"annotations"`
	LastSeenAt     time.Time       `json:"last_seen_at"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

// MirroredNetworkPolicy is one row in mirrored_network_policies. The
// JSONB columns carry the raw spec sub-objects so the UI can render
// arbitrary rule shapes without a schema bump per new field; is_managed
// is computed at ingest time from the
// app.kubernetes.io/managed-by=astronomer label so the dashboard can
// disambiguate astronomer-owned netpols from operator-created ones
// without re-deriving the rule on every read.
type MirroredNetworkPolicy struct {
	ID           uuid.UUID       `json:"id"`
	ClusterID    uuid.UUID       `json:"cluster_id"`
	Namespace    string          `json:"namespace"`
	Name         string          `json:"name"`
	PodSelector  json.RawMessage `json:"pod_selector"`
	PolicyTypes  json.RawMessage `json:"policy_types"`
	IngressRules json.RawMessage `json:"ingress_rules"`
	EgressRules  json.RawMessage `json:"egress_rules"`
	Labels       json.RawMessage `json:"labels"`
	Annotations  json.RawMessage `json:"annotations"`
	IsManaged    bool            `json:"is_managed"`
	LastSeenAt   time.Time       `json:"last_seen_at"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

// MirroredResourceQuota mirrors one v1 ResourceQuota row. hard is
// spec.hard; used is status.used — both opaque JSONB so we don't have
// to bump the schema each time a new quota key (count/foo,
// requests.bar, …) ships in upstream Kubernetes.
type MirroredResourceQuota struct {
	ID          uuid.UUID       `json:"id"`
	ClusterID   uuid.UUID       `json:"cluster_id"`
	Namespace   string          `json:"namespace"`
	Name        string          `json:"name"`
	Hard        json.RawMessage `json:"hard"`
	Used        json.RawMessage `json:"used"`
	Scopes      json.RawMessage `json:"scopes"`
	Labels      json.RawMessage `json:"labels"`
	Annotations json.RawMessage `json:"annotations"`
	LastSeenAt  time.Time       `json:"last_seen_at"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// MirroredLimitRange mirrors one v1 LimitRange row. limits is the raw
// spec.limits array (ordered, with per-entry type + default + max + …).
type MirroredLimitRange struct {
	ID          uuid.UUID       `json:"id"`
	ClusterID   uuid.UUID       `json:"cluster_id"`
	Namespace   string          `json:"namespace"`
	Name        string          `json:"name"`
	Limits      json.RawMessage `json:"limits"`
	Labels      json.RawMessage `json:"labels"`
	Annotations json.RawMessage `json:"annotations"`
	LastSeenAt  time.Time       `json:"last_seen_at"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}
