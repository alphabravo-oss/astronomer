package charlie

import (
	"context"
	"fmt"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
)

type operationReceiptReader interface {
	GetCharlieActionReceipt(context.Context, string) (sqlc.CharlieActionReceipt, error)
}

type operationSessionAuthorizer interface {
	Get(context.Context, uuid.UUID, uuid.UUID) (SessionView, error)
}

type OperationAccessService struct {
	receipts operationReceiptReader
	sessions operationSessionAuthorizer
}

func NewOperationAccessService(receipts operationReceiptReader, sessions operationSessionAuthorizer) (*OperationAccessService, error) {
	if receipts == nil || sessions == nil {
		return nil, fmt.Errorf("Charlie operation access requires receipts and live session authorization")
	}
	return &OperationAccessService{receipts: receipts, sessions: sessions}, nil
}

func (s *OperationAccessService) Get(ctx context.Context, actor uuid.UUID, operationID string) (sqlc.CharlieActionReceipt, error) {
	operationID = strings.TrimSpace(operationID)
	if actor == uuid.Nil || operationID == "" || len(operationID) > 128 {
		return sqlc.CharlieActionReceipt{}, fmt.Errorf("Charlie operation access is invalid")
	}
	receipt, err := s.receipts.GetCharlieActionReceipt(ctx, operationID)
	if err != nil {
		return sqlc.CharlieActionReceipt{}, err
	}
	if _, err := s.sessions.Get(ctx, actor, receipt.SessionID); err != nil {
		return sqlc.CharlieActionReceipt{}, fmt.Errorf("Charlie operation access is denied")
	}
	return receipt, nil
}
