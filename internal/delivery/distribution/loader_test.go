package distribution

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
)

func TestLoaderAuthenticatesOnceAndNeverFallsBack(t *testing.T) {
	payload, release, catalogDigest := archiveFixture(t, nil, nil)
	options := Options{Repository: "1.1.1.1/fixture", Digest: "sha256:" + strings.Repeat("a", 64), Release: release, CatalogDigest: catalogDigest, Profile: "builtins", Policy: model.TrustPolicy{Provider: model.SignatureCosignKey, KeyRef: "fixture"}, Client: &http.Client{}}
	loader, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	loader.fetch = func(_ context.Context, _ *http.Client, repository, _, media string, _ model.TrustPolicy, _ string, _ int64) ([]byte, resolver.Verification, error) {
		calls++
		selected := append([]byte(nil), payload...)
		if media != MediaType {
			_, chart, _ := strings.Cut(repository, "/charts/")
			selected = chartFixture(t, chart)
		}
		return selected, resolver.Verification{Status: "verified", Identity: "key:fixture", Provider: "cosign"}, nil
	}
	first, err := loader.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	first.Components[0].Values["fixture_mutation"] = "discard"
	second, err := loader.Load(t.Context())
	if err != nil || calls != 4 || second.Components[0].Values["fixture_mutation"] != nil {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
	status := loader.Status()
	if !status.InventoryComplete || status.LastSuccessfulSync.IsZero() || status.Verification != "verified" {
		t.Fatalf("status=%+v", status)
	}
	for _, verification := range []string{"unsigned", "failed", ""} {
		loader, _ := New(options)
		loader.fetch = func(context.Context, *http.Client, string, string, string, model.TrustPolicy, string, int64) ([]byte, resolver.Verification, error) {
			return []byte("not an archive"), resolver.Verification{Status: verification}, nil
		}
		if _, err := loader.Load(t.Context()); err == nil || loader.Status().InventoryComplete || loader.cached != nil {
			t.Fatal("unauthenticated archive reached parser/cache")
		}
	}
	options.Development = true
	if _, err := New(options); err == nil {
		t.Fatal("development fixture claimed signed release")
	}
	development, err := New(Options{Development: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = development.Load(t.Context()); err != nil || development.Status().Verification != "unsigned-development" {
		t.Fatal("development mode invalid")
	}
}
