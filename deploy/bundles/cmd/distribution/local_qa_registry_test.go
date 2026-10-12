//go:build unix && integration

package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/oci"
	"oras.land/oras-go/v2/registry/remote"
)

// Every registry decision is explicit ROOT constructor input, not an image,
// catalog, signed mirror plan, credential helper or ambient Docker setting.
// PushFile contains a separately owned private QA push role; Registry.Read
// captures the independent mounted canonical pull role. Neither is serialized.
type localQARegistryInput struct {
	Trust       resolver.DatabaseRegistryTrustOptions `json:"trust"`
	PushFile    string                                `json:"push_file"`
	PushRealm   string                                `json:"push_realm"`
	PushService string                                `json:"push_service"`
}

type localQAPrivatePush struct {
	path   string
	digest [sha256.Size]byte
	policy resolver.DatabaseRegistryAuthentication
}

func (localQAPrivatePush) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "<private local QA push role>")
}
func localQAReadPush(path, realm, service string) (*localQAPrivatePush, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("local QA push role path invalid")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return nil, errors.New("local QA private push role unavailable")
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, errors.New("local QA private push role unavailable")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) || opened.Mode() != info.Mode() || opened.Size() <= 0 || opened.Size() > 32<<10 {
		return nil, errors.New("local QA private push FD changed")
	}
	physical, ok := opened.Sys().(*syscall.Stat_t)
	if !ok || physical.Uid != uint32(os.Geteuid()) || physical.Nlink != 1 {
		return nil, errors.New("local QA private push FD owner invalid")
	}
	payload, err := io.ReadAll(io.LimitReader(file, (32<<10)+1))
	if err != nil || int64(len(payload)) != opened.Size() {
		return nil, errors.New("local QA private push role unavailable")
	}
	defer clear(payload)
	var document struct {
		Schema   int    `json:"schema"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if closedJSON(payload, &document) != nil || document.Schema != 1 || document.Username == "" || document.Password == "" || len(document.Username) > 4096 || len(document.Password) > 16384 || strings.ContainsAny(document.Username+document.Password, "\r\n\x00") || strings.Contains(document.Username, ":") {
		return nil, errors.New("local QA private push role invalid")
	}
	final, e := file.Stat()
	if e != nil || !os.SameFile(opened, final) || final.Mode() != opened.Mode() || final.Size() != opened.Size() || !final.ModTime().Equal(opened.ModTime()) {
		return nil, errors.New("local QA private push FD changed")
	}
	after, e := os.Lstat(path)
	if e != nil || !os.SameFile(info, after) || after.Mode() != info.Mode() || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		return nil, errors.New("local QA private push role changed")
	}
	for _, state := range []os.FileInfo{final, after} {
		physical, ok := state.Sys().(*syscall.Stat_t)
		if !ok || physical.Uid != uint32(os.Geteuid()) || physical.Nlink != 1 {
			return nil, errors.New("local QA private push owner or links changed")
		}
	}
	return &localQAPrivatePush{path: path, digest: sha256.Sum256(payload), policy: resolver.DatabaseRegistryAuthentication{RealmURL: realm, Service: service, Username: document.Username, Password: document.Password}}, nil
}
func (credential *localQAPrivatePush) current() error {
	live, err := localQAReadPush(credential.path, credential.policy.RealmURL, credential.policy.Service)
	if err != nil {
		return err
	}
	defer func() { live.policy.Username = ""; live.policy.Password = "" }()
	if live.digest != credential.digest {
		return errors.New("local QA mounted push role changed")
	}
	return nil
}

type localQAPushTransport struct {
	base       http.RoundTripper
	credential *localQAPrivatePush
}

func (transport localQAPushTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if err := transport.credential.current(); err != nil {
		return nil, err
	}
	response, err := transport.base.RoundTrip(request)
	if err != nil {
		return nil, errors.New("local QA protected registry request failed")
	}
	if err := transport.credential.current(); err != nil {
		_ = response.Body.Close()
		return nil, err
	}
	return response, nil
}
func (transport localQAPushTransport) CloseIdleConnections() {
	if closer, ok := transport.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

// Copy originals with the canonical ORAS immutable graph copy and certificate
// signatures. The canonical loader then independently reads the protected TLS
// registry through its separately captured pull-only owner, and every complete
// DB layer is rehashed through the existing bounded authenticated range reader.
// This is a QA fixture publication, never execution of production OIDC signing.
func localQACopyAndReadback(t *testing.T, ctx context.Context, input localQAGraphInput, options distribution.OfflineOptions, signed []distribution.StoredSubject, blobs map[string][]byte) string {
	t.Helper()
	selected := input.Registry
	if selected.Trust.Read == nil || !selected.Trust.Network.Restricted || selected.PushFile == "" {
		t.Fatal("local QA separate private push and pull roles required")
	}
	if selected.PushFile == filepath.Join(selected.Trust.TrustDirectory, selected.Trust.Read.CredentialRef+".json") {
		t.Fatal("local QA push cannot implicitly reuse pull material")
	}
	pullOwner, err := resolver.CaptureDatabaseRegistryTransport(ctx, selected.Trust)
	if err != nil {
		t.Fatal("local QA canonical registry physical owner unavailable")
	}
	defer pullOwner.Close()
	push, err := localQAReadPush(selected.PushFile, selected.PushRealm, selected.PushService)
	if err != nil {
		t.Fatal("local QA private push owner unavailable")
	}
	defer func() { push.policy.Username = ""; push.policy.Password = "" }()
	raw, err := pullOwner.PublicationAuthenticationClient(ctx)
	if err != nil {
		t.Fatal("local QA physical publication shell unavailable")
	}
	raw.Transport = localQAPushTransport{base: raw.Transport, credential: push}
	store, err := oci.New(filepath.Join(input.Output, "layout"))
	if err != nil {
		t.Fatal("local QA original OCI layout unavailable")
	}
	payload, err := boundedFile(filepath.Join(input.Output, "mirror-plan.json"), 1<<20)
	if err != nil {
		t.Fatal("local QA exact mirror plan absent")
	}
	endorsement, err := boundedFile(filepath.Join(input.Output, "mirror-signature.json"), 1<<20)
	if err != nil {
		t.Fatal("local QA exact mirror endorsement absent")
	}
	releaseBytes, err := boundedFile(filepath.Join(input.Output, "release.json"), 1<<20)
	if err != nil {
		t.Fatal("local QA release bytes unavailable")
	}
	plan, err := distribution.VerifyMirrorPlan(ctx, payload, endorsement, "mirror", options.TrustDirectory, options.Release, options.Digest, options.CatalogDigest, sha(releaseBytes))
	if err != nil {
		t.Fatal("local QA operator mirror authentication failed")
	}
	for _, subject := range signed {
		target := mirrorRepository(input.MirrorTarget, subject.Repository)
		if !localQAApprovedPullRepository(selected.Trust.Read.Repositories, target) || selected.Trust.Network.ValidateURL(ctx, "https://"+target) != nil {
			t.Fatal("local QA signed artifact preflight exceeds configured target/pull scope")
		}
	}
	images := localQAPreflightImages(t, ctx, input, options, blobs, pullOwner.Client())
	localQACopyImages(t, ctx, input, images, raw, push.policy)
	// Signed children and original signature manifests precede parent visibility.
	ordered := append([]distribution.StoredSubject(nil), signed...)
	for i := range ordered {
		if ordered[i].Repository == options.Repository && ordered[i].Digest == options.Digest {
			ordered[i], ordered[len(ordered)-1] = ordered[len(ordered)-1], ordered[i]
			break
		}
	}
	for _, subject := range ordered {
		repositoryName := mirrorRepository(input.MirrorTarget, subject.Repository)
		if !localQAApprovedPullRepository(selected.Trust.Read.Repositories, repositoryName) {
			t.Fatal("local QA mirror repository not constructor-approved for pull")
		}
		if err := selected.Trust.Network.ValidateURL(ctx, "https://"+repositoryName); err != nil {
			t.Fatal("local QA mirror outside explicit operator origin")
		}
		client, err := resolver.NewDatabaseRegistryPublicationClient(raw, repositoryName, push.policy)
		if err != nil {
			t.Fatal("local QA exact protected push constructor failed")
		}
		destination, err := remote.NewRepository(repositoryName)
		if err != nil {
			t.Fatal("local QA mirror repository invalid")
		}
		destination.Client = client
		destination.MaxMetadataBytes = 1 << 20
		copyOptions := oras.DefaultCopyOptions
		copyOptions.Concurrency = 2
		for _, copy := range []struct{ source, target string }{{subject.SignatureDigest, "sha256-" + strings.TrimPrefix(subject.Digest, "sha256:") + ".sig"}, {subject.Digest, subject.Digest}} {
			copied, err := oras.Copy(ctx, store, copy.source, destination, copy.target, copyOptions)
			if err != nil || copied.Digest.String() != copy.source {
				t.Fatal("local QA unchanged signed artifact copy failed")
			}
		}
		client.CloseIdleConnections()
	}
	parent := mirrorRepository(input.MirrorTarget, options.Repository)
	loader, err := distribution.New(distribution.Options{Repository: parent, Digest: options.Digest, CatalogDigest: options.CatalogDigest, Release: options.Release, Profile: options.Profile, TrustDirectory: options.TrustDirectory, Policy: options.Policy, Network: pullOwner.Network(), Client: pullOwner.Client(), MirrorPlan: plan})
	if err != nil {
		t.Fatal("local QA mirrored canonical consumer unavailable")
	}
	if _, err = loader.Load(ctx); err != nil || loader.Status().Verification != "verified" || !loader.Status().InventoryComplete {
		t.Fatal("local QA original certificate parent/children readback failed")
	}
	index := offlineTransferIndex{Repository: options.Repository, Digest: options.Digest, CatalogDigest: options.CatalogDigest, Release: options.Release, Profile: options.Profile}
	if err := verifyMirroredDatabasePayloads(ctx, index, blobs, plan, mirrorExecutionOptions{Vendor: options, Network: pullOwner.Network()}, pullOwner.Client()); err != nil {
		t.Fatal("local QA genuine complete mirrored DB readback failed")
	}
	if err = pullOwner.Revalidate(ctx); err != nil || push.current() != nil {
		t.Fatal("local QA mounted registry authority changed before final receipt")
	}
	// Actual pull-only client denies write authority before any HTTP transmission.
	negativeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(negativeCtx, http.MethodPut, "https://"+parent+"/manifests/not-permitted", nil)
	if err != nil {
		t.Fatal("local QA pull-only negative invalid")
	}
	response, err := pullOwner.Client().Do(request)
	if response != nil {
		_ = response.Body.Close()
	}
	if err == nil {
		t.Fatal("local QA pull-only client adopted protected write")
	}
	return parent
}
func localQAApprovedPullRepository(approved []string, repository string) bool {
	for _, item := range approved {
		if item == repository {
			return true
		}
	}
	return false
}

// Keep the fixture's static privacy messages; no registry body/header is copied
// into its receipt. Actual errors fail the process and private owner logs stay
// with the ROOT held lane rather than being forwarded to product UI.

// Original image repositories remain retrieval DATA. Only this independently
// supplied operator source policy/CA can authorize an image request. The private
// target's source images require the already captured exact mounted pull role;
// all other approved origins stay anonymous and use exact repository challenges.
func localQAPreflightImages(t *testing.T, ctx context.Context, input localQAGraphInput, options distribution.OfflineOptions, blobs map[string][]byte, privatePull *http.Client) map[string]imageGraph {
	t.Helper()
	if !input.ImageSourcePolicy.Restricted || input.ImageSourcePolicy.Validate() != nil {
		t.Fatal("local QA image source policy must be explicit")
	}
	tls, err := httpclient.TLSForCA(input.ImageSourceCA)
	if err != nil {
		t.Fatal("local QA image source CA invalid")
	}
	public, err := httpclient.NewDestinationClientContext(ctx, input.ImageSourcePolicy, httpclient.DestinationOptions{TLSConfig: tls, Timeout: time.Minute, MaxResponseBytes: distribution.MaximumArchiveBytes, MaxRedirects: 0})
	if err != nil {
		t.Fatal("local QA image source client unavailable")
	}
	t.Cleanup(public.CloseIdleConnections)
	var parent ocispec.Manifest
	if closedJSON(blobs[options.Digest], &parent) != nil || len(parent.Layers) != 1 {
		t.Fatal("local QA signed image parent missing")
	}
	members, bom, err := distribution.ReadClosedArchive(blobs[parent.Layers[0].Digest.String()], options.Release, options.CatalogDigest)
	if err != nil {
		t.Fatal("local QA signed image archive missing")
	}
	var inventory distribution.Inventory
	found := false
	for _, profile := range bom.Profiles {
		if profile.Name == options.Profile {
			if found || closedJSON(members[profile.InventoryPath], &inventory) != nil {
				t.Fatal("local QA signed image inventory invalid")
			}
			found = true
		}
	}
	if !found || len(inventory.Images) == 0 || len(inventory.Images) > 4096 {
		t.Fatal("local QA signed image inventory missing or oversized")
	}
	graphs := map[string]imageGraph{}
	for _, image := range inventory.Images {
		source, subject, err := contentSubject(image.Reference)
		if err != nil {
			t.Fatal("local QA immutable image source invalid")
		}
		target := mirrorRepository(input.MirrorTarget, source)
		if input.Registry.Trust.Network.ValidateURL(ctx, "https://"+target) != nil {
			t.Fatal("local QA image target outside operator policy")
		}
		var client *http.Client
		if strings.HasPrefix(source, input.Registry.Trust.Read.Registry+"/") {
			if !localQAApprovedPullRepository(input.Registry.Trust.Read.Repositories, source) {
				t.Fatal("local QA private source image pull role absent")
			}
			client = privatePull
		} else {
			if input.ImageSourcePolicy.ValidateURL(ctx, "https://"+source) != nil {
				t.Fatal("local QA image source outside operator policy")
			}
			client, err = resolver.NewDatabaseRegistryClient(public, source, resolver.DatabaseRegistryAuthentication{})
			if err != nil {
				t.Fatal("local QA anonymous image source constructor failed")
			}
		}
		repository, err := remote.NewRepository(source)
		if err != nil {
			t.Fatal("local QA image repository invalid")
		}
		repository.Client = client
		repository.MaxMetadataBytes = 1 << 20
		graph, err := preflightImageGraph(ctx, repository, subject)
		if err != nil {
			t.Fatal("local QA complete original image preflight failed")
		}
		graphs[image.Reference] = graph
	}
	return graphs
}
func localQACopyImages(t *testing.T, ctx context.Context, input localQAGraphInput, graphs map[string]imageGraph, raw *http.Client, policy resolver.DatabaseRegistryAuthentication) {
	t.Helper()
	references := make([]string, 0, len(graphs))
	for reference := range graphs {
		references = append(references, reference)
	}
	sort.Strings(references)
	for _, reference := range references {
		source, subject, err := contentSubject(reference)
		if err != nil {
			t.Fatal("local QA immutable image copy source changed")
		}
		target := mirrorRepository(input.MirrorTarget, source)
		client, err := resolver.NewDatabaseRegistryPublicationClient(raw, target, policy)
		if err != nil {
			t.Fatal("local QA exact image push constructor failed")
		}
		destination, err := remote.NewRepository(target)
		if err != nil {
			t.Fatal("local QA exact image target invalid")
		}
		destination.Client = client
		destination.MaxMetadataBytes = 1 << 20
		graph := graphs[reference]
		copyOptions := oras.DefaultCopyOptions
		copyOptions.Concurrency = 2
		copyOptions.PreCopy = graph.checkDescriptor
		copied, err := oras.Copy(ctx, graph.repository, subject, destination, subject, copyOptions)
		if err != nil || copied.Digest.String() != subject {
			t.Fatal("local QA complete unchanged image copy failed")
		}
		if _, err := preflightImageGraph(ctx, destination, subject); err != nil {
			t.Fatal("local QA complete image platform/config readback failed")
		}
		client.CloseIdleConnections()
	}
}
