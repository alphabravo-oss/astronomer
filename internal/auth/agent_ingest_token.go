package auth

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
)

// AgentIngestTokenPrefix is the human-recognisable prefix on the scoped
// outbound API token issued to a per-cluster agent for the apiserver-audit
// ingest endpoint (PATH A). Lets an operator eyeball a leaked secret in a log
// and know it is an agent ingest credential, not a user PAT.
const AgentIngestTokenPrefix = "astro_agent_ingest_"

// GenerateAgentIngestToken mints a fresh scoped ingest token for an agent. The
// plaintext is returned to the caller once at issuance time; only the SHA-256
// hash (HashAgentIngestToken) is persisted, sharing the opaque-token contract
// used by the SCIM / argocd-proxy tokens so a DB compromise yields no usable
// credential.
func GenerateAgentIngestToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate agent ingest token: %w", err)
	}
	return AgentIngestTokenPrefix + base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

// HashAgentIngestToken returns the stored hash form of an agent ingest token.
func HashAgentIngestToken(token string) string {
	return HashOpaqueToken(token)
}

// AgentIngestTokenDisplayPrefix returns the leading slice stored in the row's
// `prefix` column so the operator UI can show the token family without holding
// the secret. The prefix column is VARCHAR(16); keep within that bound.
func AgentIngestTokenDisplayPrefix(token string) string {
	token = strings.TrimSpace(token)
	if len(token) <= 16 {
		return token
	}
	return token[:16]
}

// AgentIngestTokenScopes is the minimal scope set an agent ingest token needs:
// just clusters:write, which is exactly what the apiserver-audit ingest route
// requires (see registerSecurityRoutes — requireScope(ScopeWriteClusters)).
// The token carries no read or admin scope, so it cannot be used to enumerate
// other resources if leaked.
func AgentIngestTokenScopes() []string {
	return []string{ScopeWriteClusters}
}

// AgentIngestTokenParams builds the CreateAPIToken params for a scoped agent
// ingest token. The scopes column is pinned to AgentIngestTokenScopes (NOT the
// empty/legacy "no enforcement" set) so the token is hard-limited to
// clusters:write. tokenHash and prefix come from a freshly generated plaintext
// (GenerateAgentIngestToken + HashAgentIngestToken + AgentIngestTokenDisplayPrefix).
//
// The token is associated with serviceUserID — the caller supplies the user
// whose RBAC bindings grant clusters:update on the target cluster, because the
// ingest route also gates on that permission. Naming the params after the
// cluster keeps the row identifiable in the operator UI.
func AgentIngestTokenParams(serviceUserID, clusterID uuid.UUID, tokenHash, prefix string) sqlc.CreateAPITokenParams {
	// json.Marshal of a non-nil string slice never fails; ignore the error to
	// keep the helper allocation-only and side-effect-free.
	scopes, _ := json.Marshal(AgentIngestTokenScopes())
	return sqlc.CreateAPITokenParams{
		UserID:    serviceUserID,
		Name:      "agent-ingest-" + clusterID.String(),
		TokenHash: tokenHash,
		Prefix:    prefix,
		Scopes:    scopes,
		// No IP allowlist: the agent's source IP isn't known at issuance time
		// and varies across NAT / pod restarts. Scope + RBAC are the controls.
		AllowedCidrs: "",
	}
}
