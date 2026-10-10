package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
)

// The enclosing fixture actually signs and verifies with the selected Cosign
// QA key. These tiny original bytes prove local export/import, not supplier DB
// freshness, publisher OIDC or no-egress runtime startup.
func verifyActualOfflineDatabaseExport(t *testing.T, options distribution.OfflineOptions, metadata []byte, original map[string][]byte) {
	t.Helper()
	files := writeDatabaseExportFixture(t, original)
	output := filepath.Join(t.TempDir(), "verified-kit.tar.gz")
	if err := writeOfflineDatabaseTransfer(t.Context(), output, metadata, files, options); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(output)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		t.Fatal("completed export is not a private regular file")
	}
	imported := filepath.Join(t.TempDir(), "imported")
	if err := readOfflineDatabaseTransfer(t.Context(), output, imported, options); err != nil {
		t.Fatal(err)
	}
	for subject, content := range original {
		got, err := os.ReadFile(filepath.Join(imported, "blobs", "sha256", subject[7:]))
		if err != nil || !bytes.Equal(got, content) {
			t.Fatal("export lost original authenticated opaque bytes")
		}
	}
	before, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if writeOfflineDatabaseTransfer(t.Context(), output, metadata, files, options) == nil {
		t.Fatal("export replaced an existing output")
	}
	after, err := os.ReadFile(output)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("existing output changed during rejection")
	}
	for _, mutation := range []string{"missing", "extra", "changed", "partial", "symlink", "unknown-key", "unsigned-metadata"} {
		t.Run("database-export-"+mutation, func(t *testing.T) {
			paths := writeDatabaseExportFixture(t, original)
			var subject string
			for candidate := range paths {
				subject = candidate
				break
			}
			policy, document := options, bytes.Clone(metadata)
			switch mutation {
			case "missing":
				delete(paths, subject)
			case "extra":
				paths[sha([]byte("unendorsed"))] = paths[subject]
			case "changed", "partial":
				content := bytes.Clone(original[subject])
				if mutation == "changed" {
					content[0] ^= 1
				} else {
					content = content[:len(content)-1]
				}
				if err := os.WriteFile(paths[subject], content, 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				link := filepath.Join(t.TempDir(), "link")
				if err := os.Symlink(paths[subject], link); err != nil {
					t.Fatal(err)
				}
				paths[subject] = link
			case "unknown-key":
				policy.Policy.KeyRef = "not-approved"
			case "unsigned-metadata":
				document = []byte("unsigned metadata")
			}
			directory := t.TempDir()
			path := filepath.Join(directory, "must-not-exist.tar.gz")
			if writeOfflineDatabaseTransfer(t.Context(), path, document, paths, policy) == nil {
				t.Fatal("unendorsed or changed export accepted")
			}
			assertDatabaseExportCleanup(t, directory, path)
		})
	}
	t.Run("database-export-cancel-after-staged-data", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		staged := false
		wrap := func(writer io.Writer) io.Writer {
			return &databaseExportCancelWriter{destination: writer, cancel: cancel, staged: &staged}
		}
		directory := t.TempDir()
		path := filepath.Join(directory, "canceled.tar.gz")
		err := writeDatabaseTransferWithWriter(ctx, path, metadata, files, options, wrap)
		if !staged || !errors.Is(err, context.Canceled) {
			t.Fatal("canceled staged export lost canonical cancellation")
		}
		assertDatabaseExportCleanup(t, directory, path)
	})
	t.Run("database-export-source-modtime-after-plan", func(t *testing.T) {
		paths := writeDatabaseExportFixture(t, original)
		changed := false
		wrap := func(writer io.Writer) io.Writer {
			return &databaseExportMutationWriter{destination: writer, mutate: func() {
				if changed {
					return
				}
				changed = true
				for _, path := range paths {
					info, err := os.Stat(path)
					if err != nil {
						t.Fatal(err)
					}
					when := info.ModTime().Add(time.Second)
					if err := os.Chtimes(path, when, when); err != nil {
						t.Fatal(err)
					}
				}
			}}
		}
		directory := t.TempDir()
		path := filepath.Join(directory, "changed.tar.gz")
		if err := writeDatabaseTransferWithWriter(t.Context(), path, metadata, paths, options, wrap); err == nil || !changed {
			t.Fatal("equal bytes with changed source identity accepted")
		}
		assertDatabaseExportCleanup(t, directory, path)
	})
	t.Run("database-export-replaced-owned-temp", func(t *testing.T) {
		directory := t.TempDir()
		output := filepath.Join(directory, "not-accepted.tar.gz")
		var replacement, preserved string
		foreign := []byte("replacement from another local operation")
		wrap := func(writer io.Writer) io.Writer {
			file, ok := writer.(*os.File)
			if !ok {
				t.Fatal("fixture did not reach the actual owned staging file")
			}
			return &databaseExportMutationWriter{destination: writer, mutate: func() {
				if replacement != "" {
					return
				}
				replacement, preserved = file.Name(), file.Name()+".moved"
				if err := os.Rename(replacement, preserved); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(replacement, foreign, 0600); err != nil {
					t.Fatal(err)
				}
			}}
		}
		if err := writeDatabaseTransferWithWriter(t.Context(), output, metadata, files, options, wrap); err == nil || replacement == "" {
			t.Fatal("replaced owned staging path was accepted")
		}
		if _, err := os.Lstat(output); !os.IsNotExist(err) {
			t.Fatal("replaced local staging path committed an output")
		}
		got, err := os.ReadFile(replacement)
		if err != nil || !bytes.Equal(got, foreign) {
			t.Fatal("cleanup removed or changed a replacement owned by another operation")
		}
		if _, err := os.Stat(preserved); err != nil {
			t.Fatal("fixture's renamed original unexpectedly disappeared")
		}
	})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	directory := t.TempDir()
	path := filepath.Join(directory, "pre-canceled.tar.gz")
	if !errors.Is(writeOfflineDatabaseTransfer(ctx, path, metadata, files, options), context.Canceled) {
		t.Fatal("pre-canceled export lost canonical context")
	}
	assertDatabaseExportCleanup(t, directory, path)
}

func writeDatabaseExportFixture(t *testing.T, original map[string][]byte) map[string]string {
	t.Helper()
	files := make(map[string]string, len(original))
	for subject, content := range original {
		path := filepath.Join(t.TempDir(), "original")
		if err := os.WriteFile(path, content, 0600); err != nil {
			t.Fatal(err)
		}
		files[subject] = path
	}
	return files
}

func assertDatabaseExportCleanup(t *testing.T, directory, output string) {
	t.Helper()
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		t.Fatal("failed export retained an output")
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 0 {
		t.Fatal("failed export retained owned staging files")
	}
}

type databaseExportCancelWriter struct {
	destination io.Writer
	cancel      context.CancelFunc
	staged      *bool
}

func (writer *databaseExportCancelWriter) Write(content []byte) (int, error) {
	count, err := writer.destination.Write(content)
	// Gzip's initial ten-byte header alone does not establish payload staging.
	if count > 10 {
		*writer.staged = true
		writer.cancel()
	}
	return count, err
}

type databaseExportMutationWriter struct {
	destination io.Writer
	mutate      func()
}

func (writer *databaseExportMutationWriter) Write(content []byte) (int, error) {
	writer.mutate()
	return writer.destination.Write(content)
}

func TestDatabaseExportFiniteWriterRefusesOverflow(t *testing.T) {
	var destination bytes.Buffer
	writer := boundedDatabaseExportWriter{destination: &destination, maximum: 3}
	if _, err := writer.Write([]byte("ab")); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("cd")); err == nil || destination.String() != "ab" {
		t.Fatal("finite export output limit changed accepted bytes")
	}
}
