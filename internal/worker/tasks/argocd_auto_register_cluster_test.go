package tasks

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/argolabels"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	authv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/registration"
)

type argoCDAutoRegisterTestQuerier struct {
	setting           *sqlc.PlatformSetting
	selectorSetting   *sqlc.PlatformSetting
	listTouched       bool
	cluster           sqlc.Cluster
	clusters          []sqlc.Cluster
	projects          []sqlc.Project
	instances         []sqlc.ArgocdInstance
	managed           []sqlc.ArgocdManagedCluster
	created           []sqlc.CreateArgoCDManagedClusterParams
	conditions        []sqlc.UpsertClusterConditionParams
	managedLookup     bool
	repairSuccess     []sqlc.RecordRepairJobSuccessParams
	repairFailure     []sqlc.RecordRepairJobFailureParams
	activeProxyToken  *sqlc.ArgocdClusterProxyToken
	proxyTokenUpserts []sqlc.UpsertArgoCDClusterProxyTokenParams
}

func (q *argoCDAutoRegisterTestQuerier) GetClusterByID(_ context.Context, id uuid.UUID) (sqlc.Cluster, error) {
	if q.cluster.ID == id {
		return q.cluster, nil
	}
	return sqlc.Cluster{}, pgx.ErrNoRows
}

func (q *argoCDAutoRegisterTestQuerier) ListClusters(context.Context, sqlc.ListClustersParams) ([]sqlc.Cluster, error) {
	q.listTouched = true
	return q.clusters, nil
}

func (q *argoCDAutoRegisterTestQuerier) ListProjectsByCluster(context.Context, sqlc.ListProjectsByClusterParams) ([]sqlc.Project, error) {
	return q.projects, nil
}

func (q *argoCDAutoRegisterTestQuerier) GetPlatformSetting(_ context.Context, key string) (sqlc.PlatformSetting, error) {
	if key == platformSettingArgoCDAutoRegSelector {
		if q.selectorSetting == nil {
			return sqlc.PlatformSetting{}, pgx.ErrNoRows
		}
		return *q.selectorSetting, nil
	}
	if q.setting == nil {
		return sqlc.PlatformSetting{}, pgx.ErrNoRows
	}
	return *q.setting, nil
}

func (q *argoCDAutoRegisterTestQuerier) ListArgoCDInstances(context.Context, sqlc.ListArgoCDInstancesParams) ([]sqlc.ArgocdInstance, error) {
	return q.instances, nil
}

func (q *argoCDAutoRegisterTestQuerier) CreateArgoCDManagedCluster(_ context.Context, arg sqlc.CreateArgoCDManagedClusterParams) (sqlc.ArgocdManagedCluster, error) {
	q.created = append(q.created, arg)
	row := sqlc.ArgocdManagedCluster{
		ArgocdInstanceID:  arg.ArgocdInstanceID,
		ClusterID:         arg.ClusterID,
		ClusterSecretName: arg.ClusterSecretName,
		ServerUrl:         arg.ServerUrl,
		Labels:            arg.Labels,
	}
	q.managed = append(q.managed, row)
	return row, nil
}

func (q *argoCDAutoRegisterTestQuerier) GetActiveArgoCDClusterProxyTokenByClusterID(_ context.Context, clusterID uuid.UUID) (sqlc.ArgocdClusterProxyToken, error) {
	if q.activeProxyToken != nil && q.activeProxyToken.ClusterID == clusterID {
		return *q.activeProxyToken, nil
	}
	return sqlc.ArgocdClusterProxyToken{}, pgx.ErrNoRows
}

func (q *argoCDAutoRegisterTestQuerier) UpsertArgoCDClusterProxyToken(_ context.Context, arg sqlc.UpsertArgoCDClusterProxyTokenParams) (sqlc.ArgocdClusterProxyToken, error) {
	q.proxyTokenUpserts = append(q.proxyTokenUpserts, arg)
	return sqlc.ArgocdClusterProxyToken{ClusterID: arg.ClusterID, TokenHash: arg.TokenHash, TokenEncrypted: arg.TokenEncrypted, ExpiresAt: arg.ExpiresAt}, nil
}

func (q *argoCDAutoRegisterTestQuerier) UpsertClusterCondition(_ context.Context, arg sqlc.UpsertClusterConditionParams) (sqlc.ClusterCondition, error) {
	q.conditions = append(q.conditions, arg)
	return sqlc.ClusterCondition{ClusterID: arg.ClusterID, Type: arg.Type, Status: arg.Status, Reason: arg.Reason, Message: arg.Message}, nil
}

func (q *argoCDAutoRegisterTestQuerier) ListArgoCDManagedClustersByCluster(_ context.Context, _ uuid.UUID) ([]sqlc.ArgocdManagedCluster, error) {
	q.managedLookup = true
	return q.managed, nil
}

func (q *argoCDAutoRegisterTestQuerier) RecordRepairJobSuccess(_ context.Context, arg sqlc.RecordRepairJobSuccessParams) (sqlc.RepairJobState, error) {
	q.repairSuccess = append(q.repairSuccess, arg)
	return sqlc.RepairJobState{}, nil
}

func (q *argoCDAutoRegisterTestQuerier) RecordRepairJobFailure(_ context.Context, arg sqlc.RecordRepairJobFailureParams) (sqlc.RepairJobState, error) {
	q.repairFailure = append(q.repairFailure, arg)
	return sqlc.RepairJobState{}, nil
}

type argoCDTimelineRecorder struct {
	steps []registration.StepInput
}

func (r *argoCDTimelineRecorder) WriteStep(_ context.Context, _ uuid.UUID, in registration.StepInput) (sqlc.ClusterRegistrationStep, error) {
	r.steps = append(r.steps, in)
	return sqlc.ClusterRegistrationStep{}, nil
}

func TestArgoCDAutoRegisterSkipsWhenSettingDisabled(t *testing.T) {
	ResetArgoCDAutoRegister()
	t.Cleanup(ResetArgoCDAutoRegister)
	q := &argoCDAutoRegisterTestQuerier{setting: boolPlatformSetting(false)}
	ConfigureArgoCDAutoRegister(ArgoCDAutoRegisterDeps{Queries: q})

	if err := HandleArgoCDAutoRegisterCluster(context.Background(), asynq.NewTask(ArgoCDAutoRegisterClusterType, nil)); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if q.listTouched {
		t.Fatal("expected worker to skip cluster listing when auto-adoption is disabled")
	}
}

func TestArgoCDAutoRegisterNoInstanceWritesTimelineFailureAndCondition(t *testing.T) {
	ResetArgoCDAutoRegister()
	t.Cleanup(ResetArgoCDAutoRegister)
	clusterID := uuid.New()
	q := &argoCDAutoRegisterTestQuerier{
		cluster: sqlc.Cluster{
			ID:            clusterID,
			Name:          "prod-1",
			LastHeartbeat: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
		},
	}
	timeline := &argoCDTimelineRecorder{}
	ConfigureArgoCDAutoRegister(ArgoCDAutoRegisterDeps{Queries: q, Registration: timeline})

	task, err := NewArgoCDAutoRegisterClusterTask(clusterID)
	if err != nil {
		t.Fatalf("task: %v", err)
	}
	if err := HandleArgoCDAutoRegisterCluster(context.Background(), task); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(timeline.steps) != 2 {
		t.Fatalf("steps = %d, want registering + failed", len(timeline.steps))
	}
	if timeline.steps[0].StepName != "argocd_registering" || timeline.steps[0].Status != "running" {
		t.Fatalf("first step = %+v", timeline.steps[0])
	}
	if timeline.steps[1].StepName != "argocd_registration_failed" || timeline.steps[1].Status != "failed" {
		t.Fatalf("second step = %+v", timeline.steps[1])
	}
	if len(q.conditions) != 2 {
		t.Fatalf("conditions = %d, want Unknown + False", len(q.conditions))
	}
	if q.conditions[0].Type != ConditionArgoCDAdopted || q.conditions[0].Status != "Unknown" {
		t.Fatalf("first condition = %+v", q.conditions[0])
	}
	if q.conditions[1].Type != ConditionArgoCDAdopted || q.conditions[1].Status != "False" || q.conditions[1].Reason != "RegistrationFailed" {
		t.Fatalf("second condition = %+v", q.conditions[1])
	}
}

func TestArgoCDAutoRegisterAlreadyManagedDoesNotSpamTimeline(t *testing.T) {
	ResetArgoCDAutoRegister()
	t.Cleanup(ResetArgoCDAutoRegister)
	clusterID := uuid.New()
	q := &argoCDAutoRegisterTestQuerier{
		cluster: sqlc.Cluster{
			ID:            clusterID,
			Name:          "prod-1",
			LastHeartbeat: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
		},
		managed: []sqlc.ArgocdManagedCluster{{ClusterID: clusterID}},
	}
	timeline := &argoCDTimelineRecorder{}
	ConfigureArgoCDAutoRegister(ArgoCDAutoRegisterDeps{Queries: q, Registration: timeline})

	task, err := NewArgoCDAutoRegisterClusterTask(clusterID)
	if err != nil {
		t.Fatalf("task: %v", err)
	}
	if err := HandleArgoCDAutoRegisterCluster(context.Background(), task); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if !q.managedLookup {
		t.Fatal("expected managed-cluster lookup")
	}
	if len(timeline.steps) != 0 {
		t.Fatalf("steps = %+v, want none for already-managed sweep", timeline.steps)
	}
	if len(q.conditions) != 1 || q.conditions[0].Status != "True" || q.conditions[0].Reason != "Registered" {
		t.Fatalf("conditions = %+v, want Registered True", q.conditions)
	}
}

func TestArgoCDAutoRegisterDefaultsEnabledWhenSettingMissing(t *testing.T) {
	ResetArgoCDAutoRegister()
	t.Cleanup(ResetArgoCDAutoRegister)
	q := &argoCDAutoRegisterTestQuerier{}
	ConfigureArgoCDAutoRegister(ArgoCDAutoRegisterDeps{Queries: q})

	if err := HandleArgoCDAutoRegisterCluster(context.Background(), asynq.NewTask(ArgoCDAutoRegisterClusterType, nil)); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if !q.listTouched {
		t.Fatal("expected worker to list clusters when auto-adoption setting is absent")
	}
}

// The fleet sweep must be leader-gated: a non-leader replica must NOT run the
// per-cluster token-minting/index-repair body (which otherwise races sibling
// replicas on proxy-token upserts). The enqueued per-cluster path stays
// unguarded, but the nil-cluster_id sweep is skipped off the lease holder.
func TestArgoCDAutoRegisterSweepSkippedOnNonLeader(t *testing.T) {
	ResetArgoCDAutoRegister()
	t.Cleanup(ResetArgoCDAutoRegister)
	savedRuntime := runtimeDeps
	t.Cleanup(func() { runtimeDeps = savedRuntime })

	q := &argoCDAutoRegisterTestQuerier{setting: boolPlatformSetting(true)}
	ConfigureArgoCDAutoRegister(ArgoCDAutoRegisterDeps{Queries: q})
	runtimeDeps = RuntimeDependencies{Leader: &fakeLeader{held: false}}

	if err := HandleArgoCDAutoRegisterCluster(context.Background(), asynq.NewTask(ArgoCDAutoRegisterClusterType, nil)); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if q.listTouched {
		t.Fatal("non-leader replica must not run the fleet sweep (ListClusters was called)")
	}

	// Sanity: the same call on the lease holder DOES run the sweep.
	q2 := &argoCDAutoRegisterTestQuerier{setting: boolPlatformSetting(true)}
	ResetArgoCDAutoRegister()
	ConfigureArgoCDAutoRegister(ArgoCDAutoRegisterDeps{Queries: q2})
	runtimeDeps = RuntimeDependencies{Leader: &fakeLeader{held: true}}
	if err := HandleArgoCDAutoRegisterCluster(context.Background(), asynq.NewTask(ArgoCDAutoRegisterClusterType, nil)); err != nil {
		t.Fatalf("handle (leader): %v", err)
	}
	if !q2.listTouched {
		t.Fatal("lease holder must run the fleet sweep")
	}
}

func TestArgoCDAutoRegisterSweepUpsertsClusterWithStandardLabels(t *testing.T) {
	// This test intentionally dials the loopback httptest Argo CD upstream.
	defer httpclient.DisableGuardForTest()()

	ResetArgoCDAutoRegister()
	t.Cleanup(ResetArgoCDAutoRegister)
	clusterID := uuid.New()
	instanceID := uuid.New()
	key, err := auth.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	encryptor, err := auth.NewEncryptor(key)
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}
	instanceToken, err := encryptor.Encrypt("argocd-token")
	if err != nil {
		t.Fatalf("encrypt instance token: %v", err)
	}

	var seenPath string
	var seenAuth string
	var seenRegistration struct {
		Server string `json:"server"`
		Name   string `json:"name"`
		Config struct {
			BearerToken string `json:"bearerToken"`
		} `json:"config"`
		Labels map[string]string `json:"labels"`
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.RequestURI()
		seenAuth = r.Header.Get("Authorization")
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/clusters" {
			t.Fatalf("unexpected upstream request: %s %s", r.Method, r.URL.RequestURI())
		}
		if err := json.NewDecoder(r.Body).Decode(&seenRegistration); err != nil {
			t.Fatalf("decode registration: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name":   "cluster-prod-east",
			"server": seenRegistration.Server,
		})
	}))
	defer upstream.Close()

	cluster := sqlc.Cluster{
		ID:                clusterID,
		Name:              "prod-east",
		Environment:       "production",
		Region:            "us-east-1",
		Provider:          "aws",
		Distribution:      "eks",
		AgentVersion:      "v0.4.1",
		KubernetesVersion: "v1.29.3+k3s1",
		Annotations:       json.RawMessage(`{"astronomer.io/agent-privilege-profile":"operator"}`),
		Labels:            json.RawMessage(`{"tier":"prod","Team Name":"platform"}`),
		LastHeartbeat:     pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	}
	q := &argoCDAutoRegisterTestQuerier{
		clusters: []sqlc.Cluster{cluster},
		projects: []sqlc.Project{{
			ID:   uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			Name: "platform",
		}},
		instances: []sqlc.ArgocdInstance{{
			ID:                 instanceID,
			Name:               "built-in",
			ApiUrl:             upstream.URL,
			AuthTokenEncrypted: instanceToken,
		}},
		managed: []sqlc.ArgocdManagedCluster{{
			ArgocdInstanceID:  instanceID,
			ClusterID:         clusterID,
			ClusterSecretName: "cluster-prod-east",
		}},
	}
	k8s := k8sfake.NewClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster-prod-east",
			Namespace: argoCDNamespace,
			Labels: map[string]string{
				argoCDClusterSecretTypeLabel:   argoCDClusterSecretTypeValue,
				astronomerManagedByLabelKey:    astronomerManagedByLabelValue,
				astronomerClusterIDLabelKey:    clusterID.String(),
				astronomerClusterNameLabelKey:  "prod-east",
				astronomerEnvironmentLabelKey:  "staging",
				astronomerProjectIDLabelKey:    "22222222-2222-2222-2222-222222222222",
				astronomerLabelPrefix + "tier": "dev",
			},
		},
		Data: map[string][]byte{"server": []byte("https://old-proxy.example")},
	})
	timeline := &argoCDTimelineRecorder{}
	ConfigureArgoCDAutoRegister(ArgoCDAutoRegisterDeps{
		Queries:             q,
		Encryptor:           encryptor,
		K8s:                 k8s,
		ClusterProxyBaseURL: "https://astronomer.example",
		Registration:        timeline,
	})

	if err := HandleArgoCDAutoRegisterCluster(context.Background(), asynq.NewTask(ArgoCDAutoRegisterClusterType, nil)); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if seenPath != "/api/v1/clusters?upsert=true" {
		t.Fatalf("upstream path = %q, want upsert registration", seenPath)
	}
	if seenAuth != "Bearer argocd-token" {
		t.Fatalf("Authorization = %q, want decrypted Argo token", seenAuth)
	}
	wantServer := "https://astronomer.example/api/v1/internal/argocd/clusters/" + clusterID.String() + "/k8s"
	if seenRegistration.Server != wantServer {
		t.Fatalf("registered server = %q, want %q", seenRegistration.Server, wantServer)
	}
	if !strings.HasPrefix(seenRegistration.Config.BearerToken, auth.ArgoCDClusterProxyTokenPrefix) {
		t.Fatalf("registration bearer token prefix = %q, want %q", seenRegistration.Config.BearerToken, auth.ArgoCDClusterProxyTokenPrefix)
	}
	if len(q.proxyTokenUpserts) != 1 {
		t.Fatalf("proxy token upserts = %d, want 1 when the DB row is missing", len(q.proxyTokenUpserts))
	}
	if q.proxyTokenUpserts[0].ClusterID != clusterID || q.proxyTokenUpserts[0].TokenHash != auth.HashArgoCDClusterProxyToken(seenRegistration.Config.BearerToken) {
		t.Fatalf("proxy token upsert does not match registration credential: %+v", q.proxyTokenUpserts[0])
	}
	wantLabels := map[string]string{
		astronomerManagedByLabelKey:                         astronomerManagedByLabelValue,
		astronomerClusterIDLabelKey:                         clusterID.String(),
		astronomerClusterNameLabelKey:                       "prod-east",
		astronomerIsLocalLabelKey:                           "false",
		astronomerEnvironmentLabelKey:                       "production",
		astronomerRegionLabelKey:                            "us-east-1",
		astronomerProviderLabelKey:                          "aws",
		astronomerDistributionLabelKey:                      "eks",
		astronomerAgentProfileLabelKey:                      "operator",
		astronomerAgentVersionLabelKey:                      "v0.4.1",
		astronomerKubernetesVersionLabelKey:                 "v1.29.3-k3s1",
		astronomerProjectLabelKey:                           "platform",
		astronomerProjectIDLabelKey:                         "11111111-1111-1111-1111-111111111111",
		astronomerProjectMembershipLabelPrefix + "platform": "true",
		astronomerProjectIDMembershipLabelPrefix + "11111111-1111-1111-1111-111111111111": "true",
		astronomerLabelPrefix + "tier":      "prod",
		astronomerLabelPrefix + "team-name": "platform",
	}
	for k, v := range wantLabels {
		if got := seenRegistration.Labels[k]; got != v {
			t.Fatalf("upstream labels[%q] = %q, want %q (full=%v)", k, got, v, seenRegistration.Labels)
		}
	}
	if len(q.created) != 1 {
		t.Fatalf("created rows = %d, want 1 upserted managed-cluster row", len(q.created))
	}
	var rowLabels map[string]string
	if err := json.Unmarshal(q.created[0].Labels, &rowLabels); err != nil {
		t.Fatalf("unmarshal row labels: %v", err)
	}
	if got := rowLabels[astronomerRegionLabelKey]; got != "us-east-1" {
		t.Fatalf("row region label = %q, want us-east-1 (full=%v)", got, rowLabels)
	}
	if got := rowLabels[astronomerProjectIDLabelKey]; got != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("row project label = %q, want project id (full=%v)", got, rowLabels)
	}
	if len(timeline.steps) != 1 {
		t.Fatalf("timeline steps = %+v, want one repair step", timeline.steps)
	}
	if timeline.steps[0].StepName != "argocd_registration_repaired" || timeline.steps[0].Status != "success" {
		t.Fatalf("repair step = %+v", timeline.steps[0])
	}
	repairs, ok := timeline.steps[0].Detail["repairs"].([]string)
	if !ok || len(repairs) != 1 || repairs[0] != "stale_labels" {
		t.Fatalf("repair detail = %#v, want [stale_labels]", timeline.steps[0].Detail["repairs"])
	}
}

func TestArgoCDAutoRegisterRepairsMissingSecretWithStoredProxyToken(t *testing.T) {
	// The Argo API under test is an explicit loopback upstream.
	defer httpclient.DisableGuardForTest()()

	clusterID := uuid.New()
	instanceID := uuid.New()
	key, err := auth.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	encryptor, err := auth.NewEncryptor(key)
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}
	instanceToken, err := encryptor.Encrypt("argocd-api-token")
	if err != nil {
		t.Fatalf("encrypt instance token: %v", err)
	}
	proxyToken := auth.ArgoCDClusterProxyTokenPrefix + "stored-repair-token"
	encryptedProxyToken, err := encryptor.Encrypt(proxyToken)
	if err != nil {
		t.Fatalf("encrypt proxy token: %v", err)
	}

	var seenRegistration struct {
		Server string `json:"server"`
		Config struct {
			BearerToken string `json:"bearerToken"`
		} `json:"config"`
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/clusters" || r.URL.Query().Get("upsert") != "true" {
			t.Fatalf("unexpected upstream request: %s %s", r.Method, r.URL.RequestURI())
		}
		if r.Header.Get("Authorization") != "Bearer argocd-api-token" {
			t.Fatalf("Argo API authorization = %q", r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&seenRegistration); err != nil {
			t.Fatalf("decode registration: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"name": "cluster-prod-east", "server": seenRegistration.Server})
	}))
	defer upstream.Close()

	cluster := sqlc.Cluster{
		ID:            clusterID,
		Name:          "prod-east",
		LastHeartbeat: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	}
	q := &argoCDAutoRegisterTestQuerier{
		cluster: cluster,
		instances: []sqlc.ArgocdInstance{{
			ID:                 instanceID,
			Name:               "built-in",
			ApiUrl:             upstream.URL,
			AuthTokenEncrypted: instanceToken,
		}},
		managed: []sqlc.ArgocdManagedCluster{{
			ArgocdInstanceID:  instanceID,
			ClusterID:         clusterID,
			ClusterSecretName: "missing-secret",
			ServerUrl:         "https://stale-proxy.example",
		}},
		activeProxyToken: &sqlc.ArgocdClusterProxyToken{
			ID:             uuid.New(),
			ClusterID:      clusterID,
			Purpose:        "argocd_cluster_proxy",
			TokenHash:      auth.HashArgoCDClusterProxyToken(proxyToken),
			TokenEncrypted: encryptedProxyToken,
			ExpiresAt:      pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
		},
	}
	timeline := &argoCDTimelineRecorder{}
	err = autoRegisterClusterIntoArgoCD(context.Background(), ArgoCDAutoRegisterDeps{
		Queries:             q,
		Encryptor:           encryptor,
		K8s:                 k8sfake.NewClientset(),
		ClusterProxyBaseURL: "http://astronomer-server.astronomer.svc.cluster.local:8090",
		Registration:        timeline,
	}, cluster)
	if err != nil {
		t.Fatalf("repair missing Secret: %v", err)
	}
	wantServer := "http://astronomer-server.astronomer.svc.cluster.local:8090/api/v1/internal/argocd/clusters/" + clusterID.String() + "/k8s"
	if seenRegistration.Server != wantServer || seenRegistration.Config.BearerToken != proxyToken {
		t.Fatalf("registration = server %q token-match %t, want server %q and stored token", seenRegistration.Server, seenRegistration.Config.BearerToken == proxyToken, wantServer)
	}
	if len(q.proxyTokenUpserts) != 0 {
		t.Fatalf("proxy token upserts = %d, want reuse of active DB credential", len(q.proxyTokenUpserts))
	}
	if len(timeline.steps) != 1 || timeline.steps[0].StepName != "argocd_registration_repaired" || timeline.steps[0].Status != "success" {
		t.Fatalf("repair timeline = %+v", timeline.steps)
	}
	repairs, ok := timeline.steps[0].Detail["repairs"].([]string)
	if !ok || len(repairs) != 1 || repairs[0] != "missing_secret" {
		t.Fatalf("repair reasons = %#v, want [missing_secret]", timeline.steps[0].Detail["repairs"])
	}
}

func TestArgoCDAutoRegisterRepairBlockedWhenCredentialUnavailable(t *testing.T) {
	clusterID := uuid.New()
	instanceID := uuid.New()
	cluster := sqlc.Cluster{
		ID:            clusterID,
		Name:          "prod-east",
		LastHeartbeat: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	}
	q := &argoCDAutoRegisterTestQuerier{
		cluster: cluster,
		instances: []sqlc.ArgocdInstance{{
			ID:                 instanceID,
			Name:               "built-in",
			ApiUrl:             "https://argocd.example.test",
			AuthTokenEncrypted: "token",
		}},
		managed: []sqlc.ArgocdManagedCluster{{
			ArgocdInstanceID:  instanceID,
			ClusterID:         clusterID,
			ClusterSecretName: "missing-secret",
			ServerUrl:         "https://old-proxy.example",
		}},
	}
	timeline := &argoCDTimelineRecorder{}

	err := autoRegisterClusterIntoArgoCD(context.Background(), ArgoCDAutoRegisterDeps{
		Queries:      q,
		K8s:          k8sfake.NewClientset(),
		Registration: timeline,
	}, cluster)
	if !errors.Is(err, errArgoCDManagedClusterCredentialUnavailable) {
		t.Fatalf("error = %v, want credential-unavailable sentinel", err)
	}
	if len(q.created) != 0 {
		t.Fatalf("created rows = %d, want no upsert when credential is unavailable", len(q.created))
	}
	if len(timeline.steps) != 2 {
		t.Fatalf("timeline steps = %+v, want blocked + failed", timeline.steps)
	}
	blocked := timeline.steps[0]
	if blocked.StepName != "argocd_registration_repair_blocked" || blocked.Status != "failed" {
		t.Fatalf("blocked step = %+v", blocked)
	}
	if blocked.Detail["reason"] != "credential_unavailable" {
		t.Fatalf("blocked reason = %#v", blocked.Detail)
	}
	repairs, ok := blocked.Detail["repairs"].([]string)
	if !ok || len(repairs) != 1 || repairs[0] != "missing_secret" {
		t.Fatalf("blocked repairs = %#v, want [missing_secret]", blocked.Detail["repairs"])
	}
	if timeline.steps[1].StepName != "argocd_registration_failed" {
		t.Fatalf("second step = %+v, want registration failure", timeline.steps[1])
	}
}

func TestArgoCDManagedClusterIndexRepairRebuildsMissingRowFromSecret(t *testing.T) {
	clusterID := uuid.New()
	instanceID := uuid.New()
	q := &argoCDAutoRegisterTestQuerier{
		cluster: sqlc.Cluster{
			ID:                clusterID,
			Name:              "prod-east",
			Environment:       "production",
			Region:            "us-east-1",
			Provider:          "aws",
			Distribution:      "eks",
			AgentVersion:      "v0.4.1",
			KubernetesVersion: "v1.29.3+k3s1",
			Annotations:       json.RawMessage(`{"astronomer.io/agent-privilege-profile":"viewer"}`),
			Labels:            json.RawMessage(`{"tier":"prod"}`),
		},
		projects:  []sqlc.Project{{ID: uuid.MustParse("11111111-1111-1111-1111-111111111111"), Name: "platform"}},
		instances: []sqlc.ArgocdInstance{{ID: instanceID, Name: "built-in"}},
	}
	timeline := &argoCDTimelineRecorder{}
	k8s := k8sfake.NewClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster-prod-east",
			Namespace: argoCDNamespace,
			Labels: map[string]string{
				argoCDClusterSecretTypeLabel: argoCDClusterSecretTypeValue,
				astronomerManagedByLabelKey:  astronomerManagedByLabelValue,
				astronomerClusterIDLabelKey:  clusterID.String(),
			},
		},
		Data: map[string][]byte{"server": []byte("https://kubernetes.example.test")},
	})

	err := repairArgoCDManagedClusterIndex(context.Background(), ArgoCDAutoRegisterDeps{
		Queries:      q,
		K8s:          k8s,
		Registration: timeline,
	}, q.instances)
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if len(q.created) != 1 {
		t.Fatalf("created rows = %d, want 1", len(q.created))
	}
	got := q.created[0]
	if got.ArgocdInstanceID != instanceID || got.ClusterID != clusterID || got.ClusterSecretName != "cluster-prod-east" {
		t.Fatalf("created row = %+v", got)
	}
	if got.ServerUrl != "https://kubernetes.example.test" {
		t.Fatalf("server url = %q", got.ServerUrl)
	}
	if len(q.conditions) != 1 || q.conditions[0].Type != ConditionArgoCDAdopted || q.conditions[0].Status != "True" {
		t.Fatalf("conditions = %+v, want ArgoCDAdopted True", q.conditions)
	}
	if len(timeline.steps) != 1 {
		t.Fatalf("timeline steps = %+v, want one repair step", timeline.steps)
	}
	if timeline.steps[0].StepName != "argocd_registration_repaired" || timeline.steps[0].Detail["repair"] != "db_index_recreated" {
		t.Fatalf("repair step = %+v", timeline.steps[0])
	}
	secret, err := k8s.CoreV1().Secrets(argoCDNamespace).Get(context.Background(), "cluster-prod-east", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	if secret.Labels[astronomerClusterNameLabelKey] != "prod-east" ||
		secret.Labels[astronomerEnvironmentLabelKey] != "production" ||
		secret.Labels[astronomerRegionLabelKey] != "us-east-1" ||
		secret.Labels[astronomerProviderLabelKey] != "aws" ||
		secret.Labels[astronomerDistributionLabelKey] != "eks" ||
		secret.Labels[astronomerAgentProfileLabelKey] != "viewer" ||
		secret.Labels[astronomerAgentVersionLabelKey] != "v0.4.1" ||
		secret.Labels[astronomerKubernetesVersionLabelKey] != "v1.29.3-k3s1" ||
		secret.Labels[astronomerProjectIDLabelKey] != "11111111-1111-1111-1111-111111111111" ||
		secret.Labels[astronomerProjectMembershipLabelPrefix+"platform"] != "true" ||
		secret.Labels[astronomerLabelPrefix+"tier"] != "prod" {
		t.Fatalf("secret labels were not refreshed from cluster row: %+v", secret.Labels)
	}
}

func TestArgoCDManagedClusterIndexRepairReportsOrphanSecrets(t *testing.T) {
	clusterID := uuid.New()
	instanceID := uuid.New()
	q := &argoCDAutoRegisterTestQuerier{
		instances: []sqlc.ArgocdInstance{{ID: instanceID, Name: "built-in"}},
	}
	k8s := k8sfake.NewClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "orphan-cluster",
			Namespace: argoCDNamespace,
			Labels: map[string]string{
				argoCDClusterSecretTypeLabel: argoCDClusterSecretTypeValue,
				astronomerManagedByLabelKey:  astronomerManagedByLabelValue,
				astronomerClusterIDLabelKey:  clusterID.String(),
			},
		},
		Data: map[string][]byte{"server": []byte("https://orphan.example.test")},
	})

	stats, err := repairArgoCDManagedClusterIndexWithStats(context.Background(), ArgoCDAutoRegisterDeps{
		Queries: q,
		K8s:     k8s,
	}, q.instances)
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if stats.ClusterSecretsChecked != 1 || stats.AstronomerManagedSecrets != 1 || stats.OrphanSecretsFound != 1 {
		t.Fatalf("stats = %+v, want one Astronomer-managed orphan secret", stats)
	}
	if len(q.created) != 0 {
		t.Fatalf("created rows = %d, want none for orphan secret", len(q.created))
	}
}

func TestArgoCDAutoRegisterSweepRecordsOrphanSecretRepairStats(t *testing.T) {
	ResetArgoCDAutoRegister()
	t.Cleanup(ResetArgoCDAutoRegister)
	clusterID := uuid.New()
	instanceID := uuid.New()
	q := &argoCDAutoRegisterTestQuerier{
		instances: []sqlc.ArgocdInstance{{ID: instanceID, Name: "built-in"}},
	}
	k8s := k8sfake.NewClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "orphan-cluster",
			Namespace: argoCDNamespace,
			Labels: map[string]string{
				argoCDClusterSecretTypeLabel: argoCDClusterSecretTypeValue,
				astronomerManagedByLabelKey:  astronomerManagedByLabelValue,
				astronomerClusterIDLabelKey:  clusterID.String(),
			},
		},
	})
	ConfigureArgoCDAutoRegister(ArgoCDAutoRegisterDeps{Queries: q, K8s: k8s})

	if err := HandleArgoCDAutoRegisterCluster(context.Background(), asynq.NewTask(ArgoCDAutoRegisterClusterType, nil)); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(q.repairSuccess) != 1 {
		t.Fatalf("repair success writes = %d, want 1", len(q.repairSuccess))
	}
	var metadata map[string]any
	if err := json.Unmarshal(q.repairSuccess[0].Metadata, &metadata); err != nil {
		t.Fatalf("unmarshal repair metadata: %v", err)
	}
	if metadata["argocd_orphan_secrets_found"] != float64(1) {
		t.Fatalf("orphan metadata = %#v, want 1 (full=%v)", metadata["argocd_orphan_secrets_found"], metadata)
	}
	if metadata["argocd_cluster_secrets_checked"] != float64(1) {
		t.Fatalf("checked metadata = %#v, want 1 (full=%v)", metadata["argocd_cluster_secrets_checked"], metadata)
	}
}

func TestArgoCDManagedClusterIndexRepairDoesNotDuplicateExistingRow(t *testing.T) {
	clusterID := uuid.New()
	instanceID := uuid.New()
	q := &argoCDAutoRegisterTestQuerier{
		cluster: sqlc.Cluster{ID: clusterID, Name: "prod-east"},
		instances: []sqlc.ArgocdInstance{{
			ID:   instanceID,
			Name: "built-in",
		}},
		managed: []sqlc.ArgocdManagedCluster{{
			ArgocdInstanceID:  instanceID,
			ClusterID:         clusterID,
			ClusterSecretName: "cluster-prod-east",
		}},
	}
	k8s := k8sfake.NewClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster-prod-east",
			Namespace: argoCDNamespace,
			Labels: map[string]string{
				argoCDClusterSecretTypeLabel: argoCDClusterSecretTypeValue,
				astronomerManagedByLabelKey:  astronomerManagedByLabelValue,
				astronomerClusterIDLabelKey:  clusterID.String(),
			},
		},
	})

	err := repairArgoCDManagedClusterIndex(context.Background(), ArgoCDAutoRegisterDeps{
		Queries: q,
		K8s:     k8s,
	}, q.instances)
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if len(q.created) != 0 {
		t.Fatalf("created rows = %d, want 0", len(q.created))
	}
}

// localClusterSecretWithLabels builds an astronomer-local-cluster Secret
// carrying exactly the given labels plus the ArgoCD cluster-secret marker.
func localClusterSecretWithLabels(name string, labels map[string]string) *corev1.Secret {
	merged := map[string]string{argoCDClusterSecretTypeLabel: argoCDClusterSecretTypeValue}
	for k, v := range labels {
		merged[k] = v
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: argoCDNamespace,
			Labels:    merged,
		},
		Data: map[string][]byte{"server": []byte("https://kubernetes.default.svc")},
	}
}

// TestArgoCDAutoRegisterLocalAlreadyManagedNoDriftSkipsUpsert locks single
// steady-state ownership of the local cluster Secret: when the local cluster
// already has a managed-cluster row and the Secret carries the desired
// labels, the sweep must NOT re-upsert it through the ArgoCD API (which would
// mint a fresh application-controller token, rewrite the Secret, and clobber
// the server loop's renew-after annotation) — it only refreshes the
// Registered condition.
func TestArgoCDAutoRegisterLocalAlreadyManagedNoDriftSkipsUpsert(t *testing.T) {
	defer httpclient.DisableGuardForTest()()

	clusterID := uuid.New()
	instanceID := uuid.New()
	upstreamPosts := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamPosts++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"name": "astronomer-local-cluster"})
	}))
	defer upstream.Close()

	cluster := sqlc.Cluster{
		ID:           clusterID,
		Name:         "local",
		IsLocal:      true,
		ApiServerUrl: "https://kubernetes.default.svc",
		AgentVersion: "v0.3.0",
	}
	q := &argoCDAutoRegisterTestQuerier{
		cluster: cluster,
		instances: []sqlc.ArgocdInstance{{
			ID:                 instanceID,
			Name:               "local",
			ApiUrl:             upstream.URL,
			AuthTokenEncrypted: "argocd-token",
		}},
		managed: []sqlc.ArgocdManagedCluster{{
			ArgocdInstanceID:  instanceID,
			ClusterID:         clusterID,
			ClusterSecretName: "astronomer-local-cluster",
			ServerUrl:         "https://kubernetes.default.svc",
		}},
	}
	k8s := k8sfake.NewClientset(localClusterSecretWithLabels("astronomer-local-cluster", managedClusterArgoLabelsForProjects(cluster, nil)))
	timeline := &argoCDTimelineRecorder{}

	err := autoRegisterClusterIntoArgoCD(context.Background(), ArgoCDAutoRegisterDeps{
		Queries:      q,
		K8s:          k8s,
		Registration: timeline,
	}, cluster)
	if err != nil {
		t.Fatalf("auto-register: %v", err)
	}
	if upstreamPosts != 0 {
		t.Fatalf("upstream posts = %d, want 0 (converged local cluster must not be re-upserted)", upstreamPosts)
	}
	if len(q.created) != 0 {
		t.Fatalf("created rows = %d, want none for converged local cluster", len(q.created))
	}
	if len(timeline.steps) != 0 {
		t.Fatalf("timeline steps = %+v, want none for converged local cluster", timeline.steps)
	}
	if len(q.conditions) != 1 || q.conditions[0].Status != "True" || q.conditions[0].Reason != "Registered" {
		t.Fatalf("conditions = %+v, want Registered True", q.conditions)
	}
}

// TestArgoCDAutoRegisterLocalStaleAgentVersionLabelStillRepairs is the drift
// counterpart: a local Secret whose agent-version label lags the DB row is
// repaired with exactly one upsert stamping the DB row's version.
func TestArgoCDAutoRegisterLocalStaleAgentVersionLabelStillRepairs(t *testing.T) {
	defer httpclient.DisableGuardForTest()()

	clusterID := uuid.New()
	instanceID := uuid.New()
	upstreamPosts := 0
	var seenRegistration struct {
		Labels map[string]string `json:"labels"`
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/clusters" {
			t.Fatalf("unexpected upstream request: %s %s", r.Method, r.URL.RequestURI())
		}
		upstreamPosts++
		if err := json.NewDecoder(r.Body).Decode(&seenRegistration); err != nil {
			t.Fatalf("decode registration: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"name": "astronomer-local-cluster"})
	}))
	defer upstream.Close()

	cluster := sqlc.Cluster{
		ID:           clusterID,
		Name:         "local",
		IsLocal:      true,
		ApiServerUrl: "https://kubernetes.default.svc",
		AgentVersion: "v0.3.0-new",
	}
	staleCluster := cluster
	staleCluster.AgentVersion = "v0.2.0-old"
	q := &argoCDAutoRegisterTestQuerier{
		cluster: cluster,
		instances: []sqlc.ArgocdInstance{{
			ID:                 instanceID,
			Name:               "local",
			ApiUrl:             upstream.URL,
			AuthTokenEncrypted: "argocd-token",
		}},
		managed: []sqlc.ArgocdManagedCluster{{
			ArgocdInstanceID:  instanceID,
			ClusterID:         clusterID,
			ClusterSecretName: "astronomer-local-cluster",
			ServerUrl:         "https://kubernetes.default.svc",
		}},
	}
	k8s := k8sfake.NewClientset(localClusterSecretWithLabels("astronomer-local-cluster", managedClusterArgoLabelsForProjects(staleCluster, nil)))
	k8s.Fake.PrependReactor("create", "serviceaccounts", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() != "token" {
			return false, nil, nil
		}
		return true, &authv1.TokenRequest{Status: authv1.TokenRequestStatus{Token: "minted-token"}}, nil
	})
	timeline := &argoCDTimelineRecorder{}

	err := autoRegisterClusterIntoArgoCD(context.Background(), ArgoCDAutoRegisterDeps{
		Queries:      q,
		K8s:          k8s,
		Registration: timeline,
	}, cluster)
	if err != nil {
		t.Fatalf("auto-register: %v", err)
	}
	if upstreamPosts != 1 {
		t.Fatalf("upstream posts = %d, want exactly one repair upsert", upstreamPosts)
	}
	if got := seenRegistration.Labels[astronomerAgentVersionLabelKey]; got != "v0.3.0-new" {
		t.Fatalf("upserted agent-version label = %q, want DB row version v0.3.0-new", got)
	}
	if len(timeline.steps) != 1 || timeline.steps[0].StepName != "argocd_registration_repaired" || timeline.steps[0].Status != "success" {
		t.Fatalf("timeline steps = %+v, want one repaired step", timeline.steps)
	}
	repairs, ok := timeline.steps[0].Detail["repairs"].([]string)
	if !ok || len(repairs) != 1 || repairs[0] != "stale_labels" {
		t.Fatalf("repair detail = %#v, want [stale_labels]", timeline.steps[0].Detail["repairs"])
	}
}

// TestArgoCDAutoRegisterAdoptedAlreadyManagedStillUpserts locks the
// no-regression guarantee for adopted clusters: their unconditional upsert is
// load-bearing (it renews the proxy-token credential, which label drift does
// not model), so a converged non-local cluster is still re-upserted.
func TestArgoCDAutoRegisterAdoptedAlreadyManagedStillUpserts(t *testing.T) {
	defer httpclient.DisableGuardForTest()()

	clusterID := uuid.New()
	instanceID := uuid.New()
	key, err := auth.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	encryptor, err := auth.NewEncryptor(key)
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}
	instanceToken, err := encryptor.Encrypt("argocd-token")
	if err != nil {
		t.Fatalf("encrypt instance token: %v", err)
	}
	proxyToken := auth.ArgoCDClusterProxyTokenPrefix + "stored-token"
	encryptedProxyToken, err := encryptor.Encrypt(proxyToken)
	if err != nil {
		t.Fatalf("encrypt proxy token: %v", err)
	}

	upstreamPosts := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/clusters" {
			t.Fatalf("unexpected upstream request: %s %s", r.Method, r.URL.RequestURI())
		}
		upstreamPosts++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"name": "cluster-prod-east"})
	}))
	defer upstream.Close()

	cluster := sqlc.Cluster{
		ID:            clusterID,
		Name:          "prod-east",
		AgentVersion:  "v0.4.1",
		LastHeartbeat: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	}
	serverURL := "https://astronomer.example/api/v1/internal/argocd/clusters/" + clusterID.String() + "/k8s"
	q := &argoCDAutoRegisterTestQuerier{
		cluster: cluster,
		instances: []sqlc.ArgocdInstance{{
			ID:                 instanceID,
			Name:               "built-in",
			ApiUrl:             upstream.URL,
			AuthTokenEncrypted: instanceToken,
		}},
		managed: []sqlc.ArgocdManagedCluster{{
			ArgocdInstanceID:  instanceID,
			ClusterID:         clusterID,
			ClusterSecretName: "cluster-prod-east",
			ServerUrl:         serverURL,
		}},
		activeProxyToken: &sqlc.ArgocdClusterProxyToken{
			ID:             uuid.New(),
			ClusterID:      clusterID,
			Purpose:        "argocd_cluster_proxy",
			TokenHash:      auth.HashArgoCDClusterProxyToken(proxyToken),
			TokenEncrypted: encryptedProxyToken,
			ExpiresAt:      pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
		},
	}
	k8s := k8sfake.NewClientset(localClusterSecretWithLabels("cluster-prod-east", managedClusterArgoLabelsForProjects(cluster, nil)))
	timeline := &argoCDTimelineRecorder{}

	err = autoRegisterClusterIntoArgoCD(context.Background(), ArgoCDAutoRegisterDeps{
		Queries:             q,
		Encryptor:           encryptor,
		K8s:                 k8s,
		ClusterProxyBaseURL: "https://astronomer.example",
		Registration:        timeline,
	}, cluster)
	if err != nil {
		t.Fatalf("auto-register: %v", err)
	}
	if upstreamPosts != 1 {
		t.Fatalf("upstream posts = %d, want 1 (adopted clusters keep the unconditional upsert)", upstreamPosts)
	}
	if len(q.conditions) != 1 || q.conditions[0].Status != "True" || q.conditions[0].Reason != "Registered" {
		t.Fatalf("conditions = %+v, want Registered True", q.conditions)
	}
}

// bearerTokenSecretConfig builds the config blob of an ArgoCD cluster Secret
// whose bearer token is an unsigned JWT expiring at exp.
func bearerTokenSecretConfig(t *testing.T, exp time.Time) []byte {
	t.Helper()
	payload, err := json.Marshal(map[string]int64{"exp": exp.Unix()})
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	token := fmt.Sprintf("%s.%s.sig",
		base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`)),
		base64.RawURLEncoding.EncodeToString(payload))
	cfg, err := json.Marshal(map[string]string{"bearerToken": token})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	return cfg
}

// TestArgoCDAutoRegisterLocalExpiringTokenStillRepairs covers the
// out-of-server-process renewal backstop: a converged local Secret whose
// bearer token is still fresh must be skipped, but once the remaining token
// lifetime drops inside localArgoCDClusterTokenExpiryDriftWindow (i.e. the
// server has missed both of its in-process renewal paths) the sweep re-mints
// the credential with exactly one repair upsert.
func TestArgoCDAutoRegisterLocalExpiringTokenStillRepairs(t *testing.T) {
	defer httpclient.DisableGuardForTest()()

	clusterID := uuid.New()
	instanceID := uuid.New()
	upstreamPosts := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamPosts++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"name": "astronomer-local-cluster"})
	}))
	defer upstream.Close()

	cluster := sqlc.Cluster{
		ID:           clusterID,
		Name:         "local",
		IsLocal:      true,
		ApiServerUrl: "https://kubernetes.default.svc",
		AgentVersion: "v0.3.0",
	}
	q := &argoCDAutoRegisterTestQuerier{
		cluster: cluster,
		instances: []sqlc.ArgocdInstance{{
			ID:                 instanceID,
			Name:               "local",
			ApiUrl:             upstream.URL,
			AuthTokenEncrypted: "argocd-token",
		}},
		managed: []sqlc.ArgocdManagedCluster{{
			ArgocdInstanceID:  instanceID,
			ClusterID:         clusterID,
			ClusterSecretName: "astronomer-local-cluster",
			ServerUrl:         "https://kubernetes.default.svc",
		}},
	}
	secret := localClusterSecretWithLabels("astronomer-local-cluster", managedClusterArgoLabelsForProjects(cluster, nil))
	secret.Data["config"] = bearerTokenSecretConfig(t, time.Now().Add(20*time.Hour))
	k8s := k8sfake.NewClientset(secret)
	k8s.Fake.PrependReactor("create", "serviceaccounts", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() != "token" {
			return false, nil, nil
		}
		return true, &authv1.TokenRequest{Status: authv1.TokenRequestStatus{Token: "minted-token"}}, nil
	})
	timeline := &argoCDTimelineRecorder{}
	deps := ArgoCDAutoRegisterDeps{
		Queries:      q,
		K8s:          k8s,
		Registration: timeline,
	}

	// Fresh token (renewal not due for hours): converged, no upsert.
	if err := autoRegisterClusterIntoArgoCD(context.Background(), deps, cluster); err != nil {
		t.Fatalf("auto-register with fresh token: %v", err)
	}
	if upstreamPosts != 0 {
		t.Fatalf("upstream posts with fresh token = %d, want 0", upstreamPosts)
	}

	// Token inside the expiry drift window: exactly one repair upsert.
	secret.Data["config"] = bearerTokenSecretConfig(t, time.Now().Add(time.Hour))
	if _, err := k8s.CoreV1().Secrets(argoCDNamespace).Update(context.Background(), secret, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("update secret: %v", err)
	}
	if err := autoRegisterClusterIntoArgoCD(context.Background(), deps, cluster); err != nil {
		t.Fatalf("auto-register with expiring token: %v", err)
	}
	if upstreamPosts != 1 {
		t.Fatalf("upstream posts with expiring token = %d, want exactly one repair upsert", upstreamPosts)
	}
	if len(timeline.steps) != 1 || timeline.steps[0].StepName != "argocd_registration_repaired" || timeline.steps[0].Status != "success" {
		t.Fatalf("timeline steps = %+v, want one repaired step", timeline.steps)
	}
	repairs, ok := timeline.steps[0].Detail["repairs"].([]string)
	if !ok || len(repairs) != 1 || repairs[0] != "token_expiring" {
		t.Fatalf("repair detail = %#v, want [token_expiring]", timeline.steps[0].Detail["repairs"])
	}
}

// TestArgoCDAutoRegisterLocalNewInstanceStillRegisters guards the skip
// against the new-instance gap: a local cluster already registered in the
// bundled instance with zero drift must still be registered into an ArgoCD
// instance added later — instance non-coverage is not modeled as drift, so
// the skip must not fire before checking it.
func TestArgoCDAutoRegisterLocalNewInstanceStillRegisters(t *testing.T) {
	defer httpclient.DisableGuardForTest()()

	clusterID := uuid.New()
	existingInstanceID := uuid.New()
	newInstanceID := uuid.New()
	upstreamPosts := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamPosts++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"name": "astronomer-local-cluster"})
	}))
	defer upstream.Close()

	cluster := sqlc.Cluster{
		ID:           clusterID,
		Name:         "local",
		IsLocal:      true,
		ApiServerUrl: "https://kubernetes.default.svc",
		AgentVersion: "v0.3.0",
	}
	q := &argoCDAutoRegisterTestQuerier{
		cluster: cluster,
		instances: []sqlc.ArgocdInstance{{
			ID:                 existingInstanceID,
			Name:               "local",
			ApiUrl:             upstream.URL,
			AuthTokenEncrypted: "argocd-token",
		}, {
			ID:                 newInstanceID,
			Name:               "second",
			ApiUrl:             upstream.URL,
			AuthTokenEncrypted: "argocd-token",
		}},
		managed: []sqlc.ArgocdManagedCluster{{
			ArgocdInstanceID:  existingInstanceID,
			ClusterID:         clusterID,
			ClusterSecretName: "astronomer-local-cluster",
			ServerUrl:         "https://kubernetes.default.svc",
		}},
	}
	k8s := k8sfake.NewClientset(localClusterSecretWithLabels("astronomer-local-cluster", managedClusterArgoLabelsForProjects(cluster, nil)))
	k8s.Fake.PrependReactor("create", "serviceaccounts", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() != "token" {
			return false, nil, nil
		}
		return true, &authv1.TokenRequest{Status: authv1.TokenRequestStatus{Token: "minted-token"}}, nil
	})
	timeline := &argoCDTimelineRecorder{}

	err := autoRegisterClusterIntoArgoCD(context.Background(), ArgoCDAutoRegisterDeps{
		Queries:      q,
		K8s:          k8s,
		Registration: timeline,
	}, cluster)
	if err != nil {
		t.Fatalf("auto-register: %v", err)
	}
	if upstreamPosts != 2 {
		t.Fatalf("upstream posts = %d, want 2 (registration loop must cover the new instance)", upstreamPosts)
	}
	var newInstanceRegistered bool
	for _, row := range q.created {
		if row.ArgocdInstanceID == newInstanceID {
			newInstanceRegistered = true
		}
	}
	if !newInstanceRegistered {
		t.Fatalf("created rows = %+v, want a managed-cluster row for the newly added instance", q.created)
	}
}

// TestLocalClusterServerWorkerDesiredLabelParity pins cross-component label
// parity for the local cluster Secret. The server's writer
// (internal/server.localArgoClusterSecretLabelsForProjects) emits
// argolabels.SecretLabels for a local row — its own test pins that equality —
// and this test asserts that shared output is drift-free against the worker's
// baseline in both directions, so a label added on only one side fails a test
// instead of resurrecting the agent-version ping-pong in production.
func TestLocalClusterServerWorkerDesiredLabelParity(t *testing.T) {
	cluster := sqlc.Cluster{
		ID:           uuid.New(),
		Name:         "local",
		IsLocal:      true,
		ApiServerUrl: "https://kubernetes.default.svc",
		AgentVersion: "v0.3.0",
		Environment:  "dev",
	}
	projects := []sqlc.Project{{ID: uuid.New(), Name: "alpha", ClusterID: cluster.ID}}

	serverWritten := argolabels.SecretLabels(cluster, projects)
	workerDesired := managedClusterArgoLabelsForProjects(cluster, projects)
	if managedClusterSecretLabelsDrift(serverWritten, workerDesired) {
		t.Fatal("server-written local Secret labels drift against the worker baseline")
	}

	// A worker-written registration lands with the worker labels plus the
	// ArgoCD cluster-secret marker; it must satisfy the server's desired set.
	workerWritten := map[string]string{argoCDClusterSecretTypeLabel: argoCDClusterSecretTypeValue}
	for k, v := range workerDesired {
		workerWritten[k] = v
	}
	if managedClusterSecretLabelsDrift(workerWritten, serverWritten) {
		t.Fatal("worker-written local Secret labels drift against the server baseline")
	}
}

func boolPlatformSetting(v bool) *sqlc.PlatformSetting {
	raw, _ := json.Marshal(v)
	return &sqlc.PlatformSetting{Key: platformSettingArgoCDAutoAdoptKey, Value: raw}
}

func selectorPlatformSetting(sel map[string]string) *sqlc.PlatformSetting {
	raw, _ := json.Marshal(sel)
	return &sqlc.PlatformSetting{Key: platformSettingArgoCDAutoRegSelector, Value: raw}
}

// TestArgoCDAutoRegisterSkipsClusterNotMatchingSelector locks the label-based
// gate: an unmanaged cluster whose labels don't satisfy the configured
// auto-register selector is skipped entirely (no registration timeline, no
// managed-cluster row) even though an ArgoCD instance is configured.
func TestArgoCDAutoRegisterSkipsClusterNotMatchingSelector(t *testing.T) {
	ResetArgoCDAutoRegister()
	t.Cleanup(ResetArgoCDAutoRegister)
	clusterID := uuid.New()
	q := &argoCDAutoRegisterTestQuerier{
		selectorSetting: selectorPlatformSetting(map[string]string{"astronomer.io/label-tier": "gold"}),
		cluster: sqlc.Cluster{
			ID:            clusterID,
			Name:          "prod-1",
			LastHeartbeat: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
			Labels:        json.RawMessage(`{"astronomer.io/label-tier":"bronze"}`),
		},
		instances: []sqlc.ArgocdInstance{{ID: uuid.New(), Name: "argo"}},
	}
	timeline := &argoCDTimelineRecorder{}
	ConfigureArgoCDAutoRegister(ArgoCDAutoRegisterDeps{Queries: q, Registration: timeline})

	task, err := NewArgoCDAutoRegisterClusterTask(clusterID)
	if err != nil {
		t.Fatalf("task: %v", err)
	}
	if err := HandleArgoCDAutoRegisterCluster(context.Background(), task); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(timeline.steps) != 0 {
		t.Fatalf("steps = %+v, want none for non-matching cluster", timeline.steps)
	}
	if len(q.created) != 0 {
		t.Fatalf("created = %+v, want no managed-cluster rows for non-matching cluster", q.created)
	}
	if len(q.conditions) != 0 {
		t.Fatalf("conditions = %+v, want none for non-matching cluster", q.conditions)
	}
}

// TestArgoCDAutoRegisterMatchingSelectorProceeds is the positive counterpart:
// a matching cluster is not short-circuited by the selector and reaches the
// registration timeline (failing later only because no credential is wired,
// which is out of scope here).
func TestArgoCDAutoRegisterMatchingSelectorProceeds(t *testing.T) {
	ResetArgoCDAutoRegister()
	t.Cleanup(ResetArgoCDAutoRegister)
	clusterID := uuid.New()
	q := &argoCDAutoRegisterTestQuerier{
		selectorSetting: selectorPlatformSetting(map[string]string{"astronomer.io/label-tier": "gold"}),
		cluster: sqlc.Cluster{
			ID:            clusterID,
			Name:          "prod-1",
			LastHeartbeat: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
			Labels:        json.RawMessage(`{"astronomer.io/label-tier":"gold"}`),
		},
	}
	timeline := &argoCDTimelineRecorder{}
	ConfigureArgoCDAutoRegister(ArgoCDAutoRegisterDeps{Queries: q, Registration: timeline})

	task, err := NewArgoCDAutoRegisterClusterTask(clusterID)
	if err != nil {
		t.Fatalf("task: %v", err)
	}
	if err := HandleArgoCDAutoRegisterCluster(context.Background(), task); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(timeline.steps) == 0 || timeline.steps[0].StepName != "argocd_registering" {
		t.Fatalf("steps = %+v, want registration to proceed for matching cluster", timeline.steps)
	}
}
