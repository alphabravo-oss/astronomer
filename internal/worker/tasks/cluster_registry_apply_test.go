package tasks

import (
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/crypto"
)

func TestDecryptClusterRegistryPasswordOpensSealedColumn(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	enc, err := crypto.NewEncryptor(key)
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	sealed, err := enc.EncryptString("s3cr3t")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	runtime := ClusterRegistryRuntime{Deps: ClusterRegistryApplyDeps{Encryptor: enc}}

	var cfg sqlc.ClusterRegistryConfig
	cfg.RegistryPasswordKeyID, cfg.RegistryPasswordAlgorithm, cfg.RegistryPasswordCiphertext, cfg.RegistryPasswordNonce = sealed.Parts()
	password, err := runtime.decryptClusterRegistryPassword(cfg)
	if err != nil {
		t.Fatalf("decryptClusterRegistryPassword: %v", err)
	}
	if password != "s3cr3t" {
		t.Fatalf("expected decrypted password, got %q", password)
	}
	if none, err := runtime.decryptClusterRegistryPassword(sqlc.ClusterRegistryConfig{}); err != nil || none != "" {
		t.Fatalf("row without sealed password: %q %v", none, err)
	}
}

func TestDecryptClusterRegistryPasswordRequiresEncryptor(t *testing.T) {
	runtime := ClusterRegistryRuntime{}
	cfg := sqlc.ClusterRegistryConfig{RegistryPasswordKeyID: "k", RegistryPasswordAlgorithm: crypto.AlgorithmAgeX25519, RegistryPasswordCiphertext: []byte("x")}
	if _, err := runtime.decryptClusterRegistryPassword(cfg); err == nil {
		t.Fatal("expected error for sealed password without encryptor")
	}
}
