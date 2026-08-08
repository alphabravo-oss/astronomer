package charlie

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type alertPlannerFake struct {
	connection   sqlc.CharlieConnection
	policy       sqlc.CharlieAlertPolicy
	channels     []sqlc.NotificationChannel
	candidates   []sqlc.ListCharlieAlertReconcileCandidatesRow
	policyErr    error
	created      map[string]sqlc.CreateCharlieAlertDeliveryWithOutboxParams
	createCalls  int
	failCreateAt int
}

func (f *alertPlannerFake) GetActiveCharlieConnection(context.Context) (sqlc.CharlieConnection, error) {
	return f.connection, nil
}
func (f *alertPlannerFake) GetCharlieAlertPolicy(context.Context, uuid.UUID) (sqlc.CharlieAlertPolicy, error) {
	return f.policy, f.policyErr
}
func (f *alertPlannerFake) ListCharlieAlertPolicyChannels(context.Context, uuid.UUID) ([]sqlc.NotificationChannel, error) {
	return f.channels, nil
}
func (f *alertPlannerFake) ListCharlieAlertReconcileCandidates(context.Context, int32) ([]sqlc.ListCharlieAlertReconcileCandidatesRow, error) {
	return f.candidates, nil
}
func (f *alertPlannerFake) CreateCharlieAlertDeliveryWithOutbox(_ context.Context, arg sqlc.CreateCharlieAlertDeliveryWithOutboxParams) (sqlc.CreateCharlieAlertDeliveryWithOutboxRow, error) {
	f.createCalls++
	if f.failCreateAt > 0 && f.createCalls == f.failCreateAt {
		return sqlc.CreateCharlieAlertDeliveryWithOutboxRow{}, errors.New("partial persistence outage")
	}
	key := fmt.Sprintf("%s:%s:%s:%d", arg.FindingID, uuid.UUID(arg.NotificationChannelID.Bytes), arg.DeliveryKind, arg.DedupeBucket)
	if _, exists := f.created[key]; exists {
		return sqlc.CreateCharlieAlertDeliveryWithOutboxRow{}, pgx.ErrNoRows
	}
	f.created[key] = arg
	return sqlc.CreateCharlieAlertDeliveryWithOutboxRow{ID: arg.ID}, nil
}

func TestFindingAlertPlannerReconcileCompletesPartialChannelPlan(t *testing.T) {
	findingID, channelA, channelB := uuid.New(), uuid.New(), uuid.New()
	fake := &alertPlannerFake{
		connection: sqlc.CharlieConnection{ID: uuid.New(), Active: true},
		policy:     sqlc.CharlieAlertPolicy{Enabled: true, MinimumSeverity: "medium", DedupeWindowSeconds: 1800, QuietHoursTimezone: "UTC", Revision: 1},
		channels: []sqlc.NotificationChannel{
			{ID: channelA, Enabled: true, ChannelType: "webhook"},
			{ID: channelB, Enabled: true, ChannelType: "webhook"},
		},
		candidates:   []sqlc.ListCharlieAlertReconcileCandidatesRow{{FindingID: findingID, Severity: "high", Status: "open", ResourceType: "installation", ResourceID: "i"}},
		created:      map[string]sqlc.CreateCharlieAlertDeliveryWithOutboxParams{},
		failCreateAt: 2,
	}
	planner := &FindingAlertPlanner{queries: fake, now: func() time.Time { return time.Unix(3600, 0).UTC() }}
	alert := FindingAlert{FindingID: findingID.String(), Severity: "high", Status: "open", ResourceType: "installation", ResourceID: "i"}
	if err := planner.Plan(context.Background(), alert); err == nil || len(fake.created) != 1 {
		t.Fatalf("partial plan err=%v deliveries=%d", err, len(fake.created))
	}
	fake.failCreateAt = 0
	if err := planner.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := planner.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(fake.created) != 2 {
		t.Fatalf("reconciled deliveries=%d want one per channel", len(fake.created))
	}
}

func TestFindingAlertPlannerOutageRecoveryQueuesExactlyOnce(t *testing.T) {
	findingID, channelID := uuid.New(), uuid.New()
	fake := &alertPlannerFake{
		connection: sqlc.CharlieConnection{ID: uuid.New(), Active: true},
		policy:     sqlc.CharlieAlertPolicy{Enabled: true, MinimumSeverity: "medium", DedupeWindowSeconds: 1800, EscalationAfterSeconds: 0, QuietHoursTimezone: "UTC", Revision: 1},
		channels:   []sqlc.NotificationChannel{{ID: channelID, Enabled: true, ChannelType: "webhook"}},
		created:    map[string]sqlc.CreateCharlieAlertDeliveryWithOutboxParams{},
		policyErr:  errors.New("database-secret-SENTINEL"),
	}
	planner := &FindingAlertPlanner{queries: fake, now: func() time.Time { return time.Unix(3600, 0).UTC() }}
	alert := FindingAlert{FindingID: findingID.String(), Severity: "high", Status: "open", ResourceType: "installation", ResourceID: "i"}
	if err := planner.Plan(context.Background(), alert); err == nil || len(fake.created) != 0 {
		t.Fatalf("outage result err=%v deliveries=%d", err, len(fake.created))
	}
	fake.policyErr = nil
	fake.candidates = []sqlc.ListCharlieAlertReconcileCandidatesRow{{FindingID: findingID, Severity: "high", Status: "open", ResourceType: "installation", ResourceID: "i"}}
	if err := planner.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := planner.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(fake.created) != 1 {
		t.Fatalf("recovery deliveries=%d want exactly one", len(fake.created))
	}
}

func TestFindingAlertPlannerDisabledConnectionCreatesNothing(t *testing.T) {
	fake := &alertPlannerFake{
		connection: sqlc.CharlieConnection{ID: uuid.New(), Active: true, RequestedMode: "disabled", VerifiedMode: "read_only"},
		policy:     sqlc.CharlieAlertPolicy{Enabled: true, MinimumSeverity: "info", DedupeWindowSeconds: 1800, QuietHoursTimezone: "UTC", Revision: 1},
		channels:   []sqlc.NotificationChannel{{ID: uuid.New(), Enabled: true, ChannelType: "webhook"}},
		created:    map[string]sqlc.CreateCharlieAlertDeliveryWithOutboxParams{},
	}
	planner := &FindingAlertPlanner{queries: fake, now: time.Now}
	if err := planner.Plan(context.Background(), FindingAlert{FindingID: uuid.NewString(), Severity: "critical", Status: "open"}); err != nil {
		t.Fatal(err)
	}
	if len(fake.created) != 0 {
		t.Fatalf("disabled connection queued %d deliveries", len(fake.created))
	}
}

func TestPolicyPublisherStillPublishesInAppWhenPlannerFails(t *testing.T) {
	bus := events.NewBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sub := bus.Subscribe(ctx)
	fake := &alertPlannerFake{connection: sqlc.CharlieConnection{ID: uuid.New(), Active: true}, policyErr: errors.New("planner unavailable"), created: map[string]sqlc.CreateCharlieAlertDeliveryWithOutboxParams{}}
	publisher := NewPolicyFindingPublisher(bus, &FindingAlertPlanner{queries: fake, now: time.Now})
	alert := FindingAlert{FindingID: uuid.NewString(), Severity: "high", Status: "open"}
	if err := publisher.PublishCharlieFinding(context.Background(), alert); err == nil {
		t.Fatal("expected planner error")
	}
	select {
	case event := <-sub:
		if event.Type != events.TypeCharlieFindingChanged {
			t.Fatalf("event type=%s", event.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("in-app event was suppressed by planner outage")
	}
}

func TestQuietHoursEndOvernight(t *testing.T) {
	policy := sqlc.CharlieAlertPolicy{QuietHoursEnabled: true, QuietHoursStart: "22:00", QuietHoursEnd: "07:00", QuietHoursTimezone: "UTC"}
	got := quietHoursEnd(time.Date(2026, 8, 6, 23, 0, 0, 0, time.UTC), policy)
	want := time.Date(2026, 8, 7, 7, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("quiet end=%s want %s", got, want)
	}
}

func TestFindingAlertPlannerUsesBoundedContentFreeDeepLink(t *testing.T) {
	findingID := uuid.New()
	fake := &alertPlannerFake{
		connection: sqlc.CharlieConnection{ID: uuid.New(), Active: true},
		policy:     sqlc.CharlieAlertPolicy{Enabled: true, MinimumSeverity: "info", DedupeWindowSeconds: 1800, QuietHoursTimezone: "UTC", Revision: 1},
		channels:   []sqlc.NotificationChannel{{ID: uuid.New(), Enabled: true, ChannelType: "webhook"}},
		created:    map[string]sqlc.CreateCharlieAlertDeliveryWithOutboxParams{},
	}
	planner := &FindingAlertPlanner{queries: fake, now: time.Now}
	if err := planner.Plan(context.Background(), FindingAlert{FindingID: findingID.String(), Severity: "critical", Status: "open", ResourceID: "payload-SENTINEL"}); err != nil {
		t.Fatal(err)
	}
	if len(fake.created) != 1 {
		t.Fatalf("deliveries=%d", len(fake.created))
	}
	for _, delivery := range fake.created {
		want := "/dashboard/charlie?tab=findings&finding=" + findingID.String()
		if delivery.DeepLink != want || len(delivery.DeepLink) > 256 || len(delivery.Subject) > 256 || len(delivery.Body) > 1024 || strings.Contains(delivery.Body, "payload-SENTINEL") {
			t.Fatalf("unbounded delivery deep_link=%q subject=%q body=%q", delivery.DeepLink, delivery.Subject, delivery.Body)
		}
	}
}
