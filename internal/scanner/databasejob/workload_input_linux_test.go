//go:build linux

package databasejob

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	digest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
)

func workloadInputFixture(t *testing.T, claims ...uuid.UUID) (*WorkloadInput, string, uuid.UUID, WorkloadInputSnapshot) {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	claim := uuid.New()
	if len(claims) > 0 {
		claim = claims[0]
	}
	layer := []byte("synthetic owned workload layer; not a real Trivy scan")
	config := []byte(`{"architecture":"amd64","os":"linux","config":{"Env":["PRIVATE_DEFAULT=not-public-status"]},"rootfs":{"type":"layers","diff_ids":["` + digest.FromBytes(layer).String() + `"]}}`)
	descriptor := ocispec.Descriptor{Digest: digest.FromBytes(layer), Size: int64(len(layer)), MediaType: ocispec.MediaTypeImageLayer}
	manifest := ocispec.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageManifest, Config: ocispec.Descriptor{Digest: digest.FromBytes(config), Size: int64(len(config)), MediaType: ocispec.MediaTypeImageConfig}, Layers: []ocispec.Descriptor{descriptor}}
	payload, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	subject := digest.FromBytes(payload)
	metadata := resolver.WorkloadImageMetadata{Manifest: payload, Config: config, ManifestDescriptor: ocispec.Descriptor{Digest: subject, Size: int64(len(payload)), MediaType: manifest.MediaType}, ConfigDescriptor: manifest.Config, Layers: manifest.Layers}
	owner, err := createWorkloadInput(t.Context(), root, claim, metadata, subject.String(), "amd64")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.Close)
	writer, existing, err := owner.createBlob(WorkloadInputMember{Name: "blobs/sha256/" + descriptor.Digest.Encoded(), Digest: descriptor.Digest.String(), Size: descriptor.Size})
	if err != nil || existing {
		t.Fatal("new immutable layer member unavailable")
	}
	if _, err := writer.Write(layer); err != nil {
		t.Fatal(err)
	}
	if err := owner.sealAndMeasure(t.Context()); err != nil {
		t.Fatal(err)
	}
	snapshot, err := owner.Measure(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return owner, root, claim, snapshot
}
func TestOwnedWorkloadInputSealsReopensAndRemeasuresLiteralOCIClosure(t *testing.T) {
	owner, root, claim, snapshot := workloadInputFixture(t)
	payload, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "PRIVATE_DEFAULT") || strings.Contains(string(payload), root) {
		t.Fatal("private config/path escaped comparison DATA")
	}
	copy := snapshot
	copy.Members = append([]WorkloadInputMember(nil), snapshot.Members...)
	reopened, err := ReopenWorkloadInput(t.Context(), root, claim, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	snapshot.Members[0].Digest = "sha256:" + strings.Repeat("f", 64)
	measured, err := reopened.Measure(t.Context())
	if err != nil || !reflect.DeepEqual(measured, copy) {
		t.Fatal("caller mutation changed immutable reopened content")
	}
	if _, err := owner.Measure(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"oci-layout", "index.json", "blobs", "blobs/sha256"} {
		info, err := os.Lstat(filepath.Join(owner.directory, name))
		if err != nil {
			t.Fatal(err)
		}
		expected := os.FileMode(0444)
		if info.IsDir() {
			expected = 0555
		}
		if info.Mode().Perm() != expected {
			t.Fatal("layout not sealed")
		}
	}
	// Valid original metadata must reach exclusive claim creation, rather than
	// merely failing an unrelated incomplete-metadata prerequisite.
	manifestBytes, err := os.ReadFile(filepath.Join(owner.directory, "blobs/sha256/"+strings.TrimPrefix(copy.ManifestDigest, "sha256:")))
	if err != nil {
		t.Fatal(err)
	}
	var manifest ocispec.Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	configBytes, err := os.ReadFile(filepath.Join(owner.directory, "blobs/sha256/"+manifest.Config.Digest.Encoded()))
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := resolver.ValidateWorkloadImageMetadata(manifestBytes, configBytes, copy.ManifestDigest, copy.Architecture)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := createWorkloadInput(t.Context(), root, claim, metadata, copy.ManifestDigest, copy.Architecture); err == nil || !strings.Contains(err.Error(), "exclusive staging") {
		t.Fatal("valid new producer adopted an existing claim")
	}
	if _, err := owner.Measure(t.Context()); err != nil {
		t.Fatal("existing complete input changed after refused adoption")
	}
}
func TestOwnedWorkloadInputRejectsPostOpenLinksModesReplacementAndUnownedMembers(t *testing.T) {
	for _, name := range []string{"hardlink", "file-mode", "special-mode", "directory-mode", "extra-file", "replaced-file", "changed-bytes", "removed-file", "owner"} {
		t.Run(name, func(t *testing.T) {
			owner, root, claim, snapshot := workloadInputFixture(t)
			reopened, err := ReopenWorkloadInput(t.Context(), root, claim, snapshot)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			member := snapshot.Members[len(snapshot.Members)-1]
			path := filepath.Join(owner.directory, member.Name)
			switch name {
			case "hardlink":
				if err := os.Link(path, filepath.Join(root, "foreign-link")); err != nil {
					t.Fatal(err)
				}
			case "file-mode":
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			case "special-mode":
				if err := os.Chmod(path, 0444|os.ModeSetuid); err != nil {
					t.Fatal(err)
				}
			case "directory-mode":
				if err := os.Chmod(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
			case "extra-file":
				if err := os.Chmod(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(filepath.Dir(path), "unowned"), []byte("foreign"), 0444); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(filepath.Dir(path), 0555); err != nil {
					t.Fatal(err)
				}
			case "replaced-file":
				original, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, original, 0444); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(filepath.Dir(path), 0555); err != nil {
					t.Fatal(err)
				}
			case "changed-bytes":
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(strings.Repeat("X", int(member.Size))), 0444); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, 0444); err != nil {
					t.Fatal(err)
				}
			case "removed-file":
				if err := os.Chmod(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(filepath.Dir(path), 0555); err != nil {
					t.Fatal(err)
				}
			case "owner":
				if os.Geteuid() != 0 {
					t.Log("UID mutation cannot execute without privilege; other ownership controls remain required")
					return
				}
				if err := os.Chown(path, 1, -1); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := reopened.Measure(t.Context()); err == nil {
				t.Fatal("changed post-open physical contract accepted")
			}
		})
	}
}
func TestOwnedWorkloadInputRejectsAmbiguousComparisonAndCancelledReads(t *testing.T) {
	owner, root, claim, snapshot := workloadInputFixture(t)
	for _, name := range []string{"traversal", "missing-index", "wrong-index", "duplicate", "oversize", "bad-digest", "wrong-platform"} {
		t.Run(name, func(t *testing.T) {
			changed := snapshot
			changed.Members = append([]WorkloadInputMember(nil), snapshot.Members...)
			switch name {
			case "traversal":
				changed.Members[0].Name = "../unowned"
			case "missing-index":
				changed.Members = changed.Members[2:]
			case "wrong-index":
				changed.IndexDigest = "sha256:" + strings.Repeat("f", 64)
			case "duplicate":
				changed.Members = append(changed.Members, changed.Members[0])
			case "oversize":
				changed.Members[len(changed.Members)-1].Size = maximumWorkloadInputBytes + 1
			case "bad-digest":
				changed.Members[0].Digest = "sha256:../../foreign"
			case "wrong-platform":
				changed.Architecture = "other"
			}
			if _, err := ReopenWorkloadInput(t.Context(), root, claim, changed); err == nil {
				t.Fatal("malformed comparison closure accepted")
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := owner.Measure(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("measurement lost canonical cancellation")
	}
	reader := workloadContextReader{ctx: ctx, reader: strings.NewReader("owned")}
	if _, err := io.ReadAll(reader); !errors.Is(err, context.Canceled) {
		t.Fatal("payload read lost canonical cancellation")
	}
}
