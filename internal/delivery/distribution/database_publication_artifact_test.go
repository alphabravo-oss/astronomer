//go:build linux

package distribution

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/sigstore/sigstore/pkg/cryptoutils"
	"github.com/sigstore/sigstore/pkg/signature"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
)

type databasePublicationCosignFixtureSigner struct {
	t                     *testing.T
	binary, key, password string
	public                crypto.PublicKey
}

func (signer databasePublicationCosignFixtureSigner) PublicKey(_ ...signature.PublicKeyOption) (crypto.PublicKey, error) {
	if signer.public == nil {
		return nil, fmt.Errorf("QA public key unavailable")
	}
	return signer.public, nil
}

func (signer databasePublicationCosignFixtureSigner) SignMessage(message io.Reader, _ ...signature.SignOption) ([]byte, error) {
	content, err := io.ReadAll(io.LimitReader(message, (1<<20)+1))
	if err != nil || len(content) > 1<<20 {
		return nil, fmt.Errorf("QA signing message exceeds bound")
	}
	directory := signer.t.TempDir()
	input, output := filepath.Join(directory, "input"), filepath.Join(directory, "signature")
	if err := os.WriteFile(input, content, 0600); err != nil {
		return nil, fmt.Errorf("QA signing input unavailable")
	}
	bounded, cancel := context.WithTimeout(signer.t.Context(), time.Minute)
	defer cancel()
	command := exec.CommandContext(bounded, signer.binary, "sign-blob", "--yes", "--key", signer.key, "--new-bundle-format=false", "--use-signing-config=false", "--tlog-upload=false", "--b64=false", "--output-signature", output, input)
	command.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + directory, "COSIGN_PASSWORD=" + signer.password}
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("actual selected QA blob signer failed")
	}
	value, err := os.ReadFile(output)
	if err != nil || len(value) == 0 || len(value) > 1<<20 {
		return nil, fmt.Errorf("actual selected QA signature unavailable")
	}
	return value, nil
}

func databasePublicationActualCosignFixture(t *testing.T) (*DatabaseAuthority, *VerifiedDatabaseAuthorityBase, Options, *DatabasePublicationSigner) {
	t.Helper()
	binary := os.Getenv("COSIGN_BIN")
	if binary == "" {
		t.Skip("COSIGN_BIN must select qualified actual signer")
	}
	directory := t.TempDir()
	password := rand.Text()
	keyPrefix := filepath.Join(directory, "qa")
	bounded, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	command := exec.CommandContext(bounded, binary, "generate-key-pair", "--output-key-prefix", keyPrefix)
	command.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + directory, "COSIGN_PASSWORD=" + password}
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		t.Fatal("actual selected Cosign QA key generation failed")
	}
	public, err := os.ReadFile(keyPrefix + ".pub")
	if err != nil {
		t.Fatal("actual QA public key unavailable")
	}
	parsedPublic, err := cryptoutils.UnmarshalPEMToPublicKey(public)
	if err != nil {
		t.Fatal("actual QA public key invalid")
	}
	actual := databasePublicationCosignFixtureSigner{t: t, binary: binary, key: keyPrefix + ".key", password: password, public: parsedPublic}
	authority, proof, options := publicationBaseFixtureWithSigner(t, actual, public, func(client *http.Client) *http.Client {
		clone := *client
		clone.Transport = &databasePublicationRangeFixture{base: client.Transport, writes: map[string]int{}}
		return &clone
	})
	signer, err := newDatabasePublicationSigner(binary, keyPrefix+".key", password)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(signer.Close)
	return authority, proof, options, signer
}

func publicationCapturedSupplierFile(t *testing.T, base *VerifiedDatabaseAuthorityBase, role string, now time.Time) (*CapturedSupplierDatabase, *VerifiedSupplierDatabasePayload, string) {
	t.Helper()
	options, payload := publicationSupplierFixture(t, base, role, now)
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
	return captured, verified, path
}

func TestSelectedCosignPublicationCopiesOriginalChildrenAndFeedToCanonicalReadback(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	authority, proof, _, signer := databasePublicationActualCosignFixture(t)
	mainCapture, main, mainPath := publicationCapturedSupplierFile(t, proof, "trivy-database", now)
	javaCapture, java, javaPath := publicationCapturedSupplierFile(t, proof, "trivy-java-database", now)
	repositories := DatabasePublicationRepositories{Main: "1.1.1.1/endorsed/main", Java: "1.1.1.1/endorsed/java"}
	prepared, err := authority.PrepareDatabasePublication(t.Context(), proof, main, java, repositories, now)
	if err != nil {
		t.Fatal(err)
	}
	document, valid := prepared.Document(1)
	if !valid {
		t.Fatal("actual supplier decision missing")
	}
	registries := make([]*DatabasePublicationRegistry, 0, 3)
	for _, repository := range []string{repositories.Main, repositories.Java, "1.1.1.1/endorsed/updates"} {
		registry, err := authority.PublicationRegistry(t.Context(), proof, repository, resolver.DatabaseRegistryAuthentication{})
		if err != nil {
			t.Fatal(err)
		}
		defer registry.Close()
		registries = append(registries, registry)
	}
	for index, snapshot := range document.Databases {
		captured, payload, path := mainCapture, main, mainPath
		if index == 1 {
			captured, payload, path = javaCapture, java, javaPath
		}
		if err := registries[index].PublishSupplierChild(t.Context(), signer, captured, payload, path, snapshot, now); err != nil {
			t.Fatal(err)
		}
	}
	feed, err := registries[2].PublishFeed(t.Context(), signer, document, now)
	if err != nil {
		t.Fatal(err)
	}
	update, err := authority.LoadUpdate(t.Context(), proof, registries[2].repository, feed, now)
	if err != nil {
		t.Fatal(err)
	}
	_, actual, valid := update.Snapshot()
	if !valid || !databasePublicationTestDocumentEqual(document, actual) {
		t.Fatal("actual canonical feed readback differs")
	}
	// Immutable child bytes are read back under signature-first verification.
	// This QA test does not manufacture a PG reservation or a scan-used witness.
	for index, role := range []string{"trivy-database", "trivy-java-database"} {
		cache := supplierTransferPrivateDirectory(t)
		path := filepath.Join(cache, "readback.tar.gz")
		measured, err := authority.TransferUpdatePayload(t.Context(), proof, registries[2].repository, update, role, path, now)
		if err != nil {
			t.Fatal(err)
		}
		_, snapshot, valid := measured.Snapshot()
		if !valid || snapshot != document.Databases[index] {
			t.Fatal("complete actual published payload proof differs")
		}
		expected := mainPath
		if index == 1 {
			expected = javaPath
		}
		original, _ := os.ReadFile(expected)
		copied, _ := os.ReadFile(path)
		if !bytes.Equal(original, copied) {
			t.Fatal("publisher changed supplier layer")
		}
	}
}
func databasePublicationTestDocumentEqual(left, right DatabaseUpdateDocument) bool {
	return reflect.DeepEqual(left, right)
}

func TestSelectedCosignPublicationRejectsChangedPayloadAndWrongSigningKey(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	authority, proof, _, signer := databasePublicationActualCosignFixture(t)
	mainCapture, main, path := publicationCapturedSupplierFile(t, proof, "trivy-database", now)
	java, _ := publicationSupplierProof(t, proof, "trivy-java-database", now)
	repositories := DatabasePublicationRepositories{Main: "1.1.1.1/endorsed/main", Java: "1.1.1.1/endorsed/java"}
	prepared, err := authority.PrepareDatabasePublication(t.Context(), proof, main, java, repositories, now)
	if err != nil {
		t.Fatal(err)
	}
	document, _ := prepared.Document(1)
	registry, err := authority.PublicationRegistry(t.Context(), proof, repositories.Main, resolver.DatabaseRegistryAuthentication{})
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	changed := bytes.Clone(original)
	changed[len(changed)-1] ^= 1
	if os.WriteFile(path, changed, 0600) != nil {
		t.Fatal("QA changed file setup failed")
	}
	transport := authority.loader.options.Client.Transport.(*databasePublicationRangeFixture)
	before := transport.writeCount()
	if err := registry.PublishSupplierChild(t.Context(), signer, mainCapture, main, path, document.Databases[0], now); err == nil {
		t.Fatal("historical supplier proof permitted changed current payload")
	}
	if transport.writeCount() != before {
		t.Fatal("changed current payload reached a registry write")
	}
	if os.WriteFile(path, original, 0600) != nil {
		t.Fatal("QA original restore failed")
	}
	other, _, _, otherSigner := databasePublicationActualCosignFixture(t)
	_ = other
	if err := registry.PublishSupplierChild(t.Context(), otherSigner, mainCapture, main, path, document.Databases[0], now); err == nil {
		t.Fatal("different local signing key escaped captured canonical policy")
	}
	// A public production constructor cannot select keyed QA mode by environment.
	production, err := NewDatabasePublicationSigner(signer.binary)
	if err != nil {
		t.Fatal(err)
	}
	defer production.Close()
	_, err = production.sign(t.Context(), authority.loader.options.Policy, authority.loader.options.TrustDirectory, registry.repository, document.Databases[0].Manifest.Digest)
	if err == nil {
		t.Fatal("production signer accepted keyed fixture policy")
	}
}

// Finite Range routing is test-only. Exact original bytes still come from the
// owned registry and must pass canonical whole hashes; no production DNS/TLS or
// supplier-size qualification follows from this injected peer.
type databasePublicationRangeFixture struct {
	base   http.RoundTripper
	mutex  sync.Mutex
	writes map[string]int
}

func (transport *databasePublicationRangeFixture) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Method == http.MethodPut || request.Method == http.MethodPost {
		transport.mutex.Lock()
		transport.writes[request.URL.Path]++
		transport.mutex.Unlock()
	}
	response, err := transport.base.RoundTrip(request)
	if err != nil || response == nil || request.Header.Get("Range") == "" || response.StatusCode != http.StatusOK {
		return response, err
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	_ = response.Body.Close()
	if err != nil || len(content) > 4<<20 {
		return nil, fmt.Errorf("owned QA Range payload exceeds bound")
	}
	var start, end int64
	if count, err := fmt.Sscanf(request.Header.Get("Range"), "bytes=%d-%d", &start, &end); count != 2 || err != nil || start < 0 || end < start || end >= int64(len(content)) {
		return nil, fmt.Errorf("owned QA Range request invalid")
	}
	response.StatusCode = http.StatusPartialContent
	response.Status = "206 Partial Content"
	response.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(content)))
	response.ContentLength = end - start + 1
	response.Header.Set("Content-Length", fmt.Sprint(response.ContentLength))
	response.Body = io.NopCloser(bytes.NewReader(content[start : end+1]))
	return response, nil
}
func (transport *databasePublicationRangeFixture) writeCount() int {
	transport.mutex.Lock()
	defer transport.mutex.Unlock()
	count := 0
	for _, value := range transport.writes {
		count += value
	}
	return count
}
func (transport *databasePublicationRangeFixture) CloseIdleConnections() {
	if closer, ok := transport.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}
