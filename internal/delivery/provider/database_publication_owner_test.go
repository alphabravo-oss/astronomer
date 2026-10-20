package provider

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
)

// These records test exact comparisons only; no record DATA can issue opaque
// physical/supplier/readback proof or stand in for the actual PG transitions.
func databasePublicationRecordFixture() (sqlc.VulnerabilityDatabaseBasis, sqlc.VulnerabilityDatabaseUpdateAttempt, distribution.DatabaseUpdateDocument, string) {
	digest := "sha256:" + strings.Repeat("a", 64)
	now := time.Now().UTC().Truncate(time.Second)
	binding := distribution.DatabaseUpdateBinding{BaseRelease: "v1.2.1", BaseDistributionDigest: digest, MirrorEndorsementDigest: digest, CatalogDigest: digest, Profile: "builtins", CandidateSourceCommit: strings.Repeat("b", 40), Scanner: distribution.DatabaseScannerCompatibility{ImageDigest: digest, Version: "0.70.0", MainSchema: 2, JavaSchema: 1}}
	document := distribution.DatabaseUpdateDocument{SchemaVersion: 1, Kind: "VulnerabilityDatabaseUpdate", Binding: binding, Generation: 3, PublishedAt: now, ExpiresAt: now.Add(time.Hour), Databases: []distribution.DatabaseUpdateSnapshot{{Role: "trivy-database", SupplierSubject: "supplier.invalid/db@" + digest, PublishedSubject: "operator.invalid/db@" + digest}}}
	base := sqlc.VulnerabilityDatabaseBasis{ID: uuid.New(), Epoch: 2, BaseRelease: binding.BaseRelease, DistributionDigest: binding.BaseDistributionDigest, MirrorEndorsementDigest: databaseProjectionOptional(binding.MirrorEndorsementDigest), CatalogDigest: binding.CatalogDigest, Profile: binding.Profile, CandidateSourceCommit: binding.CandidateSourceCommit, ScannerDigest: binding.Scanner.ImageDigest, ScannerVersion: binding.Scanner.Version, MainSchema: 2, JavaSchema: 1, ScanningEnabled: true}
	decision, err := databaseUpdateDecisionDigest(document)
	if err != nil {
		panic("fixture canonical DATA cannot be encoded")
	}
	attempt := sqlc.VulnerabilityDatabaseUpdateAttempt{ID: uuid.New(), BaseID: base.ID, BaseEpoch: base.Epoch, Generation: document.Generation, DecisionDigest: decision, Phase: "reserved", CreatedAt: now, ExpiresAt: now.Add(45 * time.Minute)}
	return base, attempt, document, decision
}

func TestDatabasePublicationRecordCannotChangeCurrentBaseOrScanner(t *testing.T) {
	base, attempt, document, decision := databasePublicationRecordFixture()
	if !databasePublicationAttemptMatches(attempt, base, document, decision) {
		t.Fatal("exact DATA fixture rejected")
	}
	for name, mutate := range map[string]func(*sqlc.VulnerabilityDatabaseBasis){
		"id":              func(b *sqlc.VulnerabilityDatabaseBasis) { b.ID = uuid.New() },
		"epoch":           func(b *sqlc.VulnerabilityDatabaseBasis) { b.Epoch++ },
		"release":         func(b *sqlc.VulnerabilityDatabaseBasis) { b.BaseRelease = "v9.9.9" },
		"parent":          func(b *sqlc.VulnerabilityDatabaseBasis) { b.DistributionDigest += "changed" },
		"mirror":          func(b *sqlc.VulnerabilityDatabaseBasis) { b.MirrorEndorsementDigest = pgtype.Text{} },
		"catalog":         func(b *sqlc.VulnerabilityDatabaseBasis) { b.CatalogDigest += "changed" },
		"profile":         func(b *sqlc.VulnerabilityDatabaseBasis) { b.Profile = "other" },
		"candidate":       func(b *sqlc.VulnerabilityDatabaseBasis) { b.CandidateSourceCommit = strings.Repeat("c", 40) },
		"scanner-image":   func(b *sqlc.VulnerabilityDatabaseBasis) { b.ScannerDigest += "changed" },
		"scanner-version": func(b *sqlc.VulnerabilityDatabaseBasis) { b.ScannerVersion = "9.9.9" },
		"main-schema":     func(b *sqlc.VulnerabilityDatabaseBasis) { b.MainSchema++ },
		"java-schema":     func(b *sqlc.VulnerabilityDatabaseBasis) { b.JavaSchema++ },
		"disabled":        func(b *sqlc.VulnerabilityDatabaseBasis) { b.ScanningEnabled = false },
		"revoked": func(b *sqlc.VulnerabilityDatabaseBasis) {
			b.RevokedAt = pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := base
			mutate(&changed)
			if databasePublicationAttemptMatches(attempt, changed, document, decision) {
				t.Fatal("changed current base matched reservation")
			}
		})
	}
	for name, mutate := range map[string]func(*sqlc.VulnerabilityDatabaseUpdateAttempt){
		"zero-id":    func(a *sqlc.VulnerabilityDatabaseUpdateAttempt) { a.ID = uuid.Nil },
		"base":       func(a *sqlc.VulnerabilityDatabaseUpdateAttempt) { a.BaseID = uuid.New() },
		"epoch":      func(a *sqlc.VulnerabilityDatabaseUpdateAttempt) { a.BaseEpoch++ },
		"generation": func(a *sqlc.VulnerabilityDatabaseUpdateAttempt) { a.Generation++ },
		"decision":   func(a *sqlc.VulnerabilityDatabaseUpdateAttempt) { a.DecisionDigest += "changed" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := attempt
			mutate(&changed)
			if databasePublicationAttemptMatches(changed, base, document, decision) {
				t.Fatal("changed reservation matched base")
			}
		})
	}
}

func TestDatabasePublicationReservationCannotReplaceLockedAttempt(t *testing.T) {
	_, attempt, _, _ := databasePublicationRecordFixture()
	if !databasePublicationReservationMatches(attempt, attempt) {
		t.Fatal("exact immutable attempt rejected")
	}
	for name, mutate := range map[string]func(*sqlc.VulnerabilityDatabaseUpdateAttempt){
		"id":         func(a *sqlc.VulnerabilityDatabaseUpdateAttempt) { a.ID = uuid.New() },
		"base":       func(a *sqlc.VulnerabilityDatabaseUpdateAttempt) { a.BaseID = uuid.New() },
		"epoch":      func(a *sqlc.VulnerabilityDatabaseUpdateAttempt) { a.BaseEpoch++ },
		"generation": func(a *sqlc.VulnerabilityDatabaseUpdateAttempt) { a.Generation++ },
		"decision":   func(a *sqlc.VulnerabilityDatabaseUpdateAttempt) { a.DecisionDigest += "changed" },
		"created":    func(a *sqlc.VulnerabilityDatabaseUpdateAttempt) { a.CreatedAt = a.CreatedAt.Add(time.Second) },
		"expires":    func(a *sqlc.VulnerabilityDatabaseUpdateAttempt) { a.ExpiresAt = a.ExpiresAt.Add(time.Second) },
	} {
		t.Run(name, func(t *testing.T) {
			changed := attempt
			mutate(&changed)
			if databasePublicationReservationMatches(changed, attempt) {
				t.Fatal("changed locked attempt matched owner")
			}
		})
	}
	// Phase is evaluated by the actual transition after immutable identity matches.
	published := attempt
	published.Phase = "published"
	published.PublishedFeedDigest = databaseProjectionOptional("sha256:" + strings.Repeat("b", 64))
	if !databasePublicationReservationMatches(published, attempt) {
		t.Fatal("legitimate phase transition changed immutable reservation identity")
	}
}

func TestDatabasePublicationReadbackMatchesWholeReservedDocument(t *testing.T) {
	_, _, document, _ := databasePublicationRecordFixture()
	if !databasePublicationDocumentMatches(document, document) {
		t.Fatal("exact readback DATA rejected")
	}
	for name, mutate := range map[string]func(*distribution.DatabaseUpdateDocument){
		"kind":          func(d *distribution.DatabaseUpdateDocument) { d.Kind = "other" },
		"schema":        func(d *distribution.DatabaseUpdateDocument) { d.SchemaVersion++ },
		"generation":    func(d *distribution.DatabaseUpdateDocument) { d.Generation++ },
		"mirror":        func(d *distribution.DatabaseUpdateDocument) { d.Binding.MirrorEndorsementDigest = "changed" },
		"scanner":       func(d *distribution.DatabaseUpdateDocument) { d.Binding.Scanner.ImageDigest = "changed" },
		"publisher":     func(d *distribution.DatabaseUpdateDocument) { d.Databases[0].PublishedSubject = "changed" },
		"supplier":      func(d *distribution.DatabaseUpdateDocument) { d.Databases[0].SupplierSubject = "changed" },
		"metadata":      func(d *distribution.DatabaseUpdateDocument) { d.Databases[0].Metadata.NextUpdate = time.Now().UTC() },
		"clock":         func(d *distribution.DatabaseUpdateDocument) { d.PublishedAt = d.PublishedAt.Add(time.Second) },
		"expiry":        func(d *distribution.DatabaseUpdateDocument) { d.ExpiresAt = d.ExpiresAt.Add(time.Second) },
		"missing-child": func(d *distribution.DatabaseUpdateDocument) { d.Databases = nil },
	} {
		t.Run(name, func(t *testing.T) {
			changed := document
			changed.Databases = append([]distribution.DatabaseUpdateSnapshot(nil), document.Databases...)
			mutate(&changed)
			if databasePublicationDocumentMatches(changed, document) {
				t.Fatal("changed signed readback matched reserved decision")
			}
		})
	}
}

func TestDatabasePublicationRequiresPrivateConstructorAndReservation(t *testing.T) {
	repositories := distribution.DatabasePublicationRepositories{Main: "1.1.1.1/main", Java: "1.1.1.1/java"}
	if owner, err := NewDatabasePublicationOwner(t.Context(), nil, nil, repositories); err == nil || owner != nil {
		t.Fatal("caller DATA created publication owner")
	}
	owner := &DatabasePublicationOwner{}
	if reservation, err := owner.Reserve(t.Context(), nil, nil); err == nil || reservation != nil {
		t.Fatal("zero owner reserved generation")
	}
	if err := owner.PublishVerified(t.Context(), &DatabasePublicationReservation{}, "1.1.1.1/update", "sha256:"+strings.Repeat("a", 64)); err == nil {
		t.Fatal("zero reservation or caller digest marked publication")
	}
	if err := owner.Fail(t.Context(), &DatabasePublicationReservation{}); err == nil {
		t.Fatal("foreign reservation failed transition")
	}
	if _, _, valid := (&DatabasePublicationReservation{}).Snapshot(); valid {
		t.Fatal("zero reservation produced signing DATA")
	}
	foreign := &DatabasePublicationOwner{}
	reservation := &DatabasePublicationReservation{owner: foreign}
	if err := owner.PublishVerified(context.Background(), reservation, "1.1.1.1/update", "sha256:"+strings.Repeat("a", 64)); err == nil {
		t.Fatal("different constructor published caller reservation")
	}
}
