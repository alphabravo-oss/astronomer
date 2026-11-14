package drcrypto

import (
	"archive/tar"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKeyBundleCatalogTrustRoundTrip(t *testing.T) {
	t.Parallel()
	for _, directories := range [][]string{{"catalog-ca"}, {"catalog-signature-trust"}, {"catalog-ca", "catalog-signature-trust"}} {
		t.Run(fmt.Sprint(directories), func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			source := keySource(t, f)
			for _, dir := range directories {
				if err := os.Mkdir(filepath.Join(source, dir), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(source, dir, ".trust.pem"), []byte("synthetic trust"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			archive := filepath.Join(f.root, "keys.tar")
			if err := CreateKeyBundle(source, archive); err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(f.root, "restored")
			if err := ExtractKeyBundle(archive, destination); err != nil {
				t.Fatal(err)
			}
			assertRestoredCatalogTrust(t, destination, directories)
		})
	}
}

func assertRestoredCatalogTrust(t *testing.T, destination string, directories []string) {
	t.Helper()
	for _, dir := range directories {
		path := filepath.Join(destination, dir, ".trust.pem")
		content, err := os.ReadFile(path)
		if err != nil || string(content) != "synthetic trust" {
			t.Fatalf("trust file was not restored: %v", err)
		}
		for _, entry := range []struct {
			path string
			mode os.FileMode
		}{{destination, 0700}, {filepath.Dir(path), 0700}, {path, 0600}} {
			info, err := os.Stat(entry.path)
			if err != nil || info.Mode().Perm() != entry.mode {
				t.Fatalf("wrong private permissions for %s: %v", entry.path, err)
			}
		}
	}
}

func TestKeyBundleCatalogTrustLongKeyIsDeterministic(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	source := keySource(t, f)
	directory := filepath.Join(source, "catalog-signature-trust")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	name := strings.Repeat("k", 249) + ".pem"
	if err := os.WriteFile(filepath.Join(directory, name), []byte("synthetic trust"), 0600); err != nil {
		t.Fatal(err)
	}
	first, second := filepath.Join(f.root, "first.tar"), filepath.Join(f.root, "second.tar")
	for _, path := range []string{first, second} {
		if err := CreateKeyBundle(source, path); err != nil {
			t.Fatal(err)
		}
	}
	a, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(second)
	if err != nil || !bytes.Equal(a, b) {
		t.Fatalf("long-key archive is not deterministic: %v", err)
	}
	destination := filepath.Join(f.root, "restored")
	if err := ExtractKeyBundle(first, destination); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(destination, "catalog-signature-trust", name))
	if err != nil || string(content) != "synthetic trust" {
		t.Fatalf("long Secret key did not round trip: %v", err)
	}
}

func TestKeyBundleCatalogTrustRejectsUnsafeArchives(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		members []tar.Header
	}{
		{"traversal", []tar.Header{{Name: "catalog-ca/../escape", Typeflag: tar.TypeReg, Size: 1}}},
		{"absolute", []tar.Header{{Name: "/catalog-ca/ca.pem", Typeflag: tar.TypeReg, Size: 1}}},
		{"unknown directory", []tar.Header{{Name: "other/ca.pem", Typeflag: tar.TypeReg, Size: 1}}},
		{"nested directory", []tar.Header{{Name: "catalog-ca/nested/ca.pem", Typeflag: tar.TypeReg, Size: 1}}},
		{"symlink", []tar.Header{{Name: "catalog-ca/ca.pem", Typeflag: tar.TypeSymlink, Linkname: "/tmp/escape"}}},
		{"hardlink", []tar.Header{{Name: "catalog-ca/ca.pem", Typeflag: tar.TypeLink, Linkname: "SECRET_KEY"}}},
		{"duplicate", []tar.Header{{Name: "catalog-ca/ca.pem", Typeflag: tar.TypeReg, Size: 1}, {Name: "catalog-ca/ca.pem", Typeflag: tar.TypeReg, Size: 1}}},
		{"file before directory", []tar.Header{{Name: "catalog-ca", Typeflag: tar.TypeReg, Size: 1}, {Name: "catalog-ca/ca.pem", Typeflag: tar.TypeReg, Size: 1}}},
		{"directory before file", []tar.Header{{Name: "catalog-ca/ca.pem", Typeflag: tar.TypeReg, Size: 1}, {Name: "catalog-ca", Typeflag: tar.TypeReg, Size: 1}}},
		{"oversized", []tar.Header{{Name: "catalog-ca/ca.pem", Typeflag: tar.TypeReg, Size: (1 << 20) + 1}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			members := append([]tar.Header{{Name: "SECRET_KEY", Typeflag: tar.TypeReg, Size: 1}, {Name: "ASTRONOMER_ENCRYPTION_KEY", Typeflag: tar.TypeReg, Size: 1}}, tc.members...)
			archive := f.write(t, "bad.tar", testKeyArchive(t, members))
			destination := filepath.Join(f.root, "restore")
			if err := ExtractKeyBundle(archive, destination); err == nil {
				t.Fatal("unsafe archive accepted")
			}
			if _, err := os.Stat(destination); !os.IsNotExist(err) {
				t.Fatal("rejected archive created destination")
			}
		})
	}
}

func TestKeyBundleCatalogTrustRejectsUnsafeSources(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"empty", "symlink", "nested", "unknown", "empty file"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			source := keySource(t, f)
			dir := filepath.Join(source, "catalog-ca")
			if kind == "unknown" {
				dir = filepath.Join(source, "unknown")
			}
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			var err error
			switch kind {
			case "symlink":
				err = os.Symlink(filepath.Join(source, "SECRET_KEY"), filepath.Join(dir, "ca.pem"))
			case "nested":
				err = os.Mkdir(filepath.Join(dir, "nested"), 0700)
			case "empty file":
				err = os.WriteFile(filepath.Join(dir, "ca.pem"), nil, 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			output := filepath.Join(f.root, "keys.tar")
			if err := CreateKeyBundle(source, output); err == nil {
				t.Fatal("unsafe source accepted")
			}
			if _, err := os.Stat(output); !os.IsNotExist(err) {
				t.Fatal("rejected source published archive")
			}
		})
	}
}

func TestKeyBundleCatalogTrustLimits(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		count, size int
	}{
		{"file count", maxKeyBundleFiles, 1},
		{"total bytes", 17, maxKeyFileBytes},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			source := keySource(t, f)
			dir := filepath.Join(source, "catalog-ca")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			members := []tar.Header{{Name: "SECRET_KEY", Typeflag: tar.TypeReg, Size: 3}, {Name: "ASTRONOMER_ENCRYPTION_KEY", Typeflag: tar.TypeReg, Size: 3}}
			content := bytes.Repeat([]byte("x"), tc.size)
			for i := 0; i < tc.count; i++ {
				name := fmt.Sprintf("catalog-ca/key-%04d.pem", i)
				if err := os.WriteFile(filepath.Join(source, name), content, 0600); err != nil {
					t.Fatal(err)
				}
				members = append(members, tar.Header{Name: name, Typeflag: tar.TypeReg, Size: int64(tc.size)})
			}
			if err := CreateKeyBundle(source, filepath.Join(f.root, "created.tar")); err == nil {
				t.Fatal("creator accepted excessive bundle")
			}
			archive := f.write(t, "excessive.tar", testKeyArchive(t, members))
			destination := filepath.Join(f.root, "restored")
			if err := ExtractKeyBundle(archive, destination); err == nil {
				t.Fatal("extractor accepted excessive bundle")
			}
			if _, err := os.Stat(destination); !os.IsNotExist(err) {
				t.Fatal("rejected archive created destination")
			}
		})
	}
}

func testKeyArchive(t *testing.T, members []tar.Header) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	for _, member := range members {
		if err := writer.WriteHeader(&member); err != nil {
			t.Fatal(err)
		}
		if member.Size > 0 {
			if _, err := writer.Write(make([]byte, member.Size)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
