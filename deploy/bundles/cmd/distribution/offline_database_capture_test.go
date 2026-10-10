package main

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

// Called inside the actual selected-Cosign descriptor publisher fixture. The
// network client, signed original registry bytes and independent external trap
// are the enclosing fixture's real composition, not mocked verification.
func verifyActualDatabaseCaptureAndFlags(t *testing.T, fixture *publicationRegistry, layout string, index subjectIndex, options distribution.OfflineOptions, client *http.Client, network httpclient.DestinationPolicy) {
	t.Helper()
	before := fixture.databaseExportBlobReads()
	metadata, files, err := captureOfflineDatabaseTransfer(t.Context(), layout, options, client, network)
	if err != nil {
		t.Fatal(err)
	}
	_, _, proof, err := readOfflineDatabaseMetadata(t.Context(), metadata, options)
	if err != nil {
		t.Fatal(err)
	}
	after := fixture.databaseExportBlobReads()
	for subject := range proof.DatabaseLayers() {
		if before[subject] != after[subject] {
			t.Fatal("metadata capture refetched the database payload")
		}
	}
	archive := filepath.Join(t.TempDir(), "database-v2.tar.gz")
	if err := writeOfflineDatabaseTransfer(t.Context(), archive, metadata, files, options); err != nil {
		t.Fatal(err)
	}
	if err := runDatabaseOfflineFlags("database-v2", archive, "", options); err != nil {
		t.Fatal("operator verification did not consume the canonical streamed format")
	}
	imported := filepath.Join(t.TempDir(), "owned-import")
	if err := runDatabaseOfflineFlags("database-v2", archive, imported, options); err != nil {
		t.Fatal(err)
	}
	for subject, original := range files {
		want, err := os.ReadFile(original)
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(imported, "blobs", "sha256", subject[7:]))
		if err != nil || !bytes.Equal(want, got) {
			t.Fatal("operator import lost original opaque database bytes")
		}
	}
	if runDatabaseOfflineFlags("v1", archive, "", options) == nil {
		t.Fatal("historical v1 reader silently selected the streamed format")
	}
	legacy, err := captureOfflineTransfer(t.Context(), layout, options, client, network)
	if err != nil {
		t.Fatal(err)
	}
	legacyArchive := filepath.Join(t.TempDir(), "legacy-v1.tar.gz")
	if err := os.WriteFile(legacyArchive, legacy, 0600); err != nil {
		t.Fatal(err)
	}
	if err := runDatabaseOfflineFlags("v1", legacyArchive, "", options); err != nil {
		t.Fatal("explicit v1 operator path changed for bounded original bytes")
	}
	if runDatabaseOfflineFlags("database-v2", legacyArchive, "", options) == nil {
		t.Fatal("streamed format silently selected the legacy reader")
	}
	for _, mutation := range []string{"unknown-format", "missing-file", "changed-file", "partial-file", "wrong-release", "wrong-key", "unsigned-child"} {
		t.Run("database-capture-cli-"+mutation, func(t *testing.T) {
			copyOptions := options
			newOutput := filepath.Join(t.TempDir(), "must-not-exist.tar.gz")
			if mutation == "unknown-format" {
				if runDatabaseOfflineFlags("unrecognized", archive, "", options) == nil {
					t.Fatal("unknown format accepted")
				}
				assertDatabaseExportCleanup(t, filepath.Dir(newOutput), newOutput)
				return
			}
			if mutation == "wrong-release" {
				copyOptions.Release = "v99.99.99"
			}
			if mutation == "wrong-key" {
				copyOptions.Policy.KeyRef = "not-approved"
			}
			var restore func()
			if mutation == "unsigned-child" {
				if len(index.Children) == 0 {
					t.Fatal("fixture has no published chart child")
				}
				child := index.Children[0]
				key := strings.TrimPrefix(child.Repository, "1.1.1.1/") + "/sha256-" + child.Digest[7:] + ".sig"
				fixture.mutex.Lock()
				original, exists := fixture.manifests[key]
				original = bytes.Clone(original)
				delete(fixture.manifests, key)
				fixture.mutex.Unlock()
				if !exists {
					t.Fatal("exact published child signature negative control absent")
				}
				restore = func() { fixture.mutex.Lock(); fixture.manifests[key] = original; fixture.mutex.Unlock() }
				defer restore()
			}

			document, sources, err := captureOfflineDatabaseTransfer(t.Context(), layout, copyOptions, client, network)
			if mutation == "missing-file" || mutation == "changed-file" || mutation == "partial-file" {
				if err != nil {
					t.Fatal(err)
				}
				for subject, path := range sources {
					if mutation == "missing-file" {
						sources[subject] = filepath.Join(t.TempDir(), "absent-original")
						break
					}
					content, readErr := os.ReadFile(path)
					if readErr != nil {
						t.Fatal(readErr)
					}
					if mutation == "changed-file" {
						content[0] ^= 1
					} else {
						content = content[:len(content)-1]
					}
					copy := filepath.Join(t.TempDir(), "changed-original")
					if err := os.WriteFile(copy, content, 0600); err != nil {
						t.Fatal(err)
					}
					sources[subject] = copy
					break
				}
				err = writeOfflineDatabaseTransfer(t.Context(), newOutput, document, sources, copyOptions)
			}
			if err == nil {
				t.Fatal("untrusted subject or changed original byte capture accepted")
			}
			assertDatabaseExportCleanup(t, filepath.Dir(newOutput), newOutput)
		})
	}
}

func (fixture *publicationRegistry) databaseExportBlobReads() map[string]int {
	fixture.mutex.Lock()
	defer fixture.mutex.Unlock()
	copy := make(map[string]int, len(fixture.blobReads))
	for subject, count := range fixture.blobReads {
		copy[subject] = count
	}
	return copy
}

func runDatabaseOfflineFlags(format, archive, imported string, options distribution.OfflineOptions) error {
	return offlineTransferFromFlags(format, "", "", archive, imported, options.Repository, options.Digest, options.CatalogDigest, options.Release, options.TrustDirectory, string(options.Policy.Provider), options.Policy.KeyRef, options.Policy.Identity, options.Policy.Issuer, "", "")
}

func TestOfflineFormatSelectionRejectsIgnoredOrMixedActions(t *testing.T) {
	for _, sample := range []struct {
		name, format               string
		mirror, transfer, accepted bool
	}{
		{"default-producer", "v1", false, false, true},
		{"historical-mirror", "v1", true, false, true},
		{"historical-transfer", "v1", false, true, true},
		{"streamed-transfer", "database-v2", false, true, true},
		{"streamed-without-action", "database-v2", false, false, false},
		{"streamed-mirror", "database-v2", true, false, false},
		{"mixed-mirror-transfer", "v1", true, true, false},
		{"unknown", "unknown", false, true, false},
	} {
		t.Run(sample.name, func(t *testing.T) {
			if (validateOfflineFormatSelection(sample.format, sample.mirror, sample.transfer) == nil) != sample.accepted {
				t.Fatal("incompatible or ignored format/action selection")
			}
		})
	}
}

func TestOfflineFormatsRejectNetworkCAAndOutputBeforeReadingInput(t *testing.T) {
	for _, format := range []string{"v1", "database-v2"} {
		for _, field := range []string{"network", "ca", "output"} {
			t.Run(format+"-"+field, func(t *testing.T) {
				directory := t.TempDir()
				var network, ca, output string
				switch field {
				case "network":
					network = filepath.Join(directory, "never-read-policy")
				case "ca":
					ca = filepath.Join(directory, "never-read-ca")
				case "output":
					output = filepath.Join(directory, "never-created-output")
				}
				err := offlineTransferFromFlags(format, "", output, "absent-input", "", "registry.example.test/distribution", "sha256:pin", "sha256:catalog", "v1.2.2", "/mounted/trust", "cosign_key", "vendor", "", "", network, ca)
				if err == nil || !strings.Contains(err.Error(), "offline verification does not accept network configuration") {
					t.Fatal("offline guard was deferred until input or network access")
				}
				entries, err := os.ReadDir(directory)
				if err != nil || len(entries) != 0 {
					t.Fatal("refused offline flags caused an output mutation")
				}
			})
		}
	}
}
