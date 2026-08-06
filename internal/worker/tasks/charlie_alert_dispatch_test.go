package tasks

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/charlie"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type charlieAlertDispatchFake struct {
	delivery        sqlc.CharlieAlertDelivery
	currentDelivery sqlc.CharlieAlertDelivery
	claimErr        error
	currentErr      error
	allowed         bool
	allowedErr      error
	finding         sqlc.CharlieFinding
	findingErr      error
	channel         sqlc.NotificationChannel
	channelErr      error
	retry           sqlc.MarkCharlieAlertDeliveryRetryParams
	suppressed      sqlc.SuppressCharlieAlertDeliveryParams
	delivered       bool
	claimCalls      int
}

func configureCharlieAlertDispatchTest(t *testing.T, queries CharlieAlertDispatchQuerier, fence *charlie.WriteFence) {
	t.Helper()
	if fence == nil {
		fence = charlie.NewWriteFence()
	}
	ConfigureCharlieAlertDispatch(queries, fence)
	t.Cleanup(func() { ConfigureCharlieAlertDispatch(nil, nil) })
}

func (f *charlieAlertDispatchFake) ClaimCharlieAlertDelivery(context.Context, uuid.UUID) (sqlc.CharlieAlertDelivery, error) {
	f.claimCalls++
	return f.delivery, f.claimErr
}
func (f *charlieAlertDispatchFake) GetCharlieAlertDelivery(context.Context, uuid.UUID) (sqlc.CharlieAlertDelivery, error) {
	return f.currentDelivery, f.currentErr
}
func (f *charlieAlertDispatchFake) CharlieAlertDeliveryAllowed(context.Context, uuid.UUID) (bool, error) {
	return f.allowed, f.allowedErr
}
func (f *charlieAlertDispatchFake) GetCharlieFinding(context.Context, uuid.UUID) (sqlc.CharlieFinding, error) {
	return f.finding, f.findingErr
}
func (f *charlieAlertDispatchFake) GetNotificationChannelByID(context.Context, uuid.UUID) (sqlc.NotificationChannel, error) {
	return f.channel, f.channelErr
}

func TestCharlieAlertDispatchRetriesProductDatabaseReadOutages(t *testing.T) {
	connectionID, findingID, channelID, deliveryID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	base := &charlieAlertDispatchFake{
		delivery: sqlc.CharlieAlertDelivery{ID: deliveryID, ConnectionID: connectionID, FindingID: findingID, NotificationChannelID: pgtype.UUID{Bytes: channelID, Valid: true}, DeliveryKind: "initial", Severity: "high", AttemptCount: 1, MaximumAttempts: 8, Subject: "Charlie finding", Body: "bounded", CreatedAt: time.Now()},
		finding:  sqlc.CharlieFinding{ID: findingID, ConnectionID: connectionID, Status: "open"},
		channel:  sqlc.NotificationChannel{ID: channelID, Enabled: true, ChannelType: "webhook", Configuration: []byte(`{"url":"https://93.184.216.34/notify"}`)},
		allowed:  true,
	}
	tests := []struct {
		name string
		code string
		set  func(*charlieAlertDispatchFake)
	}{
		{name: "finding", code: "finding_unavailable", set: func(f *charlieAlertDispatchFake) { f.findingErr = errors.New("database-SENTINEL") }},
		{name: "channel", code: "channel_state_unavailable", set: func(f *charlieAlertDispatchFake) { f.channelErr = errors.New("database-SENTINEL") }},
		{name: "policy", code: "policy_unavailable", set: func(f *charlieAlertDispatchFake) { f.allowedErr = errors.New("database-SENTINEL") }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := *base
			tc.set(&fake)
			configureCharlieAlertDispatchTest(t, &fake, nil)
			err := HandleCharlieAlertDispatch(context.Background(), asynq.NewTask(CharlieAlertDispatchTaskType, []byte(`{"delivery_id":"`+deliveryID.String()+`"}`)))
			if err == nil || err.Error() != "Charlie alert error: "+tc.code || strings.Contains(err.Error(), "SENTINEL") || fake.retry.LastErrorCode != tc.code {
				t.Fatalf("read outage err=%v retry=%q", err, fake.retry.LastErrorCode)
			}
		})
	}
}
func (f *charlieAlertDispatchFake) MarkCharlieAlertDeliveryDelivered(context.Context, uuid.UUID) error {
	f.delivered = true
	return nil
}
func (f *charlieAlertDispatchFake) MarkCharlieAlertDeliveryRetry(_ context.Context, arg sqlc.MarkCharlieAlertDeliveryRetryParams) error {
	f.retry = arg
	return nil
}
func (f *charlieAlertDispatchFake) SuppressCharlieAlertDelivery(_ context.Context, arg sqlc.SuppressCharlieAlertDeliveryParams) error {
	f.suppressed = arg
	return nil
}

type charlieAlertRoundTripFunc func(*http.Request) (*http.Response, error)

func (f charlieAlertRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestCharlieAlertDispatchNeverReturnsProviderOrDatabaseErrorText(t *testing.T) {
	priorRuntime := runtimeDeps
	t.Cleanup(func() { runtimeDeps = priorRuntime })
	sentinel := "credential-provider-SENTINEL"
	ConfigureRuntime(RuntimeDependencies{HTTPClient: &http.Client{Transport: charlieAlertRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New(sentinel)
	})}})
	connectionID, findingID, channelID, deliveryID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	fake := &charlieAlertDispatchFake{
		delivery: sqlc.CharlieAlertDelivery{ID: deliveryID, ConnectionID: connectionID, FindingID: findingID, NotificationChannelID: pgtype.UUID{Bytes: channelID, Valid: true}, DeliveryKind: "initial", Severity: "high", AttemptCount: 1, MaximumAttempts: 8, Subject: "Charlie finding", Body: "bounded", CreatedAt: time.Now()},
		finding:  sqlc.CharlieFinding{ID: findingID, ConnectionID: connectionID, Status: "open"},
		channel:  sqlc.NotificationChannel{ID: channelID, Enabled: true, ChannelType: "webhook", Configuration: []byte(`{"url":"https://93.184.216.34/notify"}`)},
		allowed:  true,
	}
	configureCharlieAlertDispatchTest(t, fake, nil)
	task := asynq.NewTask(CharlieAlertDispatchTaskType, []byte(`{"delivery_id":"`+deliveryID.String()+`"}`))
	err := HandleCharlieAlertDispatch(context.Background(), task)
	if err == nil || strings.Contains(err.Error(), sentinel) || err.Error() != "Charlie alert error: delivery_failed" {
		t.Fatalf("unsafe provider error: %v", err)
	}
	if fake.retry.LastErrorCode != "delivery_failed" {
		t.Fatalf("stored error code=%q", fake.retry.LastErrorCode)
	}

	fake.claimErr = errors.New("database-password-SENTINEL")
	err = HandleCharlieAlertDispatch(context.Background(), task)
	if err == nil || strings.Contains(err.Error(), "database-password-SENTINEL") || err.Error() != "Charlie alert error: claim_unavailable" {
		t.Fatalf("unsafe database error: %v", err)
	}
}

func TestCharlieAlertDispatchNoopsClaimReplay(t *testing.T) {
	fake := &charlieAlertDispatchFake{claimErr: pgx.ErrNoRows, currentErr: pgx.ErrNoRows}
	configureCharlieAlertDispatchTest(t, fake, nil)
	err := HandleCharlieAlertDispatch(context.Background(), asynq.NewTask(CharlieAlertDispatchTaskType, []byte(`{"delivery_id":"`+uuid.NewString()+`"}`)))
	if err != nil {
		t.Fatal(err)
	}
}

func TestCharlieAlertDispatchSuppressesCurrentPolicyChangesBeforeHTTP(t *testing.T) {
	priorRuntime := runtimeDeps
	t.Cleanup(func() { runtimeDeps = priorRuntime })
	httpCalls := 0
	ConfigureRuntime(RuntimeDependencies{HTTPClient: &http.Client{Transport: charlieAlertRoundTripFunc(func(*http.Request) (*http.Response, error) {
		httpCalls++
		return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody}, nil
	})}})
	connectionID, findingID, channelID, deliveryID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, name := range []string{"route removed", "minimum severity raised", "connection disabled or emergency stopped"} {
		t.Run(name, func(t *testing.T) {
			fake := &charlieAlertDispatchFake{
				delivery: sqlc.CharlieAlertDelivery{ID: deliveryID, ConnectionID: connectionID, FindingID: findingID, NotificationChannelID: pgtype.UUID{Bytes: channelID, Valid: true}, DeliveryKind: "initial", Severity: "high", AttemptCount: 1, MaximumAttempts: 8, Subject: "Charlie finding", Body: "bounded", CreatedAt: time.Now()},
				finding:  sqlc.CharlieFinding{ID: findingID, ConnectionID: connectionID, Status: "open"},
				channel:  sqlc.NotificationChannel{ID: channelID, Enabled: true, ChannelType: "webhook", Configuration: []byte(`{"url":"https://93.184.216.34/notify"}`)},
				// The database query returns false both when the channel mapping
				// disappeared and when the current threshold now exceeds severity.
				allowed: false,
			}
			configureCharlieAlertDispatchTest(t, fake, nil)
			err := HandleCharlieAlertDispatch(context.Background(), asynq.NewTask(CharlieAlertDispatchTaskType, []byte(`{"delivery_id":"`+deliveryID.String()+`"}`)))
			if err != nil || httpCalls != 0 || fake.suppressed.LastErrorCode != "policy_changed" {
				t.Fatalf("policy recheck err=%v http_calls=%d suppressed=%q", err, httpCalls, fake.suppressed.LastErrorCode)
			}
		})
	}
}

func TestCharlieAlertDispatchRecoversExpiredLeaseWithoutConcurrentSend(t *testing.T) {
	priorRuntime := runtimeDeps
	t.Cleanup(func() { runtimeDeps = priorRuntime })
	httpCalls := 0
	ConfigureRuntime(RuntimeDependencies{HTTPClient: &http.Client{Transport: charlieAlertRoundTripFunc(func(*http.Request) (*http.Response, error) {
		httpCalls++
		return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody}, nil
	})}})
	connectionID, findingID, channelID, deliveryID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	delivery := sqlc.CharlieAlertDelivery{ID: deliveryID, ConnectionID: connectionID, FindingID: findingID, NotificationChannelID: pgtype.UUID{Bytes: channelID, Valid: true}, DeliveryKind: "initial", Severity: "high", AttemptCount: 2, MaximumAttempts: 8, Subject: "Charlie finding", Body: "bounded", CreatedAt: time.Now()}
	fake := &charlieAlertDispatchFake{
		delivery: delivery, claimErr: pgx.ErrNoRows, currentDelivery: sqlc.CharlieAlertDelivery{ID: deliveryID, Status: "delivering"},
		finding: sqlc.CharlieFinding{ID: findingID, ConnectionID: connectionID, Status: "open"},
		channel: sqlc.NotificationChannel{ID: channelID, Enabled: true, ChannelType: "webhook", Configuration: []byte(`{"url":"https://93.184.216.34/notify"}`)},
		allowed: true,
	}
	configureCharlieAlertDispatchTest(t, fake, nil)
	task := asynq.NewTask(CharlieAlertDispatchTaskType, []byte(`{"delivery_id":"`+deliveryID.String()+`"}`))
	if err := HandleCharlieAlertDispatch(context.Background(), task); err == nil || err.Error() != "Charlie alert error: delivery_leased" || httpCalls != 0 {
		t.Fatalf("active lease err=%v http_calls=%d", err, httpCalls)
	}

	// A later Asynq retry can claim the row once its database lease expires.
	fake.claimErr = nil
	if err := HandleCharlieAlertDispatch(context.Background(), task); err != nil || httpCalls != 1 || !fake.delivered {
		t.Fatalf("expired lease recovery err=%v http_calls=%d delivered=%t", err, httpCalls, fake.delivered)
	}
	// A concurrent/replayed task observes the terminal row and cannot send again.
	fake.claimErr = pgx.ErrNoRows
	fake.currentDelivery.Status = "delivered"
	if err := HandleCharlieAlertDispatch(context.Background(), task); err != nil || httpCalls != 1 {
		t.Fatalf("terminal replay err=%v http_calls=%d", err, httpCalls)
	}
}

func TestCharlieAlertDispatchDisableDrainsProviderAndRejectsQueuedDelivery(t *testing.T) {
	priorRuntime := runtimeDeps
	t.Cleanup(func() { runtimeDeps = priorRuntime })
	providerEntered := make(chan struct{})
	releaseProvider := make(chan struct{})
	httpCalls := 0
	ConfigureRuntime(RuntimeDependencies{HTTPClient: &http.Client{Transport: charlieAlertRoundTripFunc(func(*http.Request) (*http.Response, error) {
		httpCalls++
		close(providerEntered)
		<-releaseProvider
		return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody}, nil
	})}})
	connectionID, findingID, channelID, deliveryID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	fake := &charlieAlertDispatchFake{
		delivery: sqlc.CharlieAlertDelivery{ID: deliveryID, ConnectionID: connectionID, FindingID: findingID, NotificationChannelID: pgtype.UUID{Bytes: channelID, Valid: true}, DeliveryKind: "initial", Severity: "high", AttemptCount: 1, MaximumAttempts: 8, Subject: "Charlie finding", Body: "bounded", CreatedAt: time.Now()},
		finding:  sqlc.CharlieFinding{ID: findingID, ConnectionID: connectionID, Status: "open"},
		channel:  sqlc.NotificationChannel{ID: channelID, Enabled: true, ChannelType: "webhook", Configuration: []byte(`{"url":"https://93.184.216.34/notify"}`)},
		allowed:  true,
	}
	fence := charlie.NewWriteFence()
	configureCharlieAlertDispatchTest(t, fake, fence)
	task := asynq.NewTask(CharlieAlertDispatchTaskType, []byte(`{"delivery_id":"`+deliveryID.String()+`"}`))
	dispatchDone := make(chan error, 1)
	go func() { dispatchDone <- HandleCharlieAlertDispatch(context.Background(), task) }()

	select {
	case <-providerEntered:
	case <-time.After(time.Second):
		t.Fatal("provider call did not start")
	}
	drainCtx, cancelDrain := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelDrain()
	drainDone := make(chan error, 1)
	go func() {
		_, err := fence.CloseAndWait(drainCtx)
		drainDone <- err
	}()
	select {
	case err := <-drainDone:
		t.Fatalf("disable completed while provider call was in flight: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(releaseProvider)
	if err := <-dispatchDone; err != nil && err.Error() != "Charlie alert error: delivery_failed" {
		t.Fatalf("uncoded dispatch result after cancellation: %v", err)
	}
	if err := <-drainDone; err != nil {
		t.Fatalf("disable did not finish after provider returned: %v", err)
	}
	claimsBefore := fake.claimCalls
	if err := HandleCharlieAlertDispatch(context.Background(), task); err == nil || err.Error() != "Charlie alert error: write_admission_closed" {
		t.Fatalf("queued delivery after closure was not rejected with a code: %v", err)
	}
	if httpCalls != 1 || fake.claimCalls != claimsBefore {
		t.Fatalf("closed admission reached product state/provider: http_calls=%d claim_calls=%d before=%d", httpCalls, fake.claimCalls, claimsBefore)
	}
}

// Two distinct pools model the API and standalone-worker processes. The
// release-gate environment is optional because this test only needs a real
// PostgreSQL advisory-lock implementation, not Astronomer schema objects.
func TestCharlieAlertDispatchDistributedFenceBlocksDisableAcrossProcesses(t *testing.T) {
	dsn := os.Getenv("CHARLIE_ALERT_POLICY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("CHARLIE_ALERT_POLICY_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	apiPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer apiPool.Close()
	workerPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer workerPool.Close()
	if err := apiPool.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	if err := workerPool.Ping(ctx); err != nil {
		t.Fatal(err)
	}

	apiFence := charlie.NewDistributedWriteFence(apiPool)
	workerFence := charlie.NewDistributedWriteFence(workerPool)
	_, releaseWorker, err := workerFence.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	drainDone := make(chan error, 1)
	go func() {
		_, drainErr := apiFence.CloseAndWait(ctx)
		drainDone <- drainErr
	}()
	select {
	case err := <-drainDone:
		releaseWorker()
		t.Fatalf("cross-process disable bypassed a shared delivery lock: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	releaseWorker()
	select {
	case err := <-drainDone:
		if err != nil {
			t.Fatalf("cross-process disable failed after delivery release: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("cross-process disable did not acquire the exclusive lock")
	}
}

func TestDistributedFenceHoldBlocksQueuedCrossReplicaAdmissionUntilTransitionRelease(t *testing.T) {
	dsn := os.Getenv("CHARLIE_ALERT_POLICY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("CHARLIE_ALERT_POLICY_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	transitionPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer transitionPool.Close()
	secondReplicaPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer secondReplicaPool.Close()
	transitionFence := charlie.NewDistributedWriteFence(transitionPool)
	secondReplicaFence := charlie.NewDistributedWriteFence(secondReplicaPool)
	_, releaseTransition, err := transitionFence.CloseAndHold(ctx)
	if err != nil {
		t.Fatal(err)
	}
	admitted := make(chan error, 1)
	var releaseWrite func()
	go func() {
		_, release, beginErr := secondReplicaFence.Begin(ctx)
		releaseWrite = release
		admitted <- beginErr
	}()
	select {
	case err := <-admitted:
		releaseTransition()
		t.Fatalf("queued second-replica shared lock bypassed held transition: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	releaseTransition()
	select {
	case err := <-admitted:
		if err != nil {
			t.Fatalf("queued second-replica admission failed after transition release: %v", err)
		}
		if releaseWrite != nil {
			releaseWrite()
		}
	case <-ctx.Done():
		t.Fatal("queued second-replica admission did not resume after transition release")
	}
}
