//go:build unix

package distribution

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sigstore/sigstore/pkg/cryptoutils"
	"github.com/sigstore/sigstore/pkg/signature"

	builtinbundles "github.com/alphabravocompany/astronomer-go/deploy/bundles"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

func privateAuthorityRegistryTLS(t *testing.T, handler http.Handler) (*httptest.Server, []byte, netip.Addr) {
	t.Helper()
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	var address netip.Addr
	for _, entry := range addresses {
		prefix, parseErr := netip.ParsePrefix(entry.String())
		if parseErr == nil && prefix.Addr().Is4() && prefix.Addr().IsPrivate() && !prefix.Addr().IsLoopback() {
			address = prefix.Addr()
			break
		}
	}
	if !address.IsValid() {
		t.Fatal("owned TLS test needs a private interface; no implicit skip or loopback bypass")
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(address.String(), "0"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	rootPublic, rootPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	rootTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "owned QA root"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTemplate, rootTemplate, rootPublic, rootPrivate)
	if err != nil {
		t.Fatal(err)
	}
	rootCertificate, err := x509.ParseCertificate(rootDER)
	if err != nil {
		t.Fatal(err)
	}
	leafPublic, leafPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leafTemplate := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "owned QA registry"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IPAddresses: []net.IP{net.ParseIP(address.String())}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, KeyUsage: x509.KeyUsageDigitalSignature}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, rootCertificate, leafPublic, rootPrivate)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	_ = server.Listener.Close()
	server.Listener = listener
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{{Certificate: [][]byte{leafDER}, PrivateKey: leafPrivate}}}
	server.StartTLS()
	t.Cleanup(server.Close)
	return server, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER}), address
}

func pinnedDatabaseAuthorityFixture(t *testing.T) (Options, resolver.DatabaseRegistryTrustOptions, *registryFixture, []byte) {
	t.Helper()
	options, _ := databaseBaseFixture(t)
	options.Client = nil
	fixture := &registryFixture{manifests: map[string][]byte{}, blobs: map[string][]byte{}}
	server, roots, address := privateAuthorityRegistryTLS(t, fixture)
	host := strings.TrimPrefix(server.URL, "https://")
	signer, key, err := signature.NewDefaultECDSASignerVerifier()
	if err != nil {
		t.Fatal(err)
	}
	public, err := cryptoutils.MarshalPublicKeyToPEM(key.Public())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(options.TrustDirectory, "qa.pub"), public, 0600); err != nil {
		t.Fatal(err)
	}
	archive, release, catalogDigest := archiveFixture(t, nil, func(bom *BOM, members map[string][]byte) {
		pinnedMirrorValues(t, bom, members)
		for index := range bom.Charts {
			chart := &bom.Charts[index]
			repository := "charts/" + chart.Component
			chart.Digest = fixture.addArtifact(repository, "application/vnd.cncf.helm.chart.content.v1.tar+gzip", members["charts/"+chart.Component+".tgz"])
			fixture.sign(t, repository, chart.Digest, signer)
		}
	})
	options.Repository = host + "/distribution"
	options.Digest = fixture.addArtifact("distribution", MediaType, archive)
	fixture.sign(t, "distribution", options.Digest, signer)
	options.Release, options.CatalogDigest = release, catalogDigest
	options.UpdateBinding.BaseRelease, options.UpdateBinding.BaseDistributionDigest, options.UpdateBinding.CatalogDigest = release, options.Digest, catalogDigest
	options.MirrorPlan = pinnedMirrorEndorsement(t, options, host)
	options.UpdateBinding.MirrorEndorsementDigest = options.MirrorPlan.identity
	rootDirectory := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootDirectory, "registry.pem"), roots, 0600); err != nil {
		t.Fatal(err)
	}
	network := httpclient.DestinationPolicy{Restricted: true, Origins: []httpclient.TrustedOrigin{{URL: server.URL, CIDRs: []netip.Prefix{netip.PrefixFrom(address, 32)}}}}
	return options, resolver.DatabaseRegistryTrustOptions{TrustDirectory: rootDirectory, RootsRef: "registry", Network: network}, fixture, roots
}

func TestPinnedDatabaseAuthorityBindsActualTLSRootsCatalogAndMountedRotation(t *testing.T) {
	options, registry, fixture, roots := pinnedDatabaseAuthorityFixture(t)
	authority, err := NewPinnedDatabaseAuthority(t.Context(), options, registry)
	if err != nil {
		t.Fatal(err)
	}
	defer authority.Close()
	proof, err := authority.Base(t.Context())
	if err != nil {
		t.Fatal("actual pinned TLS and keyed catalog composition failed")
	}
	snapshot, valid := proof.Snapshot()
	digest := sha256.Sum256(roots)
	if !valid || snapshot.VendorTrustFingerprint != authority.trust.Fingerprint() || snapshot.VendorTrustOriginFingerprint != authority.trust.OriginFingerprint() || snapshot.MirrorPolicyFingerprint != databasePolicyFingerprint(options.MirrorPlan.verified.policy) || snapshot.RegistryRootsFingerprint != fmt.Sprintf("sha256:%x", digest) || !digestPattern.MatchString(snapshot.RegistryPolicyFingerprint) || snapshot.TrustMaterialFingerprint == snapshot.RegistryRootsFingerprint || snapshot.Base.ScanningEnabled || snapshot.Base.Binding.MirrorEndorsementDigest != options.MirrorPlan.identity || snapshot.Base.DistributionIdentity != options.Digest+";mirror:"+options.MirrorPlan.identity || !digestPattern.MatchString(snapshot.MirrorTrustFingerprint) || !digestPattern.MatchString(snapshot.MirrorTrustOriginFingerprint) || snapshot.MirrorTrustFingerprint == authority.trust.Fingerprint() {
		t.Fatal("actual disabled QA catalog did not bind signing/transport authority")
	}
	if err := authority.Revalidate(t.Context(), proof); err != nil {
		t.Fatal(err)
	}
	before := fixture.readCount()
	registry.Network.Origins[0].URL = "https://caller-selected.example.test"
	if err := authority.Revalidate(t.Context(), proof); err != nil {
		t.Fatal("caller changed frozen constructor policy")
	}
	if err := os.WriteFile(filepath.Join(registry.TrustDirectory, "registry.pem"), append(roots, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	if err := authority.Revalidate(t.Context(), proof); err == nil {
		t.Fatal("current mounted TLS roots were not revalidated")
	}
	if repeated, err := authority.Base(t.Context()); err == nil || repeated != nil {
		t.Fatal("cached catalog ignored TLS root rotation")
	}
	if fixture.readCount() != before {
		t.Fatal("rotation failure refetched network content before rejection")
	}
	authority.Close()
	historical, valid := proof.Snapshot()
	if !valid || historical != snapshot {
		t.Fatal("closed authority erased immutable historical registry bindings")
	}
	if err := authority.Revalidate(t.Context(), proof); err == nil {
		t.Fatal("historical snapshot granted current authority after closure")
	}
}

func TestPinnedDatabaseAuthorityRejectsCallerTransportAndUntrustedTLS(t *testing.T) {
	for _, mutation := range []string{"caller-client", "wrong-root", "unsigned-parent"} {
		t.Run(mutation, func(t *testing.T) {
			options, registry, fixture, _ := pinnedDatabaseAuthorityFixture(t)
			switch mutation {
			case "caller-client":
				options.Client = &http.Client{}
			case "wrong-root":
				_, other, _, _ := pinnedDatabaseAuthorityFixture(t)
				material, err := os.ReadFile(filepath.Join(other.TrustDirectory, "registry.pem"))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(registry.TrustDirectory, "registry.pem"), material, 0600); err != nil {
					t.Fatal(err)
				}
			case "unsigned-parent":
				fixture.removeManifest("distribution/sha256-" + strings.TrimPrefix(options.Digest, "sha256:") + ".sig")
			}
			authority, err := NewPinnedDatabaseAuthority(t.Context(), options, registry)
			if mutation == "caller-client" {
				if err == nil || authority != nil {
					t.Fatal("caller transport granted pinned authority")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				defer authority.Close()
				if proof, err := authority.Base(t.Context()); err == nil || proof != nil {
					t.Fatal("wrong TLS root or unsigned catalog granted authority")
				}
			}
		})
	}
}

// Unlike the vendor QA key, this operator key endorses retrieval rewrites only.
// The original chart parent and child OCI signatures stay vendor-key verified.
func pinnedMirrorEndorsement(t *testing.T, options Options, host string) *MirrorPlan {
	t.Helper()
	signer, key, err := signature.NewDefaultECDSASignerVerifier()
	if err != nil {
		t.Fatal(err)
	}
	public, err := cryptoutils.MarshalPublicKeyToPEM(key.Public())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(options.TrustDirectory, "operator.pub"), public, 0600); err != nil {
		t.Fatal(err)
	}
	catalog, err := builtinbundles.Load()
	if err != nil {
		t.Fatal(err)
	}
	plan := MirrorPlan{SchemaVersion: 1, Release: options.Release, DistributionDigest: options.Digest, CatalogDigest: options.CatalogDigest, ReleaseManifestDigest: contentDigest([]byte("owned keyed QA release manifest, not production publication")), Rewrites: []Rewrite{{Kind: "chart_repository", Source: "oci://1.1.1.1/charts", Target: "oci://" + host + "/charts"}}}
	seen := map[string]bool{}
	for _, component := range catalog.Components {
		for _, image := range component.Images {
			if !seen[image] {
				plan.Rewrites = append(plan.Rewrites, Rewrite{Kind: "image", Source: image, Target: host + "/images/" + image})
				seen[image] = true
			}
		}
	}
	payload, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	rawSignature, err := signer.SignMessage(bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	encoded := []byte(base64.StdEncoding.EncodeToString(rawSignature))
	verified, err := VerifyMirrorPlan(t.Context(), payload, encoded, "operator", options.TrustDirectory, options.Release, options.Digest, options.CatalogDigest, plan.ReleaseManifestDigest)
	if err != nil {
		t.Fatal(err)
	}
	// Captured original endorsement bytes must be independent of caller buffers.
	clear(payload)
	clear(encoded)
	return verified
}

// The signed profile explicitly provides image repositories: no inferred
// unsigned Helm defaults or arbitrary substring rewrite can authorize a mirror.
func pinnedMirrorValues(t *testing.T, bom *BOM, members map[string][]byte) {
	t.Helper()
	var catalog builtinbundles.Catalog
	if err := json.Unmarshal(members["catalog.json"], &catalog); err != nil {
		t.Fatal(err)
	}
	values := map[string]map[string]any{}
	for index := range catalog.Components {
		component := &catalog.Components[index]
		repository, _, _ := strings.Cut(component.Images[0], "@")
		component.Values["image"].(map[string]any)["repository"] = repository
		if component.Source.Chart == "trivy-operator" {
			repository, _, _ := strings.Cut(component.Images[1], "@")
			component.Values["trivy"].(map[string]any)["image"].(map[string]any)["repository"] = repository
		}
		values[component.Slug] = component.Values
	}
	var err error
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
}

func TestPinnedDatabaseAuthorityRequiresCurrentSeparateOperatorMirrorKey(t *testing.T) {
	for _, mutation := range []string{"missing-before", "wrong-before", "rotated-after", "missing-after", "unsigned-endorsement", "wrong-release", "wrong-parent", "missing-endorsement", "wrong-feed-mirror"} {
		t.Run(mutation, func(t *testing.T) {
			options, registry, fixture, _ := pinnedDatabaseAuthorityFixture(t)
			before := fixture.readCount()
			mirrorPath := filepath.Join(options.TrustDirectory, "operator.pub")
			switch mutation {
			case "missing-before":
				if err := os.Remove(mirrorPath); err != nil {
					t.Fatal(err)
				}
			case "wrong-before":
				vendor, err := os.ReadFile(filepath.Join(options.TrustDirectory, "qa.pub"))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(mirrorPath, vendor, 0600); err != nil {
					t.Fatal(err)
				}
			case "unsigned-endorsement", "wrong-release", "wrong-parent":
				proof := options.MirrorPlan.verified
				release, parent, signature := options.Release, options.Digest, proof.signature
				if mutation == "wrong-release" {
					release = "v9.9.9"
				}
				if mutation == "wrong-parent" {
					parent = contentDigest([]byte("wrong original parent"))
				}
				if mutation == "unsigned-endorsement" {
					signature = nil
				}
				if plan, err := VerifyMirrorPlan(t.Context(), proof.payload, signature, "operator", options.TrustDirectory, release, parent, options.CatalogDigest, options.MirrorPlan.ReleaseManifestDigest); err == nil || plan != nil {
					t.Fatal("unauthenticated or mismatched operator endorsement accepted")
				}
				if fixture.readCount() != before {
					t.Fatal("endorsement denial contacted registry")
				}
				return
			case "missing-endorsement":
				options.MirrorPlan = nil
				options.UpdateBinding.MirrorEndorsementDigest = ""
			case "wrong-feed-mirror":
				options.UpdateBinding.MirrorEndorsementDigest = contentDigest([]byte("different endorsement"))
			}
			authority, err := NewPinnedDatabaseAuthority(t.Context(), options, registry)
			if mutation == "missing-before" || mutation == "wrong-before" || mutation == "wrong-feed-mirror" {
				if err == nil || authority != nil {
					t.Fatal("changed operator key or mirror binding granted constructor authority")
				}
				if fixture.readCount() != before {
					t.Fatal("constructor denial fetched registry content")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer authority.Close()
			proof, err := authority.Base(t.Context())
			if mutation == "missing-endorsement" {
				if err == nil || proof != nil || authority.loader.cached != nil {
					t.Fatal("restricted network bypassed required authenticated endorsement")
				}
				return
			}
			if err != nil {
				t.Fatal("genuine vendor plus different operator key composition failed")
			}
			historical, valid := proof.Snapshot()
			if !valid {
				t.Fatal("missing actual mirror provenance")
			}
			before = fixture.readCount()
			if mutation == "missing-after" {
				if err := os.Remove(mirrorPath); err != nil {
					t.Fatal(err)
				}
			} else {
				// Same valid vendor key bytes cannot silently replace the different operator key.
				vendor, err := os.ReadFile(filepath.Join(options.TrustDirectory, "qa.pub"))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(mirrorPath, vendor, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := authority.Revalidate(t.Context(), proof); err == nil {
				t.Fatal("operator mirror rotation retained current authority")
			}
			if repeated, err := authority.Base(t.Context()); err == nil || repeated != nil {
				t.Fatal("cached catalog ignored current operator key")
			}
			if fixture.readCount() != before {
				t.Fatal("operator trust denial refetched network content")
			}
			signingDir, mirrorDir := authority.trust.FrozenDirectory(), authority.mirrorTrust.FrozenDirectory()
			authority.Close()
			for _, directory := range []string{signingDir, mirrorDir} {
				if _, err := os.Lstat(directory); !os.IsNotExist(err) {
					t.Fatal("owned signing snapshot was not cleaned")
				}
			}
			repeated, valid := proof.Snapshot()
			if !valid || repeated != historical {
				t.Fatal("closed mirror authority erased historical bindings")
			}
		})
	}
}

func TestPinnedDatabaseAuthorityRejectsChangedVerifiedMirrorDocument(t *testing.T) {
	options, registry, fixture, _ := pinnedDatabaseAuthorityFixture(t)
	before := fixture.readCount()
	for _, field := range []string{"rewrite-source", "rewrite-target", "rewrite-kind", "rewrite-order", "release", "parent", "catalog", "manifest", "schema"} {
		t.Run(field, func(t *testing.T) {
			changed, err := options.MirrorPlan.immutable()
			if err != nil {
				t.Fatal(err)
			}
			switch field {
			case "rewrite-source":
				changed.Rewrites[0].Source = "oci://caller.example.test/charts"
			case "rewrite-target":
				changed.Rewrites[0].Target = "oci://caller.example.test/charts"
			case "rewrite-kind":
				changed.Rewrites[0].Kind = "image"
			case "rewrite-order":
				changed.Rewrites[0], changed.Rewrites[1] = changed.Rewrites[1], changed.Rewrites[0]
			case "release":
				changed.Release = "v9.9.9"
			case "parent":
				changed.DistributionDigest = contentDigest([]byte("caller"))
			case "catalog":
				changed.CatalogDigest = contentDigest([]byte("caller"))
			case "manifest":
				changed.ReleaseManifestDigest = contentDigest([]byte("caller"))
			case "schema":
				changed.SchemaVersion = 2
			}
			candidate := options
			candidate.MirrorPlan = changed
			if authority, err := NewPinnedDatabaseAuthority(t.Context(), candidate, registry); err == nil || authority != nil {
				t.Fatal("caller mutated authenticated mirror document")
			}
			if fixture.readCount() != before {
				t.Fatal("mirror mutation reached network")
			}
		})
	}
	authority, err := NewPinnedDatabaseAuthority(t.Context(), options, registry)
	if err != nil {
		t.Fatal(err)
	}
	defer authority.Close()
	options.MirrorPlan.Rewrites[0].Target = "oci://caller.example.test/charts"
	if _, err := authority.Base(t.Context()); err != nil {
		t.Fatal("post-constructor caller mutation replaced frozen verified rewrites")
	}
}

func TestPinnedDatabaseAuthorityBindsSignedFeedToExactOperatorMirror(t *testing.T) {
	options, registry, fixture, _ := pinnedDatabaseAuthorityFixture(t)
	// Metadata verification fetches the exact declared config, unlike the
	// parent/chart payload-only fixtures. Supply its unchanged authenticated bytes.
	fixture.setBlob(contentDigest([]byte("{}")), []byte("{}"))
	signer, key, err := signature.NewDefaultECDSASignerVerifier()
	if err != nil {
		t.Fatal(err)
	}
	public, err := cryptoutils.MarshalPublicKeyToPEM(key.Public())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(options.TrustDirectory, "qa.pub"), public, 0600); err != nil {
		t.Fatal(err)
	}
	fixture.mutex.Lock()
	var subjects []string
	for name := range fixture.manifests {
		if !strings.HasSuffix(name, ".sig") {
			subjects = append(subjects, name)
		}
	}
	fixture.mutex.Unlock()
	for _, name := range subjects {
		repository, subject, ok := strings.Cut(name, "/sha256:")
		if !ok {
			t.Fatal("immutable QA subject missing")
		}
		fixture.sign(t, repository, "sha256:"+subject, signer)
	}
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	document := databaseUpdateFixture(now)
	document.Binding = *options.UpdateBinding
	host, _, _ := strings.Cut(options.Repository, "/")
	for index := range document.Databases {
		snapshot := &document.Databases[index]
		repository := "updates/" + snapshot.Role
		subject := fixture.addArtifact(repository, snapshot.Layer.MediaType, []byte("opaque QA descriptor payload: "+snapshot.Role))
		fixture.mutex.Lock()
		manifestBytes := bytes.Clone(fixture.manifests[repository+"/"+subject])
		fixture.mutex.Unlock()
		var manifest ocispec.Manifest
		if decodeClosed(manifestBytes, &manifest) != nil || len(manifest.Layers) != 1 {
			t.Fatal("QA child manifest invalid")
		}
		snapshot.Manifest = DatabaseDescriptor{Digest: subject, Size: int64(len(manifestBytes)), MediaType: manifest.MediaType}
		snapshot.Config = DatabaseDescriptor{Digest: manifest.Config.Digest.String(), Size: manifest.Config.Size, MediaType: manifest.Config.MediaType}
		snapshot.Layer = DatabaseDescriptor{Digest: manifest.Layers[0].Digest.String(), Size: manifest.Layers[0].Size, MediaType: manifest.Layers[0].MediaType}
		snapshot.SupplierSubject = "ghcr.io/aquasecurity/" + snapshot.Role + "@" + subject
		snapshot.PublishedSubject = host + "/" + repository + "@" + subject
		fixture.sign(t, repository, subject, signer)
	}
	authority, err := NewPinnedDatabaseAuthority(t.Context(), options, registry)
	if err != nil {
		t.Fatal(err)
	}
	defer authority.Close()
	proof, err := authority.Base(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"exact-mirror", "missing-mirror", "different-mirror"} {
		t.Run(scenario, func(t *testing.T) {
			candidate := document
			if scenario == "missing-mirror" {
				candidate.Binding.MirrorEndorsementDigest = ""
			}
			if scenario == "different-mirror" {
				candidate.Binding.MirrorEndorsementDigest = contentDigest([]byte("different operator mirror"))
			}
			subject := fixture.addArtifact("updates", DatabaseUpdateMediaType, encodeDatabaseUpdateFixture(t, candidate))
			fixture.sign(t, "updates", subject, signer)
			update, err := authority.LoadUpdate(t.Context(), proof, host+"/updates", subject, now)
			if scenario != "exact-mirror" {
				if err == nil || update != nil {
					t.Fatal("valid vendor signature replaced current operator mirror binding")
				}
				return
			}
			if err != nil {
				t.Fatal("canonical signed mirrored feed or child failed")
			}
			_, snapshot, valid := update.Snapshot()
			if !valid || snapshot.Binding != document.Binding {
				t.Fatal("authenticated mirrored binding lost")
			}
		})
	}
	before := fixture.readCount()
	if err := os.Remove(filepath.Join(options.TrustDirectory, "operator.pub")); err != nil {
		t.Fatal(err)
	}
	subject := contentDigest([]byte("no retrieval permitted after operator trust change"))
	if update, err := authority.LoadUpdate(t.Context(), proof, host+"/updates", subject, now); err == nil || update != nil {
		t.Fatal("operator key rotation allowed feed acceptance")
	}
	if fixture.readCount() != before {
		t.Fatal("stale operator key authority fetched network content")
	}
	// This fixture authenticates descriptor claims only. Its disabled scanner and
	// opaque payload never establish real DB metadata, freshness or scan use.
}
