package charlie

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/crypto"
)

type receiptTestCipher struct{}

func (receiptTestCipher) Encrypt(value []byte) (crypto.Sealed, error) {
	return crypto.Sealed{KeyID: "test-key", Algorithm: crypto.AlgorithmAgeX25519, Ciphertext: []byte(base64.RawURLEncoding.EncodeToString(value))}, nil
}
func (receiptTestCipher) Decrypt(value crypto.Sealed) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(string(value.Ciphertext))
}

func resultSealed(p sqlc.TransitionCharlieActionReceiptParams) crypto.Sealed {
	return crypto.FromParts(p.ResultKeyID, p.ResultAlgorithm, p.ResultCiphertext, p.ResultNonce)
}

type fakeReceiptQueries struct {
	connection             sqlc.CharlieConnection
	session                sqlc.CharlieSession
	receipt                sqlc.CharlieActionReceipt
	claimErr               error
	commitClaimBeforeError bool
	transition             sqlc.TransitionCharlieActionReceiptParams
	claimCalls             int
}

func (f *fakeReceiptQueries) GetCharlieConnectionByDeploymentID(context.Context, string) (sqlc.CharlieConnection, error) {
	return f.connection, nil
}
func (f *fakeReceiptQueries) GetCharlieSessionByCentralID(context.Context, string) (sqlc.CharlieSession, error) {
	return f.session, nil
}
func (f *fakeReceiptQueries) ClaimCharlieActionReceipt(_ context.Context, arg sqlc.ClaimCharlieActionReceiptParams) (sqlc.CharlieActionReceipt, error) {
	f.claimCalls++
	if f.claimErr != nil && !f.commitClaimBeforeError {
		return sqlc.CharlieActionReceipt{}, f.claimErr
	}
	f.receipt = sqlc.CharlieActionReceipt{
		ID: uuid.New(), ConnectionID: arg.ConnectionID, SessionID: arg.SessionID,
		CharlieActionID: arg.CharlieActionID, TurnID: arg.TurnID,
		Capability: arg.Capability, Effect: arg.Effect, ArgumentDigest: arg.ArgumentDigest,
		ArgumentsKeyID: arg.ArgumentsKeyID, ArgumentsAlgorithm: arg.ArgumentsAlgorithm,
		ArgumentsCiphertext: arg.ArgumentsCiphertext, ArgumentsNonce: arg.ArgumentsNonce,
		AuthorizationHash: arg.AuthorizationHash, FencingEpoch: arg.FencingEpoch,
		ProductIdempotencyKey: arg.ProductIdempotencyKey, State: "claimed",
		LeaseOwner: arg.LeaseOwner, LeaseExpiresAt: arg.LeaseExpiresAt,
		ResourceDigest: arg.ResourceDigest, AuditCorrelationID: arg.AuditCorrelationID,
	}
	if f.claimErr != nil {
		return sqlc.CharlieActionReceipt{}, f.claimErr
	}
	return f.receipt, nil
}
func (f *fakeReceiptQueries) GetCharlieActionReceipt(context.Context, string) (sqlc.CharlieActionReceipt, error) {
	return f.receipt, nil
}
func (f *fakeReceiptQueries) TransitionCharlieActionReceipt(_ context.Context, arg sqlc.TransitionCharlieActionReceiptParams) (sqlc.CharlieActionReceipt, error) {
	f.transition = arg
	f.receipt.State = arg.NextState
	f.receipt.ResultDigest = arg.ResultDigest
	f.receipt.ResultStatus = arg.ResultStatus
	f.receipt.ResultKeyID, f.receipt.ResultAlgorithm, f.receipt.ResultCiphertext, f.receipt.ResultNonce = resultSealed(arg).Parts()
	return f.receipt, nil
}

func receiptFixture() (*fakeReceiptQueries, ActionEnvelope, CapabilityDescriptor) {
	connectionID := uuid.New()
	sessionID := uuid.New()
	queries := &fakeReceiptQueries{
		connection: sqlc.CharlieConnection{ID: connectionID, DeploymentID: "deployment-a", Active: true},
		session:    sqlc.CharlieSession{ID: sessionID, ConnectionID: connectionID, CharlieSessionID: "session-a", State: "active"},
	}
	action := ActionEnvelope{
		DeploymentID: "deployment-a", SessionID: "session-a", TurnID: "turn-a",
		ActionID: "action-a", ArgumentDigest: "argument-a", AuthorizationRef: "opaque-a",
		FencingEpoch: 7, IdempotencyKey: "action-a",
		Arguments: []byte(`{"resource_id":"resource-a","task_id":"task-a","operation_id":"action-a"}`),
	}
	capability, _ := capabilityByName("astronomer.queue.retry_task")
	return queries, action, capability
}

func TestDBActionReceiptClaimBindsDeploymentSessionAuthorityAndFence(t *testing.T) {
	queries, action, capability := receiptFixture()
	store, _ := NewDBActionReceiptStore(queries, receiptTestCipher{}, "server-a")
	store.now = func() time.Time { return time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC) }
	claim, err := store.Claim(context.Background(), action, capability)
	if err != nil || claim.Disposition != ReceiptClaimed {
		t.Fatalf("claim=%+v err=%v", claim, err)
	}
	receipt := queries.receipt
	if receipt.ConnectionID != queries.connection.ID || receipt.SessionID != queries.session.ID || receipt.AuthorizationHash != HashDelegation(action.AuthorizationRef) || receipt.FencingEpoch != 7 || receipt.LeaseOwner != "server-a" || !receipt.LeaseExpiresAt.Equal(store.now().Add(45*time.Second)) {
		t.Fatalf("receipt binding incomplete: %+v", receipt)
	}
}

func TestDBActionReceiptRecoversItsCommittedClaimWhenReturningIsLost(t *testing.T) {
	queries, action, capability := receiptFixture()
	queries.claimErr = context.Canceled
	queries.commitClaimBeforeError = true
	store, _ := NewDBActionReceiptStore(queries, receiptTestCipher{}, "server-a")
	claim, err := store.Claim(context.Background(), action, capability)
	if err != nil || claim.Disposition != ReceiptClaimed {
		t.Fatalf("committed claim=%+v err=%v", claim, err)
	}
	queries.receipt.AuditCorrelationID = uuid.New()
	queries.commitClaimBeforeError = false
	queries.claimErr = pgx.ErrNoRows
	claim, err = store.Claim(context.Background(), action, capability)
	if err != nil || claim.Disposition != ReceiptAmbiguous {
		t.Fatalf("foreign committed claim=%+v err=%v", claim, err)
	}
}

func TestDBActionReceiptExactReplayNeverReexecutes(t *testing.T) {
	queries, action, capability := receiptFixture()
	queries.claimErr = pgx.ErrNoRows
	queries.receipt = sqlc.CharlieActionReceipt{
		ConnectionID: queries.connection.ID, SessionID: queries.session.ID,
		CharlieActionID: action.ActionID, TurnID: action.TurnID,
		Capability: capability.Name, Effect: string(capability.Effect),
		ArgumentDigest: action.ArgumentDigest, AuthorizationHash: HashDelegation(action.AuthorizationRef),
		FencingEpoch: action.FencingEpoch, ProductIdempotencyKey: action.IdempotencyKey, State: "succeeded",
		ResourceDigest: resourceDigest(capability.Name, argumentsForResourceDigest(action.Arguments)),
	}
	replayResult := ActionResult{Allowed: true, State: "succeeded", Verified: true, Result: []byte(`{"status":"done"}`)}
	replayBytes, _ := json.Marshal(replayResult)
	replaySealed, _ := receiptTestCipher{}.Encrypt(replayBytes)
	queries.receipt.ResultKeyID, queries.receipt.ResultAlgorithm, queries.receipt.ResultCiphertext, queries.receipt.ResultNonce = replaySealed.Parts()
	queries.receipt.ResultDigest = digestBytes(replayBytes)
	queries.receipt.ResultStatus = "succeeded"
	store, _ := NewDBActionReceiptStore(queries, receiptTestCipher{}, "server-b")
	claim, err := store.Claim(context.Background(), action, capability)
	if err != nil || claim.Disposition != ReceiptReplay || claim.Result.State != "succeeded" || !claim.Result.Verified {
		t.Fatalf("replay=%+v err=%v", claim, err)
	}
	queries.receipt.ArgumentDigest = "different"
	claim, err = store.Claim(context.Background(), action, capability)
	if err != nil || claim.Disposition != ReceiptConflict {
		t.Fatalf("argument conflict=%+v err=%v", claim, err)
	}
}

func TestDBActionReceiptExactReplayPreservesFencedDecisionState(t *testing.T) {
	queries, action, capability := receiptFixture()
	queries.claimErr = pgx.ErrNoRows
	queries.receipt = sqlc.CharlieActionReceipt{
		ConnectionID: queries.connection.ID, SessionID: queries.session.ID,
		CharlieActionID: action.ActionID, TurnID: action.TurnID,
		Capability: capability.Name, Effect: string(capability.Effect),
		ArgumentDigest: action.ArgumentDigest, AuthorizationHash: HashDelegation(action.AuthorizationRef),
		FencingEpoch: action.FencingEpoch, ProductIdempotencyKey: action.IdempotencyKey,
		State: "fenced", ResultStatus: "blocked",
		ResourceDigest: resourceDigest(capability.Name, argumentsForResourceDigest(action.Arguments)),
	}
	replayResult := ActionResult{Allowed: false, Code: DeniedEmergencyDisabled, State: "blocked"}
	replayBytes, _ := json.Marshal(replayResult)
	replaySealed, _ := receiptTestCipher{}.Encrypt(replayBytes)
	queries.receipt.ResultKeyID, queries.receipt.ResultAlgorithm, queries.receipt.ResultCiphertext, queries.receipt.ResultNonce = replaySealed.Parts()
	queries.receipt.ResultDigest = digestBytes(replayBytes)
	store, _ := NewDBActionReceiptStore(queries, receiptTestCipher{}, "server-b")
	claim, err := store.Claim(context.Background(), action, capability)
	if err != nil || claim.Disposition != ReceiptReplay || claim.Result.Allowed || claim.Result.State != "blocked" || claim.Result.Code != DeniedEmergencyDisabled {
		t.Fatalf("fenced replay=%+v err=%v", claim, err)
	}
}

func TestDBActionReceiptAmbiguousPriorAttemptNeverBlindRetries(t *testing.T) {
	queries, action, capability := receiptFixture()
	queries.claimErr = pgx.ErrNoRows
	queries.receipt = sqlc.CharlieActionReceipt{
		ConnectionID: queries.connection.ID, SessionID: queries.session.ID,
		CharlieActionID: action.ActionID, TurnID: action.TurnID,
		Capability: capability.Name, Effect: string(capability.Effect),
		ArgumentDigest: action.ArgumentDigest, AuthorizationHash: HashDelegation(action.AuthorizationRef),
		FencingEpoch: action.FencingEpoch, ProductIdempotencyKey: action.IdempotencyKey, State: "ambiguous",
		ResourceDigest: resourceDigest(capability.Name, argumentsForResourceDigest(action.Arguments)),
	}
	store, _ := NewDBActionReceiptStore(queries, receiptTestCipher{}, "server-b")
	claim, err := store.Claim(context.Background(), action, capability)
	if err != nil || claim.Disposition != ReceiptAmbiguous {
		t.Fatalf("ambiguous=%+v err=%v", claim, err)
	}
}

func TestDBActionReceiptEmergencyDisableBlocksClaim(t *testing.T) {
	queries, action, capability := receiptFixture()
	queries.connection.EmergencyDisabled = true
	store, _ := NewDBActionReceiptStore(queries, receiptTestCipher{}, "server-a")
	if _, err := store.Claim(context.Background(), action, capability); err == nil || queries.claimCalls != 0 {
		t.Fatal("emergency-disabled action claimed a receipt")
	}
}

func TestDBActionReceiptTransitionsAreCASAndContentFree(t *testing.T) {
	queries, action, _ := receiptFixture()
	queries.receipt = sqlc.CharlieActionReceipt{
		ID: uuid.New(), CharlieActionID: action.ActionID, ArgumentDigest: action.ArgumentDigest,
		AuthorizationHash: HashDelegation(action.AuthorizationRef), FencingEpoch: action.FencingEpoch,
		LeaseOwner: "server-a", State: "claimed",
	}
	store, _ := NewDBActionReceiptStore(queries, receiptTestCipher{}, "server-a")
	result := ActionResult{Allowed: true, State: "dispatched", Result: []byte(`{"secret":"must-not-persist"}`)}
	if err := store.Transition(context.Background(), action, "dispatched", result); err != nil {
		t.Fatal(err)
	}
	if queries.transition.ExpectedState != "claimed" || queries.transition.NextState != "dispatched" || queries.transition.ResultDigest == "" || queries.transition.ResultStatus != "dispatched" {
		t.Fatalf("transition is not exact/content-free: %+v", queries.transition)
	}
	if queries.transition.ResultKeyID == "" || strings.Contains(string(queries.transition.ResultCiphertext), "must-not-persist") {
		t.Fatalf("transition result was not encrypted: %+v", queries.transition)
	}
	if err := store.Transition(context.Background(), action, "blocked", result); err == nil {
		t.Fatal("invalid dispatched-to-blocked transition succeeded")
	}
}
