// Package connectauth holds the shared agent CONNECT credential checks used by
// both the legacy hub tunnel (internal/tunnel) and the remotedialer path
// (internal/tunnel2). Keeping the A3 registration-token adoption gate and
// durable hashed-token acceptance in one place prevents auth drift between
// connect surfaces (SEC-R01).
package connectauth

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// Kind labels returned by Validate.
const (
	KindRegistration = "registration"
	KindAgent        = "agent"
)

// TokenLookup is the minimal DB surface needed to authorize a CONNECT
// credential. Implemented by tunnel.AgentTokenValidator / *sqlc.Queries.
type TokenLookup interface {
	GetRegistrationTokenByToken(ctx context.Context, token string) (sqlc.ClusterRegistrationToken, error)
	GetClusterAgentTokenByClusterID(ctx context.Context, clusterID uuid.UUID) (sqlc.ClusterAgentToken, error)
	GetClusterAgentTokenByToken(ctx context.Context, token string) (sqlc.ClusterAgentToken, error)
}

// Result is a successful Validate outcome. Side effects (mark-used, mint
// durable, rotate, touch, stamp adopted) remain the caller's responsibility —
// tunnel2 only needs accept/deny, while the hub performs the full exchange.
type Result struct {
	Kind              string
	RegistrationToken sqlc.ClusterRegistrationToken
	AgentToken        sqlc.ClusterAgentToken
}

// Validate checks a CONNECT credential against the requested cluster.
//
// Order matches hub A3:
//  1. Registration token: cluster match + post-adoption replay gate.
//  2. Durable agent token (hashed / plaintext / grace previous): cluster match.
//
// Does not mark the registration token used, mint a durable, rotate, or touch.
func Validate(ctx context.Context, v TokenLookup, clusterID uuid.UUID, token string) (Result, error) {
	if v == nil {
		return Result{}, fmt.Errorf("server misconfigured: no agent-token validator")
	}
	if token == "" {
		return Result{}, fmt.Errorf("authorization bearer token missing")
	}

	registrationToken, regErr := v.GetRegistrationTokenByToken(ctx, token)
	if regErr == nil {
		if registrationToken.ClusterID != clusterID {
			return Result{}, fmt.Errorf("registration token does not match cluster")
		}
		// M2/A3: a registration token created at/before the cluster's durable
		// adoption is spent — replaying it post-join must be denied. A token
		// created AFTER adoption is a deliberately re-minted (re-import) token
		// and is allowed. adopted_at NULL = pre-adoption join window -> allowed.
		// A revoked durable returns ErrNoRows -> gate skipped, also allowed.
		// Fail closed on any non-ErrNoRows error.
		existing, exErr := v.GetClusterAgentTokenByClusterID(ctx, clusterID)
		if exErr == nil && existing.AdoptedAt.Valid && !registrationToken.CreatedAt.After(existing.AdoptedAt.Time) {
			return Result{}, fmt.Errorf("registration token already redeemed; cluster has adopted its durable agent token")
		}
		if exErr != nil && !errors.Is(exErr, pgx.ErrNoRows) {
			return Result{}, fmt.Errorf("failed to verify adoption state: %w", exErr)
		}
		return Result{Kind: KindRegistration, RegistrationToken: registrationToken}, nil
	}

	agentToken, agentErr := v.GetClusterAgentTokenByToken(ctx, token)
	if agentErr == nil {
		if agentToken.ClusterID != clusterID {
			return Result{}, fmt.Errorf("agent token does not match cluster")
		}
		return Result{Kind: KindAgent, AgentToken: agentToken}, nil
	}

	return Result{}, fmt.Errorf("invalid registration token")
}

// TokenKindLabel maps the validator kind to the audited wire label
// ("registration" stays registration; "agent" becomes "durable").
func TokenKindLabel(kind string) string {
	if kind == KindRegistration {
		return "registration"
	}
	if kind == KindAgent {
		return "durable"
	}
	return "invalid"
}
