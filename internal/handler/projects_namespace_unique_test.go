package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// TestAddNamespace_CrossProjectUniqueViolationIs409 verifies the race-safe
// backstop for cross-project namespace uniqueness. When the pre-tx
// cross-project check passes (a concurrent claim to a different project has
// not yet committed to this connection's snapshot) but the DB-level unique
// index on project_namespaces(cluster_id, namespace) rejects the sidecar
// insert with unique_violation (23505), the handler must translate that into
// a clean 409 and roll the transaction back — not surface a generic 500 or
// commit a duplicate assignment.
//
// Before the fix: UpsertProjectNamespace's error was returned bare, mapping
// to 500, and (absent the index) the duplicate would have committed. After:
// 409, JSONB + sidecar untouched.
func TestAddNamespace_CrossProjectUniqueViolationIs409(t *testing.T) {
	q := newPolicyTestQuerier()
	callerID := uuid.New()
	clusterID := uuid.New()
	// Seed ONLY the target project with no namespaces, so the pre-tx
	// cross-project check finds no conflict and we reach the tx.
	id, p := seedTxProject(q, clusterID, []string{})

	store := newFakeProjectTxStore(p)
	// Simulate the concurrent committer having won the (cluster_id, namespace)
	// unique index: the sidecar upsert raises unique_violation.
	store.upsertErr = &pgconn.PgError{Code: "23505", ConstraintName: "uq_project_namespaces_cluster_namespace"}

	h := NewProjectHandler(q)
	h.SetRunTx(store.runTx())

	req := authedProjectRequest(t, http.MethodPost, "/api/v1/projects/"+id.String()+"/add-namespace/", callerID, map[string]any{"namespace": "shared-ns"})
	req = patchURLParam(req, "id", id.String())
	rec := httptest.NewRecorder()
	h.AddNamespace(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s, want 409", rec.Code, rec.Body.String())
	}
	// Transaction must have rolled back: neither JSONB nor sidecar changed.
	if got := decodeNamespaceList(store.project.Namespaces); len(got) != 0 {
		t.Fatalf("project namespaces = %v, want empty (rolled back)", got)
	}
	if store.nsRows["shared-ns"] {
		t.Fatalf("sidecar row for shared-ns present, want absent (rolled back)")
	}
}

// TestAddNamespace_NonUniqueTxErrorStays500 guards that the 23505 mapping is
// narrow: a different DB error inside the tx still surfaces as 500, not a
// misleading 409.
func TestAddNamespace_NonUniqueTxErrorStays500(t *testing.T) {
	q := newPolicyTestQuerier()
	callerID := uuid.New()
	clusterID := uuid.New()
	id, p := seedTxProject(q, clusterID, []string{})

	store := newFakeProjectTxStore(p)
	store.upsertErr = &pgconn.PgError{Code: "40001"} // serialization_failure, not a uniqueness conflict

	h := NewProjectHandler(q)
	h.SetRunTx(store.runTx())

	req := authedProjectRequest(t, http.MethodPost, "/api/v1/projects/"+id.String()+"/add-namespace/", callerID, map[string]any{"namespace": "shared-ns"})
	req = patchURLParam(req, "id", id.String())
	rec := httptest.NewRecorder()
	h.AddNamespace(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s, want 500", rec.Code, rec.Body.String())
	}
}
