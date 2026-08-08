package charlie

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/redaction"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	DefaultFindingTTL = 7 * 24 * time.Hour
)

type findingStoreQueries interface {
	GetActiveCharlieConnection(context.Context) (sqlc.CharlieConnection, error)
	GetActiveCharlieFindingByFingerprint(context.Context, sqlc.GetActiveCharlieFindingByFingerprintParams) (sqlc.CharlieFinding, error)
	UpsertCharlieFinding(context.Context, sqlc.UpsertCharlieFindingParams) (sqlc.CharlieFinding, error)
	AddCharlieFindingResource(context.Context, sqlc.AddCharlieFindingResourceParams) error
}

type DBFindingStore struct {
	queries findingStoreQueries
	now     func() time.Time
}

func NewDBFindingStore(queries findingStoreQueries) (*DBFindingStore, error) {
	if queries == nil {
		return nil, fmt.Errorf("Charlie finding database is unavailable")
	}
	return &DBFindingStore{queries: queries, now: time.Now}, nil
}

func (s *DBFindingStore) UpsertBlockedFinding(ctx context.Context, input FindingInput, recommendation FindingRecommendation, fingerprint string) (DurableFinding, error) {
	if len(fingerprint) != 64 {
		return DurableFinding{}, fmt.Errorf("Charlie finding fingerprint is invalid")
	}
	connection, err := s.queries.GetActiveCharlieConnection(ctx)
	if err != nil || !connection.Active || connection.EmergencyDisabled || connection.InstallationID.String() != input.InstallationID {
		return DurableFinding{}, fmt.Errorf("Charlie finding connection is inactive")
	}
	_, priorErr := s.queries.GetActiveCharlieFindingByFingerprint(ctx, sqlc.GetActiveCharlieFindingByFingerprintParams{ConnectionID: connection.ID, DedupeFingerprint: fingerprint})
	if priorErr != nil && !errors.Is(priorErr, pgx.ErrNoRows) {
		return DurableFinding{}, fmt.Errorf("load active Charlie finding: %w", priorErr)
	}
	now := s.now().UTC()
	row, err := s.queries.UpsertCharlieFinding(ctx, sqlc.UpsertCharlieFindingParams{
		// charlie_finding_id is globally unique for the connection while the
		// partial fingerprint index deduplicates only active findings. A unique
		// lifecycle suffix lets a resolved/dismissed/expired diagnosis recur as a
		// new active finding without colliding with its retained history.
		ConnectionID: connection.ID, CharlieFindingID: "local-" + fingerprint[:24] + "-" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		SessionID: pgtype.UUID{}, Source: "system", Severity: input.Severity,
		EffectiveMode: string(input.Mode), ExecutionBlockCode: string(recommendation.ExecutionBlockCode),
		DedupeFingerprint: fingerprint,
		Title:             redactFindingText(recommendation.Title, 256), Summary: redactFindingText(recommendation.Summary, 2048),
		RecommendedActionLabel: redactFindingText(recommendation.RecommendedAction, 256),
		RiskImpact:             redactFindingText(input.RiskImpact, 1024),
		VerificationSummary:    redactFindingText(recommendation.Verification, 1024),
		ExpiresAt:              pgtype.Timestamptz{Time: now.Add(DefaultFindingTTL), Valid: true},
	})
	if err != nil {
		return DurableFinding{}, err
	}
	// The resource disclosure is the live-authorization boundary for every
	// subsequent list/get/transition. A finding without a product-owned scope
	// must fail closed and is never surfaced merely because Charlie knows its ID.
	if err := s.queries.AddCharlieFindingResource(ctx, sqlc.AddCharlieFindingResourceParams{
		FindingID: row.ID, ResourceType: input.ResourceType, ResourceID: input.ResourceID, RequiredVerb: "read",
	}); err != nil {
		return DurableFinding{}, fmt.Errorf("persist Charlie finding resource scope: %w", err)
	}
	// Every committed occurrence reaches the product-owned alert planner. The
	// planner, not the finding store, owns severity thresholds, quiet hours,
	// deduplication, channel routing, and escalation.
	return DurableFinding{ID: row.ID.String(), Fingerprint: row.DedupeFingerprint, Status: row.Status, RepeatCount: int(row.RepeatCount), UpdatedAt: row.UpdatedAt, Notify: true}, nil
}

func redactFindingText(value string, maximum int) string {
	value = strings.TrimSpace(redaction.SensitiveLine(value))
	return truncateUTF8(value, maximum)
}
