package catalog

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/crypto"
)

type repositoryServiceCipher struct{}

func (repositoryServiceCipher) Encrypt(plaintext []byte) (crypto.Sealed, error) {
	return stubSealed("sealed", string(plaintext)), nil
}
func (repositoryServiceCipher) Decrypt(sealed crypto.Sealed) ([]byte, error) {
	if sealed.KeyID != "sealed" {
		return nil, errors.New("invalid ciphertext")
	}
	return sealed.Ciphertext, nil
}

func TestRepositoryServicePrepareCreateOwnsCredentialAndRepositoryPolicy(t *testing.T) {
	params, err := NewRepositoryService().PrepareCreate(RepositoryCreateInput{
		Name: "private", URL: " oci://registry.example.test/charts ",
		AuthConfig: json.RawMessage(`{"username":"operator","password":"secret"}`),
	}, repositoryServiceCipher{})
	if err != nil {
		t.Fatal(err)
	}
	if params.Url != "oci://registry.example.test/charts" || params.RepoType != "oci" || params.AuthType != "basic" || !params.Enabled {
		t.Fatalf("prepared repository = %+v", params)
	}
	if params.AuthConfigKeyID == "" || string(params.AuthConfig) != `{"username":"operator"}` {
		t.Fatalf("credential envelope/projection = %q %s", params.AuthConfigKeyID, params.AuthConfig)
	}
}

func TestRepositoryServicePrepareUpdatePreservesRedactedSecrets(t *testing.T) {
	existing := withSealed(sqlc.HelmRepository{
		Name:     "private",
		Url:      "https://charts.example.test",
		AuthType: "basic",
		Enabled:  true,
	}, stubSealed("sealed", `{"username":"operator","password":"secret"}`))
	config := json.RawMessage(`{"username":"operator","password":"<encrypted>"}`)
	params, err := NewRepositoryService().PrepareUpdate(existing, RepositoryUpdateInput{
		AuthConfig: &config, RedactedSecretValue: "<encrypted>",
	}, repositoryServiceCipher{}, repositoryServiceCipher{})
	if err != nil {
		t.Fatal(err)
	}
	decrypted, decryptErr := (repositoryServiceCipher{}).Decrypt(crypto.FromParts(params.AuthConfigKeyID, params.AuthConfigAlgorithm, params.AuthConfigCiphertext, params.AuthConfigNonce))
	if decryptErr != nil || string(decrypted) != `{"password":"secret","username":"operator"}` {
		t.Fatalf("redacted secret was not preserved: decrypted=%q err=%v", decrypted, decryptErr)
	}
}

func TestRepositoryServiceRejectsUnencryptedSecretPersistence(t *testing.T) {
	_, err := NewRepositoryService().PrepareCreate(RepositoryCreateInput{
		Name: "private", URL: "https://charts.example.test", AuthConfig: json.RawMessage(`{"token":"secret"}`),
	}, nil)
	if !errors.Is(err, ErrRepositoryCredentialEncryptionUnavailable) {
		t.Fatalf("error = %v, want encryption-unavailable", err)
	}
}
