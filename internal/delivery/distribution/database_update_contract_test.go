package distribution

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func databaseUpdateFixture(now time.Time) DatabaseUpdateDocument {
	binding := DatabaseUpdateBinding{BaseRelease: "v1.2.1", BaseDistributionDigest: contentDigest([]byte("base")), CatalogDigest: contentDigest([]byte("catalog")), Profile: "builtins", CandidateSourceCommit: strings.Repeat("a", 40), Scanner: DatabaseScannerCompatibility{ImageDigest: contentDigest([]byte("scanner")), Version: "0.75.0", MainSchema: 2, JavaSchema: 1}}
	document := DatabaseUpdateDocument{SchemaVersion: 1, Kind: "VulnerabilityDatabaseUpdate", Binding: binding, Generation: 1, PublishedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour)}
	for _, role := range []string{"trivy-database", "trivy-java-database"} {
		media, schema := "application/vnd.aquasec.trivy.db.layer.v1.tar+gzip", 2
		if role == "trivy-java-database" {
			media, schema = "application/vnd.aquasec.trivy.javadb.layer.v1.tar+gzip", 1
		}
		subject := contentDigest([]byte(role + "-manifest"))
		document.Databases = append(document.Databases, DatabaseUpdateSnapshot{Role: role, SupplierSubject: "ghcr.io/aquasecurity/" + role + "@" + subject, PublishedSubject: "mirror.example.test/distribution/" + role + "@" + subject, Manifest: DatabaseDescriptor{Digest: subject, Size: 616, MediaType: "application/vnd.oci.image.manifest.v1+json"}, Config: DatabaseDescriptor{Digest: contentDigest([]byte("{}")), Size: 2, MediaType: "application/vnd.oci.empty.v1+json"}, Layer: DatabaseDescriptor{Digest: contentDigest([]byte(role + "-layer")), Size: 100, MediaType: media}, Metadata: DatabaseUpdateMetadata{Digest: contentDigest([]byte(role + "-metadata")), Size: 150, Schema: schema, UpdatedAt: now.Add(-2 * time.Hour), NextUpdate: now.Add(22 * time.Hour)}})
	}
	return document
}

func encodeDatabaseUpdateFixture(t *testing.T, document DatabaseUpdateDocument) []byte {
	t.Helper()
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestDatabaseUpdateClosedContractBindsCandidateRolesAndFiniteClocks(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	original := databaseUpdateFixture(now)
	timing := CadenceDatabaseUpdateTiming()
	if _, err := DecodeDatabaseUpdateDocument(encodeDatabaseUpdateFixture(t, original), original.Binding, timing, now, true); err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []string{"version", "kind", "base", "catalog", "profile", "candidate", "scanner-image", "scanner-version", "scanner-schema", "generation", "nil-databases", "missing-main", "missing-java", "duplicate-role", "extra-role", "manifest-subject", "supplier-subject", "mutable-subject", "credential-subject", "config-media", "empty-config", "layer-media", "layer-size", "layer-digest", "metadata-digest", "metadata-size", "metadata-schema", "published-future", "expires-past", "expiry-order", "expiry-lifetime", "main-age", "java-age", "metadata-future", "metadata-after-publication", "next-order", "next-elapsed", "published-offset", "expires-offset", "updated-offset", "next-offset"} {
		t.Run(mutation, func(t *testing.T) {
			document := databaseUpdateFixture(now)
			snapshot := &document.Databases[0]
			switch mutation {
			case "version":
				document.SchemaVersion++
			case "kind":
				document.Kind = "Other"
			case "base":
				document.Binding.BaseDistributionDigest = contentDigest([]byte("other-base"))
			case "catalog":
				document.Binding.CatalogDigest = contentDigest([]byte("other-catalog"))
			case "profile":
				document.Binding.Profile = "unreviewed"
			case "candidate":
				document.Binding.CandidateSourceCommit = strings.Repeat("b", 40)
			case "scanner-image":
				document.Binding.Scanner.ImageDigest = contentDigest([]byte("other-scanner"))
			case "scanner-version":
				document.Binding.Scanner.Version = "0.76.0"
			case "scanner-schema":
				document.Binding.Scanner.MainSchema++
			case "generation":
				document.Generation = 0
			case "nil-databases":
				document.Databases = nil
			case "missing-main":
				document.Databases = document.Databases[1:]
			case "missing-java":
				document.Databases = document.Databases[:1]
			case "duplicate-role":
				document.Databases[1] = document.Databases[0]
			case "extra-role":
				snapshot.Role = "other"
			case "manifest-subject":
				snapshot.Manifest.Digest = contentDigest([]byte("other-manifest"))
			case "supplier-subject":
				snapshot.SupplierSubject = "ghcr.io/aquasecurity/trivy-db@" + contentDigest([]byte("other-supplier"))
			case "mutable-subject":
				snapshot.PublishedSubject = "mirror.example.test/database:latest"
			case "credential-subject":
				snapshot.PublishedSubject = "user:password@mirror.example.test/database@" + snapshot.Manifest.Digest
			case "config-media":
				snapshot.Config.MediaType = "application/json"
			case "empty-config":
				snapshot.Config.Digest = contentDigest([]byte("changed"))
			case "layer-media":
				snapshot.Layer.MediaType = "application/octet-stream"
			case "layer-size":
				snapshot.Layer.Size = MaximumDatabaseLayerBytes + 1
			case "layer-digest":
				snapshot.Layer.Digest = "sha256:invalid"
			case "metadata-digest":
				snapshot.Metadata.Digest = "sha256:invalid"
			case "metadata-size":
				snapshot.Metadata.Size = 64<<10 + 1
			case "metadata-schema":
				snapshot.Metadata.Schema = 3
			case "published-future":
				document.PublishedAt = now.Add(6 * time.Minute)
			case "expires-past":
				document.ExpiresAt = now
			case "expiry-order":
				document.ExpiresAt = document.PublishedAt
			case "expiry-lifetime":
				document.ExpiresAt = document.PublishedAt.Add(49 * time.Hour)
			case "main-age":
				snapshot.Metadata.UpdatedAt = now.Add(-30*time.Hour - time.Nanosecond)
			case "java-age":
				document.Databases[1].Metadata.UpdatedAt = now.Add(-48*time.Hour - time.Nanosecond)
			case "metadata-future":
				snapshot.Metadata.UpdatedAt = now.Add(6 * time.Minute)
			case "metadata-after-publication":
				snapshot.Metadata.UpdatedAt = document.PublishedAt.Add(6 * time.Minute)
			case "next-order":
				snapshot.Metadata.NextUpdate = snapshot.Metadata.UpdatedAt
			case "next-elapsed":
				snapshot.Metadata.NextUpdate = now
			case "published-offset":
				document.PublishedAt = document.PublishedAt.In(time.FixedZone("non-UTC", 3600))
			case "expires-offset":
				document.ExpiresAt = document.ExpiresAt.In(time.FixedZone("non-UTC", -3600))
			case "updated-offset":
				snapshot.Metadata.UpdatedAt = snapshot.Metadata.UpdatedAt.In(time.FixedZone("non-UTC", 3600))
			case "next-offset":
				snapshot.Metadata.NextUpdate = snapshot.Metadata.NextUpdate.In(time.FixedZone("non-UTC", -3600))
			}
			if _, err := DecodeDatabaseUpdateDocument(encodeDatabaseUpdateFixture(t, document), original.Binding, timing, now, true); err == nil {
				t.Fatal("changed source binding, open role or expired metadata accepted")
			}
		})
	}
	mainOnly := original
	mainOnly.Databases = mainOnly.Databases[:1]
	if _, err := DecodeDatabaseUpdateDocument(encodeDatabaseUpdateFixture(t, mainOnly), original.Binding, timing, now, false); err != nil {
		t.Fatal("qualified Java-not-required input demanded an unnecessary role")
	}
}

func TestDatabaseUpdateJSONRejectsAlternativeInterpretations(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	document := databaseUpdateFixture(now)
	payload := encodeDatabaseUpdateFixture(t, document)
	for name, changed := range map[string][]byte{
		"duplicate":                 append([]byte(`{"schema_version":1,`), payload[1:]...),
		"nested-scanner-duplicate":  bytes.Replace(payload, []byte(`"main_schema":2`), []byte(`"main_schema":2,"main_schema":1`), 1),
		"nested-metadata-duplicate": bytes.Replace(payload, []byte(`"schema":2`), []byte(`"schema":2,"schema":1`), 1),
		"unknown":                   append([]byte(`{"authority":"unsigned",`), payload[1:]...),
		"nested-unknown":            bytes.Replace(payload, []byte(`"main_schema":2`), []byte(`"main_schema":2,"public_fallback":true`), 1),
		"trailing":                  append(bytes.Clone(payload), []byte(` {}`)...),
		"null":                      []byte(`null`),
		"float-generation":          bytes.Replace(payload, []byte(`"generation":1`), []byte(`"generation":1.5`), 1),
		"overflow-generation":       bytes.Replace(payload, []byte(`"generation":1`), []byte(`"generation":9223372036854775808`), 1),
		"oversize":                  bytes.Repeat([]byte{' '}, int(MaximumDatabaseUpdateBytes+1)),
		"depth":                     []byte(strings.Repeat("[", 65) + strings.Repeat("]", 65)),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeDatabaseUpdateDocument(changed, document.Binding, CadenceDatabaseUpdateTiming(), now, true); err == nil {
				t.Fatal("ambiguous or unbounded document accepted")
			}
		})
	}
}

func TestDatabaseUpdateNextUpdateCannotExtendAgeOrOwnedPolicy(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	document := databaseUpdateFixture(now)
	document.Databases[0].Metadata.UpdatedAt = now.Add(-31 * time.Hour)
	document.Databases[0].Metadata.NextUpdate = now.Add(30 * 24 * time.Hour)
	if _, err := DecodeDatabaseUpdateDocument(encodeDatabaseUpdateFixture(t, document), document.Binding, CadenceDatabaseUpdateTiming(), now, true); err == nil {
		t.Fatal("future supplier NextUpdate refreshed an old database")
	}
	document = databaseUpdateFixture(now)
	for _, timing := range []DatabaseUpdateTiming{{}, {MainMaximumAge: 31 * time.Hour, JavaMaximumAge: 48 * time.Hour, FeedLifetime: 48 * time.Hour}, {MainMaximumAge: 30 * time.Hour, JavaMaximumAge: 49 * time.Hour, FeedLifetime: 48 * time.Hour}, {MainMaximumAge: 30 * time.Hour, JavaMaximumAge: 48 * time.Hour, FeedLifetime: 49 * time.Hour}, {MainMaximumAge: 30 * time.Hour, JavaMaximumAge: 48 * time.Hour, FeedLifetime: 48 * time.Hour, ClockSkew: 6 * time.Minute}} {
		if _, err := DecodeDatabaseUpdateDocument(encodeDatabaseUpdateFixture(t, document), document.Binding, timing, now, true); err == nil {
			t.Fatal("missing or excessive constructor timing policy accepted")
		}
	}
}
