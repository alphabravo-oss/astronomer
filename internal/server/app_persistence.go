package server

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/db"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
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
	// T8.1 — fail fast on a corrupt schema_migrations row set
	// (multi-row drift or dirty=true). The .247 incident on 2026-05-13
	// silently shipped {84 dirty=t, 86 clean} for hours; refusing to
	// start surfaces it in the first CrashLoop instead of letting the
	// pod serve traffic against an indeterminate schema.
	if shErr := database.SchemaHealth(ctx); shErr != nil {
		database.Close()
		return fmt.Errorf("schema health check failed: %w", shErr)
	}

	queries := sqlc.New(database.Pool())
	jwtManager, jwtErr := auth.NewJWTManager(cfg.SecretKey, cfg.SessionTimeoutMinutes)
	if jwtErr != nil {
		database.Close()
		return jwtErr
	}

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
			logger.Warn("failed to initialise encryptor", "error", encErr)
		} else {
			encryptor = enc
			callbackBase := resolveCallbackBaseURL(ctx, cfg, queries)
			ssoManager = auth.NewSSOManager(encryptor, jwtManager, callbackBase)
			if loadErr := ssoManager.LoadFromDatabase(ctx, queries); loadErr != nil {
				logger.Warn("failed to load sso providers", "error", loadErr)
			}
		}
	} else {
		logger.Warn("ASTRONOMER_ENCRYPTION_KEY is not set; encrypted columns will be returned as ciphertext and SSO is disabled")
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
	if _, err := SeedBundledDexSettings(ctx, queries, logger); err != nil {
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
