package tasks

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func gitOpsRecoveryCredentials(t *testing.T) (*auth.Encryptor, *auth.Encryptor, string, string) {
	t.Helper()
	key, err := auth.GenerateKey()
	if err != nil {
		t.Fatal("generate disposable key")
	}
	other, err := auth.GenerateKey()
	if err != nil {
		t.Fatal("generate disposable wrong key")
	}
	encryptor, err := auth.NewEncryptor("old-fixture:" + key)
	if err != nil {
		t.Fatal("initialize disposable encryptor")
	}
	wrong, err := auth.NewEncryptor("current-fixture:" + other)
	if err != nil {
		t.Fatal("initialize disposable wrong encryptor")
	}
	envelope, err := encryptor.Encrypt("disposable-credential")
	if err != nil {
		t.Fatal("encrypt disposable credential")
	}
	legacy := strings.SplitN(envelope, ":", 5)[4]
	return encryptor, wrong, envelope, legacy
}

func TestGitOpsEncryptedCredentialRecoveryFailsClosed(t *testing.T) {
	_, wrong, current, legacy := gitOpsRecoveryCredentials(t)
	for _, tc := range []struct{ name, blob string }{
		{"current", current}, {"legacy", legacy},
		{"unsupported-envelope", "astronomer:v2:fernet:missing:unrecognized"},
		{"malformed-envelope", "astronomer:v1:fernet:"},
	} {
		for _, decryptor := range []struct {
			name  string
			value GitOpsDecryptor
		}{{"missing", nil}, {"wrong", wrong}} {
			t.Run(tc.name+"/"+decryptor.name, func(t *testing.T) {
				runtime := GitOpsRuntime{Deps: GitOpsDeps{Decryptor: decryptor.value}}
				value, err := runtime.decryptGitAuth(tc.blob)
				if err == nil || value != "" {
					t.Fatal("unavailable encrypted credential was returned as plaintext")
				}
				method, err := runtime.buildGitAuth(sqlc.GitopsRegistrationSource{Name: "owned-fixture", AuthMode: "https_token", AuthEncrypted: tc.blob})
				if err == nil || method != nil {
					t.Fatal("encrypted recovery failure produced an outbound Git auth method")
				}
			})
		}
	}
}

func TestGitOpsCredentialRecoveryPreservesSupportedFormats(t *testing.T) {
	correct, wrong, current, legacy := gitOpsRecoveryCredentials(t)
	for _, blob := range []string{current, legacy} {
		value, err := (GitOpsRuntime{Deps: GitOpsDeps{Decryptor: correct}}).decryptGitAuth(blob)
		if err != nil || value != "disposable-credential" {
			t.Fatal("supported encrypted credential failed recovery")
		}
	}
	base64Plain := base64.URLEncoding.EncodeToString([]byte(strings.Repeat("ordinary", 10)))
	for _, plaintext := range []string{"", "legacy-plaintext-token", "https://example.invalid/plaintext", base64Plain} {
		for _, decryptor := range []GitOpsDecryptor{nil, wrong} {
			value, err := (GitOpsRuntime{Deps: GitOpsDeps{Decryptor: decryptor}}).decryptGitAuth(plaintext)
			if err != nil || value != plaintext {
				t.Fatal("characterized plaintext compatibility changed")
			}
		}
	}
}
