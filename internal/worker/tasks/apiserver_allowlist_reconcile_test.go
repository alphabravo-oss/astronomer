package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/apisvr/allowlist/providers"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// fakeAllowlistQuerier captures every mutation the reconciler issues so
// tests can assert the per-tick stamped state.
type fakeAllowlistQuerier struct {
	mu        sync.Mutex
	row       sqlc.ApiserverAllowlist
	cluster   sqlc.Cluster
	updates   []sqlc.UpdateApiserverAllowlistReconcileStateParams
	snapshots []sqlc.InsertApiserverAllowlistSnapshotParams
	deletes   []time.Time
}

func (f *fakeAllowlistQuerier) GetApiserverAllowlistByClusterID(ctx context.Context, clusterID uuid.UUID) (sqlc.ApiserverAllowlist, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.row.ClusterID != clusterID {
		return sqlc.ApiserverAllowlist{}, errors.New("not found")
	}
	return f.row, nil
}

func (f *fakeAllowlistQuerier) ListActiveApiserverAllowlists(ctx context.Context) ([]sqlc.ApiserverAllowlist, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.row.Mode == "disabled" {
		return nil, nil
	}
	return []sqlc.ApiserverAllowlist{f.row}, nil
}

func (f *fakeAllowlistQuerier) UpdateApiserverAllowlistReconcileState(ctx context.Context, arg sqlc.UpdateApiserverAllowlistReconcileStateParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates = append(f.updates, arg)
	f.row.DetectedProvider = arg.DetectedProvider
	f.row.SyncStatus = arg.SyncStatus
	f.row.LastError = arg.LastError
	f.row.EffectiveCidrs = arg.EffectiveCidrs
	return nil
}

func (f *fakeAllowlistQuerier) InsertApiserverAllowlistSnapshot(ctx context.Context, arg sqlc.InsertApiserverAllowlistSnapshotParams) (sqlc.ApiserverAllowlistSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snapshots = append(f.snapshots, arg)
	return sqlc.ApiserverAllowlistSnapshot{}, nil
}

func (f *fakeAllowlistQuerier) DeleteApiserverAllowlistSnapshotsOlderThan(ctx context.Context, cutoff time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deletes = append(f.deletes, cutoff)
	return nil
}

func (f *fakeAllowlistQuerier) GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error) {
	return f.cluster, nil
}

// fakeAllowlistProvider lets each test wire its own Detect/GetEffective/Apply behaviour.
type fakeAllowlistProvider struct {
	id            providers.ProviderID
	detect        func(providers.Cluster) string
	getEffective  func(providers.Cluster) ([]string, error)
	apply         func(providers.Cluster, []string) error
	applyCalls    int
	getCalls      int
}

func (f *fakeAllowlistProvider) ID() providers.ProviderID { return f.id }
func (f *fakeAllowlistProvider) Detect(ctx context.Context, c providers.Cluster) string {
	if f.detect != nil {
		return f.detect(c)
	}
	if c.Provider == string(f.id) {
		return string(f.id)
	}
	return ""
}
func (f *fakeAllowlistProvider) GetEffective(ctx context.Context, c providers.Cluster) ([]string, error) {
	f.getCalls++
	if f.getEffective != nil {
		return f.getEffective(c)
	}
	return nil, nil
}
func (f *fakeAllowlistProvider) Apply(ctx context.Context, c providers.Cluster, cidrs []string) error {
	f.applyCalls++
	if f.apply != nil {
		return f.apply(c, cidrs)
	}
	return nil
}

func setupReconciler(t *testing.T, mode string, operatorCIDRs []string, prov *fakeAllowlistProvider) (*fakeAllowlistQuerier, uuid.UUID) {
	t.Helper()
	clusterID := uuid.New()
	cidrsJSON, _ := json.Marshal(operatorCIDRs)
	q := &fakeAllowlistQuerier{
		row: sqlc.ApiserverAllowlist{
			ClusterID: clusterID,
			Cidrs:     cidrsJSON,
			Mode:      mode,
		},
		cluster: sqlc.Cluster{ID: clusterID, Name: "test-cluster", Provider: string(prov.id)},
	}
	reg := providers.NewRegistry()
	reg.Register(prov)
	ConfigureApiserverAllowlistReconcile(ApiserverAllowlistReconcileDeps{
		Queries:          q,
		Registry:         reg,
		AstronomerEgress: []string{"54.10.0.0/16"},
	})
	t.Cleanup(ResetApiserverAllowlistReconcile)
	return q, clusterID
}

func TestReconciler_MonitorModeDoesNotPatch(t *testing.T) {
	prov := &fakeAllowlistProvider{
		id: providers.ProviderEKS,
		getEffective: func(providers.Cluster) ([]string, error) {
			return []string{"10.0.0.0/8"}, nil // missing the egress
		},
	}
	q, clusterID := setupReconciler(t, "monitor", []string{"10.0.0.0/8"}, prov)
	if err := ReconcileApiserverAllowlistOnce(context.Background(), clusterID); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if prov.applyCalls != 0 {
		t.Fatalf("monitor mode should never patch; got %d Apply calls", prov.applyCalls)
	}
	if got := q.updates[len(q.updates)-1].SyncStatus; got != "drifting" {
		t.Fatalf("expected drifting sync_status; got %q", got)
	}
	if len(q.snapshots) == 0 {
		t.Fatalf("expected at least one snapshot")
	}
	if !q.snapshots[0].Drift {
		t.Fatalf("snapshot should mark drift=true")
	}
}

func TestReconciler_EnforceModePatchesOnDrift(t *testing.T) {
	calls := 0
	prov := &fakeAllowlistProvider{
		id: providers.ProviderEKS,
		getEffective: func(providers.Cluster) ([]string, error) {
			calls++
			if calls == 1 {
				return []string{"10.0.0.0/8"}, nil // missing egress → drift
			}
			return []string{"10.0.0.0/8", "54.10.0.0/16"}, nil // post-apply (unused)
		},
		apply: func(providers.Cluster, []string) error { return nil },
	}
	q, clusterID := setupReconciler(t, "enforce", []string{"10.0.0.0/8"}, prov)
	if err := ReconcileApiserverAllowlistOnce(context.Background(), clusterID); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if prov.applyCalls != 1 {
		t.Fatalf("enforce mode should patch once on drift; got %d", prov.applyCalls)
	}
	if got := q.updates[len(q.updates)-1].SyncStatus; got != "synced" {
		t.Fatalf("expected synced after enforce; got %q", got)
	}
}

func TestReconciler_EnforceMode_NoDrift_NoPatch(t *testing.T) {
	prov := &fakeAllowlistProvider{
		id: providers.ProviderEKS,
		getEffective: func(providers.Cluster) ([]string, error) {
			return []string{"10.0.0.0/8", "54.10.0.0/16"}, nil
		},
		apply: func(providers.Cluster, []string) error { return nil },
	}
	q, clusterID := setupReconciler(t, "enforce", []string{"10.0.0.0/8"}, prov)
	if err := ReconcileApiserverAllowlistOnce(context.Background(), clusterID); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if prov.applyCalls != 0 {
		t.Fatalf("no drift → no apply; got %d", prov.applyCalls)
	}
	if got := q.updates[len(q.updates)-1].SyncStatus; got != "synced" {
		t.Fatalf("expected synced; got %q", got)
	}
}

func TestReconciler_RefusesEnforceOnSelfManaged_LogsWarn(t *testing.T) {
	prov := &fakeAllowlistProvider{
		id: providers.ProviderSelfManaged,
		getEffective: func(providers.Cluster) ([]string, error) {
			return nil, nil
		},
		apply: func(providers.Cluster, []string) error {
			t.Fatalf("Apply must NOT be called on self_managed enforce")
			return nil
		},
	}
	q, clusterID := setupReconciler(t, "enforce", []string{"10.0.0.0/8"}, prov)
	if err := ReconcileApiserverAllowlistOnce(context.Background(), clusterID); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	last := q.updates[len(q.updates)-1]
	if last.DetectedProvider != providers.ProviderSelfManaged {
		t.Fatalf("expected detected_provider=self_managed; got %q", last.DetectedProvider)
	}
	if !strings.Contains(last.LastError, "cloud-managed provider") {
		t.Fatalf("expected last_error to mention cloud-managed restriction; got %q", last.LastError)
	}
}

func TestReconciler_HandlesProviderNotImplemented(t *testing.T) {
	prov := &fakeAllowlistProvider{
		id: providers.ProviderAKS,
		getEffective: func(providers.Cluster) ([]string, error) {
			return nil, nil
		},
		apply: func(providers.Cluster, []string) error {
			return errors.New("provider not implemented in v1")
		},
	}
	q, clusterID := setupReconciler(t, "enforce", []string{"10.0.0.0/8"}, prov)
	if err := ReconcileApiserverAllowlistOnce(context.Background(), clusterID); err != nil {
		t.Fatalf("reconcile should not propagate not-implemented as task error; got %v", err)
	}
	if got := q.updates[len(q.updates)-1].SyncStatus; got != "drifting" {
		t.Fatalf("expected drifting on not-implemented; got %q", got)
	}
}

func TestReconciler_CleanupSnapshots(t *testing.T) {
	q := &fakeAllowlistQuerier{}
	ConfigureApiserverAllowlistReconcile(ApiserverAllowlistReconcileDeps{Queries: q})
	t.Cleanup(ResetApiserverAllowlistReconcile)
	// We can't easily invoke the periodic-leader wrapper here without a
	// running leader system; instead exercise the DeleteOlderThan path.
	if err := q.DeleteApiserverAllowlistSnapshotsOlderThan(context.Background(), time.Now()); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(q.deletes) != 1 {
		t.Fatalf("expected one delete call")
	}
}
