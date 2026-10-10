//go:build unix

package distribution

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	builtinbundles "github.com/alphabravocompany/astronomer-go/deploy/bundles"
	"github.com/sigstore/sigstore/pkg/cryptoutils"
	"github.com/sigstore/sigstore/pkg/signature"
)

// This creates a genuinely signed enabled QA profile. No consumer proof fields
// are manufactured: Base and Catalog must authenticate the replacement parent,
// existing child subjects, exact enabled scanner and supplier DB locations.
func publicationBaseFixture(t *testing.T) (*DatabaseAuthority, *VerifiedDatabaseAuthorityBase, Options) {
	t.Helper()
	signer, key, err := signature.NewDefaultECDSASignerVerifier()
	if err != nil {
		t.Fatal(err)
	}
	public, err := cryptoutils.MarshalPublicKeyToPEM(key.Public())
	if err != nil {
		t.Fatal(err)
	}
	return publicationBaseFixtureWithSigner(t, signer, public, nil)
}

func publicationBaseFixtureWithSigner(t *testing.T, signer signature.Signer, public []byte, decorate func(*http.Client) *http.Client) (*DatabaseAuthority, *VerifiedDatabaseAuthorityBase, Options) {
	t.Helper()
	options, fixture := databaseBaseFixture(t)
	var err error
	if err := os.WriteFile(filepath.Join(options.TrustDirectory, "qa.pub"), public, 0600); err != nil {
		t.Fatal(err)
	}
	archive, release, catalogDigest, _ := descriptorFixture(t, fixture, signer, "valid")
	members, bom, err := ReadClosedArchive(archive, release, catalogDigest)
	if err != nil {
		t.Fatal(err)
	}
	var catalog builtinbundles.Catalog
	if err := json.Unmarshal(members["catalog.json"], &catalog); err != nil {
		t.Fatal(err)
	}
	subjects := map[string]string{}
	for _, asset := range bom.Assets {
		subjects[asset.ID] = asset.ArtifactSubject
	}
	values := map[string]map[string]any{}
	for index := range catalog.Components {
		component := &catalog.Components[index]
		if component.Source.Chart == "trivy-operator" {
			component.Values["operator"].(map[string]any)["vulnerabilityScannerEnabled"] = true
			scanner := component.Values["trivy"].(map[string]any)
			scanner["dbRegistry"], scanner["javaDbRegistry"] = "", ""
			scanner["dbRepository"], scanner["javaDbRepository"] = subjects["trivy-database"], subjects["trivy-java-database"]
		}
		values[component.Slug] = component.Values
	}
	members["catalog.json"], err = json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	members["profiles/values.json"], err = json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	bom.CatalogDigest = contentDigest(members["catalog.json"])
	for index := range bom.Members {
		member := &bom.Members[index]
		member.Digest, member.Size = contentDigest(members[member.Path]), int64(len(members[member.Path]))
	}
	members["bom.json"], err = json.Marshal(bom)
	if err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	writer := tar.NewWriter(compressed)
	for name, data := range members {
		if err := writer.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Size: int64(len(data)), Mode: 0600}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	options.Digest = fixture.addArtifact("distribution", MediaType, buffer.Bytes())
	fixture.sign(t, "distribution", options.Digest, signer)
	options.CatalogDigest = bom.CatalogDigest
	options.UpdateBinding.BaseDistributionDigest, options.UpdateBinding.CatalogDigest = options.Digest, options.CatalogDigest
	if decorate != nil {
		options.Client = decorate(options.Client)
	}
	authority, err := NewDatabaseAuthority(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(authority.Close)
	proof, err := authority.Base(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	snapshot, valid := proof.Snapshot()
	if !valid || !snapshot.Base.ScanningEnabled {
		t.Fatal("signed enabled QA base did not grant exact catalog input")
	}
	return authority, proof, options
}

// Capture and inspect genuine supplier bytes under the SAME actual scanner
// compatibility derived from the authenticated publication base. The generic
// supplier fixture's arbitrary scanner digest cannot stand in for that binding.
func publicationSupplierFixture(t *testing.T, base *VerifiedDatabaseAuthorityBase, role string, now time.Time) (SupplierDatabaseOptions, []byte) {
	t.Helper()
	snapshot, valid := base.Snapshot()
	if !valid || !snapshot.Base.ScanningEnabled {
		t.Fatal("actual enabled publication base unavailable")
	}
	_, options, payload, _ := supplierDatabaseFixture(t, role, now, nil)
	options.Scanner = snapshot.Base.Binding.Scanner
	return options, payload
}

func publicationSupplierProof(t *testing.T, base *VerifiedDatabaseAuthorityBase, role string, now time.Time) (*VerifiedSupplierDatabasePayload, SupplierDatabaseSnapshot) {
	t.Helper()
	options, payload := publicationSupplierFixture(t, base, role, now)
	inspector, err := NewSupplierDatabaseInspector(options)
	if err != nil {
		t.Fatal(err)
	}
	captured, err := inspector.Capture(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	verified, err := inspector.Inspect(t.Context(), captured, databasePayloadOwnedFile(t, payload), now)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, valid := verified.Snapshot()
	if !valid {
		t.Fatal("actual complete inspected supplier proof missing")
	}
	return verified, snapshot
}

func TestDatabasePublicationPreparesNewUnsignedSupplierBytesUnderActualBase(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	authority, base, _ := publicationBaseFixture(t)
	main, mainSnapshot := publicationSupplierProof(t, base, "trivy-database", now)
	java, javaSnapshot := publicationSupplierProof(t, base, "trivy-java-database", now)
	repositories := DatabasePublicationRepositories{Main: "1.1.1.1/endorsed/main", Java: "1.1.1.1/endorsed/java"}
	prepared, err := authority.PrepareDatabasePublication(t.Context(), base, main, java, repositories, now)
	if err != nil {
		t.Fatal(err)
	}
	document, valid := prepared.Document(7)
	snapshot, _ := base.Snapshot()
	if !valid || document.Generation != 7 || document.Binding != snapshot.Base.Binding || len(document.Databases) != 2 {
		t.Fatal("producer replaced exact base/generation input")
	}
	for index, original := range []SupplierDatabaseSnapshot{mainSnapshot, javaSnapshot} {
		actual := document.Databases[index]
		repository := repositories.Main
		if index == 1 {
			repository = repositories.Java
		}
		if actual.SupplierSubject != original.SupplierSubject || actual.Manifest != original.Manifest || actual.Config != original.Config || actual.Layer != original.Layer || actual.Metadata != original.Metadata || actual.PublishedSubject != repository+"@"+original.Manifest.Digest {
			t.Fatal("producer changed original supplier bytes or derived metadata")
		}
	}
	// DATA mutation cannot alter the private signing decision or base proof.
	document.Binding.Scanner.Version = "9.9.9"
	document.Databases[0].Layer.Digest = contentDigest([]byte("caller"))
	repeated, valid := prepared.Document(8)
	if !valid || repeated.Generation != 8 || repeated.Binding != snapshot.Base.Binding || repeated.Databases[0].Layer != mainSnapshot.Layer {
		t.Fatal("caller changed immutable producer decision")
	}
	if err := prepared.ValidateCurrent(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if _, valid := prepared.Document(0); valid {
		t.Fatal("unreserved generation accepted")
	}
	if _, valid := (&PreparedDatabasePublication{}).Document(1); valid {
		t.Fatal("zero preparation granted signing data")
	}
}

func TestDatabasePublicationCannotReplacePhysicalSupplierRoleOrDestination(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	authority, base, _ := publicationBaseFixture(t)
	main, _ := publicationSupplierProof(t, base, "trivy-database", now)
	java, _ := publicationSupplierProof(t, base, "trivy-java-database", now)
	repositories := DatabasePublicationRepositories{Main: "1.1.1.1/endorsed/main", Java: "1.1.1.1/endorsed/java"}
	for _, name := range []string{"zero-base", "foreign-base", "zero-main", "missing-java", "swapped-role", "wrong-scanner", "private-destination", "mutable-destination", "same-destination", "stale", "offset-clock", "canceled"} {
		t.Run(name, func(t *testing.T) {
			selectedBase, selectedMain, selectedJava, target, clock, ctx := base, main, java, repositories, now, t.Context()
			switch name {
			case "zero-base":
				selectedBase = &VerifiedDatabaseAuthorityBase{}
			case "foreign-base":
				_, selectedBase, _ = publicationBaseFixture(t)
			case "zero-main":
				selectedMain = &VerifiedSupplierDatabasePayload{}
			case "missing-java":
				selectedJava = nil
			case "swapped-role":
				selectedMain, selectedJava = java, main
			case "wrong-scanner":
				_, options, payload, _ := supplierDatabaseFixture(t, "trivy-database", now, nil)
				options.Scanner.ImageDigest = contentDigest([]byte("other scanner"))
				inspector, err := NewSupplierDatabaseInspector(options)
				if err != nil {
					t.Fatal(err)
				}
				captured, err := inspector.Capture(ctx)
				if err != nil {
					t.Fatal(err)
				}
				selectedMain, err = inspector.Inspect(ctx, captured, databasePayloadOwnedFile(t, payload), now)
				if err != nil {
					t.Fatal(err)
				}
			case "private-destination":
				target.Main = "127.0.0.1/endorsed/main"
			case "mutable-destination":
				target.Main += ":latest"
			case "same-destination":
				target.Java = target.Main
			case "stale":
				clock = now.Add(49 * time.Hour)
			case "offset-clock":
				clock = now.In(time.FixedZone("offset", 3600))
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			candidate, err := authority.PrepareDatabasePublication(ctx, selectedBase, selectedMain, selectedJava, target, clock)
			if err == nil || candidate != nil {
				t.Fatal("unowned or changed producer prerequisite accepted")
			}
			if name == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatal("canonical cancellation identity lost")
			}
		})
	}
}

func TestDatabasePublicationCurrentPreparationRejectsTrustRotationAndElapsedMetadata(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	authority, base, options := publicationBaseFixture(t)
	main, _ := publicationSupplierProof(t, base, "trivy-database", now)
	java, _ := publicationSupplierProof(t, base, "trivy-java-database", now)
	prepared, err := authority.PrepareDatabasePublication(t.Context(), base, main, java, DatabasePublicationRepositories{Main: "1.1.1.1/endorsed/main", Java: "1.1.1.1/endorsed/java"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := prepared.ValidateCurrent(t.Context(), now.Add(49*time.Hour)); err == nil {
		t.Fatal("publication refreshed old upstream metadata")
	}
	if err := os.WriteFile(filepath.Join(options.TrustDirectory, "qa.pub"), []byte("removed valid key"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := prepared.ValidateCurrent(t.Context(), now); err == nil {
		t.Fatal("old physical proof survived actual mounted rotation")
	}
	// An old prepared object must not remain usable after actual authority close.
	authority.Close()
	if err := prepared.ValidateCurrent(t.Context(), now); err == nil {
		t.Fatal("closed physical authority remained current")
	}
}

func TestDatabasePublicationRepositoryDataCannotSelectReferenceOrCredentials(t *testing.T) {
	valid := DatabasePublicationRepositories{Main: "1.1.1.1/endorsed/main", Java: "1.1.1.1/endorsed/java"}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, repository := range []string{"", "1.1.1.1/endorsed/main:latest", "1.1.1.1/endorsed/main@" + contentDigest([]byte("tag")), "user:credential@1.1.1.1/path", "1.1.1.1/../path", "https://1.1.1.1/path"} {
		changed := valid
		changed.Main = repository
		if changed.Validate() == nil {
			t.Fatal("invalid constructor retrieval repository accepted")
		}
	}
}
