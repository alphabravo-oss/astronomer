package charlie

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
)

const (
	delegationPrefix = "astro_charlie_auth_"
	minDelegationTTL = 30 * time.Second
	maxDelegationTTL = 15 * time.Minute
)

type delegationQuerier interface {
	CreateCharlieDelegation(context.Context, sqlc.CreateCharlieDelegationParams) (sqlc.CharlieDelegation, error)
	GetActiveCharlieDelegationByHash(context.Context, string) (sqlc.CharlieDelegation, error)
}

type IssuedDelegation struct {
	Reference string
	ExpiresAt time.Time
}

// IssueDelegation returns an opaque reference once and stores only its hash.
// It contains no RBAC snapshot; the MCP boundary must call ValidateDelegation
// and then perform a live exact-target permission check for every invocation.
func IssueDelegation(ctx context.Context, q delegationQuerier, sessionID, principalID uuid.UUID, principalType string, ttl time.Duration, now time.Time) (IssuedDelegation, error) {
	if q == nil {
		return IssuedDelegation{}, fmt.Errorf("Charlie delegation store is unavailable")
	}
	if sessionID == uuid.Nil || principalID == uuid.Nil {
		return IssuedDelegation{}, fmt.Errorf("Charlie delegation requires session and principal")
	}
	if principalType != "user" && principalType != "service" {
		return IssuedDelegation{}, fmt.Errorf("unsupported Charlie delegation principal type")
	}
	if ttl < minDelegationTTL || ttl > maxDelegationTTL {
		return IssuedDelegation{}, fmt.Errorf("Charlie delegation TTL must be between %s and %s", minDelegationTTL, maxDelegationTTL)
	}

	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return IssuedDelegation{}, fmt.Errorf("generate Charlie delegation: %w", err)
	}
	plaintext := delegationPrefix + base64.RawURLEncoding.EncodeToString(random[:])
	expiresAt := now.UTC().Add(ttl)
	if _, err := q.CreateCharlieDelegation(ctx, sqlc.CreateCharlieDelegationParams{
		SessionID:           sessionID,
		AuthorizationHash:   HashDelegation(plaintext),
		AuthorizationPrefix: displayDelegationPrefix(plaintext),
		PrincipalType:       principalType,
		PrincipalID:         principalID,
		ExpiresAt:           expiresAt,
	}); err != nil {
		return IssuedDelegation{}, fmt.Errorf("persist Charlie delegation: %w", err)
	}
	return IssuedDelegation{Reference: plaintext, ExpiresAt: expiresAt}, nil
}

func HashDelegation(reference string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(reference)))
	return hex.EncodeToString(digest[:])
}

func displayDelegationPrefix(reference string) string {
	reference = strings.TrimSpace(reference)
	if len(reference) <= 16 {
		return reference
	}
	return reference[:16]
}

type DelegationExpectation struct {
	SessionID     uuid.UUID
	PrincipalID   uuid.UUID
	PrincipalType string
}

// ValidateDelegation resolves only the hash and exact binding. Callers must
// next evaluate live product RBAC for the exact capability/resource/arguments;
// successful resolution is identity correlation, not action permission.
func ValidateDelegation(ctx context.Context, q delegationQuerier, reference string, expected DelegationExpectation) (sqlc.CharlieDelegation, error) {
	if q == nil || !strings.HasPrefix(reference, delegationPrefix) {
		return sqlc.CharlieDelegation{}, fmt.Errorf("Charlie authorization reference is invalid")
	}
	delegation, err := q.GetActiveCharlieDelegationByHash(ctx, HashDelegation(reference))
	if err != nil {
		return sqlc.CharlieDelegation{}, fmt.Errorf("Charlie authorization reference is inactive")
	}
	if delegation.SessionID != expected.SessionID || delegation.PrincipalID != expected.PrincipalID || delegation.PrincipalType != expected.PrincipalType {
		return sqlc.CharlieDelegation{}, fmt.Errorf("Charlie authorization reference binding changed")
	}
	return delegation, nil
}
