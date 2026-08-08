package charlie

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// AuthorityMutationAudit is the bounded, content-free intent that must be
// durable before Charlie authority can be created, widened, or consumed.
type AuthorityMutationAudit struct {
	Action       string
	ResourceType string
	ResourceID   string
	ActorID      uuid.UUID
	OutcomeCode  string
	Fields       map[string]any
}

type AuthorityMutationAuditor interface {
	RecordCharlieAuthorityMutation(context.Context, AuthorityMutationAudit) error
}

func requireAuthorityMutationAudit(ctx context.Context, auditor AuthorityMutationAuditor, event AuthorityMutationAudit) error {
	if auditor == nil {
		return fmt.Errorf("Charlie authority audit is unavailable")
	}
	if event.OutcomeCode == "" {
		event.OutcomeCode = "authorized"
	}
	return auditor.RecordCharlieAuthorityMutation(ctx, event)
}
