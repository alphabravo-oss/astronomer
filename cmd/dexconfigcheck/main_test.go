package main

import (
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/dexconfig"
)

const validRuntime = `issuer: https://dex.example.com
storage:
  type: kubernetes
  config:
    inCluster: true
web:
  http: 0.0.0.0:5556
oauth2:
  skipApprovalScreen: true
staticClients:
  - id: astronomer
    redirectURIs: [https://platform.example/api/v1/auth/callback/dex]
    secret: synthetic
connectors:
  - type: oidc
    id: primary
    name: Primary
    config:
      issuer: https://idp.example.com
      clientID: client
      clientSecret: synthetic
`

func TestValidateDexRuntimeContract(t *testing.T) {
	if err := dexconfig.ValidateRuntimeYAML([]byte(validRuntime), 1<<20); err != nil {
		t.Fatal(err)
	}
	for name, mutation := range map[string]func(string) string{
		"credential issuer": func(raw string) string {
			return strings.Replace(raw, "https://dex.example.com", "https://user:pass@dex.example.com", 1)
		},
		"query issuer": func(raw string) string {
			return strings.Replace(raw, "https://dex.example.com", "https://dex.example.com?token=x", 1)
		},
		"wrong storage":         func(raw string) string { return strings.Replace(raw, "type: kubernetes", "type: memory", 1) },
		"missing client secret": func(raw string) string { return strings.Replace(raw, "    secret: synthetic\n", "", 1) },
		"insecure redirect": func(raw string) string {
			return strings.Replace(raw, "https://platform.example", "http://platform.example", 1)
		},
		"unknown top level": func(raw string) string { return raw + "futurePassword: canary\n" },
	} {
		t.Run(name, func(t *testing.T) {
			if err := dexconfig.ValidateRuntimeYAML([]byte(mutation(validRuntime)), 1<<20); err == nil {
				t.Fatal("expected validation failure")
			}
		})
	}
}
