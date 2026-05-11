package auth

import (
	"testing"
)

func TestEncryptDecryptRoundtrip(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	enc, err := NewEncryptor(key)
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}

	plaintext := "my-super-secret-sso-client-secret"
	token, err := enc.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	got, err := enc.Decrypt(token)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}

	if got != plaintext {
		t.Errorf("roundtrip mismatch: got %q, want %q", got, plaintext)
	}
}

func TestEncryptProducesDifferentCiphertexts(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	enc, err := NewEncryptor(key)
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}

	tok1, err := enc.Encrypt("secret-a")
	if err != nil {
		t.Fatalf("Encrypt 1: %v", err)
	}

	tok2, err := enc.Encrypt("secret-b")
	if err != nil {
		t.Fatalf("Encrypt 2: %v", err)
	}

	if tok1 == tok2 {
		t.Error("different plaintexts produced identical ciphertexts")
	}

	// Same plaintext should also produce different tokens (random IV).
	tok3, err := enc.Encrypt("secret-a")
	if err != nil {
		t.Fatalf("Encrypt 3: %v", err)
	}

	if tok1 == tok3 {
		t.Error("same plaintext produced identical ciphertexts (IV should differ)")
	}
}

func TestEncryptDecryptWrongKey(t *testing.T) {
	key1, _ := GenerateKey()
	key2, _ := GenerateKey()

	enc1, _ := NewEncryptor(key1)
	enc2, _ := NewEncryptor(key2)

	token, err := enc1.Encrypt("secret")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	_, err = enc2.Decrypt(token)
	if err == nil {
		t.Error("expected error decrypting with wrong key, got nil")
	}
}

func TestEncryptDecryptInvalidToken(t *testing.T) {
	key, _ := GenerateKey()
	enc, _ := NewEncryptor(key)

	_, err := enc.Decrypt("not-a-valid-fernet-token")
	if err == nil {
		t.Error("expected error for invalid token, got nil")
	}
}

func TestEncryptDecryptEmptyPlaintext(t *testing.T) {
	key, _ := GenerateKey()
	enc, _ := NewEncryptor(key)

	token, err := enc.Encrypt("")
	if err != nil {
		t.Fatalf("Encrypt empty: %v", err)
	}

	got, err := enc.Decrypt(token)
	if err != nil {
		t.Fatalf("Decrypt empty: %v", err)
	}

	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestGenerateKeyProducesUsableKey(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	if key == "" {
		t.Fatal("GenerateKey returned empty string")
	}

	enc, err := NewEncryptor(key)
	if err != nil {
		t.Fatalf("NewEncryptor with generated key: %v", err)
	}

	token, err := enc.Encrypt("test")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	got, err := enc.Decrypt(token)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}

	if got != "test" {
		t.Errorf("got %q, want %q", got, "test")
	}
}

func TestNewEncryptorInvalidKey(t *testing.T) {
	tests := []struct {
		name string
		key  string
	}{
		{"empty key", ""},
		{"garbage", "not-base64-fernet-key!!!"},
		{"too short", "dG9vc2hvcnQ="},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewEncryptor(tt.key)
			if err == nil {
				t.Errorf("expected error for key %q, got nil", tt.key)
			}
		})
	}
}

// Multi-key rotation: ciphertext written under an old key must still
// decrypt after a new key is promoted to primary, and new writes must
// use the new primary. This is the load-bearing property of the
// rotation procedure documented in docs/secret-rotation-runbook.md.
func TestEncryptorMultiKeyRotation(t *testing.T) {
	oldKey, _ := GenerateKey()
	newKey, _ := GenerateKey()

	// Step 0: original encryptor, single key.
	encOld, err := NewEncryptor(oldKey)
	if err != nil {
		t.Fatalf("NewEncryptor(old): %v", err)
	}
	if encOld.KeyCount() != 1 {
		t.Errorf("KeyCount = %d, want 1", encOld.KeyCount())
	}
	tokenUnderOld, err := encOld.Encrypt("legacy-row")
	if err != nil {
		t.Fatalf("Encrypt(old): %v", err)
	}

	// Step 1: rotation in flight — new key is primary, old is fallback.
	encMixed, err := NewEncryptor(newKey + "," + oldKey)
	if err != nil {
		t.Fatalf("NewEncryptor(new,old): %v", err)
	}
	if encMixed.KeyCount() != 2 {
		t.Errorf("KeyCount = %d, want 2", encMixed.KeyCount())
	}

	// Old ciphertext still decrypts.
	got, err := encMixed.Decrypt(tokenUnderOld)
	if err != nil {
		t.Fatalf("Decrypt(old token): %v", err)
	}
	if got != "legacy-row" {
		t.Errorf("got %q, want %q", got, "legacy-row")
	}

	// New writes go to the new primary — decryptable by the new-only
	// encryptor that step 3 leaves us in.
	tokenUnderNew, err := encMixed.Encrypt("freshly-written")
	if err != nil {
		t.Fatalf("Encrypt(mixed): %v", err)
	}
	encNewOnly, err := NewEncryptor(newKey)
	if err != nil {
		t.Fatalf("NewEncryptor(new): %v", err)
	}
	got, err = encNewOnly.Decrypt(tokenUnderNew)
	if err != nil {
		t.Fatalf("Decrypt(new token under new-only): %v", err)
	}
	if got != "freshly-written" {
		t.Errorf("got %q, want %q", got, "freshly-written")
	}

	// And the old ciphertext fails under the new-only encryptor — this
	// is the "you must re-encrypt before dropping the old key" gate.
	if _, err := encNewOnly.Decrypt(tokenUnderOld); err == nil {
		t.Error("expected old-keyed ciphertext to FAIL under new-only encryptor")
	}
}

// Whitespace + empty entries in the comma-separated key list are
// tolerated so config files can use either inline commas or
// multi-line YAML strings.
func TestNewEncryptorWhitespaceTolerance(t *testing.T) {
	k1, _ := GenerateKey()
	k2, _ := GenerateKey()

	enc, err := NewEncryptor("  " + k1 + " , " + k2 + " ,  ")
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	if enc.KeyCount() != 2 {
		t.Errorf("KeyCount = %d, want 2", enc.KeyCount())
	}
}
