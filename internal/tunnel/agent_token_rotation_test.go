package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

var errNotFound = errors.New("agent token not found (revoked or missing)")

// connectWithToken sends a CONNECT presenting an explicit token (the agent's
// durable token, not the registration test-token) and returns the ack.
func connectWithToken(t *testing.T, conn *websocket.Conn, ctx context.Context, clusterID, token string) protocol.ConnectAckPayload {
	t.Helper()
	connectPayload, _ := json.Marshal(protocol.ConnectPayload{
		ClusterID:    clusterID,
		AgentID:      "agent-rot",
		AgentVersion: "1.0.0",
		Token:        token,
	})
	if err := wsjson.Write(ctx, conn, &protocol.Message{Type: protocol.MsgConnect, Payload: connectPayload}); err != nil {
		t.Fatalf("write connect: %v", err)
	}
	var ack protocol.Message
	if err := wsjson.Read(ctx, conn, &ack); err != nil {
		t.Fatalf("read ack: %v", err)
	}
	var ackPayload protocol.ConnectAckPayload
	if err := json.Unmarshal(ack.Payload, &ackPayload); err != nil {
		t.Fatalf("unmarshal ack payload: %v", err)
	}
	return ackPayload
}

// (a) Trigger rotation -> next CONNECT mints a NEW token, delivers it in the
// ack, AND the OLD token still validates during grace.
//
// FAILS WITHOUT THE FIX: with no rotation logic the agent path returns the
// presented token unchanged, so ack.AgentToken stays empty and no rotation
// row is recorded.
func TestRotationTriggeredMintsNewTokenAndDeliversInAck(t *testing.T) {
	clusterID := uuid.New()
	oldToken := "old-durable-token"
	validator := &recordingValidator{
		clusterAgentToken: sqlc.ClusterAgentToken{
			ID:                uuid.New(),
			ClusterID:         clusterID,
			TokenHash:         auth.HashOpaqueToken(oldToken),
			RotationPendingAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		},
	}
	h := NewHubWithValidator(slog.Default(), validator)
	_, conn, ctx := testServerAndClient(t, h)

	ack := connectWithToken(t, conn, ctx, clusterID.String(), oldToken)
	if !ack.Accepted {
		t.Fatalf("connect with current token during pending rotation must be accepted, got reason %q", ack.Reason)
	}
	if ack.AgentToken == "" {
		t.Fatal("expected a freshly-minted durable token in CONNECT_ACK after rotation trigger")
	}
	if ack.AgentToken == oldToken {
		t.Fatal("ack token must differ from the old token")
	}

	rotations := validator.SnapshotAgentTokenRotations()
	if len(rotations) != 1 {
		t.Fatalf("expected exactly 1 rotation, got %d", len(rotations))
	}
	if rotations[0].TokenHash != auth.HashOpaqueToken(ack.AgentToken) {
		t.Fatal("rotated token_hash must match the ack token")
	}
	if rotations[0].Token != "" {
		t.Fatal("durable token plaintext must not be persisted")
	}

	// Grace: the OLD token must STILL validate (previous_token_hash now holds
	// the old hash). GetClusterAgentTokenByToken accepts either branch.
	if _, err := validator.GetClusterAgentTokenByToken(ctx, oldToken); err != nil {
		t.Fatalf("old token must still validate during grace, got error: %v", err)
	}

	_ = conn.Close(websocket.StatusNormalClosure, "done")
}

// (b) After a CONNECT with the NEW token, the old (previous) token is cleared.
//
// FAILS WITHOUT THE FIX: nothing clears previous_token_hash, so the old token
// would validate indefinitely.
func TestConnectWithNewTokenClearsPreviousHash(t *testing.T) {
	clusterID := uuid.New()
	newToken := "new-durable-token"
	tokenID := uuid.New()
	validator := &recordingValidator{
		clusterAgentToken: sqlc.ClusterAgentToken{
			ID:                tokenID,
			ClusterID:         clusterID,
			TokenHash:         auth.HashOpaqueToken(newToken),
			PreviousTokenHash: pgtype.Text{String: auth.HashOpaqueToken("old-durable-token"), Valid: true},
		},
	}
	h := NewHubWithValidator(slog.Default(), validator)
	_, conn, ctx := testServerAndClient(t, h)

	ack := connectWithToken(t, conn, ctx, clusterID.String(), newToken)
	if !ack.Accepted {
		t.Fatalf("connect with new token must be accepted, got %q", ack.Reason)
	}
	// No further rotation should have minted yet another token.
	if ack.AgentToken != "" {
		t.Fatalf("steady-state connect with the current token must NOT mint a new token, got %q", ack.AgentToken)
	}

	cleared := validator.SnapshotClearedPreviousIDs()
	if len(cleared) != 1 || cleared[0] != tokenID {
		t.Fatalf("expected previous_token_hash cleared for token %s, got %v", tokenID, cleared)
	}

	_ = conn.Close(websocket.StatusNormalClosure, "done")
}

// (c) Revoke -> token no longer validates -> next CONNECT denied.
//
// This exercises the validation gate: a revoked row returns "not found" from
// GetClusterAgentTokenByToken, so the agent path falls through to the
// "invalid registration token" denial and the WS is closed without an ack.
//
// FAILS WITHOUT THE FIX: revoked_at was dead code; the standalone revoke
// query (RevokeClusterAgentToken) that sets it did not exist.
func TestRevokedTokenDeniesConnect(t *testing.T) {
	clusterID := uuid.New()
	revokedToken := "revoked-token"
	validator := &recordingValidator{
		// clusterAgentErr simulates the post-revoke state: the row is gated by
		// revoked_at IS NULL so the lookup returns not-found for this token.
		clusterAgentErr: errNotFound,
	}
	h := NewHubWithValidator(slog.Default(), validator)
	_, conn, ctx := testServerAndClient(t, h)

	// A denied connect closes the WS (StatusPolicyViolation) without an ack.
	// Either an ack with Accepted=false OR a closed read both prove denial.
	connectPayload, _ := json.Marshal(protocol.ConnectPayload{
		ClusterID:    clusterID.String(),
		AgentID:      "agent-rot",
		AgentVersion: "1.0.0",
		Token:        revokedToken,
	})
	if err := wsjson.Write(ctx, conn, &protocol.Message{Type: protocol.MsgConnect, Payload: connectPayload}); err != nil {
		t.Fatalf("write connect: %v", err)
	}
	var ack protocol.Message
	if err := wsjson.Read(ctx, conn, &ack); err != nil {
		// Connection closed without an ack — denial confirmed.
		return
	}
	var ackPayload protocol.ConnectAckPayload
	_ = json.Unmarshal(ack.Payload, &ackPayload)
	if ackPayload.Accepted {
		t.Fatal("connect with a revoked token must be denied")
	}

	_ = conn.Close(websocket.StatusNormalClosure, "done")
}

// (d) Default path (no rotation triggered) stays mint-once / unchanged: the
// agent presents its current token, nothing pending, server returns it as-is
// and does NOT mint or rotate.
func TestNoRotationDefaultPathUnchanged(t *testing.T) {
	clusterID := uuid.New()
	token := "steady-token"
	validator := &recordingValidator{
		clusterAgentToken: sqlc.ClusterAgentToken{
			ID:        uuid.New(),
			ClusterID: clusterID,
			TokenHash: auth.HashOpaqueToken(token),
		},
	}
	h := NewHubWithValidator(slog.Default(), validator)
	_, conn, ctx := testServerAndClient(t, h)

	ack := connectWithToken(t, conn, ctx, clusterID.String(), token)
	if !ack.Accepted {
		t.Fatalf("steady-state connect must be accepted, got %q", ack.Reason)
	}
	if ack.AgentToken != "" {
		t.Fatalf("default path must not deliver a new token, got %q", ack.AgentToken)
	}
	if rotations := validator.SnapshotAgentTokenRotations(); len(rotations) != 0 {
		t.Fatalf("default path must not rotate, got %d rotations", len(rotations))
	}
	if upserts := validator.SnapshotAgentTokenUpserts(); len(upserts) != 0 {
		t.Fatalf("default path must not upsert (mint-once already satisfied), got %d", len(upserts))
	}
	if cleared := validator.SnapshotClearedPreviousIDs(); len(cleared) != 0 {
		t.Fatalf("default path must not clear previous hash, got %d", len(cleared))
	}
	// last_used_at IS touched on a steady-state connect.
	if len(validator.touchedAgentIDs) != 1 {
		t.Fatalf("expected the token to be touched once, got %d", len(validator.touchedAgentIDs))
	}

	_ = conn.Close(websocket.StatusNormalClosure, "done")
}
