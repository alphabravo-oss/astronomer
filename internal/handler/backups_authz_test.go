package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

// idorBackupQuerier serves fixed backup rows so the ResourceBackups gates can be
// exercised without a DB. It embeds fakeBackupQuerier (full BackupQuerier impl)
// and overrides just the two reads the authz tests touch.
type idorBackupQuerier struct {
	*fakeBackupQuerier
	backups map[uuid.UUID]sqlc.Backup
	list    []sqlc.Backup
}

func (q *idorBackupQuerier) GetBackupByID(_ context.Context, id uuid.UUID) (sqlc.Backup, error) {
	b, ok := q.backups[id]
	if !ok {
		return sqlc.Backup{}, pgx.ErrNoRows
	}
	return b, nil
}

func (q *idorBackupQuerier) ListBackups(_ context.Context, _ sqlc.ListBackupsParams) ([]sqlc.Backup, error) {
	return q.list, nil
}

func (q *idorBackupQuerier) CountBackups(_ context.Context) (int64, error) {
	return int64(len(q.list)), nil
}

func pgClusterID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

func backupBindings(clusterID uuid.UUID, verbs ...rbac.Verb) []rbac.RoleBinding {
	vs := make([]string, 0, len(verbs))
	for _, v := range verbs {
		vs = append(vs, string(v))
	}
	return []rbac.RoleBinding{{
		ClusterID: clusterID.String(),
		RoleRules: []rbac.Rule{{Resource: string(rbac.ResourceBackups), Verbs: vs}},
	}}
}

// TestBackupHandler_CreateRestoreDeniesCrossCluster proves the most destructive
// backup action — a restore that overwrites live namespaces — is gated on the
// backup's own cluster. A caller with backups grants only on another cluster
// gets 403 and cannot trigger the restore.
func TestBackupHandler_CreateRestoreDeniesCrossCluster(t *testing.T) {
	clusterA := uuid.New()
	clusterB := uuid.New()
	backupID := uuid.New()

	q := &idorBackupQuerier{
		fakeBackupQuerier: &fakeBackupQuerier{},
		backups: map[uuid.UUID]sqlc.Backup{
			backupID: {ID: backupID, Name: "prod-nightly", ClusterID: pgClusterID(clusterB), VeleroBackupName: "prod-nightly"},
		},
	}
	h := NewBackupHandler(q)
	// Caller can create backups on cluster A, but not cluster B.
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: backupBindings(clusterA, rbac.VerbCreate)})

	rec := httptest.NewRecorder()
	h.CreateRestore(rec, authedCatalogReq(http.MethodPost, "/api/v1/backups/"+backupID.String()+"/restore/", map[string]string{"id": backupID.String()}))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-cluster restore: want 403, got %d body=%s", rec.Code, rec.Body.String())
	}
	if len(q.restoreCreates) != 0 || len(q.idemRestoreArgs) != 0 {
		t.Fatalf("denied restore must not create a restore operation")
	}

	// A caller holding backups:create on cluster B is allowed through the gate.
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: backupBindings(clusterB, rbac.VerbCreate)})
	rec = httptest.NewRecorder()
	h.CreateRestore(rec, authedCatalogReq(http.MethodPost, "/api/v1/backups/"+backupID.String()+"/restore/", map[string]string{"id": backupID.String()}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("authorized restore: want 201, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestBackupHandler_ListBackupsScopesByCluster proves the fleet-wide backup
// listing returns only rows on clusters the caller may read.
func TestBackupHandler_ListBackupsScopesByCluster(t *testing.T) {
	clusterA := uuid.New()
	clusterB := uuid.New()
	q := &idorBackupQuerier{
		fakeBackupQuerier: &fakeBackupQuerier{},
		list: []sqlc.Backup{
			{ID: uuid.New(), Name: "on-a", ClusterID: pgClusterID(clusterA), Status: "completed"},
			{ID: uuid.New(), Name: "on-b", ClusterID: pgClusterID(clusterB), Status: "completed"},
		},
	}
	h := NewBackupHandler(q)
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: backupBindings(clusterA, rbac.VerbRead)})

	rec := httptest.NewRecorder()
	h.ListBackups(rec, authedCatalogReq(http.MethodGet, "/api/v1/backups/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list backups: want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if bodyContains(rec, "on-b") {
		t.Fatalf("cluster-A caller must not see cluster-B backup: %s", rec.Body.String())
	}
	var env struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode list body: %v raw=%s", err, rec.Body.String())
	}
	if len(env.Data) != 1 {
		t.Fatalf("cluster-scoped caller should see 1 backup, got %d", len(env.Data))
	}
}
