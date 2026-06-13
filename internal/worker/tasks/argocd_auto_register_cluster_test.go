package tasks

import (
	"context"
	"encoding/json"
	"testing"
	"time"

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
	setting       *sqlc.PlatformSetting
	listTouched   bool
	cluster       sqlc.Cluster
	clusters      []sqlc.Cluster
	instances     []sqlc.ArgocdInstance
	managed       []sqlc.ArgocdManagedCluster
	created       []sqlc.CreateArgoCDManagedClusterParams
	conditions    []sqlc.UpsertClusterConditionParams
	managedLookup bool
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

func (q *argoCDAutoRegisterTestQuerier) GetPlatformSetting(context.Context, string) (sqlc.PlatformSetting, error) {
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

func TestArgoCDManagedClusterIndexRepairRebuildsMissingRowFromSecret(t *testing.T) {
	clusterID := uuid.New()
	instanceID := uuid.New()
	q := &argoCDAutoRegisterTestQuerier{
		cluster: sqlc.Cluster{
			ID:          clusterID,
			Name:        "prod-east",
			Environment: "production",
			Labels:      json.RawMessage(`{"tier":"prod"}`),
		},
		instances: []sqlc.ArgocdInstance{{ID: instanceID, Name: "built-in"}},
	}
	k8s := k8sfake.NewSimpleClientset(&corev1.Secret{
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
		Queries: q,
		K8s:     k8s,
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
	secret, err := k8s.CoreV1().Secrets(argoCDNamespace).Get(context.Background(), "cluster-prod-east", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	if secret.Labels[astronomerClusterNameLabelKey] != "prod-east" || secret.Labels[astronomerEnvironmentLabelKey] != "production" || secret.Labels[astronomerLabelPrefix+"tier"] != "prod" {
		t.Fatalf("secret labels were not refreshed from cluster row: %+v", secret.Labels)
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
	k8s := k8sfake.NewSimpleClientset(&corev1.Secret{
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
