package config

import (
	"github.com/alphabravocompany/astronomer-go/pkg/crypto"
)

// LoadEncryptor builds the column-encryption keyring from
// ASTRONOMER_ENCRYPTION_KEY_FILE or ASTRONOMER_ENCRYPTION_KEY (exactly one may
// be set). It returns an error wrapping crypto.ErrNoKey when neither is set.
func (c *Config) LoadEncryptor() (*crypto.Encryptor, error) {
	if c == nil {
		return nil, crypto.ErrNoKey
	}
	return crypto.Load(c.EncryptionKey, c.EncryptionKeyFile)
}
