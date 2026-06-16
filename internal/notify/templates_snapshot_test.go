package notify

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// expectedKeys is the canonical list of template keys this package
// MUST register. Adding a new key requires updating this list (the
// test below is the merge-blocking gate).
var expectedKeys = []string{
	KeyEmailPasswordReset,
	KeyEmailAccountLocked,
	KeyEmailAccountUnlocked,
	KeyEmailTOTPEnabled,
	KeyEmailTOTPDisabled,
	KeyEmailRecoveryCodesRegenerated,
	KeyEmailAPITokenCreated,
	KeyEmailAlertFired,
	KeyWebhookAuditEvent,
	KeyWebhookAlertFired,
	KeyWebhookAlertResolved,
	KeyWebhookClusterConnected,
	KeyWebhookClusterDisconnected,
	KeyWebhookClusterStatusChanged,
	KeyWebhookClusterCreated,
	KeyWebhookClusterUpdated,
	KeyWebhookClusterDeleted,
	KeyWebhookClusterDecommissioned,
}

func TestRegistry_HasExpectedKeys(t *testing.T) {
	reg := Registry()
	got := map[string]bool{}
	for _, d := range reg {
		got[d.Key] = true
	}
	for _, k := range expectedKeys {
		if !got[k] {
			t.Errorf("expected key %q missing from registry", k)
		}
	}
	if len(reg) != len(expectedKeys) {
		t.Errorf("registry has %d entries, expected %d (drift: declare new keys in expectedKeys)", len(reg), len(expectedKeys))
	}
}

// TestRegistry_DefaultsSnapshot locks the (key → subject, body) pair
// for every registered template. The expected values below are
// hand-mirrored from internal/email/templates.go fallbackSubjects
// (sprint 047) and the .txt embed files. If a refactor changes a
// default body, regenerate by inspecting the diff against the
// pre-refactor binary.
//
// Why hand-mirrored instead of read-from-embed: this is the gate
// that proves the registry didn't silently diverge from the
// pre-migration constants. Comparing the registry against itself
// would tautologically pass.
func TestRegistry_DefaultsSnapshot(t *testing.T) {
	cases := []struct {
		key              string
		wantSubject      string
		wantBodyMustHave []string // substrings that MUST appear in the body
	}{
		{
			key:         KeyEmailPasswordReset,
			wantSubject: "Reset your {{.Branding.ProductName}} password",
			wantBodyMustHave: []string{
				"To choose a new password, open this link within 30 minutes:",
				"{{.Data.ResetURL}}",
				"{{.Branding.ProductName}}",
			},
		},
		{
			key:         KeyEmailAccountLocked,
			wantSubject: "Your {{.Branding.ProductName}} account is temporarily locked",
			wantBodyMustHave: []string{
				"temporarily locked",
				"{{.Data.UnlockAt}}",
			},
		},
		{
			key:              KeyEmailAccountUnlocked,
			wantSubject:      "Your {{.Branding.ProductName}} account has been unlocked",
			wantBodyMustHave: []string{"An administrator has unlocked"},
		},
		{
			key:         KeyEmailTOTPEnabled,
			wantSubject: "Two-factor authentication enabled on your {{.Branding.ProductName}} account",
			wantBodyMustHave: []string{
				"Two-factor authentication has been enabled",
				"{{.Data.RecoveryCodeCount}}",
			},
		},
		{
			key:         KeyEmailTOTPDisabled,
			wantSubject: "Two-factor authentication disabled on your {{.Branding.ProductName}} account",
			wantBodyMustHave: []string{
				"Two-factor authentication has been disabled",
			},
		},
		{
			key:         KeyEmailRecoveryCodesRegenerated,
			wantSubject: "Your {{.Branding.ProductName}} recovery codes were regenerated",
			wantBodyMustHave: []string{
				"A new set of recovery codes has been generated",
				"previously-issued codes are now invalid",
			},
		},
		{
			key:         KeyEmailAPITokenCreated,
			wantSubject: "A new {{.Branding.ProductName}} API token was created",
			wantBodyMustHave: []string{
				"A new API token has been created",
				"{{.Data.TokenName}}",
				"{{.Data.TokenPrefix}}",
			},
		},
		{
			key:         KeyEmailAlertFired,
			wantSubject: "[{{.Branding.ProductName}}] Alert: {{.Subject}}",
			wantBodyMustHave: []string{
				"{{.Branding.ProductName}} alert:",
				"{{.Data.AlertName}}",
				"{{.Data.Severity}}",
				"{{.Data.DashboardURL}}",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			def, ok := Lookup(tc.key)
			if !ok {
				t.Fatalf("no registry entry for %s", tc.key)
			}
			if def.Subject != tc.wantSubject {
				t.Errorf("subject drift for %s:\n got: %q\nwant: %q", tc.key, def.Subject, tc.wantSubject)
			}
			for _, sub := range tc.wantBodyMustHave {
				if !strings.Contains(def.Body, sub) {
					t.Errorf("body for %s missing substring %q\nbody:\n%s", tc.key, sub, def.Body)
				}
			}
		})
	}
}

// TestRegistry_EmailBodiesMatchEmbed proves the bodies the notify
// registry surfaces are byte-identical to the .txt files the email
// Sender renders. This is the byte-identical-by-default guarantee.
func TestRegistry_EmailBodiesMatchEmbed(t *testing.T) {
	pairs := []struct {
		key      string
		embedded string
	}{
		{KeyEmailPasswordReset, bodyEmailPasswordReset},
		{KeyEmailAccountLocked, bodyEmailAccountLocked},
		{KeyEmailAccountUnlocked, bodyEmailAccountUnlocked},
		{KeyEmailTOTPEnabled, bodyEmailTOTPEnabled},
		{KeyEmailTOTPDisabled, bodyEmailTOTPDisabled},
		{KeyEmailRecoveryCodesRegenerated, bodyEmailRecoveryCodesRegenerated},
		{KeyEmailAPITokenCreated, bodyEmailAPITokenCreated},
		{KeyEmailAlertFired, bodyEmailAlertFired},
	}
	for _, p := range pairs {
		def, ok := Lookup(p.key)
		if !ok {
			t.Fatalf("no registry entry for %s", p.key)
		}
		if def.Body != p.embedded {
			t.Errorf("body for %s drifted from embedded snapshot", p.key)
		}
	}
}

// stubQuerier is the test fake satisfying notify.Querier.
type stubQuerier struct {
	rows map[string]sqlc.NotificationTemplate
	err  error
}

func (s *stubQuerier) GetNotificationTemplate(_ context.Context, key string) (sqlc.NotificationTemplate, error) {
	if s.err != nil {
		return sqlc.NotificationTemplate{}, s.err
	}
	row, ok := s.rows[key]
	if !ok {
		return sqlc.NotificationTemplate{}, pgx.ErrNoRows
	}
	return row, nil
}

func TestResolve_FallsBackToDefault(t *testing.T) {
	ctx := context.Background()
	q := &stubQuerier{rows: map[string]sqlc.NotificationTemplate{}}
	got, err := Resolve(ctx, q, KeyEmailPasswordReset)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.HasOverride {
		t.Error("expected HasOverride=false")
	}
	def, _ := Lookup(KeyEmailPasswordReset)
	if got.Subject != def.Subject || got.Body != def.Body {
		t.Errorf("Resolve returned non-default values without an override row")
	}
}

func TestResolve_HonorsOverride(t *testing.T) {
	ctx := context.Background()
	q := &stubQuerier{rows: map[string]sqlc.NotificationTemplate{
		KeyEmailPasswordReset: {
			TemplateKey: KeyEmailPasswordReset,
			Channel:     ChannelEmail,
			SubjectTpl:  "Override subject",
			BodyTpl:     "Override body",
			BodyFormat:  BodyFormatText,
			Enabled:     true,
		},
	}}
	got, err := Resolve(ctx, q, KeyEmailPasswordReset)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !got.HasOverride {
		t.Error("expected HasOverride=true")
	}
	if got.Subject != "Override subject" || got.Body != "Override body" {
		t.Errorf("Resolve didn't pick up override values: %+v", got)
	}
	if got.BodyFormat != BodyFormatText {
		t.Errorf("body format not propagated: got %q", got.BodyFormat)
	}
}

func TestResolve_DisabledOverrideUsesDefault(t *testing.T) {
	ctx := context.Background()
	q := &stubQuerier{rows: map[string]sqlc.NotificationTemplate{
		KeyEmailPasswordReset: {
			TemplateKey: KeyEmailPasswordReset,
			SubjectTpl:  "ignored",
			BodyTpl:     "ignored",
			Enabled:     false,
		},
	}}
	got, err := Resolve(ctx, q, KeyEmailPasswordReset)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.HasOverride {
		t.Error("HasOverride should be false when row is disabled")
	}
	if !got.Disabled {
		t.Error("Disabled flag should be true")
	}
	def, _ := Lookup(KeyEmailPasswordReset)
	if got.Subject != def.Subject || got.Body != def.Body {
		t.Errorf("disabled override should fall back to defaults")
	}
}

func TestResolve_UnknownKey(t *testing.T) {
	ctx := context.Background()
	_, err := Resolve(ctx, &stubQuerier{}, "nonexistent.key")
	if !errors.Is(err, ErrUnknownKey) {
		t.Errorf("expected ErrUnknownKey, got %v", err)
	}
}

// TestRender_Email reproduces an end-to-end render against the
// default password_reset template using the same input shape the
// email Sender supplies. This proves the registry default + the
// Render helper match the pre-migration behaviour.
func TestRender_Email(t *testing.T) {
	def, _ := Lookup(KeyEmailPasswordReset)
	data := map[string]any{
		"Branding": map[string]any{
			"ProductName": "Astronomer",
			"SupportURL":  "https://example.com/support",
			"LoginURL":    "https://dash.example.com",
		},
		"Data": map[string]any{
			"ResetURL": "https://dash.example.com/reset?token=abc",
		},
	}
	res, err := Render(Resolved{Key: def.Key, Subject: def.Subject, Body: def.Body, BodyFormat: def.BodyFormat}, data)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if res.Subject != "Reset your Astronomer password" {
		t.Errorf("subject = %q", res.Subject)
	}
	if !strings.Contains(res.Body, "https://dash.example.com/reset?token=abc") {
		t.Errorf("body missing reset url")
	}
}

func TestCheckRequiredVariables(t *testing.T) {
	def, _ := Lookup(KeyEmailPasswordReset)
	// Missing required Branding.ProductName + Data.ResetURL.
	missing := CheckRequiredVariables(def, map[string]any{})
	if len(missing) == 0 {
		t.Fatalf("expected missing variables, got none")
	}
	// With ResetURL provided as nested.
	missing = CheckRequiredVariables(def, map[string]any{
		"Branding": map[string]any{"ProductName": "P", "SupportURL": "S", "LoginURL": "L"},
		"Data":     map[string]any{"ResetURL": "https://x"},
	})
	if len(missing) != 0 {
		t.Errorf("expected no missing, got %v", missing)
	}
}

// TestRegistry_VariablesNonEmpty asserts every registered template
// declares at least the common-var set. Operator UX gate.
func TestRegistry_VariablesNonEmpty(t *testing.T) {
	for _, d := range Registry() {
		if len(d.Variables) == 0 {
			t.Errorf("template %s declares no variables", d.Key)
		}
	}
}

// canonicalEventJSON tells us what an operator-edited override
// renders against when they choose the toJSON-style format. Sanity
// check that it parses + executes against the canonical data shape.
func TestRender_WebhookCanonical(t *testing.T) {
	def, _ := Lookup(KeyWebhookAuditEvent)
	data := map[string]any{
		"event_name":    "audit.user.login",
		"event_id":      "ev-1",
		"timestamp":     "2026-01-01T00:00:00Z",
		"actor_user_id": "u-1",
		"resource_id":   "r-1",
		"resource_type": "user",
		"delivery_id":   "d-1",
		"detail":        map[string]any{"ip": "1.2.3.4"},
	}
	res, err := Render(Resolved{Key: def.Key, Body: def.Body, BodyFormat: def.BodyFormat}, data)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// Render output must be valid JSON.
	var sink map[string]any
	if err := json.Unmarshal([]byte(res.Body), &sink); err != nil {
		t.Errorf("rendered body is not valid JSON: %v\n%s", err, res.Body)
	}
}
