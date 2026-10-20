package distribution

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
)

// DatabasePublicationRegistry is a protected operator-constructor write client,
// never a loader/feed/runtime-selected authority. The exact base transport and
// independently approved destination policy remain beneath scoped bearer auth.
// It does not grant a generation, signing identity, or durable published phase.
type DatabasePublicationRegistry struct {
	authority  *DatabaseAuthority
	proof      *VerifiedDatabaseAuthorityBase
	repository string
	client     *http.Client
}

func (authority *DatabaseAuthority) PublicationRegistry(ctx context.Context, proof *VerifiedDatabaseAuthorityBase, repository string, authentication resolver.DatabaseRegistryAuthentication) (*DatabasePublicationRegistry, error) {
	if ctx == nil || authority == nil || proof == nil || proof.owner != authority || !validDatabaseUpdateSubject(repository+"@sha256:"+strings.Repeat("a", 64), "sha256:"+strings.Repeat("a", 64)) {
		return nil, errors.New("database publication registry constructor invalid")
	}
	authority.mutex.RLock()
	defer authority.mutex.RUnlock()
	if authority.closed || proof.fingerprint != authority.physicalFingerprint() || authority.loader == nil {
		return nil, errors.New("database publication registry physical authority differs")
	}
	if err := authority.revalidatePhysicalTrust(ctx); err != nil {
		return nil, err
	}
	if authority.loader.options.Network.ValidateURL(ctx, "https://"+repository) != nil {
		return nil, errors.New("database publication registry outside captured destination policy")
	}
	if authentication.RealmURL != "" && authority.loader.options.Network.ValidateURL(ctx, authentication.RealmURL) != nil {
		return nil, errors.New("database publication token realm outside captured destination policy")
	}
	base := authority.loader.options.Client
	if authority.registry != nil {
		var err error
		base, err = authority.registry.PublicationAuthenticationClient(ctx)
		if err != nil {
			return nil, err
		}
	}
	client, err := resolver.NewDatabaseRegistryPublicationClient(base, repository, authentication)
	if err != nil {
		return nil, err
	}
	// Metadata clients have a short timeout. Uploads have their own finite total
	// deadline while retaining the same transport, roots and response body cap.
	client.Timeout = 15 * time.Minute
	return &DatabasePublicationRegistry{authority: authority, proof: proof, repository: repository, client: client}, nil
}

func (registry *DatabasePublicationRegistry) Close() {
	if registry != nil && registry.client != nil {
		registry.client.CloseIdleConnections()
	}
}
func (registry *DatabasePublicationRegistry) current(ctx context.Context) error {
	if ctx == nil || registry == nil || registry.authority == nil || registry.client == nil || registry.proof == nil {
		return errors.New("database publication registry unavailable")
	}
	return registry.authority.Revalidate(ctx, registry.proof)
}

// pushBlob never sends an inherited/caller Authorization header. The scoped
// auth client obtains a token on the empty POST before the original payload is
// streamed. A challenge/body replay failure cannot count as a completed copy.
func (registry *DatabasePublicationRegistry) pushBlob(ctx context.Context, descriptor ocispec.Descriptor, body io.Reader) error {
	_, err := registry.pushOwnedBlob(ctx, descriptor, body)
	return err
}

// joined=false means the caller MUST preserve its pinned reader and must not
// drain or close it. This explicit ownership failure never permits signing.
func (registry *DatabasePublicationRegistry) pushOwnedBlob(ctx context.Context, descriptor ocispec.Descriptor, body io.Reader) (joined bool, err error) {
	if err := registry.current(ctx); err != nil {
		return true, err
	}
	if body == nil || descriptor.Digest.Validate() != nil || descriptor.Digest.Algorithm() != digest.SHA256 || descriptor.Size <= 0 || descriptor.Size > MaximumDatabaseLayerBytes || len(descriptor.URLs) != 0 || len(descriptor.Data) != 0 {
		return true, errors.New("database publication blob descriptor invalid")
	}
	bounded, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	// Registry protocol paths include /v2/, not the repository's retrieval URL.
	host, name, _ := strings.Cut(registry.repository, "/")
	upload := "https://" + host + "/v2/" + name + "/blobs/uploads/"
	request, err := http.NewRequestWithContext(bounded, http.MethodPost, upload, nil)
	if err != nil {
		return true, errors.New("database publication upload request invalid")
	}
	response, err := registry.client.Do(request)
	if err != nil {
		return true, supplierDatabaseError(bounded, "database publication upload initialization unavailable")
	}
	locationText := response.Header.Get("Location")
	accepted := response.StatusCode == http.StatusAccepted
	if err := closeDatabasePublicationResponse(response); err != nil || !accepted {
		return true, errors.New("database publication upload initialization refused")
	}
	location, err := url.Parse(locationText)
	if err != nil || len(locationText) > 8192 {
		return true, errors.New("database publication upload location invalid")
	}
	location = request.URL.ResolveReference(location)
	if location.Scheme != "https" || location.Host != host || location.User != nil || location.Opaque != "" || location.Fragment != "" || location.RawFragment != "" || location.RawPath != "" || location.ForceQuery || !strings.HasPrefix(location.Path, "/v2/"+name+"/blobs/uploads/") {
		return true, errors.New("database publication upload location outside exact repository")
	}
	identifier := strings.TrimPrefix(location.Path, "/v2/"+name+"/blobs/uploads/")
	query, err := url.ParseQuery(location.RawQuery)
	if err != nil || identifier == "" || len(identifier) > 256 || strings.ContainsAny(identifier, "/\\\x00") || len(query) > 1 {
		return true, errors.New("database publication upload location invalid")
	}
	for key, values := range query {
		if key != "_state" || len(values) != 1 || len(values[0]) > 4096 || strings.ContainsAny(values[0], "\r\n\x00") {
			return true, errors.New("database publication upload location state invalid")
		}
	}
	query.Set("digest", descriptor.Digest.String())
	location.RawQuery = query.Encode()
	ownedBody := newDatabasePublicationRequestBody(bounded, body)
	request, err = http.NewRequestWithContext(bounded, http.MethodPut, location.String(), ownedBody)
	if err != nil {
		return true, errors.New("database publication upload completion request invalid")
	}
	request.ContentLength = descriptor.Size
	request.Header.Set("Content-Type", "application/octet-stream")
	response, requestError := registry.client.Do(request)
	var copyError error
	if requestError != nil {
		copyError = supplierDatabaseError(bounded, "database publication blob copy unavailable")
	} else {
		created := response.StatusCode == http.StatusCreated
		if responseError := closeDatabasePublicationResponse(response); responseError != nil || !created {
			copyError = errors.New("database publication blob copy refused")
		}
	}
	// The finite transport request is over, but its asynchronous body writer may
	// still be reading. Cancellation stops later reads; ONLY the transport's Close
	// acknowledgement proves no further underlying-reader access is possible.
	cancel()
	copied, joinError := ownedBody.waitJoined()
	if joinError != nil {
		return false, joinError
	}
	if err := ctx.Err(); err != nil {
		return true, err
	}
	if copyError != nil {
		return true, copyError
	}
	if copied != descriptor.Size {
		return true, errors.New("database publication blob copy was incomplete")
	}
	return true, registry.current(ctx)
}

func (registry *DatabasePublicationRegistry) pushManifest(ctx context.Context, descriptor ocispec.Descriptor, content []byte, reference string) error {
	if err := registry.current(ctx); err != nil {
		return err
	}
	if descriptor.Digest.Validate() != nil || descriptor.Digest.Algorithm() != digest.SHA256 || descriptor.Size <= 0 || descriptor.Size > 1<<20 || descriptor.Size != int64(len(content)) || digest.FromBytes(content) != descriptor.Digest || descriptor.MediaType != ocispec.MediaTypeImageManifest {
		return errors.New("database publication manifest binding invalid")
	}
	if reference != "current" && reference != descriptor.Digest.String() && !validDatabasePublicationSignatureTag(reference) {
		return errors.New("database publication manifest reference invalid")
	}
	bounded, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	host, name, _ := strings.Cut(registry.repository, "/")
	request, err := http.NewRequestWithContext(bounded, http.MethodPut, "https://"+host+"/v2/"+name+"/manifests/"+reference, bytes.NewReader(content))
	if err != nil {
		return errors.New("database publication manifest request invalid")
	}
	request.Header.Set("Content-Type", descriptor.MediaType)
	response, err := registry.client.Do(request)
	if err != nil {
		return supplierDatabaseError(bounded, "database publication manifest write unavailable")
	}
	created := response.StatusCode == http.StatusCreated
	if err := closeDatabasePublicationResponse(response); err != nil || !created {
		return errors.New("database publication manifest write refused")
	}
	return registry.current(bounded)
}

func closeDatabasePublicationResponse(response *http.Response) error {
	if response == nil || response.Body == nil {
		return errors.New("database publication response unavailable")
	}
	copied, err := io.Copy(io.Discard, io.LimitReader(response.Body, (64<<10)+1))
	closeErr := response.Body.Close()
	if err != nil || closeErr != nil || copied > 64<<10 {
		return errors.New("database publication response exceeds finite policy")
	}
	return nil
}

func validDatabasePublicationSignatureTag(value string) bool {
	if len(value) != len("sha256-")+64+len(".sig") || !strings.HasPrefix(value, "sha256-") || !strings.HasSuffix(value, ".sig") {
		return false
	}
	return digestPattern.MatchString("sha256:" + strings.TrimSuffix(strings.TrimPrefix(value, "sha256-"), ".sig"))
}
