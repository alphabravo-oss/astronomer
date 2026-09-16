package deployment

import (
	"context"
	"errors"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
)

func TestControllerRejectsMissingAuditBeforeOpeningDatabase(t *testing.T) {
	if _, err := (&PostgresController{}).Act(context.Background(), Request{}); !errors.Is(err, audit.ErrOutboxUnavailable) {
		t.Fatalf("Act missing audit = %v", err)
	}
}
