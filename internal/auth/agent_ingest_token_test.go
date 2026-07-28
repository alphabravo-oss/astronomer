package auth

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func TestGenerateAgentIngestTokenShape(t *testing.T) {
	tok, err := GenerateAgentIngestToken()
	if err != nil {
		t.Fatalf("GenerateAgentIngestToken: %v", err)
	}
	if !strings.HasPrefix(tok, AgentIngestTokenPrefix) {
		t.Errorf("token %q missing prefix %q", tok, AgentIngestTokenPrefix)
	}
	// Distinct tokens each call.
	tok2, _ := GenerateAgentIngestToken()
	if tok == tok2 {
		t.Error("two generated tokens were identical")
	}
	// Hash is deterministic for a given plaintext and not the plaintext.
	h := HashAgentIngestToken(tok)
	if h == "" || h == tok {
		t.Errorf("hash unusable: %q", h)
	}
	if HashAgentIngestToken(tok) != h {
		t.Error("hash not deterministic")
	}
	// Display prefix fits the VARCHAR(16) column.
	if p := AgentIngestTokenDisplayPrefix(tok); len(p) > 16 {
		t.Errorf("display prefix too long: %q (%d)", p, len(p))
	}
}

func TestAgentIngestTokenScopesAreMinimal(t *testing.T) {
	scopes := AgentIngestTokenScopes()
	if len(scopes) != 1 || scopes[0] != ScopeWriteClusters {
		t.Fatalf("scopes = %v, want exactly [%s]", scopes, ScopeWriteClusters)
	}
	// The scoped token must satisfy the ingest route's required scope...
	if !ScopeAllowsRequest(scopes, ScopeWriteClusters) {
		t.Error("scoped token should satisfy clusters:write")
	}
	// ...but grant nothing broader.
	if ScopeAllowsRequest(scopes, ScopeWriteRBAC) {
		t.Error("scoped token must NOT grant rbac:write")
	}
	if ScopeAllowsRequest(scopes, ScopeWriteProjects) {
		t.Error("scoped token must NOT grant projects:write")
	}
	// It is not a read-only set (it can mutate) but also not admin.
	if IsReadOnlyScopeSet(scopes) {
		t.Error("clusters:write set should not be read-only")
	}
}

// TestAgentIngestTokenIsScopedToOneCluster pins the per-cluster ingest
// identity: two connecting clusters must resolve to two distinct service
// principals, each holding exactly one cluster_role_binding for its own
// cluster. With a single shared owner the principal accumulates one binding per
// connected cluster, so any one agent's token authorizes ingest fleet-wide.
func TestAgentIngestTokenIsScopedToOneCluster(t *testing.T) {
	clusterA := uuid.New()
	clusterB := uuid.New()
	f := &fakeIngestQuerier{}

	if _, err := IssueAgentIngestToken(context.Background(), f, clusterA); err != nil {
		t.Fatalf("issue for cluster A: %v", err)
	}
	if _, err := IssueAgentIngestToken(context.Background(), f, clusterB); err != nil {
		t.Fatalf("issue for cluster B: %v", err)
	}

	userA, ok := f.users[AgentIngestServiceUsername(clusterA)]
	if !ok {
		t.Fatalf("no service user for cluster A; have %v", f.users)
	}
	userB, ok := f.users[AgentIngestServiceUsername(clusterB)]
	if !ok {
		t.Fatalf("no service user for cluster B; have %v", f.users)
	}
	if userA.ID == userB.ID {
		t.Fatalf("both clusters resolved to the same principal %v", userA.ID)
	}
	// The users table requires a unique email, so the placeholder must be
	// per-cluster too or the second CreateServiceUser would collide.
	if userA.Email == userB.Email {
		t.Fatalf("placeholder emails collide across clusters: %q", userA.Email)
	}
	if !strings.HasSuffix(userA.Email, ".invalid") {
		t.Errorf("placeholder email %q must stay non-routable", userA.Email)
	}

	// Cluster A's principal carries exactly one binding, for cluster A.
	var forA []sqlc.CreateClusterRoleBindingParams
	for _, b := range f.bindings {
		if b.UserID == (pgtype.UUID{Bytes: userA.ID, Valid: true}) {
			forA = append(forA, b)
		}
	}
	if len(forA) != 1 {
		t.Fatalf("cluster A principal has %d bindings, want 1", len(forA))
	}
	if forA[0].ClusterID != clusterA {
		t.Errorf("cluster A binding targets %v, want %v", forA[0].ClusterID, clusterA)
	}

	// And its token is owned by that principal, not the other cluster's.
	for _, tok := range f.tokens {
		if tok.Name == AgentIngestTokenName(clusterA) && tok.UserID != userA.ID {
			t.Errorf("cluster A token owned by %v, want %v", tok.UserID, userA.ID)
		}
	}
}

func TestAgentIngestTokenParamsPinsScope(t *testing.T) {
	userID := uuid.New()
	clusterID := uuid.New()
	p := AgentIngestTokenParams(userID, clusterID, "hash123", "astro_agent_in")

	if p.UserID != userID {
		t.Errorf("UserID = %v, want %v", p.UserID, userID)
	}
	if p.TokenHash != "hash123" || p.Prefix != "astro_agent_in" {
		t.Errorf("hash/prefix not carried through: %+v", p)
	}
	if !strings.Contains(p.Name, clusterID.String()) {
		t.Errorf("name %q should identify the cluster", p.Name)
	}
	if p.AllowedCidrs != "" {
		t.Errorf("AllowedCidrs = %q, want empty (no IP restriction)", p.AllowedCidrs)
	}
	// Scopes column must decode to exactly clusters:write — NOT the empty
	// legacy "no enforcement" set.
	var got []string
	if err := json.Unmarshal(p.Scopes, &got); err != nil {
		t.Fatalf("scopes JSON: %v", err)
	}
	if len(got) != 1 || got[0] != ScopeWriteClusters {
		t.Fatalf("persisted scopes = %v, want [%s]", got, ScopeWriteClusters)
	}
}
