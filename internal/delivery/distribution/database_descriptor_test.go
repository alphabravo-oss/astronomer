package distribution

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	digest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sigstore/sigstore/pkg/cryptoutils"
	"github.com/sigstore/sigstore/pkg/signature"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
)

func descriptorFixture(t *testing.T, fixture *registryFixture, signer signature.Signer, mutation string) ([]byte, string, string, map[string]bool) {
	t.Helper()
	payloads := map[string]bool{}
	archive, release, catalogDigest := archiveFixture(t, nil, func(bom *BOM, members map[string][]byte) {
		bom.AssetContractVersion = DescriptorAssetContract
		bom.Profiles[0].RequiredCategories = append(bom.Profiles[0].RequiredCategories, "vulnerability_database")
		bom.Profiles[0].Capabilities = append(bom.Profiles[0].Capabilities, "vulnerability-scanning")
		for index := range bom.Charts {
			chart := &bom.Charts[index]
			repository := "charts/" + chart.Component
			chart.Digest = fixture.addArtifact(repository, "application/vnd.cncf.helm.chart.content.v1.tar+gzip", members["charts/"+chart.Component+".tgz"])
			fixture.sign(t, repository, chart.Digest, signer)
		}
		for _, role := range []string{"trivy-database", "trivy-java-database"} {
			media := "application/vnd.aquasec.trivy.db.layer.v1.tar+gzip"
			size := int64(126378346)
			if role == "trivy-java-database" {
				media = "application/vnd.aquasec.trivy.javadb.layer.v1.tar+gzip"
				size = 980383808
			}
			layer := ocispec.Descriptor{MediaType: media, Digest: digest.FromString("opaque-large-QA-" + role), Size: size}
			payloads[layer.Digest.String()] = true
			config := []byte(`{}`)
			manifest := ocispec.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageManifest, Config: ocispec.Descriptor{MediaType: "application/vnd.oci.empty.v1+json", Digest: digest.FromBytes(config), Size: 2}, Layers: []ocispec.Descriptor{layer}}
			if mutation == "inline-config" || strings.HasPrefix(mutation, "inline-") {
				manifest.Config.Data = append([]byte(nil), config...)
				switch mutation {
				case "inline-data":
					manifest.Config.Data = []byte("[]")
				case "inline-size":
					manifest.Config.Size++
				case "inline-media":
					manifest.Config.MediaType = "application/vnd.aquasec.trivy.config.v1+json"
				case "inline-url":
					manifest.Config.URLs = []string{"https://unapproved.example.test/config"}
				}
			}
			manifestBytes, _ := json.Marshal(manifest)
			subject := contentDigest(manifestBytes)
			manifestPath := "assets/" + role + ".manifest.json"
			configPath := "assets/" + role + ".config.json"
			members[manifestPath] = manifestBytes
			members[configPath] = config
			for name, data := range map[string][]byte{manifestPath: manifestBytes, configPath: config} {
				bom.Members = append(bom.Members, Member{Path: name, Category: "vulnerability_database", Digest: contentDigest(data), Size: int64(len(data))})
			}
			repository := "assets/" + role
			fixture.setManifest(repository+"/"+subject, manifestBytes)
			fixture.setBlob(contentDigest(config), config)
			fixture.sign(t, repository, subject, signer)
			claim := &DatabaseDescriptor{Digest: layer.Digest.String(), Size: layer.Size, MediaType: layer.MediaType}
			asset := Asset{ID: role, MemberPath: manifestPath, ArtifactManifestPath: manifestPath, ArtifactConfigPath: configPath, ArtifactSubject: "supplier.example.test/" + role + "@" + subject, PublishedSubject: "1.1.1.1/" + repository + "@" + subject, Images: []string{}, DatabaseDescriptor: claim}
			if role == "trivy-java-database" {
				switch mutation {
				case "missing-claim":
					asset.DatabaseDescriptor = nil
				case "changed-layer":
					claim.Digest = contentDigest([]byte("changed"))
				case "changed-size":
					claim.Size--
				case "oversized":
					claim.Size = MaximumDatabaseLayerBytes + 1
				case "wrong-media":
					claim.MediaType = "application/vnd.aquasec.trivy.db.layer.v1.tar+gzip"
				case "member-collision":
					asset.ArtifactConfigPath = manifestPath
				case "embedded-in-v2":
					asset.MemberPath = "assets/not-a-payload"
				case "changed-config":
					members[configPath] = []byte(`[]`)
				case "changed-source":
					asset.PublishedSubject = "1.1.1.1/wrong@" + contentDigest([]byte("changed-subject"))
				case "untrusted-child":
					other, _, err := signature.NewDefaultECDSASignerVerifier()
					if err != nil {
						t.Fatal(err)
					}
					fixture.sign(t, repository, subject, other)
				}
			}
			bom.Assets = append(bom.Assets, asset)
		}
	})
	return archive, release, catalogDigest, payloads
}

func TestDescriptorContractAuthenticatesRuntimeChildrenWithoutPayloadQualification(t *testing.T) {
	signer, key, err := signature.NewDefaultECDSASignerVerifier()
	if err != nil {
		t.Fatal(err)
	}
	public, err := cryptoutils.MarshalPublicKeyToPEM(key.Public())
	if err != nil {
		t.Fatal(err)
	}
	trust := t.TempDir()
	if err := os.WriteFile(filepath.Join(trust, "qa.pub"), public, 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"valid", "inline-config", "inline-data", "inline-size", "inline-media", "inline-url", "missing-claim", "changed-layer", "changed-size", "oversized", "wrong-media", "member-collision", "embedded-in-v2", "changed-config", "changed-source", "untrusted-child"} {
		t.Run(name, func(t *testing.T) {
			fixture := &registryFixture{manifests: map[string][]byte{}, blobs: map[string][]byte{}}
			archive, release, catalogDigest, payloads := descriptorFixture(t, fixture, signer, name)
			parent := fixture.addArtifact("distribution", MediaType, archive)
			fixture.sign(t, "distribution", parent, signer)
			server := httptest.NewServer(fixture)
			defer server.Close()
			routed := registryRoutingTransport{fixture: fixture, target: strings.TrimPrefix(server.URL, "http://"), base: server.Client().Transport}
			var payloadReads atomic.Int64
			client := &http.Client{Transport: descriptorRoutingTransport{next: routed, payloads: payloads, reads: &payloadReads}}
			loader, err := New(Options{Repository: "1.1.1.1/distribution", Digest: parent, Release: release, CatalogDigest: catalogDigest, Profile: "builtins", Policy: model.TrustPolicy{Provider: model.SignatureCosignKey, KeyRef: "qa"}, TrustDirectory: trust, Client: client})
			if err != nil {
				t.Fatal(err)
			}
			catalog, err := loader.Load(t.Context())
			if name == "valid" || name == "inline-config" {
				if err != nil || len(catalog.Components) != 3 || loader.Status().Verification != "verified" || loader.Status().DatabasePayloadVerification != "descriptor-only" || loader.Status().DatabaseFreshness != "not-assessed" {
					t.Fatalf("metadata-only consumer positive: %+v %v", loader.Status(), err)
				}
			} else if err == nil || loader.cached != nil {
				t.Fatal("invalid/untrusted descriptor contract accepted/cached")
			}
			if payloadReads.Load() != 0 || fixture.externalCount() != 0 {
				t.Fatal("descriptor authentication fetched large payload/public trust")
			}
		})
	}
}

type descriptorRoutingTransport struct {
	next     http.RoundTripper
	payloads map[string]bool
	reads    *atomic.Int64
}

func (transport descriptorRoutingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	for subject := range transport.payloads {
		if strings.HasSuffix(request.URL.Path, "/blobs/"+subject) {
			transport.reads.Add(1)
		}
	}
	return transport.next.RoundTrip(request)
}

func TestDescriptorClaimsCannotExpandEmbeddedContractOrOtherRoles(t *testing.T) {
	claim := &DatabaseDescriptor{Digest: contentDigest([]byte("payload")), Size: 980383808, MediaType: "application/vnd.aquasec.trivy.javadb.layer.v1.tar+gzip"}
	if claim.validate("trivy-java-database") != nil {
		t.Fatal("valid large finite descriptor rejected")
	}
	if claim.validate("postgres-server") == nil {
		t.Fatal("unrelated role received database allowance")
	}
	subject := contentDigest([]byte("exact QA manifest"))
	asset := Asset{ID: "trivy-java-database", MemberPath: "assets/db", Images: []string{}, ArtifactManifestPath: "assets/manifest", ArtifactConfigPath: "assets/config", ArtifactSubject: "supplier.example.test/db@" + subject, PublishedSubject: "mirror.example.test/db@" + subject}
	members := map[string]string{"assets/db": "vulnerability_database", "assets/manifest": "vulnerability_database", "assets/config": "vulnerability_database"}
	if err := validateAssetDeclarations(EmbeddedAssetContract, []Asset{asset}, members); err != nil {
		t.Fatalf("embedded positive control invalid: %v", err)
	}
	asset.DatabaseDescriptor = claim
	if validateAssetDeclarations(EmbeddedAssetContract, []Asset{asset}, members) == nil {
		t.Fatal("database descriptor expanded old embedded contract")
	}
	other := Asset{ID: "valkey", MemberPath: "assets/valkey", Images: []string{"mirror.example.test/valkey@" + subject}}
	if err := validateAssetDeclarations(DescriptorAssetContract, []Asset{other}, map[string]string{other.MemberPath: "runtime_image"}); err != nil {
		t.Fatalf("other role positive control invalid: %v", err)
	}
	other.DatabaseDescriptor = claim
	if validateAssetDeclarations(DescriptorAssetContract, []Asset{other}, map[string]string{other.MemberPath: "runtime_image"}) == nil {
		t.Fatal("unrelated role received new descriptor contract")
	}
	if MaximumArchiveBytes != 128<<20 || MaximumExpandedBytes != 256<<20 {
		t.Fatal("old parent bounds changed")
	}
}
