package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// AgentIngestServiceUsername is the username of the single reserved service
// principal that owns every per-cluster apiserver-audit ingest token. It is
// flagged is_service=true (migration 116) so it never appears on human-user
// surfaces. The "system:" prefix mirrors Kubernetes' reserved-identity
// convention and the colon keeps it from colliding with an operator-chosen
// username (which the UI disallows).
const AgentIngestServiceUsername = "system:agent-ingest"

// AgentIngestServiceEmail is a non-routable placeholder email for the reserved
// service user. The users table requires a unique non-null email; .invalid is
// reserved by RFC 2606 so it can never be a real deliverable address.
const AgentIngestServiceEmail = "system+agent-ingest@astronomer.invalid"

// AgentIngestClusterRoleName is the single reserved cluster role granting
// exactly clusters:update. The service user is bound to it per-cluster via a
// cluster-scoped cluster_role_binding, so the grant is narrowed to one cluster
// even though the role definition is shared.
const AgentIngestClusterRoleName = "system:agent-ingest"

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
		Name:      AgentIngestTokenName(clusterID),
		TokenHash: tokenHash,
		Prefix:    prefix,
		Scopes:    scopes,
		// No IP allowlist: the agent's source IP isn't known at issuance time
		// and varies across NAT / pod restarts. Scope + RBAC are the controls.
		AllowedCidrs: "",
	}
}

// AgentIngestTokenName is the api_tokens.name for a cluster's ingest token. It
// is stable per cluster so RevokeAPITokensByName can find and revoke the prior
// token before a fresh one is minted (mint-fresh / revoke-old: the plaintext is
// never stored, so a prior token can't be re-delivered on reconnect).
func AgentIngestTokenName(clusterID uuid.UUID) string {
	return "agent-ingest-" + clusterID.String()
}

// agentIngestClusterRoleRules is the role definition body granting exactly
// clusters:update — the verb the apiserver-audit ingest route gates on
// (requirePermission(ResourceClusters, VerbUpdate)). Nothing else.
var agentIngestClusterRoleRules = []map[string]any{
	{"resource": "clusters", "verbs": []string{"update"}},
}

// AgentIngestQuerier is the narrow slice of sqlc.Queries that
// IssueAgentIngestToken needs to provision the service identity, its
// cluster-scoped grant, and the scoped token. Declared as an interface so the
// tunnel handshake can depend on the behaviour (and tests can fake it) without
// the full Queries surface.
type AgentIngestQuerier interface {
	GetUserByUsername(ctx context.Context, username string) (sqlc.User, error)
	CreateServiceUser(ctx context.Context, arg sqlc.CreateServiceUserParams) (sqlc.User, error)
	GetClusterRoleByName(ctx context.Context, name string) (sqlc.ClusterRole, error)
	CreateClusterRole(ctx context.Context, arg sqlc.CreateClusterRoleParams) (sqlc.ClusterRole, error)
	CountClusterRoleBindingForUserCluster(ctx context.Context, arg sqlc.CountClusterRoleBindingForUserClusterParams) (int64, error)
	CreateClusterRoleBinding(ctx context.Context, arg sqlc.CreateClusterRoleBindingParams) (sqlc.ClusterRoleBinding, error)
	RevokeAPITokensByName(ctx context.Context, arg sqlc.RevokeAPITokensByNameParams) error
	CreateAPIToken(ctx context.Context, arg sqlc.CreateAPITokenParams) (sqlc.ApiToken, error)
}

// IssueAgentIngestToken provisions (idempotently) the reserved service
// principal, the shared cluster:update role, and a cluster-scoped binding for
// the connecting cluster, then mints a fresh scoped clusters:write ingest token
// and returns its plaintext. Any previously-issued token for this cluster is
// revoked first so at most one valid token exists per cluster (the plaintext is
// never stored, so it can't be re-delivered — re-mint is the only safe reuse).
//
// The returned plaintext is delivered once in CONNECT_ACK; only its SHA-256
// hash is persisted.
func IssueAgentIngestToken(ctx context.Context, q AgentIngestQuerier, clusterID uuid.UUID) (string, error) {
	user, err := ensureAgentIngestServiceUser(ctx, q)
	if err != nil {
		return "", fmt.Errorf("ensure ingest service user: %w", err)
	}
	role, err := ensureAgentIngestClusterRole(ctx, q)
	if err != nil {
		return "", fmt.Errorf("ensure ingest cluster role: %w", err)
	}
	if err := ensureAgentIngestBinding(ctx, q, user.ID, role.ID, clusterID); err != nil {
		return "", fmt.Errorf("ensure ingest binding: %w", err)
	}

	// Revoke any prior token for this cluster before minting a fresh one so the
	// fleet never accumulates live ingest credentials.
	if err := q.RevokeAPITokensByName(ctx, sqlc.RevokeAPITokensByNameParams{
		UserID: user.ID,
		Name:   AgentIngestTokenName(clusterID),
	}); err != nil {
		return "", fmt.Errorf("revoke prior ingest tokens: %w", err)
	}

	plaintext, err := GenerateAgentIngestToken()
	if err != nil {
		return "", err
	}
	params := AgentIngestTokenParams(user.ID, clusterID, HashAgentIngestToken(plaintext), AgentIngestTokenDisplayPrefix(plaintext))
	if _, err := q.CreateAPIToken(ctx, params); err != nil {
		return "", fmt.Errorf("create ingest token: %w", err)
	}
	return plaintext, nil
}

// IngestIssuer adapts an AgentIngestQuerier to the tunnel Hub's
// AuditIngestIssuer interface (IssueIngestToken). Wired at startup over
// *sqlc.Queries.
type IngestIssuer struct {
	q AgentIngestQuerier
}

// NewIngestIssuer builds an IngestIssuer over the given querier. Returns nil
// when q is nil so the nil-safe Hub setter leaves PATH A issuance disabled.
func NewIngestIssuer(q AgentIngestQuerier) *IngestIssuer {
	if q == nil {
		return nil
	}
	return &IngestIssuer{q: q}
}

// IssueIngestToken mints (or re-mints) the scoped ingest token for clusterID.
func (i *IngestIssuer) IssueIngestToken(ctx context.Context, clusterID uuid.UUID) (string, error) {
	return IssueAgentIngestToken(ctx, i.q, clusterID)
}

func ensureAgentIngestServiceUser(ctx context.Context, q AgentIngestQuerier) (sqlc.User, error) {
	if user, err := q.GetUserByUsername(ctx, AgentIngestServiceUsername); err == nil {
		return user, nil
	}
	// Not found (or transient): CreateServiceUser is ON CONFLICT (username) DO
	// UPDATE, so a concurrent connect that already inserted the row is handled.
	return q.CreateServiceUser(ctx, sqlc.CreateServiceUserParams{
		Email:    AgentIngestServiceEmail,
		Username: AgentIngestServiceUsername,
	})
}

func ensureAgentIngestClusterRole(ctx context.Context, q AgentIngestQuerier) (sqlc.ClusterRole, error) {
	if role, err := q.GetClusterRoleByName(ctx, AgentIngestClusterRoleName); err == nil {
		return role, nil
	}
	rules, _ := json.Marshal(agentIngestClusterRoleRules)
	role, err := q.CreateClusterRole(ctx, sqlc.CreateClusterRoleParams{
		Name:        AgentIngestClusterRoleName,
		DisplayName: "Agent Audit Ingest",
		Description: "Reserved role granting clusters:update for per-cluster apiserver-audit ingest tokens.",
		Permissions: json.RawMessage(`[]`),
		Rules:       rules,
		IsBuiltin:   true,
	})
	if err != nil {
		// Lost a create race: another connect inserted it first. Re-read.
		if existing, getErr := q.GetClusterRoleByName(ctx, AgentIngestClusterRoleName); getErr == nil {
			return existing, nil
		}
		return sqlc.ClusterRole{}, err
	}
	return role, nil
}

func ensureAgentIngestBinding(ctx context.Context, q AgentIngestQuerier, userID, roleID, clusterID uuid.UUID) error {
	pgUser := pgtype.UUID{Bytes: userID, Valid: true}
	count, err := q.CountClusterRoleBindingForUserCluster(ctx, sqlc.CountClusterRoleBindingForUserClusterParams{
		UserID:    pgUser,
		ClusterID: clusterID,
		RoleID:    roleID,
	})
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	_, err = q.CreateClusterRoleBinding(ctx, sqlc.CreateClusterRoleBindingParams{
		UserID:    pgUser,
		Group:     "",
		RoleID:    roleID,
		ClusterID: clusterID,
		Namespace: "",
	})
	return err
}
