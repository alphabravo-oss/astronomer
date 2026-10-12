//go:build unix

package resolver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	digest "github.com/opencontainers/go-digest"
)

// This is actual canonical TLS/challenge/range qualification when executed,
// not an injected RoundTripper or proof of a Kubernetes scanner using bytes.
func TestWorkloadPrivateContentUsesMountedPullWithoutGivingScannerCredentials(t *testing.T) {
	manifest, config, layer := workloadMetadataFixture(t)
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	subject := digest.FromBytes(manifestBytes).String()
	var origin atomic.Value
	var tokens, requests atomic.Int64
	peer, roots, address := privateRegistryTLS(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.URL.Path == "/token" {
			tokens.Add(1)
			user, password, ok := request.BasicAuth()
			if !ok || user != "owned-read-user" || password != "owned-read-password" || request.URL.Query().Get("service") != "workload-service" || request.URL.Query().Get("scope") != "repository:workload/app:pull" {
				writer.WriteHeader(403)
				return
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"token":"owned-workload-read-token"}`))
			return
		}
		if request.Header.Get("Authorization") != "Bearer owned-workload-read-token" {
			writer.Header().Set("WWW-Authenticate", `Bearer realm="`+origin.Load().(string)+`/token",service="workload-service",scope="repository:workload/app:pull"`)
			writer.WriteHeader(401)
			return
		}
		var body []byte
		media := manifest.MediaType
		switch request.URL.Path {
		case "/v2/workload/app/manifests/" + subject:
			body = manifestBytes
		case "/v2/workload/app/blobs/" + manifest.Config.Digest.String():
			body = config
			media = manifest.Config.MediaType
		case "/v2/workload/app/blobs/" + manifest.Layers[0].Digest.String():
			if request.Header.Get("Range") != fmt.Sprintf("bytes=0-%d", len(layer)-1) || request.Header.Get("Accept-Encoding") != "identity" {
				writer.WriteHeader(400)
				return
			}
			writer.Header().Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", len(layer)-1, len(layer)))
			writer.Header().Set("Content-Length", fmt.Sprint(len(layer)))
			writer.WriteHeader(206)
			_, _ = writer.Write(layer)
			return
		default:
			writer.WriteHeader(404)
			return
		}
		writer.Header().Set("Content-Type", media)
		writer.Header().Set("Docker-Content-Digest", digest.FromBytes(body).String())
		writer.Header().Set("Content-Length", fmt.Sprint(len(body)))
		_, _ = writer.Write(body)
	}))
	origin.Store(peer.URL)
	endpoint, err := url.Parse(peer.URL)
	if err != nil {
		t.Fatal(err)
	}
	mounted := t.TempDir()
	if err := os.WriteFile(filepath.Join(mounted, "registry.pem"), roots, 0600); err != nil {
		t.Fatal(err)
	}
	writeMountedReadFixture(t, mounted, "owned-read-password")
	repository := endpoint.Host + "/workload/app"
	options := DatabaseRegistryTrustOptions{TrustDirectory: mounted, RootsRef: "registry", Network: httpclient.DestinationPolicy{Restricted: true, Origins: []httpclient.TrustedOrigin{{URL: peer.URL, CIDRs: []netip.Prefix{netip.PrefixFrom(address, 32)}}}}, Read: &DatabaseRegistryReadOptions{CredentialRef: "pull", Registry: endpoint.Host, RealmURL: peer.URL + "/token", Service: "workload-service", Repositories: []string{repository}}}
	owner, err := CaptureDatabaseRegistryTransport(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	captured, err := FetchWorkloadImageMetadata(t.Context(), owner.Client(), repository, subject, "amd64")
	if err != nil {
		t.Fatal("actual private immutable metadata unavailable")
	}
	var output strings.Builder
	if err := StreamWorkloadImageLayer(t.Context(), owner.Client(), repository, captured.Layers[0], &output); err != nil || output.String() != string(layer) {
		t.Fatal("actual private immutable layer unavailable")
	}
	if tokens.Load() != 1 {
		t.Fatal("exact repository did not retain one scoped token cache")
	}
	before := requests.Load()
	if _, err := FetchWorkloadImageMetadata(t.Context(), owner.Client(), endpoint.Host+"/workload/foreign", subject, "amd64"); err == nil || requests.Load() != before {
		t.Fatal("workload request granted a new repository")
	}
	writeMountedReadFixture(t, mounted, "rotated-password")
	if err := StreamWorkloadImageLayer(t.Context(), owner.Client(), repository, captured.Layers[0], nil); err == nil || requests.Load() != before {
		t.Fatal("workload layer ignored mounted pull rotation")
	}
	// No output/config/record exposes the captured read credential to a scanner.
	if strings.Contains(output.String(), "owned-read-password") {
		t.Fatal("private credential entered workload bytes")
	}
}
