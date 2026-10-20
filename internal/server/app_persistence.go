package server

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/db"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/alphabravocompany/astronomer-go/pkg/logging"
)

func (c *productionComposition) initializePersistence(ctx context.Context, cfg *config.Config, logger zerolog.Logger) error {
	if isProductionConfig(cfg) && !dsnEnforcesTLS(cfg.DatabaseURL) {
		logger.Warn().Str("event", "production_dsn_tls_warning").Msg("DATABASE_URL does not enforce TLS but production mode is enabled " +
			"— production must use sslmode=require/verify-ca/verify-full")
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
	result, migrateErr := database.Migrate(ctx, logging.Slog(logger))
	if migrateErr != nil {
		database.Close()
		return fmt.Errorf("database migration failed: %w", migrateErr)
	}
	logger.Info().Str("event", "database_schema_ready").Int("applied", result.Applied).Int64("version", result.Version).Msg("database schema ready")
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

	// Best-effort Fernet encryptor + SSO manager. Both are optional: if the
	// encryption key is missing or invalid we still come up so dev/local
	// stacks without secrets don't break — a warning is logged. Handlers
	// that need decryption skip the work when the encryptor is nil.
	var (
		encryptor  *auth.Encryptor
		ssoManager *auth.SSOManager
	)
	if cfg.EncryptionKey != "" {
		enc, encErr := auth.NewEncryptor(cfg.EncryptionKey)
		if encErr != nil {
			logger.Warn().Err(encErr).Msg("failed to initialise encryptor")
		} else {
			encryptor = enc
			callbackBase := resolveCallbackBaseURL(ctx, cfg, queries)
			ssoManager = auth.NewSSOManager(encryptor, jwtManager, callbackBase)
			if loadErr := ssoManager.LoadFromDatabase(ctx, queries); loadErr != nil {
				logger.Warn().Err(loadErr).Msg("failed to load sso providers")
			}
		}
	} else {
		logger.Warn().Msg("ASTRONOMER_ENCRYPTION_KEY is not set; encrypted columns will be returned as ciphertext and SSO is disabled")
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
		logger.Warn().Err(err).Msg("dex bootstrap: seed failed")
	}
	// Surface drift between legacy sso_configurations and the new
	// dex_connectors path. Best-effort: log + continue on error.
	if err := WarnIfLegacySSORowsActive(ctx, queries, logger); err != nil {
		logger.Debug().Err(err).Msg("dex bootstrap: legacy SSO check failed")
	}
	c.database = database
	c.queries = queries
	c.jwtManager = jwtManager
	c.encryptor = encryptor
	c.ssoManager = ssoManager
	return nil
}
