package main

import (
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
)

// TEST-04 / CORR-04: shipped SQL builders for batch SELECT + CAS UPDATE.
func TestSelectBatchSQL_HonorsLimitOffsetPlaceholders(t *testing.T) {
	sql := selectBatchSQL(target{table: "cloud_credentials", idCol: "id", column: "data_encrypted"})
	for _, want := range []string{
		"FROM cloud_credentials",
		"data_encrypted",
		"ORDER BY id",
		"LIMIT $1 OFFSET $2",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("select SQL missing %q: %s", want, sql)
		}
	}
}

func TestCASUpdateSQL_RequiresOldCiphertext(t *testing.T) {
	sql := casUpdateSQL(target{table: "sso_configurations", idCol: "id", column: "client_secret_encrypted"})
	// Must CAS on both primary key and previous ciphertext.
	if !strings.Contains(sql, "WHERE id = $2 AND client_secret_encrypted = $3") {
		t.Fatalf("CAS UPDATE must pin old ciphertext: %s", sql)
	}
	if strings.Contains(sql, "WHERE id = $2;") || !strings.Contains(sql, "$3") {
		t.Fatalf("CAS UPDATE must not be id-only: %s", sql)
	}
}

func TestRewriteEncryptorRoundTrip(t *testing.T) {
	key, err := auth.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	enc, err := auth.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	ct, err := enc.Encrypt("rotation-secret")
	if err != nil {
		t.Fatal(err)
	}
	plain, err := enc.Decrypt(ct)
	if err != nil {
		t.Fatal(err)
	}
	newCT, err := enc.Encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	again, err := enc.Decrypt(newCT)
	if err != nil || again != "rotation-secret" {
		t.Fatalf("got %q err %v", again, err)
	}
	// CAS would compare ct != newCT for concurrent writer detection.
	if newCT == "" || ct == "" {
		t.Fatal("empty ciphertext")
	}
}
