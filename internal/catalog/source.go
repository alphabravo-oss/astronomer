package catalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"
	remoteauth "oras.land/oras-go/v2/registry/remote/auth"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

const maxCatalogBytes int64 = 4 << 20

// SourceClients retains the historical source/mirror interface. Both clients
// enforce the same fixed operator authority; a mirror URL cannot grant private
// access or enable public fallback in a restricted composition.
type SourceClients struct {
	Public *http.Client
	Mirror *http.Client
}

type SourceClientOptions struct {
	Timeout             time.Duration
	ProxyURL            string
	CAFile              string
	DestinationPolicy   *httpclient.DestinationPolicy
	AllowPrivateMirrors bool
}

type SourceOptions struct {
	URL            string
	ExpectedDigest string
	MirrorsJSON    string
	Clients        SourceClients
	// TrustPolicy enables mandatory in-process cosign verification for OCI
	// catalogs. Nil preserves digest-only operation for explicitly unsigned
	// development catalogs; a configured policy fails closed.
	TrustPolicy    *model.TrustPolicy
	TrustDirectory string
}

// NewSourceClients creates bounded, DNS-rebinding-safe clients with an
// optional Secret-sourced proxy and private CA bundle.
func NewSourceClients(options SourceClientOptions) (SourceClients, error) {
	tlsConfig, err := httpclient.TLSForCA(options.CAFile)
	if err != nil {
		return SourceClients{}, err
	}
	policy := httpclient.DestinationPolicy{}
	if options.DestinationPolicy != nil {
		policy = *options.DestinationPolicy
	}
	if options.AllowPrivateMirrors && len(policy.Origins) == 0 {
		return SourceClients{}, errors.New("private catalog mirrors require exact operator origins and CIDRs")
	}
	client, err := httpclient.NewDestinationClient(policy, httpclient.DestinationOptions{AllowPublicCrossOriginRedirects: !policy.Restricted, SensitiveHeaders: []string{"Private-Token", "X-Gitlab-Token", "X-Auth-Token", "X-Api-Key"}, TLSConfig: tlsConfig, Timeout: options.Timeout, MaxResponseBytes: maxCatalogBytes, ProxyURL: options.ProxyURL})
	if err != nil {
		return SourceClients{}, err
	}
	return SourceClients{Public: client, Mirror: client}, nil
}

type mirrorRule struct {
	source string
	target string
}

func parseMirrorRules(raw string) ([]mirrorRule, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var mappings map[string]string
	if err := json.Unmarshal([]byte(raw), &mappings); err != nil {
		return nil, fmt.Errorf("decode catalog mirrors: %w", err)
	}
	rules := make([]mirrorRule, 0, len(mappings))
	for source, target := range mappings {
		source = strings.TrimRight(strings.TrimSpace(source), "/")
		target = strings.TrimRight(strings.TrimSpace(target), "/")
		if err := validateMirrorEndpoint(source); err != nil {
			return nil, fmt.Errorf("catalog mirror source: %w", err)
		}
		if err := validateMirrorEndpoint(target); err != nil {
			return nil, fmt.Errorf("catalog mirror target: %w", err)
		}
		if strings.HasPrefix(source, "oci://") != strings.HasPrefix(target, "oci://") {
			return nil, errors.New("catalog mirror source and target must use the same transport")
		}
		rules = append(rules, mirrorRule{source: source, target: target})
	}
	sort.Slice(rules, func(i, j int) bool { return len(rules[i].source) > len(rules[j].source) })
	return rules, nil
}

func validateMirrorEndpoint(value string) error {
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "https" && u.Scheme != "oci") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("%q must be an absolute HTTPS or OCI URL without credentials, query, or fragment", value)
	}
	return nil
}

func applyMirror(source string, rules []mirrorRule) (string, bool) {
	for _, rule := range rules {
		if source == rule.source || strings.HasPrefix(source, rule.source+"/") || strings.HasPrefix(source, rule.source+"@") {
			return rule.target + strings.TrimPrefix(source, rule.source), true
		}
	}
	return source, false
}

func fetchCatalogSource(ctx context.Context, options SourceOptions) ([]byte, string, string, string, error) {
	rules, err := parseMirrorRules(options.MirrorsJSON)
	if err != nil {
		return nil, "", "", "", err
	}
	effective, mirrored := applyMirror(strings.TrimSpace(options.URL), rules)
	if options.TrustPolicy != nil {
		if err := options.TrustPolicy.Validate(); err != nil || options.TrustPolicy.AllowUnsigned {
			return nil, "", "", "", errors.New("signed catalog trust policy is invalid")
		}
		if !strings.HasPrefix(effective, "oci://") {
			return nil, "", "", "", errors.New("signed catalog verification requires a digest-pinned OCI source")
		}
	}
	client := options.Clients.Public
	if mirrored {
		client = options.Clients.Mirror
	}
	if client == nil {
		return nil, "", "", "", errors.New("catalog HTTP client is not configured")
	}
	var body []byte
	var revision, identity string
	if strings.HasPrefix(effective, "oci://") {
		body, revision, identity, err = fetchOCICatalog(ctx, client, effective)
	} else {
		body, err = fetchHTTPSCatalog(ctx, client, effective, mirrored)
		revision, identity = immutableCatalogIdentity(options.URL)
	}
	if err != nil {
		return nil, "", "", "", err
	}
	actual := fmt.Sprintf("sha256:%x", sha256.Sum256(body))
	if mirrored && strings.TrimSpace(options.ExpectedDigest) == "" {
		return nil, "", "", "", errors.New("mirrored catalog retrieval requires an expected document digest")
	}
	if expected := strings.TrimSpace(options.ExpectedDigest); expected != "" && actual != expected {
		return nil, "", "", "", fmt.Errorf("catalog document digest mismatch: got %s", actual)
	}
	if revision == "" {
		revision = actual
	}
	if identity == "" {
		identity = "digest:" + actual
	}
	verificationStatus := "digest-verified"
	if options.TrustPolicy != nil {
		reference := strings.TrimPrefix(effective, "oci://")
		repositoryName, subjectDigest, ok := strings.Cut(reference, "@")
		if !ok {
			return nil, "", "", "", errors.New("signed catalog OCI identity is invalid")
		}
		verification, verifyErr := resolver.VerifyPinnedOCI(ctx, client, repositoryName, subjectDigest, *options.TrustPolicy, options.TrustDirectory)
		if verifyErr != nil {
			return nil, "", "", "", fmt.Errorf("catalog signature verification failed: %w", verifyErr)
		}
		verificationStatus = verification.Status
		identity = verification.Provider + ":" + verification.Identity
	}
	return body, revision, identity, verificationStatus, nil
}

func fetchHTTPSCatalog(ctx context.Context, client *http.Client, source string, mirrored bool) ([]byte, error) {
	if !strings.HasPrefix(source, "https://") {
		return nil, errors.New("catalog source must use HTTPS or digest-pinned OCI")
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch catalog: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch catalog: HTTP %d", response.StatusCode)
	}
	return readCatalogBytes(response.Body, response.ContentLength)
}

func fetchOCICatalog(ctx context.Context, client *http.Client, source string) ([]byte, string, string, error) {
	reference := strings.TrimPrefix(source, "oci://")
	repositoryName, requestedDigest, ok := strings.Cut(reference, "@")
	if !ok || repositoryName == "" || digest.Digest(requestedDigest).Validate() != nil || !strings.HasPrefix(requestedDigest, "sha256:") {
		return nil, "", "", errors.New("OCI catalog source must be pinned by @sha256 digest")
	}
	repository, err := remote.NewRepository(repositoryName)
	if err != nil {
		return nil, "", "", errors.New("OCI catalog repository is invalid")
	}
	repository.Client = &remoteauth.Client{Client: client, Credential: remoteauth.StaticCredential(repository.Reference.Registry, remoteauth.EmptyCredential)}
	repository.MaxMetadataBytes = maxCatalogBytes
	descriptor, reader, err := repository.FetchReference(ctx, requestedDigest)
	if err != nil {
		return nil, "", "", fmt.Errorf("fetch OCI catalog manifest: %w", err)
	}
	manifestBytes, err := readCatalogBytes(reader, descriptor.Size)
	_ = reader.Close()
	if err != nil {
		return nil, "", "", err
	}
	if descriptor.Digest.String() != requestedDigest || digest.FromBytes(manifestBytes) != descriptor.Digest {
		return nil, "", "", errors.New("OCI catalog manifest digest mismatch")
	}
	var manifest ocispec.Manifest
	decoder := json.NewDecoder(bytes.NewReader(manifestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil || len(manifest.Layers) != 1 {
		return nil, "", "", errors.New("OCI catalog manifest must contain exactly one catalog layer")
	}
	layer := manifest.Layers[0]
	if layer.Size <= 0 || layer.Size > maxCatalogBytes {
		return nil, "", "", errors.New("OCI catalog layer exceeds the configured size limit")
	}
	layerReader, err := repository.Fetch(ctx, layer)
	if err != nil {
		return nil, "", "", fmt.Errorf("fetch OCI catalog layer: %w", err)
	}
	body, err := readCatalogBytes(layerReader, layer.Size)
	_ = layerReader.Close()
	if err != nil {
		return nil, "", "", err
	}
	if digest.FromBytes(body) != layer.Digest {
		return nil, "", "", errors.New("OCI catalog layer digest mismatch")
	}
	return body, requestedDigest, "oci:" + repositoryName + "@" + requestedDigest, nil
}

func readCatalogBytes(reader io.Reader, declared int64) ([]byte, error) {
	if declared > maxCatalogBytes {
		return nil, errors.New("catalog response exceeds 4 MiB")
	}
	body, err := io.ReadAll(io.LimitReader(reader, maxCatalogBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxCatalogBytes {
		return nil, errors.New("catalog response exceeds 4 MiB")
	}
	return body, nil
}
