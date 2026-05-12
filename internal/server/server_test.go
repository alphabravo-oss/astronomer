package server

import (
	"context"
	"net/http"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/config"
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
