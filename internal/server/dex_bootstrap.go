package server

// Migration 045 — auto-wire dex_settings when the chart ships Dex in-band.
//
// When the operator runs the chart with `dex.enabled=true` the runtime config
// sets DEX_BUNDLED_ENABLED=true plus the namespace / release-name /
// runtime-secret-name / issuer-url that match the templated objects. The server's
// boot path then seeds the singleton dex_settings row to point at those
// objects so the operator's first connector + Apply works without any manual
// settings step.
//
// Idempotent: if a dex_settings row already exists (operator has configured
// it manually, or this isn't the first boot) we leave it alone. Operators who
// later turn dex.enabled=false won't have their row clobbered.

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// dexBootstrapEnv is the chart-set switch that controls whether the bootstrap
// runs. When unset / "false" the legacy operator-managed Dex flow stays in
// effect — the DexHandler still serves /settings + /apply, but no auto-seed
// happens.
const (
	dexBootstrapEnv                  = "DEX_BUNDLED_ENABLED"
	dexBootstrapNamespaceEnv         = "DEX_BUNDLED_NAMESPACE"
	dexBootstrapReleaseNameEnv       = "DEX_BUNDLED_RELEASE_NAME"
	dexBootstrapDeploymentNameEnv    = "DEX_BUNDLED_DEPLOYMENT_NAME"
	dexBootstrapServiceNameEnv       = "DEX_BUNDLED_SERVICE_NAME"
	dexBootstrapRuntimeSecretNameEnv = "DEX_BUNDLED_RUNTIME_SECRET_NAME"
	dexBootstrapMigrationPhaseEnv    = "DEX_BUNDLED_MIGRATION_PHASE"
	dexBootstrapConfigmapNameEnv     = "DEX_BUNDLED_CONFIGMAP_NAME" // deprecated alias
	dexBootstrapIssuerURLEnv         = "DEX_BUNDLED_ISSUER_URL"
)

// dexBootstrapSingletonID is the same UUID DexHandler uses for the singleton
// settings row (internal/handler/dex_config.go). Centralised here so the
// bootstrap and the handler can't drift.
var dexBootstrapSingletonID = uuid.MustParse("00000000-0000-0000-0000-000000000001")

// dexBootstrapQuerier is the slice of DB calls the bootstrap needs. The
// production *sqlc.Queries satisfies this naturally; tests inject a fake.
type dexBootstrapQuerier interface {
	GetDexSettings(ctx context.Context, id uuid.UUID) (sqlc.DexSetting, error)
	StageDexSettingsAndDisableSSO(ctx context.Context, arg sqlc.StageDexSettingsAndDisableSSOParams) (int64, error)
}

// dexBootstrapEnvLookup is os.LookupEnv-shaped so tests can substitute a
// deterministic env source instead of mutating process state.
type dexBootstrapEnvLookup func(string) (string, bool)

// SeedBundledDexSettings is the boot-time entry point that auto-wires the
// dex_settings row when the chart's in-cluster Dex is enabled. Returns:
//
//   - (true,  nil): a new dex_settings row was inserted.
//   - (false, nil): no-op (bundled disabled, settings already exist, or the
//     chart didn't supply the issuer URL).
//   - (false, err): an unexpected DB error; the caller should log but boot
//     should continue — the connector wizard stays usable and the operator
//     can configure dex_settings via the UI as a fallback.
func SeedBundledDexSettings(ctx context.Context, queries dexBootstrapQuerier, logger *slog.Logger) (bool, error) {
	return seedBundledDexSettings(ctx, queries, logger, os.LookupEnv)
}

func seedBundledDexSettings(ctx context.Context, queries dexBootstrapQuerier, logger *slog.Logger, env dexBootstrapEnvLookup) (bool, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if queries == nil {
		return false, nil
	}
	if !isDexBundledEnabled(env) {
		logger.Debug("dex bootstrap: DEX_BUNDLED_ENABLED not set; skipping seed")
		return false, nil
	}

	issuer := strings.TrimRight(strings.TrimSpace(envOr(env, dexBootstrapIssuerURLEnv, "")), "/")
	if issuer == "" {
		// The chart should always supply this when dex.enabled=true (see
		// configmap.yaml + _helpers.tpl astronomer.dex.issuerURL). When it
		// doesn't, refuse to seed — a row with an empty issuer fails the
		// /apply preflight inside DexHandler anyway.
		logger.Warn("dex bootstrap: DEX_BUNDLED_ENABLED=true but DEX_BUNDLED_ISSUER_URL is empty; skipping seed",
			"event", "dex_bootstrap_missing_issuer",
		)
		return false, nil
	}

	// Idempotency: if a settings row exists already, leave it. This is the
	// "operator manually configured" path AND the "second boot" path; both
	// should be no-ops.
	existing, err := queries.GetDexSettings(ctx, dexBootstrapSingletonID)
	if err == nil && existing.ID == dexBootstrapSingletonID {
		desiredNamespace := envOr(env, dexBootstrapNamespaceEnv, "astronomer")
		desiredChartRelease := envOr(env, dexBootstrapReleaseNameEnv, "astronomer")
		desiredDeployment := envOr(env, dexBootstrapDeploymentNameEnv, desiredChartRelease+"-dex")
		desiredService := envOr(env, dexBootstrapServiceNameEnv, desiredDeployment)
		desiredPhase := envOr(env, dexBootstrapMigrationPhaseEnv, "fresh")
		desiredRuntimeName := envOr(env, dexBootstrapRuntimeSecretNameEnv,
			envOr(env, dexBootstrapConfigmapNameEnv, "astronomer-dex-runtime"))
		// Bundled identity is chart-owned and immutable. Reconcile every identity
		// field while preserving operator-owned issuer, cluster, clients, and
		// extension settings.
		if existing.Namespace != desiredNamespace || existing.ChartReleaseName != desiredChartRelease || existing.DeploymentName != desiredDeployment || existing.ServiceName != desiredService || existing.RuntimeSecretName != desiredRuntimeName || existing.RuntimePhase != desiredPhase {
			_, updateErr := queries.StageDexSettingsAndDisableSSO(ctx, sqlc.StageDexSettingsAndDisableSSOParams{
				ID: existing.ID, IssuerUrl: existing.IssuerUrl, ClusterID: existing.ClusterID,
				Namespace: desiredNamespace, ReleaseName: desiredDeployment,
				ConfigmapName: desiredRuntimeName, RuntimeSecretName: desiredRuntimeName,
				PublicClients: existing.PublicClients, PublicClientsEncrypted: existing.PublicClientsEncrypted,
				Expiry: existing.Expiry, Extra: existing.Extra,
				ChartReleaseName: desiredChartRelease, DeploymentName: desiredDeployment, ServiceName: desiredService,
				RuntimePhase: desiredPhase,
			})
			return updateErr == nil, updateErr
		}
		logger.Debug("dex bootstrap: settings already exist; skipping seed",
			"existing_issuer", existing.IssuerUrl,
		)
		return false, nil
	}
	// Any error other than "no rows" is unexpected; surface it.
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		// Some sqlc-generated query funcs return wrapped errors that don't
		// satisfy errors.Is for pgx.ErrNoRows; fall back to string matching
		// for the common "not found" shapes we see in tests + production.
		msg := strings.ToLower(err.Error())
		if !strings.Contains(msg, "no rows") && !strings.Contains(msg, "not found") {
			return false, err
		}
	}

	namespace := envOr(env, dexBootstrapNamespaceEnv, "astronomer")
	chartReleaseName := envOr(env, dexBootstrapReleaseNameEnv, "astronomer")
	deploymentName := envOr(env, dexBootstrapDeploymentNameEnv, chartReleaseName+"-dex")
	serviceName := envOr(env, dexBootstrapServiceNameEnv, deploymentName)
	runtimePhase := envOr(env, dexBootstrapMigrationPhaseEnv, "fresh")
	runtimeSecretName := envOr(env, dexBootstrapRuntimeSecretNameEnv,
		envOr(env, dexBootstrapConfigmapNameEnv, "astronomer-dex-runtime"))

	_, err = queries.StageDexSettingsAndDisableSSO(ctx, sqlc.StageDexSettingsAndDisableSSOParams{
		ID:                     dexBootstrapSingletonID,
		IssuerUrl:              issuer,
		ClusterID:              pgtype.UUID{}, // unset — local-cluster wiring is the operator's call via the UI
		Namespace:              namespace,
		ReleaseName:            deploymentName,
		ConfigmapName:          runtimeSecretName,
		RuntimeSecretName:      runtimeSecretName,
		PublicClientsEncrypted: "",
		PublicClients:          []byte("[]"),
		Expiry:                 []byte("{}"),
		Extra:                  []byte("{}"),
		ChartReleaseName:       chartReleaseName,
		DeploymentName:         deploymentName,
		ServiceName:            serviceName,
		RuntimePhase:           runtimePhase,
	})
	if err != nil {
		return false, err
	}
	logger.Info("dex bootstrap: seeded dex_settings for bundled Dex",
		"event", "dex_settings_seeded",
		"issuer_url", issuer,
		"namespace", namespace,
		"chart_release_name", chartReleaseName,
		"deployment_name", deploymentName,
		"service_name", serviceName,
		"runtime_phase", runtimePhase,
		"runtime_secret_name", runtimeSecretName,
	)
	return true, nil
}

// isDexBundledEnabled reads DEX_BUNDLED_ENABLED and treats "1" / "true" /
// "yes" (case-insensitive) as truthy. Everything else is false.
func isDexBundledEnabled(env dexBootstrapEnvLookup) bool {
	v, ok := env(dexBootstrapEnv)
	if !ok {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func envOr(env dexBootstrapEnvLookup, key, fallback string) string {
	if v, ok := env(key); ok {
		v = strings.TrimSpace(v)
		if v != "" {
			return v
		}
	}
	return fallback
}

// legacySSORowsQuerier is the slice CountActiveUnmigratedSSORows lives behind.
// Both *sqlc.Queries and test fakes satisfy this.
type legacySSORowsQuerier interface {
	CountActiveUnmigratedSSORows(ctx context.Context) (int64, error)
}

// WarnIfLegacySSORowsActive emits a warn-level log line when there are
// enabled sso_configurations rows that haven't been stamped as migrated to
// dex_connectors (migration 045). Operators see drift between the legacy
// per-server OAuth path and the Dex-managed path so they can clean up
// before the cleanup migration drops the table.
//
// Boot-path semantics: best-effort. Errors are returned to the caller for
// logging but boot continues — this is a posture warning, not a hard gate.
func WarnIfLegacySSORowsActive(ctx context.Context, queries legacySSORowsQuerier, logger *slog.Logger) error {
	if queries == nil {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	n, err := queries.CountActiveUnmigratedSSORows(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		logger.Warn(
			"legacy sso_configurations rows are still active; migrate to dex_connectors via the auth settings UI before the next release removes the old path",
			"event", "legacy_sso_rows_active",
			"count", n,
		)
	}
	return nil
}
