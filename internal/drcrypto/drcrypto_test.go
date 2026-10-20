package drcrypto

import (
	"archive/tar"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fixture struct {
	root, passphrase string
	context          map[string]string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	root := t.TempDir()
	passphrase := filepath.Join(root, "passphrase")
	if err := os.WriteFile(passphrase, []byte("correct horse battery staple, but generated\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return fixture{root: root, passphrase: passphrase, context: map[string]string{"release": "astronomer", "source": "prod-east"}}
}

func (f fixture) write(t *testing.T, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(f.root, name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func wantError(t *testing.T, err error, fragment string) {
	t.Helper()
	var contract *Error
	if err == nil || !errors.As(err, &contract) || !strings.Contains(err.Error(), fragment) {
		t.Fatalf("want contract error containing %q, got %v", fragment, err)
	}
}

func TestEnvelopeRoundTripMultiChunkAndTamperFailsBeforeOutput(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	for _, size := range []int{0, 17, ChunkBytes, 2*ChunkBytes + 3} {
		payload := bytes.Repeat([]byte("postgres-custom-dump\x00"), size/21+1)[:size]
		plain := f.write(t, "dump", payload)
		sealed := filepath.Join(f.root, "dump.aead")
		restored := filepath.Join(f.root, "restored")
		if err := EncryptFile(plain, sealed, f.passphrase, "database-dump", f.context); err != nil {
			t.Fatal(err)
		}
		if err := DecryptFile(sealed, restored, f.passphrase, "database-dump", f.context); err != nil {
			t.Fatalf("size %d: %v", size, err)
		}
		got, _ := os.ReadFile(restored)
		if !bytes.Equal(got, payload) {
			t.Fatalf("size %d round trip differs", size)
		}
		ciphertext, _ := os.ReadFile(sealed)
		for name, mutate := range map[string]func([]byte) []byte{
			"flip":              func(b []byte) []byte { b[len(b)-20] ^= 1; return b },
			"truncate-boundary": func(b []byte) []byte { return b[:len(b)-1] },
		} {
			tampered := f.write(t, "tampered", mutate(bytes.Clone(ciphertext)))
			rejected := filepath.Join(f.root, "rejected")
			err := DecryptFile(tampered, rejected, f.passphrase, "database-dump", f.context)
			if err == nil {
				t.Fatalf("size %d %s accepted", size, name)
			}
			if _, statErr := os.Stat(rejected); statErr == nil {
				t.Fatalf("size %d %s left output behind", size, name)
			}
			_ = os.Remove(tampered)
		}
		for _, p := range []string{plain, sealed, restored} {
			_ = os.Remove(p)
		}
	}
}

func TestEnvelopeRejectsDroppedFinalChunk(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	plain := f.write(t, "dump", bytes.Repeat([]byte{7}, 2*ChunkBytes+5))
	sealed := filepath.Join(f.root, "dump.aead")
	if err := EncryptFile(plain, sealed, f.passphrase, "database-dump", f.context); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(sealed)
	truncated := f.write(t, "truncated", raw[:len(raw)-(5+tagBytes)])
	wantError(t, DecryptFile(truncated, filepath.Join(f.root, "out"), f.passphrase, "database-dump", f.context), "authentication failed")
}

func TestEnvelopeBindsPurposeAndContext(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	source := f.write(t, "keys.tar", []byte("bundle"))
	sealed := filepath.Join(f.root, "keys.aead")
	if err := EncryptFile(source, sealed, f.passphrase, "key-bundle", f.context); err != nil {
		t.Fatal(err)
	}
	wantError(t, DecryptFile(sealed, filepath.Join(f.root, "a"), f.passphrase, "database-dump", f.context), "purpose")
	wantError(t, DecryptFile(sealed, filepath.Join(f.root, "b"), f.passphrase, "key-bundle", map[string]string{"release": "other", "source": "prod-east"}), "context")
}

func manifestInput(f fixture, archive, keyBundle, output string) ManifestInput {
	return ManifestInput{
		Archive: archive, ArchiveKey: "astronomer-pg/astronomer/daily/2026.pgcustom.aead",
		KeyBundle: keyBundle, KeyBundleKey: "astronomer-pg/astronomer/keys/2026.keys.tar.aead",
		Passphrase: f.passphrase, CreatedAt: "2026-09-17T03:00:00Z", Output: output,
		Source: Source{ReleaseName: "astronomer", ReleaseNamespace: "astronomer-system", ChartVersion: "1.1.0", Identity: "prod-east", DatabaseSchemaVersion: 47},
	}
}

func verifyInput(f fixture, manifest, archive, keyBundle string) VerifyInput {
	return VerifyInput{Input: manifest, Passphrase: f.passphrase, ArchiveFile: archive, KeyBundleFile: keyBundle, ExpectReleaseName: "astronomer", ExpectReleaseNamespace: "astronomer-system", ExpectSourceIdentity: "prod-east", Prefix: "astronomer-pg/astronomer"}
}

func TestManifestAuthenticatesIdentityObjectNamesAndPayloads(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	archive := f.write(t, "dump.aead", []byte("encrypted dump"))
	keyBundle := f.write(t, "keys.aead", []byte("encrypted keys"))
	manifest := filepath.Join(f.root, "manifest.json")
	if err := CreateManifest(manifestInput(f, archive, keyBundle, manifest)); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyManifest(verifyInput(f, manifest, archive, keyBundle)); err != nil {
		t.Fatal(err)
	}
	in := verifyInput(f, manifest, "", "")
	in.PrintField = "source.database_schema_version"
	if got, err := VerifyManifest(in); err != nil || got != "47" {
		t.Fatalf("print-field = %q, %v", got, err)
	}
	raw, _ := os.ReadFile(manifest)
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	document["archive"].(map[string]any)["key"] = "astronomer-pg/other/daily/stolen.aead"
	tampered, _ := json.Marshal(document)
	if err := os.WriteFile(manifest, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := VerifyManifest(verifyInput(f, manifest, archive, keyBundle))
	wantError(t, err, "authentication failed")
}

func TestManifestRejectsPayloadSubstitutionAndWrongSource(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	archive := f.write(t, "dump.aead", []byte("encrypted dump"))
	keyBundle := f.write(t, "keys.aead", []byte("encrypted keys"))
	manifest := filepath.Join(f.root, "manifest.json")
	if err := CreateManifest(manifestInput(f, archive, keyBundle, manifest)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archive, []byte("replacement dump"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := VerifyManifest(verifyInput(f, manifest, archive, keyBundle))
	wantError(t, err, "digest/size mismatch")
	in := verifyInput(f, manifest, "", "")
	in.ExpectSourceIdentity = "prod-west"
	_, err = VerifyManifest(in)
	wantError(t, err, "identity")
}

func keySource(t *testing.T, f fixture) string {
	t.Helper()
	source := filepath.Join(f.root, "key-source")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"SECRET_KEY": "jwt key", "ASTRONOMER_ENCRYPTION_KEY": "fernet key", "OIDC_SECRET": "oauth key"} {
		if err := os.WriteFile(filepath.Join(source, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return source
}

func TestKeyBundleRoundTripIsClosedAndDeterministic(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	source := keySource(t, f)
	first, second := filepath.Join(f.root, "first.tar"), filepath.Join(f.root, "second.tar")
	if err := CreateKeyBundle(source, first); err != nil {
		t.Fatal(err)
	}
	if err := CreateKeyBundle(source, second); err != nil {
		t.Fatal(err)
	}
	a, _ := os.ReadFile(first)
	b, _ := os.ReadFile(second)
	if !bytes.Equal(a, b) {
		t.Fatal("key bundle is not deterministic")
	}
	extracted := filepath.Join(f.root, "extracted")
	if err := ExtractKeyBundle(first, extracted); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(extracted, "OIDC_SECRET")); string(got) != "oauth key" {
		t.Fatalf("extracted = %q", got)
	}
}

func TestKeyBundleRejectsLinksTraversalAndDuplicates(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	var buf bytes.Buffer
	archive := tar.NewWriter(&buf)
	for _, name := range []string{"SECRET_KEY", "ASTRONOMER_ENCRYPTION_KEY", "../escape"} {
		_ = archive.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: name, Size: 6, Mode: 0o600})
		_, _ = archive.Write([]byte("secret"))
	}
	_ = archive.Close()
	malicious := f.write(t, "malicious.tar", buf.Bytes())
	wantError(t, ExtractKeyBundle(malicious, filepath.Join(f.root, "bad-output")), "unsafe member path")
	if _, err := os.Stat(filepath.Join(f.root, "escape")); err == nil {
		t.Fatal("traversal escaped")
	}
	source := keySource(t, f)
	if err := os.Symlink(filepath.Join(source, "SECRET_KEY"), filepath.Join(source, "link")); err != nil {
		t.Fatal(err)
	}
	wantError(t, CreateKeyBundle(source, filepath.Join(f.root, "linked.tar")), "regular files")
}

func TestLegacyCBCIsReadOnlyAndSunsetBounded(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	plaintext := []byte("legacy tar payload")
	salt := []byte("12345678")
	secret, _ := ReadSecret(f.passphrase)
	material, err := deriveKey(secret, salt, legacyCBCKDFIterations, 48)
	if err != nil {
		t.Fatal(err)
	}
	pad := aes.BlockSize - len(plaintext)%aes.BlockSize
	padded := append(bytes.Clone(plaintext), bytes.Repeat([]byte{byte(pad)}, pad)...)
	block, _ := aes.NewCipher(material[:32])
	ciphertext := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, material[32:]).CryptBlocks(ciphertext, padded)
	legacy := f.write(t, "legacy.keys.tar.enc", append(append([]byte("Salted__"), salt...), ciphertext...))
	restored := filepath.Join(f.root, "legacy.keys.tar")
	now := time.Now().UTC()
	if _, err := LegacyDecryptKeyBundle(legacy, restored, f.passphrase, now.Add(24*time.Hour), now); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(restored); !bytes.Equal(got, plaintext) {
		t.Fatalf("legacy plaintext = %q", got)
	}
	_, err = LegacyDecryptKeyBundle(legacy, filepath.Join(f.root, "expired"), f.passphrase, now.Add(-time.Hour), now)
	wantError(t, err, "expired")
}

func TestCanonicalJSONMatchesPythonEnsureASCII(t *testing.T) {
	t.Parallel()
	got, err := CanonicalJSON(map[string]any{"b": "é<>&", "a": 1, "c": "\U0001F600"})
	if err != nil {
		t.Fatal(err)
	}
	bs := string(rune(92))
	if want := `{"a":1,"b":"` + bs + `u00e9<>&","c":"` + bs + `ud83d` + bs + `ude00"}`; string(got) != want {
		t.Fatalf("canonical = %s, want %s", got, want)
	}
}
