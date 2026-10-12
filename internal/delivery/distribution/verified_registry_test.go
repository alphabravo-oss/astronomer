package distribution

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	agentdelivery "github.com/alphabravocompany/astronomer-go/internal/agent/delivery"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/google/uuid"
	digest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sigstore/sigstore/pkg/cryptoutils"
	"github.com/sigstore/sigstore/pkg/signature"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type registryFixture struct {
	mutex            sync.Mutex
	manifests        map[string][]byte
	blobs            map[string][]byte
	payloadReads     int
	externalRequests int
	corruptBlob      string
	uploads          map[string][]byte
	uploadCount      int
}

func (f *registryFixture) addArtifact(repository, media string, payload []byte) string {
	d := digest.FromBytes(payload)
	manifest := ocispec.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageManifest, Config: ocispec.Descriptor{MediaType: "application/vnd.oci.empty.v1+json", Digest: digest.FromString("{}"), Size: 2}, Layers: []ocispec.Descriptor{{MediaType: media, Digest: d, Size: int64(len(payload))}}}
	data, _ := json.Marshal(manifest)
	subject := contentDigest(data)
	f.mutex.Lock()
	f.blobs[d.String()] = payload
	f.manifests[repository+"/"+subject] = data
	f.mutex.Unlock()
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
	manifest := ocispec.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageManifest, Layers: []ocispec.Descriptor{{MediaType: "application/vnd.dev.cosign.simplesigning.v1+json", Digest: d, Size: int64(len(payload)), Annotations: map[string]string{"dev.cosignproject.cosign/signature": base64.StdEncoding.EncodeToString(raw)}}}}
	data, _ := json.Marshal(manifest)
	f.mutex.Lock()
	f.blobs[d.String()] = payload
	f.manifests[repository+"/sha256-"+strings.TrimPrefix(subject, "sha256:")+".sig"] = data
	f.mutex.Unlock()
}
func (f *registryFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Actual Cosign may upload a config/blob while another registry request
	// resolves a manifest. The owned test registry must serialize its maps.
	f.mutex.Lock()
	defer f.mutex.Unlock()
	if r.URL.Path == "/v2/" {
		w.WriteHeader(200)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/v2/")
	if repository, uploadID, found := strings.Cut(path, "/blobs/uploads/"); found {
		if f.uploads == nil {
			f.uploads = map[string][]byte{}
		}
		if r.Method == "POST" {
			f.uploadCount++
			id := fmt.Sprint(f.uploadCount)
			f.uploads[id] = nil
			w.Header().Set("Location", "/v2/"+repository+"/blobs/uploads/"+id)
			w.Header().Set("Docker-Upload-UUID", id)
			w.WriteHeader(http.StatusAccepted)
			return
		}
		content, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
		if err != nil {
			w.WriteHeader(400)
			return
		}
		f.uploads[uploadID] = append(f.uploads[uploadID], content...)
		if r.Method == "PATCH" {
			w.Header().Set("Location", r.URL.Path)
			w.WriteHeader(http.StatusAccepted)
			return
		}
		if r.Method == "PUT" {
			subject := r.URL.Query().Get("digest")
			if contentDigest(f.uploads[uploadID]) != subject {
				w.WriteHeader(400)
				return
			}
			f.blobs[subject] = f.uploads[uploadID]
			delete(f.uploads, uploadID)
			w.Header().Set("Location", "/v2/"+repository+"/blobs/"+subject)
			w.Header().Set("Docker-Content-Digest", subject)
			w.WriteHeader(http.StatusCreated)
			return
		}
	}
	var data []byte
	if repository, reference, found := strings.Cut(path, "/manifests/"); found {
		if r.Method == "PUT" {
			content, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			if err != nil {
				w.WriteHeader(400)
				return
			}
			f.manifests[repository+"/"+reference] = content
			f.manifests[repository+"/"+contentDigest(content)] = content
			w.Header().Set("Docker-Content-Digest", contentDigest(content))
			w.Header().Set("Location", r.URL.Path)
			w.WriteHeader(http.StatusCreated)
			return
		}

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
		r.fixture.recordExternal()
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
	if len(catalog.Components) != 3 || loader.Status().Verification != "verified" || f.externalCount() != 0 {
		t.Fatal("QA signed chain incomplete or contacted external trust")
	}
	for _, component := range catalog.Components {
		if component.Source.Kind != "helm_oci" || component.Source.OriginalURL == "" || component.Source.OriginalArchiveDigest == "" || component.Source.ChartDigest == component.Source.OriginalArchiveDigest {
			t.Fatal("child content identity was not projected")
		}
	}
	reads := f.readCount()
	if _, err := loader.Load(t.Context()); err != nil || f.readCount() != reads {
		t.Fatal("validated source cache refetched")
	}
	// A valid parent must not authorize a missing child signature.
	var childRepository, signatureTag string
	for _, name := range f.manifestKeys() {
		if strings.HasPrefix(name, "charts/") && strings.HasSuffix(name, ".sig") {
			childRepository, signatureTag, _ = strings.Cut(name, "/sha256-")
			break
		}
	}
	missingKey := childRepository + "/sha256-" + signatureTag
	saved := f.removeManifest(missingKey)
	fresh, _ := New(options)
	if _, err := fresh.Load(t.Context()); err == nil || fresh.cached != nil {
		t.Fatal("parent signature authorized unsigned child")
	}
	f.setManifest(missingKey, saved)
	// Changed archive bytes at an identical chart version/manifest cannot be consumed.
	f.setCorrupt(contentDigest(chartFixture(t, catalog.Components[0].Slug)))
	fresh, _ = New(options)
	if _, err := fresh.Load(t.Context()); err == nil || fresh.cached != nil {
		t.Fatal("changed child payload accepted")
	}
	f.setCorrupt("")
	// Wrong mounted QA key rejects the signature before reading parent payload.
	other, _, err := signature.NewDefaultECDSASignerVerifier()
	if err != nil {
		t.Fatal(err)
	}
	f.sign(t, "distribution", subject, other)
	before := f.readCount()
	fresh, _ = New(options)
	if _, err := fresh.Load(t.Context()); err == nil || fresh.cached != nil || f.readCount() != before+1 {
		t.Fatal("wrong identity reached parent archive")
	}
	if f.externalCount() != 0 {
		t.Fatal("verification fetched public trust material")
	}
}

// Explicit opt-in executes the selected Cosign binary against a local registry,
// while an external proxy trap proves offline QA signing needs no public service.
func TestSelectedCosignProducerToVerifiedLoader(t *testing.T) {
	binary := os.Getenv("COSIGN_BIN")
	if binary == "" {
		t.Skip("COSIGN_BIN must select the qualified producer binary")
	}
	f := &registryFixture{manifests: map[string][]byte{}, blobs: map[string][]byte{}}
	parent, release, catalogDigest := archiveFixture(t, nil, func(bom *BOM, members map[string][]byte) {
		for index := range bom.Charts {
			subject := &bom.Charts[index]
			subject.Repository = "1.1.1.1/charts"
			subject.Digest = f.addArtifact("charts/"+subject.Component, "application/vnd.cncf.helm.chart.content.v1.tar+gzip", members["charts/"+subject.Component+".tgz"])
		}
	})
	subject := f.addArtifact("distribution", MediaType, parent)
	f.setBlob(digest.FromString("{}").String(), []byte("{}"))
	server := httptest.NewServer(f)
	defer server.Close()
	var externalRequests atomic.Int64
	trap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		externalRequests.Add(1)
		w.WriteHeader(http.StatusForbidden)
	}))
	defer trap.Close()
	temporary := t.TempDir()
	random := make([]byte, 24)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	password := hex.EncodeToString(random)
	environment := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + temporary, "DOCKER_CONFIG=" + temporary, "COSIGN_PASSWORD=" + password, "HTTP_PROXY=" + trap.URL, "HTTPS_PROXY=" + trap.URL, "ALL_PROXY=" + trap.URL, "NO_PROXY=127.0.0.1,localhost"}
	command := exec.Command(binary, "generate-key-pair", "--output-key-prefix", filepath.Join(temporary, "qa"))
	command.Env = environment
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("QA key generation failed: %v %s", err, output)
	}
	version := exec.Command(binary, "version")
	version.Env = environment
	versionBytes, err := version.CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("selected producer version: %s", versionBytes)
	keys := f.manifestKeys()
	for _, key := range keys {
		repository, subject, found := strings.Cut(key, "/sha256:")
		if !found {
			t.Fatal("fixture subject malformed")
		}
		reference := strings.TrimPrefix(server.URL, "http://") + "/" + repository + "@sha256:" + subject
		command = exec.Command(binary, "sign", "--yes", "--key", filepath.Join(temporary, "qa.key"), "--new-bundle-format=false", "--registry-referrers-mode=legacy", "--tlog-upload=false", "--use-signing-config=false", "--allow-http-registry", reference)
		command.Env = environment
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("selected QA producer failed: %v %s", err, output)
		}
	}
	serverURL, _ := url.Parse(server.URL)
	client := &http.Client{Transport: registryRoutingTransport{fixture: f, target: serverURL.Host, base: server.Client().Transport}}
	loader, err := New(Options{Repository: "1.1.1.1/distribution", Digest: subject, Release: release, CatalogDigest: catalogDigest, Profile: "builtins", Policy: model.TrustPolicy{Provider: model.SignatureCosignKey, KeyRef: "qa"}, TrustDirectory: temporary, Client: client})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := loader.Load(t.Context())
	if err != nil || len(catalog.Components) != 3 || loader.Status().Verification != "verified" {
		t.Fatalf("selected CLI producer did not qualify in-process loader: %v", err)
	}
	public, err := os.ReadFile(filepath.Join(temporary, "qa.pub"))
	if err != nil {
		t.Fatal(err)
	}
	for _, component := range catalog.Components {
		projectID, deploymentID := uuid.NewString(), uuid.NewString()
		names := agentdelivery.Names(projectID, deploymentID)
		values, _ := json.Marshal(component.Values)
		assignment := protocol.DeliveryAssignmentV2{DeploymentID: deploymentID, TargetID: uuid.NewString(), ProjectID: projectID, Generation: 1, SpecDigest: component.Source.ChartDigest, Action: protocol.DeliveryActionApply, Scope: protocol.DeliveryScopePlatform,
			Source:   protocol.DeliverySourceV2{Kind: protocol.DeliverySourceHelmOCI, URL: component.Source.URL, Revision: component.Source.Version, Digest: component.Source.ChartDigest, Chart: component.Source.Chart, TrustSecret: names.TrustSecret, Verify: &protocol.DeliveryVerifyV2{Provider: "cosign"}},
			Renderer: protocol.DeliveryRendererV2{Kind: protocol.DeliveryRendererHelm, Helm: &protocol.DeliveryHelmRenderer{Chart: component.Source.Chart, Version: component.Source.Version, ReleaseName: component.ReleaseName, TargetNamespace: component.TargetNamespace, ServiceAccount: names.Applier, Values: values, InstallRetries: 3, UpgradeRetries: 3, UpgradeRemediation: "rollback", DriftMode: "enabled"}},
			Policy:   protocol.DeliveryReconciliationPolicy{Interval: "5m", RetryInterval: "30s", Timeout: "10m", Prune: true, Drift: "enabled"}, Credential: &protocol.DeliveryCredentialMaterial{Version: 1, Data: map[string][]byte{"trust.cosign.pub": public}}}
		capabilities := agentdelivery.Capabilities{SourceKinds: []protocol.DeliverySourceKind{protocol.DeliverySourceHelmOCI}, RendererKinds: []protocol.DeliveryRendererKind{protocol.DeliveryRendererHelm}, FluxAPIVersions: []string{"source.toolkit.fluxcd.io/v1", "helm.toolkit.fluxcd.io/v2"}, PlatformScope: true, NoCrossNamespaceRefs: true, NoRemoteKustomizeBases: true}
		materialized, err := agentdelivery.BuildAssignment(assignment, capabilities, agentdelivery.ValidationPolicy{AllowPlatformScope: true})
		if err != nil {
			t.Fatal(err)
		}
		source := materialized.Objects[len(materialized.Objects)-2]
		release := materialized.Objects[len(materialized.Objects)-1]
		if got, _, _ := unstructured.NestedString(source.Object, "spec", "ref", "digest"); got != component.Source.ChartDigest {
			t.Fatal("actual agent materialization refetched a mutable chart version")
		}
		if got, _, _ := unstructured.NestedString(source.Object, "spec", "url"); got != component.Source.URL+"/"+component.Source.Chart {
			t.Fatal("agent consumed a different child repository")
		}
		if got, _, _ := unstructured.NestedString(source.Object, "spec", "verify", "provider"); got != "cosign" {
			t.Fatal("agent lost mandatory child signature policy")
		}
		if got, _, _ := unstructured.NestedString(release.Object, "spec", "chartRef", "kind"); got != "OCIRepository" {
			t.Fatal("agent used an HTTP Helm chart refetch")
		}
		for _, capability := range component.Requirements.Capabilities {
			if capability == "delivery.source.helm_http" {
				t.Fatal("projected requirements still permit HTTP-only agent")
			}
		}
		capabilities.SourceKinds = []protocol.DeliverySourceKind{protocol.DeliverySourceHelmHTTP}
		if _, err := agentdelivery.BuildAssignment(assignment, capabilities, agentdelivery.ValidationPolicy{AllowPlatformScope: true}); err == nil {
			t.Fatal("HTTP-only agent accepted immutable OCI source")
		}
	}
	if externalRequests.Load() != 0 || f.externalCount() != 0 {
		t.Fatalf("QA producer/verifier attempted external service: %d", externalRequests.Load())
	}
}

func (f *registryFixture) manifestKeys() []string {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	keys := make([]string, 0, len(f.manifests))
	for key := range f.manifests {
		keys = append(keys, key)
	}
	return keys
}
func (f *registryFixture) readCount() int {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return f.payloadReads
}
func (f *registryFixture) externalCount() int {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return f.externalRequests
}
func (f *registryFixture) recordExternal() {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.externalRequests++
}
func (f *registryFixture) setCorrupt(value string) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.corruptBlob = value
}
func (f *registryFixture) setManifest(key string, value []byte) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.manifests[key] = bytes.Clone(value)
}
func (f *registryFixture) setBlob(key string, value []byte) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.blobs[key] = bytes.Clone(value)
}
func (f *registryFixture) removeManifest(key string) []byte {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	data := bytes.Clone(f.manifests[key])
	delete(f.manifests, key)
	return data
}
