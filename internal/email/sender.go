package email

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
)

// SingletonSettingsID is the well-known UUID for the singleton smtp_settings
// row. The handler always reads/writes against this id; storing the row
// in a UUID-keyed table is consistency with every other settings table
// in the schema (vs. a serial 1 magic-row).
var SingletonSettingsID = uuid.MustParse("00000000-0000-0000-0000-000000000001")

// Settings is the runtime view of smtp_settings. The encrypted password
// column is decrypted INTO Password before Send runs; callers MUST NOT
// log Password.
type Settings struct {
	Enabled       bool
	Host          string
	Port          int
	Username      string
	Password      string
	FromAddress   string
	FromName      string
	AuthMechanism string // "plain" | "login" | "cram-md5" | "none"
	Encryption    string // "starttls" | "tls" | "none"
	RequireTLS    bool
	Timeout       time.Duration
}

// Addr returns the host:port the dialer connects to.
func (s Settings) Addr() string {
	port := s.Port
	if port <= 0 {
		port = 587
	}
	return net.JoinHostPort(s.Host, strconv.Itoa(port))
}

// SettingsProvider is the indirection between the persisted Fernet
// ciphertext row and a usable Settings struct. Implementations cache
// behind their own locking — Sender calls Provide() before every send.
type SettingsProvider interface {
	Provide(ctx context.Context) (Settings, error)
}

// BrandingProvider supplies the {{.Branding}} block rendered into
// every template. Sender holds a reference so the handler-side cache
// (platform_configuration / future platform_settings) is single-
// sourced. When the provider is nil the package-level defaults
// (DefaultBranding) are used.
type BrandingProvider interface {
	Branding(ctx context.Context) Branding
}

// DefaultBranding ships with the binary so the worker can render even
// before platform_configuration has been seeded.
var DefaultBranding = Branding{
	ProductName: "Astronomer",
	SupportURL:  "https://github.com/alphabravocompany/astronomer-go",
	LoginURL:    "",
}

// SMTPClient is the subset of *smtp.Client used by Sender. Defined as
// an interface so tests can substitute a recorded fake — net/smtp's
// concrete *smtp.Client has no exported test seam and a real dial
// would slow the unit-test suite massively.
type SMTPClient interface {
	Hello(localName string) error
	StartTLS(config *tls.Config) error
	Extension(ext string) (bool, string)
	Auth(a smtp.Auth) error
	Mail(from string) error
	Rcpt(to string) error
	Data() (writeCloser, error)
	Quit() error
	Close() error
}

// writeCloser is the io.WriteCloser shape returned by smtp.Client.Data
// (we can't reference io.WriteCloser directly because we'd then need
// to adapt *smtp.Client; this matches the smtp.Client signature byte-
// for-byte).
type writeCloser interface {
	Write(p []byte) (int, error)
	Close() error
}

// Dialer abstracts the smtp.Dial / smtp.DialTLS step. Production wires
// realDialer; tests swap in a fake that returns a recording client.
type Dialer func(ctx context.Context, addr string, tlsConfig *tls.Config, useTLS bool, timeout time.Duration) (SMTPClient, error)

// Sender is the email-send entry point. It is safe for concurrent use
// by the dispatch worker; each Send call constructs a fresh SMTP
// connection (HEY/QUIT). The trade-off vs. a long-lived pool is
// simplicity — the platform sends tens of emails per hour at peak,
// and a relay reconnect cost (~10 ms) is invisible at that rate.
type Sender struct {
	cfg       SettingsProvider
	branding  BrandingProvider
	encryptor *auth.Encryptor
	log       *slog.Logger
	now       func() time.Time
	dial      Dialer
}

// NewSender wires the producer-side dependencies. dial defaults to the
// real net/smtp dialer; tests override via WithDialer.
func NewSender(cfg SettingsProvider, encryptor *auth.Encryptor, log *slog.Logger) *Sender {
	if log == nil {
		log = slog.Default()
	}
	return &Sender{
		cfg:       cfg,
		encryptor: encryptor,
		log:       log,
		now:       time.Now,
		dial:      realDialer,
	}
}

// SetBrandingProvider attaches the per-tenant branding source. Without
// it, every template renders with DefaultBranding — fine for the
// startup window before platform_configuration is queried, but
// suboptimal for steady state.
func (s *Sender) SetBrandingProvider(b BrandingProvider) { s.branding = b }

// SetDialer is the test seam. Production code should never call this.
func (s *Sender) SetDialer(d Dialer) {
	if d != nil {
		s.dial = d
	}
}

// SetNow lets tests pin the clock used in test-message bodies and
// audit timestamps.
func (s *Sender) SetNow(now func() time.Time) {
	if now != nil {
		s.now = now
	}
}

// Message is the per-send payload. Used by Sender.Send and by the
// dispatcher when it reconstructs a Send from an email_messages row.
type Message struct {
	To       string
	CC       string
	Template string
	// Subject overrides the template-default subject. Optional; left
	// empty for everything except the alert_fired template, whose
	// subject contains the alert name + severity.
	Subject string
	Data    any
}

// ErrSMTPDisabled is returned from Send when smtp_settings.enabled =
// false. Callers map this to status='skipped' on the email_messages
// row so operators can see the gap in the admin view.
var ErrSMTPDisabled = errors.New("smtp delivery is disabled")

// ErrInvalidFromAddress is returned from Send when the configured
// FROM address fails net/mail.ParseAddress. We fail closed so a
// misconfigured FROM doesn't propagate into the SMTP MAIL FROM line.
var ErrInvalidFromAddress = errors.New("smtp from_address is invalid")

// Send delivers a single Message. Returns nil on a successful relay
// hand-off (the SMTP server accepted DATA); any non-nil error is
// either ErrSMTPDisabled, a render error, or a transient network
// failure that the dispatcher will retry.
func (s *Sender) Send(ctx context.Context, msg Message) error {
	cfg, err := s.cfg.Provide(ctx)
	if err != nil {
		return fmt.Errorf("load smtp settings: %w", err)
	}
	if !cfg.Enabled {
		return ErrSMTPDisabled
	}
	if cfg.Host == "" {
		return fmt.Errorf("smtp host is not configured")
	}
	if cfg.FromAddress == "" {
		return ErrInvalidFromAddress
	}
	if _, err := mail.ParseAddress(cfg.FromAddress); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidFromAddress, err)
	}
	if msg.To == "" {
		return fmt.Errorf("recipient is required")
	}

	// Render the templates. Branding falls back to DefaultBranding
	// when no provider is wired — keeps the unit tests independent
	// of the platform_configuration table.
	branding := DefaultBranding
	if s.branding != nil {
		branding = s.branding.Branding(ctx)
		if branding.ProductName == "" {
			branding.ProductName = DefaultBranding.ProductName
		}
		if branding.SupportURL == "" {
			branding.SupportURL = DefaultBranding.SupportURL
		}
	}
	rendered, err := Render(msg.Template, branding, msg.Subject, msg.Data)
	if err != nil {
		return err
	}
	subject := rendered.Subject
	if msg.Template == TemplateAlertFired && msg.Subject != "" {
		// The alert template subject embeds the alert name supplied
		// by the caller; the rendered subject is already that
		// substitution.
		_ = subject
	}

	return s.deliver(ctx, cfg, subject, msg.To, msg.CC, rendered.BodyText, rendered.BodyHTML)
}

// SendPreRendered delivers a message whose subject/body were already rendered
// at enqueue time, WITHOUT re-running the template engine. The email dispatcher
// uses this so a queued notification ships the body it was rendered with rather
// than re-rendering the template against an empty data bag (which produced
// blank/garbled emails). Validation + SMTP transport mirror Send exactly.
func (s *Sender) SendPreRendered(ctx context.Context, to, cc, subject, bodyText, bodyHTML string) error {
	cfg, err := s.cfg.Provide(ctx)
	if err != nil {
		return fmt.Errorf("load smtp settings: %w", err)
	}
	if !cfg.Enabled {
		return ErrSMTPDisabled
	}
	if cfg.Host == "" {
		return fmt.Errorf("smtp host is not configured")
	}
	if cfg.FromAddress == "" {
		return ErrInvalidFromAddress
	}
	if _, err := mail.ParseAddress(cfg.FromAddress); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidFromAddress, err)
	}
	if to == "" {
		return fmt.Errorf("recipient is required")
	}
	return s.deliver(ctx, cfg, subject, to, cc, bodyText, bodyHTML)
}

// deliver composes the MIME multipart/alternative body and performs the SMTP
// transport. Shared by Send (post-render) and SendPreRendered.
func (s *Sender) deliver(ctx context.Context, cfg Settings, subject, to, cc, bodyText, bodyHTML string) error {
	// MIME message. We assemble a multipart/alternative with the
	// text part first (older clients prefer the first part) and the
	// HTML part second.
	from := cfg.FromAddress
	if cfg.FromName != "" {
		from = formatAddr(cfg.FromName, cfg.FromAddress)
	}
	body, err := composeMessage(subject, from, to, cc, bodyText, bodyHTML)
	if err != nil {
		return fmt.Errorf("compose mime: %w", err)
	}

	useTLS := strings.EqualFold(cfg.Encryption, "tls")
	useSTARTTLS := strings.EqualFold(cfg.Encryption, "starttls")
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	tlsConfig := &tls.Config{
		ServerName: cfg.Host,
		MinVersion: tls.VersionTLS12,
	}

	client, err := s.dial(ctx, cfg.Addr(), tlsConfig, useTLS, timeout)
	if err != nil {
		return fmt.Errorf("smtp dial %s: %w", cfg.Addr(), err)
	}
	defer func() { _ = client.Close() }()

	if err := client.Hello(localHostname()); err != nil {
		return fmt.Errorf("smtp HELO: %w", err)
	}
	if useSTARTTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok && cfg.RequireTLS {
			return fmt.Errorf("smtp relay does not advertise STARTTLS but require_tls=true")
		} else if ok {
			if err := client.StartTLS(tlsConfig); err != nil {
				return fmt.Errorf("smtp STARTTLS: %w", err)
			}
		}
	}

	if authMech := strings.ToLower(cfg.AuthMechanism); authMech != "" && authMech != "none" && cfg.Username != "" {
		a, err := buildSMTPAuth(authMech, cfg.Username, cfg.Password, cfg.Host)
		if err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
		if err := client.Auth(a); err != nil {
			return fmt.Errorf("smtp AUTH: %w", err)
		}
	}

	if err := client.Mail(cfg.FromAddress); err != nil {
		return fmt.Errorf("smtp MAIL FROM: %w", err)
	}
	for _, addr := range allRecipients(to, cc) {
		if err := client.Rcpt(addr); err != nil {
			return fmt.Errorf("smtp RCPT TO %s: %w", addr, err)
		}
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp DATA: %w", err)
	}
	if _, err := w.Write(body); err != nil {
		_ = w.Close()
		return fmt.Errorf("smtp body write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp body close: %w", err)
	}
	_ = client.Quit()
	return nil
}

// allRecipients returns the union of To + CC after trimming + de-
// duplicating. SMTP RCPT TO needs each recipient enumerated even
// though the BCC equivalent is implicit in the envelope.
func allRecipients(to, cc string) []string {
	out := []string{strings.TrimSpace(to)}
	if cc == "" {
		return out
	}
	for _, raw := range strings.Split(cc, ",") {
		v := strings.TrimSpace(raw)
		if v == "" || v == out[0] {
			continue
		}
		out = append(out, v)
	}
	return out
}

func localHostname() string {
	// best-effort; net/smtp uses "localhost" when HELO is invoked
	// without an explicit name. We pin it so the EHLO line carries a
	// stable string that operators can grep in their relay logs.
	if hn, err := net.LookupCNAME("localhost"); err == nil && hn != "" {
		return strings.TrimSuffix(hn, ".")
	}
	return "astronomer-go"
}

// buildSMTPAuth maps the configured mechanism string to a net/smtp Auth.
// CRAM-MD5 is supported because some enterprise relays still require
// it; LOGIN is the legacy ESMTP base64 dance.
func buildSMTPAuth(mech, user, pass, host string) (smtp.Auth, error) {
	switch mech {
	case "plain":
		return smtp.PlainAuth("", user, pass, host), nil
	case "login":
		return loginAuth{username: user, password: pass}, nil
	case "cram-md5":
		return smtp.CRAMMD5Auth(user, pass), nil
	default:
		return nil, fmt.Errorf("unsupported auth mechanism %q", mech)
	}
}

// loginAuth is the minimal LOGIN dance. net/smtp ships PLAIN +
// CRAM-MD5 but not LOGIN; we implement it here so we don't have to
// pull a separate dependency.
type loginAuth struct {
	username, password string
}

func (l loginAuth) Start(_ *smtp.ServerInfo) (string, []byte, error) {
	return "LOGIN", nil, nil
}

func (l loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimSpace(string(fromServer))) {
	case "username:":
		return []byte(l.username), nil
	case "password:":
		return []byte(l.password), nil
	}
	return nil, fmt.Errorf("unexpected LOGIN prompt: %q", fromServer)
}

// formatAddr renders a `"Name" <addr>` header value. We quote the
// display name unconditionally so a name with a comma or angle bracket
// doesn't corrupt the From header.
func formatAddr(name, addr string) string {
	if name == "" {
		return addr
	}
	a := &mail.Address{Name: name, Address: addr}
	return a.String()
}

// realDialer is the production Dialer. It mirrors smtp.Dial / DialTLS
// with the context-aware timeout the higher layers want.
func realDialer(ctx context.Context, addr string, tlsConfig *tls.Config, useTLS bool, timeout time.Duration) (SMTPClient, error) {
	dialer := &net.Dialer{Timeout: timeout}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	var conn net.Conn
	if useTLS {
		tlsDialer := &tls.Dialer{NetDialer: dialer, Config: tlsConfig}
		conn, err = tlsDialer.DialContext(ctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return nil, err
	}
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return &smtpClientWrapper{c: c}, nil
}

// smtpClientWrapper adapts *smtp.Client to the SMTPClient interface
// the rest of this file works against. The wrapper is needed so the
// Data() return type matches our local writeCloser interface (Go does
// not auto-widen io.WriteCloser to our anonymous interface).
type smtpClientWrapper struct{ c *smtp.Client }

func (w *smtpClientWrapper) Hello(localName string) error        { return w.c.Hello(localName) }
func (w *smtpClientWrapper) StartTLS(cfg *tls.Config) error      { return w.c.StartTLS(cfg) }
func (w *smtpClientWrapper) Extension(ext string) (bool, string) { return w.c.Extension(ext) }
func (w *smtpClientWrapper) Auth(a smtp.Auth) error              { return w.c.Auth(a) }
func (w *smtpClientWrapper) Mail(from string) error              { return w.c.Mail(from) }
func (w *smtpClientWrapper) Rcpt(to string) error                { return w.c.Rcpt(to) }
func (w *smtpClientWrapper) Data() (writeCloser, error) {
	wc, err := w.c.Data()
	if err != nil {
		return nil, err
	}
	return wc, nil
}
func (w *smtpClientWrapper) Quit() error  { return w.c.Quit() }
func (w *smtpClientWrapper) Close() error { return w.c.Close() }
