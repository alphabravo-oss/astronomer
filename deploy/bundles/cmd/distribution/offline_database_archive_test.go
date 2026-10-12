package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
)

// Called only after the enclosing actual-Cosign metadata fixture authenticated
// unchanged parent/child subjects. The tiny QA payloads are not supplier DBs.
func verifyActualOfflineDatabaseTransfer(t *testing.T, options distribution.OfflineOptions, metadata []byte, original map[string][]byte) {
	t.Helper()
	payload := databaseTransferFixture(t, metadata, original, "")
	archive := filepath.Join(t.TempDir(), "database-kit.tar.gz")
	if err := os.WriteFile(archive, payload, 0600); err != nil {
		t.Fatal(err)
	}
	if err := readOfflineDatabaseTransfer(t.Context(), archive, "", options); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "owned")
	if err := readOfflineDatabaseTransfer(t.Context(), archive, destination, options); err != nil {
		t.Fatal(err)
	}
	for subject, content := range original {
		got, err := os.ReadFile(filepath.Join(destination, "blobs", "sha256", subject[7:]))
		if err != nil || !bytes.Equal(got, content) {
			t.Fatal("authenticated opaque bytes did not round-trip")
		}
	}
	if data, err := boundedFile(filepath.Join(destination, "offline-index.json"), 1<<20); err != nil {
		t.Fatal(err)
	} else {
		var index offlineTransferIndex
		if closedJSON(data, &index) != nil || index.SchemaVersion != 2 || index.Digest != options.Digest {
			t.Fatal("imported metadata lost authenticated subject identity")
		}
	}
	marker := filepath.Join(destination, "preserve")
	if err := os.WriteFile(marker, []byte("existing operator output"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := readOfflineDatabaseTransfer(t.Context(), archive, destination, options); err == nil {
		t.Fatal("existing directory accepted as owned quarantine")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("existing destination was changed or removed")
	}
	for _, mutation := range []string{"first-member", "unsigned-metadata", "duplicate", "missing", "size", "oversize", "changed", "partial", "traversal", "symlink", "hardlink", "pax", "trailing", "second-gzip", "expanded-trailing"} {
		t.Run("database-kit-"+mutation, func(t *testing.T) {
			unsafe := filepath.Join(t.TempDir(), "unsafe.tar.gz")
			if err := os.WriteFile(unsafe, databaseTransferFixture(t, metadata, original, mutation), 0600); err != nil {
				t.Fatal(err)
			}
			output := filepath.Join(t.TempDir(), "must-not-exist")
			if err := readOfflineDatabaseTransfer(t.Context(), unsafe, output, options); err == nil {
				t.Fatal("open, changed or unsafe kit accepted")
			}
			if _, err := os.Lstat(output); !os.IsNotExist(err) {
				t.Fatal("failed transfer retained a directory or partial payload")
			}
		})
	}
	t.Run("database-kit-cancel-after-staged-write", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		staged := false
		create := func(path string) (io.WriteCloser, error) {
			file, err := openDatabaseQuarantine(path)
			if err != nil {
				return nil, err
			}
			return cancelAfterDatabaseWrite{WriteCloser: file, cancel: cancel, staged: &staged}, nil
		}
		output := filepath.Join(t.TempDir(), "canceled-after-write")
		err := readDatabaseTransferWithQuarantine(ctx, archive, output, options, create)
		if !staged || !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation after actual staged bytes lost canonical identity")
		}
		if _, err := os.Lstat(output); !os.IsNotExist(err) {
			t.Fatal("canceled staged database bytes survived cleanup")
		}
	})
	t.Run("database-kit-input-modtime", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "same-bytes.tar.gz")
		if err := os.WriteFile(path, payload, 0600); err != nil {
			t.Fatal(err)
		}
		source, _, err := openDatabaseTransfer(t.Context(), path)
		if err != nil {
			t.Fatal(err)
		}
		defer source.close()
		if err := source.checkIdentity(); err != nil {
			t.Fatal(err)
		}
		changed := source.before.ModTime().Add(time.Second)
		if err := os.Chtimes(path, changed, changed); err != nil {
			t.Fatal(err)
		}
		if source.checkIdentity() == nil {
			t.Fatal("changed input mtime was accepted despite equal bytes/inode/size")
		}
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	output := filepath.Join(t.TempDir(), "canceled")
	if !errors.Is(readOfflineDatabaseTransfer(ctx, archive, output, options), context.Canceled) {
		t.Fatal("outer transfer lost context cancellation")
	}
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		t.Fatal("canceled transfer caused a filesystem write")
	}
	link := filepath.Join(t.TempDir(), "link.tar.gz")
	if err := os.Symlink(archive, link); err != nil {
		t.Fatal(err)
	}
	if readOfflineDatabaseTransfer(t.Context(), link, "", options) == nil {
		t.Fatal("symlink input accepted")
	}
}

func databaseTransferFixture(t *testing.T, metadata []byte, originals map[string][]byte, mutation string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(compressed)
	name := databaseMetadataMember
	if mutation == "first-member" {
		name = "untrusted-first"
	}
	if mutation == "unsigned-metadata" {
		metadata = []byte("unsigned graph")
	}
	if err := archive.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Size: int64(len(metadata)), Mode: 0600, Format: tar.FormatUSTAR}); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Write(metadata); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(originals))
	for subject := range originals {
		keys = append(keys, subject)
	}
	sort.Strings(keys)
	for position, subject := range keys {
		if mutation == "missing" && position == 0 {
			continue
		}
		content := bytes.Clone(originals[subject])
		header := tar.Header{Name: "blobs/sha256/" + strings.TrimPrefix(subject, "sha256:"), Typeflag: tar.TypeReg, Size: int64(len(content)), Mode: 0600, Format: tar.FormatUSTAR}
		if position == 0 {
			switch mutation {
			case "size":
				content = append(content, 'x')
				header.Size++
			case "oversize":
				header.Size = distribution.MaximumDatabaseLayerBytes + 1
			case "changed":
				content[0] ^= 1
			case "partial":
				content = content[:len(content)-1]
				header.Size--
			case "traversal":
				header.Name = "../outside"
			case "symlink", "hardlink":
				header.Typeflag = tar.TypeSymlink
				if mutation == "hardlink" {
					header.Typeflag = tar.TypeLink
				}
				header.Linkname, header.Size, content = "outside", 0, nil
			case "pax":
				header.Format = tar.FormatPAX
				header.PAXRecords = map[string]string{"untrusted": "extension"}
			}
		}
		repeats := 1
		if mutation == "duplicate" && position == 0 {
			repeats = 2
		}
		for repeat := 0; repeat < repeats; repeat++ {
			if err := archive.WriteHeader(&header); err != nil {
				t.Fatal(err)
			}
			if _, err := archive.Write(content); err != nil {
				t.Fatal(err)
			}
		}
		if mutation == "oversize" && position == 0 {
			// Deliberately incomplete declared member: rejection must occur at
			// the header before allocation or staging the advertised size.
			if err := compressed.Close(); err != nil {
				t.Fatal(err)
			}
			return bytes.Clone(buffer.Bytes())
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if mutation == "expanded-trailing" {
		if _, err := compressed.Write([]byte("trailing expanded bytes")); err != nil {
			t.Fatal(err)
		}
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	payload := bytes.Clone(buffer.Bytes())
	if mutation == "trailing" {
		payload = append(payload, []byte("untrusted trailing bytes")...)
	}
	if mutation == "second-gzip" {
		payload = append(payload, payload...)
	}
	return payload
}

func TestOwnedTransferReadbackRejectsLinksPartialAndChangedBytes(t *testing.T) {
	content := []byte("opaque authenticated database")
	subject := sha(content)
	for _, mutation := range []string{"original", "partial", "changed", "symlink"} {
		t.Run(mutation, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "owned")
			data := bytes.Clone(content)
			if mutation == "partial" {
				data = data[:len(data)-1]
			}
			if mutation == "changed" {
				data[0] ^= 1
			}
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			if mutation == "symlink" {
				link := filepath.Join(t.TempDir(), "link")
				if err := os.Symlink(path, link); err != nil {
					t.Fatal(err)
				}
				path = link
			}
			err := verifyOwnedTransferBlob(t.Context(), path, subject, int64(len(content)))
			if (err == nil) != (mutation == "original") {
				t.Fatal("readback did not enforce exact regular content")
			}
		})
	}
}

func TestDatabaseTransferRejectsOversizedInputWithoutReadingIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "declared-oversize.tar.gz")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	// Sparse length only; no supplier payload or eight-GiB allocation/download.
	if err := file.Truncate(distribution.MaximumDatabaseKitBytes + 1); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "must-not-exist")
	if readOfflineDatabaseTransfer(t.Context(), path, output, distribution.OfflineOptions{}) == nil {
		t.Fatal("input over the finite kit limit was accepted")
	}
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		t.Fatal("oversized input caused staging")
	}
}

type cancelAfterDatabaseWrite struct {
	io.WriteCloser
	cancel context.CancelFunc
	staged *bool
}

func (writer cancelAfterDatabaseWrite) Write(content []byte) (int, error) {
	count, err := writer.WriteCloser.Write(content)
	if count > 0 {
		*writer.staged = true
		writer.cancel()
	}
	return count, err
}
