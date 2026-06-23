package auth

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
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
