//go:build linux

package databasejob

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/alphabravocompany/astronomer-go/internal/releasecontract"
)

func projectedJobMaterialFixture(t *testing.T, privatePull bool) (string, string) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("actual projected-material root-init contract requires UID0 qualification")
	}
	source, target := filepath.Join(t.TempDir(), "projected"), filepath.Join(t.TempDir(), "authority")
	for _, dir := range []string{source, target, filepath.Join(source, "..2026-owned")} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	for _, member := range projectedPreparationMembers(privatePull) {
		path := filepath.Join(source, "..2026-owned", member.name)
		if err := os.WriteFile(path, []byte("owned-private-material-"+member.name), 0444); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0444); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("..data/"+member.name, filepath.Join(source, member.name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("..2026-owned", filepath.Join(source, "..data")); err != nil {
		t.Fatal(err)
	}
	return source, target
}
func TestOwnedProjectedMaterialUsesClosedEpochAndPrivateRegularCopies(t *testing.T) {
	for _, privatePull := range []bool{false, true} {
		t.Run(map[bool]string{false: "public", true: "private"}[privatePull], func(t *testing.T) {
			source, target := projectedJobMaterialFixture(t, privatePull)
			if err := copyProjectedPreparationMaterial(t.Context(), source, target, 0, privatePull, nil); err != nil {
				t.Fatal(err)
			}
			for _, member := range projectedPreparationMembers(privatePull) {
				path := filepath.Join(target, member.relative)
				info, err := os.Lstat(path)
				if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0400 {
					t.Fatal("copy remained projected, writable or unavailable")
				}
				material, err := os.ReadFile(path)
				if err != nil || string(material) != "owned-private-material-"+member.name {
					t.Fatal("exact unchanged projected bytes unavailable")
				}
			}
			for _, dir := range []string{target, filepath.Join(target, "trust")} {
				info, err := os.Lstat(dir)
				if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
					t.Fatal("private directory not sealed")
				}
			}
			if err := copyProjectedPreparationMaterial(t.Context(), source, target, 0, privatePull, nil); err == nil {
				t.Fatal("bootstrap adopted an existing output")
			}
		})
	}
}
func TestOwnedProjectedMaterialRefusesRotationLinksPartialAndCancellation(t *testing.T) {
	for _, name := range []string{"member-symlink", "member-fifo", "member-hardlink", "wrong-mode", "oversized", "extra-key", "missing-key", "epoch-escape", "epoch-rotation", "existing-output", "canceled"} {
		t.Run(name, func(t *testing.T) {
			source, target := projectedJobMaterialFixture(t, true)
			member := filepath.Join(source, "..2026-owned", "pull.json")
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			var hook func()
			switch name {
			case "member-symlink", "member-fifo":
				if err := os.Remove(member); err != nil {
					t.Fatal(err)
				}
				if name == "member-symlink" {
					if err := os.Symlink("registry.pem", member); err != nil {
						t.Fatal(err)
					}
				} else if err := unix.Mkfifo(member, 0444); err != nil {
					t.Fatal(err)
				}
			case "member-hardlink":
				if err := os.Link(member, filepath.Join(source, "outside-member")); err != nil {
					t.Fatal(err)
				}
			case "wrong-mode":
				if err := os.Chmod(member, 0644); err != nil {
					t.Fatal(err)
				}
			case "oversized":
				if err := os.Chmod(member, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(member, []byte(strings.Repeat("x", (32<<10)+1)), 0444); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(member, 0444); err != nil {
					t.Fatal(err)
				}
			case "extra-key":
				if err := os.WriteFile(filepath.Join(source, "..2026-owned", "foreign.json"), []byte("foreign"), 0444); err != nil {
					t.Fatal(err)
				}
			case "missing-key":
				if err := os.Remove(member); err != nil {
					t.Fatal(err)
				}
			case "epoch-escape":
				if err := os.Remove(filepath.Join(source, "..data")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("../outside", filepath.Join(source, "..data")); err != nil {
					t.Fatal(err)
				}
			case "epoch-rotation":
				changed := false
				hook = func() {
					if changed {
						return
					}
					changed = true
					if err := os.Remove(filepath.Join(source, "..data")); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink("..2026-rotated", filepath.Join(source, "..data")); err != nil {
						t.Fatal(err)
					}
				}
			case "existing-output":
				if err := os.WriteFile(filepath.Join(target, "unowned"), []byte("preserve"), 0400); err != nil {
					t.Fatal(err)
				}
			case "canceled":
				cancel()
			}
			started := time.Now()
			err := copyProjectedPreparationMaterial(ctx, source, target, 0, true, hook)
			if err == nil {
				t.Fatal("changed/nonregular/unowned input was copied successfully")
			}
			if time.Since(started) > time.Second && name == "member-fifo" {
				t.Fatal("substituted FIFO blocked material reader")
			}
			if name == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
			if name == "epoch-rotation" {
				if !strings.Contains(err.Error(), "rotated") {
					t.Fatalf("rotation missed current epoch: %v", err)
				}
				if data, readErr := os.ReadFile(filepath.Join(target, "release.json")); readErr != nil || string(data) != "owned-private-material-release.json" {
					t.Fatal("exact owned partial output was deleted")
				}
			}
			if name == "existing-output" {
				if data, readErr := os.ReadFile(filepath.Join(target, "unowned")); readErr != nil || string(data) != "preserve" {
					t.Fatal("bootstrap overwrote or deleted unowned target")
				}
			}
		})
	}
}

// This positive exercises the production UID rather than a root-owned stand-in.
// It characterizes ownership DATA only; actual nonroot canonical consumption and
// selected read-only SQLite scanning remain separate runtime requirements.
func TestOwnedProjectedMaterialAssignsSelectedJobUID(t *testing.T) {
	for _, privatePull := range []bool{false, true} {
		t.Run(map[bool]string{false: "public", true: "private"}[privatePull], func(t *testing.T) {
			source, target := projectedJobMaterialFixture(t, privatePull)
			if err := copyProjectedPreparationMaterial(t.Context(), source, target, JobProcessUID, privatePull, nil); err != nil {
				t.Fatal(err)
			}
			paths := []string{target, filepath.Join(target, "trust")}
			for _, member := range projectedPreparationMembers(privatePull) {
				paths = append(paths, filepath.Join(target, member.relative))
			}
			for _, path := range paths {
				var info unix.Stat_t
				if err := unix.Lstat(path, &info); err != nil {
					t.Fatal(err)
				}
				mode := uint32(0400)
				if info.Mode&unix.S_IFMT == unix.S_IFDIR {
					mode = 0700
				} else if info.Mode&unix.S_IFMT != unix.S_IFREG || info.Nlink != 1 {
					t.Fatal("selected UID copy remained nonregular or linked")
				}
				if info.Uid != JobProcessUID || info.Gid != 0 || info.Mode&07777 != mode {
					t.Fatal("selected nonroot owner or exact mode was not installed")
				}
			}
		})
	}
}

func addProjectedJobReleaseMapping(t *testing.T, source string) []byte {
	t.Helper()
	payload := []byte(`{"schema_version":1,"locator_fixture":"unchanged-release-bytes"}`)
	releasePath := filepath.Join(source, "..2026-owned", "release.json")
	if err := os.Chmod(releasePath, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(releasePath, payload, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(releasePath, 0444); err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	entries := make([]map[string]string, 0, 3)
	for index, name := range []string{"agent", "flux", "bundle"} {
		entries = append(entries, map[string]string{"id": strings.Repeat(string("abc"[index]), 16), "kind": "container_image", "copy_tool": "skopeo", "source": "public.example.test/" + name + "@" + digest, "target": "mirror.example.test/" + name + "@" + digest, "digest": digest})
	}
	mapping, err := json.Marshal(map[string]any{"schema_version": 1, "release_version": "v1.2.1", "release_manifest_digest": fmt.Sprintf("sha256:%x", sha256.Sum256(payload)), "destination_registry": "mirror.example.test", "registry_rewrites": map[string]string{"public.example.test": "mirror.example.test"}, "entries": entries})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(source, "..2026-owned", "release-mapping.json")
	if err := os.WriteFile(path, mapping, 0444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0444); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("..data/release-mapping.json", filepath.Join(source, "release-mapping.json")); err != nil {
		t.Fatal(err)
	}
	return mapping
}

func TestOwnedProjectedMappedMaterialPreservesCanonicalLocatorAndJobUID(t *testing.T) {
	for _, privatePull := range []bool{false, true} {
		t.Run(map[bool]string{false: "public", true: "private"}[privatePull], func(t *testing.T) {
			source, target := projectedJobMaterialFixture(t, privatePull)
			original := addProjectedJobReleaseMapping(t, source)
			if err := copyProjectedPreparationMaterialWithMapping(t.Context(), source, target, JobProcessUID, privatePull, true, nil); err != nil {
				t.Fatal(err)
			}
			mappingPath := filepath.Join(target, "release-mapping.json")
			actual, err := os.ReadFile(mappingPath)
			if err != nil || string(actual) != string(original) {
				t.Fatal("original mapping bytes changed in bootstrap")
			}
			for _, path := range []string{mappingPath, target, filepath.Join(target, "trust")} {
				var stat unix.Stat_t
				if err := unix.Lstat(path, &stat); err != nil {
					t.Fatal(err)
				}
				mode := uint32(0400)
				if stat.Mode&unix.S_IFMT == unix.S_IFDIR {
					mode = 0700
				} else if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Nlink != 1 {
					t.Fatal("mapped output is linked or nonregular")
				}
				if stat.Uid != JobProcessUID || stat.Gid != 0 || stat.Mode&07777 != mode {
					t.Fatal("mapped material lost the production UID or strict mode")
				}
			}
			// Only locator comparison DATA is constructed here. The fixture is not a
			// valid release/signature; canonical Load/parent authentication is separate.
			// This exercises the SAME digest-preserving mapping called by config.Load.
			digest := "sha256:" + strings.Repeat("a", 64)
			projection := releasecontract.Projection{Version: "v1.2.1", AgentImage: "public.example.test/agent@" + digest, FluxRepository: "public.example.test/flux", FluxDigest: digest, BundleRepository: "public.example.test/bundle", BundleDigest: digest}
			rewritten, err := releasecontract.ApplyMirrorMapping(mappingPath, filepath.Join(target, "release.json"), projection)
			if err != nil || rewritten.BundleRepository != "mirror.example.test/bundle" || rewritten.BundleDigest != digest || rewritten.FluxDigest != digest || rewritten.AgentImage != "mirror.example.test/agent@"+digest {
				t.Fatal("canonical copied mapping did not retain immutable subjects")
			}
			if err := copyProjectedPreparationMaterialWithMapping(t.Context(), source, target, JobProcessUID, privatePull, true, nil); err == nil {
				t.Fatal("mapped bootstrap adopted existing output")
			}
		})
	}
}

func TestOwnedProjectedMappedMaterialRejectsIncompleteUnsafeAndRotatedScope(t *testing.T) {
	for _, name := range []string{"legacy-extra", "missing-mapping", "member-symlink", "member-hardlink", "wrong-mode", "oversized", "epoch-rotation", "canceled"} {
		t.Run(name, func(t *testing.T) {
			source, target := projectedJobMaterialFixture(t, true)
			addProjectedJobReleaseMapping(t, source)
			path := filepath.Join(source, "..2026-owned", "release-mapping.json")
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			mapped := true
			var hook func()
			switch name {
			case "legacy-extra":
				mapped = false
			case "missing-mapping":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "member-symlink":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("release.json", path); err != nil {
					t.Fatal(err)
				}
			case "member-hardlink":
				if err := os.Link(path, filepath.Join(source, "outside-mapping")); err != nil {
					t.Fatal(err)
				}
			case "wrong-mode":
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			case "oversized":
				if err := os.Chmod(path, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(strings.Repeat("x", (1<<20)+1)), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, 0444); err != nil {
					t.Fatal(err)
				}
			case "epoch-rotation":
				copied := 0
				hook = func() {
					copied++
					if copied == 8 {
						if err := os.Remove(filepath.Join(source, "..data")); err != nil {
							t.Fatal(err)
						}
						if err := os.Symlink("..2026-changed", filepath.Join(source, "..data")); err != nil {
							t.Fatal(err)
						}
					}
				}
			case "canceled":
				cancel()
			}
			err := copyProjectedPreparationMaterialWithMapping(ctx, source, target, JobProcessUID, true, mapped, hook)
			if err == nil {
				t.Fatal("incomplete/unsafe/rotated mapping was accepted")
			}
			if name == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatalf("mapped bootstrap lost cancellation: %v", err)
			}
			if name == "epoch-rotation" {
				if !strings.Contains(err.Error(), "rotated") {
					t.Fatalf("mapped bootstrap missed final epoch rotation: %v", err)
				}
				if _, err := os.Lstat(filepath.Join(target, "release-mapping.json")); err != nil {
					t.Fatal("bootstrap deleted its original owned partial mapping")
				}
			}
			if name == "legacy-extra" || name == "missing-mapping" {
				entries, err := os.ReadDir(target)
				if err != nil || len(entries) != 0 {
					t.Fatal("closed member mismatch copied material before refusal")
				}
			}
		})
	}
}
