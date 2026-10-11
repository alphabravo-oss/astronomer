//go:build unix

package distribution

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDatabasePhysicalCatalogUsesOpaqueProofAndOwnedCanonicalCopy(t *testing.T) {
	options, _ := databaseBaseFixture(t)
	counter := &physicalAuthorityRequestCounter{base: options.Client.Transport}
	options.Client.Transport = counter
	authority, err := NewDatabaseAuthority(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer authority.Close()
	proof, err := authority.Base(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	first, err := authority.Catalog(t.Context(), proof)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, valid := proof.Snapshot()
	if !valid || first.DistributionIdentity != snapshot.Base.DistributionIdentity || len(first.Components) == 0 {
		t.Fatal("catalog did not preserve opaque base identity")
	}
	source := first.Components[0].Source.URL
	first.Components[0].Source.URL = "oci://caller-selected.invalid/changed"
	first.Components[0].Values["caller-selected"] = "not authorized"
	first.DistributionIdentity = "caller-selected"
	second, err := authority.Catalog(t.Context(), proof)
	if err != nil || second.Components[0].Source.URL != source || second.Components[0].Values["caller-selected"] != nil || second.DistributionIdentity != snapshot.Base.DistributionIdentity {
		t.Fatal("caller mutated physical compilation catalog")
	}
	other, err := NewDatabaseAuthority(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	before := counter.count.Load()
	for _, invalid := range []*VerifiedDatabaseAuthorityBase{nil, {}, {owner: other}} {
		if _, err := authority.Catalog(t.Context(), invalid); err == nil {
			t.Fatal("caller/foreign proof granted physical catalog")
		}
	}
	if _, err := authority.Catalog(nil, proof); err == nil { //nolint:staticcheck // Deliberately tests rejection of a missing context.
		t.Fatal("nil context accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := authority.Catalog(ctx, proof); !errors.Is(err, context.Canceled) {
		t.Fatal("catalog lost canonical cancellation")
	}
	if err := os.WriteFile(filepath.Join(options.TrustDirectory, "qa.pub"), []byte("rotated mounted key"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := authority.Catalog(t.Context(), proof); err == nil {
		t.Fatal("cached catalog ignored current physical rotation")
	}
	if counter.count.Load() != before {
		t.Fatal("denial fetched before validating physical proof")
	}
	authority.Close()
	if _, err := authority.Catalog(t.Context(), proof); err == nil {
		t.Fatal("closed constructor granted catalog")
	}
}

func TestPinnedDatabasePhysicalCatalogPreservesExactOperatorMirrorIdentity(t *testing.T) {
	options, registry, fixture, _ := pinnedDatabaseAuthorityFixture(t)
	authority, err := NewPinnedDatabaseAuthority(t.Context(), options, registry)
	if err != nil {
		t.Fatal(err)
	}
	defer authority.Close()
	proof, err := authority.Base(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := authority.Catalog(t.Context(), proof)
	if err != nil || catalog.DistributionIdentity != options.Digest+";mirror:"+options.MirrorPlan.Identity() {
		t.Fatal("physical catalog lost exact verified operator endorsement")
	}
	before := fixture.readCount()
	if err := os.Remove(filepath.Join(options.MirrorPlan.verified.trustDirectory, options.MirrorPlan.verified.policy.KeyRef+".pub")); err != nil {
		t.Fatal(err)
	}
	if _, err := authority.Catalog(t.Context(), proof); err == nil {
		t.Fatal("physical catalog ignored removed operator key")
	}
	if fixture.readCount() != before {
		t.Fatal("removed operator key refetched before rejection")
	}
}
