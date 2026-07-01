package tasks

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/email"
)

// Migration 047 — email dispatch + retention task types.
const (
	// EmailDispatchType is the periodic task that drains queued /
	// failed rows from email_messages into real SMTP sends. Runs
	// every 30s; cooperative DB lease (via runPeriodicTaskWithLeader)
	// keeps multiple worker pods from racing on the same row.
	EmailDispatchType = "email:dispatch"

	// EmailCleanupOldType deletes rows older than the retention window
	// (90 days). Daily cadence.
	EmailCleanupOldType = "email:cleanup_old"
)

// emailDispatchBatchSize caps how many rows the dispatcher pulls per
// tick. 50 is comfortably more than the platform sends in 30s under
// any realistic load (lockouts + alert fires + token creates) and is
// small enough that a slow relay doesn't starve the next tick.
const emailDispatchBatchSize = 50

// emailMaxAttempts is the retry budget. After 3 failed attempts the
// row is marked 'failed' permanently and the dispatcher leaves it
// alone; the admin audit view + observability surface the stuck rows.
const emailMaxAttempts = 3

// emailSkippedAge is how long a queued row can sit with SMTP disabled
// before the dispatcher ages it to 'skipped'. Without this, a disabled
// stack accumulates queued rows forever.
const emailSkippedAge = time.Hour

// emailRetention is the deletion window for email_messages rows. 90
// days matches the audit_log retention default.
const emailRetention = 90 * 24 * time.Hour

// EmailSender is the surface the dispatcher needs from the email
// package. *email.Sender satisfies it directly.
type EmailSender interface {
	Send(ctx context.Context, msg email.Message) error
}

// preRenderedEmailSender is the alternate send path the sendOne comment already
// promised: it ships the enqueue-time rendered body straight through instead of
// re-rendering the template against an EMPTY data bag. The enqueuer renders the
// template with the real Data (reset links, alert details, usernames) and stores
// the result in email_messages.body_text/body_html; re-rendering here against
// map[string]any{} turned every `{{.Data.X}}` into `<no value>`, so recipients
// got password-reset emails with no link, "Hello ," account-locked notices, and
// blank alert bodies — all still marked 'sent'. Primitive params (no shared
// struct) so *email.Sender can satisfy this without importing this package.
//
// WIRING: *email.Sender must grow
//
//	SendPreRendered(ctx, to, cc, subject, bodyText, bodyHTML string) error
//
// composing the MIME directly from the pre-rendered parts (reusing composeMessage
// + the live SMTP dial/branding-FROM path). Until then sendOne falls back to the
// legacy Send so nothing regresses at compile time.
type preRenderedEmailSender interface {
	SendPreRendered(ctx context.Context, to, cc, subject, bodyText, bodyHTML string) error
}

// EmailQuerier is the database surface the dispatch + cleanup tasks
// need. *sqlc.Queries satisfies it directly.
type EmailQuerier interface {
	ListQueuedEmails(ctx context.Context, limit int32) ([]sqlc.EmailMessage, error)
	MarkEmailSent(ctx context.Context, arg sqlc.MarkEmailSentParams) error
	MarkEmailFailed(ctx context.Context, arg sqlc.MarkEmailFailedParams) error
	MarkEmailSkipped(ctx context.Context, arg sqlc.MarkEmailSkippedParams) error
	DeleteEmailsOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
	DeleteExpiredPasswordResetTokens(ctx context.Context, cutoff time.Time) (int64, error)
}

// EmailSettingsProvider is the read-only handle the dispatcher uses
// to decide between "send for real" and "mark skipped because SMTP
// is disabled". Implemented by *email.SQLSettingsProvider.
type EmailSettingsProvider interface {
	Provide(ctx context.Context) (email.Settings, error)
}

// EmailDeps is the bag of dependencies wired by NewApp before the
// dispatcher task can do anything useful. Stored in a package var so
// the asynq HandleFunc signature stays standard (it can't carry a
// closure-bound deps).
type EmailDeps struct {
	Queries  EmailQuerier
	Sender   EmailSender
	Provider EmailSettingsProvider
}

var emailDeps EmailDeps

// ConfigureEmail wires the email dispatcher's dependencies. Safe to
// call multiple times (last call wins) but every productionish path
// calls it once at startup.
func ConfigureEmail(deps EmailDeps) {
	emailDeps = deps
}

// HandleEmailDispatch is the periodic worker that drains queued rows.
// Pattern matches the other periodic tasks in this package:
//
//  1. Acquire the leader lease so only one worker pod runs the loop.
//  2. Pull a batch of queued rows.
//  3. Iterate: render is already baked into the row at enqueue time,
//     so the dispatcher only re-fetches branding for the live SMTP
//     Settings and ships the bytes.
//  4. Mark each row sent/failed/skipped.
//
// We DON'T re-render the body here — the row already contains the
// rendered text/html because the Enqueuer rendered them at
// enqueue-time. That preserves branding-at-the-time-of-event semantics
// and means a dispatcher restart can't crash the loop on a templating
// error.
func HandleEmailDispatch(ctx context.Context, _ *asynq.Task) error {
	return runPeriodicTaskWithLeader(ctx, EmailDispatchType, func() error {
		if emailDeps.Queries == nil || emailDeps.Sender == nil || emailDeps.Provider == nil {
			runtimeLogger().InfoContext(ctx, "email dispatcher not configured, skipping")
			return nil
		}
		cfg, err := emailDeps.Provider.Provide(ctx)
		if err != nil {
			runtimeLogger().WarnContext(ctx, "email dispatcher could not load smtp settings", "error", err)
			// Don't return an error — the asynq retry would just
			// re-run on the next tick. Logging is enough.
			return nil
		}

		rows, err := emailDeps.Queries.ListQueuedEmails(ctx, emailDispatchBatchSize)
		if err != nil {
			return fmt.Errorf("list queued emails: %w", err)
		}
		if len(rows) == 0 {
			return nil
		}

		// Disabled-SMTP path: age out anything that's been sitting in
		// the queue too long. We DON'T try to send these — that's
		// exactly the case the skipped status is for.
		if !cfg.Enabled {
			ageRowsToSkipped(ctx, rows)
			return nil
		}

		now := time.Now()
		for _, row := range rows {
			// Context cancellation between rows is non-fatal; the
			// next tick picks up where this one left off.
			if err := ctx.Err(); err != nil {
				return nil
			}
			sendOne(ctx, row, now)
		}
		return nil
	})
}

// ageRowsToSkipped marks rows that have sat queued past emailSkippedAge
// while SMTP is disabled. The cutoff means a freshly-queued row gets a
// grace window — operators sometimes flip SMTP on a minute after a
// lockout fires, and we don't want to drop those.
func ageRowsToSkipped(ctx context.Context, rows []sqlc.EmailMessage) {
	cutoff := time.Now().Add(-emailSkippedAge)
	for _, row := range rows {
		if row.CreatedAt.After(cutoff) {
			continue
		}
		if err := emailDeps.Queries.MarkEmailSkipped(ctx, sqlc.MarkEmailSkippedParams{
			ID:        row.ID,
			LastError: "smtp delivery disabled at dispatch time",
		}); err != nil {
			runtimeLogger().WarnContext(ctx, "email skip mark failed", "id", row.ID.String(), "error", err)
		}
	}
}

// deliverEmail ships one row. It prefers the pre-rendered path
// (SendPreRendered) which passes the enqueue-time rendered body_text/body_html
// straight through — that is the source of truth for what the recipient
// receives (reset links, alert details, usernames all substituted at enqueue).
// Only when the Sender does not implement that path do we fall back to the
// legacy template re-render; the legacy path is a KNOWN lossy path (it renders
// against an empty Data bag) kept solely so the dispatcher still compiles/sends
// before the Sender grows SendPreRendered.
func deliverEmail(ctx context.Context, row sqlc.EmailMessage) error {
	if pr, ok := emailDeps.Sender.(preRenderedEmailSender); ok {
		return pr.SendPreRendered(ctx, row.ToAddress, row.CcAddress, row.Subject, row.BodyText, row.BodyHtml)
	}
	return emailDeps.Sender.Send(ctx, email.Message{
		To:       row.ToAddress,
		CC:       row.CcAddress,
		Template: row.Template,
		Subject:  row.Subject,
		// Legacy fallback only: no original Data survives the enqueue, so
		// dynamic {{.Data.X}} fields render blank. Replaced by the
		// pre-rendered path above once the Sender implements it.
		Data: map[string]any{},
	})
}

// sendOne is the per-row send. It stamps the delivery outcome
// (sent/failed/skipped) onto the row after deliverEmail ships it.
func sendOne(ctx context.Context, row sqlc.EmailMessage, now time.Time) {
	if err := deliverEmail(ctx, row); err != nil {
		if errors.Is(err, email.ErrSMTPDisabled) {
			// Racey but plausible: settings flipped between
			// Provide() and Send(). Mark skipped, same as the
			// disabled-path branch.
			_ = emailDeps.Queries.MarkEmailSkipped(ctx, sqlc.MarkEmailSkippedParams{
				ID:        row.ID,
				LastError: "smtp delivery disabled during send",
			})
			return
		}
		newAttempts := row.Attempts + 1
		status := "queued"
		if newAttempts >= emailMaxAttempts {
			status = "failed"
		}
		errMsg := truncateDispatchLastError(err.Error(), 1024)
		_ = emailDeps.Queries.MarkEmailFailed(ctx, sqlc.MarkEmailFailedParams{
			ID:        row.ID,
			Status:    status,
			Attempts:  newAttempts,
			LastError: errMsg,
		})
		runtimeLogger().WarnContext(ctx, "email send failed",
			"id", row.ID.String(),
			"template", row.Template,
			"attempt", newAttempts,
			"error", err,
		)
		return
	}
	if err := emailDeps.Queries.MarkEmailSent(ctx, sqlc.MarkEmailSentParams{
		ID:     row.ID,
		SentAt: pgtype.Timestamptz{Time: now, Valid: true},
	}); err != nil {
		runtimeLogger().WarnContext(ctx, "email mark-sent failed", "id", row.ID.String(), "error", err)
	}
}

// HandleEmailCleanupOld deletes email_messages rows older than the
// retention window AND password_reset_tokens whose expiry has long
// passed. Daily cadence; cooperative DB lease.
func HandleEmailCleanupOld(ctx context.Context, _ *asynq.Task) error {
	return runPeriodicTaskWithLeader(ctx, EmailCleanupOldType, func() error {
		if emailDeps.Queries == nil {
			runtimeLogger().InfoContext(ctx, "email cleanup not configured, skipping")
			return nil
		}
		cutoff := time.Now().Add(-emailRetention)
		removed, err := emailDeps.Queries.DeleteEmailsOlderThan(ctx, cutoff)
		if err != nil {
			return fmt.Errorf("delete old emails: %w", err)
		}
		expired, err := emailDeps.Queries.DeleteExpiredPasswordResetTokens(ctx, time.Now())
		if err != nil {
			return fmt.Errorf("delete expired reset tokens: %w", err)
		}
		runtimeLogger().InfoContext(ctx, "email retention sweep",
			"emails_deleted", removed,
			"reset_tokens_deleted", expired,
			"cutoff", cutoff.Format(time.RFC3339),
		)
		return nil
	})
}
