package provider

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestDatabaseRestartRetainedRowReconstructionIsComparisonData(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	snapshot := distribution.DatabaseAuthoritySnapshot{
		Base: distribution.DatabaseBaseSnapshot{
			Binding:           distribution.DatabaseUpdateBinding{BaseRelease: "1.2.2", BaseDistributionDigest: digest, CatalogDigest: digest, Profile: "builtins", CandidateSourceCommit: strings.Repeat("a", 40), Scanner: distribution.DatabaseScannerCompatibility{ImageDigest: digest, Version: "0.70.0", MainSchema: 2, JavaSchema: 1}},
			PolicyFingerprint: digest, DistributionIdentity: digest, ChartSourceURL: "oci://registry.fixture/chart", Chart: "trivy-operator", ChartVersion: "0.31.0", ChartDigest: digest, ScanningEnabled: true,
		},
		TrustMaterialFingerprint: digest, VendorTrustFingerprint: digest, VendorTrustOriginFingerprint: digest,
	}
	row := sqlc.VulnerabilityDatabaseBasis{
		ID: uuid.New(), Epoch: 2, PhysicalSnapshotVersion: 1, BaseRelease: snapshot.Base.Binding.BaseRelease, DistributionDigest: digest, DistributionIdentity: digest, CatalogDigest: digest, Profile: "builtins", CandidateSourceCommit: strings.Repeat("a", 40), ScannerDigest: digest, ScannerVersion: "0.70.0", MainSchema: 2, JavaSchema: 1,
		PolicyFingerprint: digest, TrustMaterialFingerprint: digest, VendorTrustFingerprint: digest, VendorTrustOriginFingerprint: digest, ChartSourceUrl: snapshot.Base.ChartSourceURL, ChartName: snapshot.Base.Chart, ChartVersion: snapshot.Base.ChartVersion, ChartDigest: digest, ScanningEnabled: true,
		RevokedAt: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	}
	row.BindingFingerprint = databaseSnapshotBindingFingerprint(snapshot)
	actual, err := retainedDatabaseAuthoritySnapshot(row)
	if err != nil || actual != snapshot {
		t.Fatal("exact retained scalar reconstruction changed historical binding")
	}
	if !row.RevokedAt.Valid {
		t.Fatal("comparison cleared historical permanent revocation")
	}
	for name, mutate := range map[string]func(*sqlc.VulnerabilityDatabaseBasis){
		"version":   func(r *sqlc.VulnerabilityDatabaseBasis) { r.PhysicalSnapshotVersion = 2 },
		"epoch":     func(r *sqlc.VulnerabilityDatabaseBasis) { r.Epoch = 0 },
		"binding":   func(r *sqlc.VulnerabilityDatabaseBasis) { r.BindingFingerprint = "sha256:" + strings.Repeat("b", 64) },
		"candidate": func(r *sqlc.VulnerabilityDatabaseBasis) { r.CandidateSourceCommit = strings.Repeat("b", 40) },
		"scanner":   func(r *sqlc.VulnerabilityDatabaseBasis) { r.ScannerDigest = "sha256:" + strings.Repeat("b", 64) },
		"source":    func(r *sqlc.VulnerabilityDatabaseBasis) { r.ChartSourceUrl = "oci://other.fixture/chart" },
		"chart":     func(r *sqlc.VulnerabilityDatabaseBasis) { r.ChartDigest = "sha256:" + strings.Repeat("b", 64) },
		"vendor-origin": func(r *sqlc.VulnerabilityDatabaseBasis) {
			r.VendorTrustOriginFingerprint = "sha256:" + strings.Repeat("b", 64)
		},
		"mirror-presence": func(r *sqlc.VulnerabilityDatabaseBasis) {
			r.MirrorEndorsementDigest = databaseProjectionOptional(digest)
		},
		"registry-presence": func(r *sqlc.VulnerabilityDatabaseBasis) {
			r.RegistryRootsFingerprint = databaseProjectionOptional(digest)
		},
		"optional-encoding": func(r *sqlc.VulnerabilityDatabaseBasis) { r.MirrorPolicyFingerprint = pgtype.Text{Valid: true} },
		"scanning-enabled":  func(r *sqlc.VulnerabilityDatabaseBasis) { r.ScanningEnabled = false },
	} {
		t.Run(name, func(t *testing.T) {
			changed := row
			mutate(&changed)
			if _, err := retainedDatabaseAuthoritySnapshot(changed); err == nil {
				t.Fatal("changed retained row silently reconstructed exact old authority")
			}
		})
	}
}

func TestDatabaseRestartRevocationRequiresProtectedConstructor(t *testing.T) {
	if _, err := NewDatabaseRestartRevoker(nil, distribution.DatabaseRestartInspectionOptions{}); err == nil {
		t.Fatal("restart revocation accepted missing owned pool")
	}
	for _, owner := range []*DatabaseRestartRevoker{nil, {}} {
		if _, err := owner.RevokeLostCurrent(context.Background()); err == nil {
			t.Fatal("zero restart owner granted revocation")
		}
		if _, err := owner.RevokeLostCurrent(nil); err == nil { //nolint:staticcheck // Deliberately tests rejection of a missing context.
			t.Fatal("nil context granted restart operation")
		}
	}
}
