// Sprint 069 — CRD-mirror v2 ingest.
//
// The per-cluster CRD-mirror agent streams events (Add/Update/Delete) for
// five Kubernetes resources back to the management plane. These ingest
// functions translate the unstructured.Unstructured wire payload into
// the corresponding sqlc upsert and bump the prometheus counters.
//
// Why unstructured rather than typed: GatewayClass lives in
// sigs.k8s.io/gateway-api which is NOT currently a direct dependency of
// astronomer-go (we only depend on k8s.io/api for core types). Going
// unstructured lets us mirror all five GVKs through one decode path and
// keeps the agent free to evolve its wire format without dragging a
// gateway-api version bump into the management plane. The four
// core/networking GVKs could equally well be ingested typed; we keep
// them uniform with GatewayClass for code-shape reasons.
//
// All five functions are idempotent. The DB layer enforces idempotency
// via INSERT ... ON CONFLICT DO UPDATE on the natural key — calling
// Ingest* 100 times in a row with the same payload changes nothing but
// updated_at and last_seen_at. created_at is preserved by the upsert
// path (it's only set on insert, never refreshed).

package crd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// MirrorQuerier is the narrow DB surface the ingest_v2 path needs.
// Defined locally so unit tests can stand up a tiny fake without
// pulling in all of *sqlc.Queries — same pattern as the
// ClusterSnapshotQuerier interface used by the snapshot handler.
type MirrorQuerier interface {
	UpsertMirroredIngressClass(ctx context.Context, arg sqlc.UpsertMirroredIngressClassParams) (sqlc.MirroredIngressClass, error)
	DeleteMirroredIngressClass(ctx context.Context, arg sqlc.DeleteMirroredIngressClassParams) error
	PruneStaleMirroredIngressClasses(ctx context.Context, before time.Time) (int64, error)

	UpsertMirroredGatewayClass(ctx context.Context, arg sqlc.UpsertMirroredGatewayClassParams) (sqlc.MirroredGatewayClass, error)
	DeleteMirroredGatewayClass(ctx context.Context, arg sqlc.DeleteMirroredGatewayClassParams) error
	PruneStaleMirroredGatewayClasses(ctx context.Context, before time.Time) (int64, error)

	UpsertMirroredNetworkPolicy(ctx context.Context, arg sqlc.UpsertMirroredNetworkPolicyParams) (sqlc.MirroredNetworkPolicy, error)
	DeleteMirroredNetworkPolicy(ctx context.Context, arg sqlc.DeleteMirroredNetworkPolicyParams) error
	PruneStaleMirroredNetworkPolicies(ctx context.Context, before time.Time) (int64, error)

	UpsertMirroredResourceQuota(ctx context.Context, arg sqlc.UpsertMirroredResourceQuotaParams) (sqlc.MirroredResourceQuota, error)
	DeleteMirroredResourceQuota(ctx context.Context, arg sqlc.DeleteMirroredResourceQuotaParams) error
	PruneStaleMirroredResourceQuotas(ctx context.Context, before time.Time) (int64, error)

	UpsertMirroredLimitRange(ctx context.Context, arg sqlc.UpsertMirroredLimitRangeParams) (sqlc.MirroredLimitRange, error)
	DeleteMirroredLimitRange(ctx context.Context, arg sqlc.DeleteMirroredLimitRangeParams) error
	PruneStaleMirroredLimitRanges(ctx context.Context, before time.Time) (int64, error)
}

// Kind names used in the metric label set. Kept as string consts (not the
// GroupVersionKind shape) so the cardinality of the {kind=…} label is
// fixed at compile time.
const (
	KindIngressClass   = "IngressClass"
	KindGatewayClass   = "GatewayClass"
	KindNetworkPolicy  = "NetworkPolicy"
	KindResourceQuota  = "ResourceQuota"
	KindLimitRange     = "LimitRange"
)

// MirrorKinds is the fixed list of kinds the v2 path mirrors. Useful
// callers: the prune scheduler (loops over the set), unit tests (table-
// driven), and the metric init that registers per-kind label values.
var MirrorKinds = []string{
	KindIngressClass,
	KindGatewayClass,
	KindNetworkPolicy,
	KindResourceQuota,
	KindLimitRange,
}

// Annotation keys / label keys recognised by the ingest path.
const (
	// IngressClassDefaultAnnotation is the well-known annotation that
	// marks a single IngressClass as the cluster default. The boolean
	// resolution at ingest time is a UI convenience — the row's
	// is_default column saves a per-render annotation parse.
	IngressClassDefaultAnnotation = "ingressclass.kubernetes.io/is-default-class"

	// ManagedByLabel is the canonical app.kubernetes.io label key.
	// is_managed on mirrored_network_policies is true when this label
	// has the value "astronomer".
	ManagedByLabel    = "app.kubernetes.io/managed-by"
	ManagedByAstronomer = "astronomer"
)

// Metrics. Counter labels are kept fixed-cardinality (the five mirror
// kinds + outcome ∈ {ok, error, delete}); the gauge tracks per-cluster
// row counts and accepts the cluster name as a label.
var (
	IngestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "astronomer",
		Subsystem: "crd_mirror",
		Name:      "ingests_total",
		Help:      "Number of v2 mirror-ingest events, partitioned by kind and outcome.",
	}, []string{"kind", "outcome"})

	PruneTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "astronomer",
		Subsystem: "crd_mirror",
		Name:      "prune_total",
		Help:      "Number of stale rows pruned by the periodic v2 mirror prune sweep, by kind.",
	}, []string{"kind"})

	Rows = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "astronomer",
		Subsystem: "crd_mirror",
		Name:      "rows",
		Help:      "Current row count per kind/cluster in the v2 mirror tables.",
	}, []string{"kind", "cluster"})
)

// metricsRegistered guards against double-registration when both the
// server and a unit test ConfigureRuntime in the same process. The init
// function is unconditional — every linkage of this package gets the
// metrics for free; sub-package init order doesn't matter.
var metricsRegistered = false

func init() {
	// The MustRegister call is safe under -race because init runs once.
	// We avoid prometheus.DefaultRegisterer.Register's nil-error swallow
	// by using MustRegister; a duplicate registration in a test harness
	// is a programmer error worth panicking on.
	if !metricsRegistered {
		prometheus.MustRegister(IngestsTotal, PruneTotal, Rows)
		metricsRegistered = true
	}
}

// ---------------------------------------------------------------------
// Helpers — labels, annotations, conditions
// ---------------------------------------------------------------------

// stringMapToJSON encodes a (possibly nil) labels/annotations map as
// JSONB-compatible bytes. Nil input deliberately returns the empty-
// object `{}` rather than `null` — the schema column has NOT NULL
// DEFAULT '{}', so we want the same shape on the round-trip.
func stringMapToJSON(in map[string]string) (json.RawMessage, error) {
	if in == nil {
		return json.RawMessage("{}"), nil
	}
	return json.Marshal(in)
}

// objectMeta returns the standard meta fields from an unstructured
// object. The caller is expected to have already validated that the
// object is the right kind.
func objectMeta(obj *unstructured.Unstructured) (name, namespace string, labels, annotations map[string]string) {
	name = obj.GetName()
	namespace = obj.GetNamespace()
	labels = obj.GetLabels()
	annotations = obj.GetAnnotations()
	return
}

// IsDefaultIngressClass returns true when the well-known is-default
// annotation is present and parses to true. The k8s docs spec the
// value as the string "true" — anything else, including "True" or
// "1", is treated as not-default for parity with the in-tree
// behaviour.
func IsDefaultIngressClass(annotations map[string]string) bool {
	return annotations[IngressClassDefaultAnnotation] == "true"
}

// IsManagedNetworkPolicy returns true when the standard managed-by
// label has the value "astronomer". Sprint 068 owns the writes that
// produce that label; this sprint just observes it.
func IsManagedNetworkPolicy(labels map[string]string) bool {
	return labels[ManagedByLabel] == ManagedByAstronomer
}

// AcceptedConditionStatus walks a GatewayClass-shaped .status.conditions
// list (carried as []any in unstructured) and returns the Status value
// of the Accepted condition. Empty string when no Accepted condition is
// present — the UI treats that as "Unknown" without making us bake the
// string in here.
func AcceptedConditionStatus(obj *unstructured.Unstructured) string {
	conds, found, err := unstructured.NestedSlice(obj.Object, "status", "conditions")
	if err != nil || !found {
		return ""
	}
	for _, c := range conds {
		m, ok := c.(map[string]any)
		if !ok {
			continue
		}
		t, _ := m["type"].(string)
		if t != "Accepted" {
			continue
		}
		s, _ := m["status"].(string)
		return s
	}
	return ""
}

// ---------------------------------------------------------------------
// IngressClass
// ---------------------------------------------------------------------

// IngestIngressClass upserts one IngressClass row. obj is the raw
// unstructured payload from the agent; the function extracts
// spec.controller, spec.parameters (left as the raw nested object so
// the UI gets everything Kubernetes stores), the standard label/
// annotation maps, and the resolved is_default flag.
func IngestIngressClass(ctx context.Context, q MirrorQuerier, clusterID uuid.UUID, obj *unstructured.Unstructured) (sqlc.MirroredIngressClass, error) {
	if obj == nil {
		IngestsTotal.WithLabelValues(KindIngressClass, "error").Inc()
		return sqlc.MirroredIngressClass{}, fmt.Errorf("ingest IngressClass: nil object")
	}
	name, _, labels, annotations := objectMeta(obj)
	if name == "" {
		IngestsTotal.WithLabelValues(KindIngressClass, "error").Inc()
		return sqlc.MirroredIngressClass{}, fmt.Errorf("ingest IngressClass: empty metadata.name")
	}

	controller, _, _ := unstructured.NestedString(obj.Object, "spec", "controller")

	// spec.parameters is the optional ParametersReference object (apiGroup
	// / kind / name / scope / namespace). We persist it verbatim so the
	// UI can render whatever a future field addition includes without a
	// schema bump.
	var paramsJSON json.RawMessage
	if params, found, _ := unstructured.NestedMap(obj.Object, "spec", "parameters"); found && params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			IngestsTotal.WithLabelValues(KindIngressClass, "error").Inc()
			return sqlc.MirroredIngressClass{}, fmt.Errorf("ingest IngressClass: marshal parameters: %w", err)
		}
		paramsJSON = b
	} else {
		paramsJSON = json.RawMessage("{}")
	}

	labelsJSON, err := stringMapToJSON(labels)
	if err != nil {
		IngestsTotal.WithLabelValues(KindIngressClass, "error").Inc()
		return sqlc.MirroredIngressClass{}, fmt.Errorf("ingest IngressClass: marshal labels: %w", err)
	}
	annsJSON, err := stringMapToJSON(annotations)
	if err != nil {
		IngestsTotal.WithLabelValues(KindIngressClass, "error").Inc()
		return sqlc.MirroredIngressClass{}, fmt.Errorf("ingest IngressClass: marshal annotations: %w", err)
	}

	row, err := q.UpsertMirroredIngressClass(ctx, sqlc.UpsertMirroredIngressClassParams{
		ClusterID:   clusterID,
		Name:        name,
		Controller:  controller,
		Parameters:  paramsJSON,
		IsDefault:   IsDefaultIngressClass(annotations),
		Labels:      labelsJSON,
		Annotations: annsJSON,
	})
	if err != nil {
		IngestsTotal.WithLabelValues(KindIngressClass, "error").Inc()
		return sqlc.MirroredIngressClass{}, fmt.Errorf("ingest IngressClass %q: %w", name, err)
	}
	IngestsTotal.WithLabelValues(KindIngressClass, "ok").Inc()
	return row, nil
}

// ---------------------------------------------------------------------
// GatewayClass (gateway.networking.k8s.io)
// ---------------------------------------------------------------------

// IngestGatewayClass upserts one GatewayClass row. The Accepted
// condition status is resolved at ingest time so the dashboard's "is
// this class usable" badge doesn't have to re-walk conditions on every
// list call.
func IngestGatewayClass(ctx context.Context, q MirrorQuerier, clusterID uuid.UUID, obj *unstructured.Unstructured) (sqlc.MirroredGatewayClass, error) {
	if obj == nil {
		IngestsTotal.WithLabelValues(KindGatewayClass, "error").Inc()
		return sqlc.MirroredGatewayClass{}, fmt.Errorf("ingest GatewayClass: nil object")
	}
	name, _, labels, annotations := objectMeta(obj)
	if name == "" {
		IngestsTotal.WithLabelValues(KindGatewayClass, "error").Inc()
		return sqlc.MirroredGatewayClass{}, fmt.Errorf("ingest GatewayClass: empty metadata.name")
	}

	controllerName, _, _ := unstructured.NestedString(obj.Object, "spec", "controllerName")
	description, _, _ := unstructured.NestedString(obj.Object, "spec", "description")

	var paramsJSON json.RawMessage
	if params, found, _ := unstructured.NestedMap(obj.Object, "spec", "parametersRef"); found && params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			IngestsTotal.WithLabelValues(KindGatewayClass, "error").Inc()
			return sqlc.MirroredGatewayClass{}, fmt.Errorf("ingest GatewayClass: marshal parametersRef: %w", err)
		}
		paramsJSON = b
	} else {
		paramsJSON = json.RawMessage("{}")
	}

	labelsJSON, err := stringMapToJSON(labels)
	if err != nil {
		IngestsTotal.WithLabelValues(KindGatewayClass, "error").Inc()
		return sqlc.MirroredGatewayClass{}, fmt.Errorf("ingest GatewayClass: marshal labels: %w", err)
	}
	annsJSON, err := stringMapToJSON(annotations)
	if err != nil {
		IngestsTotal.WithLabelValues(KindGatewayClass, "error").Inc()
		return sqlc.MirroredGatewayClass{}, fmt.Errorf("ingest GatewayClass: marshal annotations: %w", err)
	}

	row, err := q.UpsertMirroredGatewayClass(ctx, sqlc.UpsertMirroredGatewayClassParams{
		ClusterID:      clusterID,
		Name:           name,
		ControllerName: controllerName,
		Description:    description,
		Parameters:     paramsJSON,
		AcceptedStatus: AcceptedConditionStatus(obj),
		Labels:         labelsJSON,
		Annotations:    annsJSON,
	})
	if err != nil {
		IngestsTotal.WithLabelValues(KindGatewayClass, "error").Inc()
		return sqlc.MirroredGatewayClass{}, fmt.Errorf("ingest GatewayClass %q: %w", name, err)
	}
	IngestsTotal.WithLabelValues(KindGatewayClass, "ok").Inc()
	return row, nil
}

// ---------------------------------------------------------------------
// NetworkPolicy
// ---------------------------------------------------------------------

// IngestNetworkPolicy upserts one NetworkPolicy row. policy_types is
// captured as the spec.policyTypes string slice; pod_selector +
// ingress + egress rules are kept as the verbatim nested objects.
// is_managed is computed from the standard managed-by label so the UI
// can disambiguate astronomer-owned netpols from operator-created ones.
func IngestNetworkPolicy(ctx context.Context, q MirrorQuerier, clusterID uuid.UUID, obj *unstructured.Unstructured) (sqlc.MirroredNetworkPolicy, error) {
	if obj == nil {
		IngestsTotal.WithLabelValues(KindNetworkPolicy, "error").Inc()
		return sqlc.MirroredNetworkPolicy{}, fmt.Errorf("ingest NetworkPolicy: nil object")
	}
	name, namespace, labels, annotations := objectMeta(obj)
	if name == "" || namespace == "" {
		IngestsTotal.WithLabelValues(KindNetworkPolicy, "error").Inc()
		return sqlc.MirroredNetworkPolicy{}, fmt.Errorf("ingest NetworkPolicy: name=%q namespace=%q (both required)", name, namespace)
	}

	podSelectorJSON := json.RawMessage("{}")
	if sel, found, _ := unstructured.NestedMap(obj.Object, "spec", "podSelector"); found && sel != nil {
		b, err := json.Marshal(sel)
		if err != nil {
			IngestsTotal.WithLabelValues(KindNetworkPolicy, "error").Inc()
			return sqlc.MirroredNetworkPolicy{}, fmt.Errorf("ingest NetworkPolicy: marshal podSelector: %w", err)
		}
		podSelectorJSON = b
	}

	policyTypesJSON := json.RawMessage("[]")
	if pts, found, _ := unstructured.NestedStringSlice(obj.Object, "spec", "policyTypes"); found {
		b, err := json.Marshal(pts)
		if err != nil {
			IngestsTotal.WithLabelValues(KindNetworkPolicy, "error").Inc()
			return sqlc.MirroredNetworkPolicy{}, fmt.Errorf("ingest NetworkPolicy: marshal policyTypes: %w", err)
		}
		policyTypesJSON = b
	}

	ingressJSON := json.RawMessage("[]")
	if rules, found, _ := unstructured.NestedSlice(obj.Object, "spec", "ingress"); found {
		b, err := json.Marshal(rules)
		if err != nil {
			IngestsTotal.WithLabelValues(KindNetworkPolicy, "error").Inc()
			return sqlc.MirroredNetworkPolicy{}, fmt.Errorf("ingest NetworkPolicy: marshal ingress: %w", err)
		}
		ingressJSON = b
	}

	egressJSON := json.RawMessage("[]")
	if rules, found, _ := unstructured.NestedSlice(obj.Object, "spec", "egress"); found {
		b, err := json.Marshal(rules)
		if err != nil {
			IngestsTotal.WithLabelValues(KindNetworkPolicy, "error").Inc()
			return sqlc.MirroredNetworkPolicy{}, fmt.Errorf("ingest NetworkPolicy: marshal egress: %w", err)
		}
		egressJSON = b
	}

	labelsJSON, err := stringMapToJSON(labels)
	if err != nil {
		IngestsTotal.WithLabelValues(KindNetworkPolicy, "error").Inc()
		return sqlc.MirroredNetworkPolicy{}, fmt.Errorf("ingest NetworkPolicy: marshal labels: %w", err)
	}
	annsJSON, err := stringMapToJSON(annotations)
	if err != nil {
		IngestsTotal.WithLabelValues(KindNetworkPolicy, "error").Inc()
		return sqlc.MirroredNetworkPolicy{}, fmt.Errorf("ingest NetworkPolicy: marshal annotations: %w", err)
	}

	row, err := q.UpsertMirroredNetworkPolicy(ctx, sqlc.UpsertMirroredNetworkPolicyParams{
		ClusterID:    clusterID,
		Namespace:    namespace,
		Name:         name,
		PodSelector:  podSelectorJSON,
		PolicyTypes:  policyTypesJSON,
		IngressRules: ingressJSON,
		EgressRules:  egressJSON,
		Labels:       labelsJSON,
		Annotations:  annsJSON,
		IsManaged:    IsManagedNetworkPolicy(labels),
	})
	if err != nil {
		IngestsTotal.WithLabelValues(KindNetworkPolicy, "error").Inc()
		return sqlc.MirroredNetworkPolicy{}, fmt.Errorf("ingest NetworkPolicy %s/%s: %w", namespace, name, err)
	}
	IngestsTotal.WithLabelValues(KindNetworkPolicy, "ok").Inc()
	return row, nil
}

// ---------------------------------------------------------------------
// ResourceQuota
// ---------------------------------------------------------------------

// IngestResourceQuota upserts one ResourceQuota row. spec.hard / status.used
// / spec.scopes are persisted verbatim so the UI can render whatever
// quota keys upstream Kubernetes carries (cpu, memory, count/configmaps,
// requests.nvidia.com/gpu, …) without a schema bump per key.
func IngestResourceQuota(ctx context.Context, q MirrorQuerier, clusterID uuid.UUID, obj *unstructured.Unstructured) (sqlc.MirroredResourceQuota, error) {
	if obj == nil {
		IngestsTotal.WithLabelValues(KindResourceQuota, "error").Inc()
		return sqlc.MirroredResourceQuota{}, fmt.Errorf("ingest ResourceQuota: nil object")
	}
	name, namespace, labels, annotations := objectMeta(obj)
	if name == "" || namespace == "" {
		IngestsTotal.WithLabelValues(KindResourceQuota, "error").Inc()
		return sqlc.MirroredResourceQuota{}, fmt.Errorf("ingest ResourceQuota: name=%q namespace=%q (both required)", name, namespace)
	}

	hardJSON := json.RawMessage("{}")
	if h, found, _ := unstructured.NestedMap(obj.Object, "spec", "hard"); found && h != nil {
		b, err := json.Marshal(h)
		if err != nil {
			IngestsTotal.WithLabelValues(KindResourceQuota, "error").Inc()
			return sqlc.MirroredResourceQuota{}, fmt.Errorf("ingest ResourceQuota: marshal spec.hard: %w", err)
		}
		hardJSON = b
	}

	usedJSON := json.RawMessage("{}")
	if u, found, _ := unstructured.NestedMap(obj.Object, "status", "used"); found && u != nil {
		b, err := json.Marshal(u)
		if err != nil {
			IngestsTotal.WithLabelValues(KindResourceQuota, "error").Inc()
			return sqlc.MirroredResourceQuota{}, fmt.Errorf("ingest ResourceQuota: marshal status.used: %w", err)
		}
		usedJSON = b
	}

	scopesJSON := json.RawMessage("[]")
	if s, found, _ := unstructured.NestedStringSlice(obj.Object, "spec", "scopes"); found {
		b, err := json.Marshal(s)
		if err != nil {
			IngestsTotal.WithLabelValues(KindResourceQuota, "error").Inc()
			return sqlc.MirroredResourceQuota{}, fmt.Errorf("ingest ResourceQuota: marshal spec.scopes: %w", err)
		}
		scopesJSON = b
	}

	labelsJSON, err := stringMapToJSON(labels)
	if err != nil {
		IngestsTotal.WithLabelValues(KindResourceQuota, "error").Inc()
		return sqlc.MirroredResourceQuota{}, fmt.Errorf("ingest ResourceQuota: marshal labels: %w", err)
	}
	annsJSON, err := stringMapToJSON(annotations)
	if err != nil {
		IngestsTotal.WithLabelValues(KindResourceQuota, "error").Inc()
		return sqlc.MirroredResourceQuota{}, fmt.Errorf("ingest ResourceQuota: marshal annotations: %w", err)
	}

	row, err := q.UpsertMirroredResourceQuota(ctx, sqlc.UpsertMirroredResourceQuotaParams{
		ClusterID:   clusterID,
		Namespace:   namespace,
		Name:        name,
		Hard:        hardJSON,
		Used:        usedJSON,
		Scopes:      scopesJSON,
		Labels:      labelsJSON,
		Annotations: annsJSON,
	})
	if err != nil {
		IngestsTotal.WithLabelValues(KindResourceQuota, "error").Inc()
		return sqlc.MirroredResourceQuota{}, fmt.Errorf("ingest ResourceQuota %s/%s: %w", namespace, name, err)
	}
	IngestsTotal.WithLabelValues(KindResourceQuota, "ok").Inc()
	return row, nil
}

// ---------------------------------------------------------------------
// LimitRange
// ---------------------------------------------------------------------

// IngestLimitRange upserts one LimitRange row. spec.limits is the
// ordered array of per-type default/max entries; we persist it
// verbatim so the UI can render all five LimitType variants (Container,
// Pod, PersistentVolumeClaim, …) without re-walking nested defaults.
func IngestLimitRange(ctx context.Context, q MirrorQuerier, clusterID uuid.UUID, obj *unstructured.Unstructured) (sqlc.MirroredLimitRange, error) {
	if obj == nil {
		IngestsTotal.WithLabelValues(KindLimitRange, "error").Inc()
		return sqlc.MirroredLimitRange{}, fmt.Errorf("ingest LimitRange: nil object")
	}
	name, namespace, labels, annotations := objectMeta(obj)
	if name == "" || namespace == "" {
		IngestsTotal.WithLabelValues(KindLimitRange, "error").Inc()
		return sqlc.MirroredLimitRange{}, fmt.Errorf("ingest LimitRange: name=%q namespace=%q (both required)", name, namespace)
	}

	limitsJSON := json.RawMessage("[]")
	if limits, found, _ := unstructured.NestedSlice(obj.Object, "spec", "limits"); found {
		b, err := json.Marshal(limits)
		if err != nil {
			IngestsTotal.WithLabelValues(KindLimitRange, "error").Inc()
			return sqlc.MirroredLimitRange{}, fmt.Errorf("ingest LimitRange: marshal spec.limits: %w", err)
		}
		limitsJSON = b
	}

	labelsJSON, err := stringMapToJSON(labels)
	if err != nil {
		IngestsTotal.WithLabelValues(KindLimitRange, "error").Inc()
		return sqlc.MirroredLimitRange{}, fmt.Errorf("ingest LimitRange: marshal labels: %w", err)
	}
	annsJSON, err := stringMapToJSON(annotations)
	if err != nil {
		IngestsTotal.WithLabelValues(KindLimitRange, "error").Inc()
		return sqlc.MirroredLimitRange{}, fmt.Errorf("ingest LimitRange: marshal annotations: %w", err)
	}

	row, err := q.UpsertMirroredLimitRange(ctx, sqlc.UpsertMirroredLimitRangeParams{
		ClusterID:   clusterID,
		Namespace:   namespace,
		Name:        name,
		Limits:      limitsJSON,
		Labels:      labelsJSON,
		Annotations: annsJSON,
	})
	if err != nil {
		IngestsTotal.WithLabelValues(KindLimitRange, "error").Inc()
		return sqlc.MirroredLimitRange{}, fmt.Errorf("ingest LimitRange %s/%s: %w", namespace, name, err)
	}
	IngestsTotal.WithLabelValues(KindLimitRange, "ok").Inc()
	return row, nil
}

// ---------------------------------------------------------------------
// Delete + typed-object → unstructured helpers
// ---------------------------------------------------------------------

// UnstructuredFromNetworkPolicy converts a typed NetworkPolicy into the
// unstructured shape the ingest path consumes. Useful in tests and in
// the controller-runtime watcher when the cache hands back typed objects.
func UnstructuredFromNetworkPolicy(np *networkingv1.NetworkPolicy) (*unstructured.Unstructured, error) {
	if np == nil {
		return nil, fmt.Errorf("nil NetworkPolicy")
	}
	m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(np)
	if err != nil {
		return nil, err
	}
	return &unstructured.Unstructured{Object: m}, nil
}

// UnstructuredFromResourceQuota mirrors UnstructuredFromNetworkPolicy for
// the v1.ResourceQuota typed shape.
func UnstructuredFromResourceQuota(rq *corev1.ResourceQuota) (*unstructured.Unstructured, error) {
	if rq == nil {
		return nil, fmt.Errorf("nil ResourceQuota")
	}
	m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(rq)
	if err != nil {
		return nil, err
	}
	return &unstructured.Unstructured{Object: m}, nil
}

// UnstructuredFromLimitRange mirrors UnstructuredFromNetworkPolicy for
// v1.LimitRange.
func UnstructuredFromLimitRange(lr *corev1.LimitRange) (*unstructured.Unstructured, error) {
	if lr == nil {
		return nil, fmt.Errorf("nil LimitRange")
	}
	m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(lr)
	if err != nil {
		return nil, err
	}
	return &unstructured.Unstructured{Object: m}, nil
}

// UnstructuredFromIngressClass mirrors UnstructuredFromNetworkPolicy for
// networkingv1.IngressClass.
func UnstructuredFromIngressClass(ic *networkingv1.IngressClass) (*unstructured.Unstructured, error) {
	if ic == nil {
		return nil, fmt.Errorf("nil IngressClass")
	}
	m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(ic)
	if err != nil {
		return nil, err
	}
	return &unstructured.Unstructured{Object: m}, nil
}

// ---------------------------------------------------------------------
// Delete dispatch
// ---------------------------------------------------------------------

// DeleteEvent is the wire shape the agent uses for k8s-side delete
// events. The kind label is one of the MirrorKinds constants; the
// natural key (cluster_id + name + namespace) is enough to route to
// the right DB delete.
type DeleteEvent struct {
	Kind      string
	Name      string
	Namespace string
}

// HandleDelete routes a delete event to the right table-level delete.
// Unknown kinds are returned as errors so the agent-side caller can
// log + drop, rather than silently swallowing typos.
func HandleDelete(ctx context.Context, q MirrorQuerier, clusterID uuid.UUID, ev DeleteEvent) error {
	switch ev.Kind {
	case KindIngressClass:
		if err := q.DeleteMirroredIngressClass(ctx, sqlc.DeleteMirroredIngressClassParams{
			ClusterID: clusterID, Name: ev.Name,
		}); err != nil {
			IngestsTotal.WithLabelValues(KindIngressClass, "error").Inc()
			return err
		}
		IngestsTotal.WithLabelValues(KindIngressClass, "delete").Inc()
		return nil
	case KindGatewayClass:
		if err := q.DeleteMirroredGatewayClass(ctx, sqlc.DeleteMirroredGatewayClassParams{
			ClusterID: clusterID, Name: ev.Name,
		}); err != nil {
			IngestsTotal.WithLabelValues(KindGatewayClass, "error").Inc()
			return err
		}
		IngestsTotal.WithLabelValues(KindGatewayClass, "delete").Inc()
		return nil
	case KindNetworkPolicy:
		if err := q.DeleteMirroredNetworkPolicy(ctx, sqlc.DeleteMirroredNetworkPolicyParams{
			ClusterID: clusterID, Namespace: ev.Namespace, Name: ev.Name,
		}); err != nil {
			IngestsTotal.WithLabelValues(KindNetworkPolicy, "error").Inc()
			return err
		}
		IngestsTotal.WithLabelValues(KindNetworkPolicy, "delete").Inc()
		return nil
	case KindResourceQuota:
		if err := q.DeleteMirroredResourceQuota(ctx, sqlc.DeleteMirroredResourceQuotaParams{
			ClusterID: clusterID, Namespace: ev.Namespace, Name: ev.Name,
		}); err != nil {
			IngestsTotal.WithLabelValues(KindResourceQuota, "error").Inc()
			return err
		}
		IngestsTotal.WithLabelValues(KindResourceQuota, "delete").Inc()
		return nil
	case KindLimitRange:
		if err := q.DeleteMirroredLimitRange(ctx, sqlc.DeleteMirroredLimitRangeParams{
			ClusterID: clusterID, Namespace: ev.Namespace, Name: ev.Name,
		}); err != nil {
			IngestsTotal.WithLabelValues(KindLimitRange, "error").Inc()
			return err
		}
		IngestsTotal.WithLabelValues(KindLimitRange, "delete").Inc()
		return nil
	default:
		return fmt.Errorf("unknown mirror kind %q (allowed: %s)", ev.Kind, strings.Join(MirrorKinds, ", "))
	}
}

// ---------------------------------------------------------------------
// Prune
// ---------------------------------------------------------------------

// PruneStaleAll runs the prune for every mirror table. before is the
// cutoff timestamp — any row whose last_seen_at is strictly older is
// dropped. Returns the per-kind row count map and the first error
// encountered (subsequent kinds still run, so a single failing kind
// doesn't starve the others).
func PruneStaleAll(ctx context.Context, q MirrorQuerier, before time.Time) (map[string]int64, error) {
	out := map[string]int64{}
	var firstErr error

	if n, err := q.PruneStaleMirroredIngressClasses(ctx, before); err != nil {
		if firstErr == nil {
			firstErr = fmt.Errorf("prune IngressClass: %w", err)
		}
	} else {
		out[KindIngressClass] = n
		PruneTotal.WithLabelValues(KindIngressClass).Add(float64(n))
	}

	if n, err := q.PruneStaleMirroredGatewayClasses(ctx, before); err != nil {
		if firstErr == nil {
			firstErr = fmt.Errorf("prune GatewayClass: %w", err)
		}
	} else {
		out[KindGatewayClass] = n
		PruneTotal.WithLabelValues(KindGatewayClass).Add(float64(n))
	}

	if n, err := q.PruneStaleMirroredNetworkPolicies(ctx, before); err != nil {
		if firstErr == nil {
			firstErr = fmt.Errorf("prune NetworkPolicy: %w", err)
		}
	} else {
		out[KindNetworkPolicy] = n
		PruneTotal.WithLabelValues(KindNetworkPolicy).Add(float64(n))
	}

	if n, err := q.PruneStaleMirroredResourceQuotas(ctx, before); err != nil {
		if firstErr == nil {
			firstErr = fmt.Errorf("prune ResourceQuota: %w", err)
		}
	} else {
		out[KindResourceQuota] = n
		PruneTotal.WithLabelValues(KindResourceQuota).Add(float64(n))
	}

	if n, err := q.PruneStaleMirroredLimitRanges(ctx, before); err != nil {
		if firstErr == nil {
			firstErr = fmt.Errorf("prune LimitRange: %w", err)
		}
	} else {
		out[KindLimitRange] = n
		PruneTotal.WithLabelValues(KindLimitRange).Add(float64(n))
	}

	return out, firstErr
}

// StaleRetention is the per-row "if I haven't seen this in T, it's gone"
// window. The agent re-sends every object on reconnect (full resync),
// so a row that hasn't been touched in StaleRetention is unambiguously
// no longer in the cluster. Kept at 1h per spec.
const StaleRetention = time.Hour
