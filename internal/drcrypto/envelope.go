// Package drcrypto implements the management-plane DR crypto contract used by
// the backup, retention and restore-drill jobs (`dr-crypto` in the DR image):
// authenticated streaming AES-256-GCM envelopes, an HMAC-authenticated closed
// backup manifest, and a deterministic closed key bundle. It replaces
// deploy/dr/dr_crypto.py.
//
// Envelope format (schema 2): MAGIC, uint32 BE header length, canonical JSON
// header, then STREAM chunks. Chunk i is AES-256-GCM over at most ChunkBytes
// of plaintext with nonce = nonce_prefix(8) || uint32 BE(i) and associated
// data = SHA-256(header) || uint64 BE(i) || last-flag, so reordering,
// truncation at a chunk boundary and header substitution all fail
// authentication. Every chunk is authenticated before any of it is written.
package drcrypto

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
)

// Contract constants.
const (
	Magic                 = "ASTRODR2\n"
	EnvelopeSchemaVersion = 2
	Algorithm             = "AES-256-GCM-STREAM"
	KDFName               = "PBKDF2-HMAC-SHA256"
	KDFIterations         = 600_000
	ChunkBytes            = 1 << 20
	tagBytes              = 16
	maxHeaderBytes        = 64 * 1024
)

// Purposes accepted by encrypt/decrypt.
var Purposes = []string{"database-dump", "key-bundle"}

// Error is an input or authentication contract violation.
type Error struct{ msg string }

func (e *Error) Error() string { return e.msg }

func failf(format string, args ...any) error { return &Error{msg: fmt.Sprintf(format, args...)} }

type envelopeKDF struct {
	Iterations int    `json:"iterations"`
	Name       string `json:"name"`
	Salt       string `json:"salt"`
}

type envelopeHeader struct {
	Algorithm     string            `json:"algorithm"`
	ChunkBytes    int               `json:"chunk_bytes"`
	Context       map[string]string `json:"context"`
	KDF           envelopeKDF       `json:"kdf"`
	NoncePrefix   string            `json:"nonce_prefix"`
	Purpose       string            `json:"purpose"`
	SchemaVersion int               `json:"schema_version"`
}

// ParseContext parses a non-empty JSON object of non-empty strings.
func ParseContext(raw string) (map[string]string, error) {
	var generic map[string]any
	if err := json.Unmarshal([]byte(raw), &generic); err != nil {
		return nil, failf("context must be valid JSON")
	}
	if len(generic) == 0 {
		return nil, failf("context must be a non-empty object")
	}
	out := make(map[string]string, len(generic))
	for key, value := range generic {
		text, ok := value.(string)
		if key == "" || !ok || text == "" {
			return nil, failf("context keys and values must be non-empty strings")
		}
		out[key] = text
	}
	return out, nil
}

// ReadSecret reads the wrapping passphrase (trailing newlines trimmed).
func ReadSecret(path string) ([]byte, error) {
	if err := requireRegularFile(path, "passphrase"); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path) // #nosec G304 -- operator-mounted secret path
	if err != nil {
		return nil, err
	}
	secret := bytes.TrimRight(raw, "\r\n")
	if len(secret) < 32 {
		return nil, failf("wrapping passphrase must contain at least 32 bytes")
	}
	return secret, nil
}

func deriveKey(secret, salt []byte, iterations, length int) ([]byte, error) {
	return pbkdf2.Key(sha256.New, string(secret), salt, iterations, length)
}

func chunkAAD(headerDigest []byte, index uint64, last bool) []byte {
	aad := make([]byte, 0, len(headerDigest)+9)
	aad = append(aad, headerDigest...)
	aad = binary.BigEndian.AppendUint64(aad, index)
	if last {
		return append(aad, 1)
	}
	return append(aad, 0)
}

func chunkNonce(prefix []byte, index uint64) ([]byte, error) {
	if index > 0xFFFFFFFF {
		return nil, failf("backup envelope exceeds the chunk counter")
	}
	return binary.BigEndian.AppendUint32(slices.Clone(prefix), uint32(index)), nil
}

// EncryptFile seals source into destination.
func EncryptFile(source, destination, passphrase, purpose string, context map[string]string) error {
	if !slices.Contains(Purposes, purpose) {
		return failf("purpose must be one of %v", Purposes)
	}
	if err := requireRegularFile(source, "plaintext input"); err != nil {
		return err
	}
	secret, err := ReadSecret(passphrase)
	if err != nil {
		return err
	}
	salt, prefix := make([]byte, 16), make([]byte, 8)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	if _, err := rand.Read(prefix); err != nil {
		return err
	}
	header := envelopeHeader{
		Algorithm: Algorithm, ChunkBytes: ChunkBytes, Context: maps.Clone(context),
		KDF:         envelopeKDF{Iterations: KDFIterations, Name: KDFName, Salt: base64.StdEncoding.EncodeToString(salt)},
		NoncePrefix: base64.StdEncoding.EncodeToString(prefix), Purpose: purpose, SchemaVersion: EnvelopeSchemaVersion,
	}
	headerRaw, err := CanonicalJSON(header)
	if err != nil {
		return err
	}
	aead, err := envelopeAEAD(secret, salt)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(headerRaw)
	in, err := os.Open(source) // #nosec G304 -- operator-selected backup input
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	return writeAtomically(destination, func(out io.Writer) error {
		prelude := append([]byte(Magic), binary.BigEndian.AppendUint32(nil, uint32(len(headerRaw)))...) // #nosec G115 -- canonical header is bounded
		if _, err := out.Write(append(prelude, headerRaw...)); err != nil {
			return err
		}
		return streamChunks(in, ChunkBytes, func(index uint64, chunk []byte, last bool) error {
			nonce, err := chunkNonce(prefix, index)
			if err != nil {
				return err
			}
			_, err = out.Write(aead.Seal(nil, nonce, chunk, chunkAAD(digest[:], index, last)))
			return err
		})
	})
}

func envelopeAEAD(secret, salt []byte) (cipher.AEAD, error) {
	key, err := deriveKey(secret, salt, KDFIterations, 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func parseHeader(in io.Reader) (envelopeHeader, []byte, error) {
	magic := make([]byte, len(Magic))
	if _, err := io.ReadFull(in, magic); err != nil || string(magic) != Magic {
		return envelopeHeader{}, nil, failf("unsupported backup envelope magic")
	}
	var length [4]byte
	if _, err := io.ReadFull(in, length[:]); err != nil {
		return envelopeHeader{}, nil, failf("truncated backup envelope header")
	}
	size := binary.BigEndian.Uint32(length[:])
	if size < 2 || size > maxHeaderBytes {
		return envelopeHeader{}, nil, failf("backup envelope header length is invalid")
	}
	raw := make([]byte, size)
	if _, err := io.ReadFull(in, raw); err != nil {
		return envelopeHeader{}, nil, failf("truncated backup envelope header")
	}
	var header envelopeHeader
	if err := decodeClosed(raw, &header); err != nil {
		return envelopeHeader{}, nil, failf("backup envelope header has an open or incomplete schema")
	}
	canonical, err := CanonicalJSON(header)
	if err != nil || !bytes.Equal(canonical, raw) {
		return envelopeHeader{}, nil, failf("backup envelope header is not canonical JSON")
	}
	if header.SchemaVersion != EnvelopeSchemaVersion || header.Algorithm != Algorithm || header.ChunkBytes != ChunkBytes {
		return envelopeHeader{}, nil, failf("unsupported backup envelope version or algorithm")
	}
	if header.KDF.Name != KDFName || header.KDF.Iterations != KDFIterations {
		return envelopeHeader{}, nil, failf("unsupported backup envelope KDF")
	}
	if header.Purpose == "" {
		return envelopeHeader{}, nil, failf("backup envelope purpose is invalid")
	}
	if len(header.Context) == 0 {
		return envelopeHeader{}, nil, failf("backup envelope context is invalid")
	}
	for key, value := range header.Context {
		if key == "" || value == "" {
			return envelopeHeader{}, nil, failf("backup envelope context is invalid")
		}
	}
	return header, raw, nil
}

// DecryptFile opens source into destination after checking purpose/context.
// No byte of unauthenticated plaintext is written; on any failure the
// destination does not exist.
func DecryptFile(source, destination, passphrase, expectedPurpose string, expectedContext map[string]string) error {
	if err := requireRegularFile(source, "encrypted input"); err != nil {
		return err
	}
	secret, err := ReadSecret(passphrase)
	if err != nil {
		return err
	}
	in, err := os.Open(source) // #nosec G304 -- operator-selected backup input
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	header, raw, err := parseHeader(in)
	if err != nil {
		return err
	}
	if header.Purpose != expectedPurpose {
		return failf("backup envelope purpose does not match the requested restore")
	}
	if !maps.Equal(header.Context, expectedContext) {
		return failf("backup envelope context does not match the requested restore")
	}
	salt, err := unb64(header.KDF.Salt, "kdf.salt", 16)
	if err != nil {
		return err
	}
	prefix, err := unb64(header.NoncePrefix, "nonce_prefix", 8)
	if err != nil {
		return err
	}
	aead, err := envelopeAEAD(secret, salt)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(raw)
	return writeAtomically(destination, func(out io.Writer) error {
		return streamChunks(in, ChunkBytes+tagBytes, func(index uint64, chunk []byte, last bool) error {
			if len(chunk) < tagBytes || (!last && len(chunk) != ChunkBytes+tagBytes) {
				return failf("truncated backup envelope ciphertext")
			}
			nonce, err := chunkNonce(prefix, index)
			if err != nil {
				return err
			}
			plain, err := aead.Open(nil, nonce, chunk, chunkAAD(digest[:], index, last))
			if err != nil {
				return failf("backup envelope authentication failed")
			}
			_, err = out.Write(plain)
			return err
		})
	})
}

func unb64(value, field string, length int) ([]byte, error) {
	raw, err := base64.StdEncoding.Strict().DecodeString(value)
	if err != nil {
		return nil, failf("%s is not valid base64", field)
	}
	if length > 0 && len(raw) != length {
		return nil, failf("%s must decode to %d bytes", field, length)
	}
	return raw, nil
}

// streamChunks reads in as consecutive chunks of size bytes and calls fn for
// each with its index and whether it is the final chunk. The final chunk is
// detected by look-ahead, so an empty input yields one empty final chunk.
func streamChunks(in io.Reader, size int, fn func(index uint64, chunk []byte, last bool) error) error {
	read := func() ([]byte, error) {
		buf := make([]byte, size)
		n, err := io.ReadFull(in, buf)
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
			return nil, err
		}
		return buf[:n], nil
	}
	current, err := read()
	if err != nil {
		return err
	}
	for index := uint64(0); ; index++ {
		next, err := read()
		if err != nil {
			return err
		}
		last := len(next) == 0
		if err := fn(index, current, last); err != nil {
			return err
		}
		if last {
			return nil
		}
		current = next
	}
}
