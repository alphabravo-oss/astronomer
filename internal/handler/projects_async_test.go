package handler

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	projectdomain "github.com/alphabravocompany/astronomer-go/internal/projects"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
)

type projectAsyncQuerier struct {
	upserts []sqlc.UpsertProjectNamespaceParams
	deletes []sqlc.DeleteProjectNamespaceParams
	outbox  []sqlc.UpsertTaskOutboxParams
}

func (q *projectAsyncQuerier) GetProjectByID(context.Context, uuid.UUID) (sqlc.Project, error) {
	return sqlc.Project{}, nil
}
func (q *projectAsyncQuerier) GetClusterRegistryConfig(context.Context, uuid.UUID) (sqlc.ClusterRegistryConfig, error) {
	return sqlc.ClusterRegistryConfig{}, nil
}
func (q *projectAsyncQuerier) GetDefaultPodSecurityTemplate(context.Context) (sqlc.PodSecurityTemplate, error) {
	return sqlc.PodSecurityTemplate{}, nil
}
func (q *projectAsyncQuerier) ListProjects(context.Context, sqlc.ListProjectsParams) ([]sqlc.Project, error) {
	return nil, nil
}
func (q *projectAsyncQuerier) ListProjectsByCluster(context.Context, sqlc.ListProjectsByClusterParams) ([]sqlc.Project, error) {
	return nil, nil
}
func (q *projectAsyncQuerier) CreateProject(context.Context, sqlc.CreateProjectParams) (sqlc.Project, error) {
	return sqlc.Project{}, nil
}
func (q *projectAsyncQuerier) UpdateProject(context.Context, sqlc.UpdateProjectParams) (sqlc.Project, error) {
	return sqlc.Project{}, nil
}
func (q *projectAsyncQuerier) UpdateProjectPolicy(context.Context, sqlc.UpdateProjectPolicyParams) (sqlc.Project, error) {
	return sqlc.Project{}, nil
}
func (q *projectAsyncQuerier) GetClusterByID(context.Context, uuid.UUID) (sqlc.Cluster, error) {
	return sqlc.Cluster{}, nil
}
func (q *projectAsyncQuerier) DeleteProject(context.Context, uuid.UUID) error { return nil }
func (q *projectAsyncQuerier) CountProjects(context.Context) (int64, error)   { return 0, nil }
func (q *projectAsyncQuerier) CountProjectsFiltered(context.Context, string) (int64, error) {
	return 0, nil
}
func (q *projectAsyncQuerier) CountProjectsByCluster(context.Context, uuid.UUID) (int64, error) {
	return 0, nil
}
func (q *projectAsyncQuerier) CountProjectsByClusterFiltered(context.Context, sqlc.CountProjectsByClusterFilteredParams) (int64, error) {
	return 0, nil
}
func (q *projectAsyncQuerier) UpsertProjectNamespace(_ context.Context, arg sqlc.UpsertProjectNamespaceParams) (sqlc.ProjectNamespace, error) {
	q.upserts = append(q.upserts, arg)
	return sqlc.ProjectNamespace{}, nil
}
func (q *projectAsyncQuerier) DeleteProjectNamespace(_ context.Context, arg sqlc.DeleteProjectNamespaceParams) error {
	q.deletes = append(q.deletes, arg)
	return nil
}
func (q *projectAsyncQuerier) UpsertTaskOutbox(_ context.Context, arg sqlc.UpsertTaskOutboxParams) (sqlc.TaskOutbox, error) {
	q.outbox = append(q.outbox, arg)
	return sqlc.TaskOutbox{ID: uuid.New()}, nil
}
func (q *projectAsyncQuerier) ListProjectNamespaces(context.Context, uuid.UUID) ([]sqlc.ProjectNamespace, error) {
	return nil, nil
}
func (q *projectAsyncQuerier) UpsertProjectResourceQuotaAllocation(_ context.Context, arg sqlc.UpsertProjectResourceQuotaAllocationParams) (sqlc.ProjectResourceQuotaAllocation, error) {
	return sqlc.ProjectResourceQuotaAllocation{ProjectID: arg.ProjectID, ClusterID: arg.ClusterID, Namespace: arg.Namespace, CpuLimit: arg.CpuLimit, MemoryLimit: arg.MemoryLimit, PodCount: arg.PodCount}, nil
}
func (q *projectAsyncQuerier) DeleteProjectResourceQuotaAllocation(context.Context, sqlc.DeleteProjectResourceQuotaAllocationParams) error {
	return nil
}
func (q *projectAsyncQuerier) ListAllProjectNamespaces(context.Context) ([]sqlc.ProjectNamespace, error) {
	return nil, nil
}
func (q *projectAsyncQuerier) ClaimProjectNamespaceReconcile(context.Context, sqlc.ClaimProjectNamespaceReconcileParams) (sqlc.ProjectNamespace, error) {
	return sqlc.ProjectNamespace{}, nil
}
func (q *projectAsyncQuerier) MarkProjectNamespaceReconciled(context.Context, sqlc.MarkProjectNamespaceReconciledParams) (int64, error) {
	return 1, nil
}

// RBAC-matrix surface — async tests don't hit this; satisfy the
// interface with empty stubs.
func (q *projectAsyncQuerier) ListProjectRoleBindingsByProject(context.Context, sqlc.ListProjectRoleBindingsByProjectParams) ([]sqlc.ProjectRoleBinding, error) {
	return nil, nil
}
func (q *projectAsyncQuerier) GetProjectRoleByID(context.Context, uuid.UUID) (sqlc.ProjectRole, error) {
	return sqlc.ProjectRole{}, nil
}
func (q *projectAsyncQuerier) GetUserByID(context.Context, uuid.UUID) (sqlc.User, error) {
	return sqlc.User{}, nil
}

func TestProjectDomainEnsureAndEnqueueUsesTaskOutboxWithoutRequester(t *testing.T) {
	queries := &projectAsyncQuerier{}

	projectID := uuid.New()
	clusterID := uuid.New()
	if err := projectdomain.EnsureAndEnqueue(context.Background(), queries, queries, projectID, clusterID, "team-a"); err != nil {
		t.Fatalf("EnsureAndEnqueue() error = %v", err)
	}

	if len(queries.upserts) != 1 {
		t.Fatalf("expected 1 project namespace upsert, got %d", len(queries.upserts))
	}
	if len(queries.outbox) != 1 {
		t.Fatalf("expected 1 task_outbox row, got %d", len(queries.outbox))
	}
	row := queries.outbox[0]
	if !row.DedupeKey.Valid || row.DedupeKey.String != "project_reconcile:apply:"+projectID.String()+":"+clusterID.String()+":team-a" {
		t.Fatalf("dedupe key = %+v", row.DedupeKey)
	}
	if row.TaskType != tasks.ProjectReconcileType || row.QueueName != tasks.ClusterTemplateApplyQueueName || row.MaxDeliveryAttempts != 20 {
		t.Fatalf("outbox row = %+v", row)
	}
	var payload tasks.ProjectReconcilePayload
	if err := json.Unmarshal(row.Payload, &payload); err != nil {
		t.Fatalf("decode outbox payload: %v", err)
	}
	if payload.Op != "apply" || payload.ProjectID != projectID.String() || payload.ClusterID != clusterID.String() || payload.Namespace != "team-a" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestProjectDomainEnqueueCleanupUsesTaskOutboxWithoutRequester(t *testing.T) {
	queries := &projectAsyncQuerier{}

	projectID := uuid.New()
	clusterID := uuid.New()
	if err := projectdomain.EnqueueRemove(context.Background(), queries, projectID, clusterID, "team-a"); err != nil {
		t.Fatalf("EnqueueRemove() error = %v", err)
	}

	if len(queries.deletes) != 0 {
		t.Fatalf("expected task_outbox cleanup to leave DB row for task ownership, got %d deletes", len(queries.deletes))
	}
	if len(queries.outbox) != 1 {
		t.Fatalf("expected 1 task_outbox row, got %d", len(queries.outbox))
	}
	row := queries.outbox[0]
	if !row.DedupeKey.Valid || row.DedupeKey.String != "project_reconcile:remove:"+projectID.String()+":"+clusterID.String()+":team-a" {
		t.Fatalf("dedupe key = %+v", row.DedupeKey)
	}
	var payload tasks.ProjectReconcilePayload
	if err := json.Unmarshal(row.Payload, &payload); err != nil {
		t.Fatalf("decode outbox payload: %v", err)
	}
	if payload.Op != "remove" || payload.ProjectID != projectID.String() || payload.ClusterID != clusterID.String() || payload.Namespace != "team-a" {
		t.Fatalf("payload = %+v", payload)
	}
}
