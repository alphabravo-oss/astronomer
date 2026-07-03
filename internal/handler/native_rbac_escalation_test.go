package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// fakeNativeRBACQuerier records whether a rule was actually persisted so a test
// can assert the escalation guard blocked the write BEFORE it hit the DB.
type fakeNativeRBACQuerier struct {
	created  int
	lastArgs sqlc.CreateNativeRBACRuleParams
}

func (f *fakeNativeRBACQuerier) CreateNativeRBACRule(_ context.Context, arg sqlc.CreateNativeRBACRuleParams) (sqlc.NativeRbacRule, error) {
	f.created++
	f.lastArgs = arg
	return sqlc.NativeRbacRule{
		ID:        uuid.New(),
		UserID:    arg.UserID,
		ClusterID: arg.ClusterID,
		Namespace: arg.Namespace,
		ApiGroup:  arg.ApiGroup,
		Resource:  arg.Resource,
		Verbs:     arg.Verbs,
	}, nil
}

func (f *fakeNativeRBACQuerier) GetNativeRBACRuleByID(context.Context, uuid.UUID) (sqlc.NativeRbacRule, error) {
	return sqlc.NativeRbacRule{}, nil
}
func (f *fakeNativeRBACQuerier) ListNativeRBACRulesByUser(context.Context, uuid.UUID) ([]sqlc.NativeRbacRule, error) {
	return nil, nil
}
func (f *fakeNativeRBACQuerier) ListNativeRBACRules(context.Context, sqlc.ListNativeRBACRulesParams) ([]sqlc.NativeRbacRule, error) {
	return nil, nil
}
func (f *fakeNativeRBACQuerier) DeleteNativeRBACRule(context.Context, uuid.UUID) error { return nil }

// stubNativeEscalationBindings returns a fixed binding set for the caller.
type stubNativeEscalationBindings struct {
	bindings []rbac.RoleBinding
}

func (s stubNativeEscalationBindings) GetUserBindings(context.Context, string) ([]rbac.RoleBinding, error) {
	return s.bindings, nil
}

func createNativeRuleReq(t *testing.T, callerID string, body map[string]any) *http.Request {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/native-rbac-rules/", bytes.NewReader(raw))
	ctx := middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{ID: callerID, AuthMethod: "jwt"})
	return req.WithContext(ctx)
}

func newGuardedNativeHandler(bindings []rbac.RoleBinding) (*NativeRBACHandler, *fakeNativeRBACQuerier) {
	q := &fakeNativeRBACQuerier{}
	h := NewNativeRBACHandler(q)
	h.SetAuthorization(rbac.NewEngine(), stubNativeEscalationBindings{bindings: bindings})
	return h, q
}

// TestNativeRBACCreate_BlocksSecretEscalation is the core bypass-closed proof:
// a caller holding rbac:* (the delegated-admin who may author RBAC) but NOT any
// secrets grant cannot self-author a native rule that grants secret reads.
func TestNativeRBACCreate_BlocksSecretEscalation(t *testing.T) {
	callerID := uuid.NewString()
	// Caller can author RBAC, but holds no secrets access.
	rbacOnly := []rbac.RoleBinding{{
		UserID:    callerID,
		RoleRules: []rbac.Rule{{Resource: string(rbac.ResourceRBAC), Verbs: []string{"*"}}},
	}}
	h, q := newGuardedNativeHandler(rbacOnly)

	rec := httptest.NewRecorder()
	req := createNativeRuleReq(t, callerID, map[string]any{
		"userId":   callerID,
		"apiGroup": "",
		"resource": "secrets",
		"verbs":    []string{"read"},
	})
	h.Create(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403 for secrets escalation, got %d body=%s", rec.Code, rec.Body.String())
	}
	if q.created != 0 {
		t.Fatalf("guard must block BEFORE the DB write, but CreateNativeRBACRule ran %d times", q.created)
	}
}

// TestNativeRBACCreate_BlocksCRDEscalation proves a CRD-targeting rule is
// charged against custom_resources: a caller lacking that permission is denied.
func TestNativeRBACCreate_BlocksCRDEscalation(t *testing.T) {
	callerID := uuid.NewString()
	rbacOnly := []rbac.RoleBinding{{
		UserID:    callerID,
		RoleRules: []rbac.Rule{{Resource: string(rbac.ResourceRBAC), Verbs: []string{"*"}}},
	}}
	h, q := newGuardedNativeHandler(rbacOnly)

	rec := httptest.NewRecorder()
	req := createNativeRuleReq(t, callerID, map[string]any{
		"userId":   callerID,
		"apiGroup": "example.com",
		"resource": "widgets",
		"verbs":    []string{"read"},
	})
	h.Create(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403 for CRD escalation, got %d body=%s", rec.Code, rec.Body.String())
	}
	if q.created != 0 {
		t.Fatalf("guard must block CRD escalation before DB write, got %d writes", q.created)
	}
}

// TestNativeRBACCreate_AllowsLegitAuthor proves a caller who ALREADY holds the
// permission at the rule's scope may author the equivalent native rule — the
// guard fails closed on escalation but never blocks a legitimate delegate.
func TestNativeRBACCreate_AllowsLegitAuthor(t *testing.T) {
	callerID := uuid.NewString()
	targetUser := uuid.NewString()
	// Caller genuinely holds secrets read+list globally.
	secretsReader := []rbac.RoleBinding{{
		UserID: callerID,
		RoleRules: []rbac.Rule{{
			Resource: string(rbac.ResourceSecrets),
			Verbs:    []string{string(rbac.VerbRead), string(rbac.VerbList)},
		}},
	}}
	h, q := newGuardedNativeHandler(secretsReader)

	rec := httptest.NewRecorder()
	req := createNativeRuleReq(t, callerID, map[string]any{
		"userId":   targetUser,
		"apiGroup": "",
		"resource": "secrets",
		"verbs":    []string{"read"},
	})
	h.Create(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("legit author want 201, got %d body=%s", rec.Code, rec.Body.String())
	}
	if q.created != 1 {
		t.Fatalf("legit author should persist exactly one rule, got %d", q.created)
	}
}

// TestNativeRBACCreate_BlocksWildcardCoreEscalation is the regression proof for
// the audit finding: a caller holding clusters:* + rbac:* (but no secrets grant)
// must NOT be able to author {apiGroup:"", resource:"*"}. That wildcard grants
// secret reads at request time (rbac.NativeAllow), yet the old guard charged it
// only against clusters:<verb> and let it through. It must now be charged
// against the full sensitive set and denied.
func TestNativeRBACCreate_BlocksWildcardCoreEscalation(t *testing.T) {
	callerID := uuid.NewString()
	// Caller can manage clusters and RBAC but holds no secrets/pods/etc. access.
	clustersAndRBAC := []rbac.RoleBinding{{
		UserID: callerID,
		RoleRules: []rbac.Rule{
			{Resource: string(rbac.ResourceClusters), Verbs: []string{"*"}},
			{Resource: string(rbac.ResourceRBAC), Verbs: []string{"*"}},
		},
	}}
	h, q := newGuardedNativeHandler(clustersAndRBAC)

	rec := httptest.NewRecorder()
	req := createNativeRuleReq(t, callerID, map[string]any{
		"userId":   callerID,
		"apiGroup": "",
		"resource": "*",
		"verbs":    []string{"read"},
	})
	h.Create(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403 for wildcard-core escalation, got %d body=%s", rec.Code, rec.Body.String())
	}
	if q.created != 0 {
		t.Fatalf("wildcard-core escalation must not persist, got %d writes", q.created)
	}

	// A caller who genuinely holds every sensitive resource (superuser-like via
	// explicit grants) can author it. Grant read on the whole sensitive set.
	rules := []rbac.Rule{{Resource: string(rbac.ResourceClusters), Verbs: []string{"*"}}}
	for _, res := range sensitiveNativeResources() {
		rules = append(rules, rbac.Rule{Resource: string(res), Verbs: []string{string(rbac.VerbRead)}})
	}
	h2, q2 := newGuardedNativeHandler([]rbac.RoleBinding{{UserID: callerID, RoleRules: rules}})
	rec2 := httptest.NewRecorder()
	req2 := createNativeRuleReq(t, callerID, map[string]any{
		"userId":   callerID,
		"apiGroup": "",
		"resource": "*",
		"verbs":    []string{"read"},
	})
	h2.Create(rec2, req2)
	if rec2.Code != http.StatusCreated {
		t.Fatalf("fully-privileged wildcard author want 201, got %d body=%s", rec2.Code, rec2.Body.String())
	}
	if q2.created != 1 {
		t.Fatalf("privileged wildcard author should persist one rule, got %d", q2.created)
	}
}

// TestNativeRBACCreate_ScopeMismatchDenied proves the guard is scope-aware: a
// caller holding secrets:read only on cluster A cannot author an all-clusters
// (empty clusterId) rule granting secret reads everywhere.
func TestNativeRBACCreate_ScopeMismatchDenied(t *testing.T) {
	callerID := uuid.NewString()
	clusterA := uuid.New()
	clusterScoped := []rbac.RoleBinding{{
		UserID:    callerID,
		ClusterID: clusterA.String(),
		RoleRules: []rbac.Rule{{Resource: string(rbac.ResourceSecrets), Verbs: []string{string(rbac.VerbRead)}}},
	}}
	h, q := newGuardedNativeHandler(clusterScoped)

	rec := httptest.NewRecorder()
	// No clusterId => rule applies to ALL clusters, broader than the caller holds.
	req := createNativeRuleReq(t, callerID, map[string]any{
		"userId":   callerID,
		"apiGroup": "",
		"resource": "secrets",
		"verbs":    []string{"read"},
	})
	h.Create(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403 for all-cluster grant from cluster-scoped caller, got %d body=%s", rec.Code, rec.Body.String())
	}
	if q.created != 0 {
		t.Fatalf("cross-scope escalation must not persist, got %d writes", q.created)
	}

	// But scoping the rule to cluster A (where the caller holds it) is allowed.
	rec2 := httptest.NewRecorder()
	req2 := createNativeRuleReq(t, callerID, map[string]any{
		"userId":    callerID,
		"clusterId": clusterA.String(),
		"apiGroup":  "",
		"resource":  "secrets",
		"verbs":     []string{"read"},
	})
	h.Create(rec2, req2)
	if rec2.Code != http.StatusCreated {
		t.Fatalf("in-scope grant want 201, got %d body=%s", rec2.Code, rec2.Body.String())
	}
}

// TestNativeRBACCreate_SuperuserBypass proves a superuser may author any rule
// even without an explicit matching grant (mirrors the engine short-circuit).
func TestNativeRBACCreate_SuperuserBypass(t *testing.T) {
	callerID := uuid.NewString()
	superuser := []rbac.RoleBinding{{UserID: callerID, IsSuperuser: true}}
	h, q := newGuardedNativeHandler(superuser)

	rec := httptest.NewRecorder()
	req := createNativeRuleReq(t, callerID, map[string]any{
		"userId":   callerID,
		"apiGroup": "example.com",
		"resource": "widgets",
		"verbs":    []string{"read", "create"},
	})
	h.Create(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("superuser author want 201, got %d body=%s", rec.Code, rec.Body.String())
	}
	if q.created != 1 {
		t.Fatalf("superuser rule should persist, got %d", q.created)
	}
}

// TestNativeRBACCreate_NoAuthzWiredSkipsGuard preserves the optional-
// authorization contract: with no engine/bindings the handler behaves as before.
func TestNativeRBACCreate_NoAuthzWiredSkipsGuard(t *testing.T) {
	callerID := uuid.NewString()
	q := &fakeNativeRBACQuerier{}
	h := NewNativeRBACHandler(q) // no SetAuthorization

	rec := httptest.NewRecorder()
	req := createNativeRuleReq(t, callerID, map[string]any{
		"userId":   callerID,
		"apiGroup": "",
		"resource": "secrets",
		"verbs":    []string{"read"},
	})
	h.Create(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("unwired handler want 201, got %d body=%s", rec.Code, rec.Body.String())
	}
	if q.created != 1 {
		t.Fatalf("unwired handler should persist, got %d", q.created)
	}
}
