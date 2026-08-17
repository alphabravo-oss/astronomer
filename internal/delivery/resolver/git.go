package resolver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/packfile"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp/capability"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp/sideband"
	"github.com/go-git/go-git/v5/plumbing/transport"
	transporthttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	transportssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"github.com/go-git/go-git/v5/storage/memory"
	xssh "golang.org/x/crypto/ssh"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
)

var fullGitObjectID = regexp.MustCompile(`^(?:[a-f0-9]{40}|[a-f0-9]{64})$`)

type gitResolver struct{}

func (gitResolver) Resolve(ctx context.Context, request Request, client *http.Client) (Result, error) {
	if strings.HasPrefix(request.Source.URL, "ssh://") {
		return resolveGitSSH(ctx, request)
	}
	if !strings.HasPrefix(request.Source.URL, "https://") {
		return Result{}, invalid("Git source must use HTTPS or SSH")
	}
	endpoint, err := transport.NewEndpoint(request.Source.URL)
	if err != nil {
		return Result{}, invalid("Git source URL is invalid")
	}
	auth, err := gitHTTPAuth(request.Source.AuthMode, request.Credential)
	if err != nil {
		return Result{}, err
	}
	session, err := transporthttp.NewClient(client).NewUploadPackSession(endpoint, auth)
	if err != nil {
		return Result{}, classifyGitError(err)
	}
	defer func() { _ = session.Close() }()
	advertised, err := session.AdvertisedReferencesContext(ctx)
	if err != nil {
		return Result{}, classifyGitError(err)
	}
	commit, err := selectGitCommit(advertised.References, advertised.Peeled, request.RequestedRevision)
	if err != nil {
		return Result{}, err
	}
	revision := model.ImmutableRevision{
		Kind: model.RevisionGitCommit, Value: commit, ArtifactDigest: immutableGitDigest(commit),
	}
	result := Result{Revision: revision}
	if !request.Source.Trust.AllowUnsigned {
		result.verificationPayload, err = fetchSignedCommit(ctx, session, advertised, commit, request.Limits.withDefaults().MaxArtifactBytes)
		if err != nil {
			return Result{}, err
		}
	}
	return result, nil
}

type boundedSSHAuth struct {
	user    string
	signer  xssh.Signer
	hostKey xssh.HostKeyCallback
	timeout time.Duration
}

func (a *boundedSSHAuth) Name() string   { return transportssh.PublicKeysName }
func (a *boundedSSHAuth) String() string { return a.Name() }
func (a *boundedSSHAuth) ClientConfig() (*xssh.ClientConfig, error) {
	return &xssh.ClientConfig{
		User: a.user, Auth: []xssh.AuthMethod{xssh.PublicKeys(a.signer)},
		HostKeyCallback: a.hostKey, Timeout: a.timeout,
	}, nil
}

func resolveGitSSH(ctx context.Context, request Request) (Result, error) {
	if request.Source.AuthMode != model.AuthSSH || request.Credential == nil || len(request.Credential.PrivateKey) == 0 || len(request.Credential.KnownHosts) == 0 {
		return Result{}, &Error{Code: CodeAuthentication, Message: "SSH Git key and known-hosts material are required"}
	}
	endpoint, err := transport.NewEndpoint(request.Source.URL)
	if err != nil || endpoint.Host == "" {
		return Result{}, invalid("SSH Git URL is invalid")
	}
	originalHost := endpoint.Host
	addresses, err := resolveAllowedAddresses(ctx, request.NetworkPolicy, originalHost)
	if err != nil {
		return Result{}, err
	}
	endpoint.Host = addresses[0].String()
	if endpoint.User == "" {
		endpoint.User = "git"
	}
	port := endpoint.Port
	if port <= 0 {
		port = transportssh.DefaultPort
	}
	signer, err := parseSSHSigner(request.Credential.PrivateKey, request.Credential.Passphrase)
	if err != nil {
		return Result{}, &Error{Code: CodeAuthentication, Message: "SSH private key is invalid"}
	}
	hostCallback, cleanup, err := knownHostsCallback(request.Credential.KnownHosts, originalHost, port)
	if err != nil {
		return Result{}, &Error{Code: CodeAuthentication, Message: "SSH known-hosts material is invalid"}
	}
	defer cleanup()
	auth := &boundedSSHAuth{user: endpoint.User, signer: signer, hostKey: hostCallback, timeout: request.Limits.withDefaults().Timeout}
	session, err := transportssh.NewClient(nil).NewUploadPackSession(endpoint, auth)
	if err != nil {
		return Result{}, classifyGitError(err)
	}
	defer func() { _ = session.Close() }()
	advertised, err := session.AdvertisedReferencesContext(ctx)
	if err != nil {
		return Result{}, classifyGitError(err)
	}
	commit, err := selectGitCommit(advertised.References, advertised.Peeled, request.RequestedRevision)
	if err != nil {
		return Result{}, err
	}
	result := Result{Revision: model.ImmutableRevision{Kind: model.RevisionGitCommit, Value: commit, ArtifactDigest: immutableGitDigest(commit)}}
	if !request.Source.Trust.AllowUnsigned {
		result.verificationPayload, err = fetchSignedCommit(ctx, session, advertised, commit, request.Limits.withDefaults().MaxArtifactBytes)
		if err != nil {
			return Result{}, err
		}
	}
	return result, nil
}

// fetchSignedCommit requests one shallow, blobless commit and bounds the
// demultiplexed pack stream before decoding it into an ephemeral object store.
// This proves the signature belongs to the advertised object ID; ls-refs alone
// is not cryptographic verification.
func fetchSignedCommit(ctx context.Context, session transport.UploadPackSession, advertised *packp.AdvRefs, commit string, maximum int64) ([]byte, error) {
	if maximum <= 0 {
		return nil, invalid("Git verification pack limit is invalid")
	}
	hash := plumbing.NewHash(commit)
	request := packp.NewUploadPackRequestFromCapabilities(advertised.Capabilities)
	request.Wants = []plumbing.Hash{hash}
	if advertised.Capabilities.Supports(capability.Shallow) {
		if err := request.Capabilities.Set(capability.Shallow); err != nil {
			return nil, &Error{Code: CodeUpstreamTemporary, Message: "Git shallow capability could not be negotiated", Retryable: true}
		}
		request.Depth = packp.DepthCommits(1)
	}
	if advertised.Capabilities.Supports(capability.Filter) {
		if err := request.Capabilities.Set(capability.Filter); err != nil {
			return nil, &Error{Code: CodeUpstreamTemporary, Message: "Git filter capability could not be negotiated", Retryable: true}
		}
		request.Filter = packp.FilterBlobNone()
	}
	response, err := session.UploadPack(ctx, request)
	if err != nil {
		return nil, classifyGitError(err)
	}
	defer func() { _ = response.Close() }()
	var packReader io.Reader = response
	if request.Capabilities.Supports(capability.Sideband64k) {
		packReader = sideband.NewDemuxer(sideband.Sideband64k, response)
	} else if request.Capabilities.Supports(capability.Sideband) {
		packReader = sideband.NewDemuxer(sideband.Sideband, response)
	}
	bounded := &hardLimitReader{reader: packReader, remaining: maximum + 1}
	storage := memory.NewStorage()
	if err := packfile.UpdateObjectStorage(storage, bounded); err != nil {
		if errors.Is(err, errHardLimit) {
			return nil, &Error{Code: CodeLimitExceeded, Message: "Git verification pack exceeds the configured size limit"}
		}
		return nil, &Error{Code: CodeUpstreamTemporary, Message: "Git verification pack is invalid", Retryable: true}
	}
	if bounded.read > maximum {
		return nil, &Error{Code: CodeLimitExceeded, Message: "Git verification pack exceeds the configured size limit"}
	}
	commitObject, err := object.GetCommit(storage, hash)
	if err != nil || commitObject.PGPSignature == "" {
		return nil, &Error{Code: CodeVerification, Message: "Git commit is unsigned or unavailable"}
	}
	encoded := &plumbing.MemoryObject{}
	if err := commitObject.Encode(encoded); err != nil {
		return nil, &Error{Code: CodeVerification, Message: "Git signed commit could not be encoded"}
	}
	reader, err := encoded.Reader()
	if err != nil {
		return nil, &Error{Code: CodeVerification, Message: "Git signed commit could not be read"}
	}
	defer func() { _ = reader.Close() }()
	payload, err := io.ReadAll(io.LimitReader(reader, 1<<20+1))
	if err != nil || len(payload) > 1<<20 {
		clearBytes(payload)
		return nil, &Error{Code: CodeLimitExceeded, Message: "Git signed commit exceeds the verification payload limit"}
	}
	if encoded.Hash() != hash {
		clearBytes(payload)
		return nil, &Error{Code: CodeDigestMismatch, Message: "Git commit object does not match the advertised identity"}
	}
	return payload, nil
}

var errHardLimit = errors.New("bounded reader limit exceeded")

type hardLimitReader struct {
	reader    io.Reader
	remaining int64
	read      int64
}

func (r *hardLimitReader) Read(buffer []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, errHardLimit
	}
	if int64(len(buffer)) > r.remaining {
		buffer = buffer[:r.remaining]
	}
	n, err := r.reader.Read(buffer)
	r.remaining -= int64(n)
	r.read += int64(n)
	return n, err
}

func parseSSHSigner(privateKey, passphrase []byte) (xssh.Signer, error) {
	if len(passphrase) == 0 {
		return xssh.ParsePrivateKey(privateKey)
	}
	return xssh.ParsePrivateKeyWithPassphrase(privateKey, passphrase)
}

func knownHostsCallback(contents []byte, originalHost string, port int) (xssh.HostKeyCallback, func(), error) {
	file, err := os.CreateTemp("", "astronomer-known-hosts-*")
	if err != nil {
		return nil, func() {}, err
	}
	name := file.Name()
	cleanup := func() { _ = os.Remove(name) }
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		cleanup()
		return nil, func() {}, err
	}
	if _, err := file.Write(contents); err != nil {
		_ = file.Close()
		cleanup()
		return nil, func() {}, err
	}
	if err := file.Close(); err != nil {
		cleanup()
		return nil, func() {}, err
	}
	callback, err := transportssh.NewKnownHostsCallback(name)
	cleanup()
	if err != nil {
		return nil, func() {}, err
	}
	originalAddress := net.JoinHostPort(originalHost, strconv.Itoa(port))
	return func(_ string, remote net.Addr, key xssh.PublicKey) error {
		if err := callback(originalAddress, remote, key); err != nil {
			return fmt.Errorf("SSH host key did not match registered trust: %w", err)
		}
		return nil
	}, func() {}, nil
}

func gitHTTPAuth(mode model.AuthMode, credential *CredentialMaterial) (transport.AuthMethod, error) {
	switch mode {
	case model.AuthNone:
		return nil, nil
	case model.AuthBasic:
		if credential == nil || len(credential.Username) == 0 || len(credential.Password) == 0 {
			return nil, &Error{Code: CodeAuthentication, Message: "Git basic credential is unavailable"}
		}
		return &transporthttp.BasicAuth{Username: string(credential.Username), Password: string(credential.Password)}, nil
	case model.AuthBearer:
		if credential == nil || len(credential.Token) == 0 {
			return nil, &Error{Code: CodeAuthentication, Message: "Git bearer credential is unavailable"}
		}
		return &transporthttp.TokenAuth{Token: string(credential.Token)}, nil
	case model.AuthWorkloadIdentity:
		return nil, &Error{Code: CodeAuthentication, Message: "Git workload identity is not configured on this resolver"}
	default:
		return nil, invalid("Git authentication mode is unsupported for HTTPS")
	}
}

func selectGitCommit(references map[string]plumbing.Hash, peeled map[string]plumbing.Hash, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if fullGitObjectID.MatchString(requested) {
		for _, hash := range references {
			if hash.String() == requested {
				return requested, nil
			}
		}
		for _, hash := range peeled {
			if hash.String() == requested {
				return requested, nil
			}
		}
		return "", &Error{Code: CodeNotFound, Message: "requested Git commit is not advertised by the source"}
	}
	if strings.ContainsAny(requested, "\r\n\x00") || strings.Contains(requested, "..") || strings.HasPrefix(requested, "/") {
		return "", invalid("requested Git reference is invalid")
	}
	candidates := []string{requested}
	if !strings.HasPrefix(requested, "refs/") {
		candidates = []string{"refs/heads/" + requested, "refs/tags/" + requested}
	} else if !strings.HasPrefix(requested, "refs/heads/") && !strings.HasPrefix(requested, "refs/tags/") {
		return "", invalid("only Git branch and tag references are supported")
	}
	resolved := make([]string, 0, len(candidates))
	for _, name := range candidates {
		if hash, ok := peeled[name]; ok && !hash.IsZero() {
			resolved = append(resolved, hash.String())
			continue
		}
		if hash, ok := references[name]; ok && !hash.IsZero() {
			resolved = append(resolved, hash.String())
		}
	}
	if len(resolved) == 0 {
		return "", &Error{Code: CodeNotFound, Message: "requested Git reference was not found"}
	}
	sort.Strings(resolved)
	if resolved[0] != resolved[len(resolved)-1] {
		return "", invalid("requested Git name is ambiguous between branch and tag")
	}
	return resolved[0], nil
}

func classifyGitError(err error) error {
	if err == nil {
		return nil
	}
	if err == transport.ErrAuthenticationRequired || err == transport.ErrAuthorizationFailed {
		return &Error{Code: CodeAuthentication, Message: "Git source authentication failed"}
	}
	if err == transport.ErrRepositoryNotFound || err == transport.ErrEmptyRemoteRepository {
		return &Error{Code: CodeNotFound, Message: "Git repository or revision was not found"}
	}
	return &Error{Code: CodeUpstreamTemporary, Message: "Git source request failed", Retryable: true}
}
