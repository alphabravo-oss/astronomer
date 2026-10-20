package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/db"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/alphabravocompany/astronomer-go/pkg/crypto"
)

func (c *productionComposition) initializePersistence(ctx context.Context, cfg *config.Config, logger *slog.Logger) error {
	if isProductionConfig(cfg) && !dsnEnforcesTLS(cfg.DatabaseURL) {
		logger.Warn(
			"DATABASE_URL does not enforce TLS but production mode is enabled "+
				"— production must use sslmode=require/verify-ca/verify-full",
			"event", "production_dsn_tls_warning",
		)
	}

	database, err := db.ConnectWithConfig(ctx, cfg.DatabaseURL, db.PoolConfig{
		MaxConns:          cfg.DBMaxConns,
		MinConns:          cfg.DBMinConns,
		MaxConnLifetime:   time.Duration(cfg.DBMaxConnLifetimeMin) * time.Minute,
		MaxConnIdleTime:   time.Duration(cfg.DBMaxConnIdleMin) * time.Minute,
		HealthCheckPeriod: time.Duration(cfg.DBHealthCheckPeriodSec) * time.Second,
	})
	if err != nil {
		return err
	}
	// Apply pending migrations under the session advisory lock before
	// anything else touches the schema (guide ch.02). Concurrent replicas
	// serialize on the lock; a failed migration is terminal for the process.
	result, migrateErr := database.Migrate(ctx, logger)
	if migrateErr != nil {
		database.Close()
		return fmt.Errorf("database migration failed: %w", migrateErr)
	}
	logger.Info("database schema ready", "event", "database_schema_ready",
		"applied", result.Applied, "version", result.Version)
	if shErr := database.SchemaHealth(ctx); shErr != nil {
		database.Close()
		return fmt.Errorf("schema health check failed: %w", shErr)
	}

	queries := sqlc.New(database.Pool())
	jwtManager, jwtErr := auth.NewJWTManagerWithConfig(auth.JWTConfig{
		SecretKey: cfg.SecretKey, AccessLifetimeMinutes: cfg.SessionTimeoutMinutes,
		Issuer: cfg.JWTIssuer, Audience: cfg.JWTAudience,
	})
	if jwtErr != nil {
		database.Close()
		return jwtErr
	}

	cursorCodec, cursorErr := paging.NewCursorCodec(cfg.SecretKey)
	if cursorErr != nil {
		database.Close()
		return cursorErr
	}
	c.cursorCodec = cursorCodec

	// age keyring (pkg/crypto) + SSO manager. With NO key configured the
	// server still comes up for dev/local stacks (production is refused by
	// validateProductionSecurityConfig); handlers that need the keyring fail
	// their secret-bearing requests. A key that IS configured but does not
	// parse is a startup error: silently running without it would turn every
	// credential read into an outage that looks like an upstream failure.
	var (
		encryptor  *crypto.Encryptor
		ssoManager *auth.SSOManager
	)
	enc, encErr := cfg.LoadEncryptor()
	switch {
	case errors.Is(encErr, crypto.ErrNoKey):
		logger.Warn("ASTRONOMER_ENCRYPTION_KEY / ASTRONOMER_ENCRYPTION_KEY_FILE is not set; sealed columns cannot be read or written and SSO is disabled")
	case encErr != nil:
		database.Close()
		return fmt.Errorf("load encryption keyring: %w", encErr)
	default:
		encryptor = enc
		logger.Info("encryption keyring loaded", "current_key_id", enc.CurrentKeyID(), "keys", enc.KeyCount())
		callbackBase := resolveCallbackBaseURL(ctx, cfg, queries)
		ssoManager = auth.NewSSOManager(encryptor, jwtManager, callbackBase)
		if loadErr := ssoManager.LoadFromDatabase(ctx, queries); loadErr != nil {
			logger.Warn("failed to load sso providers", "error", loadErr)
		}
	}
	if err := validateProductionSecurityConfig(cfg, encryptor); err != nil {
		database.Close()
		return err
	}
	reportInsecureDevKeys(cfg, logger)

	// Migration 045 — Dex consolidation.
	//
	// When the chart deploys the in-cluster Dex (dex.enabled=true), the
	// configmap template sets DEX_BUNDLED_ENABLED + DEX_BUNDLED_* describing
	// the templated objects. The bootstrap below auto-wires the singleton
	// dex_settings row so the operator's first connector + Apply works
	// without a manual settings step. No-op when dex.enabled=false (legacy
	// operator-managed Dex flow stays in effect).
	if _, err := SeedBundledDexSettings(ctx, queries, logger, cfg); err != nil {
		logger.Warn("dex bootstrap: seed failed", "error", err)
	}
	// Surface drift between legacy sso_configurations and the new
	// dex_connectors path. Best-effort: log + continue on error.
	if err := WarnIfLegacySSORowsActive(ctx, queries, logger); err != nil {
		logger.Debug("dex bootstrap: legacy SSO check failed", "error", err)
	}
	c.database = database
	c.queries = queries
	c.jwtManager = jwtManager
	c.encryptor = encryptor
	c.ssoManager = ssoManager
	return nil
}
