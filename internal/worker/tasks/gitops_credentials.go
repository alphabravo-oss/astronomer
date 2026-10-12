package tasks

import (
	"encoding/base64"
	"errors"
	"strings"
)

// decryptGitAuth retains the legacy-plaintext compatibility window while
// rejecting recognized encrypted values when their key is unavailable.
func (runtime GitOpsRuntime) decryptGitAuth(blob string) (string, error) {
	if blob == "" {
		return blob, nil
	}
	encrypted := strings.HasPrefix(blob, "astronomer:") || looksLikeFernetToken(blob)
	if runtime.Deps.Decryptor == nil {
		if encrypted {
			return "", errors.New("Git credential decryptor unavailable")
		}
		return blob, nil
	}
	if plaintext, err := runtime.Deps.Decryptor.Decrypt(blob); err == nil {
		return plaintext, nil
	} else if encrypted {
		return "", err
	}
	return blob, nil
}

func looksLikeFernetToken(value string) bool {
	decoded, err := base64.URLEncoding.DecodeString(value)
	return err == nil && len(decoded) >= 57 && decoded[0] == 0x80
}
