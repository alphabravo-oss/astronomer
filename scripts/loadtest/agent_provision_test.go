package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestProvisionSyntheticAgentCredentialsKeepsPerClusterMapping(t *testing.T) {
	t.Parallel()
	const adminToken = "fixture-admin-bearer"
	ids := []string{
		"11111111-1111-4111-8111-111111111111",
		"22222222-2222-4222-8222-222222222222",
		"33333333-3333-4333-8333-333333333333",
	}
	tokens := map[string]string{
		ids[0]: "registration-for-one",
		ids[1]: "registration-for-two",
		ids[2]: "registration-for-three",
	}
	var mu sync.Mutex
	nameToID := map[string]string{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+adminToken {
			t.Errorf("Authorization = %q", got)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/clusters/" {
			var body struct {
				Name string `json:"name"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode create body: %v", err)
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			var index int
			if _, err := fmt.Sscanf(body.Name, "loadtest-testrun-%04d", &index); err != nil || index >= len(ids) {
				t.Errorf("unexpected fixture name %q", body.Name)
				http.Error(w, "bad name", http.StatusBadRequest)
				return
			}
			mu.Lock()
			nameToID[body.Name] = ids[index]
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = fmt.Fprintf(w, `{"data":{"id":%q}}`, ids[index])
			return
		}
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/register/") {
			parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
			clusterID := parts[len(parts)-2]
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = fmt.Fprintf(w, `{"data":{"cluster_id":%q,"token":%q}}`, clusterID, tokens[clusterID])
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	credentials, err := provisionSyntheticAgentCredentials(
		context.Background(), server.Client(), server.URL, adminToken, len(ids), "testrun",
	)
	if err != nil {
		t.Fatal(err)
	}
	for index, credential := range credentials {
		if credential.ClusterID != ids[index] || credential.RegistrationToken != tokens[ids[index]] {
			t.Fatalf("credential[%d] mapped to cluster %q with the wrong registration credential", index, credential.ClusterID)
		}
		if credential.RegistrationToken == adminToken {
			t.Fatalf("credential[%d] reused the admin bearer", index)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(nameToID) != len(ids) {
		t.Fatalf("created fixture count = %d, want %d", len(nameToID), len(ids))
	}
}

func TestProvisionSyntheticAgentCredentialsFailsOnMismatchedRegistration(t *testing.T) {
	t.Parallel()
	const clusterID = "11111111-1111-4111-8111-111111111111"
	var cleaned atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/clusters/":
			w.WriteHeader(http.StatusCreated)
			_, _ = fmt.Fprintf(w, `{"data":{"id":%q}}`, clusterID)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/register/"):
			w.WriteHeader(http.StatusCreated)
			_, _ = fmt.Fprint(w, `{"data":{"cluster_id":"22222222-2222-4222-8222-222222222222","token":"wrong-cluster-token"}}`)
		case r.Method == http.MethodDelete:
			cleaned.Store(true)
			w.WriteHeader(http.StatusAccepted)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	credentials, err := provisionSyntheticAgentCredentials(
		context.Background(), server.Client(), server.URL, "admin", 1, "testrun",
	)
	if err == nil || !strings.Contains(err.Error(), "did not match its cluster") {
		t.Fatalf("credentials=%v error=%v", credentials, err)
	}
	if credentials != nil {
		t.Fatalf("partial credentials escaped fail-fast provisioning: %v", credentials)
	}
	if !cleaned.Load() {
		t.Fatal("partial cluster fixture was not decommissioned")
	}
}

func TestSyntheticAgentAdoptsDurableCredential(t *testing.T) {
	t.Parallel()
	agent := &syntheticAgent{token: "registration", bootstrap: true}
	if err := agent.acceptAgentCredential(""); err == nil {
		t.Fatal("bootstrap ACK without durable credential was accepted")
	}
	if err := agent.acceptAgentCredential("durable"); err != nil {
		t.Fatal(err)
	}
	if agent.token != "durable" || agent.bootstrap {
		t.Fatalf("durable credential was not adopted")
	}
	if err := agent.acceptAgentCredential(""); err != nil {
		t.Fatalf("durable reconnect without rotation should remain valid: %v", err)
	}
}

func TestCleanupProvisionedClustersForcesEveryFixture(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	deleted := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Query().Get("force") != "true" {
			http.Error(w, "expected forced delete", http.StatusBadRequest)
			return
		}
		mu.Lock()
		deleted[r.URL.Path] = true
		mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	credentials := []agentCredential{
		{ClusterID: "11111111-1111-4111-8111-111111111111"},
		{ClusterID: "22222222-2222-4222-8222-222222222222"},
	}
	cleanupProvisionedClusters(context.Background(), server.Client(), server.URL, "admin", credentials)

	mu.Lock()
	defer mu.Unlock()
	if len(deleted) != len(credentials) {
		t.Fatalf("forced cleanup count = %d, want %d", len(deleted), len(credentials))
	}
	for _, credential := range credentials {
		path := "/api/v1/clusters/" + credential.ClusterID + "/"
		if !deleted[path] {
			t.Fatalf("fixture %s was not decommissioned", credential.ClusterID)
		}
	}
}
