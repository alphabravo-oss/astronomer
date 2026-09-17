package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

const (
	refreshRotationRotated = "rotated"
	refreshRotationReused  = "reused"
	refreshRotationRevoked = "revoked"
	refreshRotationInvalid = "invalid"
)

func hashRefreshJTI(jti string) []byte {
	digest := sha256.Sum256([]byte(jti))
	return digest[:]
}

func hashRefreshFamily(familyID uuid.UUID) []byte {
	digest := sha256.Sum256(familyID[:])
	return digest[:]
}

func refreshFamilyHashString(familyID uuid.UUID) string {
	return hex.EncodeToString(hashRefreshFamily(familyID))
}

func createRefreshSession(ctx context.Context, q interface {
	CreateRefreshSession(context.Context, sqlc.CreateRefreshSessionParams) error
}, userID uuid.UUID, pair auth.PreparedTokenPair) error {
	return q.CreateRefreshSession(ctx, sqlc.CreateRefreshSessionParams{
		JtiHash: hashRefreshJTI(pair.RefreshID()), FamilyHash: hashRefreshFamily(pair.FamilyID()),
		UserID: userID, IssuedAt: pair.IssuedAt(), ExpiresAt: pair.RefreshExpiry(),
	})
}

func rotateRefreshSession(ctx context.Context, q interface {
	RotateRefreshSession(context.Context, sqlc.RotateRefreshSessionParams) (string, error)
}, claims *auth.Claims, pair auth.PreparedTokenPair, now time.Time) (string, error) {
	return q.RotateRefreshSession(ctx, sqlc.RotateRefreshSessionParams{
		PreviousJtiHash: hashRefreshJTI(claims.ID), FamilyHash: hashRefreshFamily(claims.SessionFamilyID),
		UserID: claims.UserID, RotatedAt: pgtype.Timestamptz{Time: now, Valid: true},
		NextJtiHash: hashRefreshJTI(pair.RefreshID()), NextExpiresAt: pair.RefreshExpiry(),
	})
}
