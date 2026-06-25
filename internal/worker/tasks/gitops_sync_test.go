// Hermetic gitops_sync tests. Every test spins up a local bare repo via
// go-git PlainInit + a per-test working clone, writes one or more
// ClusterRegistration files, commits, then drives SyncSource against an
// in-process fake GitOpsQuerier. No network. No shell-out. No external
// dependencies.

package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/gitops"
)

// ----- fake querier -------------------------------------------------

type fakeGitOpsQuerier struct {
	sources  map[uuid.UUID]sqlc.GitopsRegistrationSource
	clusters map[uuid.UUID]sqlc.Cluster
	byName   map[string]uuid.UUID

	templates map[string]sqlc.ClusterTemplate

	links map[uuid.UUID]sqlc.GitopsRegisteredCluster // keyed by cluster_id

	createdDecoms []sqlc.ClusterDecommission
	auditRows     []sqlc.CreateAuditLogV1Params

	now func() time.Time

	// stamping
	stampedSyncedSHA map[uuid.UUID]string
	stampedSyncedAt  map[uuid.UUID]time.Time
	stampedErrors    map[uuid.UUID]string
}

func newFakeQuerier() *fakeGitOpsQuerier {
	return &fakeGitOpsQuerier{
		sources:          map[uuid.UUID]sqlc.GitopsRegistrationSource{},
		clusters:         map[uuid.UUID]sqlc.Cluster{},
		byName:           map[string]uuid.UUID{},
		templates:        map[string]sqlc.ClusterTemplate{},
		links:            map[uuid.UUID]sqlc.GitopsRegisteredCluster{},
		stampedSyncedSHA: map[uuid.UUID]string{},
		stampedSyncedAt:  map[uuid.UUID]time.Time{},
		stampedErrors:    map[uuid.UUID]string{},
		now:              time.Now,
	}
}

func (f *fakeGitOpsQuerier) ListEnabledGitOpsSources(ctx context.Context) ([]sqlc.GitopsRegistrationSource, error) {
	out := []sqlc.GitopsRegistrationSource{}
	for _, s := range f.sources {
		if s.Enabled {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *fakeGitOpsQuerier) GetGitOpsSource(ctx context.Context, id uuid.UUID) (sqlc.GitopsRegistrationSource, error) {
	s, ok := f.sources[id]
	if !ok {
		return sqlc.GitopsRegistrationSource{}, pgx.ErrNoRows
	}
	return s, nil
}

func (f *fakeGitOpsQuerier) ListGitOpsRegisteredClustersBySource(ctx context.Context, sourceID uuid.UUID) ([]sqlc.GitopsRegisteredCluster, error) {
	out := []sqlc.GitopsRegisteredCluster{}
	for _, link := range f.links {
		if link.SourceID == sourceID {
			out = append(out, link)
		}
	}
	return out, nil
}

func (f *fakeGitOpsQuerier) TombstoneGitOpsRegisteredCluster(ctx context.Context, arg sqlc.TombstoneGitOpsRegisteredClusterParams) error {
	link, ok := f.links[arg.ClusterID]
	if !ok {
		return pgx.ErrNoRows
	}
	link.Status = "tombstoned"
	link.TombstonedAt = arg.TombstonedAt
	f.links[arg.ClusterID] = link
	return nil
}

func (f *fakeGitOpsQuerier) DeleteGitOpsRegisteredCluster(ctx context.Context, clusterID uuid.UUID) error {
	delete(f.links, clusterID)
	return nil
}

func (f *fakeGitOpsQuerier) StampGitOpsSourceSync(ctx context.Context, arg sqlc.StampGitOpsSourceSyncParams) error {
	src := f.sources[arg.ID]
	src.LastSyncedAt = arg.LastSyncedAt
	src.LastSyncedSha = arg.LastSyncedSha
	src.LastError = ""
	f.sources[arg.ID] = src
	f.stampedSyncedSHA[arg.ID] = arg.LastSyncedSha
	f.stampedSyncedAt[arg.ID] = arg.LastSyncedAt.Time
	return nil
}

func (f *fakeGitOpsQuerier) StampGitOpsSourceError(ctx context.Context, arg sqlc.StampGitOpsSourceErrorParams) error {
	src := f.sources[arg.ID]
	src.LastError = arg.LastError
	f.sources[arg.ID] = src
	f.stampedErrors[arg.ID] = arg.LastError
	return nil
}

func (f *fakeGitOpsQuerier) ListExpiredTombstones(ctx context.Context, cutoff pgtype.Timestamptz) ([]sqlc.GitopsRegisteredCluster, error) {
	out := []sqlc.GitopsRegisteredCluster{}
	for _, link := range f.links {
		if link.Status == "tombstoned" && link.TombstonedAt.Valid && !link.TombstonedAt.Time.After(cutoff.Time) {
			out = append(out, link)
		}
	}
	return out, nil
}

func (f *fakeGitOpsQuerier) CountGitOpsRegisteredClustersBySource(ctx context.Context, sourceID uuid.UUID) (int64, error) {
	n := int64(0)
	for _, link := range f.links {
		if link.SourceID == sourceID {
			n++
		}
	}
	return n, nil
}

func (f *fakeGitOpsQuerier) CountGitOpsTombstonedBySource(ctx context.Context, sourceID uuid.UUID) (int64, error) {
	n := int64(0)
	for _, link := range f.links {
		if link.SourceID == sourceID && link.Status == "tombstoned" {
			n++
		}
	}
	return n, nil
}

func (f *fakeGitOpsQuerier) GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error) {
	c, ok := f.clusters[id]
	if !ok {
		return sqlc.Cluster{}, pgx.ErrNoRows
	}
	return c, nil
}

func (f *fakeGitOpsQuerier) GetClusterByName(ctx context.Context, name string) (sqlc.Cluster, error) {
	id, ok := f.byName[name]
	if !ok {
		return sqlc.Cluster{}, pgx.ErrNoRows
	}
	return f.clusters[id], nil
}

func (f *fakeGitOpsQuerier) CreateCluster(ctx context.Context, arg sqlc.CreateClusterParams) (sqlc.Cluster, error) {
	id := uuid.New()
	c := sqlc.Cluster{
		ID:           id,
		Name:         arg.Name,
		DisplayName:  arg.DisplayName,
		Description:  arg.Description,
		Status:       "registered",
		Environment:  arg.Environment,
		Region:       arg.Region,
		Provider:     arg.Provider,
		Distribution: arg.Distribution,
		CreatedAt:    f.now(),
		UpdatedAt:    f.now(),
		Labels:       []byte(`{}`),
		Annotations:  []byte(`{}`),
	}
	f.clusters[id] = c
	f.byName[c.Name] = id
	return c, nil
}

func (f *fakeGitOpsQuerier) UpdateCluster(ctx context.Context, arg sqlc.UpdateClusterParams) (sqlc.Cluster, error) {
	c, ok := f.clusters[arg.ID]
	if !ok {
		return sqlc.Cluster{}, pgx.ErrNoRows
	}
	c.DisplayName = arg.DisplayName
	c.Description = arg.Description
	c.Environment = arg.Environment
	c.Region = arg.Region
	c.Labels = arg.Labels
	c.Annotations = arg.Annotations
	c.UpdatedAt = f.now()
	f.clusters[arg.ID] = c
	return c, nil
}

func (f *fakeGitOpsQuerier) GetClusterTemplateByName(ctx context.Context, name string) (sqlc.ClusterTemplate, error) {
	t, ok := f.templates[name]
	if !ok {
		return sqlc.ClusterTemplate{}, pgx.ErrNoRows
	}
	return t, nil
}

func (f *fakeGitOpsQuerier) UpsertClusterTemplateApplication(ctx context.Context, arg sqlc.UpsertClusterTemplateApplicationParams) (sqlc.ClusterTemplateApplication, error) {
	return sqlc.ClusterTemplateApplication{
		ClusterID:    arg.ClusterID,
		TemplateID:   arg.TemplateID,
		Status:       "pending",
		SpecSnapshot: arg.SpecSnapshot,
	}, nil
}

func (f *fakeGitOpsQuerier) UpsertGitOpsRegisteredCluster(ctx context.Context, arg sqlc.UpsertGitOpsRegisteredClusterParams) (sqlc.GitopsRegisteredCluster, error) {
	link := sqlc.GitopsRegisteredCluster{
		ClusterID:     arg.ClusterID,
		SourceID:      arg.SourceID,
		RepoPath:      arg.RepoPath,
		LastYamlSha:   arg.LastYamlSha,
		LastAppliedAt: f.now(),
		Status:        "active",
		CreatedAt:     f.now(),
		UpdatedAt:     f.now(),
	}
	f.links[arg.ClusterID] = link
	return link, nil
}

func (f *fakeGitOpsQuerier) CreateClusterDecommission(ctx context.Context, arg sqlc.CreateClusterDecommissionParams) (sqlc.ClusterDecommission, error) {
	d := sqlc.ClusterDecommission{
		ID:          uuid.New(),
		ClusterID:   arg.ClusterID,
		ClusterName: arg.ClusterName,
		Status:      "pending",
	}
	f.createdDecoms = append(f.createdDecoms, d)
	return d, nil
}

func (f *fakeGitOpsQuerier) CreateAuditLogV1(ctx context.Context, arg sqlc.CreateAuditLogV1Params) error {
	f.auditRows = append(f.auditRows, arg)
	return nil
}

// fakeEnqueuer captures asynq enqueue calls without touching Redis.
type fakeEnqueuer struct {
	tasks []*asynq.Task
}

func (f *fakeEnqueuer) Enqueue(task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	f.tasks = append(f.tasks, task)
	return &asynq.TaskInfo{}, nil
}

// ----- helpers -------------------------------------------------------

// makeBareRepo creates a hermetic bare repo and a working clone with one
// initial commit. Returns the bare repo URL (file:// path) and the
// working clone path so tests can add more files.
func makeBareRepo(t *testing.T) (string, string) {
	t.Helper()
	bare := filepath.Join(t.TempDir(), "bare.git")
	if _, err := git.PlainInit(bare, true); err != nil {
		t.Fatalf("PlainInit bare: %v", err)
	}
	work := filepath.Join(t.TempDir(), "work")
	repo, err := git.PlainInit(work, false)
	if err != nil {
		t.Fatalf("PlainInit work: %v", err)
	}
	// Re-point HEAD to refs/heads/main so the first commit creates main.
	cfg, err := repo.Config()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	cfg.Init.DefaultBranch = "main"
	if err := repo.SetConfig(cfg); err != nil {
		t.Fatalf("set config: %v", err)
	}
	// Force initial branch via reference rewrite.
	if _, err := repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{bare}}); err != nil {
		t.Fatalf("create remote: %v", err)
	}
	// We can't easily flip the default branch via go-git; instead we'll
	// commit on whatever branch and let the test use that branch.
	if err := writeCommit(t, work, "README.md", "# test repo\n", "initial"); err != nil {
		t.Fatalf("initial commit: %v", err)
	}
	// Push the current branch to origin/main.
	if err := pushBranchAsMain(t, work); err != nil {
		t.Fatalf("push: %v", err)
	}
	return bare, work
}

func writeCommit(t *testing.T, workDir, relPath, content, msg string) error {
	t.Helper()
	full := filepath.Join(workDir, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		return err
	}
	repo, err := git.PlainOpen(workDir)
	if err != nil {
		return err
	}
	wt, err := repo.Worktree()
	if err != nil {
		return err
	}
	if _, err := wt.Add(relPath); err != nil {
		return err
	}
	_, err = wt.Commit(msg, &git.CommitOptions{
		Author: &object.Signature{Name: "test", Email: "test@example.com", When: time.Now()},
	})
	return err
}

func removeAndCommit(t *testing.T, workDir, relPath string) error {
	t.Helper()
	if err := os.Remove(filepath.Join(workDir, relPath)); err != nil {
		return err
	}
	repo, err := git.PlainOpen(workDir)
	if err != nil {
		return err
	}
	wt, err := repo.Worktree()
	if err != nil {
		return err
	}
	if _, err := wt.Remove(relPath); err != nil {
		return err
	}
	_, err = wt.Commit("rm "+relPath, &git.CommitOptions{
		Author: &object.Signature{Name: "test", Email: "test@example.com", When: time.Now()},
	})
	return err
}

func pushBranchAsMain(t *testing.T, workDir string) error {
	t.Helper()
	repo, err := git.PlainOpen(workDir)
	if err != nil {
		return err
	}
	head, err := repo.Head()
	if err != nil {
		return err
	}
	currentBranch := head.Name()
	refspec := config.RefSpec(currentBranch.String() + ":refs/heads/main")
	return repo.Push(&git.PushOptions{
		RemoteName: "origin",
		RefSpecs:   []config.RefSpec{refspec},
		Force:      true,
	})
}

func setupSource(t *testing.T, q *fakeGitOpsQuerier, bareURL, onDelete string, syncMode string) sqlc.GitopsRegistrationSource {
	t.Helper()
	src := sqlc.GitopsRegistrationSource{
		ID:                  uuid.New(),
		Name:                "test-source",
		RepoUrl:             "file://" + bareURL,
		Branch:              "main",
		PathPrefix:          "clusters",
		AuthMode:            "none",
		SyncMode:            syncMode,
		SyncIntervalSeconds: 60,
		OnDelete:            onDelete,
		Enabled:             true,
		CreatedAt:           time.Now(),
		UpdatedAt:           time.Now(),
	}
	q.sources[src.ID] = src
	return src
}

func clusterRegistrationYAML(name string, labels map[string]string) string {
	yaml := "apiVersion: astronomer.alphabravo.io/v1\nkind: ClusterRegistration\nmetadata:\n  name: " + name + "\nspec:\n"
	if len(labels) > 0 {
		yaml += "  labels:\n"
		for k, v := range labels {
			yaml += "    " + k + ": " + v + "\n"
		}
	}
	return yaml
}

// ----- tests --------------------------------------------------------

func TestSync_RegistersNewCluster(t *testing.T) {
	ResetGitOps()
	defer ResetGitOps()
	q := newFakeQuerier()
	bare, work := makeBareRepo(t)
	if err := writeCommit(t, work, "clusters/prod-east.yaml", clusterRegistrationYAML("prod-east", map[string]string{"tier": "prod"}), "add prod"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := pushBranchAsMain(t, work); err != nil {
		t.Fatalf("push: %v", err)
	}

	src := setupSource(t, q, bare, "log", "interval")
	ConfigureGitOps(GitOpsDeps{
		Queries:   q,
		CloneRoot: t.TempDir(),
		Now:       func() time.Time { return time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC) },
	})

	if err := SyncSource(context.Background(), src.ID); err != nil {
		t.Fatalf("SyncSource: %v", err)
	}
	if len(q.clusters) != 1 {
		t.Fatalf("expected 1 cluster, got %d", len(q.clusters))
	}
	if id, ok := q.byName["prod-east"]; !ok {
		t.Fatalf("cluster prod-east not registered")
	} else {
		if _, ok := q.links[id]; !ok {
			t.Fatalf("link row not upserted")
		}
	}
	// Source row must be stamped synced.
	if q.stampedSyncedSHA[src.ID] == "" {
		t.Fatalf("source not stamped with HEAD sha")
	}
}

func TestSync_NoOpsOnConvergedSha(t *testing.T) {
	ResetGitOps()
	defer ResetGitOps()
	q := newFakeQuerier()
	bare, work := makeBareRepo(t)
	if err := writeCommit(t, work, "clusters/prod-east.yaml", clusterRegistrationYAML("prod-east", nil), "add prod"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := pushBranchAsMain(t, work); err != nil {
		t.Fatalf("push: %v", err)
	}
	src := setupSource(t, q, bare, "log", "interval")
	ConfigureGitOps(GitOpsDeps{Queries: q, CloneRoot: t.TempDir(), Now: time.Now})
	if err := SyncSource(context.Background(), src.ID); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	// Mutate cluster locally so we can detect any further updates.
	id := q.byName["prod-east"]
	c := q.clusters[id]
	c.DisplayName = "marker"
	q.clusters[id] = c

	if err := SyncSource(context.Background(), src.ID); err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if q.clusters[id].DisplayName != "marker" {
		t.Fatalf("converged-sha no-op failed: display_name was overwritten to %q", q.clusters[id].DisplayName)
	}
}

func TestSync_UpdatesLabelsOnYAMLChange(t *testing.T) {
	ResetGitOps()
	defer ResetGitOps()
	q := newFakeQuerier()
	bare, work := makeBareRepo(t)
	if err := writeCommit(t, work, "clusters/prod-east.yaml", clusterRegistrationYAML("prod-east", map[string]string{"tier": "prod"}), "add"); err != nil {
		t.Fatalf("commit1: %v", err)
	}
	if err := pushBranchAsMain(t, work); err != nil {
		t.Fatalf("push1: %v", err)
	}
	src := setupSource(t, q, bare, "log", "interval")
	ConfigureGitOps(GitOpsDeps{Queries: q, CloneRoot: t.TempDir(), Now: time.Now})
	if err := SyncSource(context.Background(), src.ID); err != nil {
		t.Fatalf("sync1: %v", err)
	}

	// Update labels in repo.
	if err := writeCommit(t, work, "clusters/prod-east.yaml", clusterRegistrationYAML("prod-east", map[string]string{"tier": "prod", "owner": "platform"}), "update"); err != nil {
		t.Fatalf("commit2: %v", err)
	}
	if err := pushBranchAsMain(t, work); err != nil {
		t.Fatalf("push2: %v", err)
	}
	if err := SyncSource(context.Background(), src.ID); err != nil {
		t.Fatalf("sync2: %v", err)
	}
	id := q.byName["prod-east"]
	if string(q.clusters[id].Labels) == `{}` {
		t.Fatalf("labels not updated; got %s", string(q.clusters[id].Labels))
	}
}

func TestSync_LogsMissingUnderLogPolicy(t *testing.T) {
	ResetGitOps()
	defer ResetGitOps()
	q := newFakeQuerier()
	bare, work := makeBareRepo(t)
	if err := writeCommit(t, work, "clusters/prod-east.yaml", clusterRegistrationYAML("prod-east", nil), "add"); err != nil {
		t.Fatalf("commit1: %v", err)
	}
	if err := pushBranchAsMain(t, work); err != nil {
		t.Fatalf("push1: %v", err)
	}
	src := setupSource(t, q, bare, "log", "interval")
	ConfigureGitOps(GitOpsDeps{Queries: q, CloneRoot: t.TempDir(), Now: time.Now})
	if err := SyncSource(context.Background(), src.ID); err != nil {
		t.Fatalf("sync1: %v", err)
	}
	// Remove the file.
	if err := removeAndCommit(t, work, "clusters/prod-east.yaml"); err != nil {
		t.Fatalf("rm: %v", err)
	}
	if err := pushBranchAsMain(t, work); err != nil {
		t.Fatalf("push2: %v", err)
	}
	q.auditRows = nil
	if err := SyncSource(context.Background(), src.ID); err != nil {
		t.Fatalf("sync2: %v", err)
	}
	// link row remains (log policy doesn't tombstone)
	if len(q.links) != 1 {
		t.Fatalf("log policy must keep link rows; got %d", len(q.links))
	}
	if !containsAction(q.auditRows, "gitops.cluster.missing") {
		t.Fatalf("expected gitops.cluster.missing audit")
	}
}

func TestSync_TombstonesUnderTombstonePolicy(t *testing.T) {
	ResetGitOps()
	defer ResetGitOps()
	q := newFakeQuerier()
	bare, work := makeBareRepo(t)
	if err := writeCommit(t, work, "clusters/prod-east.yaml", clusterRegistrationYAML("prod-east", nil), "add"); err != nil {
		t.Fatalf("commit1: %v", err)
	}
	if err := pushBranchAsMain(t, work); err != nil {
		t.Fatalf("push1: %v", err)
	}
	src := setupSource(t, q, bare, "tombstone", "interval")
	ConfigureGitOps(GitOpsDeps{Queries: q, CloneRoot: t.TempDir(), Now: time.Now})
	if err := SyncSource(context.Background(), src.ID); err != nil {
		t.Fatalf("sync1: %v", err)
	}
	if err := removeAndCommit(t, work, "clusters/prod-east.yaml"); err != nil {
		t.Fatalf("rm: %v", err)
	}
	if err := pushBranchAsMain(t, work); err != nil {
		t.Fatalf("push2: %v", err)
	}
	if err := SyncSource(context.Background(), src.ID); err != nil {
		t.Fatalf("sync2: %v", err)
	}
	id := q.byName["prod-east"]
	if q.links[id].Status != "tombstoned" {
		t.Fatalf("link status = %q, want tombstoned", q.links[id].Status)
	}
}

func TestSync_DecommissionsAfterGrace(t *testing.T) {
	ResetGitOps()
	defer ResetGitOps()
	q := newFakeQuerier()
	bare, work := makeBareRepo(t)
	if err := writeCommit(t, work, "clusters/prod-east.yaml", clusterRegistrationYAML("prod-east", nil), "add"); err != nil {
		t.Fatalf("commit1: %v", err)
	}
	if err := pushBranchAsMain(t, work); err != nil {
		t.Fatalf("push1: %v", err)
	}
	src := setupSource(t, q, bare, "tombstone", "interval")
	enq := &fakeEnqueuer{}
	t0 := time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)
	currentTime := t0
	ConfigureGitOps(GitOpsDeps{
		Queries:   q,
		Enqueuer:  enq,
		CloneRoot: t.TempDir(),
		Now:       func() time.Time { return currentTime },
	})

	// Tick 1: register.
	if err := SyncSource(context.Background(), src.ID); err != nil {
		t.Fatalf("sync1: %v", err)
	}
	// Remove the file.
	if err := removeAndCommit(t, work, "clusters/prod-east.yaml"); err != nil {
		t.Fatalf("rm: %v", err)
	}
	if err := pushBranchAsMain(t, work); err != nil {
		t.Fatalf("push2: %v", err)
	}
	if err := SyncSource(context.Background(), src.ID); err != nil {
		t.Fatalf("sync2: %v", err)
	}
	// Advance the clock past the grace window and run the global tick.
	currentTime = t0.Add(GitOpsTombstoneGrace + time.Minute)
	// Re-stamp last_synced_at into the past so the reaper picks it up
	// (the reaper runs at end of HandleGitOpsSync, not SyncSource).
	if err := HandleGitOpsSync(context.Background(), nil); err != nil {
		t.Fatalf("HandleGitOpsSync: %v", err)
	}
	if len(enq.tasks) == 0 {
		t.Fatalf("expected cluster:decommission enqueue after grace")
	}
	if enq.tasks[0].Type() != "cluster:decommission" {
		t.Fatalf("enqueued type = %q, want cluster:decommission", enq.tasks[0].Type())
	}
}

func TestSync_DecommissionsImmediatelyUnderDecommissionPolicy(t *testing.T) {
	ResetGitOps()
	defer ResetGitOps()
	q := newFakeQuerier()
	bare, work := makeBareRepo(t)
	if err := writeCommit(t, work, "clusters/prod-east.yaml", clusterRegistrationYAML("prod-east", nil), "add"); err != nil {
		t.Fatalf("commit1: %v", err)
	}
	if err := pushBranchAsMain(t, work); err != nil {
		t.Fatalf("push1: %v", err)
	}
	src := setupSource(t, q, bare, "decommission", "interval")
	enq := &fakeEnqueuer{}
	ConfigureGitOps(GitOpsDeps{Queries: q, Enqueuer: enq, CloneRoot: t.TempDir(), Now: time.Now})
	if err := SyncSource(context.Background(), src.ID); err != nil {
		t.Fatalf("sync1: %v", err)
	}
	if err := removeAndCommit(t, work, "clusters/prod-east.yaml"); err != nil {
		t.Fatalf("rm: %v", err)
	}
	if err := pushBranchAsMain(t, work); err != nil {
		t.Fatalf("push2: %v", err)
	}
	if err := SyncSource(context.Background(), src.ID); err != nil {
		t.Fatalf("sync2: %v", err)
	}
	if len(enq.tasks) != 1 || enq.tasks[0].Type() != "cluster:decommission" {
		t.Fatalf("expected immediate decommission enqueue; got %d tasks", len(enq.tasks))
	}
}

func TestGitOpsDecommissionWritesTaskOutboxBeforeDirectEnqueue(t *testing.T) {
	ResetGitOps()
	defer ResetGitOps()
	q := newFakeQuerier()
	clusterID := uuid.New()
	outbox := &fakeTaskOutboxWriter{}
	enq := &fakeEnqueuer{}
	ConfigureGitOps(GitOpsDeps{
		Queries:    q,
		Enqueuer:   enq,
		TaskOutbox: outbox,
		Now:        time.Now,
	})

	if err := enqueueDecommission(context.Background(), clusterID, "prod-east"); err != nil {
		t.Fatalf("enqueueDecommission: %v", err)
	}
	if len(q.createdDecoms) != 1 {
		t.Fatalf("created decommissions = %d, want 1", len(q.createdDecoms))
	}
	if len(enq.tasks) != 0 {
		t.Fatalf("direct enqueues = %d, want 0 when outbox succeeds", len(enq.tasks))
	}
	arg := outbox.arg
	if arg.TaskType != ClusterDecommissionType {
		t.Fatalf("outbox task type = %q, want %q", arg.TaskType, ClusterDecommissionType)
	}
	wantDedupe := "cluster_decommission:" + q.createdDecoms[0].ID.String()
	if !arg.DedupeKey.Valid || arg.DedupeKey.String != wantDedupe {
		t.Fatalf("dedupe key = %+v, want %s", arg.DedupeKey, wantDedupe)
	}
	if arg.QueueName != ClusterTemplateApplyQueueName || arg.MaxRetry != 3 || arg.MaxDeliveryAttempts != 20 {
		t.Fatalf("outbox options queue/max_retry/max_delivery = %s/%d/%d", arg.QueueName, arg.MaxRetry, arg.MaxDeliveryAttempts)
	}
	var payload ClusterDecommissionPayload
	if err := json.Unmarshal(arg.Payload, &payload); err != nil {
		t.Fatalf("payload JSON: %v", err)
	}
	if payload.DecommissionID != q.createdDecoms[0].ID.String() {
		t.Fatalf("payload decommission_id = %q, want %s", payload.DecommissionID, q.createdDecoms[0].ID)
	}
}

func TestSync_RestoreFromTombstone(t *testing.T) {
	ResetGitOps()
	defer ResetGitOps()
	q := newFakeQuerier()
	bare, work := makeBareRepo(t)
	if err := writeCommit(t, work, "clusters/prod-east.yaml", clusterRegistrationYAML("prod-east", nil), "add"); err != nil {
		t.Fatalf("commit1: %v", err)
	}
	if err := pushBranchAsMain(t, work); err != nil {
		t.Fatalf("push1: %v", err)
	}
	src := setupSource(t, q, bare, "tombstone", "interval")
	ConfigureGitOps(GitOpsDeps{Queries: q, CloneRoot: t.TempDir(), Now: time.Now})
	if err := SyncSource(context.Background(), src.ID); err != nil {
		t.Fatalf("sync1: %v", err)
	}
	if err := removeAndCommit(t, work, "clusters/prod-east.yaml"); err != nil {
		t.Fatalf("rm: %v", err)
	}
	if err := pushBranchAsMain(t, work); err != nil {
		t.Fatalf("push2: %v", err)
	}
	if err := SyncSource(context.Background(), src.ID); err != nil {
		t.Fatalf("sync2: %v", err)
	}
	id := q.byName["prod-east"]
	if q.links[id].Status != "tombstoned" {
		t.Fatalf("setup failed: status = %q", q.links[id].Status)
	}
	// Restore the file.
	if err := writeCommit(t, work, "clusters/prod-east.yaml", clusterRegistrationYAML("prod-east", map[string]string{"restored": "true"}), "restore"); err != nil {
		t.Fatalf("restore commit: %v", err)
	}
	if err := pushBranchAsMain(t, work); err != nil {
		t.Fatalf("push3: %v", err)
	}
	if err := SyncSource(context.Background(), src.ID); err != nil {
		t.Fatalf("sync3: %v", err)
	}
	if q.links[id].Status != "active" {
		t.Fatalf("link not restored to active; status = %q", q.links[id].Status)
	}
	if !containsAction(q.auditRows, "gitops.cluster.restored") {
		t.Fatalf("missing gitops.cluster.restored audit")
	}
}

func TestSync_ManualMode_DoesNotRunOnSchedule(t *testing.T) {
	ResetGitOps()
	defer ResetGitOps()
	q := newFakeQuerier()
	bare, work := makeBareRepo(t)
	if err := writeCommit(t, work, "clusters/prod-east.yaml", clusterRegistrationYAML("prod-east", nil), "add"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := pushBranchAsMain(t, work); err != nil {
		t.Fatalf("push: %v", err)
	}
	setupSource(t, q, bare, "log", "manual")
	ConfigureGitOps(GitOpsDeps{Queries: q, CloneRoot: t.TempDir(), Now: time.Now})
	if err := HandleGitOpsSync(context.Background(), nil); err != nil {
		t.Fatalf("HandleGitOpsSync: %v", err)
	}
	if len(q.clusters) != 0 {
		t.Fatalf("manual-mode source must not run on scheduler tick; got %d clusters", len(q.clusters))
	}
}

func TestPreview_DryRunNoWrites(t *testing.T) {
	ResetGitOps()
	defer ResetGitOps()
	q := newFakeQuerier()
	bare, work := makeBareRepo(t)
	if err := writeCommit(t, work, "clusters/prod-east.yaml", clusterRegistrationYAML("prod-east", map[string]string{"tier": "prod"}), "add"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := pushBranchAsMain(t, work); err != nil {
		t.Fatalf("push: %v", err)
	}
	src := setupSource(t, q, bare, "log", "interval")
	ConfigureGitOps(GitOpsDeps{Queries: q, CloneRoot: t.TempDir(), Now: time.Now})
	res, err := PreviewSource(context.Background(), src.ID)
	if err != nil {
		t.Fatalf("PreviewSource: %v", err)
	}
	if len(res.Applies) != 1 || res.Applies[0].ClusterName != "prod-east" {
		t.Fatalf("preview result: %+v", res)
	}
	if !res.Applies[0].Created {
		t.Fatalf("preview should report Created=true for new cluster")
	}
	if len(q.clusters) != 0 {
		t.Fatalf("dry-run wrote clusters! got %d", len(q.clusters))
	}
	if len(q.links) != 0 {
		t.Fatalf("dry-run wrote links! got %d", len(q.links))
	}
	if len(q.auditRows) != 0 {
		t.Fatalf("dry-run wrote audit rows! got %d", len(q.auditRows))
	}
}

func TestApply_TemplateBindRejectsMissingTemplate(t *testing.T) {
	q := newFakeQuerier()
	doc := gitops.ClusterRegistration{}
	doc.APIVersion = gitops.APIVersion
	doc.Kind = gitops.Kind
	doc.Metadata.Name = "with-template"
	doc.Spec.Template = "does-not-exist"
	doc.RepoPath = "clusters/with-template.yaml"
	_, err := gitops.Apply(context.Background(), q, gitops.ApplyInput{
		Doc:        doc,
		SourceID:   uuid.New(),
		ContentSHA: "abc",
	})
	if err == nil {
		t.Fatalf("expected error for missing template")
	}
	if !errors.Is(err, err) || !contains(err.Error(), "does not exist") {
		t.Fatalf("expected 'does not exist' in error; got %v", err)
	}
}

// helper assertions
func containsAction(rows []sqlc.CreateAuditLogV1Params, action string) bool {
	for _, r := range rows {
		if r.Action == action {
			return true
		}
	}
	return false
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
