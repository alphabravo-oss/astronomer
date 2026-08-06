package charlie

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AdminAlertChannelView struct {
	ID                    string `json:"id"`
	Name                  string `json:"name"`
	Type                  string `json:"type"`
	Enabled               bool   `json:"enabled"`
	DestinationConfigured bool   `json:"destination_configured"`
}

type AdminAlertPolicyView struct {
	Enabled                bool                    `json:"enabled"`
	MinimumSeverity        string                  `json:"minimum_severity"`
	DedupeWindowSeconds    int32                   `json:"dedupe_window_seconds"`
	EscalationAfterSeconds int32                   `json:"escalation_after_seconds"`
	QuietHoursEnabled      bool                    `json:"quiet_hours_enabled"`
	QuietHoursStart        string                  `json:"quiet_hours_start"`
	QuietHoursEnd          string                  `json:"quiet_hours_end"`
	QuietHoursTimezone     string                  `json:"quiet_hours_timezone"`
	Revision               int64                   `json:"revision"`
	ChannelIDs             []string                `json:"channel_ids"`
	Channels               []AdminAlertChannelView `json:"channels"`
	AvailableChannels      []AdminAlertChannelView `json:"available_channels"`
	InAppEnabled           bool                    `json:"in_app_enabled"`
}

// AdminAlertPolicyInput contains only product-local routing policy. Channel
// IDs refer to Astronomer's notification_channels rows; destination values and
// credentials never cross this boundary.
// openapi:request CharlieAdminAlertPolicyInput
type AdminAlertPolicyInput struct {
	Revision               int64    `json:"revision"`
	Enabled                bool     `json:"enabled"`
	MinimumSeverity        string   `json:"minimum_severity"`
	DedupeWindowSeconds    int32    `json:"dedupe_window_seconds"`
	EscalationAfterSeconds int32    `json:"escalation_after_seconds"`
	QuietHoursEnabled      bool     `json:"quiet_hours_enabled"`
	QuietHoursStart        string   `json:"quiet_hours_start"`
	QuietHoursEnd          string   `json:"quiet_hours_end"`
	QuietHoursTimezone     string   `json:"quiet_hours_timezone"`
	ChannelIDs             []string `json:"channel_ids"`
}

type alertPlannerQueries interface {
	GetActiveCharlieConnection(context.Context) (sqlc.CharlieConnection, error)
	GetCharlieAlertPolicy(context.Context, uuid.UUID) (sqlc.CharlieAlertPolicy, error)
	ListCharlieAlertPolicyChannels(context.Context, uuid.UUID) ([]sqlc.NotificationChannel, error)
	CreateCharlieAlertDeliveryWithOutbox(context.Context, sqlc.CreateCharlieAlertDeliveryWithOutboxParams) (sqlc.CreateCharlieAlertDeliveryWithOutboxRow, error)
	ListCharlieAlertReconcileCandidates(context.Context, int32) ([]sqlc.ListCharlieAlertReconcileCandidatesRow, error)
}

type FindingAlertPlanner struct {
	queries alertPlannerQueries
	now     func() time.Time
}

func NewFindingAlertPlanner(pool *pgxpool.Pool) (*FindingAlertPlanner, error) {
	if pool == nil {
		return nil, fmt.Errorf("Charlie alert database is unavailable")
	}
	return &FindingAlertPlanner{queries: sqlc.New(pool), now: time.Now}, nil
}

func (p *FindingAlertPlanner) Reconcile(ctx context.Context) error {
	if p == nil || p.queries == nil {
		return fmt.Errorf("Charlie alert planner is unavailable")
	}
	rows, err := p.queries.ListCharlieAlertReconcileCandidates(ctx, 100)
	if err != nil {
		return fmt.Errorf("Charlie alert reconciliation is unavailable")
	}
	for _, row := range rows {
		if err := p.Plan(ctx, FindingAlert{
			FindingID: row.FindingID.String(), Severity: row.Severity, Status: row.Status,
			ResourceType: row.ResourceType, ResourceID: row.ResourceID,
			BlockCode: row.ExecutionBlockCode, RepeatCount: int(row.RepeatCount),
		}); err != nil {
			return fmt.Errorf("Charlie alert reconciliation is unavailable")
		}
	}
	return nil
}

// Plan evaluates only Astronomer-owned notification policy. It creates a
// delivery row and a durable task-outbox intent together; it never creates an
// approval, invokes a capability, or dispatches a product action.
func (p *FindingAlertPlanner) Plan(ctx context.Context, alert FindingAlert) error {
	if p == nil || p.queries == nil {
		return fmt.Errorf("Charlie alert planner is unavailable")
	}
	findingID, err := uuid.Parse(alert.FindingID)
	if err != nil {
		return fmt.Errorf("Charlie alert finding ID is invalid")
	}
	connection, err := p.queries.GetActiveCharlieConnection(ctx)
	if err != nil || !connection.Active || connection.EmergencyDisabled || connection.RequestedMode == string(ModeDisabled) || connection.VerifiedMode == string(ModeDisabled) {
		return nil
	}
	policy, err := p.queries.GetCharlieAlertPolicy(ctx, connection.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("Charlie alert policy is unavailable")
	}
	if !policy.Enabled || !severityAtLeast(alert.Severity, policy.MinimumSeverity) || (alert.Status != "open" && alert.Status != "acknowledged") {
		return nil
	}
	channels, err := p.queries.ListCharlieAlertPolicyChannels(ctx, connection.ID)
	if err != nil {
		return fmt.Errorf("Charlie alert channels are unavailable")
	}
	now := p.now().UTC()
	base := quietHoursEnd(now, policy)
	bucket := now.Unix() / int64(policy.DedupeWindowSeconds)
	deepLink := "/dashboard/charlie?tab=findings&finding=" + findingID.String()
	for _, channel := range channels {
		if !channel.Enabled {
			continue
		}
		if err := p.create(ctx, connection.ID, findingID, channel.ID, policy, "initial", bucket, alert.Severity, base, deepLink); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if policy.EscalationAfterSeconds > 0 {
			due := quietHoursEnd(now.Add(time.Duration(policy.EscalationAfterSeconds)*time.Second), policy)
			if err := p.create(ctx, connection.ID, findingID, channel.ID, policy, "escalation", 0, escalatedSeverity(alert.Severity), due, deepLink); err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
	}
	return nil
}

func (p *FindingAlertPlanner) create(ctx context.Context, connectionID, findingID, channelID uuid.UUID, policy sqlc.CharlieAlertPolicy, kind string, bucket int64, severity string, due time.Time, deepLink string) error {
	label := "Charlie finding requires attention"
	if kind == "escalation" {
		label = "Charlie finding remains unresolved"
	}
	body := "Review the durable finding in Astronomer. No approval or product action is implied by this notification. " + deepLink
	_, err := p.queries.CreateCharlieAlertDeliveryWithOutbox(ctx, sqlc.CreateCharlieAlertDeliveryWithOutboxParams{
		ID: uuid.New(), ConnectionID: connectionID, FindingID: findingID,
		NotificationChannelID: pgtype.UUID{Bytes: channelID, Valid: true},
		PolicyRevision:        policy.Revision, DeliveryKind: kind, DedupeBucket: bucket,
		Severity: severity, NextAttemptAt: due, DeepLink: deepLink, Subject: label, Body: body,
	})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("Charlie alert delivery persistence is unavailable")
	}
	return err
}

func escalatedSeverity(value string) string {
	switch value {
	case "info":
		return "low"
	case "low":
		return "medium"
	case "medium", "warning":
		return "high"
	default:
		return "critical"
	}
}

func quietHoursEnd(at time.Time, policy sqlc.CharlieAlertPolicy) time.Time {
	if !policy.QuietHoursEnabled {
		return at.UTC()
	}
	location, err := time.LoadLocation(policy.QuietHoursTimezone)
	if err != nil {
		return at.UTC()
	}
	start, okStart := parseClock(policy.QuietHoursStart)
	end, okEnd := parseClock(policy.QuietHoursEnd)
	if !okStart || !okEnd || start == end {
		return at.UTC()
	}
	local := at.In(location)
	minute := local.Hour()*60 + local.Minute()
	quiet := false
	if start < end {
		quiet = minute >= start && minute < end
	} else {
		quiet = minute >= start || minute < end
	}
	if !quiet {
		return at.UTC()
	}
	endAt := time.Date(local.Year(), local.Month(), local.Day(), end/60, end%60, 0, 0, location)
	if !endAt.After(local) {
		endAt = endAt.AddDate(0, 0, 1)
	}
	return endAt.UTC()
}

func parseClock(value string) (int, bool) {
	parsed, err := time.Parse("15:04", strings.TrimSpace(value))
	if err != nil {
		return 0, false
	}
	return parsed.Hour()*60 + parsed.Minute(), true
}

func validAdminAlertPolicy(input AdminAlertPolicyInput) bool {
	if !severityAtLeast(input.MinimumSeverity, "info") || input.DedupeWindowSeconds < 60 || input.DedupeWindowSeconds > 604800 ||
		(input.EscalationAfterSeconds != 0 && (input.EscalationAfterSeconds < 300 || input.EscalationAfterSeconds > 604800)) {
		return false
	}
	if _, ok := parseClock(input.QuietHoursStart); !ok {
		return false
	}
	if _, ok := parseClock(input.QuietHoursEnd); !ok {
		return false
	}
	if len(input.QuietHoursTimezone) > 64 {
		return false
	}
	if _, err := time.LoadLocation(input.QuietHoursTimezone); err != nil {
		return false
	}
	if len(input.ChannelIDs) > 32 {
		return false
	}
	seen := map[uuid.UUID]struct{}{}
	for _, raw := range input.ChannelIDs {
		id, err := uuid.Parse(raw)
		if err != nil {
			return false
		}
		if _, exists := seen[id]; exists {
			return false
		}
		seen[id] = struct{}{}
	}
	return true
}

func alertChannelView(row sqlc.NotificationChannel) AdminAlertChannelView {
	return AdminAlertChannelView{ID: row.ID.String(), Name: row.Name, Type: row.ChannelType, Enabled: row.Enabled, DestinationConfigured: len(row.Configuration) > 2}
}

func sortAlertChannels(items []AdminAlertChannelView) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Name == items[j].Name {
			return items[i].ID < items[j].ID
		}
		return items[i].Name < items[j].Name
	})
}

func (s *AdminService) AlertPolicy(ctx context.Context) (AdminAlertPolicyView, error) {
	connection, err := s.connection(ctx)
	if err != nil {
		return AdminAlertPolicyView{}, err
	}
	policy, err := s.queries.GetCharlieAlertPolicy(ctx, connection.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		policy = sqlc.CharlieAlertPolicy{ConnectionID: connection.ID, Enabled: true, MinimumSeverity: "medium", DedupeWindowSeconds: 1800, EscalationAfterSeconds: 3600, QuietHoursStart: "22:00", QuietHoursEnd: "07:00", QuietHoursTimezone: "UTC", Revision: 0}
	} else if err != nil {
		return AdminAlertPolicyView{}, ErrAdminUnavailable
	}
	selected, err := s.queries.ListCharlieAlertPolicyChannels(ctx, connection.ID)
	if err != nil {
		return AdminAlertPolicyView{}, ErrAdminUnavailable
	}
	available, err := s.queries.ListCharlieAlertAvailableChannels(ctx)
	if err != nil {
		return AdminAlertPolicyView{}, ErrAdminUnavailable
	}
	view := AdminAlertPolicyView{
		Enabled: policy.Enabled, MinimumSeverity: policy.MinimumSeverity,
		DedupeWindowSeconds: policy.DedupeWindowSeconds, EscalationAfterSeconds: policy.EscalationAfterSeconds,
		QuietHoursEnabled: policy.QuietHoursEnabled, QuietHoursStart: strings.TrimSpace(policy.QuietHoursStart),
		QuietHoursEnd: strings.TrimSpace(policy.QuietHoursEnd), QuietHoursTimezone: policy.QuietHoursTimezone,
		Revision: policy.Revision, ChannelIDs: []string{}, Channels: []AdminAlertChannelView{},
		AvailableChannels: []AdminAlertChannelView{}, InAppEnabled: true,
	}
	for _, row := range selected {
		view.ChannelIDs = append(view.ChannelIDs, row.ID.String())
		view.Channels = append(view.Channels, alertChannelView(row))
	}
	for _, row := range available {
		view.AvailableChannels = append(view.AvailableChannels, alertChannelView(row))
	}
	sort.Strings(view.ChannelIDs)
	sortAlertChannels(view.Channels)
	sortAlertChannels(view.AvailableChannels)
	return view, nil
}

func (s *AdminService) UpdateAlertPolicy(ctx context.Context, input AdminAlertPolicyInput, actor uuid.UUID) (AdminAlertPolicyView, error) {
	if s == nil || s.pool == nil || actor == uuid.Nil || input.Revision < 0 || !validAdminAlertPolicy(input) {
		return AdminAlertPolicyView{}, ErrAdminConflict
	}
	connection, err := s.connection(ctx)
	if err != nil {
		return AdminAlertPolicyView{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return AdminAlertPolicyView{}, ErrAdminUnavailable
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	queries := s.queries.WithTx(tx)
	available, err := queries.ListCharlieAlertAvailableChannels(ctx)
	if err != nil {
		return AdminAlertPolicyView{}, ErrAdminUnavailable
	}
	allowed := make(map[uuid.UUID]struct{}, len(available))
	for _, channel := range available {
		allowed[channel.ID] = struct{}{}
	}
	channelIDs := make([]uuid.UUID, 0, len(input.ChannelIDs))
	for _, raw := range input.ChannelIDs {
		id, _ := uuid.Parse(raw)
		if _, ok := allowed[id]; !ok {
			return AdminAlertPolicyView{}, ErrAdminConflict
		}
		channelIDs = append(channelIDs, id)
	}
	if _, err := queries.UpsertCharlieAlertPolicy(ctx, sqlc.UpsertCharlieAlertPolicyParams{
		ConnectionID: connection.ID, Enabled: input.Enabled, MinimumSeverity: input.MinimumSeverity,
		DedupeWindowSeconds: input.DedupeWindowSeconds, EscalationAfterSeconds: input.EscalationAfterSeconds,
		QuietHoursEnabled: input.QuietHoursEnabled, QuietHoursStart: input.QuietHoursStart,
		QuietHoursEnd: input.QuietHoursEnd, QuietHoursTimezone: input.QuietHoursTimezone,
		UpdatedByID: pgtype.UUID{Bytes: actor, Valid: true}, ExpectedRevision: input.Revision,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AdminAlertPolicyView{}, ErrAdminConflict
		}
		return AdminAlertPolicyView{}, ErrAdminUnavailable
	}
	if err := queries.DeleteCharlieAlertPolicyChannels(ctx, connection.ID); err != nil {
		return AdminAlertPolicyView{}, ErrAdminUnavailable
	}
	for _, id := range channelIDs {
		if err := queries.AddCharlieAlertPolicyChannel(ctx, sqlc.AddCharlieAlertPolicyChannelParams{ConnectionID: connection.ID, NotificationChannelID: id}); err != nil {
			return AdminAlertPolicyView{}, ErrAdminUnavailable
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return AdminAlertPolicyView{}, ErrAdminConflict
	}
	return s.AlertPolicy(ctx)
}
