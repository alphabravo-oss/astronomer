package server

import (
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

func testServer(t *testing.T) http.Handler {
	t.Helper()
	return NewRouter(&config.Config{}, RouterDependencies{})
}

func TestResolveCallbackBaseURLWithoutPlatformConfig(t *testing.T) {
	got := resolveCallbackBaseURL(context.Background(), nil, nil)
	want := "http://localhost:8000/api/v1"
	if got != want {
		t.Fatalf("resolveCallbackBaseURL() = %q, want %q", got, want)
	}
}

func TestStartCRDControllerFailsClosedInProduction(t *testing.T) {
	t.Setenv("CRD_ENABLED", "true")
	t.Setenv("KUBECONFIG", t.TempDir()+"/missing-kubeconfig")
	t.Setenv("HOME", t.TempDir())

	err := startCRDController(context.Background(), slog.Default(), &config.Config{Env: "production"}, nil)
	if err == nil {
		t.Fatal("expected production CRD controller bootstrap to fail without Kubernetes config")
	}

	err = startCRDController(context.Background(), slog.Default(), &config.Config{Env: "development"}, nil)
	if err != nil {
		t.Fatalf("development CRD controller bootstrap error = %v, want nil", err)
	}
}

func TestCRDControllerNamespaceEnvDefaultsAndOverrides(t *testing.T) {
	if got := crdWatchNamespace(); got != "astronomer-mgmt" {
		t.Fatalf("default CRD watch namespace = %q", got)
	}
	if got := crdArgoNamespace(); got != "argocd" {
		t.Fatalf("default CRD Argo namespace = %q", got)
	}
	t.Setenv("CRD_WATCH_NAMESPACE", "custom-mgmt")
	t.Setenv("CRD_ARGO_NAMESPACE", "custom-argocd")
	if got := crdWatchNamespace(); got != "custom-mgmt" {
		t.Fatalf("override CRD watch namespace = %q", got)
	}
	if got := crdArgoNamespace(); got != "custom-argocd" {
		t.Fatalf("override CRD Argo namespace = %q", got)
	}
}

// dsnEnforcesTLS gates the production warning when DATABASE_URL doesn't
// require TLS. The values an operator could mis-set into a Helm install
// must all map to the right verdict.
func TestDSNEnforcesTLS(t *testing.T) {
	cases := []struct {
		name string
		dsn  string
		want bool
	}{
		{"require", "postgres://u:p@h:5432/d?sslmode=require", true},
		{"verify-ca", "postgres://u:p@h:5432/d?sslmode=verify-ca", true},
		{"verify-full", "postgres://u:p@h:5432/d?sslmode=verify-full", true},
		{"disable explicit", "postgres://u:p@h:5432/d?sslmode=disable", false},
		{"allow", "postgres://u:p@h:5432/d?sslmode=allow", false},
		{"prefer", "postgres://u:p@h:5432/d?sslmode=prefer", false},
		{"missing", "postgres://u:p@h:5432/d", false},
		{"case-insensitive REQUIRE", "postgres://u:p@h:5432/d?SSLMODE=REQUIRE", true},
		{"verify-full inside multi-param", "postgres://u:p@h:5432/d?application_name=astronomer&sslmode=verify-full&pool_max_conns=20", true},
		{"empty", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := dsnEnforcesTLS(c.dsn); got != c.want {
				t.Errorf("dsnEnforcesTLS(%q) = %v, want %v", c.dsn, got, c.want)
			}
		})
	}
}

func TestValidateProductionSecurityConfig(t *testing.T) {
	validKey := "mjc2rBj19lSbsCp4LzOVgAuWBxGJIGsQNc6oZi0iDTQ="
	enc, err := auth.NewEncryptor(validKey)
	if err != nil {
		t.Fatalf("NewEncryptor(valid): %v", err)
	}
	if err := validateProductionSecurityConfig(&config.Config{
		Env:               "production",
		DatabaseURL:       "postgres://astronomer:astronomer@db:5432/astronomer?sslmode=require",
		SecretKey:         "production-jwt-signing-key",
		EncryptionKey:     validKey,
		ServerURL:         "https://astronomer.example.com",
		DexBundledEnabled: true,
	}, enc); err != nil {
		t.Fatalf("valid production config rejected: %v", err)
	}

	if err := validateProductionSecurityConfig(&config.Config{
		Env:                   "production",
		DatabaseURL:           "postgres://astronomer:astronomer@db:5432/astronomer?sslmode=require",
		SecretKey:             "production-jwt-signing-key",
		EncryptionKey:         validKey,
		ServerURL:             "https://astronomer.example.com",
		AuthLocalPasswordOnly: true,
	}, enc); err != nil {
		t.Fatalf("local-password-only acknowledgement rejected: %v", err)
	}

	err = validateProductionSecurityConfig(&config.Config{
		Env:           "production",
		DatabaseURL:   "postgres://astronomer:astronomer@db:5432/astronomer?sslmode=require",
		SecretKey:     "production-jwt-signing-key",
		EncryptionKey: validKey,
		ServerURL:     "https://astronomer.example.com",
	}, enc)
	if err == nil {
		t.Fatal("expected production config without Dex or local-password acknowledgement to be rejected")
	}

	err = validateProductionSecurityConfig(&config.Config{
		Env:           "production",
		SecretKey:     devSecretKey,
		EncryptionKey: devEncryptionKey,
	}, nil)
	if err == nil {
		t.Fatal("expected known development secrets to be rejected")
	}

	for _, tc := range []struct {
		name      string
		serverURL string
	}{
		{name: "missing", serverURL: ""},
		{name: "http", serverURL: "http://astronomer.example.com"},
		{name: "relative", serverURL: "/astronomer"},
	} {
		t.Run("server_url_"+tc.name, func(t *testing.T) {
			err := validateProductionSecurityConfig(&config.Config{
				Env:               "production",
				DatabaseURL:       "postgres://astronomer:astronomer@db:5432/astronomer?sslmode=require",
				SecretKey:         "production-jwt-signing-key",
				EncryptionKey:     validKey,
				ServerURL:         tc.serverURL,
				DexBundledEnabled: true,
			}, enc)
			if err == nil {
				t.Fatalf("expected server_url %q to be rejected", tc.serverURL)
			}
		})
	}
}

// TestReportInsecureDevKeysOutsideProduction is the dev-keys-default-and-silent
// regression: validateProductionSecurityConfig is a no-op outside production,
// which is exactly where the published chart keys used to hide. Booting on them
// must be loud in development too.
func TestReportInsecureDevKeysOutsideProduction(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	reportInsecureDevKeys(&config.Config{
		Env:           "development",
		SecretKey:     devSecretKey,
		EncryptionKey: devEncryptionKey,
	}, logger)
	logged := buf.String()
	for _, want := range []string{"level=ERROR", "secret_key", "encryption_key"} {
		if !strings.Contains(logged, want) {
			t.Fatalf("dev-sentinel boot log missing %q:\n%s", want, logged)
		}
	}

	buf.Reset()
	reportInsecureDevKeys(&config.Config{
		Env:           "development",
		SecretKey:     "a-real-unique-secret",
		EncryptionKey: "mjc2rBj19lSbsCp4LzOVgAuWBxGJIGsQNc6oZi0iDTQ=",
	}, logger)
	if buf.Len() != 0 {
		t.Fatalf("real keys logged %q, want silence", buf.String())
	}
}

func TestValidateProductionSecurityWiring(t *testing.T) {
	if err := validateProductionSecurityWiring(&config.Config{Env: "development"}, RouterDependencies{}); err != nil {
		t.Fatalf("development wiring should not fail closed: %v", err)
	}

	err := validateProductionSecurityWiring(&config.Config{Env: "production"}, RouterDependencies{})
	if err == nil {
		t.Fatal("expected missing production security wiring to fail")
	}

	enc, err := auth.NewEncryptor("mjc2rBj19lSbsCp4LzOVgAuWBxGJIGsQNc6oZi0iDTQ=")
	if err != nil {
		t.Fatalf("NewEncryptor(valid): %v", err)
	}
	valid := RouterDependencies{
		JWT:         auth.MustNewJWTManager("production-jwt-signing-key", 60),
		AuthQueries: productionSecurityAuthQuerier{},
		RBACEngine:  rbac.NewEngine(),
		RBACQueries: routeSecurityRBACQuerier{bindings: routeSecurityAdminBindings()},
		Encryptor:   enc,
	}
	if err := validateProductionSecurityWiring(&config.Config{Env: "production"}, valid); err != nil {
		t.Fatalf("valid production wiring rejected: %v", err)
	}

	// A project handler that was never given an RBAC cache invalidator makes
	// AddNamespace/RemoveNamespace's cache flush a silent no-op, so a revoked
	// namespace keeps authorizing reads. That must fail the boot, not ship.
	unwired := valid
	unwired.Projects = handler.NewProjectHandler(nil)
	err = validateProductionSecurityWiring(&config.Config{Env: "production"}, unwired)
	if err == nil || !strings.Contains(err.Error(), "RBAC cache invalidator") {
		t.Fatalf("unwired project RBAC invalidator accepted: %v", err)
	}

	// A TYPED NIL must be rejected too. NewSQLCRBACQuerierWithCache returns a
	// nil *SQLCRBACQuerier when queries==nil; boxed into the interface it is
	// non-nil and satisfies RBACCacheInvalidator, but InvalidateAll() returns at
	// its nil-receiver guard — the exact permanent no-op this check exists to
	// catch. Same for a querier built without a cache.
	for name, inv := range map[string]appmiddleware.RBACQuerier{
		"typed nil querier": appmiddleware.NewSQLCRBACQuerierWithCache(nil, appmiddleware.NewRBACCache()),
		"cacheless querier": appmiddleware.NewSQLCRBACQuerierWithCache(stubProjectBindingsQuerier{}, nil),
	} {
		t.Run(name+" rejected", func(t *testing.T) {
			deps := valid
			p := handler.NewProjectHandler(nil)
			p.SetRBACInvalidator(inv)
			deps.Projects = p
			err := validateProductionSecurityWiring(&config.Config{Env: "production"}, deps)
			if err == nil || !strings.Contains(err.Error(), "RBAC cache invalidator") {
				t.Fatalf("%s accepted as a working invalidator: %v", name, err)
			}
		})
	}

	// The positive arm must use an invalidator that can actually flush: a real
	// querier over a real cache. Prove that behaviourally before asserting the
	// boot check accepts it, so the check can never be satisfied by something
	// that flushes nothing.
	cache := appmiddleware.NewRBACCache()
	realQuerier := appmiddleware.NewSQLCRBACQuerierWithCache(stubProjectBindingsQuerier{}, cache)
	cache.Put("11111111-1111-1111-1111-111111111111", nil)
	realQuerier.InvalidateAll()
	if _, ok := cache.Get("11111111-1111-1111-1111-111111111111"); ok {
		t.Fatal("probe invalidator did not flush the cache; the positive arm below would be vacuous")
	}

	wired := valid
	projects := handler.NewProjectHandler(nil)
	projects.SetRBACInvalidator(realQuerier)
	wired.Projects = projects
	if err := validateProductionSecurityWiring(&config.Config{Env: "production"}, wired); err != nil {
		t.Fatalf("wired project RBAC invalidator rejected: %v", err)
	}
}

// stubProjectBindingsQuerier satisfies middleware's (unexported) binding-querier
// interface so tests can build a real *SQLCRBACQuerier without a Postgres.
type stubProjectBindingsQuerier struct{}

func (stubProjectBindingsQuerier) GetUserByID(_ context.Context, id uuid.UUID) (sqlc.User, error) {
	return sqlc.User{ID: id}, nil
}

func (stubProjectBindingsQuerier) ListUserBindingsWithRoles(context.Context, pgtype.UUID) ([]sqlc.ListUserBindingsWithRolesRow, error) {
	return nil, nil
}

func (stubProjectBindingsQuerier) ListProjectNamespaces(context.Context, uuid.UUID) ([]sqlc.ProjectNamespace, error) {
	return nil, nil
}

// TestServerWiresProjectRBACInvalidator is the regression guard for the wiring
// itself, not for the validator. NewApp needs a live Postgres, so this asserts
// against the source: NewApp must contain a projectHandler.SetRBACInvalidator
// call alongside the other Set* calls.
//
// Dropping that line is silent in every non-production environment (the boot
// validator short-circuits outside Env=production), and its effect —
// remove-namespace leaving the revoked access working — is invisible until a
// tenant notices. It has been dropped once already, which is why this exists.
func TestServerWiresProjectRBACInvalidator(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "server.go", nil, 0)
	if err != nil {
		t.Fatalf("parse server.go: %v", err)
	}
	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "SetRBACInvalidator" {
			return true
		}
		recv, ok := sel.X.(*ast.Ident)
		if !ok || recv.Name != "projectHandler" {
			return true
		}
		found = true
		return false
	})
	if !found {
		t.Fatal("internal/server/server.go no longer calls projectHandler.SetRBACInvalidator: " +
			"add/remove-namespace's RBAC cache flush is a silent no-op without it, so a namespace " +
			"revoked from a project keeps authorizing reads until the cache TTL expires")
	}
}

type productionSecurityAuthQuerier struct{}

func (productionSecurityAuthQuerier) GetTokenByHash(context.Context, string) (sqlc.ApiToken, error) {
	return sqlc.ApiToken{}, nil
}

func (productionSecurityAuthQuerier) GetUserByID(context.Context, uuid.UUID) (sqlc.User, error) {
	return sqlc.User{}, nil
}

func (productionSecurityAuthQuerier) UpdateAPITokenLastUsed(context.Context, uuid.UUID) error {
	return nil
}
