package webhook

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"text/template"
)

// ErrTemplateUnsafe is returned by ValidateTemplate when a candidate
// template contains a pattern that's almost certainly an attempt to
// reach outside Go's text/template sandbox (which is already pretty
// tight — no method invocation on disallowed receivers, no filesystem
// reads, no network) but that we want to keep further away from anyway.
//
// In particular we forbid:
//
//	{{ .Exec ... }}
//	{{ .Call ... }}
//	{{ call ... }}
//
// These cannot do harm with the data shape the webhook sender supplies
// (a plain map[string]any), but a future refactor that drops a richer
// receiver into the template data bag would be a foot-gun. Failing
// closed at config-write time is the durable defense.
var ErrTemplateUnsafe = errors.New("webhook template contains a disallowed pattern")

// unsafePatterns is the deny-list. Order doesn't matter; the check is
// case-insensitive on the whole template body.
var unsafePatterns = []string{
	".Exec",
	".Call",
	"call ",
	"call\t",
	// printf is a stdlib function the sandbox allows but a hostile
	// template could use it to write enormous output; we cap body
	// size in the sender so it's a soft concern, not a security one.
}

// ValidateTemplate returns nil when raw is either empty (ship the raw
// event JSON) or a parseable, safe text/template. Called by the handler
// before persisting a subscription so a bad template can't reach the
// dispatcher.
func ValidateTemplate(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	low := strings.ToLower(raw)
	for _, pat := range unsafePatterns {
		if strings.Contains(low, strings.ToLower(pat)) {
			return fmt.Errorf("%w: %q", ErrTemplateUnsafe, pat)
		}
	}
	if _, err := template.New("webhook").Option("missingkey=zero").Parse(raw); err != nil {
		return fmt.Errorf("parse template: %w", err)
	}
	return nil
}

// Render executes raw against data. An empty template returns nil so
// the caller can ship the raw event JSON. Errors here are surfaced to
// the dispatcher, which records them on the delivery row as last_error.
func Render(raw string, data map[string]any) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if err := ValidateTemplate(raw); err != nil {
		return nil, err
	}
	tpl, err := template.New("webhook").
		Option("missingkey=zero").
		Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse template: %w", err)
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("execute template: %w", err)
	}
	return buf.Bytes(), nil
}
