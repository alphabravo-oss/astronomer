package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const providerContractDir = "testdata/contracts"

func contractFixture(t *testing.T, name string) []byte {
	t.Helper()
	value, err := os.ReadFile(filepath.Join(providerContractDir, name))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func assertContractJSON(t *testing.T, got io.Reader, fixture string) {
	t.Helper()
	var actual, expected any
	if err := json.NewDecoder(got).Decode(&actual); err != nil {
		t.Fatalf("decode provider request: %v", err)
	}
	if err := json.Unmarshal(contractFixture(t, fixture), &expected); err != nil {
		t.Fatalf("decode fixture %s: %v", fixture, err)
	}
	actualJSON, _ := json.Marshal(actual)
	expectedJSON, _ := json.Marshal(expected)
	if string(actualJSON) != string(expectedJSON) {
		t.Fatalf("provider request does not match %s\n got: %s\nwant: %s", fixture, actualJSON, expectedJSON)
	}
}

func TestEKSRecordedContract(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/clusters/fixture-eks", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("describe method=%s", r.Method)
		}
		_, _ = w.Write(contractFixture(t, "eks-describe.json"))
	})
	mux.HandleFunc("/clusters/fixture-eks/update-config", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("update request=%s content-type=%q", r.Method, r.Header.Get("Content-Type"))
		}
		assertContractJSON(t, r.Body, "eks-update.json")
		w.WriteHeader(http.StatusAccepted)
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	provider := NewEKSProvider(&fakeMaterializer{blob: map[string]string{"access_key_id": "synthetic", "secret_access_key": "synthetic"}})
	provider.Endpoint = server.URL
	provider.HTTPClient = server.Client()
	provider.SigningOverride = func(*http.Request, map[string]string) error { return nil }
	if err := provider.Apply(context.Background(), Cluster{Name: "fixture-eks", Region: "us-east-1"}, []string{"203.0.113.0/24", "10.0.0.0/8"}); err != nil {
		t.Fatal(err)
	}
}

func TestGKERecordedContract(t *testing.T) {
	path := "/projects/fixture-project/locations/us-central1/clusters/fixture-gke"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			t.Errorf("path=%s", r.URL.Path)
		}
		if r.Method == http.MethodGet {
			_, _ = w.Write(contractFixture(t, "gke-get.json"))
			return
		}
		if r.Method != http.MethodPut || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("update request=%s content-type=%q", r.Method, r.Header.Get("Content-Type"))
		}
		assertContractJSON(t, r.Body, "gke-update.json")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	provider := NewGKEProvider(&fakeMaterializer{blob: map[string]string{"service_account_json": "{}"}})
	provider.Endpoint = server.URL
	provider.HTTPClient = server.Client()
	provider.SigningOverride = func(*http.Request, map[string]string) error { return nil }
	cluster := Cluster{Name: "fixture-gke", Region: "us-central1", ProjectID: "fixture-project"}
	if err := provider.Apply(context.Background(), cluster, []string{"203.0.113.0/24", "10.0.0.0/8"}); err != nil {
		t.Fatal(err)
	}
}

func TestAKSRecordedContract(t *testing.T) {
	path := "/subscriptions/fixture-sub/resourceGroups/fixture-rg/providers/Microsoft.ContainerService/managedClusters/fixture-aks"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path || r.URL.Query().Get("api-version") != defaultAKSAPIVersion {
			t.Errorf("resource URL=%s", r.URL.String())
		}
		if r.Method == http.MethodGet {
			w.Header().Set("ETag", `"fixture-generation"`)
			_, _ = w.Write(contractFixture(t, "aks-get.json"))
			return
		}
		if r.Method != http.MethodPut || r.Header.Get("If-Match") != `"fixture-generation"` {
			t.Errorf("update method=%s if-match=%q", r.Method, r.Header.Get("If-Match"))
		}
		assertContractJSON(t, r.Body, "aks-update.json")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	provider := NewAKSProvider(&fakeMaterializer{blob: map[string]string{"subscription_id": "fixture-sub"}})
	provider.Endpoint = server.URL
	provider.HTTPClient = server.Client()
	provider.AuthOverride = func(context.Context, *http.Request, map[string]string) error { return nil }
	cluster := Cluster{Name: "fixture-aks", ResourceGroup: "fixture-rg"}
	if err := provider.Apply(context.Background(), cluster, []string{"203.0.113.0/24", "10.0.0.0/8"}); err != nil {
		t.Fatal(err)
	}
}

func TestDOKSRecordedContract(t *testing.T) {
	path := "/kubernetes/clusters/fixture-doks-id"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			t.Errorf("path=%s", r.URL.Path)
		}
		if r.Method == http.MethodGet {
			_, _ = w.Write(contractFixture(t, "doks-get.json"))
			return
		}
		if r.Method != http.MethodPut || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("update request=%s content-type=%q", r.Method, r.Header.Get("Content-Type"))
		}
		assertContractJSON(t, r.Body, "doks-update.json")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	provider := NewDOKSProvider(&fakeMaterializer{blob: map[string]string{"token": "synthetic-secret"}})
	provider.Endpoint = server.URL
	provider.HTTPClient = server.Client()
	provider.AuthOverride = func(*http.Request, map[string]string) error { return nil }
	if err := provider.Apply(context.Background(), Cluster{ProviderResourceID: "fixture-doks-id"}, []string{"203.0.113.0/24", "10.0.0.0/8"}); err != nil {
		t.Fatal(err)
	}
}

func TestProviderRecordedForbiddenResponsesShareAuthorizationTaxonomy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write(contractFixture(t, "forbidden.json"))
	}))
	defer server.Close()
	materializer := &fakeMaterializer{blob: map[string]string{
		"subscription_id": "fixture-sub", "token": "synthetic-secret",
	}}
	tests := map[string]func() error{
		"eks": func() error {
			provider := NewEKSProvider(materializer)
			provider.Endpoint, provider.HTTPClient = server.URL, server.Client()
			provider.SigningOverride = func(*http.Request, map[string]string) error { return nil }
			_, err := provider.GetEffective(context.Background(), Cluster{Name: "fixture-eks", Region: "us-east-1"})
			return err
		},
		"gke": func() error {
			provider := NewGKEProvider(materializer)
			provider.Endpoint, provider.HTTPClient = server.URL, server.Client()
			provider.SigningOverride = func(*http.Request, map[string]string) error { return nil }
			_, err := provider.GetEffective(context.Background(), Cluster{Name: "fixture-gke", Region: "us-central1", ProjectID: "fixture-project"})
			return err
		},
		"aks": func() error {
			provider := NewAKSProvider(materializer)
			provider.Endpoint, provider.HTTPClient = server.URL, server.Client()
			provider.AuthOverride = func(context.Context, *http.Request, map[string]string) error { return nil }
			_, err := provider.GetEffective(context.Background(), Cluster{Name: "fixture-aks", ResourceGroup: "fixture-rg"})
			return err
		},
		"doks": func() error {
			provider := NewDOKSProvider(materializer)
			provider.Endpoint, provider.HTTPClient = server.URL, server.Client()
			provider.AuthOverride = func(*http.Request, map[string]string) error { return nil }
			_, err := provider.GetEffective(context.Background(), Cluster{ProviderResourceID: "fixture-doks-id"})
			return err
		},
	}
	for name, call := range tests {
		t.Run(name, func(t *testing.T) {
			err := call()
			if !IsAuthorizationError(err) {
				t.Fatalf("403 was not classified as authorization failure: %v", err)
			}
			if strings.Contains(err.Error(), "synthetic-secret") {
				t.Fatalf("credential leaked through provider error: %v", err)
			}
		})
	}
}
