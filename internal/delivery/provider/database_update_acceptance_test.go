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

// Pure record comparisons are DATA controls only. These fixtures do not mint
// opaque authentication, create a published reservation or qualify PostgreSQL.
func databaseAcceptanceDataFixture() databaseAcceptanceCapture {
	now := time.Now().UTC().Truncate(time.Second)
	digest := "sha256:" + strings.Repeat("a", 64)
	capture := databaseAcceptanceCapture{feed: digest, document: distribution.DatabaseUpdateDocument{Generation: 2, PublishedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour)}}
	for _, role := range []string{"trivy-database", "trivy-java-database"} {
		schema := 2
		if role == "trivy-java-database" {
			schema = 1
		}
		snapshot := distribution.DatabaseUpdateSnapshot{Role: role, SupplierSubject: "supplier.invalid/db@" + digest, PublishedSubject: "operator.invalid/db@" + digest,
			Manifest: distribution.DatabaseDescriptor{Digest: digest, Size: 616, MediaType: "application/vnd.oci.image.manifest.v1+json"},
			Config:   distribution.DatabaseDescriptor{Digest: digest, Size: 2, MediaType: "application/vnd.oci.empty.v1+json"},
			Layer:    distribution.DatabaseDescriptor{Digest: digest, Size: 128, MediaType: "fixture-layer"},
			Metadata: distribution.DatabaseUpdateMetadata{Digest: digest, Size: 42, Schema: schema, UpdatedAt: now.Add(-time.Hour), NextUpdate: now.Add(time.Hour)}}
		if schema == 2 {
			capture.main = snapshot
		} else {
			capture.java = snapshot
		}
	}
	return capture
}

func TestDatabaseAcceptanceCannotUseCallerDataAsPhysicalAuthority(t *testing.T) {
	if _, err := NewDatabaseUpdateAcceptor(context.Background(), nil, nil, nil, DatabaseUpdateSource{}); err == nil {
		t.Fatal("incomplete physical owner accepted")
	}
	var acceptor DatabaseUpdateAcceptor
	if _, err := acceptor.Accept(context.Background(), uuid.New()); err == nil {
		t.Fatal("zero owner accepted caller cluster")
	}
	var participant databaseAcceptanceParticipant
	if err := participant.revalidateOwnedSnapshot(context.Background()); err == nil {
		t.Fatal("zero participant granted physical authority")
	}
	if _, err := participant.prepareOwnedSnapshot(context.Background(), nil, uuid.New()); err == nil {
		t.Fatal("zero transaction accepted")
	}
}

func TestDatabaseAcceptancePayloadRecordsBindEveryDescriptorAndClock(t *testing.T) {
	captured := databaseAcceptanceDataFixture()
	id := uuid.New()
	main, err := databaseAcceptanceAssetParameters(id, captured.main)
	if err != nil {
		t.Fatal(err)
	}
	java, err := databaseAcceptanceAssetParameters(id, captured.java)
	if err != nil {
		t.Fatal(err)
	}
	rows := []sqlc.VulnerabilityDatabaseUpdateAsset{sqlc.VulnerabilityDatabaseUpdateAsset(main), sqlc.VulnerabilityDatabaseUpdateAsset(java)}
	if !databaseAcceptanceAssetsMatch(id, rows, captured) {
		t.Fatal("exact records rejected")
	}
	mutations := map[string]func(*sqlc.VulnerabilityDatabaseUpdateAsset){
		"update":          func(a *sqlc.VulnerabilityDatabaseUpdateAsset) { a.UpdateID = uuid.New() },
		"role":            func(a *sqlc.VulnerabilityDatabaseUpdateAsset) { a.Role = "other" },
		"supplier":        func(a *sqlc.VulnerabilityDatabaseUpdateAsset) { a.SupplierSubject += "changed" },
		"publisher":       func(a *sqlc.VulnerabilityDatabaseUpdateAsset) { a.PublishedSubject += "changed" },
		"manifest-hash":   func(a *sqlc.VulnerabilityDatabaseUpdateAsset) { a.ManifestDigest += "changed" },
		"manifest-size":   func(a *sqlc.VulnerabilityDatabaseUpdateAsset) { a.ManifestSize++ },
		"manifest-media":  func(a *sqlc.VulnerabilityDatabaseUpdateAsset) { a.ManifestMedia += "changed" },
		"config-hash":     func(a *sqlc.VulnerabilityDatabaseUpdateAsset) { a.ConfigDigest += "changed" },
		"config-size":     func(a *sqlc.VulnerabilityDatabaseUpdateAsset) { a.ConfigSize++ },
		"config-media":    func(a *sqlc.VulnerabilityDatabaseUpdateAsset) { a.ConfigMedia += "changed" },
		"layer-hash":      func(a *sqlc.VulnerabilityDatabaseUpdateAsset) { a.LayerDigest += "changed" },
		"layer-size":      func(a *sqlc.VulnerabilityDatabaseUpdateAsset) { a.LayerSize++ },
		"layer-media":     func(a *sqlc.VulnerabilityDatabaseUpdateAsset) { a.LayerMedia += "changed" },
		"metadata-hash":   func(a *sqlc.VulnerabilityDatabaseUpdateAsset) { a.MetadataDigest += "changed" },
		"metadata-size":   func(a *sqlc.VulnerabilityDatabaseUpdateAsset) { a.MetadataSize++ },
		"metadata-schema": func(a *sqlc.VulnerabilityDatabaseUpdateAsset) { a.MetadataSchema++ },
		"updated":         func(a *sqlc.VulnerabilityDatabaseUpdateAsset) { a.UpdatedAt = a.UpdatedAt.Add(-time.Second) },
		"next":            func(a *sqlc.VulnerabilityDatabaseUpdateAsset) { a.NextUpdate = a.NextUpdate.Add(time.Second) },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			changed := append([]sqlc.VulnerabilityDatabaseUpdateAsset(nil), rows...)
			mutate(&changed[0])
			if databaseAcceptanceAssetsMatch(id, changed, captured) {
				t.Fatal("changed record accepted")
			}
		})
	}
	for _, changed := range [][]sqlc.VulnerabilityDatabaseUpdateAsset{nil, rows[:1], {rows[0], rows[0]}, {rows[0], rows[1], rows[1]}} {
		if databaseAcceptanceAssetsMatch(id, changed, captured) {
			t.Fatal("missing, repeated or extra role accepted")
		}
	}
	rows[0].UpdatedAt = rows[0].UpdatedAt.In(time.FixedZone("database representation", 3600))
	if !databaseAcceptanceAssetsMatch(id, rows, captured) {
		t.Fatal("same PostgreSQL instant interpreted as changed upstream clock")
	}
	for _, snapshot := range []distribution.DatabaseUpdateSnapshot{{Role: "other"}, {Role: "trivy-database"}, {Role: "trivy-java-database", Metadata: distribution.DatabaseUpdateMetadata{Schema: 2}}} {
		if _, err := databaseAcceptanceAssetParameters(id, snapshot); err == nil {
			t.Fatal("unsupported role/schema accepted")
		}
	}
}

func TestDatabaseAcceptanceReplayCannotAdvanceOldOrRevokedGeneration(t *testing.T) {
	captured := databaseAcceptanceDataFixture()
	base := sqlc.VulnerabilityDatabaseBasis{ID: uuid.New(), Epoch: 2, AcceptedGeneration: 2}
	attempt := uuid.New()
	snapshot := distribution.DatabaseAuthoritySnapshot{TrustMaterialFingerprint: "trust"}
	snapshot.Base.PolicyFingerprint = "policy"
	row := sqlc.VulnerabilityDatabaseUpdate{ID: uuid.New(), AttemptID: attempt, AttemptPhase: "published", DecisionDigest: "decision", BaseID: base.ID, BaseEpoch: 2, Generation: 2, FeedDigest: captured.feed, PolicyFingerprint: "policy", TrustMaterialFingerprint: "trust", PublishedAt: captured.document.PublishedAt, ExpiresAt: captured.document.ExpiresAt}
	if !databaseAcceptedUpdateMatches(row, base, attempt, "decision", snapshot, captured) {
		t.Fatal("exact current replay rejected")
	}
	for name, mutate := range map[string]func(*sqlc.VulnerabilityDatabaseUpdate){
		"base":           func(r *sqlc.VulnerabilityDatabaseUpdate) { r.BaseID = uuid.New() },
		"epoch":          func(r *sqlc.VulnerabilityDatabaseUpdate) { r.BaseEpoch++ },
		"generation":     func(r *sqlc.VulnerabilityDatabaseUpdate) { r.Generation-- },
		"attempt":        func(r *sqlc.VulnerabilityDatabaseUpdate) { r.AttemptID = uuid.New() },
		"phase":          func(r *sqlc.VulnerabilityDatabaseUpdate) { r.AttemptPhase = "reserved" },
		"decision":       func(r *sqlc.VulnerabilityDatabaseUpdate) { r.DecisionDigest += "changed" },
		"feed":           func(r *sqlc.VulnerabilityDatabaseUpdate) { r.FeedDigest += "changed" },
		"policy":         func(r *sqlc.VulnerabilityDatabaseUpdate) { r.PolicyFingerprint += "changed" },
		"physical-trust": func(r *sqlc.VulnerabilityDatabaseUpdate) { r.TrustMaterialFingerprint += "changed" },
		"published":      func(r *sqlc.VulnerabilityDatabaseUpdate) { r.PublishedAt = r.PublishedAt.Add(time.Second) },
		"expired":        func(r *sqlc.VulnerabilityDatabaseUpdate) { r.ExpiresAt = time.Now().UTC().Add(-time.Second) },
		"revoked": func(r *sqlc.VulnerabilityDatabaseUpdate) {
			r.RevokedAt = pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := row
			mutate(&changed)
			if databaseAcceptedUpdateMatches(changed, base, attempt, "decision", snapshot, captured) {
				t.Fatal("changed or revoked replay accepted")
			}
		})
	}
	base.AcceptedGeneration++
	if databaseAcceptedUpdateMatches(row, base, attempt, "decision", snapshot, captured) {
		t.Fatal("old feed replaced newer accepted generation")
	}
}

func TestDatabasePublicationDecisionBindsExactFeedData(t *testing.T) {
	// The owning producer must use this same domain and exact signed document;
	// a matching digest alone never approves or publishes the reservation.
	document := databaseAcceptanceDataFixture().document
	document.Databases = []distribution.DatabaseUpdateSnapshot{databaseAcceptanceDataFixture().main}
	baseline, err := databaseUpdateDecisionDigest(document)
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*distribution.DatabaseUpdateDocument){
		"generation": func(d *distribution.DatabaseUpdateDocument) { d.Generation++ },
		"base":       func(d *distribution.DatabaseUpdateDocument) { d.Binding.BaseDistributionDigest = "changed" },
		"mirror":     func(d *distribution.DatabaseUpdateDocument) { d.Binding.MirrorEndorsementDigest = "changed" },
		"scanner":    func(d *distribution.DatabaseUpdateDocument) { d.Binding.Scanner.ImageDigest = "changed" },
		"publisher":  func(d *distribution.DatabaseUpdateDocument) { d.Databases[0].PublishedSubject = "changed" },
		"upstream-clock": func(d *distribution.DatabaseUpdateDocument) {
			d.Databases[0].Metadata.UpdatedAt = d.Databases[0].Metadata.UpdatedAt.Add(time.Second)
		},
		"layer": func(d *distribution.DatabaseUpdateDocument) { d.Databases[0].Layer.Digest = "changed" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := document
			changed.Databases = append([]distribution.DatabaseUpdateSnapshot(nil), document.Databases...)
			mutate(&changed)
			actual, err := databaseUpdateDecisionDigest(changed)
			if err != nil || actual == baseline {
				t.Fatal("changed feed data reused producer decision")
			}
		})
	}
}
