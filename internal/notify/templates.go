// Package notify is the central registry + resolver for transactional
// notification templates (email + webhook).
//
// Background — sprint 047 baked email body strings into the binary as
// //go:embed templates and sprint 048 left the webhook body shape as a
// JSON serialization of the Event struct. Both are fine defaults; the
// operator complaint is that customising subject/body required a
// re-deploy.
//
// Migration 059 introduces a notification_templates table whose rows
// are OVERRIDES, one per template_key. The registry below owns the
// defaults; notify.Resolve(key) returns the override row when present
// and enabled, falling back to the registry default otherwise. A row
// for a key the registry doesn't know about is ignored (logged at
// debug; we don't crash the dispatcher).
//
// Invariant: when no override exists, the resolved (subject, body,
// format) tuple MUST be byte-identical to the constants/templates the
// dispatcher used pre-migration. The default-snapshot tests in
// templates_snapshot_test.go enforce this for every registered key.
package notify

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// Channel constants. These must match the CHECK constraint on the
// notification_templates.channel column.
const (
	ChannelEmail   = "email"
	ChannelWebhook = "webhook"
)

// BodyFormat constants. Must match the CHECK constraint on
// notification_templates.body_format.
const (
	BodyFormatText     = "text"
	BodyFormatMarkdown = "markdown"
	BodyFormatHTML     = "html"
	BodyFormatJSON     = "json"
)

// VariableSpec describes one template input variable. Used by the
// admin UI to render the "available variables" sidebar, and by the
// /preview/ endpoint to enforce required-set on operator-supplied
// sample input. The Resolve hot-path does NOT consult this; the
// engine doesn't enforce required at render time so a missing field
// renders as the Go template zero-value (empty string).
type VariableSpec struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
	Example     string `json:"example"`
}

// TemplateDef is one entry in the registry. Subject + Body are the
// built-in defaults; the dispatcher renders these (or the operator's
// override of them) into the final payload.
type TemplateDef struct {
	Key         string         `json:"key"`
	Channel     string         `json:"channel"`
	Subject     string         `json:"subject"`
	Body        string         `json:"body"`
	BodyFormat  string         `json:"body_format"`
	Description string         `json:"description"`
	Variables   []VariableSpec `json:"variables"`
}

// Resolved is the return shape of Resolve. HasOverride is true when
// the override row was found AND enabled — the dispatcher uses this to
// decide whether to render the override path or the bake-d-in path.
type Resolved struct {
	Key         string
	Channel     string
	Subject     string
	Body        string
	BodyFormat  string
	HasOverride bool
	Disabled    bool // true when an override row exists but enabled=false
}

// Querier is the narrow DB surface Resolve needs. *sqlc.Queries
// satisfies this; tests pass a fake.
type Querier interface {
	GetNotificationTemplate(ctx context.Context, key string) (sqlc.NotificationTemplate, error)
}

// ErrUnknownKey is returned by Resolve when the requested key is not
// in the built-in registry. Caller should treat it as a programmer
// error — overrides for unknown keys are ignored.
var ErrUnknownKey = errors.New("notify: unknown template key")

// registryBuiltins is populated by the per-channel init blocks in
// templates_email.go / templates_webhook.go. We keep the package-level
// var private and expose via Registry() so callers can't mutate it.
var registryBuiltins []TemplateDef

// registerBuiltin is called from the email/webhook init blocks. Late-
// duplicate registration of the same key is a build-time error —
// keeping the registry deterministic.
func registerBuiltin(def TemplateDef) {
	for _, existing := range registryBuiltins {
		if existing.Key == def.Key {
			panic(fmt.Sprintf("notify: duplicate template registration: %q", def.Key))
		}
	}
	registryBuiltins = append(registryBuiltins, def)
}

// Registry returns a copy of every built-in TemplateDef, sorted by
// key. Returning a copy keeps callers from accidentally mutating the
// package state — the admin handler streams this into JSON
// responses where a sort or append would otherwise race the
// dispatcher.
func Registry() []TemplateDef {
	out := make([]TemplateDef, len(registryBuiltins))
	copy(out, registryBuiltins)
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// Lookup returns the TemplateDef for a key, or false. Used by the
// preview handler (so it knows the declared Variables[] without
// hitting the DB).
func Lookup(key string) (TemplateDef, bool) {
	for _, d := range registryBuiltins {
		if d.Key == key {
			return d, true
		}
	}
	return TemplateDef{}, false
}

// Resolve returns the live (subject, body, format) tuple for a
// template key. The lookup order is:
//
//  1. Built-in registry — if the key is unknown, ErrUnknownKey.
//  2. notification_templates row — if missing (pgx.ErrNoRows), the
//     default from step 1 is returned with HasOverride=false.
//  3. Override row but enabled=false — defaults from step 1 are
//     returned with Disabled=true so callers can log the gap.
//
// Any non-ErrNoRows DB error is propagated up; the dispatcher logs +
// falls back to defaults at the call site, but we don't silently
// swallow read failures here.
func Resolve(ctx context.Context, q Querier, key string) (Resolved, error) {
	def, ok := Lookup(key)
	if !ok {
		return Resolved{}, fmt.Errorf("%w: %s", ErrUnknownKey, key)
	}
	base := Resolved{
		Key:        def.Key,
		Channel:    def.Channel,
		Subject:    def.Subject,
		Body:       def.Body,
		BodyFormat: def.BodyFormat,
	}
	if q == nil {
		return base, nil
	}
	row, err := q.GetNotificationTemplate(ctx, key)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return base, nil
		}
		return base, fmt.Errorf("notify: load override %s: %w", key, err)
	}
	if !row.Enabled {
		base.Disabled = true
		return base, nil
	}
	// An override exists and is enabled — replace the default fields.
	// Subject_tpl may legitimately be empty (e.g. webhook channel
	// doesn't carry one); in that case fall back to the default
	// subject so the dispatcher always has something to log.
	out := base
	if row.SubjectTpl != "" {
		out.Subject = row.SubjectTpl
	}
	out.Body = row.BodyTpl
	if row.BodyFormat != "" {
		out.BodyFormat = row.BodyFormat
	}
	out.HasOverride = true
	return out, nil
}
