//go:build integration

package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/testdb"
	"github.com/alphabravocompany/astronomer-go/pkg/crypto"
)

// productionRouterHarness is the real production composition (every phase of
// NewApp up to the router, no background runtime) against a fresh migrated
// Postgres and Redis. Optional route families are switched on so the router
// registers every route a deployment can expose.
type productionRouterHarness struct {
	router  chi.Router
	deps    RouterDependencies
	pool    *pgxpool.Pool
	queries *sqlc.Queries
	jwt     *auth.JWTManager
}

func newProductionRouterHarness(t *testing.T) *productionRouterHarness {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.DatabaseURL = testdb.DSN(t)
	cfg.RedisURL = testdb.Redis(t)
	cfg.SecretKey = "integration-router-secret-0123456789abcdef0123456789"
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	cfg.EncryptionKey = key
	cfg.ControlPlaneSnapshotsEnabled = true
	cfg.KubectlShellEnabled = true
	cfg.CRDEnabled = true
	cfg.NativeRBACEnabled = true
	cfg.ManagementBackupEnabled = true
	cfg.DexBundledEnabled = true
	logger := zerolog.Nop()
	ctx := context.Background()
	c := &productionComposition{runtime: newRuntimeSupervisor(logger)}
	t.Cleanup(c.closeUntransferredDatabaseAuthority)
	for _, phase := range []func(context.Context, *config.Config, zerolog.Logger) error{
		c.initializePersistence, c.initializeCoreHandlers, c.initializeTenantHandlers, c.initializeClusterHandlers,
		c.initializeIdentityAndClusterHandlers, c.initializeIntegrations, c.initializeCharlie, c.initializeDelivery,
	} {
		if err := phase(ctx, cfg, logger); err != nil {
			t.Fatal(err)
		}
	}
	routed, err := c.composeRouter(ctx, cfg, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.database.Close)
	router, ok := routed.router.(chi.Router)
	if !ok {
		t.Fatalf("production router is %T, not chi.Router", routed.router)
	}
	return &productionRouterHarness{router: router, deps: routed.deps, pool: c.database.Pool(), queries: c.queries, jwt: c.jwtManager}
}

// user creates an active user, optionally a superuser, optionally bound to a
// new global role with the given rules, and returns its id and a bearer token.
func (h *productionRouterHarness) user(t *testing.T, name string, superuser bool, rules []map[string]any) (uuid.UUID, string) {
	t.Helper()
	ctx := context.Background()
	user, err := h.queries.CreateUser(ctx, sqlc.CreateUserParams{
		Email: name + "@integration.test", Username: name, Password: "!", IsActive: true, IsSuperuser: superuser,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rules != nil {
		raw, err := json.Marshal(rules)
		if err != nil {
			t.Fatal(err)
		}
		role, err := h.queries.CreateGlobalRole(ctx, sqlc.CreateGlobalRoleParams{
			Name: name + "-role", DisplayName: name, Permissions: json.RawMessage(`{}`), Rules: raw,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.queries.CreateGlobalRoleBinding(ctx, sqlc.CreateGlobalRoleBindingParams{
			UserID: pgtype.UUID{Bytes: user.ID, Valid: true}, RoleID: role.ID,
		}); err != nil {
			t.Fatal(err)
		}
	}
	token, err := h.jwt.GenerateAccessToken(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	return user.ID, token
}

func (h *productionRouterHarness) do(t *testing.T, req *http.Request, token string) *httptest.ResponseRecorder {
	t.Helper()
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	return rec
}
