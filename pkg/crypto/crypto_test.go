package crypto

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustKey(t *testing.T) string {
	t.Helper()
	k, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	if !strings.HasPrefix(k, "AGE-SECRET-KEY-1") {
		t.Fatalf("GenerateKey returned %q", k[:16])
	}
	return k
}

func TestRoundTrip(t *testing.T) {
	t.Parallel()
	enc, err := NewEncryptor(mustKey(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, pt := range [][]byte{{}, []byte("s3cr3t"), bytes.Repeat([]byte{0xA5}, 200_000)} {
		sealed, err := enc.Encrypt(pt)
		if err != nil {
			t.Fatal(err)
		}
		if sealed.KeyID != enc.CurrentKeyID() || sealed.Algorithm != AlgorithmAgeX25519 || len(sealed.Nonce) != 0 || len(sealed.Ciphertext) == 0 {
			t.Fatalf("unexpected sealed shape: %+v", sealed.KeyID)
		}
		if sealed.IsZero() {
			t.Fatal("sealed value reported zero")
		}
		if bytes.Contains(sealed.Ciphertext, pt) && len(pt) > 0 {
			t.Fatal("ciphertext contains plaintext")
		}
		got, err := enc.Decrypt(sealed)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, pt) {
			t.Fatal("round trip mismatch")
		}
	}
	s, err := enc.EncryptString("hello")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := enc.DecryptString(s); err != nil || got != "hello" {
		t.Fatalf("DecryptString = %q, %v", got, err)
	}
}

func TestFingerprintIsDeterministicAndLabelsWin(t *testing.T) {
	t.Parallel()
	k := mustKey(t)
	a, _ := NewEncryptor(k)
	b, _ := NewEncryptor(k)
	if a.CurrentKeyID() != b.CurrentKeyID() || !strings.HasPrefix(a.CurrentKeyID(), "age-") {
		t.Fatalf("fingerprint ids %q / %q", a.CurrentKeyID(), b.CurrentKeyID())
	}
	labelled, err := NewEncryptor("prod-2026:" + k)
	if err != nil {
		t.Fatal(err)
	}
	if labelled.CurrentKeyID() != "prod-2026" {
		t.Fatalf("label id = %q", labelled.CurrentKeyID())
	}
	if id, _ := KeyID(k); id != a.CurrentKeyID() {
		t.Fatalf("KeyID = %q", id)
	}
}

func TestUnknownKey(t *testing.T) {
	t.Parallel()
	a, _ := NewEncryptor("a:" + mustKey(t))
	b, _ := NewEncryptor("b:" + mustKey(t))
	sealed, err := a.Encrypt([]byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Decrypt(sealed); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("want ErrUnknownKey, got %v", err)
	}
}

func TestWrongKeyUnderSameID(t *testing.T) {
	t.Parallel()
	a, _ := NewEncryptor("k:" + mustKey(t))
	b, _ := NewEncryptor("k:" + mustKey(t))
	sealed, _ := a.Encrypt([]byte("x"))
	if _, err := b.Decrypt(sealed); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("want ErrDecrypt, got %v", err)
	}
}

func TestTamper(t *testing.T) {
	t.Parallel()
	enc, _ := NewEncryptor(mustKey(t))
	sealed, _ := enc.Encrypt([]byte("payload that matters"))
	for _, idx := range []int{0, len(sealed.Ciphertext) / 2, len(sealed.Ciphertext) - 1} {
		tampered := sealed
		tampered.Ciphertext = append([]byte(nil), sealed.Ciphertext...)
		tampered.Ciphertext[idx] ^= 0x01
		if _, err := enc.Decrypt(tampered); !errors.Is(err, ErrDecrypt) {
			t.Fatalf("byte %d: want ErrDecrypt, got %v", idx, err)
		}
	}
	truncated := sealed
	truncated.Ciphertext = sealed.Ciphertext[:len(sealed.Ciphertext)-1]
	if _, err := enc.Decrypt(truncated); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("truncated: want ErrDecrypt, got %v", err)
	}
	wrongAlg := sealed
	wrongAlg.Algorithm = "aes-256-gcm"
	if _, err := enc.Decrypt(wrongAlg); !errors.Is(err, ErrUnsupportedAlgorithm) {
		t.Fatalf("algorithm: want ErrUnsupportedAlgorithm, got %v", err)
	}
	withNonce := sealed
	withNonce.Nonce = []byte{1}
	if _, err := enc.Decrypt(withNonce); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("nonce: want ErrDecrypt, got %v", err)
	}
	if _, err := enc.Decrypt(Sealed{}); !errors.Is(err, ErrEmpty) {
		t.Fatalf("zero: want ErrEmpty, got %v", err)
	}
}

func TestKeyringCurrentAndPrevious(t *testing.T) {
	t.Parallel()
	oldKey, newKey := mustKey(t), mustKey(t)
	old, _ := NewEncryptor("old:" + oldKey)
	sealedOld, _ := old.Encrypt([]byte("legacy row"))

	ring, err := ParseKeyring("# rotated 2026-10\nnew:" + newKey + "\n\nold:" + oldKey + "\n")
	if err != nil {
		t.Fatal(err)
	}
	if ring.CurrentKeyID() != "new" || ring.KeyCount() != 2 || !ring.HasKey("old") {
		t.Fatalf("ring = %v", ring.KeyIDs())
	}
	if got, err := ring.Decrypt(sealedOld); err != nil || string(got) != "legacy row" {
		t.Fatalf("decrypt previous: %q %v", got, err)
	}
	fresh, _ := ring.Encrypt([]byte("y"))
	if fresh.KeyID != "new" || !ring.IsCurrent(fresh) || ring.IsCurrent(sealedOld) {
		t.Fatal("writes must use the current key")
	}
	comma, err := ParseKeyring("new:" + newKey + ", old:" + oldKey)
	if err != nil || comma.KeyCount() != 2 {
		t.Fatalf("comma keyring: %v", err)
	}
	inv := ring.KeyInventory()
	if len(inv) != 2 || !inv[0].Primary || inv[1].Primary || !strings.HasPrefix(inv[0].Recipient, "age1") {
		t.Fatalf("inventory = %+v", inv)
	}
}

func TestKeyringRejects(t *testing.T) {
	t.Parallel()
	k := mustKey(t)
	cases := map[string]string{
		"empty":         "  \n# only a comment\n",
		"garbage":       "not-a-key",
		"fernet":        "dGhpcy1pcy1ub3QtYS1yZWFsLWZlcm5ldC1rZXktMDA=",
		"bad label":     "bad label!:" + k,
		"duplicate id":  "a:" + k + "\na:" + mustKey(t),
		"duplicate key": "a:" + k + "\nb:" + k,
	}
	for name, spec := range cases {
		if _, err := ParseKeyring(spec); err == nil {
			t.Errorf("%s: expected error", name)
		} else if strings.Contains(err.Error(), k[len("AGE-SECRET-KEY-1"):]) {
			t.Errorf("%s: error leaks key material: %v", name, err)
		}
	}
	if _, err := NewEncryptor(k + "," + mustKey(t)); err == nil {
		t.Error("NewEncryptor must reject a keyring")
	}
}

func TestFileEnvAndLoad(t *testing.T) {
	k := mustKey(t)
	path := filepath.Join(t.TempDir(), "keys.txt")
	if err := os.WriteFile(path, []byte("# created by age-keygen\n# public key: age1...\nfile:"+k+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fromFile, err := NewEncryptorFromFile(path)
	if err != nil || fromFile.CurrentKeyID() != "file" {
		t.Fatalf("file: %v", err)
	}
	t.Setenv("ASTRONOMER_TEST_KEYS", "env:"+k)
	fromEnv, err := NewEncryptorFromEnv("ASTRONOMER_TEST_KEYS")
	if err != nil || fromEnv.CurrentKeyID() != "env" {
		t.Fatalf("env: %v", err)
	}
	if _, err := NewEncryptorFromEnv("ASTRONOMER_TEST_UNSET_KEYS"); !errors.Is(err, ErrNoKey) {
		t.Fatalf("unset env: %v", err)
	}
	if _, err := NewEncryptorFromFile(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing file must fail")
	}
	if enc, err := Load("", path); err != nil || enc.CurrentKeyID() != "file" {
		t.Fatalf("Load file: %v", err)
	}
	if enc, err := Load("inline:"+k, ""); err != nil || enc.CurrentKeyID() != "inline" {
		t.Fatalf("Load inline: %v", err)
	}
	if _, err := Load("inline:"+k, path); err == nil {
		t.Fatal("Load with both sources must fail")
	}
	if _, err := Load("", ""); !errors.Is(err, ErrNoKey) {
		t.Fatalf("Load none: %v", err)
	}
}

func TestNilEncryptor(t *testing.T) {
	t.Parallel()
	var e *Encryptor
	if _, err := e.Encrypt([]byte("x")); !errors.Is(err, ErrNoKey) {
		t.Fatal(err)
	}
	if _, err := e.Decrypt(Sealed{KeyID: "a", Algorithm: AlgorithmAgeX25519, Ciphertext: []byte{1}}); !errors.Is(err, ErrNoKey) {
		t.Fatal(err)
	}
	if e.KeyCount() != 0 || e.CurrentKeyID() != "" || e.HasKey("a") {
		t.Fatal("nil encryptor must be empty")
	}
}
