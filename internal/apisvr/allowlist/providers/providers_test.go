package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
)

// fakeMaterializer satisfies CloudCredentialMaterializer with a static
// blob. Tests don't care what's inside; they just need it non-nil.
type fakeMaterializer struct{ blob map[string]string }

func (f *fakeMaterializer) ResolveForCluster(ctx context.Context, _ uuid.UUID) (map[string]string, error) {
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

func TestProvider_AKSReturnsNotImplemented(t *testing.T) {
	p := NewAKSProvider(nil)
	if err := p.Apply(context.Background(), Cluster{}, nil); err == nil || !ErrProviderNotImplemented(err) {
		t.Fatalf("expected not-implemented sentinel; got %v", err)
	}
	if _, err := p.GetEffective(context.Background(), Cluster{}); err == nil || !ErrProviderNotImplemented(err) {
		t.Fatalf("expected not-implemented sentinel; got %v", err)
	}
}

func TestProvider_DOKSReturnsNotImplemented(t *testing.T) {
	p := NewDOKSProvider(nil)
	if err := p.Apply(context.Background(), Cluster{}, nil); err == nil || !ErrProviderNotImplemented(err) {
		t.Fatalf("expected not-implemented sentinel; got %v", err)
	}
}

func TestProvider_SelfManagedRefusesApply(t *testing.T) {
	p := NewSelfManagedProvider()
	if err := p.Apply(context.Background(), Cluster{}, nil); err == nil || !ErrProviderNotImplemented(err) {
		t.Fatalf("self-managed apply should refuse with sentinel; got %v", err)
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
