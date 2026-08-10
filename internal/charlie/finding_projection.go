package charlie

import (
	"context"
	"fmt"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/charlie/contract"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgxpool"
)

const findingProjectionInterval = 30 * time.Second

// FindingProjection continuously closes the gap between findings created by
// unattended central work and Astronomer's local alert/finding view. The
// product agent supplies a signed monotonic summary feed; Astronomer still
// requires an exact local central-session and resource-digest match before it
// persists or alerts on any item.
type FindingProjection struct {
	pool      *pgxpool.Pool
	bridge    FindingChangeBridge
	store     CentralFindingStore
	publisher FindingAlertPublisher
	active    func() bool
	interval  time.Duration
}

func NewFindingProjection(pool *pgxpool.Pool, bridge FindingChangeBridge, store CentralFindingStore, publisher FindingAlertPublisher, active func() bool) (*FindingProjection, error) {
	if pool == nil || bridge == nil || store == nil || publisher == nil || active == nil {
		return nil, fmt.Errorf("Charlie finding projection dependencies are unavailable")
	}
	return &FindingProjection{pool: pool, bridge: bridge, store: store, publisher: publisher, active: active, interval: findingProjectionInterval}, nil
}

func (p *FindingProjection) Run(ctx context.Context) {
	if p == nil {
		return
	}
	_ = p.RunOnce(ctx)
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = p.RunOnce(ctx)
		}
	}
}

func (p *FindingProjection) RunOnce(ctx context.Context) error {
	if p == nil || !p.active() {
		return nil
	}
	connection, err := sqlc.New(p.pool).GetActiveCharlieConnection(ctx)
	if err != nil || !connection.Active || connection.EmergencyDisabled {
		return err
	}
	mode := EffectiveMode(Mode(connection.RequestedMode), Mode(connection.VerifiedMode), connection.EmergencyDisabled)
	if mode == ModeDisabled {
		return nil
	}
	conn, err := p.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	var locked bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1,0))`, "charlie-findings:"+connection.ID.String()).Scan(&locked); err != nil || !locked {
		return err
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock(hashtextextended($1,0))`, "charlie-findings:"+connection.ID.String())
	}()
	_, err = conn.Exec(ctx, `INSERT INTO charlie_finding_projection_cursors(connection_id) VALUES($1) ON CONFLICT DO NOTHING`, connection.ID)
	if err != nil {
		return err
	}
	var cursor int64
	if err := conn.QueryRow(ctx, `SELECT sequence FROM charlie_finding_projection_cursors WHERE connection_id=$1`, connection.ID).Scan(&cursor); err != nil {
		return err
	}
	fail := func(code string, cause error) error {
		_, _ = conn.Exec(ctx, `UPDATE charlie_finding_projection_cursors SET last_error_code=$2,updated_at=now() WHERE connection_id=$1`, connection.ID, code)
		return cause
	}
	for pageNumber := 0; pageNumber < 10; pageNumber++ {
		if !p.active() {
			return nil
		}
		page, pageErr := p.bridge.FindingChanges(ctx, cursor, 100)
		if pageErr != nil {
			return fail("finding_projection.bridge_failed", pageErr)
		}
		prior := cursor
		for _, change := range page.Data {
			if change.Sequence <= prior || change.Revision < 1 || string(change.FindingId) == "" || change.OccurredAt.IsZero() {
				return fail("finding_projection.invalid_change", fmt.Errorf("Charlie finding change is invalid"))
			}
			prior = change.Sequence
			switch change.Operation {
			case contract.FindingChangeDelete:
				if change.Summary != nil {
					return fail("finding_projection.invalid_change", fmt.Errorf("Charlie finding tombstone carried content"))
				}
				_, err = conn.Exec(ctx, `UPDATE charlie_findings SET status='expired',workflow_state='expired',updated_at=now()
					WHERE connection_id=$1 AND charlie_finding_id=$2 AND status NOT IN ('resolved','dismissed','expired')`, connection.ID, string(change.FindingId))
			case contract.FindingChangeUpsert:
				err = p.applyUpsert(ctx, connection, mode, change)
			default:
				err = fmt.Errorf("Charlie finding change operation is invalid")
			}
			if err != nil {
				return fail("finding_projection.apply_failed", err)
			}
		}
		if page.NextSequence != prior || page.NextSequence < cursor || page.HasMore && len(page.Data) != 100 {
			return fail("finding_projection.invalid_cursor", fmt.Errorf("Charlie finding cursor is invalid"))
		}
		result, cursorErr := conn.Exec(ctx, `UPDATE charlie_finding_projection_cursors SET sequence=$2,last_error_code='',updated_at=now()
			WHERE connection_id=$1 AND sequence=$3`, connection.ID, page.NextSequence, cursor)
		if cursorErr != nil {
			return fail("finding_projection.cursor_failed", cursorErr)
		}
		if result.RowsAffected() != 1 {
			return fail("finding_projection.cursor_failed", fmt.Errorf("Charlie finding projection cursor changed concurrently"))
		}
		cursor = page.NextSequence
		if !page.HasMore {
			return nil
		}
	}
	return fail("finding_projection.page_limit", fmt.Errorf("Charlie finding projection exceeded one reconciliation bound"))
}

func (p *FindingProjection) applyUpsert(ctx context.Context, connection sqlc.CharlieConnection, mode Mode, change contract.FindingChange) error {
	if change.Summary == nil || string(change.Summary.FindingId) != string(change.FindingId) || change.Summary.RepeatCount < 1 {
		return fmt.Errorf("Charlie finding projection summary is invalid")
	}
	summary := BridgeFindingSummary{
		FindingID: string(change.Summary.FindingId), SessionID: string(change.Summary.SessionId), InvestigationID: string(change.Summary.InvestigationId),
		DeduplicationKey: change.Summary.DeduplicationKey, RepeatCount: int32(change.Summary.RepeatCount), Severity: string(change.Summary.Severity),
		Status: string(change.Summary.Status), WorkflowState: string(change.Summary.WorkflowState), BlockCode: string(change.Summary.BlockCode),
		UpdatedAt: change.Summary.UpdatedAt.UTC(), ResourceDigest: change.Summary.ResourceDigest, RecommendedCapability: change.Summary.RecommendedCapability,
	}
	if !validBridgeFindingSummary(summary) || !isLowerHexDigest(summary.ResourceDigest) || !bridgeFindingCapabilityPattern.MatchString(summary.RecommendedCapability) {
		return fmt.Errorf("Charlie finding projection summary is invalid")
	}
	queries := sqlc.New(p.pool)
	session, err := queries.GetCharlieSessionByCentralID(ctx, summary.SessionID)
	if err != nil || session.ConnectionID != connection.ID || session.State == "aborted" || session.State == "failed" {
		return fmt.Errorf("Charlie finding projection session is unavailable")
	}
	resources, err := queries.ListCharlieSessionResources(ctx, session.ID)
	if err != nil {
		return err
	}
	target, ok := exactFindingResource(resources, summary.ResourceDigest)
	if !ok {
		return fmt.Errorf("Charlie finding projection resource is unavailable")
	}
	durable, err := p.store.UpsertCentralFinding(ctx, connection.ID, session.ID, summary, centralFindingMode(summary.BlockCode, mode))
	if err != nil {
		return err
	}
	if durable.Notify && p.active() {
		return p.publisher.PublishCharlieFinding(ctx, FindingAlert{FindingID: durable.ID, Severity: summary.Severity,
			Status: centralFindingStatus(summary.Status, summary.WorkflowState), ResourceType: target.ResourceType, ResourceID: target.ResourceID,
			BlockCode: summary.BlockCode, RepeatCount: durable.RepeatCount})
	}
	return nil
}
