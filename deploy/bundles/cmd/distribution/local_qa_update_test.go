//go:build unix && integration

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/oci"
	"oras.land/oras-go/v2/registry/remote"
)

// This test-only fixture signs a NEW feed with actual owned certificate crypto.
// It never invokes the production GitHub signer, records a published attempt,
// mutates the database, or grants approval. Document(1) is signed DATA for the
// fresh isolated import lane. Only real ImportDatabaseUpdate can reserve its
// local imported disposition and accept it against locked current authority.
func localQAPrepareSignedUpdateAndExport(t *testing.T, ctx context.Context, input localQAGraphInput, offline distribution.OfflineOptions, subjects subjectIndex, signer *localQACertificateSigner, proofs map[string]*distribution.VerifiedSupplierDatabasePayload, mirrorIdentity, parent string) (string, string) {
	t.Helper()
	if input.FeedRepository == "" || input.FeedRepository == parent || !localQAApprovedPullRepository(input.Registry.Trust.Read.Repositories, input.FeedRepository) {
		t.Fatal("local QA independent feed repository not approved")
	}
	for _, path := range []string{input.ExportCache, input.OfflineKit} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			t.Fatal("local QA export path invalid")
		}
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("local QA export paths must be absent")
		}
	}
	if input.ExportCache == input.OfflineKit || strings.HasPrefix(input.ExportCache, input.OfflineKit+string(os.PathSeparator)) || strings.HasPrefix(input.OfflineKit, input.ExportCache+string(os.PathSeparator)) {
		t.Fatal("local QA export paths overlap")
	}
	if err := os.Mkdir(input.ExportCache, 0700); err != nil {
		t.Fatal("local QA export staging creation failed")
	}
	manifest, err := boundedFile(filepath.Join(input.Output, "release.json"), 1<<20)
	if err != nil {
		t.Fatal("local QA release missing")
	}
	payload, err := boundedFile(filepath.Join(input.Output, "mirror-plan.json"), 1<<20)
	if err != nil {
		t.Fatal("local QA mirror payload missing")
	}
	endorsement, err := boundedFile(filepath.Join(input.Output, "mirror-signature.json"), 1<<20)
	if err != nil {
		t.Fatal("local QA mirror endorsement missing")
	}
	plan, err := distribution.VerifyMirrorPlan(ctx, payload, endorsement, "mirror", signer.trustDirectory, offline.Release, offline.Digest, offline.CatalogDigest, sha(manifest))
	if err != nil || plan.Identity() != mirrorIdentity {
		t.Fatal("local QA current mirror identity differs")
	}
	binding := distribution.DatabaseUpdateBinding{BaseRelease: offline.Release, BaseDistributionDigest: offline.Digest, MirrorEndorsementDigest: mirrorIdentity, CatalogDigest: offline.CatalogDigest, Profile: offline.Profile, CandidateSourceCommit: input.SourceCommit, Scanner: input.Scanner}
	authority, err := distribution.NewPinnedDatabaseAuthority(ctx, distribution.Options{Repository: parent, Digest: offline.Digest, CatalogDigest: offline.CatalogDigest, Release: offline.Release, Profile: offline.Profile, TrustDirectory: signer.trustDirectory, Policy: signer.policy, Network: input.Registry.Trust.Network, MirrorPlan: plan, UpdateBinding: &binding}, input.Registry.Trust)
	if err != nil {
		t.Fatal("local QA canonical current physical authority unavailable")
	}
	defer authority.Close()
	base, err := authority.Base(ctx)
	if err != nil {
		t.Fatal("local QA actual signed base unavailable")
	}
	var repositories distribution.DatabasePublicationRepositories
	for _, asset := range subjects.Assets {
		switch asset.ID {
		case "trivy-database":
			repositories.Main = asset.Repository
		case "trivy-java-database":
			repositories.Java = asset.Repository
		}
	}
	now := time.Now().UTC()
	prepared, err := authority.PrepareDatabasePublication(ctx, base, proofs["trivy-database"], proofs["trivy-java-database"], repositories, now)
	if err != nil {
		t.Fatal("local QA genuine supplier/current-base preparation failed")
	}
	document, valid := prepared.Document(1)
	if !valid || prepared.ValidateCurrent(ctx, time.Now().UTC()) != nil {
		t.Fatal("local QA prepared feed unavailable or stale")
	}
	content, err := json.Marshal(document)
	if err != nil || int64(len(content)) > distribution.MaximumDatabaseUpdateBytes {
		t.Fatal("local QA bounded feed encoding failed")
	}
	// Exactly the product's standard OCI empty-config/one-layer artifact shape.
	// The canonical signed feed loader validates this DATA again before export.
	layout := filepath.Join(input.Output, "layout")
	config, err := writeBlob(layout, ocispec.MediaTypeEmptyJSON, []byte("{}"))
	if err != nil {
		t.Fatal("local QA feed config unavailable")
	}
	layer, err := writeBlob(layout, distribution.DatabaseUpdateMediaType, content)
	if err != nil {
		t.Fatal("local QA feed body unavailable")
	}
	original, err := json.Marshal(ocispec.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageManifest, Config: config, Layers: []ocispec.Descriptor{layer}})
	if err != nil {
		t.Fatal("local QA feed manifest unavailable")
	}
	descriptor, err := writeBlob(layout, ocispec.MediaTypeImageManifest, original)
	if err != nil {
		t.Fatal("local QA feed manifest write failed")
	}
	feed := descriptor.Digest.String()
	signature, err := signer.signSubject(ctx, layout, input.FeedRepository, feed)
	if err != nil {
		t.Fatal("local QA feed certificate verification failed")
	}
	localQACopyStoredSubject(t, ctx, input, input.FeedRepository, feed, signature)
	// Actual canonical consumer verifies feed AND original DB signatures, transfers
	// both complete immutable layers, inspects actual metadata, exports index last,
	// and rereads the finished kit with its NO-NETWORK closed-store verifier.
	result, err := authority.ExportOfflineUpdateKit(ctx, base, input.FeedRepository, feed, input.ExportCache, input.OfflineKit, time.Now().UTC())
	if err != nil || result.Generation != 1 || result.FeedDigest != feed {
		t.Fatal("local QA genuine signed offline export/readback failed")
	}
	captured, err := authority.CaptureOfflineUpdateKit(ctx, base, input.OfflineKit, time.Now().UTC())
	if err != nil {
		t.Fatal("local QA finished disconnected kit verification failed")
	}
	defer captured.Close()
	actual, readback, _, _, valid := captured.Snapshot()
	if !valid || actual != feed || readback.Binding != binding {
		t.Fatal("local QA exported feed/current base binding differs")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if changed, e := authority.CaptureOfflineUpdateKit(canceled, base, input.OfflineKit, time.Now().UTC()); e == nil {
		if changed != nil {
			changed.Close()
		}
		t.Fatal("local QA canceled import capture accepted")
	}
	localQAOfflineActualNegatives(t, ctx, authority, base, input.OfflineKit, document)
	return feed, input.OfflineKit
}

func localQACopyStoredSubject(t *testing.T, ctx context.Context, input localQAGraphInput, repository, feed, signature string) {
	t.Helper()
	if !localQAApprovedPullRepository(input.Registry.Trust.Read.Repositories, repository) || input.Registry.Trust.Network.ValidateURL(ctx, "https://"+repository) != nil {
		t.Fatal("local QA immutable copied subject exceeds protected registry scope")
	}
	owner, err := resolver.CaptureDatabaseRegistryTransport(ctx, input.Registry.Trust)
	if err != nil {
		t.Fatal("local QA feed physical transport unavailable")
	}
	defer owner.Close()
	push, err := localQAReadPush(input.Registry.PushFile, input.Registry.PushRealm, input.Registry.PushService)
	if err != nil {
		t.Fatal("local QA feed independent push capture failed")
	}
	defer func() { push.policy.Username = ""; push.policy.Password = "" }()
	raw, err := owner.PublicationAuthenticationClient(ctx)
	if err != nil {
		t.Fatal("local QA feed physical publication shell unavailable")
	}
	raw.Transport = localQAPushTransport{base: raw.Transport, credential: push}
	client, err := resolver.NewDatabaseRegistryPublicationClient(raw, repository, push.policy)
	if err != nil {
		t.Fatal("local QA feed exact push constructor failed")
	}
	defer client.CloseIdleConnections()
	destination, err := remote.NewRepository(repository)
	if err != nil {
		t.Fatal("local QA feed target invalid")
	}
	destination.Client = client
	destination.MaxMetadataBytes = 1 << 20
	source, err := oci.New(filepath.Join(input.Output, "layout"))
	if err != nil {
		t.Fatal("local QA original feed layout unavailable")
	}
	options := oras.DefaultCopyOptions
	options.Concurrency = 2
	for _, part := range []struct{ source, target string }{{signature, "sha256-" + strings.TrimPrefix(feed, "sha256:") + ".sig"}, {feed, feed}} {
		actual, e := oras.Copy(ctx, source, part.source, destination, part.target, options)
		if e != nil || actual.Digest.String() != part.source {
			t.Fatal("local QA unchanged signed feed copy failed")
		}
	}
	if owner.Revalidate(ctx) != nil || push.current() != nil {
		t.Fatal("local QA feed physical authority changed")
	}
	// No current/discovery tag is published here. The actual local import command
	// updates it only after imported acceptance succeeds against the owning DB.
}

// Both failures use genuine signed originals, without touching the finished
// physical kit or copying multi-GiB layers. The exact main child signature is
// selected by its signed manifest digest; an arbitrary map entry is never used.
func localQAOfflineActualNegatives(t *testing.T, ctx context.Context, authority *distribution.DatabaseAuthority, base *distribution.VerifiedDatabaseAuthorityBase, directory string, document distribution.DatabaseUpdateDocument) {
	t.Helper()
	index, err := boundedFile(filepath.Join(directory, "index.json"), 64<<10)
	if err != nil {
		t.Fatal("local QA original offline index absent")
	}
	var kit distribution.OfflineDatabaseUpdateKit
	if closedJSON(index, &kit) != nil || len(kit.Subjects) != 3 || len(kit.Metadata) > 24 {
		t.Fatal("local QA original offline index invalid")
	}
	blobs := map[string][]byte{}
	var total int64
	for _, member := range kit.Metadata {
		data, e := boundedFile(filepath.Join(directory, strings.TrimPrefix(member.Digest, "sha256:")), 1<<20)
		if e != nil || int64(len(data)) != member.Size || sha(data) != member.Digest {
			t.Fatal("local QA original offline metadata changed")
		}
		total += int64(len(data))
		if total > distribution.MaximumOfflineDatabaseUpdateMetadataBytes {
			t.Fatal("local QA original offline metadata aggregate exceeded")
		}
		blobs[member.Digest] = data
	}
	input := distribution.OfflineDatabaseUpdateInput{Repository: kit.Repository, Digest: kit.Digest, Subjects: append([]distribution.StoredSubject(nil), kit.Subjects...), Blobs: blobs, MainPayloadPath: filepath.Join(directory, "main.tar.gz"), JavaPayloadPath: filepath.Join(directory, "java.tar.gz")}
	main := ""
	for _, snapshot := range document.Databases {
		if snapshot.Role == "trivy-database" {
			main = snapshot.Manifest.Digest
		}
	}
	signature := ""
	for _, subject := range kit.Subjects {
		if subject.Digest == main {
			if signature != "" {
				t.Fatal("local QA ambiguous main signature")
			}
			signature = subject.SignatureDigest
		}
	}
	original, exists := blobs[signature]
	if signature == "" || !exists {
		t.Fatal("local QA exact genuine main child signature absent")
	}
	delete(blobs, signature)
	if accepted, e := authority.CaptureOfflineUpdate(ctx, base, input, time.Now().UTC()); e == nil {
		if accepted != nil {
			accepted.Close()
		}
		t.Fatal("local QA unsigned exact main child accepted")
	}
	blobs[signature] = original
	original, exists = blobs[kit.Digest]
	if !exists || len(original) == 0 {
		t.Fatal("local QA genuine original feed manifest absent")
	}
	changed := append([]byte(nil), original...)
	changed[0] = '!'
	blobs[kit.Digest] = changed
	if accepted, e := authority.CaptureOfflineUpdate(ctx, base, input, time.Now().UTC()); e == nil {
		if accepted != nil {
			accepted.Close()
		}
		t.Fatal("local QA changed original feed accepted")
	}
	// The kit files remain unchanged and the caller's successful capture stays
	// pinned to the original signed bytes. No authority row/witness is involved.
}
