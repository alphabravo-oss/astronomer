package email

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// EnqueueQuerier is the surface an Enqueuer needs. *sqlc.Queries
// satisfies it directly.
type EnqueueQuerier interface {
	InsertEmailMessage(ctx context.Context, arg sqlc.InsertEmailMessageParams) (sqlc.EmailMessage, error)
	GetSMTPSettings(ctx context.Context, id uuid.UUID) (sqlc.SmtpSettings, error)
}

// Enqueuer is the user-facing API every hook site (auth lockout, totp
// enroll, alert fire, ...) writes to. It:
//
//   1. Renders the template (so the body is captured at the time the
//      event happened, not when the dispatcher gets around to it).
//   2. Inserts an email_messages row with status='queued' when SMTP
//      is enabled OR status='skipped' when it isn't.
//   3. Returns the row id so the caller can correlate in audit logs.
//
// The actual SMTP send happens later, in the email:dispatch worker
// task. Decoupling enqueue from send is the constraint above ("DON'T
// fail user-facing actions when an email send fails").
type Enqueuer struct {
	q        EnqueueQuerier
	branding BrandingProvider
	log      *slog.Logger
	now      func() time.Time
}

func NewEnqueuer(q EnqueueQuerier, branding BrandingProvider, log *slog.Logger) *Enqueuer {
	if log == nil {
		log = slog.Default()
	}
	return &Enqueuer{q: q, branding: branding, log: log, now: time.Now}
}

// SetNow lets tests pin the clock used for any auto-generated bodies
// (e.g. lockout email's "UnlockAt").
func (e *Enqueuer) SetNow(now func() time.Time) {
	if now != nil {
		e.now = now
	}
}

// Request is the user-supplied portion of the enqueue. Callers fill
// To + Template + Data; the Enqueuer fills in everything else
// (subject from template, status from smtp.enabled, etc.).
type Request struct {
	To       string
	CC       string
	Template string
	Subject  string // optional override
	Data     any
	// UserID is the optional user_id link recorded on the row.
	UserID uuid.UUID
}

// Enqueue persists a queued email_messages row (or 'skipped' when
// SMTP is disabled). Returns the row id so the caller can include it
// in the audit row tied to the user-facing event.
//
// This is a best-effort path: any non-nil error here is logged but
// the caller MUST NOT propagate it to the user. The constraint above
// is explicit — a missing SMTP relay must not fail logins.
func (e *Enqueuer) Enqueue(ctx context.Context, req Request) (uuid.UUID, error) {
	if e == nil || e.q == nil {
		return uuid.Nil, fmt.Errorf("email enqueuer not configured")
	}
	to := strings.TrimSpace(req.To)
	if to == "" {
		return uuid.Nil, fmt.Errorf("email recipient is required")
	}

	// Render up-front so a malformed template fails at enqueue time,
	// not 30s later in the dispatcher.
	branding := DefaultBranding
	if e.branding != nil {
		branding = e.branding.Branding(ctx)
		if branding.ProductName == "" {
			branding.ProductName = DefaultBranding.ProductName
		}
		if branding.SupportURL == "" {
			branding.SupportURL = DefaultBranding.SupportURL
		}
	}
	rendered, err := Render(req.Template, branding, req.Subject, req.Data)
	if err != nil {
		return uuid.Nil, fmt.Errorf("render %s: %w", req.Template, err)
	}

	// Decide initial status: skipped when SMTP is disabled or the
	// settings row is absent. Skipped rows are NOT retried by the
	// dispatcher; they exist so the admin view can show "we wanted
	// to send X but SMTP was off."
	status := "queued"
	lastError := ""
	row, err := e.q.GetSMTPSettings(ctx, SingletonSettingsID)
	switch {
	case err != nil:
		// pgx.ErrNoRows or any other read failure → treat as disabled.
		// We don't propagate the error; the user-facing action must
		// still succeed.
		status = "skipped"
		lastError = "smtp settings unavailable"
	case !row.Enabled:
		status = "skipped"
		lastError = "smtp delivery is disabled"
	}

	params := sqlc.InsertEmailMessageParams{
		ToAddress: to,
		CcAddress: req.CC,
		Subject:   rendered.Subject,
		Template:  req.Template,
		BodyText:  rendered.BodyText,
		BodyHtml:  rendered.BodyHTML,
		UserID:    pgtype.UUID{Bytes: req.UserID, Valid: req.UserID != uuid.Nil},
		Status:    status,
		LastError: lastError,
	}
	inserted, err := e.q.InsertEmailMessage(ctx, params)
	if err != nil {
		return uuid.Nil, fmt.Errorf("insert email_messages: %w", err)
	}
	if e.log != nil {
		e.log.InfoContext(ctx, "email enqueued",
			"template", req.Template,
			"status", status,
			"to", to,
			"id", inserted.ID.String(),
		)
	}
	return inserted.ID, nil
}

// EnqueueAndLog is a convenience for hook sites that just want
// best-effort enqueue + log without unwinding the error. Used at
// every call site so the wiring is a single line.
func (e *Enqueuer) EnqueueAndLog(ctx context.Context, req Request) {
	if e == nil {
		return
	}
	if _, err := e.Enqueue(ctx, req); err != nil && e.log != nil {
		e.log.WarnContext(ctx, "email enqueue failed",
			"template", req.Template,
			"to", req.To,
			"error", err,
		)
	}
}
