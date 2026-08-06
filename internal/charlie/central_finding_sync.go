package charlie

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const MaxCentralFindingSyncSessions = 100

type centralFindingSyncQueries interface {
	GetActiveCharlieConnection(context.Context) (sqlc.CharlieConnection, error)
	ListCharlieFindingSyncCandidateSessions(context.Context, uuid.UUID) ([]sqlc.CharlieSession, error)
}

type CentralFindingBridge interface {
	ListFindings(context.Context, string) ([]BridgeFindingSummary, error)
	GetFindingScope(context.Context, string, string) (BridgeFindingScope, error)
}

type CentralFindingStore interface {
	UpsertCentralFinding(context.Context, uuid.UUID, uuid.UUID, BridgeFindingSummary, Mode) (DurableFinding, error)
}

type FindingSummarySyncer interface {
	SyncForActor(context.Context, uuid.UUID) error
}

// CentralFindingSyncService discovers central lifecycle summaries only through
// delegations for local sessions the current user can access. A summary is
// accepted only when its required session_id exactly matches that delegated
// session; unknown, removed, cross-user, and cross-deployment links are ignored.
type CentralFindingSyncService struct {
	queries   centralFindingSyncQueries
	sessions  *SessionAccessService
	bridge    CentralFindingBridge
	store     CentralFindingStore
	publisher FindingAlertPublisher
	active    func() bool
}

func NewCentralFindingSyncService(queries centralFindingSyncQueries, sessions *SessionAccessService, bridge CentralFindingBridge, store CentralFindingStore, publisher FindingAlertPublisher, active func() bool) (*CentralFindingSyncService, error) {
	if queries == nil || sessions == nil || bridge == nil || store == nil || publisher == nil || active == nil {
		return nil, fmt.Errorf("Charlie finding sync requires local sessions, storage, publication, and an active bridge")
	}
	return &CentralFindingSyncService{queries: queries, sessions: sessions, bridge: bridge, store: store, publisher: publisher, active: active}, nil
}

func (s *CentralFindingSyncService) SyncForActor(ctx context.Context, actorID uuid.UUID) error {
	if s == nil || s.active == nil || !s.active() || actorID == uuid.Nil {
		return fmt.Errorf("Charlie finding sync is inactive")
	}
	connection, err := s.queries.GetActiveCharlieConnection(ctx)
	if err != nil || !connection.Active || connection.EmergencyDisabled {
		return fmt.Errorf("Charlie finding sync is inactive")
	}
	mode := EffectiveMode(Mode(connection.RequestedMode), Mode(connection.VerifiedMode), connection.EmergencyDisabled)
	if mode == ModeDisabled {
		return fmt.Errorf("Charlie finding sync is inactive")
	}
	candidates, err := s.queries.ListCharlieFindingSyncCandidateSessions(ctx, connection.ID)
	if err != nil || len(candidates) > MaxCentralFindingSyncSessions {
		return fmt.Errorf("Charlie finding sync candidates are unavailable")
	}
	seen := make(map[string]struct{})
	for _, candidate := range candidates {
		local, resources, authorizationRef, authorizeErr := s.sessions.authorize(ctx, actorID, candidate.ID)
		if authorizeErr != nil || local.ConnectionID != connection.ID || len(resources) == 0 {
			continue
		}
		summaries, listErr := s.bridge.ListFindings(ctx, authorizationRef)
		if listErr != nil {
			// A bridge outage is deployment-wide. Stop after one bounded failure;
			// the caller still serves already-synced local rows.
			return fmt.Errorf("Charlie finding summaries are unavailable")
		}
		if len(summaries) > MaxCharlieFindingItems {
			return fmt.Errorf("Charlie finding summaries exceed the contract limit")
		}
		for _, summary := range summaries {
			if !validBridgeFindingSummary(summary) || summary.SessionID != local.CharlieSessionID {
				continue
			}
			key := summary.SessionID + "\x00" + summary.FindingID
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
			scope, scopeErr := s.bridge.GetFindingScope(ctx, summary.FindingID, authorizationRef)
			if scopeErr != nil || scope.FindingID != summary.FindingID || scope.SessionID != summary.SessionID || scope.BlockCode != summary.BlockCode {
				continue
			}
			target, targetOK := exactFindingResource(resources, scope.ResourceDigest)
			if !targetOK {
				continue
			}
			summary.ResourceDigest = scope.ResourceDigest
			summary.RecommendedCapability = scope.RecommendedCapability
			if !s.active() {
				return fmt.Errorf("Charlie finding sync became inactive")
			}
			durable, persistErr := s.store.UpsertCentralFinding(ctx, connection.ID, local.ID, summary, centralFindingMode(summary.BlockCode, mode))
			if persistErr != nil {
				continue
			}
			if durable.Notify && s.active() {
				alert := FindingAlert{
					FindingID: durable.ID, Severity: summary.Severity,
					Status: centralFindingStatus(summary.Status, summary.WorkflowState), ResourceType: target.ResourceType,
					ResourceID: target.ResourceID, BlockCode: summary.BlockCode,
					RepeatCount: durable.RepeatCount,
				}
				if publishErr := s.publisher.PublishCharlieFinding(ctx, alert); publishErr != nil {
					return fmt.Errorf("Charlie finding notification is unavailable")
				}
			}
		}
	}
	return nil
}

func exactFindingResource(resources []sqlc.CharlieSessionResource, digest string) (sqlc.CharlieSessionResource, bool) {
	if !isLowerHexDigest(digest) {
		return sqlc.CharlieSessionResource{}, false
	}
	var match sqlc.CharlieSessionResource
	found := false
	for _, resource := range resources {
		if findingResourceDigest(resource.ResourceID) != digest {
			continue
		}
		if found {
			return sqlc.CharlieSessionResource{}, false
		}
		match, found = resource, true
	}
	return match, found
}

type PGCentralFindingStore struct {
	Pool *pgxpool.Pool
	now  func() time.Time
}

func NewPGCentralFindingStore(pool *pgxpool.Pool) (*PGCentralFindingStore, error) {
	if pool == nil {
		return nil, fmt.Errorf("Charlie central finding database is unavailable")
	}
	return &PGCentralFindingStore{Pool: pool, now: time.Now}, nil
}

func (s *PGCentralFindingStore) UpsertCentralFinding(ctx context.Context, connectionID, sessionID uuid.UUID, summary BridgeFindingSummary, mode Mode) (DurableFinding, error) {
	if s == nil || s.Pool == nil || connectionID == uuid.Nil || sessionID == uuid.Nil || !validBridgeFindingSummary(summary) ||
		!isLowerHexDigest(summary.ResourceDigest) || !bridgeFindingCapabilityPattern.MatchString(summary.RecommendedCapability) || mode == ModeDisabled {
		return DurableFinding{}, fmt.Errorf("Charlie central finding metadata is invalid")
	}
	if summary.UpdatedAt.After(s.now().UTC().Add(5 * time.Minute)) {
		return DurableFinding{}, fmt.Errorf("Charlie central finding timestamp is invalid")
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return DurableFinding{}, fmt.Errorf("begin Charlie finding sync: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	queries := sqlc.New(tx)
	connection, err := queries.GetActiveCharlieConnection(ctx)
	if err != nil || connection.ID != connectionID || !connection.Active || connection.EmergencyDisabled {
		return DurableFinding{}, fmt.Errorf("Charlie finding connection is inactive")
	}
	liveMode := EffectiveMode(Mode(connection.RequestedMode), Mode(connection.VerifiedMode), connection.EmergencyDisabled)
	if liveMode == ModeDisabled {
		return DurableFinding{}, fmt.Errorf("Charlie finding connection is inactive")
	}
	mode = centralFindingMode(summary.BlockCode, liveMode)
	session, err := queries.GetCharlieSession(ctx, sessionID)
	if err != nil || session.ConnectionID != connectionID || session.CharlieSessionID != summary.SessionID || session.State == "aborted" || session.State == "failed" {
		return DurableFinding{}, fmt.Errorf("Charlie finding session is unavailable")
	}
	resources, err := queries.ListCharlieSessionResources(ctx, session.ID)
	if err != nil || len(resources) == 0 {
		return DurableFinding{}, fmt.Errorf("Charlie finding scope is unavailable")
	}
	target, ok := exactFindingResource(resources, summary.ResourceDigest)
	if !ok {
		return DurableFinding{}, fmt.Errorf("Charlie finding target is unavailable")
	}
	prior, priorErr := queries.GetCharlieFindingByCentralID(ctx, sqlc.GetCharlieFindingByCentralIDParams{ConnectionID: connectionID, CharlieFindingID: summary.FindingID})
	if priorErr != nil && !errors.Is(priorErr, pgx.ErrNoRows) {
		return DurableFinding{}, fmt.Errorf("load Charlie finding replay state: %w", priorErr)
	}
	if priorErr == nil {
		if centralFindingReplay(prior, summary) {
			return durableCentralFinding(prior, false), nil
		}
	}
	status := centralFindingStatus(summary.Status, summary.WorkflowState)
	title, localSummary := centralFindingDisplay(summary.Severity, summary.BlockCode)
	source := "user"
	if session.Source == "event" {
		source = "trigger"
	}
	row, err := queries.UpsertSyncedCharlieFinding(ctx, sqlc.UpsertSyncedCharlieFindingParams{
		ConnectionID: connectionID, CharlieFindingID: summary.FindingID,
		SessionID: pgtype.UUID{Bytes: session.ID, Valid: true}, Source: source,
		Severity: summary.Severity, Status: status, EffectiveMode: string(mode),
		WorkflowState:      summary.WorkflowState,
		ExecutionBlockCode: summary.BlockCode,
		DedupeFingerprint:  stableFingerprint("central", connectionID.String(), session.ID.String(), summary.DeduplicationKey),
		CentralRepeatCount: summary.RepeatCount,
		Title:              title, Summary: localSummary,
		RecommendedActionLabel: "Review " + summary.RecommendedCapability,
		VerificationSummary:    "Re-read the affected Astronomer resource before any action",
		CentralUpdatedAt:       summary.UpdatedAt.UTC(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		row, err = queries.GetCharlieFindingByCentralID(ctx, sqlc.GetCharlieFindingByCentralIDParams{ConnectionID: connectionID, CharlieFindingID: summary.FindingID})
		if err == nil {
			return durableCentralFinding(row, false), nil
		}
	}
	if err != nil {
		return DurableFinding{}, fmt.Errorf("persist Charlie finding summary: %w", err)
	}
	// Replace inherited session scope with the one digest-bound action target in
	// the same transaction. A failure rolls back everything, so no ambiguous or
	// partially scoped row can render or alert.
	if _, err := tx.Exec(ctx, `DELETE FROM charlie_finding_resources WHERE finding_id=$1`, row.ID); err != nil {
		return DurableFinding{}, fmt.Errorf("replace Charlie finding scope: %w", err)
	}
	if err := queries.AddCharlieFindingResource(ctx, sqlc.AddCharlieFindingResourceParams{
		FindingID: row.ID, ResourceType: target.ResourceType,
		ResourceID: target.ResourceID, RequiredVerb: target.RequiredVerb,
	}); err != nil {
		return DurableFinding{}, fmt.Errorf("inherit Charlie finding scope: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return DurableFinding{}, fmt.Errorf("commit Charlie finding sync: %w", err)
	}
	notify := shouldNotifyCentralFinding(prior, priorErr, summary, status, s.now().UTC())
	return durableCentralFinding(row, notify), nil
}

func centralFindingReplay(prior sqlc.CharlieFinding, summary BridgeFindingSummary) bool {
	return !summary.UpdatedAt.After(prior.UpdatedAt)
}

func shouldNotifyCentralFinding(prior sqlc.CharlieFinding, priorErr error, summary BridgeFindingSummary, status string, now time.Time) bool {
	_ = now
	// Product alert policy owns thresholds and deduplication. The sync layer
	// merely says whether this was a new actionable central revision.
	return centralFindingActionable(status, summary.BlockCode) && (errors.Is(priorErr, pgx.ErrNoRows) || summary.UpdatedAt.After(prior.UpdatedAt))
}

func durableCentralFinding(row sqlc.CharlieFinding, notify bool) DurableFinding {
	return DurableFinding{ID: row.ID.String(), Fingerprint: row.DedupeFingerprint, Status: row.Status, RepeatCount: int(row.RepeatCount), UpdatedAt: row.UpdatedAt, Notify: notify}
}

func centralFindingDisplay(severity, blockCode string) (string, string) {
	return "Charlie finding requires attention",
		fmt.Sprintf("Charlie recorded a %s finding. No action ran because the product policy state is %s.", severity, strings.ReplaceAll(blockCode, "_", " "))
}

func centralFindingMode(blockCode string, fallback Mode) Mode {
	if blockCode == "read_only" {
		return ModeReadOnly
	}
	if strings.HasPrefix(blockCode, "approval_") {
		return ModeApproval
	}
	return fallback
}

func centralFindingStatus(status, workflow string) string {
	if workflow == "expired" {
		return "expired"
	}
	if status == "reopened" {
		return "open"
	}
	return status
}

func centralFindingActionable(status, blockCode string) bool {
	return (status == "open" || status == "acknowledged") && IsActionableNonExecutionReason(DenialCode(blockCode))
}

func validCentralFindingSeverity(value string) bool {
	return value == "info" || value == "low" || value == "medium" || value == "high" || value == "critical"
}

func validCentralFindingStatus(value string) bool {
	return value == "open" || value == "acknowledged" || value == "dismissed" || value == "resolved" || value == "reopened"
}

func validCentralFindingBlockCode(value string) bool {
	return IsBoundedNonExecutionReason(DenialCode(value))
}
