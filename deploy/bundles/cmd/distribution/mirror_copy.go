package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/oci"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials"
)

type mirrorExecutionOptions struct {
	Vendor                 distribution.OfflineOptions
	TargetBase             string
	ReleaseManifestDigest  string
	Endorsement            []byte
	Signature              []byte
	OperatorKeyRef         string
	OperatorTrustDirectory string
	Client                 *http.Client
	Network                httpclient.DestinationPolicy
}

// Mirror execution never accepts an edited unsigned copy schedule. Recompute
// from canonically verified vendor bytes, authenticate the exact operator plan,
// and preflight every destination/source graph before the first registry write.
func copyOfflineMirror(ctx context.Context, payload []byte, options mirrorExecutionOptions) (mirrorSchedule, error) {
	var empty mirrorSchedule
	options.Network = options.Network.Clone()
	schedule, index, blobs, err := planOfflineMirror(ctx, payload, options.Vendor, options.TargetBase, options.ReleaseManifestDigest)
	if err != nil {
		return empty, err
	}
	if options.Client == nil {
		return empty, errors.New("guarded mirror client required")
	}
	plan, err := distribution.VerifyMirrorPlan(ctx, options.Endorsement, options.Signature, options.OperatorKeyRef, options.OperatorTrustDirectory, index.Release, index.Digest, index.CatalogDigest, options.ReleaseManifestDigest)
	if err != nil {
		return empty, err
	}
	// The runtime verifier sets private authentication identity; compare public
	// shape using the owned serializer so added/removed/changed rewrites fail.
	temp, err := os.MkdirTemp("", "astronomer-mirror-")
	if err != nil {
		return empty, err
	}
	defer os.RemoveAll(temp)
	if err := writeJSON(filepath.Join(temp, "expected.json"), schedule.Plan); err != nil {
		return empty, err
	}
	if err := writeJSON(filepath.Join(temp, "actual.json"), plan); err != nil {
		return empty, err
	}
	expected, err := boundedFile(filepath.Join(temp, "expected.json"), 1<<20)
	if err != nil {
		return empty, err
	}
	actual, err := boundedFile(filepath.Join(temp, "actual.json"), 1<<20)
	if err != nil || !bytes.Equal(expected, actual) {
		return empty, errors.New("operator endorsement differs from complete verified mirror plan")
	}
	storeDirectory := filepath.Join(temp, "graph")
	if err := importOfflineTransferBytes(ctx, payload, storeDirectory, options.Vendor); err != nil {
		return empty, err
	}
	store, err := oci.New(storeDirectory)
	if err != nil {
		return empty, errors.New("verified mirror store unavailable")
	}
	credentialStore, err := credentials.NewStoreFromDocker(credentials.StoreOptions{})
	if err != nil {
		return empty, errors.New("protected mirror credentials unavailable")
	}
	credential := credentials.Credential(credentialStore)
	repositories := map[string]*remote.Repository{}
	sourceGraphs := map[string]imageGraph{}
	for _, copy := range schedule.Copies {
		targetRepository, _, err := contentSubject(copy.Target)
		if err != nil {
			return empty, err
		}
		if err := options.Network.ValidateURL(ctx, "https://"+targetRepository); err != nil {
			return empty, errors.New("mirror destination outside operator policy")
		}
		repository, err := remote.NewRepository(targetRepository)
		if err != nil {
			return empty, errors.New("mirror repository invalid")
		}
		repository.Client = &auth.Client{Client: options.Client, Credential: credential, Cache: auth.NewCache()}
		repository.MaxMetadataBytes = 1 << 20
		repositories[targetRepository] = repository
		if copy.Kind == "image" {
			sourceRepository, subject, _ := contentSubject(copy.Source)
			if err := options.Network.ValidateURL(ctx, "https://"+sourceRepository); err != nil {
				return empty, errors.New("mirror image source outside operator policy")
			}
			source, err := remote.NewRepository(sourceRepository)
			if err != nil {
				return empty, errors.New("mirror image source invalid")
			}
			source.Client = &auth.Client{Client: options.Client, Credential: credential, Cache: auth.NewCache()}
			source.MaxMetadataBytes = 1 << 20
			graph, err := preflightImageGraph(ctx, source, subject)
			if err != nil {
				return empty, err
			}
			sourceGraphs[copy.Source] = graph
		}
	}
	// Immutable signed children and their original legacy signatures precede
	// parent visibility. Neither vendor subject nor signature bytes are resigned.
	root := index.Repository + "@" + index.Digest
	ordered := append([]mirrorCopy(nil), schedule.Copies...)
	for i := range ordered {
		if ordered[i].Source == root {
			ordered[i], ordered[len(ordered)-1] = ordered[len(ordered)-1], ordered[i]
			break
		}
	}
	for _, copy := range ordered {
		targetRepository, subject, _ := contentSubject(copy.Target)
		destination := repositories[targetRepository]
		copyOptions := oras.DefaultCopyOptions
		copyOptions.Concurrency = 2
		if copy.Kind == "signed_artifact" {
			for _, reference := range []string{copy.SignatureDigest, subject} {
				targetReference := reference
				if reference == copy.SignatureDigest {
					targetReference = "sha256-" + strings.TrimPrefix(subject, "sha256:") + ".sig"
				}
				copied, err := oras.Copy(ctx, store, reference, destination, targetReference, copyOptions)
				if err != nil || copied.Digest.String() != reference {
					return empty, errors.New("original signed mirror graph copy failed")
				}
			}
		} else {
			graph := sourceGraphs[copy.Source]
			copyOptions.PreCopy = graph.checkDescriptor
			copied, err := oras.Copy(ctx, graph.repository, subject, destination, subject, copyOptions)
			if err != nil || copied.Digest.String() != subject {
				return empty, errors.New("bounded multiarch image mirror copy failed")
			}
		}
		resolved, err := destination.Resolve(ctx, subject)
		if err != nil || resolved.Digest.String() != subject {
			return empty, errors.New("mirror changed immutable subject identity")
		}
	}
	authenticated := &http.Client{Transport: registryAuthenticationTransport{client: &auth.Client{Client: options.Client, Credential: credential, Cache: auth.NewCache()}}, Timeout: options.Client.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	loader, err := distribution.New(distribution.Options{Repository: schedule.DistributionRepository, Digest: index.Digest, CatalogDigest: index.CatalogDigest, Release: index.Release, Profile: index.Profile, TrustDirectory: options.Vendor.TrustDirectory, Policy: options.Vendor.Policy, Client: authenticated, Network: options.Network, MirrorPlan: plan})
	if err != nil {
		return empty, err
	}
	if _, err := loader.Load(ctx); err != nil {
		return empty, errors.New("mirrored parent/children/vendor signatures failed canonical readback")
	}
	if !loader.Status().InventoryComplete || loader.Status().Verification != "verified" {
		return empty, errors.New("mirrored distribution remains unverified")
	}
	if err := verifyMirroredDatabasePayloads(ctx, index, blobs, plan, options, authenticated); err != nil {
		return empty, err
	}
	return schedule, nil
}

func importOfflineTransferBytes(ctx context.Context, payload []byte, destination string, options distribution.OfflineOptions) error {
	// The original bounded import owns its path contract; never hand arbitrary
	// member names to filesystem APIs. This temporary file contains public graph
	// bytes and is removed with its private parent directory.
	file := destination + ".tar.gz"
	if err := os.WriteFile(file, payload, 0600); err != nil {
		return err
	}
	defer os.Remove(file)
	return importOfflineTransfer(ctx, file, destination, options)
}
