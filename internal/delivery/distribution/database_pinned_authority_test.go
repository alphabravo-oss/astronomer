//go:build unix

package distribution

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
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

	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	"github.com/sigstore/sigstore/pkg/cryptoutils"
	"github.com/sigstore/sigstore/pkg/signature"
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
		for index := range bom.Charts {
			chart := &bom.Charts[index]
			repository := "charts/" + chart.Component
			chart.Repository = host + "/charts"
			chart.Digest = fixture.addArtifact(repository, "application/vnd.cncf.helm.chart.content.v1.tar+gzip", members["charts/"+chart.Component+".tgz"])
			fixture.sign(t, repository, chart.Digest, signer)
		}
	})
	options.Repository = host + "/distribution"
	options.Digest = fixture.addArtifact("distribution", MediaType, archive)
	fixture.sign(t, "distribution", options.Digest, signer)
	options.Release, options.CatalogDigest = release, catalogDigest
	options.UpdateBinding.BaseRelease, options.UpdateBinding.BaseDistributionDigest, options.UpdateBinding.CatalogDigest = release, options.Digest, catalogDigest
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
	if !valid || snapshot.RegistryRootsFingerprint != fmt.Sprintf("sha256:%x", digest) || !digestPattern.MatchString(snapshot.RegistryPolicyFingerprint) || snapshot.TrustMaterialFingerprint == snapshot.RegistryRootsFingerprint || snapshot.Base.ScanningEnabled {
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
