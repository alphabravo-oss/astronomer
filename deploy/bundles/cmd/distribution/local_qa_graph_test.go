//go:build unix && integration

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime/debug"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	"github.com/alphabravocompany/astronomer-go/internal/releasecontract"
	"github.com/alphabravocompany/astronomer-go/pkg/version"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// Inputs are an explicit ROOT-owned ephemeral qualification kit. Its catalog,
// charts, image platform evidence and genuine complete DB files must already
// exist. Missing real inputs fail; no miniature or fixture replacement exists.

type localQAGraphInput struct {
	Scope             string                                    `json:"scope"`
	SourceCommit      string                                    `json:"source_commit"`
	Catalog           string                                    `json:"catalog"`
	Charts            string                                    `json:"charts"`
	Assets            string                                    `json:"assets"`
	Platforms         string                                    `json:"platforms"`
	ReleaseTemplate   string                                    `json:"release_template"`
	FluxLayout        string                                    `json:"flux_layout"`
	Repository        string                                    `json:"repository"`
	MirrorTarget      string                                    `json:"mirror_target"`
	Registry          localQARegistryInput                      `json:"registry"`
	Output            string                                    `json:"output"`
	FeedRepository    string                                    `json:"feed_repository"`
	ExportCache       string                                    `json:"export_cache"`
	OfflineKit        string                                    `json:"offline_kit"`
	Scanner           distribution.DatabaseScannerCompatibility `json:"scanner"`
	SupplierPolicy    httpclient.DestinationPolicy              `json:"supplier_policy"`
	SupplierCA        string                                    `json:"supplier_ca"`
	ImageSourcePolicy httpclient.DestinationPolicy              `json:"image_source_policy"`
	ImageSourceCA     string                                    `json:"image_source_ca"`
	Databases         []localQASupplierInput                    `json:"databases"`
}
type localQASupplierInput struct {
	Role       string `json:"role"`
	Repository string `json:"repository"`
	Subject    string `json:"subject"`
	Payload    string `json:"payload"`
}
type localQAGraphReceipt struct {
	Scope                                 string                                  `json:"scope"`
	SourceCommit                          string                                  `json:"source_commit"`
	Subjects                              subjectIndex                            `json:"subjects"`
	Signed                                []distribution.StoredSubject            `json:"signed"`
	SupplierSnapshots                     []distribution.SupplierDatabaseSnapshot `json:"supplier_snapshots"`
	TrustedRootDigest                     string                                  `json:"trusted_root_digest"`
	ReleaseManifestDigest                 string                                  `json:"release_manifest_digest"`
	CanonicalOfflineSignatureVerification bool                                    `json:"canonical_offline_signature_verification"`
	OperatorMirrorIdentity                string                                  `json:"operator_mirror_identity"`
	MirroredRepository                    string                                  `json:"mirrored_repository"`
	CanonicalTLSRegistryReadback          bool                                    `json:"canonical_tls_registry_readback"`
	ProductionOIDCExecuted                bool                                    `json:"production_oidc_executed"`
	ProductionReleaseApproved             bool                                    `json:"production_release_approved"`
	NativeScannerQualified                bool                                    `json:"native_scanner_qualified"`
	NewAppQualified                       bool                                    `json:"new_app_qualified"`
	OfflineFeedDigest                     string                                  `json:"offline_feed_digest"`
	OfflineKit                            string                                  `json:"offline_kit"`
}

func TestOwnedLocalQACertificateGraphForCurrentCandidate(t *testing.T) {
	// Fail instead of skipping: ROOT supplies one finite explicit kit per lane.
	inputPath := os.Getenv("W04_LOCAL_QA_GRAPH_INPUT")
	data, err := boundedFile(inputPath, 128<<10)
	if err != nil {
		t.Fatal("local QA graph inputs unavailable")
	}
	var input localQAGraphInput
	if closedJSON(data, &input) != nil || input.Scope != localQACertificateLabel || input.SourceCommit != localQACandidateCompiledSource(t) || !filepath.IsAbs(input.Output) || filepath.Clean(input.Output) != input.Output || len(input.Databases) != 2 {
		t.Fatal("local QA graph constructor incomplete")
	}
	for _, value := range input.SourceCommit {
		if !(value >= '0' && value <= '9' || value >= 'a' && value <= 'f') {
			t.Fatal("local QA source commit invalid")
		}
	}
	for _, path := range []string{input.Catalog, input.Charts, input.Assets, input.Platforms, input.ReleaseTemplate, input.FluxLayout, input.ExportCache, input.OfflineKit} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			t.Fatal("local QA input path not owned absolute")
		}
	}
	if _, err := os.Lstat(input.Output); !os.IsNotExist(err) {
		t.Fatal("local QA output must be absent")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Minute)
	defer cancel()
	// No snapshot, key or output exists before full exact supplier inspection.
	actual, proofs := localQAInspectSuppliers(t, ctx, input)
	result, err := produce(input.Catalog, input.Charts)
	if err != nil {
		t.Fatal("local QA canonical render failed")
	}
	release, _, err := releasecontract.Load(input.ReleaseTemplate, result.Catalog.Release)
	if err != nil || release.Release.SourceCommit != input.SourceCommit {
		t.Fatal("local QA canonical source release template differs")
	}
	if release.Release.SigningPolicy.CertificateOIDCIssuer != "https://token.actions.githubusercontent.com" || release.Release.SigningPolicy.CertificateIdentity != "https://github.com/alphabravo-oss/astronomer/.github/workflows/release.yaml@refs/tags/"+result.Catalog.Release {
		t.Fatal("local QA literal production contract identity differs")
	}
	if err = os.Mkdir(input.Output, 0700); err != nil {
		t.Fatal("local QA exclusive output creation failed")
	}
	// Failures preserve partial owned files for the ROOT held runner; no broad cleanup.
	layout := filepath.Join(input.Output, "layout")
	subjects, err := buildLayout(result, input.Charts, layout, input.Repository, input.Assets, input.Platforms)
	if err != nil {
		t.Fatal("local QA canonical original graph failed")
	}
	localQABindSupplierSubjects(t, actual, subjects)
	signer, err := newLocalQACertificateSigner(filepath.Join(input.Output, "trust"), release.Release.SigningPolicy.CertificateIdentity, release.Release.SigningPolicy.CertificateOIDCIssuer)
	if err != nil {
		t.Fatal("local QA owned certificate signer unavailable")
	}
	signed := []distribution.StoredSubject{}
	appendSubject := func(repository, subject string) {
		t.Helper()
		signature, err := signer.signSubject(ctx, layout, repository, subject)
		if err != nil {
			t.Fatal("local QA actual subject certificate verification failed")
		}
		signed = append(signed, distribution.StoredSubject{Repository: repository, Digest: subject, SignatureDigest: signature})
	}
	appendSubject(subjects.Repository, subjects.Digest)
	for _, child := range subjects.Children {
		appendSubject(child.Repository, child.Digest)
	}
	for _, asset := range subjects.Assets {
		appendSubject(asset.Repository, asset.Digest)
	}
	blobs := localQAMetadataBlobs(t, layout, signed, actual)
	options := distribution.OfflineOptions{Repository: subjects.Repository, Digest: subjects.Digest, CatalogDigest: subjects.CatalogDigest, Release: subjects.Release, Profile: "builtins", TrustDirectory: signer.trustDirectory, Policy: signer.policy}
	graph, err := distribution.VerifyOfflineDatabaseMetadata(ctx, options, signed, blobs)
	if err != nil || len(graph.DatabaseLayers()) != 2 {
		t.Fatal("local QA canonical closed graph certificate verification failed")
	}
	for _, snapshot := range actual {
		claim, ok := graph.DatabaseLayers()[snapshot.Layer.Digest]
		if !ok || claim != snapshot.Layer {
			t.Fatal("local QA signed DB bytes differ from inspected original")
		}
	}
	// Canonical base release DATA now describes this ephemeral signed graph only.
	// The historical release template is never modified or published.
	release.BuiltInBundles.Artifact.Reference = subjects.Repository + "@" + subjects.Digest
	release.BuiltInBundles.Artifact.ContentDigest = subjects.ArchiveDigest
	release.BuiltInBundles.Artifact.Evidence.Signature = "cosign://" + release.BuiltInBundles.Artifact.Reference
	release.BuiltInBundles.Artifact.Evidence.SBOM = "cosign-attestation://" + release.BuiltInBundles.Artifact.Reference + "#spdxjson"
	release.BuiltInBundles.Artifact.Evidence.Provenance = "cosign-attestation://" + release.BuiltInBundles.Artifact.Reference + "#slsaprovenance"
	release.BuiltInBundles.CatalogDigest = subjects.CatalogDigest
	release.BuiltInBundles.Components = nil
	for _, component := range result.Catalog.Components {
		release.BuiltInBundles.Components = append(release.BuiltInBundles.Components, struct {
			Slug         string   `json:"slug"`
			Chart        string   `json:"chart"`
			ChartVersion string   `json:"chart_version"`
			ChartDigest  string   `json:"chart_digest"`
			Images       []string `json:"images"`
		}{Slug: component.Slug, Chart: component.Source.Chart, ChartVersion: component.Source.Version, ChartDigest: component.Source.ChartDigest, Images: append([]string(nil), component.Images...)})
	}
	releasePath := filepath.Join(input.Output, "release.json")
	if writeJSON(releasePath, release) != nil {
		t.Fatal("local QA release output unavailable")
	}
	if _, _, err = releasecontract.Load(releasePath, subjects.Release); err != nil {
		t.Fatal("local QA generated release violates unchanged contract")
	}
	releaseBytes, err := boundedFile(releasePath, 1<<20)
	if err != nil {
		t.Fatal("local QA release readback unavailable")
	}
	mirrorIdentity := localQAWriteMirror(t, ctx, input.Output, input.MirrorTarget, options, signed, blobs, sha(releaseBytes))
	mirroredRepository := localQACopyAndReadback(t, ctx, input, options, signed, blobs)
	localQACopyFluxAndRuntimeMapping(t, ctx, input, releasePath, signer, mirroredRepository)
	feed, kit := localQAPrepareSignedUpdateAndExport(t, ctx, input, options, subjects, signer, proofs, mirrorIdentity, mirroredRepository)
	receipt := localQAGraphReceipt{OfflineFeedDigest: feed, OfflineKit: kit, Scope: localQACertificateLabel, SourceCommit: input.SourceCommit, Subjects: subjects, Signed: signed, SupplierSnapshots: actual, TrustedRootDigest: sha(signer.rootBytes), ReleaseManifestDigest: sha(releaseBytes), CanonicalOfflineSignatureVerification: true, OperatorMirrorIdentity: mirrorIdentity, MirroredRepository: mirroredRepository, CanonicalTLSRegistryReadback: true}
	if writeJSON(filepath.Join(input.Output, "LOCAL-QA-CERTIFICATE-VERIFICATION.json"), receipt) != nil || writeJSON(filepath.Join(input.Output, "signed-subjects.json"), signed) != nil {
		t.Fatal("local QA owned receipt unavailable")
	}
	t.Log(localQACertificateLabel + ": actual original parent/charts/DB certificates, SCT and Rekor bundles verified; native scanner and NewApp remain unqualified")
}

func localQAInspectSuppliers(t *testing.T, ctx context.Context, input localQAGraphInput) ([]distribution.SupplierDatabaseSnapshot, map[string]*distribution.VerifiedSupplierDatabasePayload) {
	t.Helper()
	if !input.SupplierPolicy.Restricted || input.SupplierPolicy.Validate() != nil {
		t.Fatal("local QA supplier origins must be explicitly restricted")
	}
	tls, err := httpclient.TLSForCA(input.SupplierCA)
	if err != nil {
		t.Fatal("local QA supplier CA invalid")
	}
	client, err := httpclient.NewDestinationClientContext(ctx, input.SupplierPolicy, httpclient.DestinationOptions{Timeout: time.Minute, TLSConfig: tls, MaxResponseBytes: 1 << 20, MaxRedirects: 0})
	if err != nil {
		t.Fatal("local QA canonical guarded metadata client unavailable")
	}
	defer client.CloseIdleConnections()
	var assets assetIndex
	data, err := boundedFile(input.Assets, 1<<20)
	if err != nil || closedJSON(data, &assets) != nil || assets.SchemaVersion != 3 {
		t.Fatal("local QA genuine descriptor asset input required")
	}
	selected := map[string]assetInput{}
	for _, asset := range assets.Assets {
		if asset.ID == "trivy-database" || asset.ID == "trivy-java-database" {
			if _, ok := selected[asset.ID]; ok {
				t.Fatal("local QA duplicate DB role")
			}
			selected[asset.ID] = asset
		}
	}
	actual := []distribution.SupplierDatabaseSnapshot{}
	proofs := map[string]*distribution.VerifiedSupplierDatabasePayload{}
	seen := map[string]bool{}
	for _, role := range input.Databases {
		if seen[role.Role] || role.Role != "trivy-database" && role.Role != "trivy-java-database" {
			t.Fatal("local QA supplier role differs")
		}
		seen[role.Role] = true
		asset, ok := selected[role.Role]
		if !ok || asset.File != role.Payload || asset.ArtifactSubject != role.Repository+"@"+role.Subject {
			t.Fatal("local QA supplied assets do not bind exact original supplier")
		}
		inspector, err := distribution.NewSupplierDatabaseInspector(distribution.SupplierDatabaseOptions{Repository: role.Repository, Role: role.Role, Scanner: input.Scanner, Timing: distribution.CadenceDatabaseUpdateTiming(), Network: input.SupplierPolicy, Client: client})
		if err != nil {
			t.Fatal("local QA original supplier constructor unavailable")
		}
		captured, err := inspector.Capture(ctx)
		if err != nil {
			t.Fatal("local QA actual supplier metadata capture failed")
		}
		snapshot, manifest, config, ok := captured.Snapshot()
		if !ok || snapshot.Manifest.Digest != role.Subject {
			t.Fatal("local QA immutable supplier changed; no tag substitution")
		}
		expectedManifest, e := boundedFile(asset.ArtifactManifestFile, 1<<20)
		if e != nil || !bytes.Equal(expectedManifest, manifest) {
			t.Fatal("local QA original supplier manifest differs")
		}
		expectedConfig, e := boundedFile(asset.ArtifactConfigFile, 1<<20)
		if e != nil || !bytes.Equal(expectedConfig, config) {
			t.Fatal("local QA original supplier config differs")
		}
		verified, e := inspector.Inspect(ctx, captured, role.Payload, time.Now().UTC())
		if e != nil {
			t.Fatal("local QA genuine whole supplier archive inspection failed")
		}
		snapshot, ok = verified.Snapshot()
		if !ok {
			t.Fatal("local QA whole supplier proof absent")
		}
		actual = append(actual, snapshot)
		proofs[role.Role] = verified
	}
	return actual, proofs
}
func localQABindSupplierSubjects(t *testing.T, actual []distribution.SupplierDatabaseSnapshot, subjects subjectIndex) {
	t.Helper()
	for _, snapshot := range actual {
		found := false
		for _, asset := range subjects.Assets {
			if asset.ID == snapshot.Role {
				if found || asset.OriginalSubject != snapshot.SupplierSubject || asset.Digest != snapshot.Manifest.Digest || asset.ArchiveDigest != snapshot.Layer.Digest {
					t.Fatal("local QA canonical build changed supplier bytes")
				}
				found = true
			}
		}
		if !found {
			t.Fatal("local QA canonical graph omitted supplier role")
		}
	}
}
func localQAMetadataBlobs(t *testing.T, layout string, signed []distribution.StoredSubject, actual []distribution.SupplierDatabaseSnapshot) map[string][]byte {
	t.Helper()
	blobs := map[string][]byte{}
	large := map[string]bool{}
	for _, s := range actual {
		large[s.Layer.Digest] = true
	}
	var total int64
	add := func(value string, maximum int64) {
		t.Helper()
		if _, ok := blobs[value]; ok {
			return
		}
		d := digest.Digest(value)
		if d.Validate() != nil || d.Algorithm() != digest.SHA256 {
			t.Fatal("local QA graph descriptor invalid")
		}
		data, err := boundedFile(filepath.Join(layout, "blobs", "sha256", d.Encoded()), maximum)
		if err != nil || sha(data) != value {
			t.Fatal("local QA graph descriptor content changed")
		}
		total += int64(len(data))
		if total > 256<<20 {
			t.Fatal("local QA small graph aggregate exceeds bound")
		}
		blobs[value] = data
	}
	for _, subject := range signed {
		for _, value := range []string{subject.Digest, subject.SignatureDigest} {
			add(value, 1<<20)
			var manifest ocispec.Manifest
			if closedJSON(blobs[value], &manifest) != nil {
				t.Fatal("local QA manifest changed")
			}
			add(manifest.Config.Digest.String(), 1<<20)
			for _, layer := range manifest.Layers {
				if !large[layer.Digest.String()] {
					add(layer.Digest.String(), distribution.MaximumArchiveBytes)
				}
			}
		}
	}
	return blobs
}

// The source comes from the compiled production version package, never env or
// fixture reassignment. ROOT's held build binds actual source/tool/argv/image
// identities. When Go stamps VCS information, it must independently agree and
// be clean; go test binaries without VCS stamps retain the explicit build
// receipt prerequisite rather than pretending their info contains a revision.
func localQACandidateCompiledSource(t *testing.T) string {
	t.Helper()
	source := version.GitCommit
	if len(source) != 40 {
		t.Fatal("actual compiled candidate source identity missing")
	}
	for _, value := range source {
		if !(value >= '0' && value <= '9' || value >= 'a' && value <= 'f') {
			t.Fatal("actual compiled candidate source identity invalid")
		}
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		revisionSeen, modifiedSeen := false, false
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				if revisionSeen || setting.Value != source {
					t.Fatal("actual native VCS and production source identity differ")
				}
				revisionSeen = true
			case "vcs.modified":
				if modifiedSeen || setting.Value != "false" {
					t.Fatal("actual native VCS source is modified or ambiguous")
				}
				modifiedSeen = true
			}
		}
		if revisionSeen && !modifiedSeen {
			t.Fatal("actual native VCS cleanliness unavailable")
		}
	}
	return source
}
