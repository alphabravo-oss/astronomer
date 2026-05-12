package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/webhook"
)

// fakeWebhookQuerier records calls so the test can assert on the
// status transition the dispatcher chose.
type fakeWebhookQuerier struct {
	mu sync.Mutex

	pending []sqlc.WebhookDelivery
	subs    map[uuid.UUID]sqlc.WebhookSubscription

	delivered []sqlc.MarkWebhookDeliveryDeliveredParams
	failed    []sqlc.MarkWebhookDeliveryFailedParams
	dropped   []sqlc.MarkWebhookDeliveryDroppedParams
}

func (f *fakeWebhookQuerier) ListPendingWebhookDeliveries(_ context.Context, _ sqlc.ListPendingWebhookDeliveriesParams) ([]sqlc.WebhookDelivery, error) {
	return f.pending, nil
}

func (f *fakeWebhookQuerier) GetWebhookSubscription(_ context.Context, id uuid.UUID) (sqlc.WebhookSubscription, error) {
	sub, ok := f.subs[id]
	if !ok {
		return sqlc.WebhookSubscription{}, errors.New("not found")
	}
	return sub, nil
}

func (f *fakeWebhookQuerier) MarkWebhookDeliveryDelivered(_ context.Context, arg sqlc.MarkWebhookDeliveryDeliveredParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.delivered = append(f.delivered, arg)
	return nil
}

func (f *fakeWebhookQuerier) MarkWebhookDeliveryFailed(_ context.Context, arg sqlc.MarkWebhookDeliveryFailedParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failed = append(f.failed, arg)
	return nil
}

func (f *fakeWebhookQuerier) MarkWebhookDeliveryDropped(_ context.Context, arg sqlc.MarkWebhookDeliveryDroppedParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dropped = append(f.dropped, arg)
	return nil
}

func (f *fakeWebhookQuerier) DeleteWebhookDeliveriesOlderThan(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}

// fakeWebhookSender returns a programmed Outcome every time.
type fakeWebhookSender struct {
	out  webhook.Outcome
	err  error
	size int
}

func (f *fakeWebhookSender) Send(_ context.Context, _ webhook.Subscription, _ webhook.Event) (webhook.Outcome, int, error) {
	return f.out, f.size, f.err
}

func newDelivery(t *testing.T, subID uuid.UUID, attempts int) sqlc.WebhookDelivery {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{
		"event_name": "audit.user.login",
		"event_id":   "42",
		"timestamp":  time.Now().UTC(),
	})
	return sqlc.WebhookDelivery{
		ID:             uuid.New(),
		SubscriptionID: subID,
		EventName:      "audit.user.login",
		EventID:        "42",
		Payload:        payload,
		PayloadSize:    int32(len(payload)),
		Status:         "queued",
		Attempts:       int32(attempts),
	}
}

func newSubscription(id uuid.UUID, maxRetries int) sqlc.WebhookSubscription {
	return sqlc.WebhookSubscription{
		ID:              id,
		Name:            "test",
		Url:             "http://example.invalid/hook",
		SecretEncrypted: "", // no Fernet — bypasses decrypt path
		EventFilters:    json.RawMessage(`["*"]`),
		ExtraHeaders:    json.RawMessage(`{}`),
		Enabled:         true,
		MaxRetries:      int32(maxRetries),
		TimeoutSeconds:  10,
	}
}

func setupDispatch(t *testing.T, sender *fakeWebhookSender, q *fakeWebhookQuerier) {
	t.Helper()
	ConfigureWebhook(WebhookDeps{
		Queries: q,
		Sender:  sender,
	})
	// Drop runtime deps so HandleWebhookDispatch doesn't try the leader
	// path against a non-existent DB.
	resetRuntime()
	t.Cleanup(func() {
		ConfigureWebhook(WebhookDeps{})
		resetRuntime()
	})
}

func TestDispatcher_BatchProcessesPending_Delivered(t *testing.T) {
	subID := uuid.New()
	q := &fakeWebhookQuerier{
		subs: map[uuid.UUID]sqlc.WebhookSubscription{
			subID: newSubscription(subID, 5),
		},
		pending: []sqlc.WebhookDelivery{
			newDelivery(t, subID, 0),
			newDelivery(t, subID, 0),
		},
	}
	sender := &fakeWebhookSender{out: webhook.Outcome{Status: http.StatusOK}}
	setupDispatch(t, sender, q)

	if err := HandleWebhookDispatch(context.Background(), nil); err != nil {
		t.Fatalf("HandleWebhookDispatch: %v", err)
	}

	if got, want := len(q.delivered), 2; got != want {
		t.Errorf("delivered=%d want %d", got, want)
	}
	if got := len(q.failed); got != 0 {
		t.Errorf("failed=%d want 0", got)
	}
	if got := len(q.dropped); got != 0 {
		t.Errorf("dropped=%d want 0", got)
	}
}

func TestDispatcher_BackoffSchedule_RetryableFailure(t *testing.T) {
	subID := uuid.New()
	q := &fakeWebhookQuerier{
		subs: map[uuid.UUID]sqlc.WebhookSubscription{
			subID: newSubscription(subID, 5),
		},
		pending: []sqlc.WebhookDelivery{
			newDelivery(t, subID, 0), // first attempt → backoff[0] = 30s
		},
	}
	// 5xx is retryable; the dispatcher should reschedule with the
	// backoff slot.
	sender := &fakeWebhookSender{out: webhook.Outcome{Status: http.StatusInternalServerError, ResponseBody: "boom"}}
	setupDispatch(t, sender, q)

	now := time.Now().UTC()
	if err := HandleWebhookDispatch(context.Background(), nil); err != nil {
		t.Fatalf("HandleWebhookDispatch: %v", err)
	}

	if got := len(q.failed); got != 1 {
		t.Fatalf("failed=%d want 1", got)
	}
	got := q.failed[0]
	if got.Attempts != 1 {
		t.Errorf("attempts after first failure = %d, want 1", got.Attempts)
	}
	if got.ResponseStatus != http.StatusInternalServerError {
		t.Errorf("response_status = %d, want 500", got.ResponseStatus)
	}
	if !got.NextAttemptAt.Valid {
		t.Fatalf("next_attempt_at not set on retryable failure")
	}
	wantDelay := webhook.NextBackoff(1)
	gotDelay := got.NextAttemptAt.Time.Sub(now)
	// Allow generous slack for the test scheduling.
	if gotDelay < wantDelay-time.Second || gotDelay > wantDelay+5*time.Second {
		t.Errorf("backoff slot mismatch: got %v, want ~%v", gotDelay, wantDelay)
	}
}

func TestDispatcher_PermanentFailure_4xx_Dropped(t *testing.T) {
	subID := uuid.New()
	q := &fakeWebhookQuerier{
		subs: map[uuid.UUID]sqlc.WebhookSubscription{
			subID: newSubscription(subID, 5),
		},
		pending: []sqlc.WebhookDelivery{
			newDelivery(t, subID, 0),
		},
	}
	// 404 is a permanent 4xx — must be dropped immediately.
	sender := &fakeWebhookSender{out: webhook.Outcome{Status: http.StatusNotFound, ResponseBody: "no route"}}
	setupDispatch(t, sender, q)

	if err := HandleWebhookDispatch(context.Background(), nil); err != nil {
		t.Fatalf("HandleWebhookDispatch: %v", err)
	}

	if got := len(q.dropped); got != 1 {
		t.Errorf("dropped=%d want 1", got)
	}
	if got := len(q.failed); got != 0 {
		t.Errorf("failed=%d want 0 (4xx must not enter the retry loop)", got)
	}
}

func TestDispatcher_RetryBudgetExhausted_Dropped(t *testing.T) {
	subID := uuid.New()
	q := &fakeWebhookQuerier{
		subs: map[uuid.UUID]sqlc.WebhookSubscription{
			subID: newSubscription(subID, 3), // budget = 3
		},
		pending: []sqlc.WebhookDelivery{
			newDelivery(t, subID, 2), // already failed twice, next is attempt #3
		},
	}
	sender := &fakeWebhookSender{out: webhook.Outcome{Status: http.StatusServiceUnavailable}}
	setupDispatch(t, sender, q)

	if err := HandleWebhookDispatch(context.Background(), nil); err != nil {
		t.Fatalf("HandleWebhookDispatch: %v", err)
	}
	if got := len(q.dropped); got != 1 {
		t.Errorf("dropped=%d want 1", got)
	}
	if got := q.dropped[0].Attempts; got != 3 {
		t.Errorf("dropped row attempts = %d, want 3", got)
	}
}

func TestDispatcher_TransportError_Retries(t *testing.T) {
	subID := uuid.New()
	q := &fakeWebhookQuerier{
		subs: map[uuid.UUID]sqlc.WebhookSubscription{
			subID: newSubscription(subID, 5),
		},
		pending: []sqlc.WebhookDelivery{
			newDelivery(t, subID, 0),
		},
	}
	sender := &fakeWebhookSender{out: webhook.Outcome{Err: errors.New("dial tcp: timeout")}}
	setupDispatch(t, sender, q)

	if err := HandleWebhookDispatch(context.Background(), nil); err != nil {
		t.Fatalf("HandleWebhookDispatch: %v", err)
	}
	if got := len(q.failed); got != 1 {
		t.Errorf("failed=%d want 1 (transport error is retryable)", got)
	}
	if got := len(q.dropped); got != 0 {
		t.Errorf("dropped=%d want 0", got)
	}
}

func TestDispatcher_OversizedPayload_BuildError_Dropped(t *testing.T) {
	subID := uuid.New()
	q := &fakeWebhookQuerier{
		subs: map[uuid.UUID]sqlc.WebhookSubscription{
			subID: newSubscription(subID, 5),
		},
		pending: []sqlc.WebhookDelivery{
			newDelivery(t, subID, 0),
		},
	}
	// A build-side error (template render, oversized payload) must NOT
	// retry: the failure is deterministic on the row.
	sender := &fakeWebhookSender{out: webhook.Outcome{}, err: errors.New("payload too large")}
	setupDispatch(t, sender, q)

	if err := HandleWebhookDispatch(context.Background(), nil); err != nil {
		t.Fatalf("HandleWebhookDispatch: %v", err)
	}
	if got := len(q.dropped); got != 1 {
		t.Errorf("dropped=%d want 1", got)
	}
}

// Smoke test for the parameter wiring on ListPending* — exercising the
// argument shape catches regression on a missed pgtype.Timestamptz
// when() pair.
func TestDispatcher_ListPendingArgShape(t *testing.T) {
	subID := uuid.New()
	q := &recordingPendingQuerier{
		fakeWebhookQuerier: fakeWebhookQuerier{
			subs: map[uuid.UUID]sqlc.WebhookSubscription{subID: newSubscription(subID, 5)},
		},
	}
	setupDispatch(t, &fakeWebhookSender{out: webhook.Outcome{Status: 200}}, &q.fakeWebhookQuerier)
	// override the queries to use the recording querier
	ConfigureWebhook(WebhookDeps{
		Queries: q,
		Sender:  &fakeWebhookSender{out: webhook.Outcome{Status: 200}},
	})
	defer ConfigureWebhook(WebhookDeps{})

	if err := HandleWebhookDispatch(context.Background(), nil); err != nil {
		t.Fatalf("HandleWebhookDispatch: %v", err)
	}
	if !q.gotArg.NextAttemptAt.Valid {
		t.Errorf("ListPendingWebhookDeliveries was called with invalid NextAttemptAt")
	}
	if q.gotArg.Limit != webhookDispatchBatchSize {
		t.Errorf("limit = %d, want %d", q.gotArg.Limit, webhookDispatchBatchSize)
	}
}

type recordingPendingQuerier struct {
	fakeWebhookQuerier
	gotArg sqlc.ListPendingWebhookDeliveriesParams
}

func (r *recordingPendingQuerier) ListPendingWebhookDeliveries(_ context.Context, arg sqlc.ListPendingWebhookDeliveriesParams) ([]sqlc.WebhookDelivery, error) {
	r.gotArg = arg
	return nil, nil
}

// guard against an off-by-one in the next_attempt_at predicate: arg
// must be a valid (Valid=true) Timestamptz so the SQL predicate
// `WHERE next_attempt_at <= $1` doesn't compare against a NULL.
var _ = pgtype.Timestamptz{}
