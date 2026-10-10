package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/oci"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials"
)

type publicationOptions struct {
	Directory       string
	ExpectedRelease string
	Mode            string
	CosignBinary    string
	QAKeyFile       string
	TrustDirectory  string
	Trust           model.TrustPolicy
	Network         httpclient.DestinationPolicy
	Client          *http.Client
	RegistryCAFile  string
	// Only tests inject a signer that addresses their owned loopback registry;
	// production signs the exact emitted repository@digest without rewriting it.
	sign func(context.Context, string) error
}

// publishLayout copies every exact prepared subject and signs each child before
// its parent. A successful copy/sign command is insufficient: the same runtime
// loader must authenticate parent and every child before success is reported.
func publishLayout(ctx context.Context, options publicationOptions) error {
	if options.ExpectedRelease == "" || options.Client == nil || options.Trust.Validate() != nil || options.Trust.AllowUnsigned {
		return errors.New("publication identity/client/trust missing")
	}
	if options.Mode != "production" && options.Mode != "qa" {
		return errors.New("publication mode invalid")
	}
	if options.Mode == "production" && (options.Trust.Provider != model.SignatureCosignKeyless || options.QAKeyFile != "" || options.Trust.Issuer != "https://token.actions.githubusercontent.com" || options.Trust.Identity != "https://github.com/alphabravo-oss/astronomer/.github/workflows/release.yaml@refs/tags/"+options.ExpectedRelease) {
		return errors.New("production publication requires exact vendor OIDC policy")
	}
	if options.Mode == "qa" && (options.Trust.Provider != model.SignatureCosignKey || options.QAKeyFile == "") {
		return errors.New("QA publication requires distinct mounted key policy")
	}
	if options.sign == nil {
		if err := preflightSigner(options.CosignBinary); err != nil {
			return err
		}
	}
	ca, removeCA, err := snapshotRegistryCA(options.RegistryCAFile)
	if err != nil {
		return err
	}
	defer removeCA()
	options.RegistryCAFile = ca
	snapshot, cleanup, err := snapshotPreparedLayout(options.Directory)
	if err != nil {
		return err
	}
	defer cleanup()
	options.Directory = snapshot
	data, err := boundedFile(filepath.Join(options.Directory, "subjects.json"), 1<<20)
	var index subjectIndex
	if err != nil || closedJSON(data, &index) != nil || index.Release != options.ExpectedRelease {
		return errors.New("publication layout release differs")
	}
	store, err := oci.New(options.Directory)
	if err != nil {
		return errors.New("prepared OCI store unavailable")
	}
	credentialStore, err := credentials.NewStoreFromDocker(credentials.StoreOptions{})
	if err != nil {
		return errors.New("protected registry credential store unavailable")
	}
	credential := credentials.Credential(credentialStore)
	subjects := append([]publishedSubject{}, index.Children...)
	for _, asset := range index.Assets {
		subjects = append(subjects, publishedSubject{Repository: asset.Repository, Digest: asset.Digest})
	}
	subjects = append(subjects, publishedSubject{Repository: index.Repository, Digest: index.Digest})
	signer := options.sign
	if signer == nil {
		signer = func(ctx context.Context, reference string) error {
			return signPublishedSubject(ctx, options, reference)
		}
	}
	for _, subject := range subjects {
		if err := options.Network.ValidateURL(ctx, "https://"+subject.Repository); err != nil {
			return errors.New("publication target outside operator network policy")
		}
		repository, err := remote.NewRepository(subject.Repository)
		if err != nil {
			return errors.New("publication repository invalid")
		}
		repository.Client = &auth.Client{Client: options.Client, Credential: credential, Cache: auth.NewCache()}
		repository.MaxMetadataBytes = 1 << 20
		// Source and destination select the immutable digest, never a mutable tag.
		copyOptions := oras.DefaultCopyOptions
		copyOptions.Concurrency = 2
		copied, err := oras.Copy(ctx, store, subject.Digest, repository, subject.Digest, copyOptions)
		if err != nil || copied.Digest.String() != subject.Digest {
			return errors.New("immutable OCI publication copy failed")
		}
		resolved, err := repository.Resolve(ctx, subject.Digest)
		if err != nil || resolved.Digest.String() != subject.Digest {
			return errors.New("published subject changed content identity")
		}
		if err := signer(ctx, subject.Repository+"@"+subject.Digest); err != nil {
			return errors.New("published subject signing failed")
		}
	}
	// Credential decoration stays within the same guarded underlying client.
	// The canonical loader does not gain ambient/private destination permission.
	authenticated := &http.Client{Transport: registryAuthenticationTransport{client: &auth.Client{Client: options.Client, Credential: credential, Cache: auth.NewCache()}}, Timeout: options.Client.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	loader, err := distribution.New(distribution.Options{Repository: index.Repository, Digest: index.Digest, CatalogDigest: index.CatalogDigest, Release: index.Release, Profile: "builtins", Policy: options.Trust, TrustDirectory: options.TrustDirectory, Client: authenticated, Network: options.Network})
	if err != nil {
		return errors.New("published distribution verifier configuration failed")
	}
	if _, err := loader.Load(ctx); err != nil {
		return errors.New("published parent or child authentication failed")
	}
	if loader.Status().Verification != "verified" || !loader.Status().InventoryComplete {
		return errors.New("published distribution remains unqualified")
	}
	return verifyPublishedDatabasePayloads(ctx, options, index, authenticated)
}

type registryAuthenticationTransport struct{ client *auth.Client }

func (transport registryAuthenticationTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport.client.Do(request)
}

func signPublishedSubject(ctx context.Context, options publicationOptions, reference string) error {
	if err := preflightSigner(options.CosignBinary); err != nil {
		return err
	}
	arguments := []string{"sign", "--yes", "--new-bundle-format=false", "--registry-referrers-mode=legacy", "--use-signing-config=false"}
	if options.RegistryCAFile != "" {
		arguments = append(arguments, "--registry-cacert", options.RegistryCAFile)
	}
	if options.Mode == "qa" {
		arguments = append(arguments, "--key", options.QAKeyFile, "--tlog-upload=false")
	}
	arguments = append(arguments, reference)
	// Bounded process time; output is discarded, never echoed with credentials,
	// OAuth state, registry URLs or private key paths from an upstream diagnostic.
	bounded, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	command := exec.CommandContext(bounded, options.CosignBinary, arguments...)
	if err := command.Run(); err != nil {
		return errors.New("qualified signing command failed")
	}
	return nil
}

func preflightSigner(binary string) error {
	info, err := os.Lstat(binary)
	if !filepath.IsAbs(binary) || err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return errors.New("absolute qualified signer executable required before publication")
	}
	return nil
}

func snapshotRegistryCA(name string) (string, func(), error) {
	if name == "" {
		return "", func() {}, nil
	}
	data, err := boundedFile(name, 1<<20)
	if err != nil {
		return "", nil, errors.New("bounded registry CA unavailable")
	}
	directory, err := os.MkdirTemp("", "astronomer-registry-ca-")
	if err != nil {
		return "", nil, errors.New("owned registry CA snapshot unavailable")
	}
	cleanup := func() { _ = os.RemoveAll(directory) }
	snapshot := filepath.Join(directory, "registry-ca.pem")
	if err := os.WriteFile(snapshot, data, 0600); err != nil {
		cleanup()
		return "", nil, err
	}
	if _, err := httpclient.TLSForCA(snapshot); err != nil {
		cleanup()
		return "", nil, err
	}
	return snapshot, cleanup, nil
}

func publishFromFlags(directory, expectedRelease, mode, cosignBinary, qaKey, trustDirectory, provider, keyRef, identity, issuer, networkFile, caFile string) error {
	if err := preflightSigner(cosignBinary); err != nil {
		return err
	}
	caFile, cleanupCA, err := snapshotRegistryCA(caFile)
	if err != nil {
		return err
	}
	defer cleanupCA()
	network := httpclient.DestinationPolicy{}
	if networkFile != "" {
		data, err := boundedFile(networkFile, 64<<10)
		if err != nil {
			return err
		}
		network, err = httpclient.ParseDestinationPolicy(string(data))
		if err != nil {
			return err
		}
	}
	tls, err := httpclient.TLSForCA(caFile)
	if err != nil {
		return err
	}
	client, err := httpclient.NewDestinationClient(network, httpclient.DestinationOptions{TLSConfig: tls, MaxResponseBytes: distribution.MaximumArchiveBytes})
	if err != nil {
		return err
	}
	defer client.CloseIdleConnections()
	policy := model.TrustPolicy{Provider: model.SignatureProvider(provider), KeyRef: keyRef, Identity: identity, Issuer: issuer}
	// No URL-derived publisher identity, unsigned retry, CLI offline flag, or
	// fetched trust root participates in the final canonical verification.
	if strings.TrimSpace(trustDirectory) == "" {
		return errors.New("mounted publication trust required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	return publishLayout(ctx, publicationOptions{Directory: directory, ExpectedRelease: expectedRelease, Mode: mode, CosignBinary: cosignBinary, QAKeyFile: qaKey, TrustDirectory: trustDirectory, Trust: policy, Network: network, Client: client, RegistryCAFile: caFile})
}
