package tunnel

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// A3 closes M2: a registration token is single-use-by-adoption. Once the
// cluster has adopted its durable agent token, replaying the (older)
// registration token must be denied at CONNECT. These tests drive
// validateAndMaybeRotateToken directly with a recording fake.

func ts(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

// M2-replay-denied: reg token created BEFORE adoption, durable adopted +
// non-revoked -> deny, no mint, no mark-used.
func TestA3RegistrationReplayDeniedAfterAdoption(t *testing.T) {
	clusterID := uuid.New()
	adoptedAt := time.Now().Add(-1 * time.Hour)
	v := &recordingValidator{
		tokenClusterID:    clusterID.String(),
		regTokenCreatedAt: adoptedAt.Add(-30 * time.Minute), // pre-adoption: the leaked original
		byClusterIDForce: &sqlc.ClusterAgentToken{
			ID: uuid.New(), ClusterID: clusterID, TokenHash: auth.HashOpaqueToken("durable"),
			AdoptedAt: ts(adoptedAt),
		},
	}
	h := NewHubWithValidator(slog.Default(), v)

	kind, durable, err := h.validateAndMaybeRotateToken(context.Background(), clusterID,
		protocol.ConnectPayload{ClusterID: clusterID.String(), Token: "leaked-reg-token"})
	if err == nil {
		t.Fatal("expected replay of a spent registration token to be denied")
	}
	if kind != "" || durable != "" {
		t.Fatalf("denied replay must mint/return nothing, got kind=%q durable=%q", kind, durable)
	}
	if len(v.markedRegIDs) != 0 {
		t.Fatalf("MarkRegistrationTokenUsed must not be reached on deny, got %d", len(v.markedRegIDs))
	}
	if len(v.SnapshotAgentTokenUpserts()) != 0 {
		t.Fatal("no durable upsert may occur on a denied replay")
	}
}

// join-window-retry-ok: durable exists but NOT yet adopted (adopted_at NULL) ->
// reg token still bootstraps (re-delivers the same durable), no error.
func TestA3JoinWindowRetryBeforeAdoptionAllowed(t *testing.T) {
	clusterID := uuid.New()
	v := &recordingValidator{
		tokenClusterID:    clusterID.String(),
		regTokenCreatedAt: time.Now().Add(-2 * time.Minute),
		byClusterIDForce: &sqlc.ClusterAgentToken{
			ID: uuid.New(), ClusterID: clusterID, Token: "durable-plain",
			TokenHash: auth.HashOpaqueToken("durable-plain"), // adopted_at zero/NULL
		},
	}
	h := NewHubWithValidator(slog.Default(), v)

	kind, durable, err := h.validateAndMaybeRotateToken(context.Background(), clusterID,
		protocol.ConnectPayload{ClusterID: clusterID.String(), Token: "reg-token"})
	if err != nil {
		t.Fatalf("pre-adoption join-window retry must succeed, got %v", err)
	}
	if kind != "registration" {
		t.Fatalf("expected registration kind, got %q", kind)
	}
	if durable != "durable-plain" {
		t.Fatalf("expected the existing durable re-delivered, got %q", durable)
	}
}

// crash-before-persist: durable row exists, adopted_at NULL (agent crashed
// before re-presenting it); re-presenting the registration token re-delivers a
// durable with no error / no lockout.
func TestA3CrashBeforePersistRedelivers(t *testing.T) {
	clusterID := uuid.New()
	v := &recordingValidator{
		tokenClusterID:    clusterID.String(),
		regTokenCreatedAt: time.Now().Add(-90 * time.Second),
		byClusterIDForce: &sqlc.ClusterAgentToken{
			ID: uuid.New(), ClusterID: clusterID, Token: "durable-x",
			TokenHash: auth.HashOpaqueToken("durable-x"),
		},
	}
	h := NewHubWithValidator(slog.Default(), v)
	if _, durable, err := h.validateAndMaybeRotateToken(context.Background(), clusterID,
		protocol.ConnectPayload{ClusterID: clusterID.String(), Token: "reg-token"}); err != nil || durable == "" {
		t.Fatalf("crash-before-persist replay must re-deliver a durable, got durable=%q err=%v", durable, err)
	}
}

// re-import-ok: durable adopted at T2, a re-minted reg token created at T3>T2 is
// ALLOWED (re-import intent); the hash-only durable is rotated via Upsert which
// resets adopted_at=NULL.
func TestA3ReimportAfterAdoptionAllowed(t *testing.T) {
	clusterID := uuid.New()
	adoptedAt := time.Now().Add(-1 * time.Hour)
	v := &recordingValidator{
		tokenClusterID:    clusterID.String(),
		regTokenCreatedAt: adoptedAt.Add(30 * time.Minute), // post-adoption: deliberate re-mint
		byClusterIDForce: &sqlc.ClusterAgentToken{
			ID: uuid.New(), ClusterID: clusterID, TokenHash: auth.HashOpaqueToken("old-durable"),
			AdoptedAt: ts(adoptedAt), // hash-only (Token=="") -> ensureClusterAgentToken rotates
		},
	}
	h := NewHubWithValidator(slog.Default(), v)

	kind, durable, err := h.validateAndMaybeRotateToken(context.Background(), clusterID,
		protocol.ConnectPayload{ClusterID: clusterID.String(), Token: "reimport-reg-token"})
	if err != nil {
		t.Fatalf("re-import (reg token created after adoption) must be allowed, got %v", err)
	}
	if kind != "registration" || durable == "" {
		t.Fatalf("re-import must mint/return a fresh durable, got kind=%q durable=%q", kind, durable)
	}
	if len(v.markedRegIDs) != 1 {
		t.Fatalf("re-import must mark the registration token used once, got %d", len(v.markedRegIDs))
	}
	ups := v.SnapshotAgentTokenUpserts()
	if len(ups) != 1 {
		t.Fatalf("re-import must rotate the hash-only durable via one Upsert, got %d", len(ups))
	}
}

// adopted-reset-on-reimport: the UpsertClusterAgentToken contract resets
// adopted_at to NULL on the new generation (guards M2 staying closed). The
// reset lives in the ON CONFLICT clause; assert the query source carries it so
// a future edit can't silently drop it and leave M2 re-openable.
func TestA3UpsertResetsAdoptedAt(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "db", "queries", "clusters.sql"))
	if err != nil {
		t.Fatalf("read clusters.sql: %v", err)
	}
	src := string(b)
	idx := strings.Index(src, "name: UpsertClusterAgentToken")
	if idx < 0 {
		t.Fatal("UpsertClusterAgentToken query not found")
	}
	end := strings.Index(src[idx:], "RETURNING *;")
	if end < 0 {
		t.Fatal("UpsertClusterAgentToken RETURNING not found")
	}
	block := src[idx : idx+end]
	if !strings.Contains(block, "adopted_at = NULL") {
		t.Fatal("UpsertClusterAgentToken ON CONFLICT must reset adopted_at = NULL so the A3 gate anchor advances on re-import")
	}
}

// fail-closed: a non-ErrNoRows error from GetClusterAgentTokenByClusterID must
// deny (not fall through to mint a durable).
func TestA3FailClosedOnAdoptionLookupError(t *testing.T) {
	clusterID := uuid.New()
	v := &recordingValidator{
		tokenClusterID:      clusterID.String(),
		byClusterIDErrForce: context.DeadlineExceeded, // any non-ErrNoRows error
	}
	h := NewHubWithValidator(slog.Default(), v)
	_, durable, err := h.validateAndMaybeRotateToken(context.Background(), clusterID,
		protocol.ConnectPayload{ClusterID: clusterID.String(), Token: "reg-token"})
	if err == nil {
		t.Fatal("a DB error verifying adoption state must fail closed (deny)")
	}
	if durable != "" {
		t.Fatalf("fail-closed must not return a durable, got %q", durable)
	}
	if len(v.markedRegIDs) != 0 {
		t.Fatal("fail-closed must not mark the token used")
	}
}

// revoked-escape-hatch: a revoked durable returns ErrNoRows from
// GetClusterAgentTokenByClusterID -> the gate is skipped, reg token bootstraps.
func TestA3RevokedDurableLetsRegistrationBootstrap(t *testing.T) {
	clusterID := uuid.New()
	v := &recordingValidator{
		tokenClusterID:      clusterID.String(),
		regTokenCreatedAt:   time.Now().Add(-5 * time.Minute),
		byClusterIDErrForce: pgx.ErrNoRows, // revoked rows are filtered out by the query
	}
	h := NewHubWithValidator(slog.Default(), v)
	kind, durable, err := h.validateAndMaybeRotateToken(context.Background(), clusterID,
		protocol.ConnectPayload{ClusterID: clusterID.String(), Token: "reg-token"})
	if err != nil {
		t.Fatalf("a revoked durable must let a registration token re-bootstrap, got %v", err)
	}
	if kind != "registration" || durable == "" {
		t.Fatalf("expected a freshly minted durable, got kind=%q durable=%q", kind, durable)
	}
}

// default-path-unchanged: no durable at all (pgx.ErrNoRows) -> reg token mints
// the first durable; a subsequent durable CONNECT stamps adopted_at.
func TestA3DefaultHappyPathJoin(t *testing.T) {
	clusterID := uuid.New()
	v := &recordingValidator{
		tokenClusterID:    clusterID.String(),
		regTokenCreatedAt: time.Now(),
	}
	h := NewHubWithValidator(slog.Default(), v)

	kind, durable, err := h.validateAndMaybeRotateToken(context.Background(), clusterID,
		protocol.ConnectPayload{ClusterID: clusterID.String(), Token: "reg-token"})
	if err != nil || kind != "registration" || durable == "" {
		t.Fatalf("default no-CA join must bootstrap a durable, got kind=%q durable=%q err=%v", kind, durable, err)
	}

	// The agent now presents the durable; adoption must be stamped exactly once.
	v.clusterAgentToken = sqlc.ClusterAgentToken{ID: uuid.New(), ClusterID: clusterID, Token: durable, TokenHash: auth.HashOpaqueToken(durable)}
	v.tokenErr = pgx.ErrNoRows // reg-token lookup now misses; durable path taken
	if _, _, err := h.validateAndMaybeRotateToken(context.Background(), clusterID,
		protocol.ConnectPayload{ClusterID: clusterID.String(), Token: durable}); err != nil {
		t.Fatalf("durable CONNECT must succeed, got %v", err)
	}
	v.mu.Lock()
	adopted := len(v.adoptedAgentIDs)
	v.mu.Unlock()
	if adopted != 1 {
		t.Fatalf("durable CONNECT must stamp adoption once, got %d", adopted)
	}
}
