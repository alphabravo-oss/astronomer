package httpclient

import (
	"net/http"
	"testing"
	"time"
)

func TestNewUsesDefaultExternalTimeout(t *testing.T) {
	client := New(0)
	if client.Timeout != DefaultExternalTimeout {
		t.Fatalf("Timeout = %s, want %s", client.Timeout, DefaultExternalTimeout)
	}
}

func TestNewUsesProvidedTimeout(t *testing.T) {
	client := New(5 * time.Second)
	if client.Timeout != 5*time.Second {
		t.Fatalf("Timeout = %s, want 5s", client.Timeout)
	}
}

func TestWithDefaultPreservesProvidedClient(t *testing.T) {
	provided := &http.Client{Timeout: time.Second}
	got := WithDefault(provided, 5*time.Second)
	if got != provided {
		t.Fatal("WithDefault should preserve provided clients")
	}
}
