package email

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// SettingsQuerier is the database surface a SQLSettingsProvider needs.
// Implemented by *sqlc.Queries; declared locally so the worker /
// handler call sites can wire a narrow fake into tests without
// dragging the rest of the schema along.
type SettingsQuerier interface {
	GetSMTPSettings(ctx context.Context, id uuid.UUID) (sqlc.SmtpSettings, error)
}

// SQLSettingsProvider loads SMTP settings from the smtp_settings row
// and decrypts the password through auth.Encryptor. A tiny TTL cache
// (5 s) sits in front so the dispatcher worker doesn't hammer the
// table 50× per tick on a busy queue.
type SQLSettingsProvider struct {
	q         SettingsQuerier
	encryptor *auth.Encryptor
	ttl       time.Duration

	mu     sync.Mutex
	cached *Settings
	expiry time.Time
}

// NewSQLSettingsProvider wires the production provider. ttl <= 0
// disables caching (handy in tests that mutate the row between
// reads).
func NewSQLSettingsProvider(q SettingsQuerier, encryptor *auth.Encryptor, ttl time.Duration) *SQLSettingsProvider {
	return &SQLSettingsProvider{q: q, encryptor: encryptor, ttl: ttl}
}

// Invalidate drops the cached row. Called from the handler PUT path
// so a fresh settings push is visible on the next dispatcher tick
// instead of waiting out the TTL.
func (p *SQLSettingsProvider) Invalidate() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cached = nil
	p.expiry = time.Time{}
}

// Provide returns the current Settings. The error path:
//   * no smtp_settings row yet → returns (empty, nil) so the dispatch
//     worker logs "smtp disabled" cleanly and skips the send.
//   * fernet decrypt fail → returns a non-nil error; the dispatcher
//     marks the row failed (it'd otherwise loop forever).
func (p *SQLSettingsProvider) Provide(ctx context.Context) (Settings, error) {
	p.mu.Lock()
	if p.cached != nil && time.Now().Before(p.expiry) {
		out := *p.cached
		p.mu.Unlock()
		return out, nil
	}
	p.mu.Unlock()

	row, err := p.q.GetSMTPSettings(ctx, SingletonSettingsID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// No row yet — caller treats this as "disabled".
			out := Settings{Enabled: false}
			p.store(out)
			return out, nil
		}
		return Settings{}, fmt.Errorf("read smtp_settings: %w", err)
	}

	out := Settings{
		Enabled:       row.Enabled,
		Host:          row.Host,
		Port:          int(row.Port),
		Username:      row.Username,
		FromAddress:   row.FromAddress,
		FromName:      row.FromName,
		AuthMechanism: row.AuthMechanism,
		Encryption:    row.Encryption,
		RequireTLS:    row.RequireTls,
		Timeout:       time.Duration(row.TimeoutSeconds) * time.Second,
	}
	if row.PasswordEncrypted != "" && p.encryptor != nil {
		plain, err := p.encryptor.Decrypt(row.PasswordEncrypted)
		if err != nil {
			return Settings{}, fmt.Errorf("decrypt smtp password: %w", err)
		}
		out.Password = plain
	}
	p.store(out)
	return out, nil
}

func (p *SQLSettingsProvider) store(s Settings) {
	if p.ttl <= 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	cp := s
	p.cached = &cp
	p.expiry = time.Now().Add(p.ttl)
}

// StaticSettingsProvider is for unit tests + the "smtp test send"
// admin path that wants to dry-run a candidate config without first
// PUTting it to the DB.
type StaticSettingsProvider struct {
	Cfg Settings
}

func (p StaticSettingsProvider) Provide(_ context.Context) (Settings, error) { return p.Cfg, nil }

// PlatformConfigQuerier is the minimal surface needed to render
// branding (product name + dashboard URL) from the platform_configuration
// row. Implemented by *sqlc.Queries.
type PlatformConfigQuerier interface {
	GetPlatformConfig(ctx context.Context) (sqlc.PlatformConfiguration, error)
}

// PlatformConfigBrandingProvider reads product_name + server_url from
// platform_configuration on every call and falls back to DefaultBranding
// for empty/missing values. The query is cheap (singleton row) but we
// still gate it behind a 30s cache so a burst of email sends doesn't
// hammer the table.
type PlatformConfigBrandingProvider struct {
	q          PlatformConfigQuerier
	supportURL string

	mu     sync.Mutex
	cached Branding
	expiry time.Time
}

// NewPlatformConfigBrandingProvider wires the production provider.
// supportURL is the operator-configured help link (typically the
// platform's docs URL); when empty, DefaultBranding.SupportURL fills
// in.
func NewPlatformConfigBrandingProvider(q PlatformConfigQuerier, supportURL string) *PlatformConfigBrandingProvider {
	return &PlatformConfigBrandingProvider{q: q, supportURL: supportURL}
}

func (p *PlatformConfigBrandingProvider) Branding(ctx context.Context) Branding {
	p.mu.Lock()
	if time.Now().Before(p.expiry) {
		out := p.cached
		p.mu.Unlock()
		return out
	}
	p.mu.Unlock()

	out := DefaultBranding
	if p.supportURL != "" {
		out.SupportURL = p.supportURL
	}
	if p.q != nil {
		if row, err := p.q.GetPlatformConfig(ctx); err == nil {
			if row.PlatformName != "" {
				out.ProductName = row.PlatformName
			}
			if row.ServerUrl != "" {
				out.LoginURL = row.ServerUrl
			}
		}
	}

	p.mu.Lock()
	p.cached = out
	p.expiry = time.Now().Add(30 * time.Second)
	p.mu.Unlock()
	return out
}
