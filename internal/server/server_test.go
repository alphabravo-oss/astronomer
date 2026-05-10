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
