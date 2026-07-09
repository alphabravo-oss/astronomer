package main

import (
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/agent"
)

func TestConnect2RequiresActiveDurableIdentity(t *testing.T) {
	if err := validateConnect2CredentialSource(agent.CredentialSourceIdentity); err != nil {
		t.Fatalf("active identity rejected: %v", err)
	}
	for _, source := range []string{"bootstrap_secret", "legacy_durable_secret", "environment", ""} {
		if err := validateConnect2CredentialSource(source); err == nil {
			t.Fatalf("connect2 accepted credential source %q", source)
		}
	}
}
