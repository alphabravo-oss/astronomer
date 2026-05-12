// Sprint 069 — unit tests for the CRD-mirror v2 ingest layer.
//
// Each Ingest* function is exercised against a tiny in-memory
// MirrorQuerier fake that records the upsert params verbatim, so the
// tests assert both the wire decode (annotation → is_default,
// labels → is_managed, conditions → accepted_status) and the
// idempotency property (re-ingesting the same object preserves
// created_at and only refreshes updated_at).

package crd

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// fakeMirrorQuerier is a tiny in-memory MirrorQuerier. Upserts respect
// the natural-key idempotency: a second insert with the same
// (cluster_id, name [, namespace]) tuple bumps updated_at but
// preserves created_at, matching the DB-level ON CONFLICT ... DO UPDATE
// semantics.
type fakeMirrorQuerier struct {
	mu          sync.Mutex
	ic          map[string]sqlc.MirroredIngressClass
	gwc         map[string]sqlc.MirroredGatewayClass
	np          map[string]sqlc.MirroredNetworkPolicy
	rq          map[string]sqlc.MirroredResourceQuota
	lr          map[string]sqlc.MirroredLimitRange
	upsertCalls map[string]int
}

func newFakeMirrorQuerier() *fakeMirrorQuerier {
	return &fakeMirrorQuerier{
		ic:          map[string]sqlc.MirroredIngressClass{},
		gwc:         map[string]sqlc.MirroredGatewayClass{},
		np:          map[string]sqlc.MirroredNetworkPolicy{},
		rq:          map[string]sqlc.MirroredResourceQuota{},
		lr:          map[string]sqlc.MirroredLimitRange{},
		upsertCalls: map[string]int{},
	}
}

func icKey(cid uuid.UUID, name string) string { return cid.String() + "|" + name }
func nsKey(cid uuid.UUID, ns, name string) string {
	return cid.String() + "|" + ns + "|" + name
}

func (f *fakeMirrorQuerier) UpsertMirroredIngressClass(_ context.Context, arg sqlc.UpsertMirroredIngressClassParams) (sqlc.MirroredIngressClass, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := icKey(arg.ClusterID, arg.Name)
	now := time.Now()
	prev, exists := f.ic[k]
	row := sqlc.MirroredIngressClass{
		ID:          ifNew(prev.ID, uuid.New()),
		ClusterID:   arg.ClusterID,
		Name:        arg.Name,
		Controller:  arg.Controller,
		Parameters:  arg.Parameters,
		IsDefault:   arg.IsDefault,
		Labels:      arg.Labels,
		Annotations: arg.Annotations,
		LastSeenAt:  now,
		CreatedAt:   ifNewTime(prev.CreatedAt, now),
		UpdatedAt:   now,
	}
	f.ic[k] = row
	f.upsertCalls["ic|"+k]++
	_ = exists
	return row, nil
}
func (f *fakeMirrorQuerier) DeleteMirroredIngressClass(_ context.Context, arg sqlc.DeleteMirroredIngressClassParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.ic, icKey(arg.ClusterID, arg.Name))
	return nil
}
func (f *fakeMirrorQuerier) PruneStaleMirroredIngressClasses(_ context.Context, before time.Time) (int64, error) {
	return f.pruneByTime(&f.mu, &f.ic, before)
}

func (f *fakeMirrorQuerier) UpsertMirroredGatewayClass(_ context.Context, arg sqlc.UpsertMirroredGatewayClassParams) (sqlc.MirroredGatewayClass, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := icKey(arg.ClusterID, arg.Name)
	now := time.Now()
	prev := f.gwc[k]
	row := sqlc.MirroredGatewayClass{
		ID:             ifNew(prev.ID, uuid.New()),
		ClusterID:      arg.ClusterID,
		Name:           arg.Name,
		ControllerName: arg.ControllerName,
		Description:    arg.Description,
		Parameters:     arg.Parameters,
		AcceptedStatus: arg.AcceptedStatus,
		Labels:         arg.Labels,
		Annotations:    arg.Annotations,
		LastSeenAt:     now,
		CreatedAt:      ifNewTime(prev.CreatedAt, now),
		UpdatedAt:      now,
	}
	f.gwc[k] = row
	f.upsertCalls["gwc|"+k]++
	return row, nil
}
func (f *fakeMirrorQuerier) DeleteMirroredGatewayClass(_ context.Context, arg sqlc.DeleteMirroredGatewayClassParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.gwc, icKey(arg.ClusterID, arg.Name))
	return nil
}
func (f *fakeMirrorQuerier) PruneStaleMirroredGatewayClasses(_ context.Context, before time.Time) (int64, error) {
	return f.pruneByTime(&f.mu, &f.gwc, before)
}

func (f *fakeMirrorQuerier) UpsertMirroredNetworkPolicy(_ context.Context, arg sqlc.UpsertMirroredNetworkPolicyParams) (sqlc.MirroredNetworkPolicy, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := nsKey(arg.ClusterID, arg.Namespace, arg.Name)
	now := time.Now()
	prev := f.np[k]
	row := sqlc.MirroredNetworkPolicy{
		ID:           ifNew(prev.ID, uuid.New()),
		ClusterID:    arg.ClusterID,
		Namespace:    arg.Namespace,
		Name:         arg.Name,
		PodSelector:  arg.PodSelector,
		PolicyTypes:  arg.PolicyTypes,
		IngressRules: arg.IngressRules,
		EgressRules:  arg.EgressRules,
		Labels:       arg.Labels,
		Annotations:  arg.Annotations,
		IsManaged:    arg.IsManaged,
		LastSeenAt:   now,
		CreatedAt:    ifNewTime(prev.CreatedAt, now),
		UpdatedAt:    now,
	}
	f.np[k] = row
	f.upsertCalls["np|"+k]++
	return row, nil
}
func (f *fakeMirrorQuerier) DeleteMirroredNetworkPolicy(_ context.Context, arg sqlc.DeleteMirroredNetworkPolicyParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.np, nsKey(arg.ClusterID, arg.Namespace, arg.Name))
	return nil
}
func (f *fakeMirrorQuerier) PruneStaleMirroredNetworkPolicies(_ context.Context, before time.Time) (int64, error) {
	return f.pruneByTime(&f.mu, &f.np, before)
}

func (f *fakeMirrorQuerier) UpsertMirroredResourceQuota(_ context.Context, arg sqlc.UpsertMirroredResourceQuotaParams) (sqlc.MirroredResourceQuota, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := nsKey(arg.ClusterID, arg.Namespace, arg.Name)
	now := time.Now()
	prev := f.rq[k]
	row := sqlc.MirroredResourceQuota{
		ID:          ifNew(prev.ID, uuid.New()),
		ClusterID:   arg.ClusterID,
		Namespace:   arg.Namespace,
		Name:        arg.Name,
		Hard:        arg.Hard,
		Used:        arg.Used,
		Scopes:      arg.Scopes,
		Labels:      arg.Labels,
		Annotations: arg.Annotations,
		LastSeenAt:  now,
		CreatedAt:   ifNewTime(prev.CreatedAt, now),
		UpdatedAt:   now,
	}
	f.rq[k] = row
	f.upsertCalls["rq|"+k]++
	return row, nil
}
func (f *fakeMirrorQuerier) DeleteMirroredResourceQuota(_ context.Context, arg sqlc.DeleteMirroredResourceQuotaParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.rq, nsKey(arg.ClusterID, arg.Namespace, arg.Name))
	return nil
}
func (f *fakeMirrorQuerier) PruneStaleMirroredResourceQuotas(_ context.Context, before time.Time) (int64, error) {
	return f.pruneByTime(&f.mu, &f.rq, before)
}

func (f *fakeMirrorQuerier) UpsertMirroredLimitRange(_ context.Context, arg sqlc.UpsertMirroredLimitRangeParams) (sqlc.MirroredLimitRange, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := nsKey(arg.ClusterID, arg.Namespace, arg.Name)
	now := time.Now()
	prev := f.lr[k]
	row := sqlc.MirroredLimitRange{
		ID:          ifNew(prev.ID, uuid.New()),
		ClusterID:   arg.ClusterID,
		Namespace:   arg.Namespace,
		Name:        arg.Name,
		Limits:      arg.Limits,
		Labels:      arg.Labels,
		Annotations: arg.Annotations,
		LastSeenAt:  now,
		CreatedAt:   ifNewTime(prev.CreatedAt, now),
		UpdatedAt:   now,
	}
	f.lr[k] = row
	f.upsertCalls["lr|"+k]++
	return row, nil
}
func (f *fakeMirrorQuerier) DeleteMirroredLimitRange(_ context.Context, arg sqlc.DeleteMirroredLimitRangeParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.lr, nsKey(arg.ClusterID, arg.Namespace, arg.Name))
	return nil
}
func (f *fakeMirrorQuerier) PruneStaleMirroredLimitRanges(_ context.Context, before time.Time) (int64, error) {
	return f.pruneByTime(&f.mu, &f.lr, before)
}

// rowWithLastSeen is the property the per-kind table maps share — every
// row carries a LastSeenAt field. We can't introspect via a switch on
// map types in Go generics without one wrapper helper per kind, so we
// reimplement the prune loop per-kind in pruneByTime via a tiny
// type-specific shim function.

func (f *fakeMirrorQuerier) pruneByTime(mu *sync.Mutex, rawMap any, before time.Time) (int64, error) {
	mu.Lock()
	defer mu.Unlock()
	switch m := rawMap.(type) {
	case *map[string]sqlc.MirroredIngressClass:
		return pruneMap(*m, before, func(v sqlc.MirroredIngressClass) time.Time { return v.LastSeenAt }, func(k string) { delete(*m, k) }), nil
	case *map[string]sqlc.MirroredGatewayClass:
		return pruneMap(*m, before, func(v sqlc.MirroredGatewayClass) time.Time { return v.LastSeenAt }, func(k string) { delete(*m, k) }), nil
	case *map[string]sqlc.MirroredNetworkPolicy:
		return pruneMap(*m, before, func(v sqlc.MirroredNetworkPolicy) time.Time { return v.LastSeenAt }, func(k string) { delete(*m, k) }), nil
	case *map[string]sqlc.MirroredResourceQuota:
		return pruneMap(*m, before, func(v sqlc.MirroredResourceQuota) time.Time { return v.LastSeenAt }, func(k string) { delete(*m, k) }), nil
	case *map[string]sqlc.MirroredLimitRange:
		return pruneMap(*m, before, func(v sqlc.MirroredLimitRange) time.Time { return v.LastSeenAt }, func(k string) { delete(*m, k) }), nil
	default:
		return 0, nil
	}
}

func pruneMap[V any](m map[string]V, before time.Time, lastSeen func(V) time.Time, drop func(string)) int64 {
	n := int64(0)
	for k, v := range m {
		if lastSeen(v).Before(before) {
			drop(k)
			n++
		}
	}
	return n
}

func ifNew(prev, fresh uuid.UUID) uuid.UUID {
	if prev == uuid.Nil {
		return fresh
	}
	return prev
}

func ifNewTime(prev, fresh time.Time) time.Time {
	if prev.IsZero() {
		return fresh
	}
	return prev
}

// Compile-time assert the fake satisfies the interface.
var _ MirrorQuerier = (*fakeMirrorQuerier)(nil)

// ---------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------

func mustUnstructured(t *testing.T, body string) *unstructured.Unstructured {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	return &unstructured.Unstructured{Object: m}
}

const ingressClassDefaultFixture = `{
  "apiVersion": "networking.k8s.io/v1",
  "kind": "IngressClass",
  "metadata": {
    "name": "nginx",
    "labels": {"app.kubernetes.io/managed-by": "helm"},
    "annotations": {"ingressclass.kubernetes.io/is-default-class": "true"}
  },
  "spec": {
    "controller": "k8s.io/ingress-nginx",
    "parameters": {"apiGroup": "k8s.example.com", "kind": "IngressParameters", "name": "external-lb"}
  }
}`

const ingressClassNonDefaultFixture = `{
  "apiVersion": "networking.k8s.io/v1",
  "kind": "IngressClass",
  "metadata": {"name": "nginx-internal"},
  "spec": {"controller": "k8s.io/ingress-nginx"}
}`

const gatewayClassFixture = `{
  "apiVersion": "gateway.networking.k8s.io/v1",
  "kind": "GatewayClass",
  "metadata": {"name": "cilium"},
  "spec": {"controllerName": "io.cilium/gateway-controller", "description": "Cilium gateway"},
  "status": {
    "conditions": [
      {"type": "Programmed", "status": "True"},
      {"type": "Accepted", "status": "True"}
    ]
  }
}`

const networkPolicyManagedFixture = `{
  "apiVersion": "networking.k8s.io/v1",
  "kind": "NetworkPolicy",
  "metadata": {
    "name": "deny-egress",
    "namespace": "prod-api",
    "labels": {"app.kubernetes.io/managed-by": "astronomer", "app.kubernetes.io/part-of": "project-x"}
  },
  "spec": {
    "podSelector": {"matchLabels": {"app": "api"}},
    "policyTypes": ["Egress"],
    "egress": [{"to": [{"ipBlock": {"cidr": "10.0.0.0/8"}}]}]
  }
}`

const networkPolicyOperatorFixture = `{
  "apiVersion": "networking.k8s.io/v1",
  "kind": "NetworkPolicy",
  "metadata": {"name": "deny-all", "namespace": "kube-system"},
  "spec": {"podSelector": {}, "policyTypes": ["Ingress"]}
}`

const resourceQuotaFixture = `{
  "apiVersion": "v1",
  "kind": "ResourceQuota",
  "metadata": {"name": "team-a-quota", "namespace": "team-a"},
  "spec": {"hard": {"cpu": "32", "memory": "64Gi", "pods": "100"}, "scopes": ["NotTerminating"]},
  "status": {"used": {"cpu": "4", "memory": "8Gi", "pods": "12"}}
}`

const limitRangeFixture = `{
  "apiVersion": "v1",
  "kind": "LimitRange",
  "metadata": {"name": "container-defaults", "namespace": "team-a"},
  "spec": {
    "limits": [
      {"type": "Container", "default": {"cpu": "500m", "memory": "256Mi"}, "defaultRequest": {"cpu": "100m", "memory": "64Mi"}}
    ]
  }
}`

// ---------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------

func TestIngestIngressClass_UpsertIsIdempotent(t *testing.T) {
	q := newFakeMirrorQuerier()
	cid := uuid.New()
	obj := mustUnstructured(t, ingressClassDefaultFixture)
	ctx := context.Background()

	row1, err := IngestIngressClass(ctx, q, cid, obj)
	if err != nil {
		t.Fatalf("first ingest: %v", err)
	}
	first := row1.CreatedAt
	// Sleep just enough that an updated_at refresh would be visibly different.
	time.Sleep(2 * time.Millisecond)

	for i := 0; i < 5; i++ {
		row, err := IngestIngressClass(ctx, q, cid, obj)
		if err != nil {
			t.Fatalf("ingest %d: %v", i, err)
		}
		if !row.CreatedAt.Equal(first) {
			t.Fatalf("created_at drifted on iter %d: %v vs %v", i, row.CreatedAt, first)
		}
	}

	if got := q.upsertCalls["ic|"+icKey(cid, "nginx")]; got != 6 {
		t.Fatalf("expected 6 upsert calls, got %d", got)
	}
}

func TestIngestIngressClass_DetectsIsDefaultAnnotation(t *testing.T) {
	q := newFakeMirrorQuerier()
	cid := uuid.New()
	ctx := context.Background()

	defRow, err := IngestIngressClass(ctx, q, cid, mustUnstructured(t, ingressClassDefaultFixture))
	if err != nil {
		t.Fatalf("default ingest: %v", err)
	}
	if !defRow.IsDefault {
		t.Fatalf("expected is_default=true for annotated row")
	}

	nonRow, err := IngestIngressClass(ctx, q, cid, mustUnstructured(t, ingressClassNonDefaultFixture))
	if err != nil {
		t.Fatalf("non-default ingest: %v", err)
	}
	if nonRow.IsDefault {
		t.Fatalf("expected is_default=false for unannotated row")
	}
	if nonRow.Controller != "k8s.io/ingress-nginx" {
		t.Fatalf("controller not captured: %q", nonRow.Controller)
	}
}

func TestIngestGatewayClass_CapturesAcceptedStatus(t *testing.T) {
	q := newFakeMirrorQuerier()
	cid := uuid.New()
	ctx := context.Background()

	row, err := IngestGatewayClass(ctx, q, cid, mustUnstructured(t, gatewayClassFixture))
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if row.AcceptedStatus != "True" {
		t.Fatalf("expected AcceptedStatus=True, got %q", row.AcceptedStatus)
	}
	if row.ControllerName != "io.cilium/gateway-controller" {
		t.Fatalf("controllerName: %q", row.ControllerName)
	}
	if row.Description != "Cilium gateway" {
		t.Fatalf("description: %q", row.Description)
	}
}

func TestIngestNetworkPolicy_MarksManagedFromLabels(t *testing.T) {
	q := newFakeMirrorQuerier()
	cid := uuid.New()
	ctx := context.Background()

	managed, err := IngestNetworkPolicy(ctx, q, cid, mustUnstructured(t, networkPolicyManagedFixture))
	if err != nil {
		t.Fatalf("managed ingest: %v", err)
	}
	if !managed.IsManaged {
		t.Fatalf("expected is_managed=true for label managed-by=astronomer")
	}
	// Policy types preserved
	if !strings.Contains(string(managed.PolicyTypes), "Egress") {
		t.Fatalf("policy_types missing Egress: %s", managed.PolicyTypes)
	}

	unmanaged, err := IngestNetworkPolicy(ctx, q, cid, mustUnstructured(t, networkPolicyOperatorFixture))
	if err != nil {
		t.Fatalf("operator ingest: %v", err)
	}
	if unmanaged.IsManaged {
		t.Fatalf("expected is_managed=false for unlabelled netpol")
	}
}

func TestIngestResourceQuota_PreservesUsedVsHard(t *testing.T) {
	q := newFakeMirrorQuerier()
	cid := uuid.New()
	ctx := context.Background()

	row, err := IngestResourceQuota(ctx, q, cid, mustUnstructured(t, resourceQuotaFixture))
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if !strings.Contains(string(row.Hard), `"cpu":"32"`) {
		t.Fatalf("hard quota lost cpu: %s", row.Hard)
	}
	if !strings.Contains(string(row.Used), `"pods":"12"`) {
		t.Fatalf("used quota lost pods: %s", row.Used)
	}
	if !strings.Contains(string(row.Scopes), "NotTerminating") {
		t.Fatalf("scopes lost: %s", row.Scopes)
	}
}

func TestIngestLimitRange_PreservesLimits(t *testing.T) {
	q := newFakeMirrorQuerier()
	cid := uuid.New()
	ctx := context.Background()

	row, err := IngestLimitRange(ctx, q, cid, mustUnstructured(t, limitRangeFixture))
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if !strings.Contains(string(row.Limits), `"500m"`) {
		t.Fatalf("limits dropped default.cpu: %s", row.Limits)
	}
	if !strings.Contains(string(row.Limits), `"defaultRequest"`) {
		t.Fatalf("limits dropped defaultRequest: %s", row.Limits)
	}
}

func TestPruneStale_DropsRowsOlderThan1h(t *testing.T) {
	q := newFakeMirrorQuerier()
	cid := uuid.New()
	ctx := context.Background()

	// Seed two rows; force one's last_seen_at into the past.
	if _, err := IngestIngressClass(ctx, q, cid, mustUnstructured(t, ingressClassDefaultFixture)); err != nil {
		t.Fatal(err)
	}
	if _, err := IngestIngressClass(ctx, q, cid, mustUnstructured(t, ingressClassNonDefaultFixture)); err != nil {
		t.Fatal(err)
	}
	// Backdate the "nginx-internal" row two hours.
	k := icKey(cid, "nginx-internal")
	row := q.ic[k]
	row.LastSeenAt = time.Now().Add(-2 * time.Hour)
	q.ic[k] = row

	cutoff := time.Now().Add(-StaleRetention)
	counts, err := PruneStaleAll(ctx, q, cutoff)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if got := counts[KindIngressClass]; got != 1 {
		t.Fatalf("expected 1 IngressClass pruned, got %d", got)
	}
	if _, ok := q.ic[k]; ok {
		t.Fatalf("stale row not deleted")
	}
	if _, ok := q.ic[icKey(cid, "nginx")]; !ok {
		t.Fatalf("fresh row was incorrectly pruned")
	}
}

func TestHandleDelete_RoutesByKind(t *testing.T) {
	q := newFakeMirrorQuerier()
	cid := uuid.New()
	ctx := context.Background()
	if _, err := IngestNetworkPolicy(ctx, q, cid, mustUnstructured(t, networkPolicyManagedFixture)); err != nil {
		t.Fatal(err)
	}
	if err := HandleDelete(ctx, q, cid, DeleteEvent{Kind: KindNetworkPolicy, Namespace: "prod-api", Name: "deny-egress"}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := q.np[nsKey(cid, "prod-api", "deny-egress")]; ok {
		t.Fatalf("row not deleted")
	}
	if err := HandleDelete(ctx, q, cid, DeleteEvent{Kind: "BogusKind"}); err == nil {
		t.Fatalf("expected error on unknown kind")
	}
}
