package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	builtinbundles "github.com/alphabravocompany/astronomer-go/deploy/bundles"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"oras.land/oras-go/v2/registry/remote"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	"github.com/sigstore/sigstore/pkg/cryptoutils"
	"github.com/sigstore/sigstore/pkg/signature"
)

func verifyActualMirrorPublication(t *testing.T, fixture *publicationRegistry, transfer []byte, options distribution.OfflineOptions, client *http.Client, platforms string) {
	t.Helper()
	target := "1.1.1.1/mirror"
	manifestDigest := sha([]byte("independently pinned QA release manifest"))
	schedule, _, _, err := planOfflineMirror(t.Context(), transfer, options, target, manifestDigest)
	if err != nil {
		t.Fatal(err)
	}
	repeated, _, _, err := planOfflineMirror(t.Context(), transfer, options, target, manifestDigest)
	first, _ := json.Marshal(schedule)
	second, _ := json.Marshal(repeated)
	if err != nil || !bytes.Equal(first, second) {
		t.Fatal("mirror preparation is not deterministic")
	}
	if len(schedule.Copies) != 5 && len(schedule.Copies) != 7 {
		t.Fatal("parent, all chart/database subjects and inventory image not closed")
	}
	if len(schedule.Copies) == 7 {
		if len(schedule.Assets) != 15 {
			t.Fatal("closed role mirror inventory incomplete")
		}
		for _, asset := range schedule.Assets {
			if asset.MemberDigest == "" || asset.MemberPath == "" {
				t.Fatal("role lacks original signed member identity")
			}
			for _, image := range asset.Images {
				if !strings.HasPrefix(image, "1.1.1.1/mirror/") {
					t.Fatal("role image not projected")
				}
			}
		}
	}
	verifyActualMirrorCLI(t, transfer, options, schedule)
	for _, copy := range schedule.Copies {
		if copy.Kind == "image" {
			repository, subject, _ := contentSubject(copy.Source)
			entries, err := os.ReadDir(platforms)
			if err != nil {
				t.Fatal(err)
			}
			fixture.mutex.Lock()
			for _, entry := range entries {
				data, err := os.ReadFile(filepath.Join(platforms, entry.Name()))
				if err != nil {
					fixture.mutex.Unlock()
					t.Fatal(err)
				}
				digest := "sha256:" + strings.TrimSuffix(entry.Name(), ".json")
				var document map[string]any
				_ = json.Unmarshal(data, &document)
				if document["schemaVersion"] != nil {
					fixture.manifests[strings.TrimPrefix(repository, "1.1.1.1/")+"/"+digest] = data
				} else {
					fixture.blobs[digest] = data
				}
			}
			fixture.blobs[sha([]byte("fixture"))] = []byte("fixture")
			fixture.mutex.Unlock()
			if fixture.manifestDigest(strings.TrimPrefix(repository, "1.1.1.1/")+"/"+subject) != subject {
				t.Fatal("QA image seed failed")
			}
		}
	}
	signer, key, err := signature.NewDefaultECDSASignerVerifier()
	if err != nil {
		t.Fatal(err)
	}
	public, err := cryptoutils.MarshalPublicKeyToPEM(key.Public())
	if err != nil {
		t.Fatal(err)
	}
	operatorTrust := t.TempDir()
	if err := os.WriteFile(filepath.Join(operatorTrust, "operator.pub"), public, 0600); err != nil {
		t.Fatal(err)
	}
	endorsement, _ := json.Marshal(schedule.Plan)
	raw, err := signer.SignMessage(bytes.NewReader(endorsement))
	if err != nil {
		t.Fatal(err)
	}
	execution := mirrorExecutionOptions{Vendor: options, TargetBase: target, ReleaseManifestDigest: manifestDigest, Endorsement: endorsement, Signature: []byte(base64.StdEncoding.EncodeToString(raw)), OperatorKeyRef: "operator", OperatorTrustDirectory: operatorTrust, Client: client, Network: httpclient.DestinationPolicy{Restricted: true, Origins: []httpclient.TrustedOrigin{{URL: "https://1.1.1.1", CIDRs: []netip.Prefix{netip.MustParsePrefix("1.1.1.1/32")}}}}}
	// Changed complete plan/signature cannot perform even one upload.
	beforeManifests, beforeBlobs := fixture.snapshot()
	beforeWrites := fixture.writes()
	changed := execution
	changed.Endorsement = append(bytes.Clone(endorsement), byte('x'))
	if _, err := copyOfflineMirror(t.Context(), transfer, changed); err == nil {
		t.Fatal("tampered endorsement accepted")
	}
	afterManifests, afterBlobs := fixture.snapshot()
	if len(beforeManifests) != len(afterManifests) || len(beforeBlobs) != len(afterBlobs) || fixture.writes() != beforeWrites {
		t.Fatal("invalid endorsement mutated registry")
	}
	// An authentically signed incomplete plan also fails before mutation.
	missing := schedule.Plan
	missing.Rewrites = missing.Rewrites[:len(missing.Rewrites)-1]
	missingBytes, _ := json.Marshal(missing)
	missingSignature, _ := signer.SignMessage(bytes.NewReader(missingBytes))
	changed.Endorsement = missingBytes
	changed.Signature = []byte(base64.StdEncoding.EncodeToString(missingSignature))
	if _, err := copyOfflineMirror(t.Context(), transfer, changed); err == nil {
		t.Fatal("signed incomplete mirror plan accepted")
	}
	if fixture.writes() != beforeWrites {
		t.Fatal("authentically signed incomplete mirror plan performed registry writes")
	}
	result, err := copyOfflineMirror(t.Context(), transfer, execution)
	if err != nil {
		t.Fatalf("original vendor graph mirror: %v", err)
	}
	for _, copy := range result.Copies {
		source, subject, _ := contentSubject(copy.Source)
		destination, _, _ := contentSubject(copy.Target)
		if fixture.manifestDigest(strings.TrimPrefix(destination, "1.1.1.1/")+"/"+subject) != subject {
			t.Fatal("mirrored subject digest changed")
		}
		if copy.Kind == "signed_artifact" {
			tag := "sha256-" + subject[7:] + ".sig"
			manifests, _ := fixture.snapshot()
			if !bytes.Equal(manifests[strings.TrimPrefix(source, "1.1.1.1/")+"/"+tag], manifests[strings.TrimPrefix(destination, "1.1.1.1/")+"/"+tag]) {
				t.Fatal("original vendor signature manifest changed")
			}
		}
	}
	plan, err := distribution.VerifyMirrorPlan(t.Context(), endorsement, execution.Signature, "operator", operatorTrust, options.Release, options.Digest, options.CatalogDigest, manifestDigest)
	if err != nil {
		t.Fatal(err)
	}
	index := subjectIndex{Repository: result.DistributionRepository, Digest: options.Digest, CatalogDigest: options.CatalogDigest, Release: options.Release}
	verifyActualKeyedConsumerOptions(t, index, client, options.TrustDirectory, plan, execution.Network)
	// Retry preserves the same digest and signatures; no resigning occurs.
	if _, err := copyOfflineMirror(t.Context(), transfer, execution); err != nil {
		t.Fatalf("immutable mirror retry: %v", err)
	}
	for _, base := range []string{"user:password@mirror.example.test/base", "1.1.1.1/mirror:latest", "1.1.1.1/mirror/../escape", "https://1.1.1.1/mirror"} {
		if _, _, _, err := planOfflineMirror(t.Context(), transfer, options, base, manifestDigest); err == nil {
			t.Fatal("unsafe mirror destination accepted")
		}
	}
	if mirrorRepository("mirror.example.test/root", "a.example.test/shared") == mirrorRepository("mirror.example.test/root", "b.example.test/shared") {
		t.Fatal("different upstream hosts collide")
	}
}

func TestMirrorImagePreflightRejectsAmbiguousAndUnavailableGraphs(t *testing.T) {
	_, _, _, platforms := producerFixture(t, "apiVersion: v1\nkind: Pod\nspec:\n  containers:\n  - image: {{ .Values.image }}\n")
	fixture := &publicationRegistry{manifests: map[string][]byte{}, blobs: map[string][]byte{}}
	var root []byte
	entries, err := os.ReadDir(platforms)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		data, _ := os.ReadFile(filepath.Join(platforms, entry.Name()))
		var value map[string]any
		_ = json.Unmarshal(data, &value)
		if value["schemaVersion"] != nil {
			fixture.manifests["fixture/"+sha(data)] = data
		} else {
			fixture.blobs[sha(data)] = data
		}
		if value["manifests"] != nil {
			root = data
		}
	}
	fixture.blobs[sha([]byte("fixture"))] = []byte("fixture")
	server := httptest.NewServer(fixture)
	defer server.Close()
	target, _ := url.Parse(server.URL)
	transport := &publicationRoutingTransport{base: server.Client().Transport, target: target}
	client := &http.Client{Transport: transport, Timeout: 20 * time.Second}
	repository, err := remote.NewRepository("1.1.1.1/fixture")
	if err != nil {
		t.Fatal(err)
	}
	repository.Client = client
	if _, err := preflightImageGraph(t.Context(), repository, sha(root)); err != nil {
		t.Fatalf("valid multiarch control: %v", err)
	}
	for _, variant := range []string{"duplicate", "trailing", "external", "missing-architecture", "missing-layer", "oversized-layer"} {
		t.Run(variant, func(t *testing.T) {
			var index ocispec.Index
			_ = json.Unmarshal(root, &index)
			data := bytes.Clone(root)
			switch variant {
			case "duplicate":
				data = bytes.Replace(data, []byte(`"schemaVersion":2`), []byte(`"schemaVersion":2,"schemaVersion":2`), 1)
			case "trailing":
				data = append(data, []byte(` {}`)...)
			case "external":
				index.Manifests[0].URLs = []string{"https://public.qa.example.test/not-authorized"}
				data, _ = json.Marshal(index)
			case "missing-architecture":
				index.Manifests = index.Manifests[:1]
				data, _ = json.Marshal(index)
			case "missing-layer":
				fixture.mutex.Lock()
				delete(fixture.blobs, sha([]byte("fixture")))
				fixture.mutex.Unlock()
				defer func() {
					fixture.mutex.Lock()
					fixture.blobs[sha([]byte("fixture"))] = []byte("fixture")
					fixture.mutex.Unlock()
				}()
			case "oversized-layer":
				manifests, _ := fixture.snapshot()
				var child ocispec.Manifest
				_ = json.Unmarshal(manifests["fixture/"+index.Manifests[0].Digest.String()], &child)
				child.Layers[0].Size = distribution.MaximumArchiveBytes + 1
				changed, _ := json.Marshal(child)
				index.Manifests[0].Digest = digest.FromBytes(changed)
				index.Manifests[0].Size = int64(len(changed))
				fixture.mutex.Lock()
				fixture.manifests["fixture/"+sha(changed)] = changed
				fixture.mutex.Unlock()
				data, _ = json.Marshal(index)
			}
			fixture.mutex.Lock()
			fixture.manifests["fixture/"+sha(data)] = data
			before := fixture.uploadCount
			fixture.mutex.Unlock()
			if _, err := preflightImageGraph(t.Context(), repository, sha(data)); err == nil {
				t.Fatal("unqualified supplier graph accepted")
			}
			fixture.mutex.Lock()
			defer fixture.mutex.Unlock()
			if fixture.uploadCount != before {
				t.Fatal("preflight performed registry writes")
			}
		})
	}
	if transport.unexpected.Load() != 0 {
		t.Fatal("image preflight used external URL")
	}
}

// These small public QA members exercise role planning only. They are not the
// actual supplier operators, shell toolkit or database/runtime qualification.
func configureMirrorRoleFixture(t *testing.T, catalogFile, assetFile string) {
	t.Helper()
	data, err := os.ReadFile(catalogFile)
	if err != nil {
		t.Fatal(err)
	}
	var catalog builtinbundles.Catalog
	if err := json.Unmarshal(data, &catalog); err != nil {
		t.Fatal(err)
	}
	image := catalog.Components[0].Images[0]
	data, err = os.ReadFile(assetFile)
	if err != nil {
		t.Fatal(err)
	}
	var assets assetIndex
	if err := json.Unmarshal(data, &assets); err != nil {
		t.Fatal(err)
	}
	roles := map[string]string{"cnpg-operator": "operator", "cnpg-crds": "crd", "postgres-server": "database", "barman-cloud-plugin": "plugin", "barman-cloud-tools": "tool", "object-store": "runtime_image", "dr-shell": "tool", "valkey": "runtime_image", "dex": "runtime_image", "fluent-bit": "runtime_image", "charlie": "runtime_image", "application-catalog": "application_catalog", "distribution-registry": "runtime_image"}
	categories := map[string]bool{}
	for _, category := range assets.RequiredCategories {
		categories[category] = true
	}
	root := t.TempDir()
	for id, category := range roles {
		content := []byte("apiVersion: v1\nkind: Pod\nmetadata:\n  name: " + id + "\nspec:\n  containers:\n  - name: qa\n    image: " + image + "\n")
		images := []string{image}
		if id == "cnpg-crds" || id == "application-catalog" {
			content = []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: qa-" + id + "\n")
			images = []string{}
		}
		file := filepath.Join(root, id+".yaml")
		if err := os.WriteFile(file, content, 0600); err != nil {
			t.Fatal(err)
		}
		assets.Assets = append(assets.Assets, assetInput{ID: id, Category: category, Path: "assets/" + id + ".yaml", File: file, Digest: sha(content), Images: images})
		categories[category] = true
	}
	assets.Capabilities = append(assets.Capabilities, "platform-core", "application-catalog", "distribution-registry")
	assets.RequiredCategories = []string{}
	for category := range categories {
		assets.RequiredCategories = append(assets.RequiredCategories, category)
	}
	sort.Strings(assets.RequiredCategories)
	sort.Slice(assets.Assets, func(i, j int) bool { return assets.Assets[i].ID < assets.Assets[j].ID })
	if err := writeJSON(assetFile, assets); err != nil {
		t.Fatal(err)
	}
}

func verifyActualMirrorCLI(t *testing.T, transfer []byte, options distribution.OfflineOptions, schedule mirrorSchedule) {
	t.Helper()
	binary := os.Getenv("DISTRIBUTION_BIN")
	if binary == "" {
		t.Log("DISTRIBUTION_BIN not selected; actual mirror CLI preparation not qualified in this invocation")
		return
	}
	directory := t.TempDir()
	archive := filepath.Join(directory, "original.tar.gz")
	if err := os.WriteFile(archive, transfer, 0600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(directory, "prepared")
	arguments := []string{"--plan-offline-mirror", archive, "--repository", options.Repository, "--expected-distribution-digest", options.Digest, "--expected-catalog-digest", options.CatalogDigest, "--expected-release", options.Release, "--trust-directory", options.TrustDirectory, "--signature-provider", string(options.Policy.Provider), "--key-ref", options.Policy.KeyRef, "--mirror-target", "1.1.1.1/mirror", "--release-manifest-digest", schedule.Plan.ReleaseManifestDigest, "--mirror-output-directory", output}
	if err := exec.Command(binary, arguments...).Run(); err != nil {
		t.Fatal("selected compiled mirror preparation CLI failed")
	}
	expected, _ := json.Marshal(schedule.Plan)
	actual, err := os.ReadFile(filepath.Join(output, "mirror-plan.json"))
	if err != nil || !bytes.Equal(expected, actual) {
		t.Fatal("CLI output differs from authenticated complete plan")
	}
	failed := filepath.Join(directory, "rejected")
	changed := append([]string(nil), arguments...)
	for i, arg := range changed {
		if arg == "--mirror-output-directory" {
			changed[i+1] = failed
		}
		if arg == "--expected-catalog-digest" {
			changed[i+1] = sha([]byte("wrong catalog"))
		}
	}
	if err := exec.Command(binary, changed...).Run(); err == nil {
		t.Fatal("CLI accepted wrong pinned catalog")
	}
	if _, err := os.Stat(failed); !os.IsNotExist(err) {
		t.Fatal("unauthenticated CLI output created")
	}
}
