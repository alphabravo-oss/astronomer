package tasks

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/email"
)

// fakeEmailQuerier captures the dispatcher's writes for assertion.
type fakeEmailQuerier struct {
	queued        []sqlc.EmailMessage
	sentIDs       []uuid.UUID
	failedRows    []sqlc.MarkEmailFailedParams
	skippedIDs    []uuid.UUID
	deletedEmails int64
	deletedTokens int64
}

func (f *fakeEmailQuerier) ListQueuedEmails(_ context.Context, _ int32) ([]sqlc.EmailMessage, error) {
	return f.queued, nil
}
func (f *fakeEmailQuerier) MarkEmailSent(_ context.Context, arg sqlc.MarkEmailSentParams) error {
	f.sentIDs = append(f.sentIDs, arg.ID)
	return nil
}
func (f *fakeEmailQuerier) MarkEmailFailed(_ context.Context, arg sqlc.MarkEmailFailedParams) error {
	f.failedRows = append(f.failedRows, arg)
	return nil
}
func (f *fakeEmailQuerier) MarkEmailSkipped(_ context.Context, arg sqlc.MarkEmailSkippedParams) error {
	f.skippedIDs = append(f.skippedIDs, arg.ID)
	return nil
}
func (f *fakeEmailQuerier) DeleteEmailsOlderThan(_ context.Context, _ time.Time) (int64, error) {
	return f.deletedEmails, nil
}
func (f *fakeEmailQuerier) DeleteExpiredPasswordResetTokens(_ context.Context, _ time.Time) (int64, error) {
	return f.deletedTokens, nil
}

type fakeSender struct {
	calls atomic.Int32
	err   error
}

func (f *fakeSender) Send(_ context.Context, _ email.Message) error {
	f.calls.Add(1)
	return f.err
}

type fakeProvider struct{ cfg email.Settings }

func (f fakeProvider) Provide(_ context.Context) (email.Settings, error) { return f.cfg, nil }

// preRenderedFakeSender implements both Send and SendPreRendered so the
// dispatcher exercises the pre-rendered path and we can assert the stored body
// is shipped verbatim (not re-rendered against an empty Data bag).
type preRenderedFakeSender struct {
	preRenderedCalls int
	legacyCalls      int
	lastBodyText     string
	lastBodyHTML     string
	lastSubject      string
	lastTo           string
}

func (f *preRenderedFakeSender) Send(_ context.Context, _ email.Message) error {
	f.legacyCalls++
	return nil
}

func (f *preRenderedFakeSender) SendPreRendered(_ context.Context, to, _, subject, bodyText, bodyHTML string) error {
	f.preRenderedCalls++
	f.lastTo = to
	f.lastSubject = subject
	f.lastBodyText = bodyText
	f.lastBodyHTML = bodyHTML
	return nil
}

// The dispatcher must ship the enqueue-time rendered body (which carries the
// working reset link / alert details), NOT re-render the template against an
// empty data bag. When the Sender supports SendPreRendered, the stored
// body_text/body_html are passed through unchanged.
func TestEmailDispatcher_UsesPreRenderedBody(t *testing.T) {
	body := "Reset your password: https://astronomer.example/reset?token=abc123"
	q := &fakeEmailQuerier{
		queued: []sqlc.EmailMessage{{
			ID:        uuid.New(),
			Status:    "queued",
			ToAddress: "user@example.com",
			Template:  "password_reset",
			Subject:   "Reset your password",
			BodyText:  body,
			BodyHtml:  "<p>" + body + "</p>",
			CreatedAt: time.Now(),
		}},
	}
	sender := &preRenderedFakeSender{}
	prev := emailDeps
	defer func() { emailDeps = prev }()
	ConfigureEmail(EmailDeps{
		Queries:  q,
		Sender:   sender,
		Provider: fakeProvider{cfg: email.Settings{Enabled: true}},
	})

	if err := HandleEmailDispatch(context.Background(), &asynq.Task{}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if sender.preRenderedCalls != 1 {
		t.Fatalf("expected 1 pre-rendered send, got %d", sender.preRenderedCalls)
	}
	if sender.legacyCalls != 0 {
		t.Fatalf("legacy re-render path must not be used when SendPreRendered is available, got %d calls", sender.legacyCalls)
	}
	if sender.lastBodyText != body {
		t.Errorf("body_text not passed through verbatim:\n got: %q\nwant: %q", sender.lastBodyText, body)
	}
	if sender.lastBodyHTML != "<p>"+body+"</p>" {
		t.Errorf("body_html not passed through verbatim: %q", sender.lastBodyHTML)
	}
	if len(q.sentIDs) != 1 {
		t.Errorf("expected row marked sent, got %d", len(q.sentIDs))
	}
}

func TestEmailDispatcher_BatchProcesses(t *testing.T) {
	q := &fakeEmailQuerier{
		queued: []sqlc.EmailMessage{
			{ID: uuid.New(), Status: "queued", ToAddress: "a@b.com", Template: "account_locked", CreatedAt: time.Now()},
			{ID: uuid.New(), Status: "queued", ToAddress: "c@d.com", Template: "account_locked", CreatedAt: time.Now()},
		},
	}
	sender := &fakeSender{}
	prev := emailDeps
	defer func() { emailDeps = prev }()
	ConfigureEmail(EmailDeps{
		Queries:  q,
		Sender:   sender,
		Provider: fakeProvider{cfg: email.Settings{Enabled: true}},
	})

	if err := HandleEmailDispatch(context.Background(), &asynq.Task{}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if sender.calls.Load() != 2 {
		t.Errorf("expected 2 sends, got %d", sender.calls.Load())
	}
	if len(q.sentIDs) != 2 {
		t.Errorf("expected 2 MarkEmailSent calls, got %d", len(q.sentIDs))
	}
	if len(q.failedRows) != 0 {
		t.Errorf("expected 0 failures, got %d", len(q.failedRows))
	}
}

func TestEmailDispatcher_SMTPDisabled_AgesQueuedRows(t *testing.T) {
	// One fresh row (under the 1-hour grace) and one stale row
	// (created 2 hours ago) — only the stale one should be aged.
	freshID := uuid.New()
	staleID := uuid.New()
	q := &fakeEmailQuerier{
		queued: []sqlc.EmailMessage{
			{ID: freshID, Status: "queued", CreatedAt: time.Now()},
			{ID: staleID, Status: "queued", CreatedAt: time.Now().Add(-2 * time.Hour)},
		},
	}
	sender := &fakeSender{}
	prev := emailDeps
	defer func() { emailDeps = prev }()
	ConfigureEmail(EmailDeps{
		Queries:  q,
		Sender:   sender,
		Provider: fakeProvider{cfg: email.Settings{Enabled: false}},
	})

	if err := HandleEmailDispatch(context.Background(), &asynq.Task{}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if sender.calls.Load() != 0 {
		t.Errorf("sender must not be called when SMTP disabled, got %d", sender.calls.Load())
	}
	if len(q.skippedIDs) != 1 || q.skippedIDs[0] != staleID {
		t.Errorf("expected staleID to be skipped, got %v", q.skippedIDs)
	}
}

func TestEmailDispatcher_FailureRetries(t *testing.T) {
	id := uuid.New()
	q := &fakeEmailQuerier{
		queued: []sqlc.EmailMessage{
			{ID: id, Status: "queued", Attempts: 0, ToAddress: "x@y.com", CreatedAt: time.Now()},
		},
	}
	prev := emailDeps
	defer func() { emailDeps = prev }()
	ConfigureEmail(EmailDeps{
		Queries:  q,
		Sender:   &fakeSender{err: errors.New("connection refused")},
		Provider: fakeProvider{cfg: email.Settings{Enabled: true}},
	})

	if err := HandleEmailDispatch(context.Background(), &asynq.Task{}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(q.failedRows) != 1 {
		t.Fatalf("expected 1 failure marked, got %d", len(q.failedRows))
	}
	if q.failedRows[0].Status != "queued" {
		t.Errorf("first failure should remain 'queued' (retryable), got %s", q.failedRows[0].Status)
	}
	if q.failedRows[0].Attempts != 1 {
		t.Errorf("attempts should be 1, got %d", q.failedRows[0].Attempts)
	}

	// Final attempt → status flips to "failed".
	q.queued = []sqlc.EmailMessage{
		{ID: id, Status: "failed", Attempts: emailMaxAttempts - 1, ToAddress: "x@y.com", CreatedAt: time.Now()},
	}
	q.failedRows = nil
	if err := HandleEmailDispatch(context.Background(), &asynq.Task{}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(q.failedRows) != 1 || q.failedRows[0].Status != "failed" {
		t.Errorf("expected final failure to flip to 'failed', got %+v", q.failedRows)
	}
}

func TestEmailCleanupOld_DeletesBoth(t *testing.T) {
	q := &fakeEmailQuerier{deletedEmails: 17, deletedTokens: 4}
	prev := emailDeps
	defer func() { emailDeps = prev }()
	ConfigureEmail(EmailDeps{Queries: q})
	if err := HandleEmailCleanupOld(context.Background(), &asynq.Task{}); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
}
