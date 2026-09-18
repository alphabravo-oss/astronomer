package sqlc

import (
	"context"
	"crypto/sha256"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRefreshSessionConcurrentRotationAndReplayRevokesFamily(t *testing.T) {
	dsn := os.Getenv("REFRESH_SESSION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("REFRESH_SESSION_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	userID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users (id,email,username) VALUES ($1,$2,$3)`, userID, userID.String()+"@example.test", "refresh-"+userID.String()); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)

	hash := func(value string) []byte {
		digest := sha256.Sum256([]byte(value))
		return digest[:]
	}
	familyHash := hash(uuid.NewString())
	initialJTI := hash(uuid.NewString())
	now := time.Now().UTC().Truncate(time.Microsecond)
	expires := now.Add(time.Hour)
	queries := New(pool)
	if err := queries.CreateRefreshSession(ctx, CreateRefreshSessionParams{
		JtiHash: initialJTI, FamilyHash: familyHash, UserID: userID, IssuedAt: now, ExpiresAt: expires,
	}); err != nil {
		t.Fatal(err)
	}

	type result struct {
		status string
		next   []byte
		err    error
	}
	results := make(chan result, 2)
	var start sync.WaitGroup
	start.Add(1)
	for range 2 {
		next := hash(uuid.NewString())
		go func() {
			start.Wait()
			status, rotateErr := New(pool).RotateRefreshSession(ctx, RotateRefreshSessionParams{
				PreviousJtiHash: initialJTI, FamilyHash: familyHash, UserID: userID,
				RotatedAt:   pgtype.Timestamptz{Time: now.Add(time.Second), Valid: true},
				NextJtiHash: next, NextExpiresAt: expires,
			})
			results <- result{status: status, next: next, err: rotateErr}
		}()
	}
	start.Done()

	statuses := make([]string, 0, 2)
	var rotatedJTI []byte
	for range 2 {
		got := <-results
		if got.err != nil {
			t.Fatal(got.err)
		}
		statuses = append(statuses, got.status)
		if got.status == "rotated" {
			rotatedJTI = got.next
		}
	}
	sort.Strings(statuses)
	if statuses[0] != "reused" || statuses[1] != "rotated" {
		t.Fatalf("concurrent rotation statuses = %v, want [reused rotated]", statuses)
	}
	revoked, err := queries.IsRefreshSessionFamilyRevoked(ctx, familyHash)
	if err != nil || !revoked {
		t.Fatalf("family revoked = %v, err=%v", revoked, err)
	}
	status, err := queries.RotateRefreshSession(ctx, RotateRefreshSessionParams{
		PreviousJtiHash: rotatedJTI, FamilyHash: familyHash, UserID: userID,
		RotatedAt:   pgtype.Timestamptz{Time: now.Add(2 * time.Second), Valid: true},
		NextJtiHash: hash(uuid.NewString()), NextExpiresAt: expires,
	})
	if err != nil || status != "revoked" {
		t.Fatalf("replay-after-rotation status = %q, err=%v, want revoked", status, err)
	}
}
