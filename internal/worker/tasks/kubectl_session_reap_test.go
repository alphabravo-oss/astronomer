package tasks

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/kubectl"
)

// fakeReapRequester is a minimal kubectl.K8sRequester for reaper tests.
type fakeReapRequester struct {
	mu       sync.Mutex
	calls    []string // METHOD PATH
	listBody []byte
}

func (f *fakeReapRequester) Do(_ context.Context, _ string, method, path string, _ []byte, _ map[string]string) (*kubectl.K8sResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, method+" "+path)
	if method == "GET" && strings.Contains(path, "labelSelector=astronomer.io/component=kubectl-shell") {
		if f.listBody != nil {
			return &kubectl.K8sResponse{StatusCode: 200, Body: f.listBody}, nil
		}
		return &kubectl.K8sResponse{StatusCode: 200, Body: []byte(`{"items":[]}`)}, nil
	}
	return &kubectl.K8sResponse{StatusCode: 200, Body: []byte("{}")}, nil
}

// fakeReapQuerier is a tiny in-memory queries fake just for the reaper.
type fakeReapQuerier struct {
	mu       sync.Mutex
	sessions map[uuid.UUID]*sqlc.KubectlSession
}

func newFakeReapQuerier() *fakeReapQuerier {
	return &fakeReapQuerier{sessions: map[uuid.UUID]*sqlc.KubectlSession{}}
}

func (f *fakeReapQuerier) CreateKubectlSession(_ context.Context, _ sqlc.CreateKubectlSessionParams) (sqlc.KubectlSession, error) {
	return sqlc.KubectlSession{}, nil
}
func (f *fakeReapQuerier) GetKubectlSessionByID(_ context.Context, id uuid.UUID) (sqlc.KubectlSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.sessions[id]
	if !ok {
		return sqlc.KubectlSession{}, pgx.ErrNoRows
	}
	return *r, nil
}
func (f *fakeReapQuerier) ListActiveKubectlSessionsByCluster(_ context.Context, _ uuid.UUID) ([]sqlc.KubectlSession, error) {
	return nil, nil
}
func (f *fakeReapQuerier) ListAllActiveKubectlSessions(_ context.Context) ([]sqlc.KubectlSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []sqlc.KubectlSession
	for _, r := range f.sessions {
		if r.Status == "starting" || r.Status == "active" {
			out = append(out, *r)
		}
	}
	return out, nil
}
func (f *fakeReapQuerier) ListExpiredKubectlSessions(_ context.Context) ([]sqlc.KubectlSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	var out []sqlc.KubectlSession
	for _, r := range f.sessions {
		if r.Status != "starting" && r.Status != "active" {
			continue
		}
		if !r.ExpiresAt.After(now) || r.LastInputAt.Add(30*time.Minute).Before(now) {
			out = append(out, *r)
		}
	}
	return out, nil
}
func (f *fakeReapQuerier) SetKubectlSessionStatus(_ context.Context, arg sqlc.SetKubectlSessionStatusParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.sessions[arg.ID]; ok {
		r.Status = arg.Status
		if arg.LastError.Valid {
			r.LastError = arg.LastError.String
		}
		if arg.Status == "closed" || arg.Status == "expired" || arg.Status == "failed" {
			r.ClosedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
		}
	}
	return nil
}
func (f *fakeReapQuerier) TouchKubectlSessionInput(_ context.Context, _ uuid.UUID) error {
	return nil
}
func (f *fakeReapQuerier) InsertKubectlSessionCommand(_ context.Context, _ sqlc.InsertKubectlSessionCommandParams) error {
	return nil
}
func (f *fakeReapQuerier) ListKubectlSessionCommands(_ context.Context, _ sqlc.ListKubectlSessionCommandsParams) ([]sqlc.KubectlSessionCommand, error) {
	return nil, nil
}
func (f *fakeReapQuerier) CountKubectlSessionCommands(_ context.Context, _ uuid.UUID) (int64, error) {
	return 0, nil
}

func TestKubectlReaper_HandleNoopWhenUnconfigured(t *testing.T) {
	ResetKubectlSessionReap()
	if err := HandleKubectlSessionReap(context.Background(), NewKubectlSessionReapTask()); err != nil {
		t.Fatalf("expected nil when unconfigured, got %v", err)
	}
}

func TestKubectlReaper_ExpiresAndDeletesPods(t *testing.T) {
	q := newFakeReapQuerier()
	r := &fakeReapRequester{}

	sid := uuid.New()
	q.sessions[sid] = &sqlc.KubectlSession{
		ID: sid, ClusterID: uuid.New(), Status: "active",
		LastInputAt:  time.Now().Add(-1 * time.Hour), // idle expired
		ExpiresAt:    time.Now().Add(1 * time.Hour),
		SaName:       "astro-shell-reap1",
		SaNamespace:  "kube-system",
		PodName:      "astro-shell-reap1",
		PodNamespace: "kube-system",
	}
	ConfigureKubectlSessionReap(KubectlSessionReapDeps{
		Deps: kubectl.Deps{Queries: q, Requester: r},
	})
	defer ResetKubectlSessionReap()

	if err := HandleKubectlSessionReap(context.Background(), NewKubectlSessionReapTask()); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	q.mu.Lock()
	got := q.sessions[sid].Status
	q.mu.Unlock()
	if got != "expired" {
		t.Fatalf("want expired, got %s", got)
	}

	r.mu.Lock()
	calls := append([]string(nil), r.calls...)
	r.mu.Unlock()
	wantSubstrs := []string{"DELETE /api/v1/namespaces/kube-system/pods/astro-shell-reap1"}
	for _, w := range wantSubstrs {
		found := false
		for _, c := range calls {
			if c == w {
				found = true
			}
		}
		if !found {
			t.Errorf("expected reaper to issue %q; got %v", w, calls)
		}
	}
}

func TestKubectlReaper_OrphanPodSweep(t *testing.T) {
	q := newFakeReapQuerier()
	r := &fakeReapRequester{}

	cid := uuid.New()
	sid := uuid.New()
	q.sessions[sid] = &sqlc.KubectlSession{
		ID: sid, ClusterID: cid, Status: "active",
		LastInputAt:  time.Now(),
		ExpiresAt:    time.Now().Add(1 * time.Hour),
		SaName:       "astro-shell-keep",
		SaNamespace:  "kube-system",
		PodName:      "astro-shell-keep",
		PodNamespace: "kube-system",
	}

	listBody, _ := json.Marshal(map[string]any{
		"items": []map[string]any{
			{"metadata": map[string]any{"name": "astro-shell-keep", "namespace": "kube-system"}},
			{"metadata": map[string]any{"name": "astro-shell-orphan", "namespace": "kube-system"}},
		},
	})
	r.listBody = listBody

	ConfigureKubectlSessionReap(KubectlSessionReapDeps{
		Deps: kubectl.Deps{Queries: q, Requester: r},
	})
	defer ResetKubectlSessionReap()

	if err := HandleKubectlSessionReap(context.Background(), NewKubectlSessionReapTask()); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	r.mu.Lock()
	calls := append([]string(nil), r.calls...)
	r.mu.Unlock()

	sawOrphan := false
	for _, c := range calls {
		if c == "DELETE /api/v1/namespaces/kube-system/pods/astro-shell-orphan" {
			sawOrphan = true
		}
		if c == "DELETE /api/v1/namespaces/kube-system/pods/astro-shell-keep" {
			t.Errorf("orphan sweep should NOT have deleted the active pod (calls=%v)", calls)
		}
	}
	if !sawOrphan {
		t.Fatalf("orphan pod was not deleted (calls=%v)", calls)
	}
}
