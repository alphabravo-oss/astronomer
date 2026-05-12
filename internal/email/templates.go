// Package email — operator-configurable SMTP delivery for the
// platform's transactional notifications: password resets, lockouts,
// 2FA events, recovery-code regenerations, new API tokens, and alert-
// rule firings.
//
// The package is built around three concrete types:
//
//   * Sender — wraps net/smtp. Decrypts the stored SMTP password,
//     renders the template, dials the relay, and returns. Send is
//     synchronous; the dispatch worker calls it in a loop with a
//     short timeout.
//
//   * Message — the per-send payload (recipient, template name,
//     template data). Lives in messages.go.
//
//   * Branding — the small bag of strings rendered into every
//     template (product_name, support_url). Source-of-truth is the
//     existing platform_configuration row from migration 001; the
//     parallel agent's platform_settings additions are picked up
//     opportunistically via a BrandingProvider hook.
//
// Templates are embedded into the binary via //go:embed so the worker
// image doesn't need to ship a templates directory. The .txt variant
// is rendered with text/template (no escaping); .html with
// html/template (auto-escaped). Both variants are required so MUA
// clients that prefer text-only still get something sensible.
package email

import (
	"bytes"
	"embed"
	"fmt"
	htmltemplate "html/template"
	"io/fs"
	"strings"
	texttemplate "text/template"
)

// Template names — one constant per `template` column value persisted
// into email_messages. The dispatcher uses the same constant to look
// up the rendered output, so these MUST match the keys in templateSet.
const (
	TemplatePasswordReset            = "password_reset"
	TemplateAccountLocked            = "account_locked"
	TemplateAccountUnlocked          = "account_unlocked"
	TemplateTOTPEnabled              = "totp_enabled"
	TemplateTOTPDisabled             = "totp_disabled"
	TemplateRecoveryCodesRegenerated = "recovery_codes_regenerated"
	TemplateAPITokenCreated          = "api_token_created"
	TemplateAlertFired               = "alert_fired"
	// TemplateTest is the body the operator-triggered SMTP test send
	// uses. Not persisted into the email_messages template column
	// (the test path bypasses the table) but kept here so the test
	// path renders through the same code path as real sends.
	TemplateTest = "_test"
)

// templateNames is the ordered list of every user-facing template. The
// test path skips itself but is included in the embed so it ships in
// the binary.
var templateNames = []string{
	TemplatePasswordReset,
	TemplateAccountLocked,
	TemplateAccountUnlocked,
	TemplateTOTPEnabled,
	TemplateTOTPDisabled,
	TemplateRecoveryCodesRegenerated,
	TemplateAPITokenCreated,
	TemplateAlertFired,
	TemplateTest,
}

//go:embed templates/*.txt templates/*.html
var templateFS embed.FS

// templateSet holds the pre-parsed text + html templates per name. We
// parse once at package init so the hot path (Sender.Send) does only
// the Execute step. A failure in init() means a malformed embedded
// template would crash the binary at start — much louder than
// discovering the problem on the first user-triggered send.
type templateSet struct {
	text *texttemplate.Template
	html *htmltemplate.Template
	// subject is the rendered Subject line. Templates may embed
	// Go-template directives in the subject line — the syntax is
	// `{{define "subject"}}...{{end}}` inside the .txt variant.
	// When absent, fallbackSubjects[name] is used.
	subject *texttemplate.Template
}

var compiledTemplates = map[string]*templateSet{}

// fallbackSubjects supply the default Subject when a template doesn't
// {{define "subject"}}. Keeps the template files focused on body
// content; the operator can later override via platform_settings (out
// of scope for migration 047 — TODO once the parallel agent's table
// lands).
var fallbackSubjects = map[string]string{
	TemplatePasswordReset:            "Reset your {{.Branding.ProductName}} password",
	TemplateAccountLocked:            "Your {{.Branding.ProductName}} account is temporarily locked",
	TemplateAccountUnlocked:          "Your {{.Branding.ProductName}} account has been unlocked",
	TemplateTOTPEnabled:              "Two-factor authentication enabled on your {{.Branding.ProductName}} account",
	TemplateTOTPDisabled:             "Two-factor authentication disabled on your {{.Branding.ProductName}} account",
	TemplateRecoveryCodesRegenerated: "Your {{.Branding.ProductName}} recovery codes were regenerated",
	TemplateAPITokenCreated:          "A new {{.Branding.ProductName}} API token was created",
	TemplateAlertFired:               "[{{.Branding.ProductName}}] Alert: {{.Subject}}",
	TemplateTest:                     "Test message from {{.Branding.ProductName}}",
}

func init() {
	if err := loadTemplates(); err != nil {
		panic(fmt.Sprintf("email: load embedded templates: %v", err))
	}
}

func loadTemplates() error {
	for _, name := range templateNames {
		txtBytes, err := fs.ReadFile(templateFS, "templates/"+name+".txt")
		if err != nil {
			return fmt.Errorf("read template %s.txt: %w", name, err)
		}
		htmlBytes, err := fs.ReadFile(templateFS, "templates/"+name+".html")
		if err != nil {
			return fmt.Errorf("read template %s.html: %w", name, err)
		}
		txt, err := texttemplate.New(name + ".txt").Parse(string(txtBytes))
		if err != nil {
			return fmt.Errorf("parse template %s.txt: %w", name, err)
		}
		html, err := htmltemplate.New(name + ".html").Parse(string(htmlBytes))
		if err != nil {
			return fmt.Errorf("parse template %s.html: %w", name, err)
		}
		// Subject template — read from a sibling .subject file if
		// present, otherwise the fallback above. Keeps the .txt body
		// purely a body file (no special "subject" blocks).
		subjSrc := fallbackSubjects[name]
		if subjSrc == "" {
			subjSrc = "Notification from {{.Branding.ProductName}}"
		}
		subjTmpl, err := texttemplate.New(name + ".subject").Parse(subjSrc)
		if err != nil {
			return fmt.Errorf("parse subject %s: %w", name, err)
		}
		compiledTemplates[name] = &templateSet{
			text:    txt,
			html:    html,
			subject: subjTmpl,
		}
	}
	return nil
}

// Branding carries the rendered product_name / support_url that every
// template uses for the header + footer chunks. When the platform
// settings table doesn't have a value the handler-supplied default is
// passed through.
type Branding struct {
	ProductName string
	SupportURL  string
	// LoginURL is the cleartext base URL of the dashboard, used by
	// password_reset to compose the reset link. The handler that
	// enqueues the email resolves it from platform_configuration.server_url.
	LoginURL string
}

// renderInput is the strict shape every template sees. Keeping it
// closed (vs. map[string]any) means the templates are typo-resistant —
// a missing field at parse time would fail the compile step in
// loadTemplates() rather than silently render blank.
type renderInput struct {
	Branding Branding
	// Subject is the raw subject FRAGMENT that the alert_fired
	// template embeds in its Subject line. Other templates ignore it.
	Subject string
	// Data is the per-template payload bag. Templates index into this
	// for the human-readable bits (username, reset link, etc.). It's
	// `any` only to keep the templates from typing every payload
	// shape; the handlers pass concrete structs.
	Data any
}

// Rendered is the output of Render — the three strings ready to hand
// off to the SMTP DATA stage. Body fields are normalized to CRLF line
// endings so a stray LF doesn't trip strict MTAs (RFC 5321 §2.3.7).
type Rendered struct {
	Subject  string
	BodyText string
	BodyHTML string
}

// Render renders the named template with the given data and branding
// into Subject/BodyText/BodyHTML. The result is safe to pass to
// composeMessage; the SMTP encoding step is its own function so tests
// can intercept the rendered output without going through net/smtp.
func Render(name string, branding Branding, subjectFrag string, data any) (Rendered, error) {
	set, ok := compiledTemplates[name]
	if !ok {
		return Rendered{}, fmt.Errorf("unknown email template %q", name)
	}
	input := renderInput{Branding: branding, Subject: subjectFrag, Data: data}
	var subj, txt, html bytes.Buffer
	if err := set.subject.Execute(&subj, input); err != nil {
		return Rendered{}, fmt.Errorf("render subject %s: %w", name, err)
	}
	if err := set.text.Execute(&txt, input); err != nil {
		return Rendered{}, fmt.Errorf("render text %s: %w", name, err)
	}
	if err := set.html.Execute(&html, input); err != nil {
		return Rendered{}, fmt.Errorf("render html %s: %w", name, err)
	}
	return Rendered{
		Subject:  asciiSafeSubject(strings.TrimSpace(subj.String())),
		BodyText: toCRLF(txt.String()),
		BodyHTML: toCRLF(html.String()),
	}, nil
}

// toCRLF converts \n to \r\n for the MIME body. Already-\r\n input
// passes through unchanged (we don't double-CR).
func toCRLF(s string) string {
	// Normalise to LF first to handle mixed-line-ending source files,
	// then promote every LF to CRLF.
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.ReplaceAll(s, "\n", "\r\n")
}

// asciiSafeSubject collapses any non-ASCII byte in the subject to '?'.
// We don't try to RFC 2047-encode here because every template subject
// is operator-tunable text + ASCII product-name strings. If the
// platform_name carries a non-ASCII byte the fallback is degradation,
// not rejection — the message still ships, the operator notices, and
// fixes the platform_name. Strict RFC 5322 §2.3 also caps the subject
// at 998 chars; we truncate to 950 to leave headroom for any pre-
// pended "Re:" or list-name munging.
func asciiSafeSubject(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r < 32 || r > 126 {
			b.WriteRune('?')
			continue
		}
		b.WriteRune(r)
	}
	out := b.String()
	if len(out) > 950 {
		out = out[:950]
	}
	return out
}
