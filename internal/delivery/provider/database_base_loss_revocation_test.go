package provider

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
)

func TestLostDatabaseBaseRevocationCannotSelectAnotherPhysicalBinding(t *testing.T) {
	// Relational DATA matching is not a physical loss proof. Production requires
	// the existing constructor's actual observed loss and locked current base.
	snapshot := distribution.DatabaseAuthoritySnapshot{TrustMaterialFingerprint: "material", VendorTrustFingerprint: "vendor", VendorTrustOriginFingerprint: "vendor-origin", MirrorPolicyFingerprint: "mirror-policy", RegistryRootsFingerprint: "roots", RegistryPolicyFingerprint: "registry-policy", RegistryRootOriginFingerprint: "registry-origin"}
	snapshot.Base.ScanningEnabled = true
	snapshot.Base.PolicyFingerprint = "policy"
	snapshot.Base.Binding.BaseDistributionDigest = "parent"
	snapshot.Base.ChartDigest = "chart"
	base := sqlc.VulnerabilityDatabaseBasis{Epoch: 1, PhysicalSnapshotVersion: 1, VendorTrustFingerprint: "vendor", VendorTrustOriginFingerprint: "vendor-origin", MirrorPolicyFingerprint: databaseProjectionOptional("mirror-policy"), RegistryRootsFingerprint: databaseProjectionOptional("roots"), RegistryPolicyFingerprint: databaseProjectionOptional("registry-policy"), RegistryRootOriginFingerprint: databaseProjectionOptional("registry-origin"), ScanningEnabled: true, BindingFingerprint: "binding", TrustMaterialFingerprint: "material", PolicyFingerprint: "policy", DistributionDigest: "parent", ChartDigest: "chart", RevokedAt: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}}
	if !databaseLostBaseMatches(base, snapshot, "binding") {
		t.Fatal("exact permanently revoked replay rejected")
	}
	for name, mutate := range map[string]func(*sqlc.VulnerabilityDatabaseBasis){
		"fingerprint": func(b *sqlc.VulnerabilityDatabaseBasis) { b.BindingFingerprint = "other" },
		"material":    func(b *sqlc.VulnerabilityDatabaseBasis) { b.TrustMaterialFingerprint = "other" },
		"policy":      func(b *sqlc.VulnerabilityDatabaseBasis) { b.PolicyFingerprint = "other" },
		"parent":      func(b *sqlc.VulnerabilityDatabaseBasis) { b.DistributionDigest = "other" },
		"chart":       func(b *sqlc.VulnerabilityDatabaseBasis) { b.ChartDigest = "other" },
		"mirror": func(b *sqlc.VulnerabilityDatabaseBasis) {
			b.MirrorEndorsementDigest = pgtype.Text{String: "other", Valid: true}
		},
		"snapshot-version": func(b *sqlc.VulnerabilityDatabaseBasis) { b.PhysicalSnapshotVersion = 0 },
		"vendor-material":  func(b *sqlc.VulnerabilityDatabaseBasis) { b.VendorTrustFingerprint = "other" },
		"vendor-origin":    func(b *sqlc.VulnerabilityDatabaseBasis) { b.VendorTrustOriginFingerprint = "other" },
		"mirror-policy": func(b *sqlc.VulnerabilityDatabaseBasis) {
			b.MirrorPolicyFingerprint = databaseProjectionOptional("other")
		},
		"registry-roots": func(b *sqlc.VulnerabilityDatabaseBasis) {
			b.RegistryRootsFingerprint = databaseProjectionOptional("other")
		},
		"registry-policy": func(b *sqlc.VulnerabilityDatabaseBasis) {
			b.RegistryPolicyFingerprint = databaseProjectionOptional("other")
		},
		"registry-origin": func(b *sqlc.VulnerabilityDatabaseBasis) {
			b.RegistryRootOriginFingerprint = databaseProjectionOptional("other")
		},
		"registry-absent": func(b *sqlc.VulnerabilityDatabaseBasis) { b.RegistryRootsFingerprint = databaseProjectionOptional("") },
		"enabled":         func(b *sqlc.VulnerabilityDatabaseBasis) { b.ScanningEnabled = false },
		"epoch":           func(b *sqlc.VulnerabilityDatabaseBasis) { b.Epoch = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			changed := base
			mutate(&changed)
			if databaseLostBaseMatches(changed, snapshot, "binding") {
				t.Fatal("different captured base allowed revocation")
			}
		})
	}
	for _, owner := range []*DatabaseBaseOwner{nil, {}} {
		if _, err := owner.RevokeAfterTrustLoss(context.Background()); err == nil {
			t.Fatal("zero owner authorized emergency revocation")
		}
	}
	if !base.RevokedAt.Valid {
		t.Fatal("comparison reset original permanent revocation")
	}
}
