package tasks

import (
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func TestMaterializeClusterRegistryPasswordDecryptsEncryptedColumn(t *testing.T) {
	t.Cleanup(ResetClusterRegistryApply)
	key, err := auth.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	enc, err := auth.NewEncryptor(key)
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	ciphertext, err := enc.Encrypt("s3cr3t")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	ConfigureClusterRegistryApply(ClusterRegistryApplyDeps{Encryptor: enc})

	cfg := sqlc.ClusterRegistryConfig{RegistryPasswordEncrypted: ciphertext}
	if err := materializeClusterRegistryPassword(&cfg); err != nil {
		t.Fatalf("materializeClusterRegistryPassword: %v", err)
	}
	if cfg.RegistryPassword != "s3cr3t" {
		t.Fatalf("expected decrypted password, got %q", cfg.RegistryPassword)
	}
}

func TestMaterializeClusterRegistryPasswordRequiresEncryptor(t *testing.T) {
	t.Cleanup(ResetClusterRegistryApply)
	cfg := sqlc.ClusterRegistryConfig{RegistryPasswordEncrypted: "ciphertext"}
	if err := materializeClusterRegistryPassword(&cfg); err == nil {
		t.Fatal("expected error for encrypted password without encryptor")
	}
}
