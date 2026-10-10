package distribution

import (
	"testing"
	"time"
)

func TestAuthenticatedDatabaseMetadataExpiresWithoutRefreshingItsClock(t *testing.T) {
	// Package-private typed fixtures exercise clock reevaluation only. The real
	// selected-Cosign loader fixture separately establishes authentication.
	now := time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC)
	document := databaseUpdateFixture(now)
	update := &AuthenticatedDatabaseUpdate{digest: contentDigest([]byte("feed")), document: document, timing: CadenceDatabaseUpdateTiming(), javaRequired: true}
	if err := update.ValidateCurrentMetadata(now); err != nil {
		t.Fatal(err)
	}
	if err := update.ValidateCurrentMetadata(document.ExpiresAt); err == nil {
		t.Fatal("authentication became a permanent freshness grant")
	}
	for name, mutate := range map[string]func(*AuthenticatedDatabaseUpdate){
		"main-age": func(u *AuthenticatedDatabaseUpdate) {
			u.document.Databases[0].Metadata.UpdatedAt = now.Add(-31 * time.Hour)
		},
		"java-age": func(u *AuthenticatedDatabaseUpdate) {
			u.document.Databases[1].Metadata.UpdatedAt = now.Add(-49 * time.Hour)
		},
		"elapsed-next": func(u *AuthenticatedDatabaseUpdate) {
			u.document.Databases[0].Metadata.NextUpdate = now.Add(-time.Minute)
		},
		"feed-expired": func(u *AuthenticatedDatabaseUpdate) { u.document.ExpiresAt = now.Add(-time.Minute) },
		"clock-offset": func(u *AuthenticatedDatabaseUpdate) {
			u.document.PublishedAt = u.document.PublishedAt.In(time.FixedZone("offset", 3600))
		},
		"clock-future":   func(u *AuthenticatedDatabaseUpdate) { u.document.Databases[0].Metadata.UpdatedAt = now.Add(time.Hour) },
		"missing-java":   func(u *AuthenticatedDatabaseUpdate) { u.document.Databases = u.document.Databases[:1] },
		"owned-policy":   func(u *AuthenticatedDatabaseUpdate) { u.timing.MainMaximumAge = time.Hour },
		"missing-policy": func(u *AuthenticatedDatabaseUpdate) { u.timing = DatabaseUpdateTiming{} },
	} {
		t.Run(name, func(t *testing.T) {
			changed := *update
			changed.document.Databases = append([]DatabaseUpdateSnapshot(nil), document.Databases...)
			mutate(&changed)
			if changed.ValidateCurrentMetadata(now) == nil {
				t.Fatal("stale or changed finite metadata policy accepted")
			}
		})
	}
	_, exported, valid := update.Snapshot()
	if !valid {
		t.Fatal("fixture snapshot unavailable")
	}
	exported.Databases[0].Metadata.NextUpdate = now.Add(-time.Hour)
	if update.ValidateCurrentMetadata(now) != nil {
		t.Fatal("snapshot DATA mutation changed captured document")
	}
	var zero AuthenticatedDatabaseUpdate
	if zero.ValidateCurrentMetadata(now) == nil {
		t.Fatal("zero update minted current authenticated metadata")
	}
}
