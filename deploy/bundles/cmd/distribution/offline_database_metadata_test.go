package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// Called by the actual Cosign3 descriptor publication fixture after snapshotting
// original registry bytes under its mutex. This reuses its working public trap
// and QA key; it does not claim vendor OIDC or real supplier qualification.
func verifyActualOfflineDatabaseMetadata(t *testing.T, options distribution.OfflineOptions, subjects []distribution.StoredSubject, fullGraph map[string][]byte) {
	t.Helper()
	subjects = append([]distribution.StoredSubject(nil), subjects...)
	metadataGraph := map[string][]byte{}
	for subject, content := range fullGraph {
		metadataGraph[subject] = bytes.Clone(content)
	}
	// Parse only the signed parent payload already verified by the enclosing
	// canonical fixture; the metadata constructor independently authenticates it.
	bom, err := offlineDatabaseFixtureBOM(options, fullGraph)
	if err != nil {
		t.Fatal(err)
	}
	original := map[string][]byte{}
	var databaseConfig string
	for _, asset := range bom.Assets {
		if claim := asset.DatabaseDescriptor; claim != nil {
			var child ocispec.Manifest
			_, subject, ok := strings.Cut(asset.PublishedSubject, "@")
			if !ok {
				t.Fatal("QA database subject unavailable")
			}
			if err := closedJSON(fullGraph[subject], &child); err != nil {
				t.Fatal(err)
			}
			databaseConfig = child.Config.Digest.String()
			original[claim.Digest] = bytes.Clone(metadataGraph[claim.Digest])
			delete(metadataGraph, claim.Digest)
		}
	}
	if len(original) == 0 {
		t.Fatal("descriptor fixture has no database claims")
	}
	if _, _, err := distribution.VerifyOfflineStore(t.Context(), options, subjects, metadataGraph); err == nil {
		t.Fatal("v1 verifier accepted missing layers")
	}
	proof, err := distribution.VerifyOfflineDatabaseMetadata(t.Context(), options, subjects, metadataGraph)
	if err != nil {
		t.Fatal(err)
	}
	status := proof.DatabaseMetadataStatus()
	if status.Verification != "verified" || status.DatabasePayloadVerification != "descriptor-only" || status.DatabaseFreshness != "not-assessed" {
		t.Fatal("metadata authentication claimed payload/freshness qualification")
	}
	encoded, _, err := packOfflineDatabaseMetadata(t.Context(), options, subjects, metadataGraph)
	if err != nil {
		t.Fatal(err)
	}
	_, _, imported, err := readOfflineDatabaseMetadata(t.Context(), encoded, options)
	if err != nil {
		t.Fatal(err)
	}
	verifyActualOfflineDatabaseTransfer(t, options, encoded, original)
	verifyActualOfflineDatabaseExport(t, options, encoded, original)
	// Return values are copies. Neither caller mutation nor a returned claim can
	// change the constructor-produced file authority or its captured status.
	claims := imported.DatabaseLayers()
	for subject, source := range original {
		claim := claims[subject]
		claim.Size++
		claims[subject] = claim
		var output bytes.Buffer
		if err := imported.CopyDatabaseLayer(t.Context(), subject, bytes.NewReader(source), &output); err != nil || !bytes.Equal(output.Bytes(), source) {
			t.Fatal("original authenticated bytes cannot round-trip")
		}
		corrupted := bytes.Clone(source)
		corrupted[0] ^= 1
		for name, changed := range map[string][]byte{"changed": corrupted, "partial": source[:len(source)-1], "extra": append(bytes.Clone(source), 'x')} {
			if bytes.Equal(changed, source) {
				t.Fatal("negative bytes equal positive fixture")
			}
			t.Run("metadata-layer-"+name, func(t *testing.T) {
				if imported.CopyDatabaseLayer(t.Context(), subject, bytes.NewReader(changed), io.Discard) == nil {
					t.Fatal("invalid whole layer accepted")
				}
			})
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		output.Reset()
		if !errors.Is(imported.CopyDatabaseLayer(ctx, subject, bytes.NewReader(source), &output), context.Canceled) || output.Len() != 0 {
			t.Fatal("canceled layer wrote bytes")
		}
	}
	if imported.CopyDatabaseLayer(t.Context(), "sha256:"+string(bytes.Repeat([]byte{'0'}, 64)), bytes.NewReader([]byte("unendorsed")), io.Discard) == nil {
		t.Fatal("unendorsed layer granted staging authority")
	}
	var empty distribution.VerifiedOfflineDatabaseGraph
	if empty.DatabaseLayers() != nil || empty.DatabaseMetadataStatus().Verification != "failed" || empty.CopyDatabaseLayer(t.Context(), "", bytes.NewReader(nil), nil) == nil {
		t.Fatal("zero metadata object forged verification")
	}
	for _, mutation := range []string{"missing-config", "missing-signature", "changed-parent", "orphan"} {
		t.Run("metadata-"+mutation, func(t *testing.T) {
			changed := map[string][]byte{}
			for subject, content := range metadataGraph {
				changed[subject] = bytes.Clone(content)
			}
			switch mutation {
			case "missing-config":
				delete(changed, databaseConfig)
			case "missing-signature":
				delete(changed, subjects[0].SignatureDigest)
			case "changed-parent":
				changed[options.Digest][0] ^= 1
			case "orphan":
				changed[sha([]byte("orphan"))] = []byte("orphan")
			}
			if _, err := distribution.VerifyOfflineDatabaseMetadata(t.Context(), options, subjects, changed); err == nil {
				t.Fatal("invalid closed metadata authenticated")
			}
		})
	}
	wrongKey := options
	wrongKey.Policy.KeyRef = "not-approved"
	if _, err := distribution.VerifyOfflineDatabaseMetadata(t.Context(), wrongKey, subjects, metadataGraph); err == nil {
		t.Fatal("metadata accepted a different mounted trust key")
	}
	// Captured proof cannot follow later replacement of caller-owned bytes or
	// identity slices. Mutate only this helper's copies, leaving outer tests intact.
	metadataGraph[options.Digest][0] ^= 1
	subjects[0].Digest = "replacement"
	for subject, source := range original {
		if proof.CopyDatabaseLayer(t.Context(), subject, bytes.NewReader(source), nil) != nil {
			t.Fatal("captured proof followed later caller mutations")
		}
	}
	if proof.DatabaseMetadataStatus().DatabasePayloadVerification != "descriptor-only" {
		t.Fatal("streaming changed conservative metadata status")
	}
}

func offlineDatabaseFixtureBOM(options distribution.OfflineOptions, graph map[string][]byte) (distribution.BOM, error) {
	var manifest ocispec.Manifest
	if err := closedJSON(graph[options.Digest], &manifest); err != nil {
		return distribution.BOM{}, err
	}
	if len(manifest.Layers) != 1 {
		return distribution.BOM{}, errors.New("QA database parent has unexpected layers")
	}
	_, bom, err := distribution.ReadArchive(graph[manifest.Layers[0].Digest.String()], options.Release, options.CatalogDigest, options.Profile)
	return bom, err
}
