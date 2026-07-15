package server

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/internal/sessionpolicy"
)

type sessionTimeoutSettingQuerier interface {
	GetPlatformSetting(ctx context.Context, key string) (sqlc.PlatformSetting, error)
}

// configureSessionTimeoutPolicy wires the same bounded resolver into both the
// auth handler and JWT manager. It deliberately has no dependency on the
// encryptor/TOTP/SSO setup: all access-token mint paths must honor the runtime
// setting even in a password-only deployment.
func configureSessionTimeoutPolicy(
	authHandler *handler.AuthHandler,
	jwtManager *auth.JWTManager,
	queries sessionTimeoutSettingQuerier,
	logger *slog.Logger,
) {
	resolveMinutes := newSessionTimeoutResolver(queries, logger)
	authHandler.SetSessionTimeoutPolicy(resolveMinutes)
	jwtManager.SetAccessTokenTTLProvider(func(ctx context.Context) time.Duration {
		if minutes, ok := sessionpolicy.MinutesFromContext(ctx); ok {
			return time.Duration(minutes) * time.Minute
		}
		return time.Duration(resolveMinutes(ctx)) * time.Minute
	})
}

func newSessionTimeoutResolver(queries sessionTimeoutSettingQuerier, logger *slog.Logger) func(context.Context) int {
	if logger == nil {
		logger = slog.Default()
	}
	return func(ctx context.Context) int {
		if queries == nil {
			logger.Error("session timeout settings store is unavailable; using safe default",
				"setting_key", sessionpolicy.SettingKey,
				"default_minutes", sessionpolicy.DefaultMinutes,
			)
			return sessionpolicy.DefaultMinutes
		}

		row, err := queries.GetPlatformSetting(ctx, sessionpolicy.SettingKey)
		if errors.Is(err, pgx.ErrNoRows) {
			return sessionpolicy.DefaultMinutes
		}
		if err != nil {
			logger.Error("session timeout setting read failed; using safe default",
				"setting_key", sessionpolicy.SettingKey,
				"default_minutes", sessionpolicy.DefaultMinutes,
				"error", err,
			)
			return sessionpolicy.DefaultMinutes
		}

		minutes, parseErr := sessionpolicy.ParseMinutes(row.Value)
		if parseErr != nil {
			logger.Error("session timeout setting is invalid; using safe default",
				"setting_key", sessionpolicy.SettingKey,
				"default_minutes", sessionpolicy.DefaultMinutes,
				"minimum_minutes", sessionpolicy.MinMinutes,
				"maximum_minutes", sessionpolicy.MaxMinutes,
				"error", parseErr,
			)
		}
		return minutes
	}
}
