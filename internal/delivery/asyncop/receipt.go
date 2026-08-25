package asyncop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
)

const MaxIdempotencyKeyBytes = 128

var (
	ErrConflict = errors.New("delivery idempotency key identifies a different request")
	ErrCorrupt  = errors.New("delivery idempotency receipt is incomplete or invalid")
)

// Receipt is the common exact 202 response for asynchronous Delivery
// mutations. OperationID names the durable row/event that proves acceptance;
// StatusURL is always an existing project-scoped GET for the affected resource.
type Receipt struct {
	OperationID uuid.UUID `json:"operation_id"`
	Operation   string    `json:"operation"`
	Resource    string    `json:"resource"`
	ResourceID  uuid.UUID `json:"resource_id"`
	ProjectID   uuid.UUID `json:"project_id"`
	Status      string    `json:"status"`
	StatusURL   string    `json:"status_url"`
	AcceptedAt  time.Time `json:"accepted_at"`
}

type Store interface {
	ReserveOperationIdempotencyKey(context.Context, sqlc.ReserveOperationIdempotencyKeyParams) (sqlc.OperationIdempotencyKey, error)
	AttachOperationIdempotencyKey(context.Context, sqlc.AttachOperationIdempotencyKeyParams) (sqlc.OperationIdempotencyKey, error)
}

type ClaimRequest struct {
	ActorID        uuid.UUID
	ProjectID      uuid.UUID
	Operation      string
	Resource       string
	TargetID       uuid.UUID
	IdempotencyKey string
	RequestDigest  string
	OperationTable string
}

type Claim struct {
	Scope          string
	IdempotencyKey string
	RequestDigest  string
	OperationTable string
	Receipt        Receipt
	Replay         bool
}

type persistedEnvelope struct {
	RequestDigest string  `json:"request_digest"`
	Receipt       Receipt `json:"receipt"`
}

func ValidateKey(key string) error {
	if key == "" || key != strings.TrimSpace(key) || len(key) > MaxIdempotencyKeyBytes || !utf8.ValidString(key) || strings.IndexFunc(key, unicode.IsControl) >= 0 {
		return fmt.Errorf("Idempotency-Key must be 1 through %d printable UTF-8 bytes without surrounding whitespace", MaxIdempotencyKeyBytes)
	}
	return nil
}

func Digest(value any) (string, error) {
	digest, err := model.CanonicalDigest(value)
	if err != nil {
		return "", err
	}
	return digest.String(), nil
}

// ClaimKey reserves one actor+operation+target scoped key. Because callers use
// a transaction-bound Store, the reservation, domain mutation, audit outbox,
// task outbox and attached response share one commit decision.
func ClaimKey(ctx context.Context, store Store, request ClaimRequest) (Claim, error) {
	if store == nil || request.ActorID == uuid.Nil || request.ProjectID == uuid.Nil || request.TargetID == uuid.Nil || strings.TrimSpace(request.Operation) == "" || strings.TrimSpace(request.Resource) == "" || strings.TrimSpace(request.OperationTable) == "" || strings.TrimSpace(request.RequestDigest) == "" {
		return Claim{}, ErrCorrupt
	}
	if err := ValidateKey(request.IdempotencyKey); err != nil {
		return Claim{}, err
	}
	scope := strings.Join([]string{"delivery", "actor:" + request.ActorID.String(), "operation:" + request.Operation, "target:" + request.TargetID.String()}, ":")
	row, err := store.ReserveOperationIdempotencyKey(ctx, sqlc.ReserveOperationIdempotencyKeyParams{Scope: scope, IdempotencyKey: request.IdempotencyKey})
	if err != nil {
		return Claim{}, err
	}
	claim := Claim{Scope: scope, IdempotencyKey: request.IdempotencyKey, RequestDigest: request.RequestDigest, OperationTable: request.OperationTable}
	if !row.OperationID.Valid {
		return claim, nil
	}
	if row.OperationTable != request.OperationTable {
		return Claim{}, ErrConflict
	}
	var persisted persistedEnvelope
	if err := json.Unmarshal(row.Response, &persisted); err != nil || persisted.RequestDigest == "" || persisted.Receipt.OperationID == uuid.Nil || persisted.Receipt.StatusURL == "" ||
		persisted.Receipt.Operation != request.Operation || persisted.Receipt.Resource != request.Resource || persisted.Receipt.ResourceID != request.TargetID || persisted.Receipt.ProjectID != request.ProjectID || persisted.Receipt.Status != "accepted" {
		return Claim{}, ErrCorrupt
	}
	if persisted.RequestDigest != request.RequestDigest {
		return Claim{}, ErrConflict
	}
	claim.Receipt = persisted.Receipt
	claim.Replay = true
	return claim, nil
}

func Attach(ctx context.Context, store Store, claim Claim, receipt Receipt) error {
	if store == nil || claim.Replay || claim.Scope == "" || claim.IdempotencyKey == "" || claim.RequestDigest == "" || claim.OperationTable == "" || receipt.OperationID == uuid.Nil || receipt.StatusURL == "" {
		return ErrCorrupt
	}
	receipt.AcceptedAt = receipt.AcceptedAt.UTC()
	raw, err := json.Marshal(persistedEnvelope{RequestDigest: claim.RequestDigest, Receipt: receipt})
	if err != nil {
		return err
	}
	_, err = store.AttachOperationIdempotencyKey(ctx, sqlc.AttachOperationIdempotencyKeyParams{
		Scope: claim.Scope, IdempotencyKey: claim.IdempotencyKey,
		OperationTable: claim.OperationTable, OperationID: receipt.OperationID, Response: raw,
	})
	return err
}

func NewReceipt(operationID uuid.UUID, operation, resource string, resourceID, projectID uuid.UUID, statusURL string, acceptedAt time.Time) Receipt {
	return Receipt{
		OperationID: operationID, Operation: operation, Resource: resource,
		ResourceID: resourceID, ProjectID: projectID, Status: "accepted",
		StatusURL: statusURL, AcceptedAt: acceptedAt.UTC(),
	}
}
