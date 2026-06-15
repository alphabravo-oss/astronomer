package server

import (
	"context"
	"log/slog"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
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
	if err := validateProductionSecurityWiring(&config.Config{Env: "production"}, RouterDependencies{
		JWT:         auth.NewJWTManager("production-jwt-signing-key", 60),
		AuthQueries: productionSecurityAuthQuerier{},
		RBACEngine:  rbac.NewEngine(),
		RBACQueries: routeSecurityRBACQuerier{bindings: routeSecurityAdminBindings()},
		Encryptor:   enc,
	}); err != nil {
		t.Fatalf("valid production wiring rejected: %v", err)
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
