package charlie

import (
	"context"
	"fmt"
	"time"
)

const (
	CharlieFindingMetadataRetention = 90 * 24 * time.Hour
	CharlieSessionMetadataRetention = 30 * 24 * time.Hour
)

type retentionQueries interface {
	RevokeExpiredCharlieDelegations(context.Context) (int64, error)
	ExpireCharlieFindings(context.Context, time.Time) (int64, error)
	DeleteCharlieFindingMetadataBefore(context.Context, time.Time) (int64, error)
	DeleteCharlieSessionMetadataBefore(context.Context, time.Time) (int64, error)
}

// RetentionService removes only bounded local Charlie metadata. Charlie-owned
// conversation, evidence, action result, and RAG content never exists in these
// tables; Astronomer audit records are intentionally not deleted here.
type RetentionService struct {
	queries retentionQueries
	active  func() bool
	now     func() time.Time
}

func NewRetentionService(queries retentionQueries, active func() bool) *RetentionService {
	if queries == nil || active == nil {
		return nil
	}
	return &RetentionService{queries: queries, active: active, now: time.Now}
}

func (s *RetentionService) Run(ctx context.Context) error {
	if s == nil || !s.active() {
		return nil
	}
	now := s.now().UTC()
	if _, err := s.queries.RevokeExpiredCharlieDelegations(ctx); err != nil {
		return fmt.Errorf("revoke expired Charlie delegations: %w", err)
	}
	if _, err := s.queries.ExpireCharlieFindings(ctx, now); err != nil {
		return fmt.Errorf("expire Charlie finding metadata: %w", err)
	}
	if _, err := s.queries.DeleteCharlieFindingMetadataBefore(ctx, now.Add(-CharlieFindingMetadataRetention)); err != nil {
		return fmt.Errorf("delete retained Charlie finding metadata: %w", err)
	}
	if _, err := s.queries.DeleteCharlieSessionMetadataBefore(ctx, now.Add(-CharlieSessionMetadataRetention)); err != nil {
		return fmt.Errorf("delete retained Charlie session metadata: %w", err)
	}
	return nil
}
