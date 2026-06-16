package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/crypto/pbkdf2"
)

func TestVerifyPassword_Bcrypt(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	ok, rehash, err := VerifyPassword(string(hash), "secret")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !ok {
		t.Fatal("expected match")
	}
	if rehash {
		t.Fatal("bcrypt should not request rehash")
	}

	ok, _, err = VerifyPassword(string(hash), "wrong")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if ok {
		t.Fatal("expected mismatch")
	}
}

func TestVerifyPassword_PBKDF2SHA256(t *testing.T) {
	password := "p@ssw0rd"
	salt := "saltyMcSalt"
	iterations := 1000
	dk := pbkdf2.Key([]byte(password), []byte(salt), iterations, sha256.Size, sha256.New)
	hash := strings.Join([]string{
		"pbkdf2_sha256",
		"1000",
		salt,
		base64.StdEncoding.EncodeToString(dk),
	}, "$")

	ok, rehash, err := VerifyPassword(hash, password)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !ok {
		t.Fatal("expected match")
	}
	if !rehash {
		t.Fatal("pbkdf2 verification should request rehash")
	}

	ok, _, _ = VerifyPassword(hash, "wrong")
	if ok {
		t.Fatal("expected mismatch with wrong password")
	}
}

func TestVerifyPassword_Argon2id(t *testing.T) {
	password := "p@ssw0rd"
	salt := []byte("saltsalt12345678")
	memory := uint32(64 * 1024)
	time := uint32(1)
	threads := uint8(2)
	keyLen := uint32(16)
	dk := argon2.IDKey([]byte(password), salt, time, memory, threads, keyLen)

	hash := strings.Join([]string{
		"argon2",
		"argon2id",
		"v=19",
		"m=65536,t=1,p=2",
		strings.TrimRight(base64.StdEncoding.EncodeToString(salt), "="),
		strings.TrimRight(base64.StdEncoding.EncodeToString(dk), "="),
	}, "$")

	ok, rehash, err := VerifyPassword(hash, password)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !ok {
		t.Fatal("expected argon2 match")
	}
	if !rehash {
		t.Fatal("argon2 verification should request rehash")
	}
}

func TestDetectHashScheme(t *testing.T) {
	cases := map[string]HashScheme{
		"":                                  HashSchemeUnknown,
		"$2a$10$abcd":                       HashSchemeBcrypt,
		"$2b$12$abcd":                       HashSchemeBcrypt,
		"$2y$12$abcd":                       HashSchemeBcrypt,
		"pbkdf2_sha256$390000$salt$payload": HashSchemePBKDF2SHA256,
		"pbkdf2_sha1$390000$salt$payload":   HashSchemePBKDF2SHA1,
		"argon2$argon2id$v=19$m=64,t=1,p=4$salt$hash": HashSchemeArgon2,
		"random-other": HashSchemeUnknown,
	}
	for input, want := range cases {
		if got := DetectHashScheme(input); got != want {
			t.Errorf("DetectHashScheme(%q) = %v, want %v", input, got, want)
		}
	}
}
