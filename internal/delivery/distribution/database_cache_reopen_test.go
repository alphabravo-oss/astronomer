//go:build unix

package distribution

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestDatabaseJobCacheUsesExclusiveLiteralClaimAndReopensMeasuredFiles(t *testing.T) {
	update, main, java, now := databaseCacheUnitFixture(t)
	root := databaseCachePrivateRoot(t)
	claim := uuid.MustParse("857e0a17-a815-49b8-b62c-52e404b00667")
	original, err := MaterializeDatabaseCacheForJob(t.Context(), update, main, java, root, claim, now)
	if err != nil {
		t.Fatal(err)
	}
	defer original.Close()
	snapshot, valid := original.Snapshot()
	if !valid {
		t.Fatal("cache owner invalid")
	}
	_, document, _ := update.Snapshot()
	var mm, jm DatabaseUpdateMetadata
	for _, row := range document.Databases {
		if row.Role == "trivy-database" {
			mm = row.Metadata
		} else {
			jm = row.Metadata
		}
	}
	reopened, err := OpenSealedDatabaseCache(t.Context(), root, claim, snapshot, mm, jm)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	actual, err := reopened.Measure(t.Context())
	if err != nil || actual != snapshot {
		t.Fatalf("actual measurement: %v", err)
	}
	directory, _ := original.Directory()
	if directory != filepath.Join(root, "database-cache-"+claim.String()) {
		t.Fatal("claim directory not literal")
	}
	if _, err := MaterializeDatabaseCacheForJob(t.Context(), update, main, java, root, claim, now); err == nil {
		t.Fatal("existing claim adopted")
	}
	actual, err = reopened.Measure(t.Context())
	if err != nil || actual != snapshot {
		t.Fatal("exclusive refusal changed old cache")
	}
}

func TestDatabaseJobCacheReopenRejectsForeignChangedAndLinkedData(t *testing.T) {
	for _, mode := range []string{"foreign-claim", "invalid-claim", "wrong-digest", "wrong-metadata", "symlink", "replacement", "hardlink", "public"} {
		t.Run(mode, func(t *testing.T) {
			update, main, java, now := databaseCacheUnitFixture(t)
			root := databaseCachePrivateRoot(t)
			claim := uuid.MustParse("857e0a17-a815-49b8-b62c-52e404b00667")
			owner, err := MaterializeDatabaseCacheForJob(t.Context(), update, main, java, root, claim, now)
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Close()
			snapshot, _ := owner.Snapshot()
			_, document, _ := update.Snapshot()
			var mm, jm DatabaseUpdateMetadata
			for _, row := range document.Databases {
				if row.Role == "trivy-database" {
					mm = row.Metadata
				} else {
					jm = row.Metadata
				}
			}
			directory, _ := owner.Directory()
			role := filepath.Join(directory, "db")
			member := filepath.Join(role, "trivy.db")
			switch mode {
			case "foreign-claim":
				claim = uuid.New()
			case "invalid-claim":
				claim = uuid.Nil
			case "wrong-digest":
				snapshot.Main.Digest = "sha256:" + strings.Repeat("b", 64)
			case "wrong-metadata":
				mm.Digest = "sha256:" + strings.Repeat("b", 64)
			case "public":
				if err := os.Chmod(member, 0644); err != nil {
					t.Fatal(err)
				}
			case "symlink", "replacement", "hardlink":
				if err := os.Chmod(role, 0700); err != nil {
					t.Fatal(err)
				}
				if mode == "hardlink" {
					if err := os.Link(member, filepath.Join(role, "second")); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := os.Rename(member, filepath.Join(role, "old")); err != nil {
						t.Fatal(err)
					}
					if mode == "symlink" {
						if err := os.Symlink("old", member); err != nil {
							t.Fatal(err)
						}
					} else {
						body, err := os.ReadFile(filepath.Join(role, "old"))
						if err != nil {
							t.Fatal(err)
						}
						body[0] ^= 1
						if err := os.WriteFile(member, body, 0444); err != nil {
							t.Fatal(err)
						}
					}
				}
				if err := os.Chmod(role, 0555); err != nil {
					t.Fatal(err)
				}
			}
			if actual, err := OpenSealedDatabaseCache(t.Context(), root, claim, snapshot, mm, jm); err == nil {
				actual.Close()
				t.Fatal("invalid reopen supplied measurement")
			}
			canceled, cancel := context.WithCancel(t.Context())
			cancel()
			if _, err := OpenSealedDatabaseCache(canceled, root, claim, snapshot, mm, jm); !errors.Is(err, context.Canceled) && mode != "invalid-claim" {
				t.Fatalf("context identity: %v", err)
			}
		})
	}
}

func TestDatabaseJobCacheFinalMeasureRechecksSealedOwnership(t *testing.T) {
	modes := []string{"file-hardlink", "file-public", "file-setuid", "file-sticky", "role-setgid", "cache-setuid"}
	if os.Geteuid() == 0 {
		modes = append(modes, "file-owner", "role-owner", "cache-owner")
	} else {
		t.Log("privileged owner mutation controls unavailable; no owner-change execution claimed")
	}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			update, main, java, now := databaseCacheUnitFixture(t)
			root := databaseCachePrivateRoot(t)
			claim := uuid.MustParse("857e0a17-a815-49b8-b62c-52e404b00667")
			cache, err := MaterializeDatabaseCacheForJob(t.Context(), update, main, java, root, claim, now)
			if err != nil {
				t.Fatal(err)
			}
			defer cache.Close()
			snapshot, _ := cache.Snapshot()
			_, document, _ := update.Snapshot()
			var mm, jm DatabaseUpdateMetadata
			for _, row := range document.Databases {
				if row.Role == "trivy-database" {
					mm = row.Metadata
				} else {
					jm = row.Metadata
				}
			}
			observer, err := OpenSealedDatabaseCache(t.Context(), root, claim, snapshot, mm, jm)
			if err != nil {
				t.Fatal(err)
			}
			defer observer.Close()
			directory, _ := cache.Directory()
			role := filepath.Join(directory, "db")
			member := filepath.Join(role, "trivy.db")
			switch mode {
			case "file-hardlink":
				if err = os.Chmod(role, 0700); err == nil {
					err = os.Link(member, filepath.Join(role, "second"))
				}
				if err == nil {
					err = os.Chmod(role, 0555)
				}
			case "file-public":
				err = os.Chmod(member, 0644)
			case "file-setuid":
				err = os.Chmod(member, 0444|os.ModeSetuid)
			case "file-sticky":
				err = os.Chmod(member, 0444|os.ModeSticky)
			case "role-setgid":
				err = os.Chmod(role, 0555|os.ModeSetgid)
			case "cache-setuid":
				err = os.Chmod(directory, 0555|os.ModeSetuid)
			case "file-owner":
				err = os.Chown(member, os.Geteuid()+1, -1)
			case "role-owner":
				err = os.Chown(role, os.Geteuid()+1, -1)
			case "cache-owner":
				err = os.Chown(directory, os.Geteuid()+1, -1)
			}
			if err != nil {
				t.Fatalf("controlled sealed mutation failed: %v", err)
			}
			if _, err = observer.Measure(t.Context()); err == nil {
				t.Fatal("unchanged bytes renewed changed physical ownership")
			}
		})
	}
}

// The second context check is performed by the canonical reader AFTER its
// first buffer read. This deterministic synchronous mutation proves the final
// ownership check rejects changed permissions even when complete bytes match.
type cacheReadMutationContext struct {
	context.Context
	checks        int
	mutate        func() error
	mutationError error
}

func (ctx *cacheReadMutationContext) Err() error {
	ctx.checks++
	if ctx.checks == 2 {
		ctx.mutationError = ctx.mutate()
	}
	return ctx.Context.Err()
}
func TestDatabaseJobCacheReadCannotRenewMidReadModeChange(t *testing.T) {
	root := databaseCachePrivateRoot(t)
	path := filepath.Join(root, "member")
	body := bytes.Repeat([]byte{'a'}, 128<<10)
	if err := os.WriteFile(path, body, 0444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0444); err != nil {
		t.Fatal(err)
	}
	original, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func(close func() error) { _ = close() }(original.Close)
	directory, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func(close func() error) { _ = close() }(directory.Close)
	sum := sha256.Sum256(body)
	ctx := &cacheReadMutationContext{Context: t.Context(), mutate: func() error { return os.Chmod(path, 0644) }}
	err = measureDatabaseCacheMember(ctx, directory, "member", original, "sha256:"+hex.EncodeToString(sum[:]), int64(len(body)))
	if ctx.checks < 2 || ctx.mutationError != nil {
		t.Fatalf("mid-read mutation not executed: checks=%d error=%v", ctx.checks, ctx.mutationError)
	}
	if err == nil {
		t.Fatal("complete matching bytes hid mid-read mode change")
	}
}
