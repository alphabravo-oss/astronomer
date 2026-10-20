package server

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

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
	logger zerolog.Logger,
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

func newSessionTimeoutResolver(queries sessionTimeoutSettingQuerier, logger zerolog.Logger) func(context.Context) int {
	return func(ctx context.Context) int {
		if queries == nil {
			logger.Error().Str("setting_key", sessionpolicy.SettingKey).Int("default_minutes", sessionpolicy.DefaultMinutes).Msg("session timeout settings store is unavailable; using safe default")
			return sessionpolicy.DefaultMinutes
		}

		row, err := queries.GetPlatformSetting(ctx, sessionpolicy.SettingKey)
		if errors.Is(err, pgx.ErrNoRows) {
			return sessionpolicy.DefaultMinutes
		}
		if err != nil {
			logger.Error().Str("setting_key", sessionpolicy.SettingKey).Int("default_minutes", sessionpolicy.DefaultMinutes).Err(err).Msg("session timeout setting read failed; using safe default")
			return sessionpolicy.DefaultMinutes
		}

		minutes, parseErr := sessionpolicy.ParseMinutes(row.Value)
		if parseErr != nil {
			logger.Error().Str("setting_key", sessionpolicy.SettingKey).Int("default_minutes", sessionpolicy.DefaultMinutes).Int("minimum_minutes", sessionpolicy.MinMinutes).Int("maximum_minutes", sessionpolicy.MaxMinutes).Err(parseErr).Msg("session timeout setting is invalid; using safe default")
		}
		return minutes
	}
}
