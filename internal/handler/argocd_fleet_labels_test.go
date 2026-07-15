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
	argocdclient "github.com/alphabravocompany/astronomer-go/internal/handler/argocd"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
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
	cluster := sqlc.Cluster{
		ID:                id,
		Name:              "prod-east-1",
		Environment:       "production",
		Region:            "us-east-1",
		Provider:          "aws",
		Distribution:      "eks",
		AgentVersion:      "v0.4.1",
		KubernetesVersion: "v1.29.3+k3s1",
		Annotations:       json.RawMessage(`{"astronomer.io/agent-privilege-profile":"operator"}`),
	}
	labels := managedClusterLabels(cluster)

	want := map[string]string{
		"astronomer.io/managed-by":              "astronomer",
		"astronomer.io/cluster-id":              id.String(),
		"astronomer.io/cluster-name":            "prod-east-1",
		"astronomer.io/environment":             "production",
		"astronomer.io/is-local":                "false",
		"astronomer.io/region":                  "us-east-1",
		"astronomer.io/provider":                "aws",
		"astronomer.io/distribution":            "eks",
		"astronomer.io/agent-privilege-profile": "operator",
		"astronomer.io/agent-version":           "v0.4.1",
		"astronomer.io/kubernetes-version":      "v1.29.3-k3s1",
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

func TestManagedClusterLabels_ProjectMembership(t *testing.T) {
	projectA := uuid.New()
	projectB := uuid.New()
	labels := managedClusterLabelsForProjects(sqlc.Cluster{ID: uuid.New(), Name: "prod-east"}, []sqlc.Project{
		{ID: projectA, Name: "Platform"},
		{ID: projectB, Name: "Data Science"},
	})

	if _, ok := labels[astronomerProjectIDLabelKey]; ok {
		t.Fatalf("single project-id label should be omitted for multi-project clusters: %v", labels)
	}
	if _, ok := labels[astronomerProjectLabelKey]; ok {
		t.Fatalf("single project label should be omitted for multi-project clusters: %v", labels)
	}
	want := map[string]string{
		astronomerProjectIDMembershipLabelPrefix + projectA.String(): "true",
		astronomerProjectIDMembershipLabelPrefix + projectB.String(): "true",
		astronomerProjectMembershipLabelPrefix + "platform":          "true",
		astronomerProjectMembershipLabelPrefix + "data-science":      "true",
	}
	for k, v := range want {
		if got := labels[k]; got != v {
			t.Fatalf("labels[%q] = %q, want %q (full=%v)", k, got, v, labels)
		}
	}
}

// TestRegisterClusterWithArgoCD_StampsLabels — given a cluster with
// labels = {tier: prod, environment: us-east}, the created Secret carries
// astronomer.io/label-tier=prod + astronomer.io/label-environment=us-east +
// the two static labels. Drives the production code path that talks to upstream
// Argo so we know the labels actually leave the handler.
func TestRegisterClusterWithArgoCD_StampsLabels(t *testing.T) {
	defer httpclient.DisableGuardForTest()() // reach the httptest upstream on loopback
	instanceID := uuid.New()
	clusterID := uuid.New()
	clusterServer := "https://k8s.example.test:6443"
	secretName := "cluster-k8s.example.test"

	clusterLabels := json.RawMessage(`{"tier":"prod","environment":"us-east"}`)
	queries := &argocdManagedClusterQueryStub{
		instance: sqlc.ArgocdInstance{ID: instanceID, ApiUrl: "ignored", AuthTokenEncrypted: "upstream", VerifySsl: true},
		cluster: sqlc.Cluster{
			ID:                clusterID,
			Name:              "prod-1",
			ApiServerUrl:      clusterServer,
			CaCertificate:     "ca",
			Environment:       "production",
			Region:            "us-east-1",
			Provider:          "aws",
			Distribution:      "eks",
			AgentVersion:      "v0.4.1",
			KubernetesVersion: "v1.29.3+k3s1",
			Annotations:       json.RawMessage(`{"astronomer.io/agent-privilege-profile":"operator"}`),
			Labels:            clusterLabels,
		},
		managed: sqlc.ArgocdManagedCluster{ArgocdInstanceID: instanceID, ClusterID: clusterID},
		projects: []sqlc.Project{{
			ID:   uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			Name: "platform",
		}},
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

	k8s := k8sfake.NewClientset(&corev1.Secret{
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
	h.http = upstream.Client()
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
		"astronomer.io/managed-by":                                      "astronomer",
		"astronomer.io/cluster-id":                                      clusterID.String(),
		"astronomer.io/cluster-name":                                    "prod-1",
		"astronomer.io/environment":                                     "production",
		"astronomer.io/is-local":                                        "false",
		"astronomer.io/region":                                          "us-east-1",
		"astronomer.io/provider":                                        "aws",
		"astronomer.io/distribution":                                    "eks",
		"astronomer.io/agent-privilege-profile":                         "operator",
		"astronomer.io/agent-version":                                   "v0.4.1",
		"astronomer.io/kubernetes-version":                              "v1.29.3-k3s1",
		"astronomer.io/project":                                         "platform",
		"astronomer.io/project-id":                                      "11111111-1111-1111-1111-111111111111",
		"astronomer.io/project.platform":                                "true",
		"astronomer.io/project-id.11111111-1111-1111-1111-111111111111": "true",
		"astronomer.io/label-tier":                                      "prod",
		"astronomer.io/label-environment":                               "us-east",
	}
	for k, v := range want {
		if got := seenLabels[k]; got != v {
			t.Errorf("upstream labels[%q] = %q, want %q (full: %v)", k, got, v, seenLabels)
		}
	}
}

func TestRegisterClusterWithArgoCDRejectsReservedLabelOverrides(t *testing.T) {
	instanceID := uuid.New()
	clusterID := uuid.New()
	clusterServer := "https://k8s.example.test:6443"
	queries := &argocdManagedClusterQueryStub{
		instance: sqlc.ArgocdInstance{ID: instanceID, ApiUrl: "http://127.0.0.1:1", AuthTokenEncrypted: "upstream"},
		cluster: sqlc.Cluster{
			ID:            clusterID,
			Name:          "prod-1",
			ApiServerUrl:  clusterServer,
			CaCertificate: "ca",
			Environment:   "production",
		},
		managed: sqlc.ArgocdManagedCluster{ArgocdInstanceID: instanceID, ClusterID: clusterID},
	}

	h := NewArgoCDHandler(queries)
	body := `{"bearer_token":"remote-token","insecure":true,"labels":{"astronomer.io/managed-by":"other"}}`
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/argocd/instances/"+instanceID.String()+"/clusters/"+clusterID.String()+"/register/",
		bytes.NewBufferString(body))
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("id", instanceID.String())
	routeCtx.URLParams.Add("cluster_id", clusterID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
	rr := httptest.NewRecorder()

	h.RegisterManagedCluster(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "reserved") {
		t.Fatalf("body = %s, want reserved label message", rr.Body.String())
	}
	if len(queries.createCalls) != 0 {
		t.Fatalf("managed cluster rows created despite validation failure: %d", len(queries.createCalls))
	}
}

func TestValidateApplicationSetClusterGeneratorsRequiresManagedSelector(t *testing.T) {
	valid := argocdclient.ApplicationSetSpec{
		Generators: []argocdclient.ApplicationSetGenerator{{
			Cluster: &argocdclient.ClusterGenerator{
				Selector: &argocdclient.LabelSelector{MatchLabels: map[string]string{
					"astronomer.io/managed-by":  "astronomer",
					"astronomer.io/environment": "production",
				}},
			},
		}},
	}
	if err := validateApplicationSetClusterGenerators(valid); err != nil {
		t.Fatalf("valid cluster generator rejected: %v", err)
	}

	validExpression := argocdclient.ApplicationSetSpec{
		Generators: []argocdclient.ApplicationSetGenerator{{
			Cluster: &argocdclient.ClusterGenerator{
				Selector: &argocdclient.LabelSelector{MatchExpressions: []argocdclient.LabelSelectorRequirement{{
					Key:      "astronomer.io/managed-by",
					Operator: "In",
					Values:   []string{"astronomer"},
				}}},
			},
		}},
	}
	if err := validateApplicationSetClusterGenerators(validExpression); err != nil {
		t.Fatalf("valid expression cluster generator rejected: %v", err)
	}

	noClusterGenerator := argocdclient.ApplicationSetSpec{
		Generators: []argocdclient.ApplicationSetGenerator{{List: &argocdclient.ListGenerator{Elements: []json.RawMessage{json.RawMessage(`{"name":"one"}`)}}}},
	}
	if err := validateApplicationSetClusterGenerators(noClusterGenerator); err != nil {
		t.Fatalf("non-cluster generator rejected: %v", err)
	}

	missing := argocdclient.ApplicationSetSpec{
		Generators: []argocdclient.ApplicationSetGenerator{{
			Cluster: &argocdclient.ClusterGenerator{Selector: &argocdclient.LabelSelector{MatchLabels: map[string]string{
				"astronomer.io/environment": "production",
			}}},
		}},
	}
	if err := validateApplicationSetClusterGenerators(missing); err == nil {
		t.Fatal("cluster generator without managed-by selector was accepted")
	}

	wrongValue := argocdclient.ApplicationSetSpec{
		Generators: []argocdclient.ApplicationSetGenerator{{
			Cluster: &argocdclient.ClusterGenerator{Selector: &argocdclient.LabelSelector{MatchLabels: map[string]string{
				"astronomer.io/managed-by": "someone-else",
			}}},
		}},
	}
	if err := validateApplicationSetClusterGenerators(wrongValue); err == nil {
		t.Fatal("cluster generator with wrong managed-by selector was accepted")
	}

	nestedMissing := argocdclient.ApplicationSetSpec{
		Generators: []argocdclient.ApplicationSetGenerator{{
			Matrix: &argocdclient.MatrixGenerator{Generators: []argocdclient.ApplicationSetGenerator{{
				Cluster: &argocdclient.ClusterGenerator{Selector: &argocdclient.LabelSelector{}},
			}}},
		}},
	}
	if err := validateApplicationSetClusterGenerators(nestedMissing); err == nil || !strings.Contains(err.Error(), "matrix.generators[0]") {
		t.Fatalf("nested missing selector err = %v, want matrix path", err)
	}
}
