package catalog

import (
	"context"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
)

func TestSignedCatalogRejectsHTTPSBeforeNetworkAccess(t *testing.T) {
	policy := model.TrustPolicy{Provider: model.SignatureCosignKeyless, Identity: "publisher", Issuer: "https://issuer.example"}
	_, _, _, _, err := fetchCatalogSource(context.Background(), SourceOptions{
		URL: "https://unresolvable.invalid/catalog.json", TrustPolicy: &policy,
	})
	if err == nil || !strings.Contains(err.Error(), "digest-pinned OCI") {
		t.Fatalf("expected signed HTTPS source to fail closed, got %v", err)
	}
}

func TestSignedCatalogRejectsUnsignedPolicyBeforeFetch(t *testing.T) {
	policy := model.TrustPolicy{AllowUnsigned: true}
	_, _, _, _, err := fetchCatalogSource(context.Background(), SourceOptions{
		URL:     "oci://registry.invalid/catalog@sha256:" + strings.Repeat("a", 64),
		Clients: SourceClients{}, TrustPolicy: &policy,
	})
	if err == nil || !strings.Contains(err.Error(), "trust policy is invalid") {
		t.Fatalf("expected invalid signed-catalog policy to fail, got %v", err)
	}
}
