package server

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

type ssoWiringInvalidator struct{}

func (ssoWiringInvalidator) Invalidate(string) {}

func TestSSOCallbackProductionTransactionWiring(t *testing.T) {
	var _ handler.SSOCallbackTx = (*sqlc.Queries)(nil)
	source, err := os.ReadFile("app_identity_cluster.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "ssoHandler.SetRunTx(sqlcMutationTxRunner[handler.SSOCallbackTx](database))") {
		t.Fatal("production SSO callback is missing its transaction-bound sqlc adapter")
	}
}

func TestSSOCallbackStartupRequiresSecurityDependencies(t *testing.T) {
	key, err := auth.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	enc, err := auth.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	jwt := auth.MustNewJWTManager("sso-wiring-test-secret", 60)
	for _, missing := range []string{"transaction", "encryption", "cache", "jwt", "manager", ""} {
		t.Run(missing, func(t *testing.T) {
			jwt.SetRevocationChecker(newSharedRevocations())
			h := handler.NewSSOHandler(auth.NewSSOManager(enc, jwt, "/"), jwt, "/")
			h.SetRunTx(func(context.Context, func(handler.SSOCallbackTx) error) error { return nil })
			h.SetEncryptor(enc)
			h.SetRBACCacheInvalidator(ssoWiringInvalidator{})
			switch missing {
			case "transaction":
				h.SetRunTx(nil)
			case "encryption":
				h.SetEncryptor(nil)
			case "cache":
				h.SetRBACCacheInvalidator(nil)
			case "jwt":
				h = handler.NewSSOHandler(auth.NewSSOManager(enc, jwt, "/"), nil, "/")
				h.SetRunTx(func(context.Context, func(handler.SSOCallbackTx) error) error { return nil })
				h.SetEncryptor(enc)
				h.SetRBACCacheInvalidator(ssoWiringInvalidator{})
			case "manager":
				h = handler.NewSSOHandler(nil, jwt, "/")
				h.SetRunTx(func(context.Context, func(handler.SSOCallbackTx) error) error { return nil })
				h.SetEncryptor(enc)
				h.SetRBACCacheInvalidator(ssoWiringInvalidator{})
			}
			queries := sqlc.New(nil)
			deps := RouterDependencies{CoreAuth: CoreAuthDependencies{JWT: jwt, AuthQueries: productionSecurityAuthQuerier{}, RBACEngine: rbac.NewEngine(), RBACQueries: routeSecurityRBACQuerier{}, Encryptor: enc, SettingsCache: handler.NewSettingsCache(nil, time.Minute), Queries: queries, AuditWriter: queries, SSO: h}}
			err := validateProductionSecurityWiring(&config.Config{Env: "development"}, deps)
			if missing == "" && err != nil {
				t.Fatal(err)
			}
			if missing != "" && (err == nil || !strings.Contains(err.Error(), "SSO callback")) {
				t.Fatalf("missing %s: %v", missing, err)
			}
		})
	}
}
