package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// A project's namespaces expand into synthetic namespace-scoped CLUSTER
// bindings carrying the project role's full rule set (expandProjectBindings).
// The tests here pin the two guards that keep "can update this project" from
// becoming "can pick which namespace my own grants apply to":
//
//  1. assigning a namespace requires clusters:update on the project's cluster,
//     not projects:update on the project;
//  2. a hard denylist of reserved namespaces, enforced even for a caller who
//     does hold cluster authority.
//
// Without (1), the shipped project-owner template (projects:[read,list,update]
// plus pods:[read,list,watch,logs,exec,proxy]) is a one-request path from
// "member of a project" to exec in kube-system, i.e. cluster-admin.

func TestAddNamespace_ProjectOwnerCannotClaimNamespace(t *testing.T) {
	q := newPolicyTestQuerier()
	callerID := uuid.New()
	clusterID := uuid.New()
	id, p := seedTxProject(q, clusterID, []string{})

	store := newFakeProjectTxStore(p)
	h := NewProjectHandler(q)
	h.SetRunTx(store.runTx())
	// The caller is project-owner on this very project and nothing else — the
	// exact principal the route's projects:update gate admits.
	grantProjectOwnerOnly(h, id)

	for _, ns := range []string{"kube-system", "someone-elses-ns"} {
		t.Run(ns, func(t *testing.T) {
			req := authedProjectRequest(t, http.MethodPost, "/api/v1/projects/"+id.String()+"/add-namespace/", callerID, map[string]any{"namespace": ns})
			req = patchURLParam(req, "id", id.String())
			rec := httptest.NewRecorder()
			h.AddNamespace(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("status=%d body=%s, want 403", rec.Code, rec.Body.String())
			}
			if store.nsRows[ns] {
				t.Fatalf("sidecar row for %s written despite 403", ns)
			}
			if got := decodeNamespaceList(store.project.Namespaces); len(got) != 0 {
				t.Fatalf("project namespaces = %v, want empty", got)
			}
		})
	}
}

// Even a cluster-authorised caller may not pull a control-plane namespace under
// a project: the denylist is deliberately independent of RBAC so a mis-granted
// verb cannot reach the escalation.
func TestAddNamespace_ReservedNamespacesRefusedEvenForClusterAdmin(t *testing.T) {
	q := newPolicyTestQuerier()
	callerID := uuid.New()
	clusterID := uuid.New()
	id, p := seedTxProject(q, clusterID, []string{})

	store := newFakeProjectTxStore(p)
	h := NewProjectHandler(q)
	h.SetRunTx(store.runTx())
	grantClusterNamespaceAssignment(h, clusterID)

	for _, ns := range []string{"kube-system", "kube-public", "kube-node-lease", "default", "astronomer-system", "KUBE-SYSTEM", " kube-system "} {
		t.Run(ns, func(t *testing.T) {
			req := authedProjectRequest(t, http.MethodPost, "/api/v1/projects/"+id.String()+"/add-namespace/", callerID, map[string]any{"namespace": ns})
			req = patchURLParam(req, "id", id.String())
			rec := httptest.NewRecorder()
			h.AddNamespace(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status=%d body=%s, want 403", rec.Code, rec.Body.String())
			}
		})
	}

	// Control: the same caller CAN claim an ordinary namespace, so the 403s
	// above are the denylist and not a broken fixture.
	req := authedProjectRequest(t, http.MethodPost, "/api/v1/projects/"+id.String()+"/add-namespace/", callerID, map[string]any{"namespace": "payments"})
	req = patchURLParam(req, "id", id.String())
	rec := httptest.NewRecorder()
	h.AddNamespace(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("control add status=%d body=%s, want 200", rec.Code, rec.Body.String())
	}
}

// Update is the second door onto the same claim: it writes the namespaces JSONB
// and upserts project_namespaces for every entry. It must carry both guards.
func TestUpdate_NamespaceAdditionsNeedClusterAuthority(t *testing.T) {
	q := newPolicyTestQuerier()
	callerID := uuid.New()
	clusterID := uuid.New()
	id, _ := seedTxProject(q, clusterID, []string{"team-a"})

	h := NewProjectHandler(q)
	grantProjectOwnerOnly(h, id)

	req := authedProjectRequest(t, http.MethodPut, "/api/v1/projects/"+id.String()+"/", callerID, map[string]any{
		"display_name": "Team A",
		"namespaces":   []string{"team-a", "kube-system"},
	})
	req = patchURLParam(req, "id", id.String())
	rec := httptest.NewRecorder()
	h.Update(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s, want 403", rec.Code, rec.Body.String())
	}
	if got := decodeNamespaceList(q.projects[id].Namespaces); len(got) != 1 || got[0] != "team-a" {
		t.Fatalf("namespaces = %v, want [team-a] (unchanged)", got)
	}
}

// TestUpdate_ShrinkingListPrunesSidecarAndFlushesCache is the revocation hole.
// Before this, Update replaced the JSONB wholesale and only ever UPSERTED
// project_namespaces, so a dropped namespace kept its sidecar row — and that row
// is what expandProjectBindings reads. Nothing converged it: the reconciler
// re-applies rows, it never prunes them.
func TestUpdate_ShrinkingListPrunesSidecarAndFlushesCache(t *testing.T) {
	q := newPolicyTestQuerier()
	callerID := uuid.New()
	clusterID := uuid.New()
	id, _ := seedTxProject(q, clusterID, []string{"team-a", "team-b"})
	q.nsRows = []sqlc.ProjectNamespace{
		{ProjectID: id, ClusterID: clusterID, Namespace: "team-a"},
		{ProjectID: id, ClusterID: clusterID, Namespace: "team-b"},
	}

	inv := &fakeRBACInvalidator{}
	h := NewProjectHandler(q)
	h.SetRBACInvalidator(inv)
	grantClusterNamespaceAssignment(h, clusterID)

	req := authedProjectRequest(t, http.MethodPut, "/api/v1/projects/"+id.String()+"/", callerID, map[string]any{
		"display_name": "Team A",
		"namespaces":   []string{"team-a"},
	})
	req = patchURLParam(req, "id", id.String())
	rec := httptest.NewRecorder()
	h.Update(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if q.hasProjectNamespaceRow("team-b") {
		t.Fatal("project_namespaces row for team-b survived the shrink: it keeps minting a synthetic namespace-scoped cluster binding forever")
	}
	if !q.hasProjectNamespaceRow("team-a") {
		t.Fatal("project_namespaces row for team-a was pruned, but it is still in the list")
	}
	if inv.invalidateAll != 1 {
		t.Fatalf("InvalidateAll called %d times, want 1 — a revoked namespace keeps authorizing until the cache TTL otherwise", inv.invalidateAll)
	}
}

// A body that omits `namespaces` (every policy-only PATCH) must not be read as
// "clear them all" — that used to wipe the JSONB while leaving the sidecar rows
// behind, which is the revocation hole in its worst form.
func TestUpdate_OmittedNamespacesPreservesMembership(t *testing.T) {
	q := newPolicyTestQuerier()
	callerID := uuid.New()
	clusterID := uuid.New()
	id, _ := seedTxProject(q, clusterID, []string{"team-a", "team-b"})
	q.nsRows = []sqlc.ProjectNamespace{
		{ProjectID: id, ClusterID: clusterID, Namespace: "team-a"},
		{ProjectID: id, ClusterID: clusterID, Namespace: "team-b"},
	}

	inv := &fakeRBACInvalidator{}
	h := NewProjectHandler(q)
	h.SetRBACInvalidator(inv)
	// Deliberately NO cluster grant: an untouched namespace set is not a
	// namespace-assignment act, so this must still succeed.
	grantProjectOwnerOnly(h, id)

	req := authedProjectRequest(t, http.MethodPatch, "/api/v1/projects/"+id.String()+"/", callerID, map[string]any{
		"display_name": "Renamed",
	})
	req = patchURLParam(req, "id", id.String())
	rec := httptest.NewRecorder()
	h.Update(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want 200", rec.Code, rec.Body.String())
	}
	got := decodeNamespaceList(q.projects[id].Namespaces)
	if len(got) != 2 {
		t.Fatalf("namespaces = %v, want [team-a team-b] preserved", got)
	}
	if !q.hasProjectNamespaceRow("team-a") || !q.hasProjectNamespaceRow("team-b") {
		t.Fatalf("sidecar rows = %v, want both preserved", q.nsRows)
	}
	if inv.invalidateAll != 0 {
		t.Fatalf("InvalidateAll called %d times for a no-op membership change, want 0", inv.invalidateAll)
	}
}

// Delete cascades project_namespaces and project_role_bindings in the DB, but
// the RBAC binding cache is in-process — without a flush every member keeps the
// deleted project's namespace grants for the cache TTL.
func TestDelete_FlushesRBACCache(t *testing.T) {
	q := newPolicyTestQuerier()
	callerID := uuid.New()
	clusterID := uuid.New()
	id, _ := seedTxProject(q, clusterID, []string{"team-a"})

	inv := &fakeRBACInvalidator{}
	h := NewProjectHandler(q)
	h.SetRBACInvalidator(inv)

	req := authedProjectRequest(t, http.MethodDelete, "/api/v1/projects/"+id.String()+"/", callerID, nil)
	req = patchURLParam(req, "id", id.String())
	rec := httptest.NewRecorder()
	h.Delete(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s, want 204", rec.Code, rec.Body.String())
	}
	if inv.invalidateAll != 1 {
		t.Fatalf("InvalidateAll called %d times, want 1", inv.invalidateAll)
	}
}

// TestRBACInvalidatorWired_RejectsTypedNil pins the boot guard the revocation
// fix rests on. middleware.NewSQLCRBACQuerierWithCache returns a nil
// *SQLCRBACQuerier when queries==nil; boxed into the interface it is non-nil and
// satisfies RBACCacheInvalidator, but InvalidateAll() no-ops on the nil
// receiver. The guard must reject it.
func TestRBACInvalidatorWired_RejectsTypedNil(t *testing.T) {
	h := NewProjectHandler(newPolicyTestQuerier())
	if h.RBACInvalidatorWired() {
		t.Fatal("unset invalidator reported as wired")
	}
	h.SetRBACInvalidator(nilSQLCRBACQuerier())
	if h.RBACInvalidatorWired() {
		t.Fatal("typed-nil invalidator reported as wired; its InvalidateAll() flushes nothing")
	}
	inv := &fakeRBACInvalidator{}
	h.SetRBACInvalidator(inv)
	if !h.RBACInvalidatorWired() {
		t.Fatal("a real invalidator was rejected")
	}
	h.invalidateRBACCache()
	if inv.invalidateAll != 1 {
		t.Fatalf("InvalidateAll called %d times, want 1", inv.invalidateAll)
	}
}

// nilSQLCRBACQuerier returns the typed-nil *SQLCRBACQuerier that
// NewSQLCRBACQuerierWithCache yields for a nil queries argument, boxed into the
// interface exactly as SetRBACInvalidator would receive it.
func nilSQLCRBACQuerier() middleware.RBACQuerier {
	return middleware.NewSQLCRBACQuerierWithCache(nil, middleware.NewRBACCache())
}
