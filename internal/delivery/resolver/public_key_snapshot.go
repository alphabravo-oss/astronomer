package resolver

import (
	"bytes"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/sigstore/sigstore/pkg/cryptoutils"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// PublicKeySnapshot captures operator-mounted public trust before QA catalog
// authentication. Callers give its owned directory to the canonical loader and
// its cloned public material to a constructor-only keyed consumer adapter.
// Production OIDC configuration never selects this keyed qualification lane.
type PublicKeySnapshot struct {
	directory string
	reference string
	material  []byte
	mutex     sync.Mutex
	closeOnce sync.Once
}

func CaptureMountedPublicKey(directory, reference string) (*PublicKeySnapshot, error) {
	verifier, err := NewExecVerifier(directory)
	if err != nil {
		return nil, err
	}
	material, _, err := verifier.readTrustKey(reference, ".pub")
	if err != nil {
		return nil, err
	}
	defer clear(material)
	if err := validateMountedPublicKey(material); err != nil {
		return nil, err
	}
	owned, err := os.MkdirTemp("", "astronomer-qa-public-trust-")
	if err != nil {
		return nil, errors.New("owned public trust directory unavailable")
	}
	if err := os.WriteFile(filepath.Join(owned, reference+".pub"), material, 0400); err != nil {
		_ = os.RemoveAll(owned)
		return nil, errors.New("owned public trust snapshot unavailable")
	}
	return &PublicKeySnapshot{directory: owned, reference: reference, material: bytes.Clone(material)}, nil
}

func (s *PublicKeySnapshot) Directory() string { return s.directory }
func (s *PublicKeySnapshot) Reference() string { return s.reference }
func (s *PublicKeySnapshot) Material() []byte {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return bytes.Clone(s.material)
}
func (s *PublicKeySnapshot) Close() {
	s.closeOnce.Do(func() {
		_ = os.RemoveAll(s.directory)
		s.mutex.Lock()
		defer s.mutex.Unlock()
		clear(s.material)
		s.material = nil
	})
}

// Shared by the historical keyed adapter and physical authority snapshot.
func validateMountedPublicKey(material []byte) error {
	block, rest := pem.Decode(material)
	if len(material) > protocol.MaxDeliveryCredentialValue || !bytes.HasPrefix(bytes.TrimSpace(material), []byte("-----BEGIN PUBLIC KEY-----")) || block == nil || block.Type != "PUBLIC KEY" || len(block.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 {
		return errors.New("mounted public signing key is invalid")
	}
	if _, err := cryptoutils.UnmarshalPEMToPublicKey(material); err != nil {
		return errors.New("mounted public signing key is invalid")
	}
	return nil
}
