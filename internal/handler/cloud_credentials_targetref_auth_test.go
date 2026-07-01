package handler

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// nsAuthFakeQuerier extends the base cloud-credential fake with the
// project_namespaces surface so the handler's target_ref ownership
// authorization is exercised (the production *sqlc.Queries implements
// ListProjectNamespaces; the base fake deliberately does not, so this
// wrapper drives the enforced path).
type nsAuthFakeQuerier struct {
	*fakeCloudCredQuerier
	namespaces []sqlc.ProjectNamespace
}

func (f *nsAuthFakeQuerier) ListProjectNamespaces(_ context.Context, projectID uuid.UUID) ([]sqlc.ProjectNamespace, error) {
	out := []sqlc.ProjectNamespace{}
	for _, ns := range f.namespaces {
		if ns.ProjectID == projectID {
			out = append(out, ns)
		}
	}
	return out, nil
}

// newNSAuthHandler wires a handler whose querier authorizes target_refs
// against project_namespaces. Returns the handler, the querier, the
// project id, and a cluster id that EXISTS but whose namespaces the project
// does NOT own (the cross-tenant victim cluster).
func newNSAuthHandler(t *testing.T) (*CloudCredentialHandler, *nsAuthFakeQuerier, uuid.UUID, uuid.UUID) {
	t.Helper()
	base := newFakeCloudCredQuerier()
	q := &nsAuthFakeQuerier{fakeCloudCredQuerier: base}
	pid := uuid.New()
	victimCluster := uuid.New()
	base.projectOK[pid] = true
	// The victim cluster exists (imported by some other project) so the
	// existence check passes — only the ownership check should stop it.
	base.clusterOK[victimCluster] = true
	enc, err := auth.NewEncryptor(testKey(t))
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}
	h := NewCloudCredentialHandler(q)
	h.SetEncryptor(enc)
	h.SetEnqueuer(&fakeCloudCredEnqueuer{})
	return h, q, pid, victimCluster
}

// TestCloudCreds_TargetRefRejectsUnownedNamespace is the security
// regression: a caller with Project-Update on project A must not be able to
// target a Secret in a (cluster, namespace) the project does not own. Before
// the fix the credential was created (201) and a materialize/delete task was
// enqueued against the victim cluster; after the fix the write is rejected.
func TestCloudCreds_TargetRefRejectsUnownedNamespace(t *testing.T) {
	h, _, pid, victimCluster := newNSAuthHandler(t)
	// Project owns NOTHING in the victim cluster/namespace.
	body := fmt.Sprintf(`{
		"name": "evil-cred",
		"provider": "aws",
		"data": {"access_key_id": "AKIAFAKE", "secret_access_key": "shhh"},
		"target_refs": [
			{"cluster_id": "%s", "namespace": "kube-system", "secret_name": "victim"}
		]
	}`, victimCluster)
	resp := callRoute(t, http.HandlerFunc(h.Create), http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/cloud-credentials/", pid), body)
	if resp.StatusCode != http.StatusBadRequest {
		dumpResponse(t, resp)
		t.Fatalf("expected 400 rejecting unowned target namespace, got %d", resp.StatusCode)
	}
	bodyBytes, _ := readAll(resp)
	if !strings.Contains(string(bodyBytes), "not owned by this project") {
		t.Fatalf("expected ownership error, got %s", bodyBytes)
	}
}

// TestCloudCreds_TargetRefAllowsOwnedNamespace proves a legitimate caller
// still works: when the project owns the (cluster, namespace) the create
// succeeds and the target is materialized.
func TestCloudCreds_TargetRefAllowsOwnedNamespace(t *testing.T) {
	h, q, pid, cluster := newNSAuthHandler(t)
	// Grant the project the (cluster, namespace) it targets.
	q.namespaces = append(q.namespaces, sqlc.ProjectNamespace{
		ProjectID: pid,
		ClusterID: cluster,
		Namespace: "apps",
	})
	body := fmt.Sprintf(`{
		"name": "good-cred",
		"provider": "aws",
		"data": {"access_key_id": "AKIAFAKE", "secret_access_key": "shhh"},
		"target_refs": [
			{"cluster_id": "%s", "namespace": "apps"}
		]
	}`, cluster)
	resp := callRoute(t, http.HandlerFunc(h.Create), http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/cloud-credentials/", pid), body)
	if resp.StatusCode != http.StatusCreated {
		dumpResponse(t, resp)
		t.Fatalf("expected 201 for owned target namespace, got %d", resp.StatusCode)
	}
	created := decodeCloudCredResponse(t, resp)
	if len(created.TargetRefs) != 1 || created.TargetRefs[0].Namespace != "apps" {
		t.Fatalf("unexpected target_refs echo: %+v", created.TargetRefs)
	}
}

// TestCloudCreds_UpdateTargetRefRejectsUnownedNamespace covers the PUT path:
// a low-privileged caller must not be able to add a cross-tenant target via
// Update either (the delete-on-shrink path is what enqueues Secret DELETEs).
func TestCloudCreds_UpdateTargetRefRejectsUnownedNamespace(t *testing.T) {
	h, q, pid, victimCluster := newNSAuthHandler(t)
	// Create a clean credential with no target_refs first.
	createBody := `{
		"name": "start-clean",
		"provider": "aws",
		"data": {"access_key_id": "AKIAFAKE", "secret_access_key": "shhh"}
	}`
	resp := callRoute(t, http.HandlerFunc(h.Create), http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/cloud-credentials/", pid), createBody)
	if resp.StatusCode != http.StatusCreated {
		dumpResponse(t, resp)
		t.Fatalf("setup create failed: %d", resp.StatusCode)
	}
	created := decodeCloudCredResponse(t, resp)
	_ = q // fake state is shared through the embedded base querier

	updateBody := fmt.Sprintf(`{
		"data": {"access_key_id": "<set>", "secret_access_key": "<set>"},
		"target_refs": [
			{"cluster_id": "%s", "namespace": "kube-system", "secret_name": "victim"}
		]
	}`, victimCluster)
	resp = callRoute(t, http.HandlerFunc(h.Update), http.MethodPut, fmt.Sprintf("/api/v1/projects/%s/cloud-credentials/%s/", pid, created.ID), updateBody)
	if resp.StatusCode != http.StatusBadRequest {
		dumpResponse(t, resp)
		t.Fatalf("expected 400 rejecting unowned target on update, got %d", resp.StatusCode)
	}
}
