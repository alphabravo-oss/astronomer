package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestIsAuthorizationError(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "unauthorized", err: &HTTPError{Provider: ProviderEKS, StatusCode: http.StatusUnauthorized}, want: true},
		{name: "forbidden wrapped", err: fmt.Errorf("provider call: %w", &HTTPError{Provider: ProviderAKS, StatusCode: http.StatusForbidden}), want: true},
		{name: "throttled", err: &HTTPError{Provider: ProviderGKE, StatusCode: http.StatusTooManyRequests}},
		{name: "unrelated", err: fmt.Errorf("credential materializer unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsAuthorizationError(tc.err); got != tc.want {
				t.Fatalf("IsAuthorizationError() = %v, want %v", got, tc.want)
			}
		})
	}
}

// fakeMaterializer satisfies CloudCredentialMaterializer with a static
// blob. Tests don't care what's inside; they just need it non-nil.
type fakeMaterializer struct{ blob map[string]string }

func (f *fakeMaterializer) ResolveForCluster(ctx context.Context, _ Cluster, _ ProviderID) (map[string]string, error) {
	return f.blob, nil
}

func TestProvider_DetectsEKSFromClusterAnnotations(t *testing.T) {
	p := NewEKSProvider(nil)
	got := p.Detect(context.Background(), Cluster{Annotations: map[string]string{"astronomer.io/provider": "eks"}})
	if got != ProviderEKS {
		t.Fatalf("expected detect via annotation; got %q", got)
	}
	// Direct Provider field also detects.
	got = p.Detect(context.Background(), Cluster{Provider: "eks"})
	if got != ProviderEKS {
		t.Fatalf("expected detect via Provider field; got %q", got)
	}
	// Non-EKS clusters don't match.
	got = p.Detect(context.Background(), Cluster{Provider: "gke"})
	if got != "" {
		t.Fatalf("expected empty for non-EKS; got %q", got)
	}
}

func TestProvider_DetectsGKE(t *testing.T) {
	p := NewGKEProvider(nil)
	if got := p.Detect(context.Background(), Cluster{Provider: "gke"}); got != ProviderGKE {
		t.Fatalf("GKE detect failed: %q", got)
	}
}

func TestProvider_DetectsSelfManaged_FallsBackOnEmptyProvider(t *testing.T) {
	p := NewSelfManagedProvider()
	if got := p.Detect(context.Background(), Cluster{Provider: ""}); got != ProviderSelfManaged {
		t.Fatalf("empty-provider cluster should fall through to self_managed; got %q", got)
	}
	if got := p.Detect(context.Background(), Cluster{Provider: "eks"}); got != "" {
		t.Fatalf("self_managed should not claim eks; got %q", got)
	}
}

func TestProvider_EKSApplyIsIdempotent(t *testing.T) {
	var describeCalls, updateCalls int32
	mux := http.NewServeMux()
	mux.HandleFunc("/clusters/test-eks", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&describeCalls, 1)
		_ = json.NewEncoder(w).Encode(eksDescribeClusterResponse{
			Cluster: struct {
				ResourcesVpcConfig struct {
					PublicAccessCidrs []string `json:"publicAccessCidrs"`
				} `json:"resourcesVpcConfig"`
			}{
				ResourcesVpcConfig: struct {
					PublicAccessCidrs []string `json:"publicAccessCidrs"`
				}{
					PublicAccessCidrs: []string{"10.0.0.0/8", "192.168.0.0/16"},
				},
			},
		})
	})
	mux.HandleFunc("/clusters/test-eks/update-config", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&updateCalls, 1)
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := NewEKSProvider(&fakeMaterializer{})
	p.Endpoint = srv.URL
	p.SigningOverride = func(*http.Request, map[string]string) error { return nil }
	cluster := Cluster{Name: "test-eks", Provider: "eks", Region: "us-east-1"}

	// First Apply with the SAME set the API returns — should NOT POST.
	if err := p.Apply(context.Background(), cluster, []string{"10.0.0.0/8", "192.168.0.0/16"}); err != nil {
		t.Fatalf("apply (idempotent): %v", err)
	}
	if got := atomic.LoadInt32(&updateCalls); got != 0 {
		t.Fatalf("idempotent apply should skip update; got %d update calls", got)
	}

	// Second Apply with a NEW set — must POST.
	if err := p.Apply(context.Background(), cluster, []string{"10.0.0.0/8", "192.168.0.0/16", "203.0.113.0/24"}); err != nil {
		t.Fatalf("apply (changed): %v", err)
	}
	if got := atomic.LoadInt32(&updateCalls); got != 1 {
		t.Fatalf("changed apply should POST once; got %d", got)
	}
}

func TestProvider_EKSGetEffective(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/clusters/test-eks", func(w http.ResponseWriter, r *http.Request) {
		// Return entries in mixed order + duplicate to verify canonicalisation.
		_ = json.NewEncoder(w).Encode(eksDescribeClusterResponse{
			Cluster: struct {
				ResourcesVpcConfig struct {
					PublicAccessCidrs []string `json:"publicAccessCidrs"`
				} `json:"resourcesVpcConfig"`
			}{
				ResourcesVpcConfig: struct {
					PublicAccessCidrs []string `json:"publicAccessCidrs"`
				}{
					PublicAccessCidrs: []string{"192.168.0.0/16", "10.0.0.0/8", "10.0.0.0/8"},
				},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := NewEKSProvider(&fakeMaterializer{})
	p.Endpoint = srv.URL
	p.SigningOverride = func(*http.Request, map[string]string) error { return nil }
	got, err := p.GetEffective(context.Background(), Cluster{Name: "test-eks"})
	if err != nil {
		t.Fatalf("get-effective: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected dedupe to 2 entries, got %v", got)
	}
	if got[0] != "10.0.0.0/8" || got[1] != "192.168.0.0/16" {
		t.Fatalf("expected canonical sorted order, got %v", got)
	}
}

func TestProvider_GKEApplyIsIdempotent(t *testing.T) {
	var updateCalls int32
	mux := http.NewServeMux()
	const path = "/projects/proj/locations/us-central1/clusters/test-gke"
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(gkeClusterResponse{
				MasterAuthorizedNetworksConfig: gkeMasterAuthorizedNetworksConfig{
					Enabled:    true,
					CidrBlocks: []gkeCidrBlock{{CidrBlock: "10.0.0.0/8"}, {CidrBlock: "192.168.0.0/16"}},
				},
			})
		case http.MethodPut:
			atomic.AddInt32(&updateCalls, 1)
			w.WriteHeader(http.StatusOK)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := NewGKEProvider(&fakeMaterializer{})
	p.Endpoint = srv.URL
	p.SigningOverride = func(*http.Request, map[string]string) error { return nil }
	cluster := Cluster{Name: "test-gke", Provider: "gke", Region: "us-central1", ProjectID: "proj"}

	if err := p.Apply(context.Background(), cluster, []string{"10.0.0.0/8", "192.168.0.0/16"}); err != nil {
		t.Fatalf("apply (idempotent): %v", err)
	}
	if got := atomic.LoadInt32(&updateCalls); got != 0 {
		t.Fatalf("idempotent apply should skip; got %d", got)
	}
	if err := p.Apply(context.Background(), cluster, []string{"10.0.0.0/8"}); err != nil {
		t.Fatalf("apply (changed): %v", err)
	}
	if got := atomic.LoadInt32(&updateCalls); got != 1 {
		t.Fatalf("changed apply should PUT once; got %d", got)
	}
}

func TestProvider_AKSGetAndApply(t *testing.T) {
	var putCalls int32
	mux := http.NewServeMux()
	mux.HandleFunc("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/test-aks", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("api-version") != defaultAKSAPIVersion {
			t.Fatalf("unexpected api version: %s", r.URL.RawQuery)
		}
		if r.Method == http.MethodGet {
			w.Header().Set("ETag", `"generation-7"`)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "/read-only", "location": "eastus",
				"properties": map[string]any{"provisioningState": "Succeeded", "apiServerAccessProfile": map[string]any{"authorizedIPRanges": []string{"10.0.0.0/8"}}},
			})
			return
		}
		atomic.AddInt32(&putCalls, 1)
		if r.Header.Get("If-Match") != `"generation-7"` {
			t.Fatalf("missing optimistic concurrency header")
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if _, leaked := body["id"]; leaked {
			t.Fatalf("read-only id leaked into update body")
		}
		w.WriteHeader(http.StatusAccepted)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	p := NewAKSProvider(&fakeMaterializer{blob: map[string]string{"subscription_id": "sub"}})
	p.Endpoint = srv.URL
	p.HTTPClient = srv.Client()
	p.AuthOverride = func(context.Context, *http.Request, map[string]string) error { return nil }
	cluster := Cluster{Name: "test-aks", ResourceGroup: "rg", Provider: ProviderAKS}
	if got, err := p.GetEffective(context.Background(), cluster); err != nil || len(got) != 1 {
		t.Fatalf("get effective: %v %v", got, err)
	}
	if err := p.Apply(context.Background(), cluster, []string{"10.0.0.0/8", "203.0.113.0/24"}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if atomic.LoadInt32(&putCalls) != 1 {
		t.Fatalf("expected one AKS update")
	}
}

func TestProvider_DOKSGetAndApply(t *testing.T) {
	var putCalls int32
	mux := http.NewServeMux()
	mux.HandleFunc("/kubernetes/clusters/do-cluster-id", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]any{"kubernetes_cluster": map[string]any{"control_plane_firewall": map[string]any{"enabled": true, "allowed_addresses": []string{"10.0.0.0/8"}}}})
			return
		}
		atomic.AddInt32(&putCalls, 1)
		w.WriteHeader(http.StatusAccepted)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	p := NewDOKSProvider(&fakeMaterializer{blob: map[string]string{"token": "secret"}})
	p.Endpoint = srv.URL
	p.HTTPClient = srv.Client()
	p.AuthOverride = func(*http.Request, map[string]string) error { return nil }
	cluster := Cluster{Provider: ProviderDOKS, ProviderResourceID: "do-cluster-id"}
	if err := p.Apply(context.Background(), cluster, []string{"10.0.0.0/8", "203.0.113.0/24"}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if atomic.LoadInt32(&putCalls) != 1 {
		t.Fatalf("expected one DOKS update")
	}
}

func TestProvider_SelfManagedRefusesApply(t *testing.T) {
	p := NewSelfManagedProvider()
	var unsupported *UnsupportedEnforcementError
	if err := p.Apply(context.Background(), Cluster{}, nil); !errors.As(err, &unsupported) {
		t.Fatalf("self-managed apply should return a permanent capability error; got %v", err)
	}
	// GetEffective is allowed to return empty (monitor mode keeps recording).
	got, err := p.GetEffective(context.Background(), Cluster{})
	if err != nil {
		t.Fatalf("self-managed GetEffective should not error in v1; got %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("self-managed GetEffective should return empty in v1; got %v", got)
	}
}

func TestRegistry_DetectsInOrder(t *testing.T) {
	r := NewRegistry()
	r.Register(NewEKSProvider(nil))
	r.Register(NewGKEProvider(nil))
	r.Register(NewAKSProvider(nil))
	r.Register(NewDOKSProvider(nil))
	r.Register(NewSelfManagedProvider())

	id, _ := r.Detect(context.Background(), Cluster{Provider: "eks"})
	if id != ProviderEKS {
		t.Fatalf("EKS cluster: %q", id)
	}
	id, _ = r.Detect(context.Background(), Cluster{Provider: "gke"})
	if id != ProviderGKE {
		t.Fatalf("GKE cluster: %q", id)
	}
	id, _ = r.Detect(context.Background(), Cluster{Provider: ""})
	if id != ProviderSelfManaged {
		t.Fatalf("empty provider should fall through to self_managed; got %q", id)
	}
}

func TestRegistry_Lookup(t *testing.T) {
	r := NewRegistry()
	r.Register(NewEKSProvider(nil))
	r.Register(NewGKEProvider(nil))
	if p := r.Lookup(ProviderEKS); p == nil {
		t.Fatalf("Lookup(eks) returned nil")
	}
	if p := r.Lookup("never-heard-of-it"); p != nil {
		t.Fatalf("Lookup of unknown should return nil")
	}
}
