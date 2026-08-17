package resolver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/google/uuid"
	ocidigest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
)

type staticClientFactory struct{ client *http.Client }

func (f staticClientFactory) Client(NetworkPolicy, *tls.Config, string, Limits) (*http.Client, error) {
	return f.client, nil
}

type resolverFunc func(context.Context, Request, *http.Client) (Result, error)

func (f resolverFunc) Resolve(ctx context.Context, request Request, client *http.Client) (Result, error) {
	return f(ctx, request, client)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func unsignedSource(sourceType model.SourceType, rawURL string) model.Source {
	return model.Source{
		ID: uuid.New(), ProjectID: uuid.New(), Name: "fixture", Type: sourceType,
		URL: rawURL, AuthMode: model.AuthNone, Trust: model.TrustPolicy{AllowUnsigned: true},
	}
}

func TestServiceClearsCredentialsOnEveryExit(t *testing.T) {
	credential := &CredentialMaterial{Username: []byte("user"), Password: []byte("secret")}
	service := New(nil)
	service.clients = staticClientFactory{client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("must not be called")
	})}}
	service.resolvers[model.SourceGit] = resolverFunc(func(context.Context, Request, *http.Client) (Result, error) {
		return Result{}, &Error{Code: CodeAuthentication, Message: "fixture failure"}
	})
	_, err := service.Resolve(context.Background(), Request{
		Source:            unsignedSource(model.SourceGit, "https://example.com/repo.git"),
		RequestedRevision: "main", Credential: credential,
	})
	if !HasCode(err, CodeAuthentication) {
		t.Fatalf("Resolve() error = %v", err)
	}
	if credential.Username != nil || credential.Password != nil {
		t.Fatalf("credential material was retained: %#v", credential)
	}
}

func TestServiceRequiresVerifierWhenUnsignedIsForbidden(t *testing.T) {
	service := New(nil)
	service.clients = staticClientFactory{client: &http.Client{}}
	service.resolvers[model.SourceOCIArtifact] = resolverFunc(func(context.Context, Request, *http.Client) (Result, error) {
		digest := mustDigest(t, "sha256:"+strings.Repeat("a", 64))
		return Result{Revision: model.ImmutableRevision{Kind: model.RevisionOCIDigest, Value: digest.String(), ArtifactDigest: digest}}, nil
	})
	source := unsignedSource(model.SourceOCIArtifact, "oci://example.com/team/app")
	source.Trust = model.TrustPolicy{Provider: model.SignatureCosignKey, KeyRef: "release-key"}
	_, err := service.Resolve(context.Background(), Request{Source: source, RequestedRevision: "1.2.3"})
	if !HasCode(err, CodeVerification) {
		t.Fatalf("Resolve() error = %v, want verification failure", err)
	}
}

func TestServiceRejectsSSHUnlessOperatorEnablesIt(t *testing.T) {
	service := New(nil)
	source := unsignedSource(model.SourceGit, "ssh://git@git.corp.example/platform/config.git")
	source.AuthMode = model.AuthSSH
	credential := &CredentialMaterial{PrivateKey: []byte("not-used"), KnownHosts: []byte("not-used")}
	_, err := service.Resolve(context.Background(), Request{
		Source: source, RequestedRevision: "main", Credential: credential,
	})
	if !HasCode(err, CodeNetworkDenied) || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("Resolve() error = %v, want disabled SSH policy", err)
	}
	if credential.PrivateKey != nil || credential.KnownHosts != nil {
		t.Fatal("SSH credential was retained after policy rejection")
	}
}

func TestHelmHTTPResolverPinsDownloadedDigest(t *testing.T) {
	chart := []byte("bounded-chart-archive")
	sum := sha256.Sum256(chart)
	digest := hex.EncodeToString(sum[:])
	index := "apiVersion: v1\nentries:\n  widget:\n    - apiVersion: v2\n      name: widget\n      version: 1.2.3\n      digest: " + digest + "\n      urls: [widget-1.2.3.tgz]\ngenerated: 2026-08-17T00:00:00Z\n"
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body := chart
		if strings.HasSuffix(request.URL.Path, "/index.yaml") {
			body = []byte(index)
		}
		return &http.Response{
			StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body))),
			ContentLength: int64(len(body)), Request: request,
		}, nil
	})}
	result, err := (helmHTTPResolver{}).Resolve(context.Background(), Request{
		Source:            unsignedSource(model.SourceHelmHTTP, "https://example.com/stable"),
		RequestedRevision: "1.2.3", Chart: "widget", Limits: Limits{}.withDefaults(),
	}, client)
	if err != nil {
		t.Fatal(err)
	}
	if result.Revision.Value != "1.2.3" || result.Revision.ArtifactDigest.String() != "sha256:"+digest {
		t.Fatalf("unexpected immutable revision: %#v", result.Revision)
	}
	if string(result.verificationPayload) != string(chart) {
		t.Fatal("verifier did not receive the exact bounded chart bytes")
	}
}

func TestFetchBoundedRejectsStreamingOverflow(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("123456")), ContentLength: -1, Request: request}, nil
	})}
	_, err := fetchBounded(context.Background(), client, "https://example.com/blob", Request{Source: unsignedSource(model.SourceHelmHTTP, "https://example.com")}, 5)
	if !HasCode(err, CodeLimitExceeded) {
		t.Fatalf("fetchBounded() error = %v", err)
	}
}

func TestFetchCosignEvidenceUsesGuardedClientAndBindsSubjectDigest(t *testing.T) {
	subject := "sha256:" + strings.Repeat("a", 64)
	payload := []byte(`{"critical":{"type":"cosign container image signature","identity":{"docker-reference":"registry.example.test/team/app"},"image":{"Docker-manifest-digest":"` + subject + `"}},"optional":null}`)
	layer := ocispec.Descriptor{
		MediaType: cosignSimpleSigningMediaType,
		Digest:    ocidigest.FromBytes(payload),
		Size:      int64(len(payload)),
		Annotations: map[string]string{
			cosignSignatureAnnotation: base64.StdEncoding.EncodeToString([]byte("fixture-signature")),
		},
	}
	manifest, err := json.Marshal(ocispec.Manifest{Layers: []ocispec.Descriptor{layer}})
	if err != nil {
		t.Fatal(err)
	}
	manifestDigest := ocidigest.FromBytes(manifest)
	requests := make([]string, 0, 2)
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "registry.example.test" {
			t.Fatalf("signature fetch escaped the guarded registry client: %s", request.URL)
		}
		requests = append(requests, request.URL.Path)
		body := payload
		digestHeader := layer.Digest.String()
		contentType := cosignSimpleSigningMediaType
		if strings.Contains(request.URL.Path, "/manifests/sha256-") {
			body = manifest
			digestHeader = manifestDigest.String()
			contentType = ocispec.MediaTypeImageManifest
		}
		header := make(http.Header)
		header.Set("Docker-Content-Digest", digestHeader)
		header.Set("Content-Type", contentType)
		return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)), Request: request}, nil
	})}
	source := unsignedSource(model.SourceOCIArtifact, "oci://registry.example.test/team/app")
	source.Trust = model.TrustPolicy{Provider: model.SignatureCosignKey, KeyRef: "release"}
	evidence, err := fetchCosignEvidence(context.Background(), "registry.example.test/team/app", subject, Request{Source: source}, client)
	if err != nil {
		t.Fatal(err)
	}
	defer clearSignatureEvidence(evidence)
	if len(evidence) != 1 || !bytes.Equal(evidence[0].Payload, payload) {
		t.Fatalf("unexpected evidence: %#v", evidence)
	}
	if len(requests) != 2 || !strings.HasSuffix(requests[0], "/manifests/sha256-"+strings.Repeat("a", 64)+".sig") || !strings.Contains(requests[1], "/blobs/") {
		t.Fatalf("unexpected registry requests: %#v", requests)
	}
}

func TestFetchCosignEvidenceRejectsMismatchedSubjectClaim(t *testing.T) {
	expected := "sha256:" + strings.Repeat("a", 64)
	payload := []byte(`{"critical":{"type":"cosign container image signature","image":{"Docker-manifest-digest":"sha256:` + strings.Repeat("b", 64) + `"}}}`)
	layer := ocispec.Descriptor{MediaType: cosignSimpleSigningMediaType, Digest: ocidigest.FromBytes(payload), Size: int64(len(payload)), Annotations: map[string]string{cosignSignatureAnnotation: base64.StdEncoding.EncodeToString([]byte("signature"))}}
	manifest, _ := json.Marshal(ocispec.Manifest{Layers: []ocispec.Descriptor{layer}})
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, contentType := payload, cosignSimpleSigningMediaType
		if strings.Contains(request.URL.Path, "/manifests/") {
			body, contentType = manifest, ocispec.MediaTypeImageManifest
		}
		header := make(http.Header)
		header.Set("Docker-Content-Digest", ocidigest.FromBytes(body).String())
		header.Set("Content-Type", contentType)
		return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)), Request: request}, nil
	})}
	source := unsignedSource(model.SourceOCIArtifact, "oci://registry.example.test/team/app")
	source.Trust = model.TrustPolicy{Provider: model.SignatureCosignKey, KeyRef: "release"}
	if _, err := fetchCosignEvidence(context.Background(), "registry.example.test/team/app", expected, Request{Source: source}, client); !HasCode(err, CodeVerification) {
		t.Fatalf("mismatched signed subject was accepted: %v", err)
	}
}

func TestSelectGitCommitRejectsAmbiguousShortName(t *testing.T) {
	refs := map[string]plumbing.Hash{
		"refs/heads/release": plumbing.NewHash(strings.Repeat("a", 40)),
		"refs/tags/release":  plumbing.NewHash(strings.Repeat("b", 40)),
	}
	if _, err := selectGitCommit(refs, nil, "release"); !HasCode(err, CodeInvalidRequest) {
		t.Fatalf("selectGitCommit() error = %v", err)
	}
}

func TestNetworkPolicyPrivateDestinationsRequireHostAndCIDR(t *testing.T) {
	policy := NetworkPolicy{
		AllowedPrivateHosts: []string{"*.corp.example"},
		AllowedPrivateCIDRs: []netip.Prefix{netip.MustParsePrefix("10.42.0.0/16")},
	}
	if !policy.allows("git.corp.example", netip.MustParseAddr("10.42.1.7")) {
		t.Fatal("explicit enterprise host/CIDR pair was denied")
	}
	for _, test := range []struct {
		host string
		ip   string
	}{
		{"other.example", "10.42.1.7"},
		{"git.corp.example", "10.43.1.7"},
		{"git.corp.example", "127.0.0.1"},
		{"git.corp.example", "169.254.169.254"},
	} {
		if policy.allows(test.host, netip.MustParseAddr(test.ip)) {
			t.Fatalf("policy unexpectedly allowed %s at %s", test.host, test.ip)
		}
	}
}

func TestResolverErrorsNeverRetainUpstreamText(t *testing.T) {
	secret := "token-fixture-must-not-escape"
	err := sanitize(errors.New("upstream echoed "+secret), CodeUpstreamTemporary, true)
	if strings.Contains(err.Error(), secret) {
		t.Fatal("sanitized error retained upstream secret")
	}
}

func TestServiceProducesStableResolutionTime(t *testing.T) {
	fixed := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	service := New(nil)
	service.clients = staticClientFactory{client: &http.Client{}}
	service.now = func() time.Time { return fixed }
	service.resolvers[model.SourceOCIArtifact] = resolverFunc(func(context.Context, Request, *http.Client) (Result, error) {
		digest := mustDigest(t, "sha256:"+strings.Repeat("c", 64))
		return Result{Revision: model.ImmutableRevision{Kind: model.RevisionOCIDigest, Value: digest.String(), ArtifactDigest: digest}}, nil
	})
	result, err := service.Resolve(context.Background(), Request{Source: unsignedSource(model.SourceOCIArtifact, "oci://example.com/app"), RequestedRevision: "latest"})
	if err != nil {
		t.Fatal(err)
	}
	if result.ResolvedAt != fixed || result.Verification.Status != "unsigned" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func mustDigest(t *testing.T, value string) model.Digest {
	t.Helper()
	digest, err := model.ParseDigest(value)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}
