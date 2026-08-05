package charlie

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type actionReceiptQueries interface {
	GetCharlieConnectionByDeploymentID(context.Context, string) (sqlc.CharlieConnection, error)
	GetCharlieSessionByCentralID(context.Context, string) (sqlc.CharlieSession, error)
	ClaimCharlieActionReceipt(context.Context, sqlc.ClaimCharlieActionReceiptParams) (sqlc.CharlieActionReceipt, error)
	GetCharlieActionReceipt(context.Context, string) (sqlc.CharlieActionReceipt, error)
	TransitionCharlieActionReceipt(context.Context, sqlc.TransitionCharlieActionReceiptParams) (sqlc.CharlieActionReceipt, error)
}

// actionReceiptCipher is satisfied by Astronomer's rotation-aware Fernet
// encryptor. Arguments and terminal results are needed for post-crash
// postcondition reconciliation and exact replay, but must never be stored as
// plaintext or emitted to logs/support bundles.
type actionReceiptCipher interface {
	Encrypt(string) (string, error)
	Decrypt(string) (string, error)
}

type DBActionReceiptStore struct {
	queries    actionReceiptQueries
	cipher     actionReceiptCipher
	leaseOwner string
	now        func() time.Time
}

func NewDBActionReceiptStore(queries actionReceiptQueries, cipher actionReceiptCipher, leaseOwner string) (*DBActionReceiptStore, error) {
	if queries == nil || cipher == nil || leaseOwner == "" || len(leaseOwner) > 128 {
		return nil, fmt.Errorf("Charlie action receipts require a store, encryption, and bounded server identity")
	}
	return &DBActionReceiptStore{queries: queries, cipher: cipher, leaseOwner: leaseOwner, now: time.Now}, nil
}

func (s *DBActionReceiptStore) Claim(ctx context.Context, action ActionEnvelope, capability CapabilityDescriptor) (ReceiptClaim, error) {
	argumentsEncrypted, err := s.cipher.Encrypt(string(action.Arguments))
	if err != nil || argumentsEncrypted == "" {
		return ReceiptClaim{}, fmt.Errorf("encrypt Charlie action receipt arguments")
	}
	connection, err := s.queries.GetCharlieConnectionByDeploymentID(ctx, action.DeploymentID)
	if err != nil || !connection.Active || connection.EmergencyDisabled {
		return ReceiptClaim{}, fmt.Errorf("Charlie connection is inactive")
	}
	session, err := s.queries.GetCharlieSessionByCentralID(ctx, action.SessionID)
	if err != nil || session.ConnectionID != connection.ID || (session.State != "active" && session.State != "waiting_approval") {
		return ReceiptClaim{}, fmt.Errorf("Charlie session binding changed")
	}
	_, err = s.queries.ClaimCharlieActionReceipt(ctx, sqlc.ClaimCharlieActionReceiptParams{
		ConnectionID: connection.ID, SessionID: session.ID,
		CharlieActionID: action.ActionID, TurnID: action.TurnID,
		Capability: capability.Name, Effect: string(capability.Effect),
		ArgumentDigest: action.ArgumentDigest, ArgumentsEncrypted: argumentsEncrypted,
		AuthorizationHash: HashDelegation(action.AuthorizationRef),
		FencingEpoch:      action.FencingEpoch, ProductIdempotencyKey: action.IdempotencyKey,
		LeaseOwner: s.leaseOwner, LeaseExpiresAt: s.now().UTC().Add(45 * time.Second),
		AuditCorrelationID: uuid.New(),
		ResourceDigest:     resourceDigest(capability.Name, argumentsForResourceDigest(action.Arguments)),
	})
	if err == nil {
		return ReceiptClaim{Disposition: ReceiptClaimed}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ReceiptClaim{}, err
	}
	existing, err := s.queries.GetCharlieActionReceipt(ctx, action.ActionID)
	if err != nil {
		return ReceiptClaim{}, err
	}
	if existing.ConnectionID != connection.ID || existing.SessionID != session.ID || existing.TurnID != action.TurnID || existing.Capability != capability.Name || existing.Effect != string(capability.Effect) || existing.ArgumentDigest != action.ArgumentDigest || existing.AuthorizationHash != HashDelegation(action.AuthorizationRef) || existing.FencingEpoch != action.FencingEpoch || existing.ProductIdempotencyKey != action.IdempotencyKey || existing.ResourceDigest != resourceDigest(capability.Name, argumentsForResourceDigest(action.Arguments)) {
		return ReceiptClaim{Disposition: ReceiptConflict}, nil
	}
	switch existing.State {
	case "succeeded", "failed", "blocked", "fenced", "deferred":
		result, replayErr := s.decryptResult(existing)
		if replayErr != nil {
			return ReceiptClaim{}, replayErr
		}
		return ReceiptClaim{Disposition: ReceiptReplay, Result: result}, nil
	case "claimed", "waiting_approval", "dispatched", "ambiguous", "verifying":
		return ReceiptClaim{Disposition: ReceiptAmbiguous}, nil
	default:
		return ReceiptClaim{Disposition: ReceiptConflict}, nil
	}
}

func (s *DBActionReceiptStore) Transition(ctx context.Context, action ActionEnvelope, next string, result ActionResult) error {
	receipt, err := s.queries.GetCharlieActionReceipt(ctx, action.ActionID)
	if err != nil {
		return err
	}
	if receipt.ArgumentDigest != action.ArgumentDigest || receipt.AuthorizationHash != HashDelegation(action.AuthorizationRef) || receipt.FencingEpoch != action.FencingEpoch || receipt.LeaseOwner != s.leaseOwner {
		return fmt.Errorf("Charlie action receipt binding changed")
	}
	if !validReceiptTransition(receipt.State, next) {
		return fmt.Errorf("Charlie action receipt transition is invalid")
	}
	bounded := result
	encoded, err := json.Marshal(bounded)
	if err != nil || len(encoded) == 0 || len(encoded) > maxActionResult+(16<<10) {
		return fmt.Errorf("Charlie action receipt result exceeds its safe bound")
	}
	resultEncrypted, err := s.cipher.Encrypt(string(encoded))
	if err != nil || resultEncrypted == "" {
		return fmt.Errorf("encrypt Charlie action receipt result")
	}
	_, err = s.queries.TransitionCharlieActionReceipt(ctx, sqlc.TransitionCharlieActionReceiptParams{
		NextState: next, ResultDigest: digestBytes(encoded), ResultStatus: result.State,
		ResultEncrypted: resultEncrypted,
		ID:              receipt.ID, ExpectedState: receipt.State, LeaseOwner: s.leaseOwner,
		FencingEpoch: action.FencingEpoch,
	})
	return err
}

func (s *DBActionReceiptStore) decryptResult(receipt sqlc.CharlieActionReceipt) (ActionResult, error) {
	if receipt.ResultEncrypted == "" || receipt.ResultDigest == "" {
		return ActionResult{}, fmt.Errorf("Charlie terminal receipt has no replay result")
	}
	plain, err := s.cipher.Decrypt(receipt.ResultEncrypted)
	if err != nil || len(plain) == 0 || len(plain) > maxActionResult+(16<<10) || digestBytes([]byte(plain)) != receipt.ResultDigest {
		return ActionResult{}, fmt.Errorf("Charlie terminal receipt replay integrity failed")
	}
	var result ActionResult
	// Receipt state records the durable lifecycle transition, while the exact
	// client result records the product decision. A fenced transition, for
	// example, deliberately replays the original blocked result; requiring those
	// two vocabularies to be identical would turn a safe exact replay into an
	// integrity failure after a disable race.
	if json.Unmarshal([]byte(plain), &result) != nil || result.State != receipt.ResultStatus {
		return ActionResult{}, fmt.Errorf("Charlie terminal receipt replay state is invalid")
	}
	if len(result.Result) > maxActionResult || (len(result.Result) > 0 && !json.Valid(result.Result)) {
		return ActionResult{}, fmt.Errorf("Charlie terminal receipt replay payload is invalid")
	}
	return result, nil
}

func validReceiptTransition(current, next string) bool {
	switch current {
	case "claimed":
		return next == "blocked" || next == "deferred" || next == "dispatched" || next == "fenced"
	case "dispatched":
		return next == "ambiguous" || next == "verifying" || next == "succeeded" || next == "failed" || next == "fenced"
	case "verifying":
		return next == "succeeded" || next == "failed" || next == "fenced"
	case "ambiguous":
		return next == "verifying" || next == "failed" || next == "fenced"
	default:
		return false
	}
}
