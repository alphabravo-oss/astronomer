package systemrollout

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
)

func TestServiceRejectsMissingAuditBeforeOpeningDatabase(t *testing.T) {
	service := &Service{}
	if _, err := service.Start(context.Background(), StartRequest{}); !errors.Is(err, audit.ErrOutboxUnavailable) {
		t.Fatalf("Start missing audit = %v", err)
	}
	if _, err := service.Act(context.Background(), uuid.New(), 1, ActionPause, uuid.New(), "", audit.Intent{}); !errors.Is(err, audit.ErrOutboxUnavailable) {
		t.Fatalf("Act missing audit = %v", err)
	}
}
