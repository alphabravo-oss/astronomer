//go:build linux

package upgradecli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Decoder/file tests here operate only AFTER the production cryptographic gate
// by contract. They do not fake a signed result or qualify actual stage crypto.
// The shared verifier's separate local virtual-CA tests own its crypto proof.
func TestSignedReleaseMetadataDecodesOnlyCanonicalExactAuthenticatedBytes(t *testing.T) {
	manifest, _ := canonicalHelmManifestFixture("v1.2.2")
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeAuthenticatedManifest(raw, "v1.2.2"); err != nil {
		t.Fatal("canonical authenticated payload refused", err)
	}
	for _, mode := range []string{"unknown", "duplicate", "trailing", "wrong-target", "publisher", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			changed := append([]byte(nil), raw...)
			target := "v1.2.2"
			switch mode {
			case "unknown":
				changed = append([]byte(`{"unsupported":true,`), raw[1:]...)
			case "duplicate":
				changed = append([]byte(`{"schema_version":1,`), raw[1:]...)
			case "trailing":
				changed = append(changed, []byte(` {}`)...)
			case "wrong-target":
				target = "v1.2.3"
			case "publisher":
				changed = []byte(strings.ReplaceAll(string(raw), "release.yaml@refs/tags/v1.2.2", "foreign.yaml@refs/tags/v1.2.2"))
			case "oversized":
				changed = make([]byte, (1<<20)+1)
			}
			if _, err := decodeAuthenticatedManifest(changed, target); err == nil {
				t.Fatal("unowned authenticated contract admitted")
			}
		})
	}
}

func TestSignedRecoveryRetainsExactHistoricalSourceAndFileDigests(t *testing.T) {
	manifest, _ := canonicalHelmManifestFixture(recoveryTag)
	manifest.Release.SourceCommit = recoverySourceCommit
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	schema := uint32(1)
	record := recoveryRecord{SchemaVersion: &schema, Tag: recoveryTag, SourceCommit: recoverySourceCommit, SourceRunID: recoverySourceRun, ProducerRunID: "123", ProducerCommit: strings.Repeat("b", 40), Files: map[string]string{"release-manifest.json": "sha256:" + hex.EncodeToString(sum[:]), "runtime-image-evidence.json": "sha256:" + strings.Repeat("c", 64), "runtime-image-evidence.tar.gz": "sha256:" + strings.Repeat("d", 64), "astronomer-1.2.0.tgz": "sha256:" + strings.Repeat("e", 64)}}
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := decodeAuthenticatedRecovery(encoded, manifest, raw, nil); err != nil {
		t.Fatal("exact historical proof refused", err)
	}
	for _, mode := range []string{"source", "run", "producer", "schema", "missing-file", "extra-file", "wrong-manifest", "present-file", "unknown", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			var changed recoveryRecord
			if err := json.Unmarshal(encoded, &changed); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "source":
				changed.SourceCommit = strings.Repeat("f", 40)
			case "run":
				changed.SourceRunID = "2"
			case "producer":
				changed.ProducerRunID = "0"
			case "schema":
				changed.SchemaVersion = nil
			case "missing-file":
				delete(changed.Files, "astronomer-1.2.0.tgz")
			case "extra-file":
				changed.Files["arbitrary"] = "sha256:" + strings.Repeat("a", 64)
			case "wrong-manifest":
				changed.Files["release-manifest.json"] = "sha256:" + strings.Repeat("a", 64)
			}
			input, err := json.Marshal(changed)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "unknown" {
				input = append([]byte(`{"arbitrary":null,`), input[1:]...)
			}
			if mode == "duplicate" {
				input = append([]byte(`{"tag":"v1.2.0",`), input[1:]...)
			}
			var optional map[string][32]byte
			if mode == "present-file" {
				optional = map[string][32]byte{"runtime-image-evidence.json": sha256.Sum256([]byte("wrong present runtime evidence"))}
			}
			if err := decodeAuthenticatedRecovery(input, manifest, raw, optional); err == nil {
				t.Fatal("foreign historical source or content acknowledged")
			}
		})
	}
}

func TestSignedMetadataRejectsUnboundInputsAndIncompleteRecoveryBeforeTrust(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewSignedReleaseMetadata(ctx, "/private", "/trust", "v1.2.2", ArtifactPolicy{1}); !errors.Is(err, context.Canceled) {
		t.Fatal("caller cancellation identity lost")
	}
	if _, err := NewSignedReleaseMetadata(nil, "/private", "/trust", "v1.2.2", ArtifactPolicy{1}); err == nil {
		t.Fatal("nil context admitted")
	}
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "release-recovery.json"), []byte("incomplete private proof"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewSignedReleaseMetadata(context.Background(), directory, "/missing-trust", "v1.2.0", ArtifactPolicy{1}); err == nil {
		t.Fatal("incomplete recovery fell through to normal policy")
	}
}

func TestSignedMetadataPrivateArtifactHashRejectsAliasesBudgetsAndCancellation(t *testing.T) {
	for _, mode := range []string{"valid", "symlink", "hardlink", "oversized", "public", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "exact-private-artifact")
			raw := []byte("synthetic public release archive bytes")
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			maximum := int64(len(raw))
			switch mode {
			case "symlink":
				original := path + "-retained"
				if err := os.Rename(path, original); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(original, path); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(path, path+"-alias"); err != nil {
					t.Fatal(err)
				}
			case "oversized":
				maximum--
			case "public":
				if err := os.Chmod(path, 0o644); err != nil {
					t.Fatal(err)
				}
			case "cancel":
				cancel()
			}
			bound, err := bindMetadataArtifact(ctx, path, maximum)
			if mode == "valid" {
				if err != nil || bound.hash != sha256.Sum256(raw) {
					t.Fatal("exact private digest differs", err)
				}
			} else if err == nil {
				t.Fatal("unsafe private recovery artifact admitted")
			}
		})
	}
}
