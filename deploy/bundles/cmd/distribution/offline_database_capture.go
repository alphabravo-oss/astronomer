package main

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
)

// Capture unchanged published metadata with the canonical vendor verifier.
// Layer bytes come from digest-derived regular original prepared files, not a
// mutable upstream locator. The writer must fully hash those files and verify
// the emitted kit again; a successful metadata capture is not payload acceptance.
func captureOfflineDatabaseTransfer(ctx context.Context, directory string, options distribution.OfflineOptions, client *http.Client, network httpclient.DestinationPolicy) ([]byte, map[string]string, error) {
	data, err := boundedFile(filepath.Join(directory, "subjects.json"), 1<<20)
	var index subjectIndex
	if err != nil || closedJSON(data, &index) != nil || index.SchemaVersion != 2 || index.MediaType != distribution.MediaType || index.Repository != options.Repository || index.Digest != options.Digest || index.CatalogDigest != options.CatalogDigest || index.Release != options.Release || options.Profile != "builtins" || client == nil {
		return nil, nil, errors.New("database export differs from caller-pinned prepared identity")
	}
	if options.Policy.AllowUnsigned || options.Policy.Validate() != nil {
		return nil, nil, errors.New("mandatory original vendor policy required")
	}
	subjects, err := databaseTransferSubjects(index)
	if err != nil {
		return nil, nil, err
	}
	stored := make([]distribution.StoredSubject, 0, len(subjects))
	graph := &capturedGraph{blobs: map[string][]byte{}}
	for _, subject := range subjects {
		retained, err := captureDatabaseTransferSubject(ctx, client, network, options, subject, graph)
		if err != nil {
			return nil, nil, err
		}
		stored = append(stored, retained)
	}
	metadata, proof, err := packOfflineDatabaseMetadata(ctx, options, stored, graph.blobs)
	if err != nil {
		return nil, nil, err
	}
	files := make(map[string]string, len(proof.DatabaseLayers()))
	for subject := range proof.DatabaseLayers() {
		files[subject] = filepath.Join(directory, "blobs", "sha256", strings.TrimPrefix(subject, "sha256:"))
	}
	return metadata, files, nil
}

type databaseTransferSubject struct {
	repository string
	digest     string
	database   bool
}

func databaseTransferSubjects(index subjectIndex) ([]databaseTransferSubject, error) {
	if len(index.Children)+len(index.Assets)+1 > distribution.MaximumMembers {
		return nil, errors.New("database export subject count exceeds bounds")
	}
	all := []databaseTransferSubject{{repository: index.Repository, digest: index.Digest}}
	for _, child := range index.Children {
		all = append(all, databaseTransferSubject{repository: child.Repository, digest: child.Digest})
	}
	for _, asset := range index.Assets {
		database := asset.ID == "trivy-database" || asset.ID == "trivy-java-database"
		if database {
			expected := "application/vnd.aquasec.trivy.db.layer.v1.tar+gzip"
			if asset.ID == "trivy-java-database" {
				expected = "application/vnd.aquasec.trivy.javadb.layer.v1.tar+gzip"
			}
			if asset.MediaType != expected {
				return nil, errors.New("database export media type invalid")
			}
		}
		all = append(all, databaseTransferSubject{repository: asset.Repository, digest: asset.Digest, database: database})
	}
	seen := map[string]bool{}
	for _, subject := range all {
		identity := subject.repository + "@" + subject.digest
		if seen[identity] {
			return nil, errors.New("database export subject repeated")
		}
		seen[identity] = true
	}
	return all, nil
}

func captureDatabaseTransferSubject(ctx context.Context, client *http.Client, network httpclient.DestinationPolicy, options distribution.OfflineOptions, subject databaseTransferSubject, graph *capturedGraph) (distribution.StoredSubject, error) {
	var empty distribution.StoredSubject
	if err := network.ValidateURL(ctx, "https://"+subject.repository); err != nil {
		return empty, errors.New("export subject outside operator network policy")
	}
	if verification, err := resolver.VerifyPinnedOCI(ctx, client, subject.repository, subject.digest, options.Policy, options.TrustDirectory); err != nil || verification.Status != "verified" {
		return empty, errors.New("export subject signature invalid")
	}
	repository, err := remote.NewRepository(subject.repository)
	if err != nil {
		return empty, errors.New("export repository invalid")
	}
	repository.Client = &auth.Client{Client: client}
	repository.MaxMetadataBytes = 1 << 20
	if _, err := captureManifestGraph(ctx, repository, subject.digest, graph, false, subject.database); err != nil {
		return empty, err
	}
	signatureTag := "sha256-" + strings.TrimPrefix(subject.digest, "sha256:") + ".sig"
	signatureDigest, err := captureManifestGraph(ctx, repository, signatureTag, graph, true, false)
	if err != nil {
		return empty, err
	}
	return distribution.StoredSubject{Repository: subject.repository, Digest: subject.digest, SignatureDigest: signatureDigest}, nil
}
