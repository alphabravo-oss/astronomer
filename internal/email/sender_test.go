package email

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/smtp"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// recordingClient captures every SMTPClient call so the assertions
// below can prove the protocol sequence (HELO -> AUTH -> MAIL -> RCPT
// -> DATA -> body -> QUIT) without speaking to a real relay.
type recordingClient struct {
	steps       []string
	body        bytes.Buffer
	authMech    string
	failOn      string // step name to fail at; "" for success
	hasStartTLS bool
}

type stepWriter struct{ c *recordingClient }

func (w *stepWriter) Write(p []byte) (int, error) { return w.c.body.Write(p) }
func (w *stepWriter) Close() error                { w.c.steps = append(w.c.steps, "body-close"); return nil }

func (c *recordingClient) record(step string) error {
	c.steps = append(c.steps, step)
	if c.failOn == step {
		return fmt.Errorf("recordingClient: simulated failure at %s", step)
	}
	return nil
}

func (c *recordingClient) Hello(_ string) error         { return c.record("HELLO") }
func (c *recordingClient) StartTLS(_ *tls.Config) error { return c.record("STARTTLS") }
func (c *recordingClient) Extension(ext string) (bool, string) {
	if ext == "STARTTLS" {
		return c.hasStartTLS, ""
	}
	return false, ""
}
func (c *recordingClient) Auth(a smtp.Auth) error {
	c.authMech, _, _ = a.Start(&smtp.ServerInfo{Name: "test", TLS: true, Auth: []string{"PLAIN"}})
	return c.record("AUTH")
}
func (c *recordingClient) Mail(_ string) error { return c.record("MAIL") }
func (c *recordingClient) Rcpt(_ string) error { return c.record("RCPT") }
func (c *recordingClient) Data() (writeCloser, error) {
	if err := c.record("DATA"); err != nil {
		return nil, err
	}
	return &stepWriter{c: c}, nil
}
func (c *recordingClient) Quit() error  { return c.record("QUIT") }
func (c *recordingClient) Close() error { return nil }

func TestSender_RendersTemplate(t *testing.T) {
	rc := &recordingClient{hasStartTLS: true}
	s := NewSender(StaticSettingsProvider{Cfg: Settings{
		Enabled:       true,
		Host:          "smtp.example.com",
		Port:          587,
		Username:      "u",
		Password:      "p",
		FromAddress:   "noreply@example.com",
		FromName:      "Astronomer",
		AuthMechanism: "plain",
		Encryption:    "starttls",
		RequireTLS:    true,
		Timeout:       2 * time.Second,
	}}, nil, nil)
	s.SetDialer(func(_ context.Context, _ string, _ *tls.Config, _ bool, _ time.Duration) (SMTPClient, error) {
		return rc, nil
	})

	err := s.Send(context.Background(), Message{
		To:       "alice@example.com",
		Template: TemplatePasswordReset,
		Data:     map[string]any{"ResetURL": "https://app.example.com/reset?token=abc"},
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	wantSteps := []string{"HELLO", "STARTTLS", "AUTH", "MAIL", "RCPT", "DATA", "body-close", "QUIT"}
	if got := strings.Join(rc.steps, ","); got != strings.Join(wantSteps, ",") {
		t.Fatalf("steps mismatch: got %s", got)
	}
	body := rc.body.String()
	if !strings.Contains(body, "Reset your Astronomer password") {
		t.Errorf("subject not in body: %s", body)
	}
	if !strings.Contains(body, "https://app.example.com/reset?token=3Dabc") &&
		!strings.Contains(body, "https://app.example.com/reset?token=abc") {
		// quoted-printable encodes '=' as '=3D'; either form is fine.
		t.Errorf("reset url not in body: %s", body)
	}
	if !strings.Contains(body, "text/plain") || !strings.Contains(body, "text/html") {
		t.Errorf("multipart/alternative not assembled: %s", body)
	}
}

type fakeBrandingProvider struct {
	called atomic.Int32
	brand  Branding
}

func (f *fakeBrandingProvider) Branding(_ context.Context) Branding {
	f.called.Add(1)
	return f.brand
}

func TestSender_FallbackBranding(t *testing.T) {
	rc := &recordingClient{hasStartTLS: true}
	s := NewSender(StaticSettingsProvider{Cfg: Settings{
		Enabled: true, Host: "smtp", Port: 25, FromAddress: "n@e.com",
		AuthMechanism: "none", Encryption: "none", Timeout: time.Second,
	}}, nil, nil)
	s.SetDialer(func(_ context.Context, _ string, _ *tls.Config, _ bool, _ time.Duration) (SMTPClient, error) {
		return rc, nil
	})

	// Brand provider returns empty strings — Sender should fall back
	// to DefaultBranding rather than render with blank values.
	fp := &fakeBrandingProvider{brand: Branding{}}
	s.SetBrandingProvider(fp)
	if err := s.Send(context.Background(), Message{
		To: "a@b.com", Template: TemplateAccountUnlocked,
		Data: map[string]any{"Username": "alice"},
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if fp.called.Load() != 1 {
		t.Errorf("branding provider should have been called exactly once")
	}
	if !strings.Contains(rc.body.String(), DefaultBranding.ProductName) {
		t.Errorf("default branding not rendered: %s", rc.body.String())
	}
}

func TestSender_FailsClosedWhenSMTPDisabled(t *testing.T) {
	dialCalled := atomic.Int32{}
	s := NewSender(StaticSettingsProvider{Cfg: Settings{Enabled: false}}, nil, nil)
	s.SetDialer(func(_ context.Context, _ string, _ *tls.Config, _ bool, _ time.Duration) (SMTPClient, error) {
		dialCalled.Add(1)
		return nil, errors.New("must not dial")
	})
	err := s.Send(context.Background(), Message{To: "a@b.com", Template: TemplateAccountLocked, Data: map[string]any{"Username": "x", "UnlockAt": "now"}})
	if !errors.Is(err, ErrSMTPDisabled) {
		t.Fatalf("want ErrSMTPDisabled, got %v", err)
	}
	if dialCalled.Load() != 0 {
		t.Errorf("Send should not dial when SMTP disabled")
	}
}

func TestSender_RetriesOnTransientError(t *testing.T) {
	// Two-attempt loop: first dial fails, second succeeds. This
	// validates that the dispatcher's retry policy (it will call
	// Send again on error) flows through the test seam correctly —
	// we don't have a retry IN Sender itself, that's by design (the
	// dispatcher owns the budget).
	rc := &recordingClient{hasStartTLS: false}
	calls := atomic.Int32{}
	s := NewSender(StaticSettingsProvider{Cfg: Settings{
		Enabled: true, Host: "smtp", Port: 25, FromAddress: "n@e.com",
		AuthMechanism: "none", Encryption: "none", Timeout: time.Second,
	}}, nil, nil)
	s.SetDialer(func(_ context.Context, _ string, _ *tls.Config, _ bool, _ time.Duration) (SMTPClient, error) {
		n := calls.Add(1)
		if n == 1 {
			return nil, errors.New("connection refused")
		}
		return rc, nil
	})

	if err := s.Send(context.Background(), Message{To: "a@b.com", Template: TemplateTOTPEnabled, Data: map[string]any{"Username": "x", "RecoveryCodeCount": 10}}); err == nil {
		t.Fatalf("expected first attempt to fail")
	}
	if err := s.Send(context.Background(), Message{To: "a@b.com", Template: TemplateTOTPEnabled, Data: map[string]any{"Username": "x", "RecoveryCodeCount": 10}}); err != nil {
		t.Fatalf("second attempt: %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("expected 2 dial attempts, got %d", calls.Load())
	}
}

func TestSender_RejectsInvalidFromAddress(t *testing.T) {
	s := NewSender(StaticSettingsProvider{Cfg: Settings{
		Enabled: true, Host: "smtp", Port: 25, FromAddress: "<<<bad",
		AuthMechanism: "none", Encryption: "none", Timeout: time.Second,
	}}, nil, nil)
	s.SetDialer(func(_ context.Context, _ string, _ *tls.Config, _ bool, _ time.Duration) (SMTPClient, error) {
		t.Fatalf("dialer should not be called")
		return nil, nil
	})
	err := s.Send(context.Background(), Message{To: "a@b.com", Template: TemplateAccountUnlocked, Data: map[string]any{"Username": "x"}})
	if !errors.Is(err, ErrInvalidFromAddress) {
		t.Fatalf("want ErrInvalidFromAddress, got %v", err)
	}
}

func TestSender_RequireTLS_FailsClosedWhenNotAdvertised(t *testing.T) {
	// require_tls=true + relay does NOT advertise STARTTLS → fail
	// closed; sender must refuse to AUTH in plaintext.
	rc := &recordingClient{hasStartTLS: false}
	s := NewSender(StaticSettingsProvider{Cfg: Settings{
		Enabled: true, Host: "smtp", Port: 587, FromAddress: "n@e.com",
		AuthMechanism: "plain", Encryption: "starttls", RequireTLS: true,
		Username: "u", Password: "p", Timeout: time.Second,
	}}, nil, nil)
	s.SetDialer(func(_ context.Context, _ string, _ *tls.Config, _ bool, _ time.Duration) (SMTPClient, error) {
		return rc, nil
	})
	err := s.Send(context.Background(), Message{To: "a@b.com", Template: TemplateAPITokenCreated, Data: map[string]any{
		"Username": "x", "TokenName": "ci", "TokenPrefix": "ast_", "CreatedAt": "now",
	}})
	if err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("expected STARTTLS error, got %v", err)
	}
}
