package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/registration"
)

type argoCDAutoRegisterTestQuerier struct {
	setting         *sqlc.PlatformSetting
	selectorSetting *sqlc.PlatformSetting
	listTouched     bool
	cluster         sqlc.Cluster
	clusters        []sqlc.Cluster
	projects        []sqlc.Project
	instances       []sqlc.ArgocdInstance
	managed         []sqlc.ArgocdManagedCluster
	created         []sqlc.CreateArgoCDManagedClusterParams
	conditions      []sqlc.UpsertClusterConditionParams
	managedLookup   bool
	repairSuccess   []sqlc.RecordRepairJobSuccessParams
	repairFailure   []sqlc.RecordRepairJobFailureParams
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

func (q *argoCDAutoRegisterTestQuerier) GetActiveArgoCDClusterProxyTokenByClusterID(context.Context, uuid.UUID) (sqlc.ArgocdClusterProxyToken, error) {
	return sqlc.ArgocdClusterProxyToken{}, pgx.ErrNoRows
}

func (q *argoCDAutoRegisterTestQuerier) UpsertArgoCDClusterProxyToken(context.Context, sqlc.UpsertArgoCDClusterProxyTokenParams) (sqlc.ArgocdClusterProxyToken, error) {
	return sqlc.ArgocdClusterProxyToken{}, nil
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

func TestArgoCDAutoRegisterSweepUpsertsClusterWithStandardLabels(t *testing.T) {
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
