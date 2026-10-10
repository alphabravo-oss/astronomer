//go:build unix

package gitops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"golang.org/x/sys/unix"
)

func TestWalkCheckoutPreservesCanonicalPathsAndSkipsPresentSymlinks(t *testing.T) {
	repository := t.TempDir()
	outside := t.TempDir()
	if err := os.Mkdir(filepath.Join(repository, "clusters"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "clusters", "one.YAML"), []byte("owned content\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.yaml"), []byte("bare private content\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.yaml"), filepath.Join(repository, "clusters", "outside.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("clusters", filepath.Join(repository, "alias")); err != nil {
		t.Fatal(err)
	}
	var names []string
	err := WalkCheckout(context.Background(), repository, "alias", func(name string, content []byte) error {
		if string(content) != "owned content\n" {
			t.Fatal("repository read exposed a different file")
		}
		names = append(names, name)
		return nil
	})
	if err != nil || !reflect.DeepEqual(names, []string{"clusters/one.YAML"}) {
		t.Fatal("canonical alias/path/symlink behavior changed", names, err)
	}
}

func TestCheckoutFileRejectsSymlinkHardlinkFIFOAndReplacementAfterEnumeration(t *testing.T) {
	for _, kind := range []string{"final-symlink", "directory-symlink", "hardlink", "fifo", "regular-replacement"} {
		t.Run(kind, func(t *testing.T) {
			repository := t.TempDir()
			outside := t.TempDir()
			directory := filepath.Join(repository, "clusters")
			if err := os.Mkdir(directory, 0755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(directory, "one.yaml")
			if err := os.WriteFile(path, []byte("owned\n"), 0644); err != nil {
				t.Fatal(err)
			}
			outsidePath := filepath.Join(outside, "one.yaml")
			if err := os.WriteFile(outsidePath, []byte("bare private content\n"), 0600); err != nil {
				t.Fatal(err)
			}
			root, err := os.OpenRoot(repository)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			enumerated, err := root.Lstat("clusters/one.yaml")
			if err != nil {
				t.Fatal(err)
			}
			if kind == "directory-symlink" {
				if err := os.Rename(directory, directory+"-original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, directory); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Rename(path, path+".original"); err != nil {
					t.Fatal(err)
				}
				switch kind {
				case "final-symlink":
					err = os.Symlink(outsidePath, path)
				case "hardlink":
					err = os.Link(outsidePath, path)
				case "fifo":
					err = unix.Mkfifo(path, 0600)
				case "regular-replacement":
					err = os.WriteFile(path, []byte("replacement\n"), 0644)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			content, err := readCheckoutFile(context.Background(), root, "clusters/one.yaml", enumerated)
			if err == nil || content != nil {
				t.Fatal("substituted YAML yielded content", kind, err)
			}
			retained, err := os.ReadFile(outsidePath)
			if err != nil || string(retained) != "bare private content\n" {
				t.Fatal("outside original changed", err)
			}
		})
	}
}

func TestWalkCheckoutMissingFilePrefixAndDirectoryEscapeRemainHardFailures(t *testing.T) {
	for _, kind := range []string{"missing-prefix", "file-prefix", "escaping-prefix", "symlink-root"} {
		t.Run(kind, func(t *testing.T) {
			repository := t.TempDir()
			outside := t.TempDir()
			prefix := ""
			switch kind {
			case "missing-prefix":
				prefix = "absent"
			case "file-prefix":
				prefix = "one.yaml"
				if err := os.WriteFile(filepath.Join(repository, prefix), []byte("owned\n"), 0644); err != nil {
					t.Fatal(err)
				}
			case "escaping-prefix":
				prefix = "alias"
				if err := os.Symlink(outside, filepath.Join(repository, prefix)); err != nil {
					t.Fatal(err)
				}
			case "symlink-root":
				alias := filepath.Join(t.TempDir(), "repo")
				if err := os.Symlink(repository, alias); err != nil {
					t.Fatal(err)
				}
				repository = alias
			}
			calls := 0
			err := WalkCheckout(context.Background(), repository, prefix, func(string, []byte) error { calls++; return nil })
			if err == nil || calls != 0 {
				t.Fatal("invalid checkout became a successful empty snapshot", kind, err, calls)
			}
		})
	}
}

func TestWalkCheckoutCancellationCannotAcknowledgePartialDesiredSnapshot(t *testing.T) {
	for _, kind := range []string{"before-read", "after-last-callback"} {
		t.Run(kind, func(t *testing.T) {
			repository := t.TempDir()
			if err := os.WriteFile(filepath.Join(repository, "one.yaml"), []byte("owned\n"), 0644); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			if kind == "before-read" {
				cancel()
			}
			err := WalkCheckout(ctx, repository, "", func(string, []byte) error { calls++; cancel(); return nil })
			if !errors.Is(err, context.Canceled) {
				t.Fatal("canceled enumeration acknowledged desired snapshot", err)
			}
			want := 1
			if kind == "before-read" {
				want = 0
			}
			if calls != want {
				t.Fatal("unexpected callback reachability", calls, want)
			}
		})
	}
}
