package builtin

import (
	"strings"
	"testing"

	builtinbundles "github.com/alphabravocompany/astronomer-go/deploy/bundles"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/google/uuid"
)

func TestVerifiedVersionTransitionPreservesOwnershipAndFencesContent(t *testing.T) {
	catalog, err := builtinbundles.Load()
	if err != nil {
		t.Fatal(err)
	}
	component := catalog.Components[0]
	bundle := uuid.New()
	legacy, legacyLabel := componentVersionIdentity(bundle, catalog.Release, component, "")
	signed, signedLabel := componentVersionIdentity(bundle, catalog.Release, component, "sha256:"+strings.Repeat("a", 64))
	mirrored, _ := componentVersionIdentity(bundle, catalog.Release, component, "sha256:"+strings.Repeat("a", 64)+";mirror:sha256:"+strings.Repeat("b", 64))
	retry, retryLabel := componentVersionIdentity(bundle, catalog.Release, component, "sha256:"+strings.Repeat("a", 64)+";mirror:sha256:"+strings.Repeat("b", 64))
	if legacy == signed || signed == mirrored || mirrored != retry || legacyLabel == signedLabel || retryLabel == "" {
		t.Fatal("distribution identity aliases immutable history")
	}
	component.Source.OriginalURL = component.Source.URL
	component.Source.URL = "oci://registry.corp.local/charts"
	component.Source.Kind = "helm_oci"
	identity, err := catalogSourceIdentityForComponent(uuid.New(), component, "sha256:"+strings.Repeat("a", 64))
	if err != nil || sourceType(identity) != model.SourceHelmOCI || component.Source.OriginalURL == "" {
		t.Fatalf("source=%+v err=%v", identity, err)
	}
	if _, err := catalogSourceIdentity(uuid.New(), component.Source.URL); err == nil {
		t.Fatal("unsigned source-development accepted a private OCI mirror")
	}
}
