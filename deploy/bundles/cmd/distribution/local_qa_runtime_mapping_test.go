//go:build unix && integration

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
	"github.com/alphabravocompany/astronomer-go/internal/releasecontract"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/oci"
)

type localQAReleaseMapping struct {
	SchemaVersion         int                          `json:"schema_version"`
	ReleaseVersion        string                       `json:"release_version"`
	ReleaseManifestDigest string                       `json:"release_manifest_digest"`
	DestinationRegistry   string                       `json:"destination_registry"`
	RegistryRewrites      map[string]string            `json:"registry_rewrites"`
	Entries               []localQAReleaseMappingEntry `json:"entries"`
}
type localQAReleaseMappingEntry struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Source   string `json:"source"`
	Target   string `json:"target"`
	Digest   string `json:"digest"`
	CopyTool string `json:"copy_tool"`
}

// Flux is a separate release-owned OCI artifact, not a new invented BOM role.
// ROOT supplies its genuine already-acquired immutable OCI layout. Every byte
// copied to the owned graph is descriptor-hashed before LOCAL certificate
// endorsement and canonical guarded registry readback. No upstream signature,
// production attestation, release publication, or native Flux gate is inferred.
func localQACopyFluxAndRuntimeMapping(t *testing.T, ctx context.Context, input localQAGraphInput, releasePath string, signer *localQACertificateSigner, parent string) {
	t.Helper()
	var release releasecontract.Manifest
	bytes, err := boundedFile(releasePath, 1<<20)
	if err != nil || closedJSON(bytes, &release) != nil {
		t.Fatal("local QA exact release readback unavailable")
	}
	_, projection, err := releasecontract.Load(releasePath, release.Release.Version)
	if err != nil {
		t.Fatal("local QA exact release projection invalid")
	}
	repository, subject, err := contentSubject(projection.FluxRepository + "@" + projection.FluxDigest)
	if err != nil {
		t.Fatal("local QA exact selected Flux subject invalid")
	}
	payload, err := boundedFile(filepath.Join(input.Output, "mirror-plan.json"), 1<<20)
	if err != nil {
		t.Fatal("local QA actual authenticated image closure unavailable")
	}
	signatureBytes, err := boundedFile(filepath.Join(input.Output, "mirror-signature.json"), 1<<20)
	if err != nil {
		t.Fatal("local QA actual authenticated image endorsement unavailable")
	}
	plan, err := distribution.VerifyMirrorPlan(ctx, payload, signatureBytes, "mirror", signer.trustDirectory, projection.Version, projection.BundleDigest, projection.BundleCatalogDigest, sha(bytes))
	if err != nil {
		t.Fatal("local QA runtime mapping requires unchanged actual image endorsement")
	}
	agentRepository, agentDigest, err := contentSubject(projection.AgentImage)
	if err != nil {
		t.Fatal("local QA source-bound candidate agent identity invalid")
	}
	agentTarget := mirrorRepository(input.MirrorTarget, agentRepository) + "@" + agentDigest
	agentFound := false
	for _, rewrite := range plan.Rewrites {
		if rewrite.Source == projection.AgentImage {
			if agentFound || rewrite.Kind != "image" || rewrite.Target != agentTarget {
				t.Fatal("local QA candidate agent mapping differs from signed selected-profile image closure")
			}
			agentFound = true
		}
	}
	if !agentFound {
		t.Fatal("local QA candidate agent missing from actual signed selected-profile image inventory")
	}

	before, err := os.Lstat(input.FluxLayout)
	if err != nil || !before.IsDir() || before.Mode().Perm() != 0700 {
		t.Fatal("local QA genuine owned Flux layout absent")
	}
	source, err := oci.New(input.FluxLayout)
	if err != nil {
		t.Fatal("local QA original Flux layout unavailable")
	}
	target, err := oci.New(filepath.Join(input.Output, "layout"))
	if err != nil {
		t.Fatal("local QA graph layout unavailable")
	}
	var mutex sync.Mutex
	total := int64(0)
	seen := map[string]bool{}
	options := oras.DefaultCopyOptions
	options.Concurrency = 2
	options.PreCopy = func(_ context.Context, descriptor ocispec.Descriptor) error {
		mutex.Lock()
		defer mutex.Unlock()
		if descriptor.Digest.Validate() != nil || descriptor.Digest.Algorithm().String() != "sha256" || descriptor.Size <= 0 || descriptor.Size > distribution.MaximumArchiveBytes || len(descriptor.URLs) != 0 || len(descriptor.Data) != 0 {
			return errors.New("local QA Flux descriptor invalid")
		}
		if !seen[descriptor.Digest.String()] {
			if len(seen) >= 256 || descriptor.Size > (512<<20)-total {
				return errors.New("local QA Flux graph bounded aggregate exhausted")
			}
			total += descriptor.Size
			seen[descriptor.Digest.String()] = true
		}
		return nil
	}
	copied, err := oras.Copy(ctx, source, subject, target, subject, options)
	if err != nil || copied.Digest.String() != subject {
		t.Fatal("local QA unchanged complete Flux graph copy failed")
	}
	after, err := os.Lstat(input.FluxLayout)
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("local QA Flux input layout replaced")
	}
	original, err := boundedFile(filepath.Join(input.Output, "layout", "blobs", "sha256", strings.TrimPrefix(subject, "sha256:")), 1<<20)
	if err != nil || sha(original) != subject {
		t.Fatal("local QA original Flux manifest changed")
	}
	var artifact ocispec.Manifest
	if closedJSON(original, &artifact) != nil || len(artifact.Layers) != 1 {
		t.Fatal("local QA selected Flux artifact shape unsupported")
	}
	if artifact.Layers[0].Digest.String() != release.Flux.Distribution.ContentDigest {
		t.Fatal("local QA actual Flux archive differs from exact selected release")
	}
	signature, err := signer.signSubject(ctx, filepath.Join(input.Output, "layout"), repository, subject)
	if err != nil {
		t.Fatal("local QA actual Flux certificate verification failed")
	}
	targetRepository := mirrorRepository(input.MirrorTarget, repository)
	localQACopyStoredSubject(t, ctx, input, targetRepository, subject, signature)
	owner, err := resolver.CaptureDatabaseRegistryTransport(ctx, input.Registry.Trust)
	if err != nil {
		t.Fatal("local QA Flux pull transport unavailable")
	}
	defer owner.Close()
	content, verified, err := resolver.FetchVerifiedOCIArtifact(ctx, owner.Client(), targetRepository, subject, artifact.Layers[0].MediaType, signer.policy, signer.trustDirectory, distribution.MaximumArchiveBytes)
	if err != nil || verified.Status != "verified" || sha(content) != release.Flux.Distribution.ContentDigest {
		t.Fatal("local QA complete unchanged Flux certificate/payload readback failed")
	}
	mapping := localQAReleaseMapping{SchemaVersion: 1, ReleaseVersion: projection.Version, ReleaseManifestDigest: sha(bytes), DestinationRegistry: strings.Split(input.MirrorTarget, "/")[0], RegistryRewrites: map[string]string{}, Entries: []localQAReleaseMappingEntry{}}
	for _, item := range []struct{ id, kind, source, target, tool string }{{"agent", "container_image", projection.AgentImage, "", "skopeo"}, {"flux", "oci_artifact", projection.FluxRepository + "@" + projection.FluxDigest, targetRepository + "@" + subject, "oras"}, {"builtins", "oci_artifact", projection.BundleRepository + "@" + projection.BundleDigest, parent + "@" + projection.BundleDigest, "oras"}} {
		sourceRepository, value, e := contentSubject(item.source)
		if e != nil {
			t.Fatal("local QA required runtime subject invalid")
		}
		destination := item.target
		if destination == "" {
			destination = mirrorRepository(input.MirrorTarget, sourceRepository) + "@" + value
		}
		if !localQAApprovedPullRepository(input.Registry.Trust.Read.Repositories, strings.Split(destination, "@")[0]) {
			t.Fatal("local QA runtime mirror exceeds independently approved pull scope")
		}
		mapping.RegistryRewrites[strings.Split(sourceRepository, "/")[0]] = mapping.DestinationRegistry
		mapping.Entries = append(mapping.Entries, localQAReleaseMappingEntry{ID: item.id, Kind: item.kind, Source: item.source, Target: destination, Digest: value, CopyTool: item.tool})
	}
	path := filepath.Join(input.Output, "release-mapping.json")
	if writeJSON(path, mapping) != nil {
		t.Fatal("local QA canonical runtime mapping write failed")
	}
	projected, err := releasecontract.ApplyMirrorMapping(path, releasePath, projection)
	if err != nil || projected.BundleRepository != parent || projected.FluxRepository != targetRepository {
		t.Fatal("local QA canonical runtime mapping readback failed")
	}
}
