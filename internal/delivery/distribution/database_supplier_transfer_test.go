//go:build unix

package distribution

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type supplierTransferTransport struct {
	base     http.RoundTripper
	path     string
	payload  []byte
	scenario string
	mutex    sync.Mutex
	calls    int
}

func (transport *supplierTransferTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Path != transport.path {
		return transport.base.RoundTrip(request)
	}
	transport.mutex.Lock()
	transport.calls++
	transport.mutex.Unlock()
	start, end := int64(0), int64(0)
	if request.Method != http.MethodGet || request.URL.Scheme != "https" || request.URL.Host != "1.1.1.1" || request.URL.RawQuery != "" || request.Header.Get("Accept-Encoding") != "identity" {
		return nil, errors.New("fixture transfer request lost exact source")
	}
	if n, err := fmt.Sscanf(request.Header.Get("Range"), "bytes=%d-%d", &start, &end); n != 2 || err != nil || start < 0 || end < start || end >= int64(len(transport.payload)) || end-start >= 64<<20 {
		return nil, errors.New("fixture transfer range invalid")
	}
	data := append([]byte(nil), transport.payload[start:end+1]...)
	status := http.StatusPartialContent
	header := http.Header{"Content-Range": {fmt.Sprintf("bytes %d-%d/%d", start, end, len(transport.payload))}}
	length := int64(len(data))
	switch transport.scenario {
	case "200":
		status = http.StatusOK
	case "multipart":
		header.Set("Content-Type", "multipart/byteranges")
	case "misaligned":
		header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start+1, end, len(transport.payload)))
	case "wrong-total":
		header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(transport.payload)+1))
	case "duplicate-range":
		header.Add("Content-Range", header.Get("Content-Range"))
	case "partial":
		data = data[:len(data)-1]
		length = -1
	case "oversized":
		data = append(data, '!')
		length = -1
	case "changed":
		data[0] ^= 1
	}
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(bytes.NewReader(data)), ContentLength: length, Request: request}, nil
}

func (transport *supplierTransferTransport) count() int {
	transport.mutex.Lock()
	defer transport.mutex.Unlock()
	return transport.calls
}

func supplierTransferFixture(t *testing.T, role, scenario string, now time.Time, metadataEdit func([]byte) []byte) (*SupplierDatabaseInspector, *CapturedSupplierDatabase, *supplierTransferTransport, []byte) {
	t.Helper()
	_, options, payload, _ := supplierDatabaseFixture(t, role, now, metadataEdit)
	transport := &supplierTransferTransport{base: options.Client.Transport, path: "/v2/" + strings.TrimPrefix(options.Repository, "1.1.1.1/") + "/blobs/" + contentDigest(payload), payload: payload, scenario: scenario}
	options.Client.Transport = transport
	inspector, err := NewSupplierDatabaseInspector(options)
	if err != nil {
		t.Fatal(err)
	}
	captured, err := inspector.Capture(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if transport.count() != 0 {
		t.Fatal("capture downloaded layer before transfer")
	}
	return inspector, captured, transport, payload
}

func TestSupplierTransferCommitsOnlyCompleteInspectedOriginalLayer(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	for _, role := range []string{"trivy-database", "trivy-java-database"} {
		t.Run(role, func(t *testing.T) {
			inspector, captured, transport, payload := supplierTransferFixture(t, role, "valid", now, nil)
			output := filepath.Join(supplierTransferPrivateDirectory(t), "database.tar.gz")
			proof, err := inspector.Transfer(t.Context(), captured, output, now)
			if err != nil {
				t.Fatal(err)
			}
			snapshot, ok := proof.Snapshot()
			data, err := os.ReadFile(output)
			info, statError := os.Lstat(output)
			if !ok || err != nil || statError != nil || info.Mode().Perm() != 0600 || !bytes.Equal(data, payload) || snapshot.Metadata.Digest == "" || snapshot.Layer.Digest != contentDigest(data) || transport.count() != 1 {
				t.Fatal("supplier transfer did not commit exactly original verified bytes")
			}
			assertNoSupplierTemporary(t, filepath.Dir(output))
			before := transport.count()
			if other, err := inspector.Transfer(t.Context(), captured, output, now); err == nil || other != nil {
				t.Fatal("existing output overwritten")
			}
			unchanged, err := os.ReadFile(output)
			if err != nil || !bytes.Equal(unchanged, data) || transport.count() != before {
				t.Fatal("refused output changed or initiated transfer")
			}
		})
	}
}

func TestSupplierTransferRefusesRangeFailuresBeforeAcceptedOutput(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	for _, scenario := range []string{"200", "multipart", "misaligned", "wrong-total", "duplicate-range", "partial", "oversized", "changed"} {
		t.Run(scenario, func(t *testing.T) {
			inspector, captured, transport, _ := supplierTransferFixture(t, "trivy-database", scenario, now, nil)
			output := filepath.Join(supplierTransferPrivateDirectory(t), "database.tar.gz")
			if proof, err := inspector.Transfer(t.Context(), captured, output, now); err == nil || proof != nil {
				t.Fatal("invalid supplier range produced accepted output")
			}
			if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("failed transfer left output")
			}
			if transport.count() != 1 {
				t.Fatal("failed range silently retried or bypassed policy")
			}
			assertNoSupplierTemporary(t, filepath.Dir(output))
		})
	}
}

func TestSupplierTransferRejectsUnownedPreconditionsBeforeLayerFetch(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	for _, scenario := range []string{"zero-capture", "foreign-capture", "relative-output", "existing-output", "public-directory", "symlink-directory", "missing-directory", "pre-cancel"} {
		t.Run(scenario, func(t *testing.T) {
			inspector, captured, transport, _ := supplierTransferFixture(t, "trivy-database", "valid", now, nil)
			parent := supplierTransferPrivateDirectory(t)
			output := filepath.Join(parent, "database.tar.gz")
			ctx := t.Context()
			switch scenario {
			case "zero-capture":
				captured = &CapturedSupplierDatabase{}
			case "foreign-capture":
				other, err := NewSupplierDatabaseInspector(inspector.options)
				if err != nil {
					t.Fatal(err)
				}
				captured.owner = other
			case "relative-output":
				output = "relative.tar.gz"
			case "existing-output":
				if os.WriteFile(output, []byte("preserved owned output"), 0600) != nil {
					t.Fatal("fixture preparation failed")
				}
			case "public-directory":
				if os.Chmod(parent, 0755) != nil {
					t.Fatal("fixture preparation failed")
				}
			case "symlink-directory":
				link := filepath.Join(supplierTransferPrivateDirectory(t), "link")
				if os.Symlink(parent, link) != nil {
					t.Fatal("fixture preparation failed")
				}
				output = filepath.Join(link, "database.tar.gz")
			case "missing-directory":
				output = filepath.Join(parent, "missing", "database.tar.gz")
			case "pre-cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			proof, err := inspector.Transfer(ctx, captured, output, now)
			if err == nil || proof != nil || transport.count() != 0 {
				t.Fatal("unowned transfer created proof or fetched layer")
			}
			if scenario == "pre-cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("pre-cancel identity lost")
			}
			assertNoSupplierTemporary(t, parent)
		})
	}
}

func TestSupplierTransferCancellationAfterStagedBytesAndReplacementPreservation(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	t.Run("cancel-after-bytes", func(t *testing.T) {
		inspector, captured, _, _ := supplierTransferFixture(t, "trivy-database", "valid", now, nil)
		output := filepath.Join(supplierTransferPrivateDirectory(t), "database.tar.gz")
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		staged := false
		proof, err := inspector.transfer(ctx, captured, output, now, func(file *os.File) { info, e := file.Stat(); staged = e == nil && info.Size() > 0; cancel() })
		if proof != nil || !errors.Is(err, context.Canceled) || !staged {
			t.Fatal("cancellation after real staged bytes lost identity or produced proof")
		}
		if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("canceled transfer committed output")
		}
		assertNoSupplierTemporary(t, filepath.Dir(output))
	})
	t.Run("owned-temp-replaced", func(t *testing.T) {
		inspector, captured, _, _ := supplierTransferFixture(t, "trivy-database", "valid", now, nil)
		output := filepath.Join(supplierTransferPrivateDirectory(t), "database.tar.gz")
		var replaced, moved string
		var phaseError error
		proof, err := inspector.transfer(t.Context(), captured, output, now, func(file *os.File) {
			replaced = file.Name()
			moved = replaced + ".moved"
			if e := os.Rename(replaced, moved); e != nil {
				phaseError = e
				return
			}
			phaseError = os.WriteFile(replaced, []byte("same-user replacement preserved"), 0600)
		})
		if phaseError != nil {
			t.Fatal(phaseError)
		}
		if proof != nil || err == nil {
			t.Fatal("replaced owned temporary produced proof")
		}
		data, err := os.ReadFile(replaced)
		if err != nil || string(data) != "same-user replacement preserved" {
			t.Fatal("failure cleanup removed replacement")
		}
		if _, err := os.Stat(moved); err != nil {
			t.Fatal("failure cleanup removed moved original")
		}
		if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("replaced temporary committed output")
		}
	})
}

func TestSupplierTransferRejectsStaleMetadataAfterCompleteHash(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	edit := func(raw []byte) []byte {
		return bytes.Replace(raw, []byte(now.Add(-2*time.Hour).Format(time.RFC3339)), []byte(now.Add(-31*time.Hour).Format(time.RFC3339)), 1)
	}
	inspector, captured, transport, _ := supplierTransferFixture(t, "trivy-database", "valid", now, edit)
	output := filepath.Join(supplierTransferPrivateDirectory(t), "database.tar.gz")
	if proof, err := inspector.Transfer(t.Context(), captured, output, now); proof != nil || err == nil {
		t.Fatal("stale actual upstream metadata produced proof")
	}
	if transport.count() != 1 {
		t.Fatal("stale metadata fixture never completed layer transfer")
	}
	if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("stale metadata committed output")
	}
	assertNoSupplierTemporary(t, filepath.Dir(output))
}

func assertNoSupplierTemporary(t *testing.T, parent string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(parent, ".supplier-database-*"))
	if err != nil || len(matches) != 0 {
		t.Fatal("failed transfer left owned temporary staging")
	}
}

// testing.TempDir creates a child with host-umask permissions. Each transfer
// fixture explicitly establishes the production private-directory prerequisite.
func supplierTransferPrivateDirectory(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal("private supplier transfer fixture unavailable")
	}
	return directory
}
