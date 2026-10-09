package distribution

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	digest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sigstore/sigstore/pkg/cryptoutils"
	"github.com/sigstore/sigstore/pkg/signature"
)

type registryFixture struct {
	manifests        map[string][]byte
	blobs            map[string][]byte
	payloadReads     int
	externalRequests int
	corruptBlob      string
}

func (f *registryFixture) addArtifact(repository, media string, payload []byte) string {
	d := digest.FromBytes(payload)
	f.blobs[d.String()] = payload
	manifest := ocispec.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageManifest, Config: ocispec.Descriptor{MediaType: "application/vnd.oci.empty.v1+json", Digest: digest.FromString("{}"), Size: 2}, Layers: []ocispec.Descriptor{{MediaType: media, Digest: d, Size: int64(len(payload))}}}
	data, _ := json.Marshal(manifest)
	subject := contentDigest(data)
	f.manifests[repository+"/"+subject] = data
	return subject
}
func (f *registryFixture) sign(t *testing.T, repository, subject string, signer signature.Signer) {
	t.Helper()
	payload := []byte(fmt.Sprintf(`{"critical":{"type":"cosign container image signature","image":{"Docker-manifest-digest":%q}}}`, subject))
	raw, err := signer.SignMessage(bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	d := digest.FromBytes(payload)
	f.blobs[d.String()] = payload
	manifest := ocispec.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageManifest, Layers: []ocispec.Descriptor{{MediaType: "application/vnd.dev.cosign.simplesigning.v1+json", Digest: d, Size: int64(len(payload)), Annotations: map[string]string{"dev.cosignproject.cosign/signature": base64.StdEncoding.EncodeToString(raw)}}}}
	data, _ := json.Marshal(manifest)
	f.manifests[repository+"/sha256-"+strings.TrimPrefix(subject, "sha256:")+".sig"] = data
}
func (f *registryFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/v2/" {
		w.WriteHeader(200)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/v2/")
	var data []byte
	if repository, reference, found := strings.Cut(path, "/manifests/"); found {
		data = f.manifests[repository+"/"+reference]
		w.Header().Set("Content-Type", ocispec.MediaTypeImageManifest)
	} else if _, reference, found := strings.Cut(path, "/blobs/"); found {
		data = f.blobs[reference]
		if reference == f.corruptBlob {
			data = []byte("changed bytes at immutable subject")
		}
		f.payloadReads++
	} else {
		http.NotFound(w, r)
		return
	}
	if data == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Docker-Content-Digest", contentDigest(data))
	w.Header().Set("Content-Length", fmt.Sprint(len(data)))
	if r.Method != "HEAD" {
		_, _ = w.Write(data)
	}
}

type registryRoutingTransport struct {
	fixture *registryFixture
	target  string
	base    http.RoundTripper
}

func (r registryRoutingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Host != "1.1.1.1" {
		r.fixture.externalRequests++
		return nil, fmt.Errorf("unexpected external fixture destination")
	}
	clone := request.Clone(request.Context())
	u := *request.URL
	clone.URL = &u
	clone.URL.Scheme = "http"
	clone.URL.Host = r.target
	return r.base.RoundTrip(clone)
}

// This uses ephemeral QA identity, real ECDSA signatures, the production OCI
// verifier and payload reader, and an actual HTTP registry fixture. It is not
// vendor publication or a disconnected cluster qualification.
func TestVerifiedRegistryParentAndEveryChildBeforeCache(t *testing.T) {
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
	f := &registryFixture{manifests: map[string][]byte{}, blobs: map[string][]byte{}}
	parent, release, catalogDigest := archiveFixture(t, nil, func(bom *BOM, members map[string][]byte) {
		for index := range bom.Charts {
			subject := &bom.Charts[index]
			repository := "charts/" + subject.Component
			subject.Repository = "1.1.1.1/charts"
			subject.Digest = f.addArtifact(repository, "application/vnd.cncf.helm.chart.content.v1.tar+gzip", members["charts/"+subject.Component+".tgz"])
			f.sign(t, repository, subject.Digest, signer)
		}
	})
	subject := f.addArtifact("distribution", MediaType, parent)
	f.sign(t, "distribution", subject, signer)
	server := httptest.NewServer(f)
	defer server.Close()
	client := &http.Client{Transport: registryRoutingTransport{fixture: f, target: strings.TrimPrefix(server.URL, "http://"), base: server.Client().Transport}}
	options := Options{Repository: "1.1.1.1/distribution", Digest: subject, Release: release, CatalogDigest: catalogDigest, Profile: "builtins", Policy: model.TrustPolicy{Provider: model.SignatureCosignKey, KeyRef: "qa"}, TrustDirectory: trust, Client: client}
	loader, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := loader.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Components) != 3 || loader.Status().Verification != "verified" || f.externalRequests != 0 {
		t.Fatal("QA signed chain incomplete or contacted external trust")
	}
	for _, component := range catalog.Components {
		if component.Source.Kind != "helm_oci" || component.Source.OriginalURL == "" || component.Source.OriginalArchiveDigest == "" || component.Source.ChartDigest == component.Source.OriginalArchiveDigest {
			t.Fatal("child content identity was not projected")
		}
	}
	reads := f.payloadReads
	if _, err := loader.Load(t.Context()); err != nil || f.payloadReads != reads {
		t.Fatal("validated source cache refetched")
	}
	// A valid parent must not authorize a missing child signature.
	var childRepository, signatureTag string
	for name := range f.manifests {
		if strings.HasPrefix(name, "charts/") && strings.HasSuffix(name, ".sig") {
			childRepository, signatureTag, _ = strings.Cut(name, "/sha256-")
			break
		}
	}
	missingKey := childRepository + "/sha256-" + signatureTag
	saved := f.manifests[missingKey]
	delete(f.manifests, missingKey)
	fresh, _ := New(options)
	if _, err := fresh.Load(t.Context()); err == nil || fresh.cached != nil {
		t.Fatal("parent signature authorized unsigned child")
	}
	f.manifests[missingKey] = saved
	// Changed archive bytes at an identical chart version/manifest cannot be consumed.
	f.corruptBlob = contentDigest(chartFixture(t, catalog.Components[0].Slug))
	fresh, _ = New(options)
	if _, err := fresh.Load(t.Context()); err == nil || fresh.cached != nil {
		t.Fatal("changed child payload accepted")
	}
	f.corruptBlob = ""
	// Wrong mounted QA key rejects the signature before reading parent payload.
	other, _, err := signature.NewDefaultECDSASignerVerifier()
	if err != nil {
		t.Fatal(err)
	}
	f.sign(t, "distribution", subject, other)
	before := f.payloadReads
	fresh, _ = New(options)
	if _, err := fresh.Load(t.Context()); err == nil || fresh.cached != nil || f.payloadReads != before+1 {
		t.Fatal("wrong identity reached parent archive")
	}
	if f.externalRequests != 0 {
		t.Fatal("verification fetched public trust material")
	}
}
