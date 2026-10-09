package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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
	unexpected int
}

func (transport *publicationRoutingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Host != "1.1.1.1" {
		transport.unexpected++
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
	binary := os.Getenv("COSIGN_BIN")
	if binary == "" {
		t.Skip("COSIGN_BIN selects the qualified actual producer")
	}
	catalog, charts, assets, platforms := producerFixture(t, "apiVersion: v1\nkind: Pod\nspec:\n  containers:\n  - image: {{ .Values.image }}\n")
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
	publicRequests := 0
	trap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		publicRequests++
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
	if publicRequests != 1 {
		t.Fatal("external trap negative control did not work")
	}
	publicRequests = 0
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
			if sha(fixture.manifests[repository+"/"+child.Digest]) != child.Digest {
				t.Fatal("child original subject digest changed")
			}
		}
	}
	if signed != 2*(len(index.Children)+1) || publicRequests != 0 || transport.unexpected != 0 {
		t.Fatalf("signed=%d public=%d unexpected=%d", signed, publicRequests, transport.unexpected)
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
