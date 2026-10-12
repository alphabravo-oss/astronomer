package distribution

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func publicationPayloadFixture(t *testing.T, role string) (*SupplierDatabaseInspector, *CapturedSupplierDatabase, *VerifiedSupplierDatabasePayload, string, []byte, time.Time) {
	t.Helper()
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	_, options, payload, _ := supplierDatabaseFixture(t, role, now, nil)
	inspector, err := NewSupplierDatabaseInspector(options)
	if err != nil {
		t.Fatal(err)
	}
	captured, err := inspector.Capture(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	path := databasePayloadOwnedFile(t, payload)
	verified, err := inspector.Inspect(t.Context(), captured, path, now)
	if err != nil {
		t.Fatal(err)
	}
	return inspector, captured, verified, path, payload, now
}

func TestSupplierPublicationRehashesBeforeCopyAndClosesOnlyCompleteOriginal(t *testing.T) {
	for _, role := range []string{"trivy-database", "trivy-java-database"} {
		t.Run(role, func(t *testing.T) {
			inspector, captured, verified, path, payload, now := publicationPayloadFixture(t, role)
			reader, err := inspector.OpenPublicationPayload(t.Context(), captured, verified, path, now)
			if err != nil {
				t.Fatal(err)
			}
			actual, err := io.ReadAll(reader)
			closed := reader.Close()
			if err != nil || closed != nil || !bytes.Equal(actual, payload) {
				t.Fatal("publication copy did not preserve complete original bytes")
			}
			// A previously issued whole-payload proof cannot approve a replacement
			// file; rejection happens before any reader is exposed to a writer.
			changed := bytes.Clone(payload)
			changed[len(changed)/2] ^= 1
			if err := os.WriteFile(path, changed, 0600); err != nil {
				t.Fatal(err)
			}
			if reader, err := inspector.OpenPublicationPayload(t.Context(), captured, verified, path, now); err == nil || reader != nil {
				t.Fatal("historical supplier proof bypassed current complete rehash")
			}
		})
	}
}

func TestSupplierPublicationRejectsPartialCanceledAndChangedCopies(t *testing.T) {
	for _, name := range []string{"partial", "canceled-after-bytes", "replacement-after-bytes", "in-place-after-bytes"} {
		t.Run(name, func(t *testing.T) {
			inspector, captured, verified, path, payload, now := publicationPayloadFixture(t, "trivy-database")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			reader, err := inspector.OpenPublicationPayload(ctx, captured, verified, path, now)
			if err != nil {
				t.Fatal(err)
			}
			prefix := make([]byte, 1)
			if n, err := reader.Read(prefix); n != 1 || err != nil {
				_ = reader.Close()
				t.Fatal("fixture did not copy actual initial byte")
			}
			switch name {
			case "partial":
				if err := reader.Close(); err == nil {
					t.Fatal("partial copy qualified for signing")
				}
				return
			case "canceled-after-bytes":
				cancel()
				if _, err := io.Copy(io.Discard, reader); !errors.Is(err, context.Canceled) {
					_ = reader.Close()
					t.Fatal("mid-copy cancellation lost canonical context identity")
				}
				if !errors.Is(reader.Close(), context.Canceled) {
					t.Fatal("canceled close qualified incomplete copy")
				}
				return
			case "replacement-after-bytes":
				if err := os.Rename(path, path+".original"); err != nil {
					_ = reader.Close()
					t.Fatal(err)
				}
				if err := os.WriteFile(path, payload, 0600); err != nil {
					_ = reader.Close()
					t.Fatal(err)
				}
			case "in-place-after-bytes":
				changed := bytes.Clone(payload)
				changed[len(changed)-1] ^= 1
				if err := os.WriteFile(path, changed, 0600); err != nil {
					_ = reader.Close()
					t.Fatal(err)
				}
			}
			_, copied := io.Copy(io.Discard, reader)
			closed := reader.Close()
			if copied == nil || closed == nil {
				t.Fatal("changed payload copy qualified for signing")
			}
		})
	}
}

func TestSupplierPublicationCannotUseForeignProofMissingOrLinkedFiles(t *testing.T) {
	inspector, captured, verified, path, _, now := publicationPayloadFixture(t, "trivy-database")
	for _, name := range []string{"zero-reader", "foreign-capture", "foreign-proof", "missing", "symlink", "stale", "relative"} {
		t.Run(name, func(t *testing.T) {
			selectedCapture, selectedProof, input, clock := captured, verified, path, now
			switch name {
			case "zero-reader":
				if (&SupplierPublicationReader{}).Close() == nil {
					t.Fatal("zero reader closed successfully")
				}
				return
			case "foreign-capture":
				_, selectedCapture, _, _, _, _ = publicationPayloadFixture(t, "trivy-database")
			case "foreign-proof":
				_, _, selectedProof, _, _, _ = publicationPayloadFixture(t, "trivy-database")
			case "missing":
				input = filepath.Join(t.TempDir(), "missing")
			case "symlink":
				input = filepath.Join(t.TempDir(), "link")
				if err := os.Symlink(path, input); err != nil {
					t.Fatal(err)
				}
			case "stale":
				clock = now.Add(48 * time.Hour)
			case "relative":
				input = "layer.tar.gz"
			}
			if reader, err := inspector.OpenPublicationPayload(t.Context(), selectedCapture, selectedProof, input, clock); err == nil || reader != nil {
				t.Fatal("unowned or unavailable supplier input permitted copy")
			}
		})
	}
}
