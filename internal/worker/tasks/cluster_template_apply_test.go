package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// fakeApplyQuerier is the narrow ClusterTemplateApplyQuerier the worker
// tests stand up. Records every mutation in-memory so assertions can
// inspect the end state without poking the DB.
type fakeApplyQuerier struct {
	mu sync.Mutex

	cluster              sqlc.Cluster
	application          sqlc.ClusterTemplateApplication
	tools                map[string]sqlc.ClusterTool
	projects             map[string]sqlc.Project // key: cluster_id.name
	policies             map[uuid.UUID]sqlc.ClusterRegistrationPolicy
	installedCharts      map[uuid.UUID][]sqlc.InstalledChart
	statusTransitions    []string
	clusterUpdateCount   int
	projectCreateCount   int
	registrationPolicies int

	failOnUpdate bool
}

func newFakeApplyQuerier(cluster sqlc.Cluster, app sqlc.ClusterTemplateApplication) *fakeApplyQuerier {
	return &fakeApplyQuerier{
		cluster:         cluster,
		application:     app,
		tools:           map[string]sqlc.ClusterTool{},
		projects:        map[string]sqlc.Project{},
		policies:        map[uuid.UUID]sqlc.ClusterRegistrationPolicy{},
		installedCharts: map[uuid.UUID][]sqlc.InstalledChart{},
	}
}

func (f *fakeApplyQuerier) GetClusterByID(_ context.Context, id uuid.UUID) (sqlc.Cluster, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cluster.ID != id {
		return sqlc.Cluster{}, pgx.ErrNoRows
	}
	return f.cluster, nil
}

func (f *fakeApplyQuerier) UpdateCluster(_ context.Context, arg sqlc.UpdateClusterParams) (sqlc.Cluster, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failOnUpdate {
		return sqlc.Cluster{}, errors.New("forced update failure")
	}
	f.clusterUpdateCount++
	f.cluster.Environment = arg.Environment
	f.cluster.Labels = arg.Labels
	f.cluster.Region = arg.Region
	return f.cluster, nil
}

func (f *fakeApplyQuerier) GetClusterTemplateApplication(_ context.Context, clusterID uuid.UUID) (sqlc.ClusterTemplateApplication, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.application.ClusterID != clusterID {
		return sqlc.ClusterTemplateApplication{}, pgx.ErrNoRows
	}
	return f.application, nil
}

func (f *fakeApplyQuerier) MarkClusterTemplateApplicationStatus(_ context.Context, arg sqlc.MarkClusterTemplateApplicationStatusParams) (sqlc.ClusterTemplateApplication, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statusTransitions = append(f.statusTransitions, arg.Status)
	f.application.Status = arg.Status
	f.application.LastError = arg.LastError
	f.application.AppliedAt = arg.AppliedAt
	return f.application, nil
}

func (f *fakeApplyQuerier) ListClusterTemplateApplicationsByStatus(_ context.Context, _ sqlc.ListClusterTemplateApplicationsByStatusParams) ([]sqlc.ClusterTemplateApplication, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return []sqlc.ClusterTemplateApplication{f.application}, nil
}

func (f *fakeApplyQuerier) GetToolBySlug(_ context.Context, slug string) (sqlc.ClusterTool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tools[slug]
	if !ok {
		return sqlc.ClusterTool{}, pgx.ErrNoRows
	}
	return t, nil
}

func (f *fakeApplyQuerier) GetProjectByNameAndCluster(_ context.Context, arg sqlc.GetProjectByNameAndClusterParams) (sqlc.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := arg.ClusterID.String() + "." + arg.Name
	p, ok := f.projects[key]
	if !ok {
		return sqlc.Project{}, pgx.ErrNoRows
	}
	return p, nil
}

func (f *fakeApplyQuerier) CreateProject(_ context.Context, arg sqlc.CreateProjectParams) (sqlc.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := arg.ClusterID.String() + "." + arg.Name
	p := sqlc.Project{
		ID:                       uuid.New(),
		Name:                     arg.Name,
		DisplayName:              arg.DisplayName,
		ClusterID:                arg.ClusterID,
		PodSecurityProfile:       arg.PodSecurityProfile,
		ResourceQuotaCpuLimit:    arg.ResourceQuotaCpuLimit,
		ResourceQuotaMemoryLimit: arg.ResourceQuotaMemoryLimit,
		NetworkPolicyMode:        arg.NetworkPolicyMode,
	}
	f.projects[key] = p
	f.projectCreateCount++
	return p, nil
}

func (f *fakeApplyQuerier) UpsertClusterRegistrationPolicy(_ context.Context, arg sqlc.UpsertClusterRegistrationPolicyParams) (sqlc.ClusterRegistrationPolicy, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	pol := sqlc.ClusterRegistrationPolicy{
		ClusterID:         arg.ClusterID,
		TokenRotationDays: arg.TokenRotationDays,
		SourceTemplateID:  arg.SourceTemplateID,
	}
	f.policies[arg.ClusterID] = pol
	f.registrationPolicies++
	return pol, nil
}

func (f *fakeApplyQuerier) ListInstalledChartsByCluster(_ context.Context, arg sqlc.ListInstalledChartsByClusterParams) ([]sqlc.InstalledChart, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.installedCharts[arg.ClusterID], nil
}

// fakeInstaller records EnsureInstalled calls so the test can assert
// that each spec.tools entry produced exactly one install request.
type fakeInstaller struct {
	mu       sync.Mutex
	installs []fakeInstallCall
}

type fakeInstallCall struct {
	ClusterID uuid.UUID
	Slug      string
	Preset    string
}

func (f *fakeInstaller) EnsureInstalled(_ context.Context, clusterID uuid.UUID, slug, _ /*releaseName*/, preset, _ /*valuesYAML*/ string) (sqlc.InstalledChart, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.installs = append(f.installs, fakeInstallCall{ClusterID: clusterID, Slug: slug, Preset: preset})
	return sqlc.InstalledChart{}, nil
}

// ────────────────────────────────────────────────────────────────────────
// Tests
// ────────────────────────────────────────────────────────────────────────

// TestClusterTemplate_Apply_SetsLabelsAndEnvironment verifies steps 2+3.
func TestClusterTemplate_Apply_SetsLabelsAndEnvironment(t *testing.T) {
	clusterID := uuid.New()
	tmplID := uuid.New()
	spec := json.RawMessage(`{"environment":"production","labels":{"tier":"prod"}}`)
	q := newFakeApplyQuerier(
		sqlc.Cluster{ID: clusterID, Name: "demo", Environment: "development", Labels: json.RawMessage(`{}`), Annotations: json.RawMessage(`{}`)},
		sqlc.ClusterTemplateApplication{ClusterID: clusterID, TemplateID: tmplID, SpecSnapshot: spec, Status: "pending"},
	)

	deps := ClusterTemplateApplyDeps{Queries: q}
	if err := runClusterTemplateApply(context.Background(), deps, clusterID); err != nil {
		t.Fatalf("runClusterTemplateApply: %v", err)
	}
	if q.cluster.Environment != "production" {
		t.Errorf("environment=%s, want production", q.cluster.Environment)
	}
	var labels map[string]string
	_ = json.Unmarshal(q.cluster.Labels, &labels)
	if labels["tier"] != "prod" {
		t.Errorf("labels=%v, want tier=prod", labels)
	}
	if got := lastStatus(q); got != "applied" {
		t.Errorf("final status=%s, want applied", got)
	}
}

// TestClusterTemplate_Apply_EnqueuesToolInstalls verifies step 4 — each
// spec.tools entry triggers EnsureInstalled on the Installer.
func TestClusterTemplate_Apply_EnqueuesToolInstalls(t *testing.T) {
	clusterID := uuid.New()
	tmplID := uuid.New()
	spec := json.RawMessage(`{"tools":[{"slug":"argocd","preset":"ha"},{"slug":"cert-manager"}]}`)
	q := newFakeApplyQuerier(
		sqlc.Cluster{ID: clusterID, Name: "demo", Environment: "development", Labels: json.RawMessage(`{}`)},
		sqlc.ClusterTemplateApplication{ClusterID: clusterID, TemplateID: tmplID, SpecSnapshot: spec, Status: "pending"},
	)
	installer := &fakeInstaller{}
	deps := ClusterTemplateApplyDeps{Queries: q, Installer: installer}
	if err := runClusterTemplateApply(context.Background(), deps, clusterID); err != nil {
		t.Fatalf("runClusterTemplateApply: %v", err)
	}
	if len(installer.installs) != 2 {
		t.Fatalf("installer received %d installs, want 2", len(installer.installs))
	}
	if installer.installs[0].Slug != "argocd" || installer.installs[0].Preset != "ha" {
		t.Errorf("installs[0]=%+v", installer.installs[0])
	}
	if installer.installs[1].Slug != "cert-manager" {
		t.Errorf("installs[1]=%+v", installer.installs[1])
	}
}

// TestClusterTemplate_Apply_CreatesDefaultProject verifies step 5.
func TestClusterTemplate_Apply_CreatesDefaultProject(t *testing.T) {
	clusterID := uuid.New()
	tmplID := uuid.New()
	spec := json.RawMessage(`{"default_project":{"name":"platform","pod_security_profile":"baseline","resource_quota_cpu_limit":"8","resource_quota_memory_limit":"16Gi"}}`)
	q := newFakeApplyQuerier(
		sqlc.Cluster{ID: clusterID, Name: "demo", Labels: json.RawMessage(`{}`)},
		sqlc.ClusterTemplateApplication{ClusterID: clusterID, TemplateID: tmplID, SpecSnapshot: spec, Status: "pending"},
	)
	deps := ClusterTemplateApplyDeps{Queries: q}
	if err := runClusterTemplateApply(context.Background(), deps, clusterID); err != nil {
		t.Fatalf("runClusterTemplateApply: %v", err)
	}
	if q.projectCreateCount != 1 {
		t.Errorf("projectCreateCount=%d, want 1", q.projectCreateCount)
	}
	p := q.projects[clusterID.String()+".platform"]
	if p.PodSecurityProfile != "baseline" {
		t.Errorf("PSS=%s", p.PodSecurityProfile)
	}
	if p.ResourceQuotaCpuLimit != "8" {
		t.Errorf("cpu limit=%s", p.ResourceQuotaCpuLimit)
	}
}

// TestClusterTemplate_Apply_Idempotent runs apply twice and verifies the
// second pass is a no-op (cluster not re-updated, project not re-created).
func TestClusterTemplate_Apply_Idempotent(t *testing.T) {
	clusterID := uuid.New()
	tmplID := uuid.New()
	spec := json.RawMessage(`{"environment":"production","labels":{"tier":"prod"},"default_project":{"name":"platform"},"registration_policy":{"token_rotation_days":90}}`)
	q := newFakeApplyQuerier(
		sqlc.Cluster{ID: clusterID, Name: "demo", Environment: "development", Labels: json.RawMessage(`{}`), Annotations: json.RawMessage(`{}`)},
		sqlc.ClusterTemplateApplication{ClusterID: clusterID, TemplateID: tmplID, SpecSnapshot: spec, Status: "pending"},
	)
	deps := ClusterTemplateApplyDeps{Queries: q}

	if err := runClusterTemplateApply(context.Background(), deps, clusterID); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	firstUpdates := q.clusterUpdateCount
	firstProjects := q.projectCreateCount
	firstPolicies := q.registrationPolicies

	if firstUpdates != 1 {
		t.Errorf("first apply clusterUpdateCount=%d, want 1", firstUpdates)
	}
	if firstProjects != 1 {
		t.Errorf("first apply projectCreateCount=%d, want 1", firstProjects)
	}

	if err := runClusterTemplateApply(context.Background(), deps, clusterID); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if q.clusterUpdateCount != firstUpdates {
		t.Errorf("idempotent: clusterUpdateCount went from %d to %d", firstUpdates, q.clusterUpdateCount)
	}
	if q.projectCreateCount != firstProjects {
		t.Errorf("idempotent: projectCreateCount went from %d to %d", firstProjects, q.projectCreateCount)
	}
	// Policy upsert IS called both times — it's an ON CONFLICT DO UPDATE
	// so re-running with the same values is a DB-level no-op, but the
	// worker doesn't try to skip it (the check would be more code than
	// the no-op write). Assert both calls happened.
	if q.registrationPolicies != firstPolicies+1 {
		t.Errorf("policies: want %d, got %d", firstPolicies+1, q.registrationPolicies)
	}
}

// TestClusterTemplate_Reapply_OnDrift simulates an operator manually
// editing the cluster's environment between two applies — the second
// apply should converge it back.
func TestClusterTemplate_Reapply_OnDrift(t *testing.T) {
	clusterID := uuid.New()
	tmplID := uuid.New()
	spec := json.RawMessage(`{"environment":"production","labels":{"tier":"prod"}}`)
	q := newFakeApplyQuerier(
		sqlc.Cluster{ID: clusterID, Name: "demo", Environment: "development", Labels: json.RawMessage(`{}`), Annotations: json.RawMessage(`{}`)},
		sqlc.ClusterTemplateApplication{ClusterID: clusterID, TemplateID: tmplID, SpecSnapshot: spec, Status: "pending"},
	)
	deps := ClusterTemplateApplyDeps{Queries: q}
	if err := runClusterTemplateApply(context.Background(), deps, clusterID); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	if q.cluster.Environment != "production" {
		t.Fatalf("first apply did not set environment to production")
	}

	// Simulate the operator manually editing the cluster.
	q.cluster.Environment = "staging"
	q.cluster.Labels = json.RawMessage(`{}`)
	q.application.Status = "pending" // reapply path would reset this.

	if err := runClusterTemplateApply(context.Background(), deps, clusterID); err != nil {
		t.Fatalf("reapply: %v", err)
	}
	if q.cluster.Environment != "production" {
		t.Errorf("reapply: environment=%s, want production", q.cluster.Environment)
	}
}

// TestClusterTemplate_Apply_FailedStatusOnDBError checks the failure
// branch — when UpdateCluster fails, status goes to 'failed' with the
// error captured.
func TestClusterTemplate_Apply_FailedStatusOnDBError(t *testing.T) {
	clusterID := uuid.New()
	tmplID := uuid.New()
	spec := json.RawMessage(`{"environment":"production"}`)
	q := newFakeApplyQuerier(
		sqlc.Cluster{ID: clusterID, Name: "demo", Environment: "development", Labels: json.RawMessage(`{}`), Annotations: json.RawMessage(`{}`)},
		sqlc.ClusterTemplateApplication{ClusterID: clusterID, TemplateID: tmplID, SpecSnapshot: spec, Status: "pending"},
	)
	q.failOnUpdate = true
	deps := ClusterTemplateApplyDeps{Queries: q}
	if err := runClusterTemplateApply(context.Background(), deps, clusterID); err != nil {
		t.Fatalf("runClusterTemplateApply: %v", err)
	}
	if q.application.Status != "failed" {
		t.Errorf("status=%s, want failed", q.application.Status)
	}
	if q.application.LastError == "" {
		t.Errorf("expected last_error to be set")
	}
}

// TestClusterTemplate_DriftCheck_SmokeTest exercises the periodic sweep
// without driving it through asynq. With no drift, the sweep should
// return cleanly.
func TestClusterTemplate_DriftCheck_SmokeTest(t *testing.T) {
	clusterID := uuid.New()
	tmplID := uuid.New()
	spec := json.RawMessage(`{"environment":"production","labels":{"tier":"prod"}}`)
	q := newFakeApplyQuerier(
		sqlc.Cluster{ID: clusterID, Name: "demo", Environment: "production", Labels: json.RawMessage(`{"tier":"prod"}`)},
		sqlc.ClusterTemplateApplication{ClusterID: clusterID, TemplateID: tmplID, SpecSnapshot: spec, Status: "applied"},
	)
	ConfigureClusterTemplateApply(ClusterTemplateApplyDeps{Queries: q})
	defer ResetClusterTemplateApply()
	if err := HandleClusterTemplateDriftCheck(context.Background(), nil); err != nil {
		t.Errorf("drift check: %v", err)
	}
}

// lastStatus returns the most recent status string written to the fake.
func lastStatus(q *fakeApplyQuerier) string {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.statusTransitions) == 0 {
		return ""
	}
	return q.statusTransitions[len(q.statusTransitions)-1]
}
