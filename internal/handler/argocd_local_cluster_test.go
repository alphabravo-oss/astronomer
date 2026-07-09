package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"

	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	argocdclient "github.com/alphabravocompany/astronomer-go/internal/handler/argocd"
)

type argocdManagedClusterQueryStub struct {
	instance sqlc.ArgocdInstance
	cluster  sqlc.Cluster
	managed  sqlc.ArgocdManagedCluster
	projects []sqlc.Project

	decisions   map[string]sqlc.ArgocdBaselineOwnershipDecision
	createCalls []sqlc.CreateArgoCDManagedClusterParams
	deleteCalls []sqlc.DeleteArgoCDManagedClusterParams
	updateCalls []sqlc.UpdateArgoCDManagedClusterLabelsParams
}

func (q *argocdManagedClusterQueryStub) GetArgoCDInstanceByID(context.Context, uuid.UUID) (sqlc.ArgocdInstance, error) {
	return q.instance, nil
}
func (q *argocdManagedClusterQueryStub) GetArgoCDInstanceByName(context.Context, string) (sqlc.ArgocdInstance, error) {
	panic("not used")
}
func (q *argocdManagedClusterQueryStub) GetArgoCDApplicationByName(context.Context, sqlc.GetArgoCDApplicationByNameParams) (sqlc.ArgocdApplication, error) {
	panic("not used")
}
func (q *argocdManagedClusterQueryStub) ListArgoCDInstances(context.Context, sqlc.ListArgoCDInstancesParams) ([]sqlc.ArgocdInstance, error) {
	return nil, nil
}
func (q *argocdManagedClusterQueryStub) CreateArgoCDInstance(context.Context, sqlc.CreateArgoCDInstanceParams) (sqlc.ArgocdInstance, error) {
	panic("not used")
}
func (q *argocdManagedClusterQueryStub) UpdateArgoCDInstance(context.Context, sqlc.UpdateArgoCDInstanceParams) (sqlc.ArgocdInstance, error) {
	panic("not used")
}
func (q *argocdManagedClusterQueryStub) UpdateArgoCDInstanceHealth(context.Context, sqlc.UpdateArgoCDInstanceHealthParams) error {
	return nil
}
func (q *argocdManagedClusterQueryStub) DeleteArgoCDInstance(context.Context, uuid.UUID) error {
	return nil
}
func (q *argocdManagedClusterQueryStub) CountArgoCDInstances(context.Context) (int64, error) {
	return 0, nil
}
func (q *argocdManagedClusterQueryStub) ListArgoCDApplications(context.Context, sqlc.ListArgoCDApplicationsParams) ([]sqlc.ArgocdApplication, error) {
	return nil, nil
}
func (q *argocdManagedClusterQueryStub) ListAppsByInstance(context.Context, sqlc.ListAppsByInstanceParams) ([]sqlc.ArgocdApplication, error) {
	return nil, nil
}
func (q *argocdManagedClusterQueryStub) GetArgoCDApplicationByID(context.Context, uuid.UUID) (sqlc.ArgocdApplication, error) {
	return sqlc.ArgocdApplication{}, nil
}
func (q *argocdManagedClusterQueryStub) UpdateArgoCDApplication(context.Context, sqlc.UpdateArgoCDApplicationParams) (sqlc.ArgocdApplication, error) {
	return sqlc.ArgocdApplication{}, nil
}
func (q *argocdManagedClusterQueryStub) CountArgoCDApplications(context.Context) (int64, error) {
	return 0, nil
}
func (q *argocdManagedClusterQueryStub) CountAppsByInstance(context.Context, uuid.UUID) (int64, error) {
	return 0, nil
}
func (q *argocdManagedClusterQueryStub) CreateArgoCDOperation(context.Context, sqlc.CreateArgoCDOperationParams) (sqlc.ArgocdOperation, error) {
	return sqlc.ArgocdOperation{}, nil
}
func (q *argocdManagedClusterQueryStub) GetArgoCDOperation(context.Context, uuid.UUID) (sqlc.ArgocdOperation, error) {
	return sqlc.ArgocdOperation{}, nil
}
func (q *argocdManagedClusterQueryStub) ListArgoCDOperations(context.Context, sqlc.ListArgoCDOperationsParams) ([]sqlc.ArgocdOperation, error) {
	return nil, nil
}
func (q *argocdManagedClusterQueryStub) CountArgoCDOperations(context.Context, sqlc.CountArgoCDOperationsParams) (int64, error) {
	return 0, nil
}
func (q *argocdManagedClusterQueryStub) ListPendingArgoCDOperations(context.Context, int32) ([]sqlc.ArgocdOperation, error) {
	return nil, nil
}
func (q *argocdManagedClusterQueryStub) GetLatestArgoCDOperationForTarget(context.Context, sqlc.GetLatestArgoCDOperationForTargetParams) (sqlc.ArgocdOperation, error) {
	return sqlc.ArgocdOperation{}, nil
}
func (q *argocdManagedClusterQueryStub) MarkArgoCDOperationRunning(context.Context, uuid.UUID) (sqlc.ArgocdOperation, error) {
	return sqlc.ArgocdOperation{}, nil
}
func (q *argocdManagedClusterQueryStub) MarkArgoCDOperationCompleted(context.Context, uuid.UUID) (sqlc.ArgocdOperation, error) {
	return sqlc.ArgocdOperation{}, nil
}
func (q *argocdManagedClusterQueryStub) MarkArgoCDOperationFailed(context.Context, sqlc.MarkArgoCDOperationFailedParams) (sqlc.ArgocdOperation, error) {
	return sqlc.ArgocdOperation{}, nil
}
func (q *argocdManagedClusterQueryStub) MarkArgoCDOperationSuperseded(context.Context, sqlc.MarkArgoCDOperationSupersededParams) (sqlc.ArgocdOperation, error) {
	return sqlc.ArgocdOperation{}, nil
}
func (q *argocdManagedClusterQueryStub) RequeueArgoCDOperation(context.Context, uuid.UUID) (sqlc.ArgocdOperation, error) {
	return sqlc.ArgocdOperation{}, nil
}
func (q *argocdManagedClusterQueryStub) ListRunningArgoCDOperations(context.Context, int32) ([]sqlc.ArgocdOperation, error) {
	return nil, nil
}
func (q *argocdManagedClusterQueryStub) UpdateArgoCDOperationProgress(context.Context, sqlc.UpdateArgoCDOperationProgressParams) (sqlc.ArgocdOperation, error) {
	return sqlc.ArgocdOperation{}, nil
}
func (q *argocdManagedClusterQueryStub) CompleteArgoCDOperationWithResult(context.Context, sqlc.CompleteArgoCDOperationWithResultParams) (sqlc.ArgocdOperation, error) {
	return sqlc.ArgocdOperation{}, nil
}
func (q *argocdManagedClusterQueryStub) FailArgoCDOperationWithResult(context.Context, sqlc.FailArgoCDOperationWithResultParams) (sqlc.ArgocdOperation, error) {
	return sqlc.ArgocdOperation{}, nil
}
func (q *argocdManagedClusterQueryStub) CreateArgoCDOperationEvent(context.Context, sqlc.CreateArgoCDOperationEventParams) (sqlc.ArgocdOperationEvent, error) {
	return sqlc.ArgocdOperationEvent{}, nil
}
func (q *argocdManagedClusterQueryStub) ListArgoCDOperationEvents(context.Context, uuid.UUID) ([]sqlc.ArgocdOperationEvent, error) {
	return nil, nil
}
func (q *argocdManagedClusterQueryStub) GetClusterByID(context.Context, uuid.UUID) (sqlc.Cluster, error) {
	return q.cluster, nil
}
func (q *argocdManagedClusterQueryStub) ListProjectsByCluster(context.Context, sqlc.ListProjectsByClusterParams) ([]sqlc.Project, error) {
	return q.projects, nil
}
func (q *argocdManagedClusterQueryStub) CreateArgoCDManagedCluster(_ context.Context, arg sqlc.CreateArgoCDManagedClusterParams) (sqlc.ArgocdManagedCluster, error) {
	q.createCalls = append(q.createCalls, arg)
	q.managed.ClusterSecretName = arg.ClusterSecretName
	q.managed.ServerUrl = arg.ServerUrl
	q.managed.Labels = arg.Labels
	return q.managed, nil
}
func (q *argocdManagedClusterQueryStub) GetArgoCDManagedCluster(context.Context, sqlc.GetArgoCDManagedClusterParams) (sqlc.ArgocdManagedCluster, error) {
	return q.managed, nil
}
func (q *argocdManagedClusterQueryStub) ListArgoCDManagedClusters(context.Context, uuid.UUID) ([]sqlc.ArgocdManagedCluster, error) {
	return []sqlc.ArgocdManagedCluster{q.managed}, nil
}
func (q *argocdManagedClusterQueryStub) ListArgoCDManagedClustersByCluster(context.Context, uuid.UUID) ([]sqlc.ArgocdManagedCluster, error) {
	if q.managed.ID == uuid.Nil && q.managed.ArgocdInstanceID == uuid.Nil {
		return nil, nil
	}
	return []sqlc.ArgocdManagedCluster{q.managed}, nil
}
func (q *argocdManagedClusterQueryStub) DeleteArgoCDManagedCluster(_ context.Context, arg sqlc.DeleteArgoCDManagedClusterParams) error {
	q.deleteCalls = append(q.deleteCalls, arg)
	return nil
}
func (q *argocdManagedClusterQueryStub) UpdateArgoCDManagedClusterLabels(_ context.Context, arg sqlc.UpdateArgoCDManagedClusterLabelsParams) (sqlc.ArgocdManagedCluster, error) {
	q.updateCalls = append(q.updateCalls, arg)
	q.managed.Labels = arg.Labels
	return q.managed, nil
}
func (q *argocdManagedClusterQueryStub) ListArgoCDBaselineOwnershipDecisions(context.Context, uuid.UUID) ([]sqlc.ArgocdBaselineOwnershipDecision, error) {
	out := make([]sqlc.ArgocdBaselineOwnershipDecision, 0, len(q.decisions))
	for _, row := range q.decisions {
		out = append(out, row)
	}
	return out, nil
}
func (q *argocdManagedClusterQueryStub) UpsertArgoCDBaselineOwnershipDecision(_ context.Context, arg sqlc.UpsertArgoCDBaselineOwnershipDecisionParams) (sqlc.ArgocdBaselineOwnershipDecision, error) {
	if q.decisions == nil {
		q.decisions = map[string]sqlc.ArgocdBaselineOwnershipDecision{}
	}
	row := sqlc.ArgocdBaselineOwnershipDecision{
		ID:            uuid.New(),
		ClusterID:     arg.ClusterID,
		ComponentSlug: arg.ComponentSlug,
		Decision:      arg.Decision,
		Reason:        arg.Reason,
		ExpiresAt:     arg.ExpiresAt,
		DecidedByID:   arg.DecidedByID,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	q.decisions[arg.ComponentSlug] = row
	return row, nil
}

func TestRegisterManagedClusterLocalAutoToken(t *testing.T) {
	defer httpclient.DisableGuardForTest()()
	instanceID := uuid.New()
	clusterID := uuid.New()
	clusterServer := "https://10.43.0.1:443"
	secretName := "cluster-10.43.0.1-1704193794"
	queries := &argocdManagedClusterQueryStub{
		instance: sqlc.ArgocdInstance{ID: instanceID, ApiUrl: "http://argocd.example.test", AuthTokenEncrypted: "upstream-token"},
		cluster:  sqlc.Cluster{ID: clusterID, Name: "local", ApiServerUrl: clusterServer, CaCertificate: "ca-bytes", Environment: "production", IsLocal: true},
		managed:  sqlc.ArgocdManagedCluster{ArgocdInstanceID: instanceID, ClusterID: clusterID},
	}

	var seen struct {
		BearerToken string
		Server      string
		Query       string
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/clusters" {
			t.Fatalf("unexpected upstream request: %s %s", r.Method, r.URL.Path)
		}
		var reg struct {
			Server string `json:"server"`
			Config struct {
				BearerToken string `json:"bearerToken"`
			} `json:"config"`
		}
		if err := json.NewDecoder(r.Body).Decode(&reg); err != nil {
			t.Fatalf("decode upstream registration: %v", err)
		}
		seen.Server = reg.Server
		seen.BearerToken = reg.Config.BearerToken
		seen.Query = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"local","server":"` + clusterServer + `"}`))
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
		Data: map[string][]byte{
			"server": []byte(clusterServer),
		},
	})
	k8s.PrependReactor("create", "serviceaccounts", func(action k8stesting.Action) (bool, runtime.Object, error) {
		createAction := action.(k8stesting.CreateAction)
		if createAction.GetSubresource() != "token" {
			return false, nil, nil
		}
		return true, &authenticationv1.TokenRequest{
			Status: authenticationv1.TokenRequestStatus{Token: "fresh-local-token"},
		}, nil
	})

	h := NewArgoCDHandler(queries)
	h.SetKubernetesClient(k8s)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/argocd/instances/"+instanceID.String()+"/clusters/"+clusterID.String()+"/register/", bytes.NewBufferString(`{"labels":{"team":"platform"}}`))
	req = req.WithContext(context.Background())
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("id", instanceID.String())
	routeCtx.URLParams.Add("cluster_id", clusterID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
	rr := httptest.NewRecorder()

	h.RegisterManagedCluster(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if seen.BearerToken != "fresh-local-token" {
		t.Fatalf("bearer token = %q", seen.BearerToken)
	}
	if seen.Server != clusterServer {
		t.Fatalf("server = %q", seen.Server)
	}
	if seen.Query != "upsert=true" {
		t.Fatalf("query = %q", seen.Query)
	}
	if len(queries.createCalls) != 1 {
		t.Fatalf("want 1 managed-cluster upsert, got %d", len(queries.createCalls))
	}
	if queries.createCalls[0].ClusterSecretName != secretName {
		t.Fatalf("cluster_secret_name = %q", queries.createCalls[0].ClusterSecretName)
	}
}

func TestRegisterManagedClusterRemoteDefaultsToTunnelProxyURL(t *testing.T) {
	defer httpclient.DisableGuardForTest()()
	instanceID := uuid.New()
	clusterID := uuid.New()
	expectedServer := "http://astronomer-server.astronomer.svc.cluster.local:8000/api/v1/clusters/" + clusterID.String() + "/k8s"
	queries := &argocdManagedClusterQueryStub{
		instance: sqlc.ArgocdInstance{ID: instanceID, ApiUrl: "http://argocd.example.test", AuthTokenEncrypted: "upstream-token"},
		cluster:  sqlc.Cluster{ID: clusterID, Name: "dev", CaCertificate: "ca-bytes", Environment: "dev", IsLocal: false},
		managed:  sqlc.ArgocdManagedCluster{ArgocdInstanceID: instanceID, ClusterID: clusterID},
	}

	var seen struct {
		Server      string
		BearerToken string
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reg struct {
			Server string `json:"server"`
			Config struct {
				BearerToken string `json:"bearerToken"`
			} `json:"config"`
		}
		if err := json.NewDecoder(r.Body).Decode(&reg); err != nil {
			t.Fatalf("decode upstream registration: %v", err)
		}
		seen.Server = reg.Server
		seen.BearerToken = reg.Config.BearerToken
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"dev","server":"` + expectedServer + `"}`))
	}))
	defer upstream.Close()
	queries.instance.ApiUrl = upstream.URL

	k8s := k8sfake.NewClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster-dev",
			Namespace: argocdNamespace,
			Labels: map[string]string{
				argocdClusterSecretTypeLabelKey: argocdClusterSecretTypeLabelValue,
			},
		},
		Data: map[string][]byte{
			"server": []byte(expectedServer),
		},
	})

	h := NewArgoCDHandler(queries)
	h.SetKubernetesClient(k8s)
	h.SetClusterProxyBaseURL("http://astronomer-server.astronomer.svc.cluster.local:8000")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/argocd/instances/"+instanceID.String()+"/clusters/"+clusterID.String()+"/register/", bytes.NewBufferString(`{"bearer_token":"remote-token","insecure":true}`))
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("id", instanceID.String())
	routeCtx.URLParams.Add("cluster_id", clusterID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
	rr := httptest.NewRecorder()

	h.RegisterManagedCluster(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if seen.Server != expectedServer {
		t.Fatalf("server = %q", seen.Server)
	}
	if seen.BearerToken != "remote-token" {
		t.Fatalf("bearer token = %q", seen.BearerToken)
	}
}

func TestRefreshManagedClusterLabelsRestampsSecretAndDB(t *testing.T) {
	instanceID := uuid.New()
	clusterID := uuid.New()
	server := "https://k8s.example.test:6443"
	secretName := "cluster-k8s.example.test"
	queries := &argocdManagedClusterQueryStub{
		instance: sqlc.ArgocdInstance{ID: instanceID, ApiUrl: "http://argocd.example.test", AuthTokenEncrypted: "upstream-token"},
		cluster: sqlc.Cluster{
			ID:           clusterID,
			Name:         "prod-1",
			Environment:  "production",
			Region:       "us-east-1",
			Provider:     "aws",
			Distribution: "eks",
			Annotations:  json.RawMessage(`{"astronomer.io/agent-privilege-profile":"viewer"}`),
			Labels:       json.RawMessage(`{"tier":"prod","Team Name":"platform"}`),
		},
		managed: sqlc.ArgocdManagedCluster{
			ID:                uuid.New(),
			ArgocdInstanceID:  instanceID,
			ClusterID:         clusterID,
			ClusterSecretName: secretName,
			ServerUrl:         server,
			Labels:            []byte(`{"astronomer.io/label-tier":"old"}`),
			CreatedAt:         time.Now(),
			UpdatedAt:         time.Now(),
		},
	}
	k8s := k8sfake.NewClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: argocdNamespace,
			Labels: map[string]string{
				argocdClusterSecretTypeLabelKey: argocdClusterSecretTypeLabelValue,
				"owner":                         "platform",
				"astronomer.io/cluster-name":    "stale-name",
				"astronomer.io/label-tier":      "old",
				"astronomer.io/label-obsolete":  "remove-me",
			},
		},
		Data: map[string][]byte{"server": []byte(server)},
	})
	h := NewArgoCDHandler(queries)
	h.SetKubernetesClient(k8s)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/argocd/instances/"+instanceID.String()+"/clusters/"+clusterID.String()+"/refresh-labels/", nil)
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("id", instanceID.String())
	routeCtx.URLParams.Add("cluster_id", clusterID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
	rr := httptest.NewRecorder()

	h.RefreshManagedClusterLabels(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	secret, err := k8s.CoreV1().Secrets(argocdNamespace).Get(context.Background(), secretName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get patched secret: %v", err)
	}
	want := map[string]string{
		"owner":                                 "platform",
		argocdClusterSecretTypeLabelKey:         argocdClusterSecretTypeLabelValue,
		"astronomer.io/managed-by":              "astronomer",
		"astronomer.io/cluster-id":              clusterID.String(),
		"astronomer.io/cluster-name":            "prod-1",
		"astronomer.io/is-local":                "false",
		"astronomer.io/environment":             "production",
		"astronomer.io/region":                  "us-east-1",
		"astronomer.io/provider":                "aws",
		"astronomer.io/distribution":            "eks",
		"astronomer.io/agent-privilege-profile": "viewer",
		"astronomer.io/label-tier":              "prod",
		"astronomer.io/label-team-name":         "platform",
	}
	for k, v := range want {
		if got := secret.Labels[k]; got != v {
			t.Errorf("secret labels[%q] = %q, want %q (full: %v)", k, got, v, secret.Labels)
		}
	}
	if _, exists := secret.Labels["astronomer.io/label-obsolete"]; exists {
		t.Errorf("stale Astronomer label was not removed: %v", secret.Labels)
	}
	if len(queries.updateCalls) != 1 {
		t.Fatalf("want 1 DB label update, got %d", len(queries.updateCalls))
	}
	var dbLabels map[string]string
	if err := json.Unmarshal(queries.updateCalls[0].Labels, &dbLabels); err != nil {
		t.Fatalf("unmarshal updated labels: %v", err)
	}
	if got := dbLabels["astronomer.io/label-tier"]; got != "prod" {
		t.Fatalf("db label tier = %q, want prod (full: %v)", got, dbLabels)
	}
	if _, exists := dbLabels["owner"]; exists {
		t.Fatalf("non-Astronomer Secret label leaked into DB labels: %v", dbLabels)
	}
}

func TestRefreshLocalManagedClusterRegistrationMigratesServerAndSecretName(t *testing.T) {
	defer httpclient.DisableGuardForTest()()
	instanceID := uuid.New()
	clusterID := uuid.New()
	oldServer := "https://kubernetes.default.svc"
	newServer := "https://10.43.0.1:443"
	newSecretName := "cluster-10.43.0.1-1704193794"
	queries := &argocdManagedClusterQueryStub{
		cluster: sqlc.Cluster{
			ID:            clusterID,
			Name:          "local",
			ApiServerUrl:  newServer,
			CaCertificate: "ca-bytes",
			Environment:   "production",
			Region:        "us-east-1",
			Provider:      "local",
			Distribution:  "k3s",
			Annotations:   json.RawMessage(`{"astronomer.io/agent-privilege-profile":"operator"}`),
			IsLocal:       true,
		},
		managed: sqlc.ArgocdManagedCluster{
			ArgocdInstanceID:  instanceID,
			ClusterID:         clusterID,
			ClusterSecretName: "local",
			ServerUrl:         oldServer,
			Labels:            []byte(`{"team":"platform","astronomer.io/managed-by":"other","argocd.argoproj.io/secret-type":"not-cluster"}`),
		},
	}

	var requests []string
	var seenLabels map[string]string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			var reg struct {
				Labels map[string]string `json:"labels"`
			}
			if err := json.NewDecoder(r.Body).Decode(&reg); err != nil {
				t.Fatalf("decode upstream registration: %v", err)
			}
			seenLabels = reg.Labels
			_, _ = w.Write([]byte(`{"name":"local","server":"` + newServer + `"}`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	k8s := k8sfake.NewClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      newSecretName,
			Namespace: argocdNamespace,
			Labels: map[string]string{
				argocdClusterSecretTypeLabelKey: argocdClusterSecretTypeLabelValue,
			},
		},
		Data: map[string][]byte{
			"server": []byte(newServer),
		},
	})
	k8s.PrependReactor("create", "serviceaccounts", func(action k8stesting.Action) (bool, runtime.Object, error) {
		createAction := action.(k8stesting.CreateAction)
		if createAction.GetSubresource() != "token" {
			return false, nil, nil
		}
		return true, &authenticationv1.TokenRequest{
			Status: authenticationv1.TokenRequestStatus{Token: "fresh-local-token"},
		}, nil
	})

	h := NewArgoCDHandler(queries)
	h.SetKubernetesClient(k8s)

	err := h.refreshLocalManagedClusterRegistration(context.Background(), argocdclient.NewClient(upstream.URL, "upstream-token", argocdclient.Options{HTTPClient: upstream.Client()}), instanceID, queries.cluster, queries.managed)
	if err != nil {
		t.Fatalf("refreshLocalManagedClusterRegistration: %v", err)
	}
	if len(queries.createCalls) != 1 {
		t.Fatalf("want 1 managed-cluster upsert, got %d", len(queries.createCalls))
	}
	if queries.createCalls[0].ServerUrl != newServer {
		t.Fatalf("server_url = %q", queries.createCalls[0].ServerUrl)
	}
	if queries.createCalls[0].ClusterSecretName != newSecretName {
		t.Fatalf("cluster_secret_name = %q", queries.createCalls[0].ClusterSecretName)
	}
	if got := seenLabels["astronomer.io/managed-by"]; got != "astronomer" {
		t.Fatalf("managed-by label = %q, want astronomer (labels=%v)", got, seenLabels)
	}
	if got := seenLabels["team"]; got != "platform" {
		t.Fatalf("team label = %q, want platform (labels=%v)", got, seenLabels)
	}
	if got := seenLabels["astronomer.io/region"]; got != "us-east-1" {
		t.Fatalf("region label = %q, want us-east-1 (labels=%v)", got, seenLabels)
	}
	if got := seenLabels["astronomer.io/provider"]; got != "local" {
		t.Fatalf("provider label = %q, want local (labels=%v)", got, seenLabels)
	}
	if got := seenLabels["astronomer.io/distribution"]; got != "k3s" {
		t.Fatalf("distribution label = %q, want k3s (labels=%v)", got, seenLabels)
	}
	if got := seenLabels["astronomer.io/agent-privilege-profile"]; got != "operator" {
		t.Fatalf("agent profile label = %q, want operator (labels=%v)", got, seenLabels)
	}
	if _, exists := seenLabels["argocd.argoproj.io/secret-type"]; exists {
		t.Fatalf("reserved ArgoCD label replayed: %v", seenLabels)
	}
	if len(requests) != 2 || requests[0] != "POST /api/v1/clusters" || requests[1] != "DELETE /api/v1/clusters/https://kubernetes.default.svc" {
		t.Fatalf("unexpected upstream requests: %#v", requests)
	}
}

func TestLocalManagedClusterNeedsRefreshWhenTokenNearExpiry(t *testing.T) {
	exp := time.Now().Add(30 * time.Minute).Unix()
	token := `header.` + mustJWTClaims(t, map[string]any{"exp": exp}) + `.sig`
	k8s := k8sfake.NewClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster-local",
			Namespace: argocdNamespace,
			Labels: map[string]string{
				argocdClusterSecretTypeLabelKey: argocdClusterSecretTypeLabelValue,
			},
		},
		Data: map[string][]byte{
			"server": []byte("https://10.43.0.1:443"),
			"config": []byte(`{"bearerToken":"` + token + `"}`),
		},
	})
	h := NewArgoCDHandler(&argocdManagedClusterQueryStub{})
	h.SetKubernetesClient(k8s)

	refresh, err := h.localManagedClusterNeedsRefresh(context.Background(), sqlc.ArgocdManagedCluster{
		ClusterSecretName: "cluster-local",
		ServerUrl:         "https://10.43.0.1:443",
	}, "https://10.43.0.1:443")
	if err != nil {
		t.Fatalf("localManagedClusterNeedsRefresh: %v", err)
	}
	if !refresh {
		t.Fatal("expected expiring token to require refresh")
	}
}

func mustJWTClaims(t *testing.T, claims map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	return base64URLEncode(raw)
}

func base64URLEncode(raw []byte) string {
	enc := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	out := make([]byte, 0, ((len(raw)+2)/3)*4)
	for i := 0; i < len(raw); i += 3 {
		var b0, b1, b2 byte
		b0 = raw[i]
		if i+1 < len(raw) {
			b1 = raw[i+1]
		}
		if i+2 < len(raw) {
			b2 = raw[i+2]
		}
		out = append(out, enc[b0>>2])
		out = append(out, enc[((b0&0x03)<<4)|(b1>>4)])
		if i+1 < len(raw) {
			out = append(out, enc[((b1&0x0f)<<2)|(b2>>6)])
		}
		if i+2 < len(raw) {
			out = append(out, enc[b2&0x3f])
		}
	}
	return string(out)
}
