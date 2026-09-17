package auth

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPreparedSessionSignsReservedMetadata(t *testing.T) {
	m := MustNewJWTManager("session-metadata-test-secret", 60)
	type key struct{}
	ctx := context.WithValue(context.Background(), key{}, true)
	m.SetAccessTokenTTLProvider(func(got context.Context) time.Duration {
		if got.Value(key{}) != true {
			t.Fatal("request context missing")
		}
		return 7 * time.Minute
	})
	pair, err := m.PrepareTokenPairContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	userID := uuid.New()
	access, refresh, err := m.SignPreparedTokenPair(userID, pair)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{access, refresh} {
		claims, err := m.ValidateToken(raw)
		if err != nil {
			t.Fatal(err)
		}
		if claims.UserID != userID {
			t.Fatal("wrong user")
		}
		if claims.SessionFamilyID != pair.FamilyID() || claims.Subject != userID.String() {
			t.Fatal("session family or subject changed at signing")
		}
		if claims.TokenType == AccessToken {
			if claims.ID != pair.AccessID() || claims.ExpiresAt.Sub(claims.IssuedAt.Time) != 7*time.Minute {
				t.Fatal("access metadata changed at signing")
			}
		} else if !claims.ExpiresAt.Equal(pair.RefreshExpiry()) {
			t.Fatal("refresh metadata changed at signing")
		}
	}
}

func TestPreparedSessionRejectsInvalidOrExpiredMetadata(t *testing.T) {
	m := MustNewJWTManager("session-metadata-test-secret", 60)
	pair, err := m.PrepareTokenPairContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	pair.accessExpiry = time.Now().Add(-time.Second)
	for _, invalid := range []PreparedTokenPair{{}, pair} {
		access, refresh, err := m.SignPreparedTokenPair(uuid.New(), invalid)
		if err == nil || access != "" || refresh != "" {
			t.Fatal("signed invalid session")
		}
	}
}
