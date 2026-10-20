package drcrypto

import (
	"archive/tar"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"time"
)

const (
	maxKeyFileBytes          = 1 << 20
	maxKeyBundleBytes        = 16 << 20
	legacyCBCKDFIterations   = 10_000
	legacyCBCMaxPayloadBytes = maxKeyBundleBytes + 32
)

var (
	secretKeyNamePattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]{0,251}[A-Za-z0-9])?$`)
	requiredKeys         = []string{"ASTRONOMER_ENCRYPTION_KEY", "SECRET_KEY"}
)

func decodeNumbers(raw []byte, into any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	return decoder.Decode(into)
}

// listKeyFiles validates source and returns its sorted key file names.
func listKeyFiles(source string) ([]string, error) {
	info, err := os.Lstat(source)
	if err != nil || !info.IsDir() {
		return nil, failf("key source must be a regular directory")
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, failf("key source directory is empty")
	}
	names := make([]string, 0, len(entries))
	var total int64
	for _, entry := range entries {
		fileInfo, err := os.Lstat(filepath.Join(source, entry.Name()))
		if err != nil || !fileInfo.Mode().IsRegular() || !secretKeyNamePattern.MatchString(entry.Name()) {
			return nil, failf("key source may contain only top-level regular files")
		}
		if fileInfo.Size() <= 0 || fileInfo.Size() > maxKeyFileBytes {
			return nil, failf("key file %s has an unsafe size", entry.Name())
		}
		total += fileInfo.Size()
		names = append(names, entry.Name())
	}
	if total > maxKeyBundleBytes {
		return nil, failf("key bundle exceeds the size limit")
	}
	if err := requireKeys(func(name string) bool { return slices.Contains(names, name) }); err != nil {
		return nil, err
	}
	slices.Sort(names)
	return names, nil
}

func requireKeys(has func(string) bool) error {
	for _, required := range requiredKeys {
		if !has(required) {
			return failf("key bundle is missing %s", required)
		}
	}
	return nil
}

// CreateKeyBundle writes a deterministic tar of the top-level regular files
// in source (sorted, mode 0600, mtime 0, uid/gid 0).
func CreateKeyBundle(source, output string) error {
	names, err := listKeyFiles(source)
	if err != nil {
		return err
	}
	return writeAtomically(output, func(out io.Writer) error {
		archive := tar.NewWriter(out)
		for _, name := range names {
			content, err := os.ReadFile(filepath.Join(source, name)) // #nosec G304 -- validated top-level regular file
			if err != nil {
				return err
			}
			header := &tar.Header{Typeflag: tar.TypeReg, Name: name, Size: int64(len(content)), Mode: 0o600, ModTime: time.Unix(0, 0), Format: tar.FormatUSTAR}
			if err := archive.WriteHeader(header); err != nil {
				return err
			}
			if _, err := archive.Write(content); err != nil {
				return err
			}
		}
		return archive.Close()
	})
}

type keyMember struct {
	name    string
	content []byte
}

func readKeyMember(reader *tar.Reader, header *tar.Header, seen map[string]bool) (keyMember, error) {
	if header.Typeflag != tar.TypeReg || seen[header.Name] {
		return keyMember{}, failf("key bundle contains a link, non-file, or duplicate member")
	}
	if filepath.Base(header.Name) != header.Name || !secretKeyNamePattern.MatchString(header.Name) {
		return keyMember{}, failf("key bundle contains an unsafe member path")
	}
	if header.Size <= 0 || header.Size > maxKeyFileBytes {
		return keyMember{}, failf("key bundle member has an unsafe size")
	}
	content, err := io.ReadAll(io.LimitReader(reader, maxKeyFileBytes+1))
	if err != nil || int64(len(content)) != header.Size {
		return keyMember{}, failf("key bundle member size mismatch")
	}
	return keyMember{name: header.Name, content: content}, nil
}

// readKeyBundle validates every member of bundle before anything is written.
func readKeyBundle(bundle string) ([]keyMember, error) {
	file, err := os.Open(bundle) // #nosec G304 -- operator-selected bundle
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	var members []keyMember
	seen := map[string]bool{}
	var total int64
	reader := tar.NewReader(file)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, failf("key bundle is not a valid tar archive")
		}
		member, err := readKeyMember(reader, header, seen)
		if err != nil {
			return nil, err
		}
		if total += header.Size; total > maxKeyBundleBytes {
			return nil, failf("key bundle exceeds the size limit")
		}
		seen[member.name] = true
		members = append(members, member)
	}
	return members, requireKeys(func(name string) bool { return seen[name] })
}

func writeKeyFiles(directory string, members []keyMember) error {
	for _, m := range members {
		handle, err := os.OpenFile(filepath.Join(directory, m.name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- validated single-segment name
		if err != nil {
			return err
		}
		_, err = handle.Write(m.content)
		if closeErr := handle.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// ExtractKeyBundle validates the whole bundle before creating destination
// (0700) with 0600 files; links, nested paths, duplicates and oversize
// members are refused.
func ExtractKeyBundle(bundle, destination string) error {
	if err := requireRegularFile(bundle, "key bundle"); err != nil {
		return err
	}
	if _, err := os.Lstat(destination); err == nil {
		return failf("refusing to overwrite key extraction directory")
	}
	members, err := readKeyBundle(bundle)
	if err != nil {
		return err
	}
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		return err
	}
	temporary := filepath.Join(filepath.Dir(destination), "."+filepath.Base(destination)+"."+hex.EncodeToString(suffix))
	if err := os.Mkdir(temporary, 0o700); err != nil {
		return err
	}
	if err := writeKeyFiles(temporary, members); err != nil {
		_ = os.RemoveAll(temporary)
		return err
	}
	if err := os.Rename(temporary, destination); err != nil {
		_ = os.RemoveAll(temporary)
		return err
	}
	return nil
}

// LegacyDecryptKeyBundle reads a pre-GA OpenSSL "Salted__" AES-256-CBC key
// bundle (PBKDF2-SHA256, 10k iterations). CBC is unauthenticated, so it is
// never used by the automated drill and refuses to run on or after sunset.
func LegacyDecryptKeyBundle(source, destination, passphrase string, sunset, now time.Time) (string, error) {
	if !now.UTC().Before(sunset) {
		return "", failf("legacy AES-CBC compatibility expired on %s", sunset.Format("2006-01-02"))
	}
	if err := requireRegularFile(source, "legacy key bundle"); err != nil {
		return "", err
	}
	info, err := os.Stat(source)
	if err != nil {
		return "", err
	}
	if info.Size() > legacyCBCMaxPayloadBytes {
		return "", failf("legacy key bundle exceeds the size limit")
	}
	raw, err := os.ReadFile(source) // #nosec G304 -- operator-selected legacy bundle
	if err != nil {
		return "", err
	}
	if len(raw) < 32 || string(raw[:8]) != "Salted__" || (len(raw)-16)%aes.BlockSize != 0 {
		return "", failf("legacy key bundle is not an OpenSSL salted AES-CBC envelope")
	}
	secret, err := ReadSecret(passphrase)
	if err != nil {
		return "", err
	}
	material, err := deriveKey(secret, raw[8:16], legacyCBCKDFIterations, 48)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(material[:32])
	if err != nil {
		return "", err
	}
	plaintext := make([]byte, len(raw)-16)
	cipher.NewCBCDecrypter(block, material[32:]).CryptBlocks(plaintext, raw[16:])
	pad := int(plaintext[len(plaintext)-1])
	if pad < 1 || pad > aes.BlockSize || pad > len(plaintext) || !bytes.Equal(plaintext[len(plaintext)-pad:], bytes.Repeat([]byte{byte(pad)}, pad)) {
		return "", failf("legacy key bundle padding is invalid")
	}
	plaintext = plaintext[:len(plaintext)-pad]
	if err := writeAtomically(destination, func(out io.Writer) error {
		_, err := out.Write(plaintext)
		return err
	}); err != nil {
		return "", err
	}
	return "dr-crypto: WARNING: legacy AES-CBC input is unauthenticated; extract only with key-bundle-extract in an isolated environment", nil
}
