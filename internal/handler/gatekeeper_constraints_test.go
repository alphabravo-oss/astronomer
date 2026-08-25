package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// --- fakes ---

type fakeGatekeeperQuerier struct {
	fakeOperationIdempotencyStore
	authored map[string]sqlc.AuthoredConstraint // key: name
	upserts  int
	deletes  int
}

func newFakeGatekeeperQuerier() *fakeGatekeeperQuerier {
	return &fakeGatekeeperQuerier{authored: map[string]sqlc.AuthoredConstraint{}}
}

func (f *fakeGatekeeperQuerier) ListAuthoredConstraintsForCluster(_ context.Context, clusterID uuid.UUID) ([]sqlc.AuthoredConstraint, error) {
	out := make([]sqlc.AuthoredConstraint, 0, len(f.authored))
	for _, c := range f.authored {
		if c.ClusterID == clusterID {
			out = append(out, c)
		}
	}
	return out, nil
}

func (f *fakeGatekeeperQuerier) GetAuthoredConstraintByName(_ context.Context, arg sqlc.GetAuthoredConstraintByNameParams) (sqlc.AuthoredConstraint, error) {
	c, ok := f.authored[arg.Name]
	if !ok || c.ClusterID != arg.ClusterID {
		return sqlc.AuthoredConstraint{}, pgx.ErrNoRows
	}
	return c, nil
}

func (f *fakeGatekeeperQuerier) UpsertAuthoredConstraint(_ context.Context, arg sqlc.UpsertAuthoredConstraintParams) (sqlc.AuthoredConstraint, error) {
	f.upserts++
	generation := int64(1)
	if existing, ok := f.authored[arg.Name]; ok {
		generation = existing.Generation + 1
	}
	c := sqlc.AuthoredConstraint{
		ID:           uuid.New(),
		ClusterID:    arg.ClusterID,
		Name:         arg.Name,
		Kind:         arg.Kind,
		ApiVersion:   arg.ApiVersion,
		Yaml:         arg.Yaml,
		CreatedBy:    arg.CreatedBy,
		DesiredState: "present",
		SyncStatus:   "pending",
		Generation:   generation,
	}
	f.authored[arg.Name] = c
	return c, nil
}

func (f *fakeGatekeeperQuerier) DeleteAuthoredConstraint(_ context.Context, arg sqlc.DeleteAuthoredConstraintParams) error {
	f.deletes++
	delete(f.authored, arg.Name)
	return nil
}

func clustersVerbBindings(clusterID uuid.UUID, verb rbac.Verb) []rbac.RoleBinding {
	return []rbac.RoleBinding{{
		ClusterID: clusterID.String(),
		RoleRules: []rbac.Rule{{
			Resource: string(rbac.ResourceClusters),
			Verbs:    []string{string(verb)},
		}},
	}}
}

func authedConstraintReq(method, target, clusterID string, body any) *http.Request {
	var reader *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, target, reader)
	req.Header.Set("Content-Type", "application/json")
	if method == http.MethodPost || method == http.MethodDelete {
		req.Header.Set("Idempotency-Key", uuid.NewString())
	}
	rc := chi.NewRouteContext()
	rc.URLParams.Add("id", clusterID)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rc)
	ctx = middleware.SetAuthenticatedUserForTest(ctx, &middleware.AuthenticatedUser{ID: uuid.NewString()})
	return req.WithContext(ctx)
}

const validConstraintTemplateYAML = `apiVersion: templates.gatekeeper.sh/v1
kind: ConstraintTemplate
metadata:
  name: k8srequiredfoo
spec:
  crd:
    spec:
      names:
        kind: K8sRequiredFoo
  targets:
    - target: admission.k8s.gatekeeper.sh
      rego: |
        package k8srequiredfoo
        violation[{"msg": msg}] {
          msg := "foo is required"
        }
`

const invalidRegoConstraintTemplateYAML = `apiVersion: templates.gatekeeper.sh/v1
kind: ConstraintTemplate
metadata:
  name: k8sbadrego
spec:
  crd:
    spec:
      names:
        kind: K8sBadRego
  targets:
    - target: admission.k8s.gatekeeper.sh
      rego: |
        this is not rego and has an unbalanced brace {
`

const sampleConstraintYAML = `apiVersion: constraints.gatekeeper.sh/v1beta1
kind: K8sRequiredFoo
metadata:
  name: must-have-foo
spec:
  enforcementAction: dryrun
`

func decodeConstraintValidation(t *testing.T, body []byte) ConstraintValidationResponse {
	t.Helper()
	var env struct {
		Data ConstraintValidationResponse `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, string(body))
	}
	return env.Data
}

func TestListConstraintsReturnsAuthoredYAMLAndEnforcementAction(t *testing.T) {
	clusterID := uuid.New()
	q := newFakeGatekeeperQuerier()
	q.authored["must-have-foo"] = sqlc.AuthoredConstraint{
		ID:         uuid.New(),
		ClusterID:  clusterID,
		Name:       "must-have-foo",
		Kind:       "K8sRequiredFoo",
		ApiVersion: "constraints.gatekeeper.sh/v1beta1",
		Yaml:       sampleConstraintYAML,
	}
	h := NewGatekeeperConstraintsHandler(q, nil)
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: clustersVerbBindings(clusterID, rbac.VerbRead)})

	rec := httptest.NewRecorder()
	h.ListConstraints(rec, authedConstraintReq(http.MethodGet, "/", clusterID.String(), nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		Data struct {
			Custom []map[string]any `json:"custom"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Data.Custom) != 1 {
		t.Fatalf("want one custom constraint, got %#v", response.Data.Custom)
	}
	item := response.Data.Custom[0]
	if item["yaml"] != sampleConstraintYAML {
		t.Fatalf("authored YAML missing from response: %#v", item)
	}
	if item["enforcement_action"] != "dryrun" {
		t.Fatalf("want dryrun enforcement action, got %#v", item["enforcement_action"])
	}
}

func TestListConstraintsSkipsLiveViolationLookupForDeletionIntent(t *testing.T) {
	clusterID := uuid.New()
	q := newFakeGatekeeperQuerier()
	q.authored["must-have-foo"] = sqlc.AuthoredConstraint{
		ID:           uuid.New(),
		ClusterID:    clusterID,
		Name:         "must-have-foo",
		Kind:         "K8sRequiredFoo",
		ApiVersion:   "constraints.gatekeeper.sh/v1beta1",
		Yaml:         sampleConstraintYAML,
		DesiredState: "absent",
		SyncStatus:   "pending",
		Generation:   2,
	}
	requester := &stubK8sRequester{}
	h := NewGatekeeperConstraintsHandler(q, requester)
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: clustersVerbBindings(clusterID, rbac.VerbRead)})

	rec := httptest.NewRecorder()
	h.ListConstraints(rec, authedConstraintReq(http.MethodGet, "/", clusterID.String(), nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if requests := requester.snapshot(); len(requests) != 0 {
		t.Fatalf("deletion intent must not trigger a live violation lookup, got %+v", requests)
	}
	var response struct {
		Data struct {
			Custom []map[string]any `json:"custom"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Data.Custom) != 1 || response.Data.Custom[0]["desired_state"] != "absent" {
		t.Fatalf("pending deletion must remain visible, got %#v", response.Data.Custom)
	}
}

// TestCreateConstraint_ValidTemplateApplies proves a valid ConstraintTemplate
// is server-side-applied through the tunnel and its authored record persisted.
func TestCreateConstraint_ValidTemplateApplies(t *testing.T) {
	clusterID := uuid.New()
	q := newFakeGatekeeperQuerier()
	req := &stubK8sRequester{}
	txq := &transactionalGatekeeperQ{fakeGatekeeperQuerier: q}
	h := transactionalGatekeeperHandler(txq, clusterID, req)

	rec := httptest.NewRecorder()
	h.CreateConstraint(rec, authedConstraintReq(http.MethodPost, "/api/v1/clusters/"+clusterID.String()+"/gatekeeper/constraints/", clusterID.String(), ConstraintYAMLRequest{YAML: validConstraintTemplateYAML}))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status: want 202, got %d body=%s", rec.Code, rec.Body.String())
	}
	resp := decodeConstraintValidation(t, rec.Body.Bytes())
	if !resp.Valid || resp.Applied || resp.Status != "pending" {
		t.Fatalf("want valid queued intent, got %+v", resp)
	}
	if resp.Name != "k8srequiredfoo" || resp.Kind != "ConstraintTemplate" {
		t.Fatalf("unexpected name/kind: %+v", resp)
	}
	if q.upserts != 1 {
		t.Fatalf("want 1 upsert, got %d", q.upserts)
	}
	if reqs := req.snapshot(); len(reqs) != 0 {
		t.Fatalf("HTTP path must not apply remotely, got %+v", reqs)
	}
}

// TestCreateConstraint_InvalidRegoRejectedWithoutApply proves malformed Rego is
// a 400 and NOTHING is applied or persisted.
func TestCreateConstraint_InvalidRegoRejectedWithoutApply(t *testing.T) {
	clusterID := uuid.New()
	q := newFakeGatekeeperQuerier()
	req := &stubK8sRequester{}
	txq := &transactionalGatekeeperQ{fakeGatekeeperQuerier: q}
	h := transactionalGatekeeperHandler(txq, clusterID, req)

	rec := httptest.NewRecorder()
	h.CreateConstraint(rec, authedConstraintReq(http.MethodPost, "/api/v1/clusters/"+clusterID.String()+"/gatekeeper/constraints/", clusterID.String(), ConstraintYAMLRequest{YAML: invalidRegoConstraintTemplateYAML}))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	if len(req.snapshot()) != 0 {
		t.Fatalf("invalid rego must not reach the cluster, got %+v", req.snapshot())
	}
	if q.upserts != 0 {
		t.Fatalf("invalid rego must not persist, got %d upserts", q.upserts)
	}
}

// TestCreateConstraint_NonGatekeeperYAMLRejected proves a non-Gatekeeper
// document is rejected without apply.
func TestCreateConstraint_NonGatekeeperYAMLRejected(t *testing.T) {
	clusterID := uuid.New()
	q := newFakeGatekeeperQuerier()
	req := &stubK8sRequester{}
	h := NewGatekeeperConstraintsHandler(q, req)
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: clustersVerbBindings(clusterID, rbac.VerbUpdate)})

	rec := httptest.NewRecorder()
	h.CreateConstraint(rec, authedConstraintReq(http.MethodPost, "/", clusterID.String(), ConstraintYAMLRequest{YAML: "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: x\n"}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	if len(req.snapshot()) != 0 || q.upserts != 0 {
		t.Fatalf("non-gatekeeper YAML must not apply/persist")
	}
}

// TestCreateConstraint_UnauthorizedForbidden proves a caller lacking
// clusters:update is a 403 and nothing is applied.
func TestCreateConstraint_UnauthorizedForbidden(t *testing.T) {
	clusterID := uuid.New()
	q := newFakeGatekeeperQuerier()
	req := &stubK8sRequester{}
	h := NewGatekeeperConstraintsHandler(q, req)
	// Only clusters:read — not enough for the create path.
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: clustersVerbBindings(clusterID, rbac.VerbRead)})

	rec := httptest.NewRecorder()
	h.CreateConstraint(rec, authedConstraintReq(http.MethodPost, "/", clusterID.String(), ConstraintYAMLRequest{YAML: validConstraintTemplateYAML}))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status: want 403, got %d body=%s", rec.Code, rec.Body.String())
	}
	if len(req.snapshot()) != 0 || q.upserts != 0 {
		t.Fatalf("unauthorized caller must not apply/persist")
	}
}

// TestDeleteConstraint_RemovesRecordAndClusterResource proves delete drops the
// DB record and issues a DELETE to the cluster.
func TestDeleteConstraint_RemovesRecordAndClusterResource(t *testing.T) {
	clusterID := uuid.New()
	q := newFakeGatekeeperQuerier()
	q.authored["must-have-foo"] = sqlc.AuthoredConstraint{
		ID:         uuid.New(),
		ClusterID:  clusterID,
		Name:       "must-have-foo",
		Kind:       "K8sRequiredFoo",
		ApiVersion: "constraints.gatekeeper.sh/v1beta1",
		Yaml:       sampleConstraintYAML,
	}
	req := &stubK8sRequester{}
	txq := &transactionalGatekeeperQ{fakeGatekeeperQuerier: q}
	h := transactionalGatekeeperHandler(txq, clusterID, req)

	r := authedConstraintReq(http.MethodDelete, "/", clusterID.String(), nil)
	// Add the {name} path param the delete handler reads.
	rc := chi.RouteContext(r.Context())
	rc.URLParams.Add("name", "must-have-foo")

	rec := httptest.NewRecorder()
	h.DeleteConstraint(rec, r)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status: want 202, got %d body=%s", rec.Code, rec.Body.String())
	}
	if q.deletes != 0 {
		t.Fatalf("durable delete must retain a tombstone, physical deletes=%d", q.deletes)
	}
	if row := q.authored["must-have-foo"]; row.DesiredState != "absent" || row.SyncStatus != "pending" {
		t.Fatalf("record should be a pending tombstone after delete, got %+v", row)
	}
	if reqs := req.snapshot(); len(reqs) != 0 {
		t.Fatalf("HTTP path must not delete remotely, got %+v", reqs)
	}
}

// TestDeleteConstraint_UnauthorizedForbidden proves delete is RBAC-gated.
func TestDeleteConstraint_UnauthorizedForbidden(t *testing.T) {
	clusterID := uuid.New()
	q := newFakeGatekeeperQuerier()
	q.authored["must-have-foo"] = sqlc.AuthoredConstraint{ClusterID: clusterID, Name: "must-have-foo", Yaml: sampleConstraintYAML}
	req := &stubK8sRequester{}
	h := NewGatekeeperConstraintsHandler(q, req)
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: clustersVerbBindings(clusterID, rbac.VerbRead)})

	r := authedConstraintReq(http.MethodDelete, "/", clusterID.String(), nil)
	chi.RouteContext(r.Context()).URLParams.Add("name", "must-have-foo")
	rec := httptest.NewRecorder()
	h.DeleteConstraint(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status: want 403, got %d body=%s", rec.Code, rec.Body.String())
	}
	if q.deletes != 0 || len(req.snapshot()) != 0 {
		t.Fatalf("unauthorized delete must not mutate")
	}
}

// TestValidateConstraint_NoApply proves validate never touches the cluster.
func TestValidateConstraint_NoApply(t *testing.T) {
	clusterID := uuid.New()
	q := newFakeGatekeeperQuerier()
	req := &stubK8sRequester{}
	h := NewGatekeeperConstraintsHandler(q, req)
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: clustersVerbBindings(clusterID, rbac.VerbRead)})

	rec := httptest.NewRecorder()
	h.ValidateConstraint(rec, authedConstraintReq(http.MethodPost, "/", clusterID.String(), ConstraintYAMLRequest{YAML: validConstraintTemplateYAML}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	resp := decodeConstraintValidation(t, rec.Body.Bytes())
	if !resp.Valid || resp.Applied {
		t.Fatalf("validate should be valid and not applied: %+v", resp)
	}
	if len(req.snapshot()) != 0 {
		t.Fatalf("validate must not reach the cluster")
	}
}
