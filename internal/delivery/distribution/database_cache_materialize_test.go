//go:build unix

package distribution

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The unit feed is deliberately assembled inside this package, not presented
// as a production signature or durable acceptance. Signed canonical fixtures
// remain necessary for the actual prepare-cache command/job path.
func databaseCacheUnitFixture(t *testing.T) (*AuthenticatedDatabaseUpdate, string, string, time.Time) {
	t.Helper()
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	main, _, mainBytes := databasePayloadUnitFixture(t, now, "trivy-database")
	java, _, javaBytes := databasePayloadUnitFixture(t, now, "trivy-java-database")
	main.document.Databases = append(main.document.Databases, java.document.Databases...)
	return main, databasePayloadOwnedFile(t, mainBytes), databasePayloadOwnedFile(t, javaBytes), now
}
func databaseCachePrivateRoot(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "owned-cache")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDatabaseCacheMaterializationMeasuresOriginalReadOnlyRoleFiles(t *testing.T) {
	update, main, java, now := databaseCacheUnitFixture(t)
	cache, err := MaterializeDatabaseCache(t.Context(), update, main, java, databaseCachePrivateRoot(t), now)
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	directory, valid := cache.Directory()
	if !valid {
		t.Fatal("owned materialized cache unavailable")
	}
	original := []byte("opaque QA database, not a supplier DB")
	for _, name := range []string{"db/trivy.db", "java-db/trivy-java.db"} {
		content, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil || string(content) != string(original) {
			t.Fatal("materialization changed database bytes")
		}
		info, err := os.Lstat(filepath.Join(directory, name))
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0444 {
			t.Fatal("cache file is not sealed read-only")
		}
	}
	snapshot, err := cache.Measure(t.Context())
	if err != nil || snapshot.FeedDigest != update.digest || snapshot.Main.Digest != contentDigest(original) || snapshot.Java.Digest != contentDigest(original) {
		t.Fatal("actual owned file measurement differs")
	}
	snapshot.Main.Digest = "caller-mutated"
	again, err := cache.Measure(t.Context())
	if err != nil || again.Main.Digest == "caller-mutated" {
		t.Fatal("returned measurement mutated retained owner")
	}
	cache.Close()
	if _, err := cache.Measure(t.Context()); err == nil {
		t.Fatal("closed owner supplied scan cache measurement")
	}
	if _, err := os.Stat(filepath.Join(directory, "db/trivy.db")); err != nil {
		t.Fatal("Close deleted retained original evidence")
	}
}

func TestDatabaseCacheInvalidInputRefusesAllStaging(t *testing.T) {
	for _, name := range []string{"changed-main", "changed-java", "missing-java", "canceled", "expired", "zero-proof", "symlink-root", "public-root"} {
		t.Run(name, func(t *testing.T) {
			update, main, java, now := databaseCacheUnitFixture(t)
			root := databaseCachePrivateRoot(t)
			inputRoot := root
			ctx := t.Context()
			switch name {
			case "changed-main":
				if err := os.WriteFile(main, []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "changed-java":
				if err := os.WriteFile(java, []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing-java":
				if err := os.Remove(java); err != nil {
					t.Fatal(err)
				}
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "expired":
				now = update.document.ExpiresAt.Add(time.Second)
			case "zero-proof":
				update = nil
			case "symlink-root":
				inputRoot = filepath.Join(filepath.Dir(root), "alias")
				if err := os.Symlink(root, inputRoot); err != nil {
					t.Fatal(err)
				}
			case "public-root":
				if err := os.Chmod(root, 0755); err != nil {
					t.Fatal(err)
				}
			}
			cache, err := MaterializeDatabaseCache(ctx, update, main, java, inputRoot, now)
			if err == nil || cache != nil {
				t.Fatal("invalid input returned a materialized cache")
			}
			if name == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatal("cache authentication lost cancellation")
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatal("input refusal staged cache members")
			}
		})
	}
	t.Run("aggregate-bound-before-staging", func(t *testing.T) {
		root := databaseCachePrivateRoot(t)
		for _, size := range []int64{0, -1, MaximumDatabaseExpandedBytes + 1, int64(1<<63 - 1)} {
			cache, err := createDatabaseCacheDestination(root, size)
			if err == nil || cache != nil {
				t.Fatal("invalid aggregate granted a private cache destination")
			}
			entries, readError := os.ReadDir(root)
			if readError != nil || len(entries) != 0 {
				t.Fatal("aggregate refusal staged a cache directory or member")
			}
		}
	})

}

func TestDatabaseCachePostMaterializationMutationCannotSupplyMeasurement(t *testing.T) {
	for _, name := range []string{"bytes", "replacement", "symlink", "metadata", "directory", "role-directory"} {
		t.Run(name, func(t *testing.T) {
			update, main, java, now := databaseCacheUnitFixture(t)
			cache, err := MaterializeDatabaseCache(t.Context(), update, main, java, databaseCachePrivateRoot(t), now)
			if err != nil {
				t.Fatal(err)
			}
			defer cache.Close()
			directory, _ := cache.Directory()
			roleDirectory := filepath.Join(directory, "db")
			path := filepath.Join(roleDirectory, "trivy.db")
			switch name {
			case "bytes":
				if err := os.Chmod(path, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, 0444); err != nil {
					t.Fatal(err)
				}
			case "replacement", "symlink":
				if err := os.Chmod(roleDirectory, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(path, path+".original"); err != nil {
					t.Fatal(err)
				}
				if name == "replacement" {
					if err := os.WriteFile(path, []byte("opaque QA database, not a supplier DB"), 0444); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := os.Symlink(path+".original", path); err != nil {
						t.Fatal(err)
					}
				}
			case "metadata":
				path = filepath.Join(roleDirectory, "metadata.json")
				if err := os.Chmod(path, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, 0444); err != nil {
					t.Fatal(err)
				}
			case "role-directory":
				if err := os.Chmod(directory, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(roleDirectory, roleDirectory+".original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(roleDirectory, 0555); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(directory, 0555); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Rename(directory, directory+".original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(directory, 0555); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := cache.Measure(t.Context()); err == nil {
				t.Fatal("changed cache returned accepted file measurement")
			}
		})
	}
}
