package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"oras.land/oras-go/v2/content/oci"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A bounded owned registry fixture exercises actual ORAS writes and selected
// Cosign signing. It is not a supplier registry or runtime qualification.
type publicationRegistry struct {
	mutex        sync.Mutex
	manifests    map[string][]byte
	blobs        map[string][]byte
	uploads      map[string][]byte
	uploadCount  int
	payloadReads int
	corruptBlob  string
}

type publicationRoutingTransport struct {
	base       http.RoundTripper
	target     *url.URL
	unexpected atomic.Int64
}

func (transport *publicationRoutingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Host != "1.1.1.1" {
		transport.unexpected.Add(1)
		return nil, errors.New("QA client refused unrelated destination")
	}
	clone := request.Clone(request.Context())
	copyURL := *request.URL
	clone.URL = &copyURL
	clone.URL.Scheme = transport.target.Scheme
	clone.URL.Host = transport.target.Host
	clone.Host = transport.target.Host
	response, err := transport.base.RoundTrip(clone)
	if response != nil {
		response.Request = request
	}
	return response, err
}

func TestActualPublisherCopiesSignsVerifiesAndRetriesImmutableChildren(t *testing.T) {
	actualPublisherFixture(t, false)
}
func TestActualPublisherClosesScannerDatabaseSubjectsAndOfflineGraph(t *testing.T) {
	actualPublisherFixture(t, true)
}

func actualPublisherFixture(t *testing.T, scanner bool) {
	t.Helper()
	binary := os.Getenv("COSIGN_BIN")
	if binary == "" {
		t.Skip("COSIGN_BIN selects the qualified actual producer")
	}
	catalog, charts, assets, platforms := producerFixture(t, "apiVersion: v1\nkind: Pod\nspec:\n  containers:\n  - image: {{ .Values.image }}\n")
	if scanner {
		configureScannerDatabaseFixture(t, catalog, assets)
	}
	output, err := produce(catalog, charts)
	if err != nil {
		t.Fatal(err)
	}
	layout := filepath.Join(t.TempDir(), "layout")
	index, err := buildLayout(output, charts, layout, "1.1.1.1/distribution", assets, platforms)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &publicationRegistry{manifests: map[string][]byte{}, blobs: map[string][]byte{}}
	server := httptest.NewServer(fixture)
	defer server.Close()
	target, _ := url.Parse(server.URL)
	transport := &publicationRoutingTransport{base: server.Client().Transport, target: target}
	client := &http.Client{Transport: transport, Timeout: 20 * time.Second}
	directory := t.TempDir()
	random := make([]byte, 24)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", directory)
	t.Setenv("DOCKER_CONFIG", directory)
	t.Setenv("COSIGN_PASSWORD", hex.EncodeToString(random))
	var publicRequests atomic.Int64
	trap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		publicRequests.Add(1)
		w.WriteHeader(http.StatusForbidden)
	}))
	defer trap.Close()
	proxy, _ := url.Parse(trap.URL)
	control := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxy)}, Timeout: 2 * time.Second}
	response, err := control.Get("http://public.qa.example.test/negative-control")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if publicRequests.Load() != 1 {
		t.Fatal("external trap negative control did not work")
	}
	publicRequests.Store(0)
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		t.Setenv(name, trap.URL)
	}
	t.Setenv("NO_PROXY", "127.0.0.1,localhost")
	t.Setenv("no_proxy", "127.0.0.1,localhost")
	private := filepath.Join(directory, "qa.key")
	if err := exec.Command(binary, "generate-key-pair", "--output-key-prefix", filepath.Join(directory, "qa")).Run(); err != nil {
		t.Fatal("QA key generation failed")
	}
	options := publicationOptions{Directory: layout, ExpectedRelease: index.Release, Mode: "qa", CosignBinary: binary, QAKeyFile: private, TrustDirectory: directory, Trust: model.TrustPolicy{Provider: model.SignatureCosignKey, KeyRef: "qa"}, Client: client}
	signed := 0
	options.sign = func(ctx context.Context, reference string) error {
		signed++
		reference = strings.Replace(reference, "1.1.1.1", target.Host, 1)
		command := exec.CommandContext(ctx, binary, "sign", "--yes", "--new-bundle-format=false", "--registry-referrers-mode=legacy", "--use-signing-config=false", "--key", private, "--tlog-upload=false", "--allow-http-registry", reference)
		if err := command.Run(); err != nil {
			return errors.New("QA actual signer failed")
		}
		return nil
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := publishLayout(t.Context(), options); err != nil {
			t.Fatalf("actual producer attempt %d: %v", attempt, err)
		}
		for _, child := range index.Children {
			repository := strings.TrimPrefix(child.Repository, "1.1.1.1/")
			if fixture.manifestDigest(repository+"/"+child.Digest) != child.Digest {
				t.Fatal("child original subject digest changed")
			}
		}
	}
	if signed != 2*(len(index.Children)+len(index.Assets)+1) || publicRequests.Load() != 0 || transport.unexpected.Load() != 0 {
		t.Fatalf("signed=%d public=%d unexpected=%d", signed, publicRequests.Load(), transport.unexpected.Load())
	}
	verifyActualKeyedConsumer(t, index, client, directory)
	verifyActualOfflinePublication(t, fixture, index, options.Trust, directory)
	offlineOptions := distribution.OfflineOptions{Repository: index.Repository, Digest: index.Digest, CatalogDigest: index.CatalogDigest, Release: index.Release, Profile: "builtins", TrustDirectory: directory, Policy: options.Trust}
	transfer, err := captureOfflineTransfer(t.Context(), layout, offlineOptions, client, options.Network)
	if err != nil {
		t.Fatalf("actual authenticated registry capture failed: %v", err)
	}
	if _, _, err := readOfflineTransfer(t.Context(), transfer, offlineOptions); err != nil {
		t.Fatalf("captured actual graph cannot verify offline: %v", err)
	}
	if publicRequests.Load() != 0 {
		t.Fatal("offline verification contacted external proxy")
	}
	if scanner {
		asset := index.Assets[0]
		key := strings.TrimPrefix(asset.Repository, "1.1.1.1/") + "/sha256-" + asset.Digest[7:] + ".sig"
		manifests, _ := fixture.snapshot()
		fixture.mutex.Lock()
		delete(fixture.manifests, key)
		fixture.mutex.Unlock()
		unsigned := options
		unsigned.sign = func(context.Context, string) error { return nil }
		if err := publishLayout(t.Context(), unsigned); err == nil {
			t.Fatal("signed parent authorized unsigned database child")
		}
		fixture.mutex.Lock()
		fixture.manifests[key] = manifests[key]
		fixture.mutex.Unlock()
	}
	// Successful write/sign cannot cover a missing vendor signature after import.
	fixture.mutex.Lock()
	for name := range fixture.manifests {
		if strings.Contains(name, "/charts/") && strings.HasSuffix(name, ".sig") {
			delete(fixture.manifests, name)
		}
	}
	fixture.mutex.Unlock()
	options.sign = func(context.Context, string) error { return nil }
	if err := publishLayout(t.Context(), options); err == nil {
		t.Fatal("unsigned child reuse reported successful publication")
	}
	options.Mode = "production"
	if err := publishLayout(t.Context(), options); err == nil {
		t.Fatal("QA key masqueraded as production vendor publisher")
	}
}

func verifyActualOfflinePublication(t *testing.T, fixture *publicationRegistry, index subjectIndex, policy model.TrustPolicy, trustDirectory string) {
	t.Helper()
	manifests, registryBlobs := fixture.snapshot()
	var stored []distribution.StoredSubject
	graph := map[string][]byte{}
	subjects := append([]publishedSubject{}, index.Children...)
	for _, asset := range index.Assets {
		subjects = append(subjects, publishedSubject{Repository: asset.Repository, Digest: asset.Digest})
	}
	subjects = append(subjects, publishedSubject{Repository: index.Repository, Digest: index.Digest})
	for _, subject := range subjects {
		repository := strings.TrimPrefix(subject.Repository, "1.1.1.1/")
		signature := manifests[repository+"/sha256-"+subject.Digest[7:]+".sig"]
		stored = append(stored, distribution.StoredSubject{Repository: subject.Repository, Digest: subject.Digest, SignatureDigest: sha(signature)})
		for _, data := range [][]byte{manifests[repository+"/"+subject.Digest], signature} {
			var manifest ocispec.Manifest
			if closedJSON(data, &manifest) != nil {
				t.Fatal("actual registry manifest invalid")
			}
			graph[sha(data)] = data
			graph[manifest.Config.Digest.String()] = registryBlobs[manifest.Config.Digest.String()]
			for _, layer := range manifest.Layers {
				graph[layer.Digest.String()] = registryBlobs[layer.Digest.String()]
			}
		}
	}
	options := distribution.OfflineOptions{Repository: index.Repository, Digest: index.Digest, CatalogDigest: index.CatalogDigest, Release: index.Release, Profile: "builtins", TrustDirectory: trustDirectory, Policy: policy}
	catalog, status, err := distribution.VerifyOfflineStore(t.Context(), options, stored, graph)
	if err != nil || status.Verification != "verified" || len(catalog.Components) != len(index.Children) {
		t.Fatalf("actual offline graph authentication failed: %v", err)
	}
	if len(index.Assets) > 0 {
		for _, component := range catalog.Components {
			if component.Source.Chart != "trivy-operator" {
				continue
			}
			trivy := component.Values["trivy"].(map[string]any)
			for _, asset := range index.Assets {
				registryKey, repositoryKey := "dbRegistry", "dbRepository"
				if asset.ID == "trivy-java-database" {
					registryKey, repositoryKey = "javaDbRegistry", "javaDbRepository"
				}
				if trivy[registryKey].(string)+"/"+trivy[repositoryKey].(string) != asset.Repository+"@"+asset.Digest {
					t.Fatal("downstream database fetch retained supplier location instead of exact endorsed subject")
				}
			}
		}
	}
	transfer, err := packOfflineTransfer(t.Context(), options, stored, graph)
	if err != nil {
		t.Fatal(err)
	}
	assertOfflineTransferRejectsUnsafeMembers(t, transfer, options)
	archive := filepath.Join(t.TempDir(), "transfer.tar.gz")
	if err := os.WriteFile(archive, transfer, 0600); err != nil {
		t.Fatal(err)
	}
	imported := filepath.Join(t.TempDir(), "verified-import")
	if err := importOfflineTransfer(t.Context(), archive, imported, options); err != nil {
		t.Fatalf("actual signed graph import: %v", err)
	}
	store, err := oci.New(imported)
	if err != nil {
		t.Fatal("verified import is not a standard OCI store")
	}
	for _, subject := range stored {
		if descriptor, err := store.Resolve(t.Context(), subject.Digest); err != nil || descriptor.Digest.String() != subject.Digest {
			t.Fatalf("import lost exact original OCI subject: %v", err)
		}
		if descriptor, err := store.Resolve(t.Context(), subject.SignatureDigest); err != nil || descriptor.Digest.String() != subject.SignatureDigest {
			t.Fatalf("import lost exact original legacy signature manifest: %v", err)
		}
		original := graph[subject.SignatureDigest]
		copied, err := os.ReadFile(filepath.Join(imported, "blobs", "sha256", subject.SignatureDigest[7:]))
		if err != nil || string(copied) != string(original) {
			t.Fatal("import erased or replaced original vendor signature bytes")
		}
	}
	wrongRelease := options
	wrongRelease.Release = "v99.99.99"
	denied := filepath.Join(t.TempDir(), "denied-import")
	if err := importOfflineTransfer(t.Context(), archive, denied, wrongRelease); err == nil {
		t.Fatal("transfer bypassed caller release pin")
	}
	if _, err := os.Stat(denied); !os.IsNotExist(err) {
		t.Fatal("unverified transfer wrote destination")
	}
	for _, mutation := range []string{"missing-config", "changed-blob", "orphan-blob", "extra-subject", "wrong-key", "tampered-signature", "duplicate-json"} {
		t.Run("offline-"+mutation, func(t *testing.T) {
			changed := map[string][]byte{}
			for digest, data := range graph {
				changed[digest] = append([]byte(nil), data...)
			}
			subjects := append([]distribution.StoredSubject(nil), stored...)
			changedOptions := options
			var manifest ocispec.Manifest
			if closedJSON(changed[subjects[0].Digest], &manifest) != nil {
				t.Fatal("fixture missing manifest")
			}
			switch mutation {
			case "missing-config":
				delete(changed, manifest.Config.Digest.String())
			case "changed-blob":
				changed[manifest.Layers[0].Digest.String()] = []byte("changed payload")
			case "orphan-blob":
				changed[sha([]byte("orphan"))] = []byte("orphan")
			case "extra-subject":
				extra := subjects[0]
				extra.Repository += "/unconsumed"
				subjects = append(subjects, extra)
			case "wrong-key":
				changedOptions.Policy.KeyRef = "not-mounted"
			case "tampered-signature", "duplicate-json":
				original := subjects[0].SignatureDigest
				var signature ocispec.Manifest
				if closedJSON(changed[original], &signature) != nil {
					t.Fatal("fixture missing signature")
				}
				signature.Layers[0].Annotations["dev.cosignproject.cosign/signature"] = "AA=="
				data, err := json.Marshal(signature)
				if err != nil {
					t.Fatal(err)
				}
				if mutation == "duplicate-json" {
					data = append([]byte(`{"schemaVersion":2,`), data[1:]...)
				}
				delete(changed, original)
				subjects[0].SignatureDigest = sha(data)
				changed[sha(data)] = data
			}
			if _, _, err := distribution.VerifyOfflineStore(t.Context(), changedOptions, subjects, changed); err == nil {
				t.Fatal("changed/unclosed offline graph authenticated")
			}
		})
	}
}
func (f *publicationRegistry) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
			if sha(f.uploads[uploadID]) != subject {
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
			f.manifests[repository+"/"+sha(content)] = content
			w.Header().Set("Docker-Content-Digest", sha(content))
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
	w.Header().Set("Docker-Content-Digest", sha(data))
	w.Header().Set("Content-Length", fmt.Sprint(len(data)))
	if r.Method != "HEAD" {
		_, _ = w.Write(data)
	}
}

func (f *publicationRegistry) manifestDigest(key string) string {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return sha(f.manifests[key])
}
func (f *publicationRegistry) snapshot() (map[string][]byte, map[string][]byte) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	manifests, blobs := map[string][]byte{}, map[string][]byte{}
	for key, data := range f.manifests {
		manifests[key] = append([]byte(nil), data...)
	}
	for key, data := range f.blobs {
		blobs[key] = append([]byte(nil), data...)
	}
	return manifests, blobs
}
