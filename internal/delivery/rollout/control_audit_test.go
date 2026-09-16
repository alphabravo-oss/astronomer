package rollout

import (
	"context"
	"errors"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
)

func TestControllerRejectsMissingAuditBeforeOpeningDatabase(t *testing.T) {
	controller := &PostgresController{}
	if _, err := controller.Act(context.Background(), ActionRequest{}); !errors.Is(err, audit.ErrOutboxUnavailable) {
		t.Fatalf("Act missing audit = %v", err)
	}
	if _, err := controller.Approve(context.Background(), ApprovalRequest{}); !errors.Is(err, audit.ErrOutboxUnavailable) {
		t.Fatalf("Approve missing audit = %v", err)
	}
}
