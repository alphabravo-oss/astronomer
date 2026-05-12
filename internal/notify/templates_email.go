// Email built-in template registrations.
//
// Each entry mirrors a constant defined in internal/email/templates.go
// (sprint 047). The Body field embeds the SAME .txt template bytes
// that the email Sender renders via //go:embed, so a defaults
// snapshot taken before this refactor is byte-identical to what the
// registry returns now.
//
// We DELIBERATELY do not embed the .html sibling — operator overrides
// supply a single body string (markdown source by convention) and the
// email transport renders markdown → HTML at delivery time. The
// default path keeps the existing .html embed in internal/email,
// which the dispatcher chooses when no override is in effect.

package notify

import (
	_ "embed"
)

// The Subject defaults below are lifted verbatim from
// internal/email/templates.go (fallbackSubjects). The defaults
// snapshot test rejects any drift.
const (
	// Email template keys. Stored on the notification_templates.template_key
	// column for overrides.
	KeyEmailPasswordReset            = "email.password_reset"
	KeyEmailAccountLocked            = "email.account_locked"
	KeyEmailAccountUnlocked          = "email.account_unlocked"
	KeyEmailTOTPEnabled              = "email.totp_enabled"
	KeyEmailTOTPDisabled             = "email.totp_disabled"
	KeyEmailRecoveryCodesRegenerated = "email.recovery_codes_regenerated"
	KeyEmailAPITokenCreated          = "email.api_token_created"
	KeyEmailAlertFired               = "email.alert_fired"
)

//go:embed bodies/email/password_reset.txt
var bodyEmailPasswordReset string

//go:embed bodies/email/account_locked.txt
var bodyEmailAccountLocked string

//go:embed bodies/email/account_unlocked.txt
var bodyEmailAccountUnlocked string

//go:embed bodies/email/totp_enabled.txt
var bodyEmailTOTPEnabled string

//go:embed bodies/email/totp_disabled.txt
var bodyEmailTOTPDisabled string

//go:embed bodies/email/recovery_codes_regenerated.txt
var bodyEmailRecoveryCodesRegenerated string

//go:embed bodies/email/api_token_created.txt
var bodyEmailAPITokenCreated string

//go:embed bodies/email/alert_fired.txt
var bodyEmailAlertFired string

// commonEmailVars are the {{.Branding.*}} fields every email
// template sees. The handler concatenates these into each
// TemplateDef.Variables list so the UI surfaces them.
var commonEmailVars = []VariableSpec{
	{Name: "Branding.ProductName", Description: "Display name of the platform (configured via /admin/settings/branding/)", Example: "Astronomer"},
	{Name: "Branding.SupportURL", Description: "Support / docs link rendered into the footer", Example: "https://example.com/support"},
	{Name: "Branding.LoginURL", Description: "Base URL of the dashboard, used to compose reset/verify links", Example: "https://dash.example.com"},
}

func emailVars(extra ...VariableSpec) []VariableSpec {
	out := make([]VariableSpec, 0, len(commonEmailVars)+len(extra))
	out = append(out, commonEmailVars...)
	out = append(out, extra...)
	return out
}

func init() {
	registerBuiltin(TemplateDef{
		Key:         KeyEmailPasswordReset,
		Channel:     ChannelEmail,
		Subject:     "Reset your {{.Branding.ProductName}} password",
		Body:        bodyEmailPasswordReset,
		BodyFormat:  BodyFormatMarkdown,
		Description: "Sent when a user requests a password reset via /forgot-password/.",
		Variables: emailVars(
			VariableSpec{Name: "Data.ResetURL", Description: "One-time password-reset link (valid 30 minutes)", Required: true, Example: "https://dash.example.com/reset?token=…"},
		),
	})

	registerBuiltin(TemplateDef{
		Key:         KeyEmailAccountLocked,
		Channel:     ChannelEmail,
		Subject:     "Your {{.Branding.ProductName}} account is temporarily locked",
		Body:        bodyEmailAccountLocked,
		BodyFormat:  BodyFormatMarkdown,
		Description: "Sent to the user when failed-login throttling triggers an automatic lockout.",
		Variables: emailVars(
			VariableSpec{Name: "Data.Username", Description: "Username of the locked account", Required: true, Example: "alice"},
			VariableSpec{Name: "Data.UnlockAt", Description: "Human-friendly timestamp the lock auto-clears", Required: true, Example: "2026-01-01 12:34 UTC"},
		),
	})

	registerBuiltin(TemplateDef{
		Key:         KeyEmailAccountUnlocked,
		Channel:     ChannelEmail,
		Subject:     "Your {{.Branding.ProductName}} account has been unlocked",
		Body:        bodyEmailAccountUnlocked,
		BodyFormat:  BodyFormatMarkdown,
		Description: "Sent when an administrator manually clears an active lockout.",
		Variables: emailVars(
			VariableSpec{Name: "Data.Username", Description: "Username of the unlocked account", Required: true, Example: "alice"},
		),
	})

	registerBuiltin(TemplateDef{
		Key:         KeyEmailTOTPEnabled,
		Channel:     ChannelEmail,
		Subject:     "Two-factor authentication enabled on your {{.Branding.ProductName}} account",
		Body:        bodyEmailTOTPEnabled,
		BodyFormat:  BodyFormatMarkdown,
		Description: "Sent to the user when they complete TOTP enrollment.",
		Variables: emailVars(
			VariableSpec{Name: "Data.Username", Description: "Username", Required: true, Example: "alice"},
			VariableSpec{Name: "Data.RecoveryCodeCount", Description: "How many recovery codes were issued at enrollment", Required: true, Example: "10"},
		),
	})

	registerBuiltin(TemplateDef{
		Key:         KeyEmailTOTPDisabled,
		Channel:     ChannelEmail,
		Subject:     "Two-factor authentication disabled on your {{.Branding.ProductName}} account",
		Body:        bodyEmailTOTPDisabled,
		BodyFormat:  BodyFormatMarkdown,
		Description: "Sent when the user (or an admin acting on their behalf) disables TOTP.",
		Variables: emailVars(
			VariableSpec{Name: "Data.Username", Description: "Username", Required: true, Example: "alice"},
		),
	})

	registerBuiltin(TemplateDef{
		Key:         KeyEmailRecoveryCodesRegenerated,
		Channel:     ChannelEmail,
		Subject:     "Your {{.Branding.ProductName}} recovery codes were regenerated",
		Body:        bodyEmailRecoveryCodesRegenerated,
		BodyFormat:  BodyFormatMarkdown,
		Description: "Sent when the user regenerates their 2FA recovery codes (previous set invalidated).",
		Variables: emailVars(
			VariableSpec{Name: "Data.Username", Description: "Username", Required: true, Example: "alice"},
		),
	})

	registerBuiltin(TemplateDef{
		Key:         KeyEmailAPITokenCreated,
		Channel:     ChannelEmail,
		Subject:     "A new {{.Branding.ProductName}} API token was created",
		Body:        bodyEmailAPITokenCreated,
		BodyFormat:  BodyFormatMarkdown,
		Description: "Sent to the user when a new API token is minted on their account.",
		Variables: emailVars(
			VariableSpec{Name: "Data.Username", Description: "Username", Required: true, Example: "alice"},
			VariableSpec{Name: "Data.TokenName", Description: "Operator-supplied label", Required: true, Example: "ci-deploy"},
			VariableSpec{Name: "Data.TokenPrefix", Description: "Public prefix of the new token (sans secret)", Required: true, Example: "atn_abc123"},
			VariableSpec{Name: "Data.CreatedAt", Description: "Creation timestamp", Required: true, Example: "2026-01-01 12:34 UTC"},
		),
	})

	registerBuiltin(TemplateDef{
		Key:         KeyEmailAlertFired,
		Channel:     ChannelEmail,
		Subject:     "[{{.Branding.ProductName}}] Alert: {{.Subject}}",
		Body:        bodyEmailAlertFired,
		BodyFormat:  BodyFormatMarkdown,
		Description: "Sent when an alert rule transitions into firing state (channel kind=email).",
		Variables: emailVars(
			VariableSpec{Name: "Subject", Description: "Alert name (embedded in the Subject line)", Required: true, Example: "etcd quorum loss"},
			VariableSpec{Name: "Data.AlertName", Description: "Alert rule name", Required: true, Example: "etcd-quorum-loss"},
			VariableSpec{Name: "Data.Severity", Description: "critical | warning | info", Required: true, Example: "critical"},
			VariableSpec{Name: "Data.FiredAt", Description: "When the rule first fired", Required: true, Example: "2026-01-01T12:34:00Z"},
			VariableSpec{Name: "Data.Resource", Description: "Resource the alert is scoped to (cluster/project/etc.)", Required: false, Example: "cluster/edge-1"},
			VariableSpec{Name: "Data.Message", Description: "Free-form alert body", Required: true, Example: "etcd cluster has lost quorum"},
			VariableSpec{Name: "Data.DashboardURL", Description: "Deep link into the alert detail view", Required: false, Example: "https://dash.example.com/alerts/…"},
		),
	})
}
