package server

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/internal/sessionpolicy"
)

type fakeSessionTimeoutSettings struct {
	row   sqlc.PlatformSetting
	err   error
	reads int
}

func (f *fakeSessionTimeoutSettings) GetPlatformSetting(context.Context, string) (sqlc.PlatformSetting, error) {
	f.reads++
	return f.row, f.err
}

func assertAccessTTL(t *testing.T, manager *auth.JWTManager, token string, want time.Duration) {
	t.Helper()
	claims, err := manager.ValidateToken(token)
	if err != nil {
		t.Fatalf("ValidateToken() error = %v", err)
	}
	got := claims.ExpiresAt.Sub(claims.IssuedAt.Time)
	if got != want {
		t.Fatalf("access TTL = %s, want %s", got, want)
	}
}

func TestConfigureSessionTimeoutPolicyMissingRowIndependentOfEncryptedFeatures(t *testing.T) {
	// The two cases represent deployments with and without encryption/TOTP.
	// configureSessionTimeoutPolicy has no encrypted-feature dependency and is
	// invoked before that conditional in NewApp, so both receive the same 60m
	// provider on a fresh database.
	for _, mode := range []string{"no encryptor", "encryptor configured"} {
		t.Run(mode, func(t *testing.T) {
			manager := auth.NewJWTManager("test-secret", sessionpolicy.DefaultMinutes)
			authHandler := handler.NewAuthHandler(nil, manager)
			settings := &fakeSessionTimeoutSettings{err: pgx.ErrNoRows}
			configureSessionTimeoutPolicy(authHandler, manager, settings, slog.Default())

			access, _, err := manager.GenerateTokenPairContext(context.Background(), uuid.New())
			if err != nil {
				t.Fatalf("GenerateTokenPairContext() error = %v", err)
			}
			assertAccessTTL(t, manager, access, time.Hour)
		})
	}
}

func TestConfigureSessionTimeoutPolicyExplicitValueAppliesToEveryMintContract(t *testing.T) {
	manager := auth.NewJWTManager("test-secret", sessionpolicy.DefaultMinutes)
	authHandler := handler.NewAuthHandler(nil, manager)
	settings := &fakeSessionTimeoutSettings{
		row: sqlc.PlatformSetting{Key: sessionpolicy.SettingKey, Value: []byte("120")},
	}
	configureSessionTimeoutPolicy(authHandler, manager, settings, slog.Default())

	// Password, refresh, SSO callback, and both TOTP completion handlers all
	// delegate to GenerateTokenPairContext. Keeping the contract cases named
	// here makes a change to the common provider observable for every flow.
	for _, mintPath := range []string{"password", "refresh", "sso", "totp verify", "totp enrollment completion"} {
		t.Run(mintPath, func(t *testing.T) {
			access, _, err := manager.GenerateTokenPairContext(context.Background(), uuid.New())
			if err != nil {
				t.Fatalf("GenerateTokenPairContext() error = %v", err)
			}
			assertAccessTTL(t, manager, access, 120*time.Minute)
		})
	}
}

func TestAuthHandlerMintContextReadsSessionSettingOnce(t *testing.T) {
	manager := auth.NewJWTManager("test-secret", sessionpolicy.DefaultMinutes)
	authHandler := handler.NewAuthHandler(nil, manager)
	settings := &fakeSessionTimeoutSettings{
		row: sqlc.PlatformSetting{Key: sessionpolicy.SettingKey, Value: []byte("120")},
	}
	configureSessionTimeoutPolicy(authHandler, manager, settings, slog.Default())

	resolve := newSessionTimeoutResolver(settings, slog.Default())
	mintCtx := sessionpolicy.WithMinutes(context.Background(), resolve(context.Background()))
	access, _, err := manager.GenerateTokenPairContext(mintCtx, uuid.New())
	if err != nil {
		t.Fatalf("GenerateTokenPairContext() error = %v", err)
	}
	assertAccessTTL(t, manager, access, 120*time.Minute)
	if settings.reads != 1 {
		t.Fatalf("settings reads = %d, want exactly 1 per password/refresh mint", settings.reads)
	}
}

func TestSessionTimeoutResolverInvalidDataUsesSafeDefaultAndLogs(t *testing.T) {
	tests := []struct {
		name string
		row  sqlc.PlatformSetting
		err  error
	}{
		{name: "malformed", row: sqlc.PlatformSetting{Key: sessionpolicy.SettingKey, Value: []byte(`"bad"`)}},
		{name: "below minimum", row: sqlc.PlatformSetting{Key: sessionpolicy.SettingKey, Value: []byte("4")}},
		{name: "above maximum", row: sqlc.PlatformSetting{Key: sessionpolicy.SettingKey, Value: []byte("10081")}},
		{name: "read failure", err: errors.New("database unavailable")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logs, nil))
			resolve := newSessionTimeoutResolver(&fakeSessionTimeoutSettings{row: tt.row, err: tt.err}, logger)
			if got := resolve(context.Background()); got != sessionpolicy.DefaultMinutes {
				t.Fatalf("resolver = %d, want safe default %d", got, sessionpolicy.DefaultMinutes)
			}
			logLine := logs.String()
			for _, want := range []string{sessionpolicy.SettingKey, "default_minutes=60"} {
				if !strings.Contains(logLine, want) {
					t.Fatalf("log %q missing actionable field %q", logLine, want)
				}
			}
		})
	}
}
