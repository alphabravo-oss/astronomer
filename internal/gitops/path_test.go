package gitops

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestValidatePathPrefix(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		prefix  string
		wantErr bool
	}{
		{name: "repo root", prefix: ""},
		{name: "relative directory", prefix: "clusters/production"},
		{name: "clean relative directory", prefix: "clusters/../production"},
		{name: "parent", prefix: "..", wantErr: true},
		{name: "traversal", prefix: "../../etc", wantErr: true},
		{name: "absolute", prefix: string(filepath.Separator) + "etc", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := ValidatePathPrefix(test.prefix)
			if test.wantErr && !errors.Is(err, ErrInvalidPathPrefix) {
				t.Fatalf("ValidatePathPrefix(%q) error = %v, want ErrInvalidPathPrefix", test.prefix, err)
			}
			if !test.wantErr && err != nil {
				t.Fatalf("ValidatePathPrefix(%q): %v", test.prefix, err)
			}
		})
	}
}

func TestResolvePathPrefixRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "outside")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	_, err := ResolvePathPrefix(root, "outside")
	if !errors.Is(err, ErrInvalidPathPrefix) {
		t.Fatalf("ResolvePathPrefix error = %v, want ErrInvalidPathPrefix", err)
	}
}

func TestResolvePathPrefixReturnsContainedRealPath(t *testing.T) {
	root := t.TempDir()
	want := filepath.Join(root, "clusters", "production")
	if err := os.MkdirAll(want, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := ResolvePathPrefix(root, "clusters/production")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("ResolvePathPrefix = %q, want %q", got, want)
	}
}
