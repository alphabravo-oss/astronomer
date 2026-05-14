package handler

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type projectAsyncQuerier struct {
	upserts []sqlc.UpsertProjectNamespaceParams
	deletes []sqlc.DeleteProjectNamespaceParams
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
func (q *projectAsyncQuerier) CountProjectsByCluster(context.Context, uuid.UUID) (int64, error) {
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
func (q *projectAsyncQuerier) ListProjectNamespaces(context.Context, uuid.UUID) ([]sqlc.ProjectNamespace, error) {
	return nil, nil
}
func (q *projectAsyncQuerier) ListAllProjectNamespaces(context.Context) ([]sqlc.ProjectNamespace, error) {
	return nil, nil
}
func (q *projectAsyncQuerier) ClaimProjectNamespaceReconcile(context.Context, sqlc.ClaimProjectNamespaceReconcileParams) (sqlc.ProjectNamespace, error) {
	return sqlc.ProjectNamespace{}, nil
}
func (q *projectAsyncQuerier) MarkProjectNamespaceReconciled(context.Context, sqlc.MarkProjectNamespaceReconciledParams) error {
	return nil
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

type noopProjectRequester struct{}

func (noopProjectRequester) Do(context.Context, string, string, string, []byte, map[string]string) (*protocol.K8sResponsePayload, error) {
	return nil, nil
}

func TestProjectHandlerUpsertAndEnqueueRunsLocallyWhenRequesterPresent(t *testing.T) {
	queries := &projectAsyncQuerier{}
	h := NewProjectHandler(queries)
	h.requester = noopProjectRequester{}

	got := make(chan tasks.ProjectReconcilePayload, 1)
	h.runTask = func(_ context.Context, task *asynq.Task) error {
		var payload tasks.ProjectReconcilePayload
		if err := json.Unmarshal(task.Payload(), &payload); err != nil {
			return err
		}
		got <- payload
		return nil
	}

	projectID := uuid.New()
	clusterID := uuid.New()
	h.upsertAndEnqueue(context.Background(), projectID, clusterID, "team-a")

	if len(queries.upserts) != 1 {
		t.Fatalf("expected 1 project namespace upsert, got %d", len(queries.upserts))
	}

	select {
	case payload := <-got:
		if payload.Op != "apply" {
			t.Fatalf("expected op=apply, got %q", payload.Op)
		}
		if payload.ProjectID != projectID.String() || payload.ClusterID != clusterID.String() || payload.Namespace != "team-a" {
			t.Fatalf("unexpected payload: %+v", payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected local project reconcile task to run")
	}
}

func TestProjectHandlerEnqueueCleanupRunsLocallyWhenRequesterPresent(t *testing.T) {
	queries := &projectAsyncQuerier{}
	h := NewProjectHandler(queries)
	h.requester = noopProjectRequester{}

	got := make(chan tasks.ProjectReconcilePayload, 1)
	h.runTask = func(_ context.Context, task *asynq.Task) error {
		var payload tasks.ProjectReconcilePayload
		if err := json.Unmarshal(task.Payload(), &payload); err != nil {
			return err
		}
		got <- payload
		return nil
	}

	projectID := uuid.New()
	clusterID := uuid.New()
	h.enqueueCleanup(context.Background(), projectID, clusterID, "team-a")

	if len(queries.deletes) != 0 {
		t.Fatalf("expected no synchronous delete when requester is present, got %d", len(queries.deletes))
	}

	select {
	case payload := <-got:
		if payload.Op != "remove" {
			t.Fatalf("expected op=remove, got %q", payload.Op)
		}
		if payload.ProjectID != projectID.String() || payload.ClusterID != clusterID.String() || payload.Namespace != "team-a" {
			t.Fatalf("unexpected payload: %+v", payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected local project cleanup task to run")
	}
}
