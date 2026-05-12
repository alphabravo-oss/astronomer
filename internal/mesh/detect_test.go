// Detector unit tests for the sprint-071 service-mesh tab. Tests
// stand up a fakeRequester that responds to specific path patterns
// with canned JSON. The detector never mutates anything, so the
// fakes are pure read-side fixtures.

package mesh

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// fakeRequester is a scripted K8sRequester. Each entry in responses
// keys on the verb + path; the first matching entry wins. The default
// (no match) returns 404 so probes silently fall to zero — the
// detector treats CRD-not-installed as a normal cluster, not an
// error.
type fakeRequester struct {
	responses map[string]fakeResponse
	calls     []string
}

type fakeResponse struct {
	status int
	body   any // either []byte / string (raw) or a value to be JSON-marshalled
}

func newFakeRequester() *fakeRequester {
	return &fakeRequester{responses: map[string]fakeResponse{}}
}

func (f *fakeRequester) set(method, path string, status int, body any) {
	f.responses[method+" "+path] = fakeResponse{status: status, body: body}
}

func (f *fakeRequester) Do(_ context.Context, _ string, method, path string, _ []byte, _ map[string]string) (*protocol.K8sResponsePayload, error) {
	key := method + " " + path
	f.calls = append(f.calls, key)
	resp, ok := f.responses[key]
	if !ok {
		// Some probes use query-string variants — match on prefix too.
		for k, v := range f.responses {
			if strings.HasPrefix(key, k) {
				resp = v
				ok = true
				break
			}
		}
	}
	if !ok {
		return &protocol.K8sResponsePayload{StatusCode: http.StatusNotFound}, nil
	}
	out := &protocol.K8sResponsePayload{StatusCode: resp.status}
	switch v := resp.body.(type) {
	case nil:
		// empty body
	case []byte:
		out.Body = base64.StdEncoding.EncodeToString(v)
	case string:
		out.Body = base64.StdEncoding.EncodeToString([]byte(v))
	default:
		raw, _ := json.Marshal(v)
		out.Body = base64.StdEncoding.EncodeToString(raw)
	}
	return out, nil
}

// itemsList builds a Kubernetes-style list body with `count` items —
// the detector only cares about len(items), so each entry can be an
// empty object.
func itemsList(count int) map[string]any {
	items := make([]map[string]any, count)
	for i := range items {
		items[i] = map[string]any{}
	}
	return map[string]any{"items": items}
}

// nsList shapes a v1/namespaces response with the given names.
func nsList(names ...string) map[string]any {
	items := make([]map[string]any, 0, len(names))
	for _, n := range names {
		items = append(items, map[string]any{"metadata": map[string]any{"name": n}})
	}
	return map[string]any{"items": items}
}

func TestDetect_IstioFromGatewayCount(t *testing.T) {
	f := newFakeRequester()
	f.set("GET", "/api/v1/namespaces", 200, nsList("default", "app1"))
	f.set("GET", "/apis/networking.istio.io/v1beta1/gateways", 200, itemsList(3))
	det, err := Detect(context.Background(), nil, f, uuid.New())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if det.Mesh != MeshIstio {
		t.Fatalf("mesh = %q, want %q", det.Mesh, MeshIstio)
	}
	if det.GatewayCount != 3 {
		t.Errorf("gateway_count = %d, want 3", det.GatewayCount)
	}
	if det.ControlPlaneNamespace != "istio-system" {
		t.Errorf("control_plane_namespace = %q, want istio-system", det.ControlPlaneNamespace)
	}
}

func TestDetect_LinkerdFromCRDCount(t *testing.T) {
	f := newFakeRequester()
	f.set("GET", "/api/v1/namespaces", 200, nsList("app1"))
	f.set("GET", "/apis/linkerd.io/v1alpha2/serviceprofiles", 200, itemsList(2))
	f.set("GET", "/apis/policy.linkerd.io/v1beta1/servers", 200, itemsList(1))
	det, err := Detect(context.Background(), nil, f, uuid.New())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if det.Mesh != MeshLinkerd {
		t.Fatalf("mesh = %q, want %q", det.Mesh, MeshLinkerd)
	}
	if det.ServiceProfileCount != 2 || det.ServerAuthCount != 1 {
		t.Errorf("counts = (sps=%d, servers=%d), want (2, 1)", det.ServiceProfileCount, det.ServerAuthCount)
	}
}

func TestDetect_NoMeshWhenAllZero(t *testing.T) {
	f := newFakeRequester()
	f.set("GET", "/api/v1/namespaces", 200, nsList("default"))
	det, err := Detect(context.Background(), nil, f, uuid.New())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if det.Mesh != MeshNone {
		t.Errorf("mesh = %q, want %q", det.Mesh, MeshNone)
	}
	if det.GatewayCount != 0 || det.PeerAuthCount != 0 || det.ServerAuthCount != 0 {
		t.Errorf("expected zero counts, got %+v", det)
	}
}

func TestDetect_PrefersIstioWhenBothPresent(t *testing.T) {
	// Document the precedence: Istio wins when both meshes have
	// artifacts. Rare in practice (operators rarely co-install both),
	// but the precedence is part of the contract.
	f := newFakeRequester()
	f.set("GET", "/api/v1/namespaces", 200, nsList("app1"))
	f.set("GET", "/apis/networking.istio.io/v1beta1/gateways", 200, itemsList(1))
	f.set("GET", "/apis/linkerd.io/v1alpha2/serviceprofiles", 200, itemsList(1))
	det, err := Detect(context.Background(), nil, f, uuid.New())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if det.Mesh != MeshIstio {
		t.Errorf("mesh = %q, want %q (precedence)", det.Mesh, MeshIstio)
	}
	if det.ServiceProfileCount != 1 {
		t.Errorf("linkerd counts must still populate; got %d", det.ServiceProfileCount)
	}
}

func TestDetect_ParsesVersionFromIstiodDeploymentImageTag(t *testing.T) {
	f := newFakeRequester()
	f.set("GET", "/api/v1/namespaces", 200, nsList("default"))
	f.set("GET", "/apis/apps/v1/namespaces/istio-system/deployments/istiod", 200, map[string]any{
		"metadata": map[string]any{"namespace": "istio-system"},
		"spec": map[string]any{
			"template": map[string]any{
				"spec": map[string]any{
					"containers": []map[string]any{
						{"image": "docker.io/istio/pilot:1.22.4-distroless"},
					},
				},
			},
		},
	})
	det, err := Detect(context.Background(), nil, f, uuid.New())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if det.Mesh != MeshIstio {
		t.Fatalf("mesh = %q, want %q", det.Mesh, MeshIstio)
	}
	if det.Version != "1.22.4-distroless" {
		t.Errorf("version = %q, want 1.22.4-distroless", det.Version)
	}
}

func TestDetect_MTLSCoverageZeroOnLinkerdWithoutServers(t *testing.T) {
	f := newFakeRequester()
	f.set("GET", "/api/v1/namespaces", 200, nsList("app1", "app2"))
	// Some Linkerd ServiceProfiles but zero Servers — the Linkerd
	// mTLS heuristic only fires when at least one Server exists.
	f.set("GET", "/apis/linkerd.io/v1alpha2/serviceprofiles", 200, itemsList(3))
	det, err := Detect(context.Background(), nil, f, uuid.New())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if det.Mesh != MeshLinkerd {
		t.Fatalf("mesh = %q, want %q", det.Mesh, MeshLinkerd)
	}
	if det.MTLSCoveragePct != 0 {
		t.Errorf("mtls_coverage_pct = %d, want 0 (no Servers)", det.MTLSCoveragePct)
	}
}

func TestDetect_MTLSCoverageStrictPeerAuthIstio(t *testing.T) {
	// Two user namespaces; one has a STRICT PeerAuthentication. Coverage = 50%.
	f := newFakeRequester()
	f.set("GET", "/api/v1/namespaces", 200, nsList("app1", "app2"))
	f.set("GET", "/apis/networking.istio.io/v1beta1/gateways", 200, itemsList(1))
	f.set("GET", "/apis/security.istio.io/v1beta1/peerauthentications", 200, map[string]any{
		"items": []map[string]any{
			{
				"metadata": map[string]any{"namespace": "app1"},
				"spec":     map[string]any{"mtls": map[string]any{"mode": "STRICT"}},
			},
		},
	})
	det, err := Detect(context.Background(), nil, f, uuid.New())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if det.MTLSCoveragePct != 50 {
		t.Errorf("mtls_coverage_pct = %d, want 50", det.MTLSCoveragePct)
	}
	if det.PeerAuthCount != 1 {
		t.Errorf("peer_auth_count = %d, want 1", det.PeerAuthCount)
	}
}

func TestDetect_KumaControlPlane(t *testing.T) {
	f := newFakeRequester()
	f.set("GET", "/api/v1/namespaces", 200, nsList("app1"))
	f.set("GET", "/apis/apps/v1/namespaces/kuma-system/deployments/kuma-control-plane", 200, map[string]any{
		"metadata": map[string]any{"namespace": "kuma-system"},
	})
	det, err := Detect(context.Background(), nil, f, uuid.New())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if det.Mesh != MeshKuma {
		t.Errorf("mesh = %q, want %q", det.Mesh, MeshKuma)
	}
	if det.ControlPlaneNamespace != "kuma-system" {
		t.Errorf("namespace = %q", det.ControlPlaneNamespace)
	}
}

// TestDetect_NilRequesterIsError documents that calling Detect
// without a K8sRequester is a programmer error — the worker must
// degrade-gracefully on its side rather than passing nil.
func TestDetect_NilRequesterIsError(t *testing.T) {
	_, err := Detect(context.Background(), nil, nil, uuid.New())
	if err == nil {
		t.Fatalf("expected error for nil requester")
	}
}

// fakeQuerier is a scripted Querier used to verify that a mirrored
// kind shortcut is honored over the tunnel probe.
type fakeQuerier struct {
	counts map[string]int
}

func (f fakeQuerier) CountMirroredKind(_ context.Context, _ uuid.UUID, group, kind string) (int, bool) {
	c, ok := f.counts[group+"/"+kind]
	return c, ok
}

func TestDetect_UsesMirroredCountWhenAvailable(t *testing.T) {
	q := fakeQuerier{counts: map[string]int{
		"networking.istio.io/Gateway": 7,
	}}
	f := newFakeRequester()
	f.set("GET", "/api/v1/namespaces", 200, nsList("default"))
	det, err := Detect(context.Background(), q, f, uuid.New())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if det.GatewayCount != 7 {
		t.Errorf("gateway_count = %d, want 7 (mirrored)", det.GatewayCount)
	}
	if det.Mesh != MeshIstio {
		t.Errorf("mesh = %q, want %q", det.Mesh, MeshIstio)
	}
	// The detector must NOT have hit the tunnel list path for Gateways.
	for _, call := range f.calls {
		if call == "GET /apis/networking.istio.io/v1beta1/gateways" {
			t.Errorf("tunnel was hit for mirrored kind: %q", call)
		}
	}
}
