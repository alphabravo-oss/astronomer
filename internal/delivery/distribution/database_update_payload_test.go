package distribution

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sigstore/sigstore/pkg/cryptoutils"
	"github.com/sigstore/sigstore/pkg/signature"
)

type databasePayloadMember struct {
	header tar.Header
	bytes  []byte
}

func databasePayloadMetadata(t *testing.T, snapshot DatabaseUpdateSnapshot) []byte {
	t.Helper()
	data, err := json.Marshal(upstreamDatabaseMetadata{Version: snapshot.Metadata.Schema, UpdatedAt: snapshot.Metadata.UpdatedAt, NextUpdate: snapshot.Metadata.NextUpdate})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func encodeDatabasePayloadArchive(t *testing.T, members []databasePayloadMember, trailing []byte) []byte {
	t.Helper()
	var uncompressed bytes.Buffer
	archive := tar.NewWriter(&uncompressed)
	for _, member := range members {
		if err := archive.WriteHeader(&member.header); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write(member.bytes); err != nil {
			t.Fatal(err)
		}
	}
	// A deliberately truncated member fixture may make Close fail. Its bytes
	// are retained so inspection must reject it without allocating the size.
	_ = archive.Close()
	_, _ = uncompressed.Write(trailing)
	var compressed bytes.Buffer
	zipper := gzip.NewWriter(&compressed)
	if _, err := zipper.Write(uncompressed.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := zipper.Close(); err != nil {
		t.Fatal(err)
	}
	return compressed.Bytes()
}

func databasePayloadMembers(snapshot DatabaseUpdateSnapshot, metadata []byte) []databasePayloadMember {
	name := "trivy.db"
	if snapshot.Role == "trivy-java-database" {
		name = "trivy-java.db"
	}
	database := []byte("opaque QA database, not a supplier DB")
	return []databasePayloadMember{{header: tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0600, Size: int64(len(database)), Format: tar.FormatUSTAR}, bytes: database}, {header: tar.Header{Name: "metadata.json", Typeflag: tar.TypeReg, Mode: 0600, Size: int64(len(metadata)), Format: tar.FormatUSTAR}, bytes: metadata}}
}

// Unit construction bypasses cryptography inside this package deliberately.
// Canonical signed feed qualification is a separate registry/producer test.
func databasePayloadUnitFixture(t *testing.T, now time.Time, role string) (*AuthenticatedDatabaseUpdate, DatabaseUpdateSnapshot, []byte) {
	t.Helper()
	document := databaseUpdateFixture(now)
	var snapshot DatabaseUpdateSnapshot
	for _, candidate := range document.Databases {
		if candidate.Role == role {
			snapshot = candidate
		}
	}
	metadata := databasePayloadMetadata(t, snapshot)
	payload := encodeDatabasePayloadArchive(t, databasePayloadMembers(snapshot, metadata), nil)
	snapshot.Metadata.Digest, snapshot.Metadata.Size = contentDigest(metadata), int64(len(metadata))
	snapshot.Layer.Digest, snapshot.Layer.Size = contentDigest(payload), int64(len(payload))
	document.Databases = []DatabaseUpdateSnapshot{snapshot}
	return &AuthenticatedDatabaseUpdate{digest: contentDigest([]byte("unit-only authenticated update")), document: document}, snapshot, payload
}

func databasePayloadOwnedFile(t *testing.T, payload []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "payload.tar.gz")
	if err := os.WriteFile(path, payload, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDatabasePayloadWholeHashAndExactMetadataNoExtraction(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	for _, role := range []string{"trivy-database", "trivy-java-database"} {
		t.Run(role, func(t *testing.T) {
			update, expected, payload := databasePayloadUnitFixture(t, now, role)
			verified, err := InspectDatabaseUpdatePayload(t.Context(), update, role, databasePayloadOwnedFile(t, payload), CadenceDatabaseUpdateTiming(), now)
			if err != nil {
				t.Fatal(err)
			}
			feed, snapshot, ok := verified.Snapshot()
			if !ok || feed != update.digest || snapshot != expected {
				t.Fatal("complete payload/metadata not bound")
			}
			if _, _, ok := new(VerifiedDatabaseUpdatePayload).Snapshot(); ok {
				t.Fatal("zero value forged payload verification")
			}
		})
	}
}

func TestCanonicalSignedDatabaseFeedToWholePayloadInspection(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	registry, document, options, _ := databaseUpdateRegistryFixture(t, now)
	paths := map[string]string{}
	for index := range document.Databases {
		snapshot := &document.Databases[index]
		metadata := databasePayloadMetadata(t, *snapshot)
		payload := encodeDatabasePayloadArchive(t, databasePayloadMembers(*snapshot, metadata), nil)
		repository := "databases/" + snapshot.Role
		subject := registry.fixture.addArtifact(repository, snapshot.Layer.MediaType, payload)
		registry.fixture.mutex.Lock()
		manifestBytes := bytes.Clone(registry.fixture.manifests[repository+"/"+subject])
		registry.fixture.mutex.Unlock()
		var manifest ocispec.Manifest
		if json.Unmarshal(manifestBytes, &manifest) != nil {
			t.Fatal("QA manifest invalid")
		}
		snapshot.Manifest = DatabaseDescriptor{Digest: subject, Size: int64(len(manifestBytes)), MediaType: manifest.MediaType}
		snapshot.Layer.Digest, snapshot.Layer.Size = contentDigest(payload), int64(len(payload))
		snapshot.Metadata.Digest, snapshot.Metadata.Size = contentDigest(metadata), int64(len(metadata))
		snapshot.SupplierSubject = "ghcr.io/aquasecurity/" + snapshot.Role + "@" + subject
		snapshot.PublishedSubject = "1.1.1.1/" + repository + "@" + subject
		paths[snapshot.Role] = databasePayloadOwnedFile(t, payload)
	}
	signer, key, err := signature.NewDefaultECDSASignerVerifier()
	if err != nil {
		t.Fatal(err)
	}
	public, err := cryptoutils.MarshalPublicKeyToPEM(key.Public())
	if err != nil || os.WriteFile(filepath.Join(options.TrustDirectory, "qa.pub"), public, 0600) != nil {
		t.Fatal("QA public key preparation failed")
	}
	options = signDatabaseUpdateFixture(t, registry, document, options, signer)
	loader, err := NewDatabaseUpdateLoader(options)
	if err != nil {
		t.Fatal(err)
	}
	update, err := loader.Load(t.Context(), now)
	if err != nil {
		t.Fatal(err)
	}
	for _, snapshot := range document.Databases {
		verified, err := InspectDatabaseUpdatePayload(t.Context(), update, snapshot.Role, paths[snapshot.Role], options.Timing, now)
		if err != nil {
			t.Fatal(err)
		}
		feed, actual, ok := verified.Snapshot()
		if !ok || feed != options.Digest || actual != snapshot {
			t.Fatal("canonical signature and complete local bytes did not compose")
		}
	}
	if registry.fixture.externalCount() != 0 {
		t.Fatal("signed payload composition contacted external trust")
	}
}

func TestDatabasePayloadArchiveAndMetadataNegatives(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	for _, scenario := range []string{"changed-compressed-byte", "partial", "missing-metadata", "missing-db", "wrong-db-name", "extra-member", "duplicate-member", "traversal", "symlink", "hardlink", "pax", "oversized-member", "truncated-member", "second-gzip", "trailing-compressed", "trailing-expanded", "metadata-digest", "metadata-schema", "metadata-clock", "metadata-unknown", "metadata-case-alias", "metadata-duplicate", "metadata-trailing", "unverified-feed", "expired-feed", "unknown-role"} {
		t.Run(scenario, func(t *testing.T) {
			update, snapshot, payload := databasePayloadUnitFixture(t, now, "trivy-database")
			metadata := databasePayloadMetadata(t, snapshot)
			members := databasePayloadMembers(snapshot, metadata)
			role := snapshot.Role
			var trailing []byte
			rebuild := true
			switch scenario {
			case "changed-compressed-byte":
				payload[9] ^= 1 // OS byte is outside gzip content CRC, whole SHA must reject.
				rebuild = false
			case "partial":
				payload = payload[:len(payload)-1]
				rebuild = false
			case "missing-metadata":
				members = members[:1]
			case "missing-db":
				members = members[1:]
			case "wrong-db-name":
				members[0].header.Name = "trivy-java.db"
			case "extra-member":
				members = append(members, databasePayloadMember{header: tar.Header{Name: "other", Typeflag: tar.TypeReg, Mode: 0600, Size: 1, Format: tar.FormatUSTAR}, bytes: []byte("x")})
			case "duplicate-member":
				members = append(members, members[0])
			case "traversal":
				members[0].header.Name = "../trivy.db"
			case "symlink":
				members[0].header.Typeflag, members[0].header.Linkname, members[0].header.Size, members[0].bytes = tar.TypeSymlink, "outside", 0, nil
			case "hardlink":
				members[0].header.Typeflag, members[0].header.Linkname, members[0].header.Size, members[0].bytes = tar.TypeLink, "outside", 0, nil
			case "pax":
				members[0].header.Format, members[0].header.PAXRecords = tar.FormatPAX, map[string]string{"comment": "not allowed"}
			case "oversized-member":
				members = members[:1]
				members[0].header.Size, members[0].bytes = MaximumDatabaseExpandedBytes+1, nil
			case "truncated-member":
				members = members[:1]
				members[0].header.Size, members[0].bytes = 1<<30, nil
			case "second-gzip":
				payload = append(payload, payload...)
				rebuild = false
			case "trailing-compressed":
				payload = append(payload, 'x')
				rebuild = false
			case "trailing-expanded":
				trailing = []byte("unexpected content after tar footer")
			case "metadata-digest":
				snapshot.Metadata.Digest = contentDigest([]byte("wrong metadata"))
			case "metadata-schema":
				metadata = bytes.Replace(metadata, []byte(`"Version":2`), []byte(`"Version":1`), 1)
			case "metadata-clock":
				metadata = bytes.Replace(metadata, []byte(snapshot.Metadata.UpdatedAt.Format(time.RFC3339)), []byte(now.Add(-3*time.Hour).Format(time.RFC3339)), 1)
			case "metadata-unknown":
				metadata = append([]byte(`{"Authority":"unsigned",`), metadata[1:]...)
			case "metadata-case-alias":
				metadata = bytes.Replace(metadata, []byte(`"Version":2`), []byte(`"version":2`), 1)
			case "metadata-duplicate":
				metadata = append([]byte(`{"Version":2,`), metadata[1:]...)
			case "metadata-trailing":
				metadata = append(metadata, []byte(` {}`)...)
			case "unverified-feed":
				update = new(AuthenticatedDatabaseUpdate)
			case "expired-feed":
				update.document.ExpiresAt = now
			case "unknown-role":
				role = "other"
			}
			if strings.HasPrefix(scenario, "metadata-") && scenario != "metadata-digest" {
				members = databasePayloadMembers(snapshot, metadata)
				snapshot.Metadata.Digest, snapshot.Metadata.Size = contentDigest(metadata), int64(len(metadata))
			}
			if rebuild {
				payload = encodeDatabasePayloadArchive(t, members, trailing)
			}
			if scenario != "changed-compressed-byte" && scenario != "partial" {
				snapshot.Layer.Digest, snapshot.Layer.Size = contentDigest(payload), int64(len(payload))
			}
			if scenario != "unverified-feed" {
				update.document.Databases[0] = snapshot
			}
			if result, err := InspectDatabaseUpdatePayload(t.Context(), update, role, databasePayloadOwnedFile(t, payload), CadenceDatabaseUpdateTiming(), now); err == nil || result != nil {
				t.Fatal("malformed payload or unmatched metadata produced verification proof")
			}
		})
	}
}

func TestDatabasePayloadCancellationAfterBytesAndSafeIOErrors(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	update, snapshot, payload := databasePayloadUnitFixture(t, now, "trivy-database")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	authenticated := false
	if result, err := inspectDatabaseUpdateFile(ctx, update, snapshot.Role, databasePayloadOwnedFile(t, payload), CadenceDatabaseUpdateTiming(), now, func() { authenticated = true; cancel() }); result != nil || !errors.Is(err, context.Canceled) || !authenticated {
		t.Fatal("cancellation after compressed bytes lost identity or returned proof")
	}
	file, err := os.Open(databasePayloadOwnedFile(t, payload))
	if err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	if err := hashDatabaseInspectionFile(t.Context(), file, snapshot.Layer); err == nil || strings.Contains(err.Error(), "payload.tar.gz") {
		t.Fatal("raw input failure escaped sanitized boundary")
	}
}

func TestDatabasePayloadIdentityAndSecondHashRejectMutation(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	for _, scenario := range []string{"mtime", "replacement", "changed-content-restored-mtime", "symlink", "partial", "missing", "relative"} {
		t.Run(scenario, func(t *testing.T) {
			update, snapshot, payload := databasePayloadUnitFixture(t, now, "trivy-database")
			path := databasePayloadOwnedFile(t, payload)
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			var afterHash func()
			switch scenario {
			case "mtime":
				afterHash = func() {
					if err := os.Chtimes(path, before.ModTime(), before.ModTime().Add(time.Second)); err != nil {
						t.Fatal(err)
					}
				}
			case "replacement":
				afterHash = func() {
					if err := os.Rename(path, path+".owned-original"); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, payload, 0600); err != nil {
						t.Fatal(err)
					}
				}
			case "changed-content-restored-mtime":
				afterHash = func() {
					changed := bytes.Clone(payload)
					changed[9] ^= 1
					if err := os.WriteFile(path, changed, 0600); err != nil {
						t.Fatal(err)
					}
					if err := os.Chtimes(path, before.ModTime(), before.ModTime()); err != nil {
						t.Fatal(err)
					}
				}
			case "symlink":
				if err := os.Rename(path, path+".original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+".original", path); err != nil {
					t.Fatal(err)
				}
			case "partial":
				if err := os.Truncate(path, int64(len(payload)-1)); err != nil {
					t.Fatal(err)
				}
			case "missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "relative":
				path = "relative.tar.gz"
			}
			if result, err := inspectDatabaseUpdateFile(t.Context(), update, snapshot.Role, path, CadenceDatabaseUpdateTiming(), now, afterHash); result != nil || err == nil {
				t.Fatal("changed file returned payload proof")
			}
		})
	}
}
