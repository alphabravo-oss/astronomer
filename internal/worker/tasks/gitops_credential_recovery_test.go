package tasks

import (
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/crypto"
)

func gitOpsRecoveryCredentials(t *testing.T) (*crypto.Encryptor, *crypto.Encryptor, sqlc.GitopsRegistrationSource) {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal("generate disposable key")
	}
	other, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal("generate disposable wrong key")
	}
	encryptor, err := crypto.NewEncryptor("old-fixture:" + key)
	if err != nil {
		t.Fatal("initialize disposable encryptor")
	}
	wrong, err := crypto.NewEncryptor("current-fixture:" + other)
	if err != nil {
		t.Fatal("initialize disposable wrong encryptor")
	}
	sealed, err := encryptor.EncryptString("disposable-credential")
	if err != nil {
		t.Fatal("encrypt disposable credential")
	}
	src := sqlc.GitopsRegistrationSource{Name: "owned-fixture", AuthMode: "https_token"}
	src.AuthKeyID, src.AuthAlgorithm, src.AuthCiphertext, src.AuthNonce = sealed.Parts()
	return encryptor, wrong, src
}

func TestGitOpsEncryptedCredentialRecoveryFailsClosed(t *testing.T) {
	_, wrong, src := gitOpsRecoveryCredentials(t)
	tampered := src
	tampered.AuthCiphertext = append([]byte(nil), src.AuthCiphertext...)
	tampered.AuthCiphertext[len(tampered.AuthCiphertext)-1] ^= 1
	unknownAlg := src
	unknownAlg.AuthAlgorithm = "aes-256-gcm"
	for _, tc := range []struct {
		name string
		src  sqlc.GitopsRegistrationSource
	}{{"current", src}, {"tampered", tampered}, {"unsupported-algorithm", unknownAlg}} {
		for _, decryptor := range []struct {
			name  string
			value GitOpsDecryptor
		}{{"missing", nil}, {"wrong", wrong}} {
			t.Run(tc.name+"/"+decryptor.name, func(t *testing.T) {
				runtime := GitOpsRuntime{Deps: GitOpsDeps{Decryptor: decryptor.value}}
				value, err := runtime.decryptGitAuth(tc.src)
				if err == nil || value != "" {
					t.Fatal("unavailable encrypted credential was returned")
				}
				method, err := runtime.buildGitAuth(tc.src)
				if err == nil || method != nil {
					t.Fatal("encrypted recovery failure produced an outbound Git auth method")
				}
			})
		}
	}
}

func TestGitOpsCredentialRecoveryDecryptsSealedCredential(t *testing.T) {
	correct, _, src := gitOpsRecoveryCredentials(t)
	value, err := (GitOpsRuntime{Deps: GitOpsDeps{Decryptor: correct}}).decryptGitAuth(src)
	if err != nil || value != "disposable-credential" {
		t.Fatal("sealed credential failed recovery")
	}
	method, err := (GitOpsRuntime{Deps: GitOpsDeps{Decryptor: correct}}).buildGitAuth(src)
	if err != nil || method == nil {
		t.Fatalf("buildGitAuth: %T %v", method, err)
	}
	// A credentialed mode with no sealed value is refused, not sent empty.
	empty := sqlc.GitopsRegistrationSource{Name: "empty", AuthMode: "https_token"}
	if method, err := (GitOpsRuntime{Deps: GitOpsDeps{Decryptor: correct}}).buildGitAuth(empty); err == nil || method != nil {
		t.Fatal("https_token source without a sealed credential was accepted")
	}
}
