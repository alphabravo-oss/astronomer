package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// TestSanitizeLabelKey covers truncation at 63 chars and char-set rules. This
// is the contract every astronomer.io/label-<k> projection relies on — if the
// sanitizer changes meaning, every existing Argo cluster Secret needs a
// re-stamp.
func TestSanitizeLabelKey(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"empty", "", ""},
		{"simple lower", "team", "team"},
		{"uppercase folded", "Team", "team"},
		{"mixed case", "TeamName", "teamname"},
		{"space becomes dash", "Team Name", "team-name"},
		{"multiple spaces collapse", "Team   Name", "team-name"},
		{"slash becomes dash", "team/name", "team-name"},
		{"underscores become dash", "team_name", "team-name"},
		{"leading dash stripped", "-team", "team"},
		{"trailing dash stripped", "team-", "team"},
		{"leading non-alnum stripped", "_team", "team"},
		{"dots preserved", "team.name", "team.name"},
		{"digits preserved", "team0", "team0"},
		{"unicode replaced with dash", "tëam", "t-am"},
		{"truncation at 63", strings.Repeat("a", 80), strings.Repeat("a", 63)},
		{"truncation trims trailing non-alnum",
			strings.Repeat("a", 62) + "-" + "bbbbb",
			strings.Repeat("a", 62),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeLabelKey(tc.in)
			if got != tc.want {
				t.Fatalf("sanitizeLabelKey(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestManagedClusterLabels_StaticKeys exercises the always-on label keys.
func TestManagedClusterLabels_StaticKeys(t *testing.T) {
	id := uuid.New()
	cluster := sqlc.Cluster{ID: id, Name: "prod-east-1", Environment: "production"}
	labels := managedClusterLabels(cluster)

	want := map[string]string{
		"astronomer.io/managed-by":   "astronomer",
		"astronomer.io/cluster-id":   id.String(),
		"astronomer.io/cluster-name": "prod-east-1",
		"astronomer.io/environment":  "production",
	}
	for k, v := range want {
		if got := labels[k]; got != v {
			t.Errorf("labels[%q] = %q, want %q", k, got, v)
		}
	}
}

// TestManagedClusterLabels_UserLabelsMirrored covers the prefix projection of
// cluster.Labels onto astronomer.io/label-<k>.
func TestManagedClusterLabels_UserLabelsMirrored(t *testing.T) {
	id := uuid.New()
	cluster := sqlc.Cluster{
		ID:   id,
		Name: "us-east-1",
		Labels: json.RawMessage(`{
			"tier":        "prod",
			"environment": "us-east",
			"Team Name":   "platform"
		}`),
	}
	labels := managedClusterLabels(cluster)

	if got := labels["astronomer.io/label-tier"]; got != "prod" {
		t.Errorf("label-tier = %q, want prod", got)
	}
	if got := labels["astronomer.io/label-environment"]; got != "us-east" {
		t.Errorf("label-environment = %q, want us-east", got)
	}
	// "Team Name" is sanitized to "team-name" — the original key is gone.
	if got := labels["astronomer.io/label-team-name"]; got != "platform" {
		t.Errorf("label-team-name = %q, want platform", got)
	}
	if _, exists := labels["astronomer.io/label-Team Name"]; exists {
		t.Errorf("non-sanitized key leaked: %q", "astronomer.io/label-Team Name")
	}
}

// TestRegisterClusterWithArgoCD_StampsLabels — given a cluster with
// labels = {tier: prod, environment: us-east}, the created Secret carries
// astronomer.io/label-tier=prod + astronomer.io/label-environment=us-east +
// the two static labels. Drives the production code path that talks to upstream
// Argo so we know the labels actually leave the handler.
func TestRegisterClusterWithArgoCD_StampsLabels(t *testing.T) {
	instanceID := uuid.New()
	clusterID := uuid.New()
	clusterServer := "https://k8s.example.test:6443"
	secretName := "cluster-k8s.example.test"

	clusterLabels := json.RawMessage(`{"tier":"prod","environment":"us-east"}`)
	queries := &argocdManagedClusterQueryStub{
		instance: sqlc.ArgocdInstance{ID: instanceID, ApiUrl: "ignored", AuthTokenEncrypted: "upstream"},
		cluster: sqlc.Cluster{
			ID:           clusterID,
			Name:         "prod-1",
			ApiServerUrl: clusterServer,
			CaCertificate: "ca",
			Environment:  "production",
			Labels:       clusterLabels,
		},
		managed: sqlc.ArgocdManagedCluster{ArgocdInstanceID: instanceID, ClusterID: clusterID},
	}

	var seenLabels map[string]string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reg struct {
			Labels map[string]string `json:"labels"`
		}
		if err := json.NewDecoder(r.Body).Decode(&reg); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		seenLabels = reg.Labels
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"prod-1","server":"` + clusterServer + `"}`))
	}))
	defer upstream.Close()
	queries.instance.ApiUrl = upstream.URL

	k8s := k8sfake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: argocdNamespace,
			Labels: map[string]string{
				argocdClusterSecretTypeLabelKey: argocdClusterSecretTypeLabelValue,
			},
		},
		Data: map[string][]byte{"server": []byte(clusterServer)},
	})

	h := NewArgoCDHandler(queries)
	h.SetKubernetesClient(k8s)

	body := `{"bearer_token":"remote-token","insecure":true}`
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/argocd/instances/"+instanceID.String()+"/clusters/"+clusterID.String()+"/register/",
		bytes.NewBufferString(body))
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("id", instanceID.String())
	routeCtx.URLParams.Add("cluster_id", clusterID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
	rr := httptest.NewRecorder()
	h.RegisterManagedCluster(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}

	want := map[string]string{
		"astronomer.io/managed-by":        "astronomer",
		"astronomer.io/cluster-id":        clusterID.String(),
		"astronomer.io/cluster-name":      "prod-1",
		"astronomer.io/environment":       "production",
		"astronomer.io/label-tier":        "prod",
		"astronomer.io/label-environment": "us-east",
	}
	for k, v := range want {
		if got := seenLabels[k]; got != v {
			t.Errorf("upstream labels[%q] = %q, want %q (full: %v)", k, got, v, seenLabels)
		}
	}
}
