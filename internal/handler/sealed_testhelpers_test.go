package handler

import "encoding/base64"

// ciphertextB64 is how a sealed ciphertext would appear if it leaked into a
// JSON body ([]byte encodes as standard base64); leak assertions search for it.
func ciphertextB64(ciphertext []byte) string {
	if len(ciphertext) == 0 {
		return "\x00no-ciphertext\x00"
	}
	return base64.StdEncoding.EncodeToString(ciphertext)
}
