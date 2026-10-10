package tasks

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	dto "github.com/prometheus/client_model/go"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/alphabravocompany/astronomer-go/internal/mesh"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

type meshBoundsMirror struct {
	counts map[string]int
}

func (m meshBoundsMirror) CountMirroredKind(_ context.Context, _ uuid.UUID, _ string, kind string) (int, bool) {
	count, ok := m.counts[kind]
	return count, ok
}

func meshBoundsGauge(t *testing.T, labels []string) float64 {
	t.Helper()
	metric := &dto.Metric{}
	if err := astronomerClusterMesh.WithLabelValues(labels...).Write(metric); err != nil {
		t.Fatal(err)
	}
	return metric.GetGauge().GetValue()
}

func meshBoundsSubscription(t *testing.T) (*events.Bus, <-chan events.Event) {
	t.Helper()
	bus := events.NewBus()
	ctx, cancel := context.WithCancel(context.Background())
	changed := bus.Subscribe(ctx, events.AcceptAll)
	t.Cleanup(func() {
		cancel()
		timer := time.NewTimer(time.Second)
		defer timer.Stop()
		for {
			select {
			case _, open := <-changed:
				if !open {
					return
				}
			case <-timer.C:
				t.Error("mesh event subscription did not join")
				return
			}
		}
	})
	return bus, changed
}

func TestMeshDetectionRejectsMirroredCountsBeforeEffects(t *testing.T) {
	for _, kind := range []string{"Gateway", "VirtualService", "DestinationRule", "ServiceProfile", "Server"} {
		for _, value := range []struct {
			name  string
			count int
		}{
			{"negative", -1}, {"overflow", math.MaxInt32 + 1},
		} {
			t.Run(kind+"/"+value.name, func(t *testing.T) {
				q, requester, runtime := setupMeshDeps(t)
				clusterID := uuid.New()
				prior := sqlc.ClusterServiceMesh{ClusterID: clusterID, DetectedMesh: mesh.MeshLinkerd, GatewayCount: 7}
				q.rows[clusterID] = prior
				requester.set("GET", "/api/v1/namespaces", 200, meshNSList("default"))
				runtime.Deps.MirroredQuerier = meshBoundsMirror{counts: map[string]int{kind: value.count}}
				bus, changed := meshBoundsSubscription(t)
				runtime.Deps.Bus = bus
				priorLabels := observability.MetricValues(clusterID.String(), mesh.MeshLinkerd)
				newLabels := observability.MetricValues(clusterID.String(), mesh.MeshIstio)
				astronomerClusterMesh.WithLabelValues(priorLabels...).Set(1)
				astronomerClusterMesh.WithLabelValues(newLabels...).Set(0)
				t.Cleanup(func() {
					astronomerClusterMesh.DeleteLabelValues(priorLabels...)
					astronomerClusterMesh.DeleteLabelValues(newLabels...)
				})
				if err := runtime.DetectAndUpsert(context.Background(), clusterID); err == nil || !strings.Contains(err.Error(), "supported range") {
					t.Fatalf("invalid mirrored count must fail admission, got %v", err)
				}
				if len(q.upserts) != 0 || q.rows[clusterID] != prior {
					t.Fatalf("invalid detection changed persistence: upserts=%v row=%+v", q.upserts, q.rows[clusterID])
				}
				if meshBoundsGauge(t, priorLabels) != 1 || meshBoundsGauge(t, newLabels) != 0 {
					t.Fatal("invalid detection changed success gauges")
				}
				select {
				case event := <-changed:
					t.Fatalf("invalid detection published a changed event: %+v", event)
				default:
				}
			})
		}
	}
}

func TestMeshDetectionRetainsValidCountsAndPartialErrors(t *testing.T) {
	for _, test := range []struct {
		name    string
		count   int
		partial bool
	}{
		{"zero", 0, false}, {"maximum", math.MaxInt32, false}, {"partial_probe_failure", 1, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			q, requester, runtime := setupMeshDeps(t)
			clusterID := uuid.New()
			requester.set("GET", "/api/v1/namespaces", 200, meshNSList("default"))
			counts := map[string]int{"Gateway": test.count, "VirtualService": test.count, "DestinationRule": test.count, "ServiceProfile": test.count, "Server": test.count}
			if test.partial {
				delete(counts, "VirtualService")
				requester.set("GET", "/apis/networking.istio.io/v1beta1/virtualservices", 503, nil)
			}
			runtime.Deps.MirroredQuerier = meshBoundsMirror{counts: counts}
			bus, changed := meshBoundsSubscription(t)
			runtime.Deps.Bus = bus
			if err := runtime.DetectAndUpsert(context.Background(), clusterID); err != nil {
				t.Fatal(err)
			}
			if len(q.upserts) != 1 {
				t.Fatalf("upserts=%d, want 1", len(q.upserts))
			}
			got := q.upserts[0]
			virtualServices := int32(test.count)
			if test.partial {
				virtualServices = 0
			}
			if got.ClusterID != clusterID || got.GatewayCount != int32(test.count) || got.VirtualServiceCount != virtualServices || got.DestinationRuleCount != int32(test.count) || got.ServiceProfileCount != int32(test.count) || got.ServerAuthCount != int32(test.count) || got.PeerAuthenticationCount != 0 || got.MtlsCoveragePct != 0 {
				t.Fatalf("valid observation was narrowed incorrectly: %+v", got)
			}
			if test.partial != strings.Contains(got.LastError, "status=503") {
				t.Fatalf("partial probe diagnostic changed: %q", got.LastError)
			}
			labels := observability.MetricValues(clusterID.String(), got.DetectedMesh)
			t.Cleanup(func() { astronomerClusterMesh.DeleteLabelValues(labels...) })
			if meshBoundsGauge(t, labels) != 1 {
				t.Fatal("successful observation did not publish its gauge")
			}
			select {
			case event := <-changed:
				payload, ok := event.Data.(map[string]any)
				if event.Type != events.TypeServiceMeshChanged || !ok || payload["cluster_id"] != clusterID.String() || payload["mesh"] != got.DetectedMesh {
					t.Fatalf("changed event lost correlation: %+v", event)
				}
			default:
				t.Fatal("successful observation did not publish its changed event")
			}
		})
	}
}

func TestMeshDetectionDatabaseAdmissionValidatesEveryField(t *testing.T) {
	fields := []struct {
		name    string
		set     func(*mesh.Detection, int)
		maximum int
	}{
		{"gateway_count", func(d *mesh.Detection, n int) { d.GatewayCount = n }, math.MaxInt32},
		{"virtual_service_count", func(d *mesh.Detection, n int) { d.VirtualServiceCount = n }, math.MaxInt32},
		{"destination_rule_count", func(d *mesh.Detection, n int) { d.DestinationRuleCount = n }, math.MaxInt32},
		{"peer_auth_count", func(d *mesh.Detection, n int) { d.PeerAuthCount = n }, math.MaxInt32},
		{"service_profile_count", func(d *mesh.Detection, n int) { d.ServiceProfileCount = n }, math.MaxInt32},
		{"server_auth_count", func(d *mesh.Detection, n int) { d.ServerAuthCount = n }, math.MaxInt32},
		{"mtls_coverage_pct", func(d *mesh.Detection, n int) { d.MTLSCoveragePct = n }, 100},
	}
	for _, field := range fields {
		for _, value := range []struct {
			name  string
			count int
		}{
			{"negative", -1}, {"above_maximum", field.maximum + 1},
		} {
			t.Run(field.name+"/"+value.name, func(t *testing.T) {
				detection := mesh.Detection{Mesh: mesh.MeshIstio}
				field.set(&detection, value.count)
				params, err := meshDetectionParams(uuid.New(), detection)
				if err == nil || !strings.Contains(err.Error(), field.name) || params != (sqlc.UpsertClusterServiceMeshParams{}) {
					t.Fatalf("invalid %s admitted partial params=%+v error=%v", field.name, params, err)
				}
			})
		}
	}
	for _, maximum := range []bool{false, true} {
		name := "zero"
		if maximum {
			name = "maximum"
		}
		t.Run(name, func(t *testing.T) {
			detection := mesh.Detection{Mesh: mesh.MeshIstio, Version: "1.29.0", ControlPlaneNamespace: "mesh-system", Errors: []string{"first probe failed", "second probe failed"}}
			for _, field := range fields {
				if maximum {
					field.set(&detection, field.maximum)
				}
			}
			clusterID := uuid.New()
			params, err := meshDetectionParams(clusterID, detection)
			if err != nil {
				t.Fatal(err)
			}
			if params.ClusterID != clusterID || params.DetectedMesh != detection.Mesh || params.DetectedVersion != detection.Version || params.ControlPlaneNamespace != detection.ControlPlaneNamespace || params.LastError != "first probe failed; second probe failed" {
				t.Fatalf("valid observation metadata changed: %+v", params)
			}
			counts := []int32{params.GatewayCount, params.VirtualServiceCount, params.DestinationRuleCount, params.PeerAuthenticationCount, params.ServiceProfileCount, params.ServerAuthCount}
			for _, count := range counts {
				if maximum && count != math.MaxInt32 || !maximum && count != 0 {
					t.Fatalf("count=%d maximum=%t", count, maximum)
				}
			}
			if maximum && params.MtlsCoveragePct != 100 || !maximum && params.MtlsCoveragePct != 0 {
				t.Fatalf("coverage=%d maximum=%t", params.MtlsCoveragePct, maximum)
			}
		})
	}
}
