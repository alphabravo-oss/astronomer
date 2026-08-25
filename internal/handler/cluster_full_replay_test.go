package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

func requestWithParams(method, target, key string, body []byte, params map[string]string) *http.Request {
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	req.Header.Set("Idempotency-Key", key)
	rctx := chi.NewRouteContext()
	for name, value := range params {
		rctx.URLParams.Add(name, value)
	}
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func serveTwice(t *testing.T, invoke func(http.ResponseWriter, *http.Request), build func() *http.Request) (string, string) {
	t.Helper()
	first, second := httptest.NewRecorder(), httptest.NewRecorder()
	invoke(first, build())
	invoke(second, build())
	if first.Code != http.StatusAccepted || second.Code != http.StatusAccepted {
		t.Fatalf("replay statuses=%d/%d bodies=%s / %s", first.Code, second.Code, first.Body.String(), second.Body.String())
	}
	if first.Body.String() != second.Body.String() {
		t.Fatalf("replay receipt changed:\nfirst=%s\nsecond=%s", first.Body.String(), second.Body.String())
	}
	return first.Body.String(), second.Body.String()
}

func TestClusterAgentUpgradeFullReplayAndConflict(t *testing.T) {
	h, q, _, base := transactionalAgentUpgradeFixture(t, nil)
	build := func(body []byte) *http.Request {
		req := requestWithParams(http.MethodPost, base.URL.Path, "agent-upgrade-full-replay", body, map[string]string{"cluster_id": chi.URLParam(base, "cluster_id")})
		return req
	}
	serveTwice(t, h.Upgrade, func() *http.Request { return build(nil) })
	if len(q.created) != 1 || len(q.idempotent) != 1 || len(q.audits) != 1 {
		t.Fatalf("operations/idempotent/audits=%d/%d/%d, want 1/1/1", len(q.created), len(q.idempotent), len(q.audits))
	}
	conflict := httptest.NewRecorder()
	h.Upgrade(conflict, build([]byte(`{"strategy":"agent_self_rollout"}`)))
	if conflict.Code != http.StatusConflict || len(q.created) != 1 || len(q.audits) != 1 {
		t.Fatalf("changed request status=%d operations=%d audits=%d body=%s", conflict.Code, len(q.created), len(q.audits), conflict.Body.String())
	}
}

type tokenRotationReplayTx struct {
	ClusterMutationTx
	fakeOperationIdempotencyStore
	mu      sync.Mutex
	rows    int64
	mutates int
	audits  []sqlc.UpsertAuditOutboxParams
}

func (tx *tokenRotationReplayTx) SetClusterAgentTokenRotationPending(context.Context, uuid.UUID) (int64, error) {
	tx.mutates++
	return tx.rows, nil
}

func (tx *tokenRotationReplayTx) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	tx.audits = append(tx.audits, arg)
	return sqlc.AuditOutbox{ID: arg.ID, Action: arg.Action}, nil
}

func TestAgentTokenRotateFullReplay(t *testing.T) {
	clusterID := uuid.New()
	q := &clusterRegistryTestQuerier{}
	tx := &tokenRotationReplayTx{rows: 1}
	h := NewClusterHandler(q)
	h.SetRunTx(func(_ context.Context, fn func(ClusterMutationTx) error) error {
		tx.mu.Lock()
		defer tx.mu.Unlock()
		return fn(tx)
	})
	build := func() *http.Request {
		return requestWithParams(http.MethodPost, "/api/v1/clusters/"+clusterID.String()+"/agent-token/rotate/", "token-rotate-full-replay", nil, map[string]string{"id": clusterID.String()})
	}
	serveTwice(t, h.RotateAgentToken, build)
	if tx.mutates != 1 || len(tx.audits) != 1 {
		t.Fatalf("rotation mutations/audits=%d/%d, want 1/1", tx.mutates, len(tx.audits))
	}
}

func (f *fakeAtomicClusterTemplateQuerier) UpsertTaskOutbox(_ context.Context, arg sqlc.UpsertTaskOutboxParams) (sqlc.TaskOutbox, error) {
	f.taskRows = append(f.taskRows, arg)
	return sqlc.TaskOutbox{ID: uuid.New(), TaskType: arg.TaskType}, nil
}

func (f *fakeAtomicClusterTemplateQuerier) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	f.auditOutbox = append(f.auditOutbox, arg)
	return sqlc.AuditOutbox{ID: arg.ID, Action: arg.Action}, nil
}

func TestClusterTemplateBindAndReapplyFullReplay(t *testing.T) {
	base := newFakeClusterTemplateQuerier()
	clusterID, firstTemplateID, secondTemplateID := uuid.New(), uuid.New(), uuid.New()
	base.clusters[clusterID] = sqlc.Cluster{ID: clusterID, Name: "prod"}
	base.templates[firstTemplateID] = sqlc.ClusterTemplate{ID: firstTemplateID, Name: "first", Spec: json.RawMessage(`{"tools":[]}`)}
	base.templates[secondTemplateID] = sqlc.ClusterTemplate{ID: secondTemplateID, Name: "second", Spec: json.RawMessage(`{"tools":[]}`)}
	q := &fakeAtomicClusterTemplateQuerier{fakeClusterTemplateQuerier: base}
	h := NewClusterTemplateHandler(q)
	var txMu sync.Mutex
	h.SetRunTx(func(_ context.Context, fn func(ClusterTemplateMutationTx) error) error {
		txMu.Lock()
		defer txMu.Unlock()
		return fn(q)
	})
	applyBody, _ := json.Marshal(ApplyClusterTemplateRequest{TemplateID: firstTemplateID.String()})
	buildApply := func(body []byte) *http.Request {
		return requestWithParams(http.MethodPost, "/api/v1/clusters/"+clusterID.String()+"/template/", "template-bind-full-replay", body, map[string]string{"cluster_id": clusterID.String()})
	}
	serveTwice(t, h.Apply, func() *http.Request { return buildApply(applyBody) })
	if len(q.atomicApps) != 1 || len(q.auditOutbox) != 1 {
		t.Fatalf("bind tasks/audits=%d/%d, want 1/1", len(q.atomicApps), len(q.auditOutbox))
	}
	changedBody, _ := json.Marshal(ApplyClusterTemplateRequest{TemplateID: secondTemplateID.String()})
	changed := httptest.NewRecorder()
	h.Apply(changed, buildApply(changedBody))
	if changed.Code != http.StatusConflict || len(q.atomicApps) != 1 || len(q.auditOutbox) != 1 {
		t.Fatalf("bind conflict status=%d tasks=%d audits=%d", changed.Code, len(q.atomicApps), len(q.auditOutbox))
	}
	buildReapply := func() *http.Request {
		return requestWithParams(http.MethodPost, "/api/v1/clusters/"+clusterID.String()+"/template/reapply/", "template-reapply-full-replay", nil, map[string]string{"cluster_id": clusterID.String()})
	}
	serveTwice(t, h.Reapply, buildReapply)
	if len(q.atomicApps) != 2 || len(q.auditOutbox) != 2 {
		t.Fatalf("bind+reapply tasks/audits=%d/%d, want 2/2", len(q.atomicApps), len(q.auditOutbox))
	}
}

func TestAllowlistReconcileFullReplay(t *testing.T) {
	clusterID := uuid.New()
	q := &transactionalAllowlistQ{fakeAllowlistQuerier: &fakeAllowlistQuerier{cluster: sqlc.Cluster{ID: clusterID, Provider: "eks"}, row: &sqlc.ApiserverAllowlist{ClusterID: clusterID, Mode: "monitor"}}}
	h := transactionalAllowlistHandler(q)
	build := func() *http.Request {
		return requestWithParams(http.MethodPost, "/clusters/"+clusterID.String()+"/apiserver-allowlist/reconcile/", "allowlist-full-replay", nil, map[string]string{"cluster_id": clusterID.String()})
	}
	serveTwice(t, h.Reconcile, build)
	if q.rowLocks != 1 || len(q.tasks) != 1 || len(q.auditRows) != 1 {
		t.Fatalf("locks/tasks/audits=%d/%d/%d, want 1/1/1", q.rowLocks, len(q.tasks), len(q.auditRows))
	}
}

func (f *fakeNetPolHandlerQuerier) UpsertNetworkPolicyApplication(ctx context.Context, arg sqlc.UpsertNetworkPolicyApplicationParams) (sqlc.NetworkPolicyApplication, error) {
	if existing, err := f.GetNetworkPolicyApplicationByUnique(ctx, sqlc.GetNetworkPolicyApplicationByUniqueParams{ClusterID: arg.ClusterID, Namespace: arg.Namespace, TemplateID: arg.TemplateID}); err == nil {
		existing.Status, existing.LastError = "pending", ""
		f.applications[existing.ID] = existing
		return existing, nil
	}
	return f.CreateNetworkPolicyApplication(ctx, sqlc.CreateNetworkPolicyApplicationParams(arg))
}

func (f *fakeNetPolHandlerQuerier) UpsertTaskOutbox(_ context.Context, arg sqlc.UpsertTaskOutboxParams) (sqlc.TaskOutbox, error) {
	f.taskOutbox = append(f.taskOutbox, arg)
	return sqlc.TaskOutbox{ID: uuid.New(), TaskType: arg.TaskType}, nil
}

func (f *fakeNetPolHandlerQuerier) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	f.auditOutbox = append(f.auditOutbox, arg)
	return sqlc.AuditOutbox{ID: arg.ID, Action: arg.Action}, nil
}

func TestNetworkPolicyCreateAndReapplyFullReplay(t *testing.T) {
	q := newFakeNetPolHandlerQuerier()
	tmpl := mkNetPolTemplate("deny_all_ingress", "builtin")
	q.templates[tmpl.ID], q.bySlug[tmpl.Slug] = tmpl, tmpl.ID
	clusterID := uuid.New()
	q.clusters[clusterID] = sqlc.Cluster{ID: clusterID, Name: "prod"}
	h := NewNetworkPolicyHandler(q)
	var txMu sync.Mutex
	h.SetRunTx(func(_ context.Context, fn func(NetworkPolicyMutationTx) error) error {
		txMu.Lock()
		defer txMu.Unlock()
		return fn(q)
	})
	body, _ := json.Marshal(ApplyNetworkPolicyRequest{TemplateID: tmpl.ID.String(), Namespace: "team-a"})
	buildCreate := func(payload []byte) *http.Request {
		return requestWithParams(http.MethodPost, "/api/v1/clusters/"+clusterID.String()+"/network-policies/applications/", "network-create-full-replay", payload, map[string]string{"cluster_id": clusterID.String()})
	}
	serveTwice(t, h.CreateApplications, func() *http.Request { return buildCreate(body) })
	if len(q.applications) != 1 || len(q.taskOutbox) != 1 || len(q.auditOutbox) != 1 {
		t.Fatalf("applications/tasks/audits=%d/%d/%d, want 1/1/1", len(q.applications), len(q.taskOutbox), len(q.auditOutbox))
	}
	changedBody, _ := json.Marshal(ApplyNetworkPolicyRequest{TemplateID: tmpl.ID.String(), Namespace: "team-b"})
	changed := httptest.NewRecorder()
	h.CreateApplications(changed, buildCreate(changedBody))
	if changed.Code != http.StatusConflict || len(q.applications) != 1 || len(q.taskOutbox) != 1 {
		t.Fatalf("create conflict status=%d applications=%d tasks=%d", changed.Code, len(q.applications), len(q.taskOutbox))
	}
	var appID uuid.UUID
	for id := range q.applications {
		appID = id
	}
	q.applications[appID] = sqlc.NetworkPolicyApplication{ID: appID, TemplateID: tmpl.ID, ClusterID: clusterID, Namespace: "team-a", PolicyName: "astronomer-deny", Status: "failed"}
	buildReapply := func() *http.Request {
		return requestWithParams(http.MethodPost, "/api/v1/clusters/"+clusterID.String()+"/network-policies/applications/"+appID.String()+"/reapply/", "network-reapply-full-replay", nil, map[string]string{"cluster_id": clusterID.String(), "id": appID.String()})
	}
	serveTwice(t, h.Reapply, buildReapply)
	if len(q.taskOutbox) != 2 || len(q.auditOutbox) != 2 {
		t.Fatalf("create+reapply tasks/audits=%d/%d, want 2/2", len(q.taskOutbox), len(q.auditOutbox))
	}
}

func constraintRequestWithActor(method, target, key, clusterID, name, actor string, body any) *http.Request {
	req := authedConstraintReq(method, target, clusterID, body)
	req.Header.Set("Idempotency-Key", key)
	if name != "" {
		chi.RouteContext(req.Context()).URLParams.Add("name", name)
	}
	ctx := middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{ID: actor})
	return req.WithContext(ctx)
}

func TestGatekeeperCreateAndDeleteFullReplay(t *testing.T) {
	clusterID, actor := uuid.New(), uuid.NewString()
	base := newFakeGatekeeperQuerier()
	q := &transactionalGatekeeperQ{fakeGatekeeperQuerier: base}
	h := transactionalGatekeeperHandler(q, clusterID, &stubK8sRequester{})
	buildCreate := func(yaml string) *http.Request {
		return constraintRequestWithActor(http.MethodPost, "/", "gatekeeper-create-full-replay", clusterID.String(), "", actor, ConstraintYAMLRequest{YAML: yaml})
	}
	serveTwice(t, h.CreateConstraint, func() *http.Request { return buildCreate(sampleConstraintYAML) })
	row := q.authored["must-have-foo"]
	if row.Generation != 1 || len(q.tasks) != 1 || len(q.audits) != 1 {
		t.Fatalf("create generation/tasks/audits=%d/%d/%d, want 1/1/1", row.Generation, len(q.tasks), len(q.audits))
	}
	changedYAML := bytes.ReplaceAll([]byte(sampleConstraintYAML), []byte("dryrun"), []byte("deny"))
	changed := httptest.NewRecorder()
	h.CreateConstraint(changed, buildCreate(string(changedYAML)))
	if changed.Code != http.StatusConflict || q.authored["must-have-foo"].Generation != 1 || len(q.tasks) != 1 {
		t.Fatalf("create conflict status=%d generation=%d tasks=%d", changed.Code, q.authored["must-have-foo"].Generation, len(q.tasks))
	}
	buildDelete := func() *http.Request {
		return constraintRequestWithActor(http.MethodDelete, "/", "gatekeeper-delete-full-replay", clusterID.String(), "must-have-foo", actor, nil)
	}
	serveTwice(t, h.DeleteConstraint, buildDelete)
	row = q.authored["must-have-foo"]
	if row.Generation != 2 || len(q.tasks) != 2 || len(q.audits) != 2 {
		t.Fatalf("delete generation/tasks/audits=%d/%d/%d, want 2/2/2", row.Generation, len(q.tasks), len(q.audits))
	}
}

func TestClusterFullReplayConcurrentIdentical(t *testing.T) {
	// The actor+route reservation is serialized by the transaction just as the
	// production PostgreSQL row lock is. Running the same request concurrently
	// under the race detector proves the receipt path does not require shared
	// mutable request state.
	h, q, _, base := transactionalAgentUpgradeFixture(t, nil)
	var txMu sync.Mutex
	baseRunTx := fakeClusterAgentRunTx(q)
	h.SetRunTx(func(ctx context.Context, fn func(ClusterAgentMutationTx) error) error {
		txMu.Lock()
		defer txMu.Unlock()
		return baseRunTx(ctx, fn)
	})
	const callers = 8
	statuses := make(chan int, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := requestWithParams(http.MethodPost, base.URL.Path, "agent-upgrade-race", nil, map[string]string{"cluster_id": chi.URLParam(base, "cluster_id")})
			rec := httptest.NewRecorder()
			h.Upgrade(rec, req)
			statuses <- rec.Code
		}()
	}
	wg.Wait()
	close(statuses)
	for status := range statuses {
		if status != http.StatusAccepted {
			t.Fatalf("concurrent replay status=%d", status)
		}
	}
	if len(q.created) != 1 || len(q.audits) != 1 {
		t.Fatalf("concurrent operations/audits=%d/%d, want 1/1", len(q.created), len(q.audits))
	}
}

func TestClusterOperationReceiptBindingsConcurrentReplayAndConflict(t *testing.T) {
	for _, tc := range []struct {
		name, domain, table, path string
	}{
		{name: "cluster agent upgrade", domain: "agent_lifecycle", table: "agent_lifecycle_operations", path: "/api/v1/cluster-agents/c/upgrade/"},
		{name: "agent token rotate", domain: "agent_token_rotation", table: "cluster_agent_token_rotations", path: "/api/v1/clusters/c/agent-token/rotate/"},
		{name: "template bind", domain: "cluster_template_apply", table: "cluster_template_applications", path: "/api/v1/clusters/c/template/"},
		{name: "template reapply", domain: "cluster_template_reapply", table: "cluster_template_reapplications", path: "/api/v1/clusters/c/template/reapply/"},
		{name: "allowlist reconcile", domain: "apiserver_allowlist_reconcile", table: "apiserver_allowlist_reconciles", path: "/api/v1/clusters/c/apiserver-allowlist/reconcile/"},
		{name: "network policy create", domain: "network_policy_apply", table: "network_policy_applications", path: "/api/v1/clusters/c/network-policies/applications/"},
		{name: "network policy reapply", domain: "network_policy_reapply", table: "network_policy_reapplications", path: "/api/v1/clusters/c/network-policies/applications/a/reapply/"},
		{name: "Gatekeeper create", domain: "gatekeeper_constraint_create", table: "gatekeeper_constraint_creates", path: "/api/v1/clusters/c/gatekeeper/constraints/"},
		{name: "Gatekeeper delete", domain: "gatekeeper_constraint_delete", table: "gatekeeper_constraint_deletes", path: "/api/v1/clusters/c/gatekeeper/constraints/name/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeOperationIdempotencyStore{}
			operationID := uuid.New()
			request := httptest.NewRequest(http.MethodPost, tc.path, nil)
			request.Header.Set("Idempotency-Key", "concurrent-binding")
			ctx := withOperationIdempotency(request, tc.domain)
			const callers = 8
			var txMu sync.Mutex
			created := 0
			statuses := make(chan error, callers)
			var wg sync.WaitGroup
			for i := 0; i < callers; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					txMu.Lock()
					defer txMu.Unlock()
					_, receipt, replay, err := claimOperationReceipt[string](ctx, store, tc.table, "sha256:same")
					if err != nil {
						statuses <- err
						return
					}
					if replay {
						if receipt != "exact-receipt" {
							statuses <- errOperationIdempotencyConflict
							return
						}
						statuses <- nil
						return
					}
					created++
					statuses <- attachOperationReceipt(ctx, store, tc.table, operationID, "sha256:same", "exact-receipt")
				}()
			}
			wg.Wait()
			close(statuses)
			for err := range statuses {
				if err != nil {
					t.Fatalf("concurrent replay: %v", err)
				}
			}
			if created != 1 {
				t.Fatalf("created bindings=%d, want 1", created)
			}
			if _, _, _, err := claimOperationReceipt[string](ctx, store, tc.table, "sha256:changed"); !errors.Is(err, errOperationIdempotencyConflict) {
				t.Fatalf("changed digest error=%v, want conflict", err)
			}
		})
	}
}
