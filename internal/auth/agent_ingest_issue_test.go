package auth

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

// fakeIngestQuerier is an in-memory AgentIngestQuerier that records every
// provisioning call so the test can assert the service identity, the
// cluster-scoped grant, and the minted token carry exactly the needed authority.
type fakeIngestQuerier struct {
	user     *sqlc.User
	role     *sqlc.ClusterRole
	bindings []sqlc.CreateClusterRoleBindingParams
	tokens   []sqlc.CreateAPITokenParams
	revoked  []sqlc.RevokeAPITokensByNameParams

	createUserCalls int
	createRoleCalls int
}

func (f *fakeIngestQuerier) GetUserByUsername(_ context.Context, username string) (sqlc.User, error) {
	if f.user != nil && f.user.Username == username {
		return *f.user, nil
	}
	return sqlc.User{}, errors.New("not found")
}

func (f *fakeIngestQuerier) CreateServiceUser(_ context.Context, arg sqlc.CreateServiceUserParams) (sqlc.User, error) {
	f.createUserCalls++
	u := sqlc.User{ID: uuid.New(), Email: arg.Email, Username: arg.Username, IsActive: true}
	f.user = &u
	return u, nil
}

func (f *fakeIngestQuerier) GetClusterRoleByName(_ context.Context, name string) (sqlc.ClusterRole, error) {
	if f.role != nil && f.role.Name == name {
		return *f.role, nil
	}
	return sqlc.ClusterRole{}, errors.New("not found")
}

func (f *fakeIngestQuerier) CreateClusterRole(_ context.Context, arg sqlc.CreateClusterRoleParams) (sqlc.ClusterRole, error) {
	f.createRoleCalls++
	r := sqlc.ClusterRole{ID: uuid.New(), Name: arg.Name, Rules: arg.Rules}
	f.role = &r
	return r, nil
}

func (f *fakeIngestQuerier) CountClusterRoleBindingForUserCluster(_ context.Context, arg sqlc.CountClusterRoleBindingForUserClusterParams) (int64, error) {
	for _, b := range f.bindings {
		if b.UserID == arg.UserID && b.ClusterID == arg.ClusterID && b.RoleID == arg.RoleID {
			return 1, nil
		}
	}
	return 0, nil
}

func (f *fakeIngestQuerier) CreateClusterRoleBinding(_ context.Context, arg sqlc.CreateClusterRoleBindingParams) (sqlc.ClusterRoleBinding, error) {
	f.bindings = append(f.bindings, arg)
	return sqlc.ClusterRoleBinding{ID: uuid.New()}, nil
}

func (f *fakeIngestQuerier) RevokeAPITokensByName(_ context.Context, arg sqlc.RevokeAPITokensByNameParams) error {
	f.revoked = append(f.revoked, arg)
	return nil
}

func (f *fakeIngestQuerier) CreateAPIToken(_ context.Context, arg sqlc.CreateAPITokenParams) (sqlc.ApiToken, error) {
	f.tokens = append(f.tokens, arg)
	return sqlc.ApiToken{ID: uuid.New(), UserID: arg.UserID, Name: arg.Name, TokenHash: arg.TokenHash, Scopes: arg.Scopes}, nil
}

func TestIssueAgentIngestTokenProvisionsNarrowAuthority(t *testing.T) {
	clusterID := uuid.New()
	f := &fakeIngestQuerier{}

	plaintext, err := IssueAgentIngestToken(context.Background(), f, clusterID)
	if err != nil {
		t.Fatalf("IssueAgentIngestToken: %v", err)
	}

	// 1. Service user created once, flagged as a service principal username.
	if f.createUserCalls != 1 {
		t.Errorf("createUserCalls = %d, want 1", f.createUserCalls)
	}
	if f.user == nil || f.user.Username != AgentIngestServiceUsername {
		t.Fatalf("service user not created with reserved username")
	}

	// 2. Exactly one cluster-scoped binding for THIS cluster, no namespace,
	//    no group (it is the user's own binding).
	if len(f.bindings) != 1 {
		t.Fatalf("bindings = %d, want 1", len(f.bindings))
	}
	b := f.bindings[0]
	if b.ClusterID != clusterID {
		t.Errorf("binding cluster = %v, want %v", b.ClusterID, clusterID)
	}
	if b.Namespace != "" || b.Group != "" {
		t.Errorf("binding should be unscoped-within-cluster and user-owned: %+v", b)
	}
	if b.UserID != (pgtype.UUID{Bytes: f.user.ID, Valid: true}) {
		t.Errorf("binding user = %v, want %v", b.UserID, f.user.ID)
	}

	// 3. The role grants exactly clusters:update — assert via the real RBAC
	//    engine that the binding lets cluster:update through on THIS cluster but
	//    not on another, and grants nothing else.
	var rules []rbac.Rule
	if err := json.Unmarshal(f.role.Rules, &rules); err != nil {
		t.Fatalf("role rules JSON: %v", err)
	}
	binding := rbac.RoleBinding{
		UserID:    f.user.ID.String(),
		RoleRules: rules,
		Scope:     "cluster",
		ClusterID: clusterID.String(),
	}
	engine := rbac.NewEngine()
	if !engine.CheckPermission([]rbac.RoleBinding{binding}, rbac.ResourceClusters, rbac.VerbUpdate, clusterID, uuid.Nil) {
		t.Error("binding must grant clusters:update on the target cluster")
	}
	if engine.CheckPermission([]rbac.RoleBinding{binding}, rbac.ResourceClusters, rbac.VerbUpdate, uuid.New(), uuid.Nil) {
		t.Error("binding must NOT grant clusters:update on a different cluster")
	}
	if engine.CheckPermission([]rbac.RoleBinding{binding}, rbac.ResourceClusters, rbac.VerbDelete, clusterID, uuid.Nil) {
		t.Error("binding must NOT grant clusters:delete")
	}
	if engine.CheckPermission([]rbac.RoleBinding{binding}, rbac.ResourceRBAC, rbac.VerbUpdate, clusterID, uuid.Nil) {
		t.Error("binding must NOT grant rbac:update")
	}

	// 4. Exactly one token minted, scoped to clusters:write only, hashed (not
	//    plaintext), named for the cluster.
	if len(f.tokens) != 1 {
		t.Fatalf("tokens = %d, want 1", len(f.tokens))
	}
	tok := f.tokens[0]
	if tok.UserID != f.user.ID {
		t.Errorf("token user = %v, want %v", tok.UserID, f.user.ID)
	}
	if tok.Name != AgentIngestTokenName(clusterID) {
		t.Errorf("token name = %q, want %q", tok.Name, AgentIngestTokenName(clusterID))
	}
	if tok.TokenHash == plaintext || tok.TokenHash != HashAgentIngestToken(plaintext) {
		t.Errorf("token must persist the hash of the plaintext, not the plaintext")
	}
	scopes, err := ParseTokenScopes(tok.Scopes)
	if err != nil {
		t.Fatalf("parse token scopes: %v", err)
	}
	if !ScopeAllowsRequest(scopes, ScopeWriteClusters) {
		t.Error("token must satisfy clusters:write (the ingest route scope)")
	}
	if ScopeAllowsRequest(scopes, ScopeWriteRBAC) || ScopeAllowsRequest(scopes, ScopeAdmin) {
		t.Error("token must grant nothing beyond clusters:write")
	}
}

func TestIssueAgentIngestTokenReusesIdentityAndRemints(t *testing.T) {
	clusterID := uuid.New()
	f := &fakeIngestQuerier{}

	if _, err := IssueAgentIngestToken(context.Background(), f, clusterID); err != nil {
		t.Fatalf("first issue: %v", err)
	}
	if _, err := IssueAgentIngestToken(context.Background(), f, clusterID); err != nil {
		t.Fatalf("second issue: %v", err)
	}

	// Identity is created once and reused across connects.
	if f.createUserCalls != 1 {
		t.Errorf("service user created %d times, want 1 (reuse)", f.createUserCalls)
	}
	if f.createRoleCalls != 1 {
		t.Errorf("cluster role created %d times, want 1 (reuse)", f.createRoleCalls)
	}
	// Binding is not duplicated on the second connect.
	if len(f.bindings) != 1 {
		t.Errorf("bindings = %d, want 1 (no duplicate on reconnect)", len(f.bindings))
	}
	// Each connect revokes prior tokens then mints a fresh one (no pileup).
	if len(f.revoked) != 2 {
		t.Errorf("revoke calls = %d, want 2 (mint-fresh/revoke-old per connect)", len(f.revoked))
	}
	if len(f.tokens) != 2 {
		t.Errorf("tokens minted = %d, want 2", len(f.tokens))
	}
}
