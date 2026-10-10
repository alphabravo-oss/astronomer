package distribution

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

// These new supplier subjects deliberately have no Cosign signatures and no
// authenticated update feed. Only the operator constructor can capture them;
// a later configured publisher must endorse exact unchanged bytes separately.
func supplierDatabaseFixture(t *testing.T, role string, now time.Time, metadataEdit func([]byte) []byte) (*databaseUpdateRegistry, SupplierDatabaseOptions, []byte, DatabaseUpdateMetadata) {
	t.Helper()
	registry, document, loaderOptions, _ := databaseUpdateRegistryFixture(t, now)
	var snapshot DatabaseUpdateSnapshot
	for _, candidate := range document.Databases {
		if candidate.Role == role {
			snapshot = candidate
		}
	}
	metadata := databasePayloadMetadata(t, snapshot)
	if metadataEdit != nil {
		metadata = metadataEdit(metadata)
	}
	payload := encodeDatabasePayloadArchive(t, databasePayloadMembers(snapshot, metadata), nil)
	repository := "supplier/" + role
	subject := registry.fixture.addArtifact(repository, snapshot.Layer.MediaType, payload)
	tag, _, _ := supplierDatabaseRole(role)
	registry.fixture.mutex.Lock()
	registry.fixture.manifests[repository+"/"+tag] = append([]byte(nil), registry.fixture.manifests[repository+"/"+subject]...)
	registry.fixture.mutex.Unlock()
	options := SupplierDatabaseOptions{Repository: "1.1.1.1/" + repository, Role: role, Scanner: document.Binding.Scanner, Timing: loaderOptions.Timing, Network: loaderOptions.Network, Client: loaderOptions.Client}
	expected := snapshot.Metadata
	expected.Digest, expected.Size = contentDigest(metadata), int64(len(metadata))
	return registry, options, payload, expected
}

func TestSupplierDatabaseCaptureThenWholePayloadWithoutFabricatedFeed(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	for _, role := range []string{"trivy-database", "trivy-java-database"} {
		t.Run(role, func(t *testing.T) {
			registry, options, payload, expected := supplierDatabaseFixture(t, role, now, nil)
			inspector, err := NewSupplierDatabaseInspector(options)
			if err != nil {
				t.Fatal(err)
			}
			// Mutating the caller's policy cannot grant or remove authority.
			options.Network.Origins[0].URL = "https://unapproved.example.test"
			// Replacing the caller's HTTP transport must not replace this attempt's
			// captured constructor transport. Its internals remain operator-owned.
			unexpected := 0
			options.Client.Transport = blockedTransport{calls: &unexpected}
			captured, err := inspector.Capture(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			snapshot, manifest, config, ok := captured.Snapshot()
			if !ok || snapshot.Metadata != (DatabaseUpdateMetadata{}) || snapshot.Layer.Digest != contentDigest(payload) || snapshot.SupplierSubject != inspector.options.Repository+"@"+contentDigest(manifest) || !bytes.Equal(config, []byte("{}")) {
				t.Fatal("supplier capture did not preserve original descriptor-only scope")
			}
			// Change both returned bytes and the discovery tag after capture.
			manifest[0], config[0] = '!', '!'
			tag, _, _ := supplierDatabaseRole(role)
			registry.fixture.mutex.Lock()
			registry.fixture.manifests[strings.TrimPrefix(inspector.options.Repository, "1.1.1.1/")+"/"+tag] = []byte(`{"changed":"mutable tag"}`)
			registry.fixture.mutex.Unlock()
			verified, err := inspector.Inspect(t.Context(), captured, databasePayloadOwnedFile(t, payload), now)
			if err != nil {
				t.Fatal(err)
			}
			actual, ok := verified.Snapshot()
			if !ok || actual.Metadata != expected || actual.Layer != snapshot.Layer || actual.SupplierSubject != snapshot.SupplierSubject {
				t.Fatal("whole supplier payload metadata not derived from captured immutable content")
			}
			_, unchanged, unchangedConfig, _ := captured.Snapshot()
			if contentDigest(unchanged) != snapshot.Manifest.Digest || contentDigest(unchangedConfig) != snapshot.Config.Digest {
				t.Fatal("caller changed captured raw content")
			}
			layerPath := "/v2/" + strings.TrimPrefix(inspector.options.Repository, "1.1.1.1/") + "/blobs/" + snapshot.Layer.Digest
			if registry.readCount(layerPath) != 0 {
				t.Fatal("capture/owned-file inspection fetched layer bytes")
			}
			registry.mutex.Lock()
			for path := range registry.reads {
				if strings.Contains(path, "/manifests/sha256-") {
					registry.mutex.Unlock()
					t.Fatal("supplier adapter claimed an upstream signature")
				}
			}
			registry.mutex.Unlock()
			if unexpected != 0 {
				t.Fatal("caller replaced the captured operator transport")
			}
		})
	}
}

func TestSupplierDatabaseConstructorAuthorityRefusals(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	_, original, _, _ := supplierDatabaseFixture(t, "trivy-database", now, nil)
	for _, name := range []string{"missing-client", "missing-repository", "mutable-repository", "subject-repository", "unknown-role", "wrong-schema", "wrong-scanner", "missing-timing", "empty-restricted-policy"} {
		t.Run(name, func(t *testing.T) {
			options := original
			switch name {
			case "missing-client":
				options.Client = nil
			case "missing-repository":
				options.Repository = ""
			case "mutable-repository":
				options.Repository += ":latest"
			case "subject-repository":
				options.Repository += "@" + contentDigest([]byte("subject"))
			case "unknown-role":
				options.Role = "runtime-selected-role"
			case "wrong-schema":
				options.Scanner.MainSchema = 3
			case "wrong-scanner":
				options.Scanner.ImageDigest = "latest"
			case "missing-timing":
				options.Timing = DatabaseUpdateTiming{}
			case "empty-restricted-policy":
				options.Network = httpclient.DestinationPolicy{Restricted: true}
			}
			if inspector, err := NewSupplierDatabaseInspector(options); err == nil || inspector != nil {
				t.Fatal("invalid supplier constructor accepted")
			}
		})
	}
}

func TestSupplierDatabaseOpaqueAuthorityAndUnapprovedOrigin(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	registry, options, payload, _ := supplierDatabaseFixture(t, "trivy-database", now, nil)
	inspector, err := NewSupplierDatabaseInspector(options)
	if err != nil {
		t.Fatal(err)
	}
	captured, err := inspector.Capture(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	other, err := NewSupplierDatabaseInspector(options)
	if err != nil {
		t.Fatal(err)
	}
	path := databasePayloadOwnedFile(t, payload)
	for _, name := range []string{"other-constructor", "zero-capture", "relative-file", "canceled", "changed-file", "missing-file"} {
		t.Run(name, func(t *testing.T) {
			selected, input, candidate, ctx := inspector, path, captured, t.Context()
			switch name {
			case "other-constructor":
				selected = other
			case "zero-capture":
				candidate = &CapturedSupplierDatabase{}
			case "relative-file":
				input = "relative.tar.gz"
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "changed-file":
				changed := append([]byte(nil), payload...)
				changed[9] ^= 1
				input = databasePayloadOwnedFile(t, changed)
			case "missing-file":
				input = databasePayloadOwnedFile(t, payload)
				if os.Remove(input) != nil {
					t.Fatal("missing file preparation failed")
				}
			}
			proof, err := selected.Inspect(ctx, candidate, input, now)
			if err == nil || proof != nil {
				t.Fatal("unbound or changed supplier input accepted")
			}
			if name == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation identity lost")
			}
		})
	}
	if _, _, _, ok := (&CapturedSupplierDatabase{}).Snapshot(); ok {
		t.Fatal("zero capture forged authority")
	}
	if _, ok := (&VerifiedSupplierDatabasePayload{}).Snapshot(); ok {
		t.Fatal("zero payload forged authority")
	}
	deniedOptions := options
	deniedOptions.Repository = "8.8.8.8/unapproved"
	denied, err := NewSupplierDatabaseInspector(deniedOptions)
	if err != nil {
		t.Fatal(err)
	}
	before := registry.readCount("/v2/unapproved/manifests/2")
	if candidate, err := denied.Capture(t.Context()); err == nil || candidate != nil {
		t.Fatal("unapproved supplier origin captured")
	}
	if registry.readCount("/v2/unapproved/manifests/2") != before {
		t.Fatal("unapproved supplier reached transport")
	}
}

func TestSupplierDatabaseDerivesAndRejectsActualUpstreamMetadata(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	for _, name := range []string{"wrong-schema", "stale-updated", "elapsed-next", "future-updated", "non-utc", "unknown-key", "duplicate-key", "downloaded-not-freshness"} {
		t.Run(name, func(t *testing.T) {
			edit := func(raw []byte) []byte {
				var fields map[string]any
				if json.Unmarshal(raw, &fields) != nil {
					t.Fatal("metadata fixture invalid")
				}
				switch name {
				case "wrong-schema":
					fields["Version"] = 3
				case "stale-updated":
					fields["UpdatedAt"] = now.Add(-31 * time.Hour)
				case "elapsed-next":
					fields["NextUpdate"] = now.Add(-time.Minute)
				case "future-updated":
					fields["UpdatedAt"] = now.Add(time.Hour)
				case "non-utc":
					fields["UpdatedAt"] = now.In(time.FixedZone("offset", 3600))
				case "unknown-key":
					fields["SecretOrigin"] = "unapproved"
				case "downloaded-not-freshness":
					fields["UpdatedAt"] = now.Add(-31 * time.Hour)
					fields["DownloadedAt"] = now
				case "duplicate-key":
					return bytes.Replace(raw, []byte(`"Version":2`), []byte(`"Version":2,"Version":2`), 1)
				}
				modified, err := json.Marshal(fields)
				if err != nil {
					t.Fatal(err)
				}
				return modified
			}
			_, options, payload, _ := supplierDatabaseFixture(t, "trivy-database", now, edit)
			inspector, err := NewSupplierDatabaseInspector(options)
			if err != nil {
				t.Fatal(err)
			}
			captured, err := inspector.Capture(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if proof, err := inspector.Inspect(t.Context(), captured, databasePayloadOwnedFile(t, payload), now); err == nil || proof != nil {
				t.Fatal("invalid actual supplier metadata accepted")
			}
		})
	}
}
