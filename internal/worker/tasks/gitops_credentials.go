package tasks

import (
	"errors"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/crypto"
)

// decryptGitAuth opens the source's sealed auth family. There is no plaintext
// or ciphertext fallback: a missing key or failed decrypt is an error, so the
// stored ciphertext is never handed to go-git as a credential.
func (runtime GitOpsRuntime) decryptGitAuth(src sqlc.GitopsRegistrationSource) (string, error) {
	if runtime.Deps.Decryptor == nil {
		return "", errors.New("Git credential decryptor unavailable")
	}
	return runtime.Deps.Decryptor.DecryptString(crypto.FromParts(src.AuthKeyID, src.AuthAlgorithm, src.AuthCiphertext, src.AuthNonce))
}
