package drcrypto

import (
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

// ManifestSchemaVersion is the closed backup manifest schema.
const ManifestSchemaVersion = 1

var sha256Pattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

// EnvelopeRecord names the envelope contract of one payload.
type EnvelopeRecord struct {
	Algorithm     string `json:"algorithm"`
	Purpose       string `json:"purpose"`
	SchemaVersion int    `json:"schema_version"`
}

// Payload is one encrypted object the manifest authenticates.
type Payload struct {
	Envelope  EnvelopeRecord `json:"envelope"`
	Key       string         `json:"key"`
	SHA256    string         `json:"sha256"`
	SizeBytes int64          `json:"size_bytes"`
}

// Source identifies the installation and schema a backup came from.
type Source struct {
	ChartVersion          string `json:"chart_version"`
	DatabaseSchemaVersion int64  `json:"database_schema_version"`
	Identity              string `json:"identity"`
	ReleaseName           string `json:"release_name"`
	ReleaseNamespace      string `json:"release_namespace"`
}

// Authentication carries the HMAC over the canonical document without tag.
type Authentication struct {
	Algorithm     string `json:"algorithm"`
	KeyDerivation string `json:"key_derivation"`
	Salt          string `json:"salt"`
	Tag           string `json:"tag,omitempty"`
}

// Manifest is the closed backup manifest document.
type Manifest struct {
	Archive        Payload        `json:"archive"`
	Authentication Authentication `json:"authentication"`
	CreatedAt      string         `json:"created_at"`
	KeyBundle      Payload        `json:"key_bundle"`
	SchemaVersion  int            `json:"schema_version"`
	Source         Source         `json:"source"`
}

// ManifestInput is manifest-create's input.
type ManifestInput struct {
	Archive, ArchiveKey, KeyBundle, KeyBundleKey, Passphrase string
	Source                                                   Source
	CreatedAt, Output                                        string
}

func manifestAuthKey(secret, salt []byte) ([]byte, error) {
	return hkdf.Key(sha256.New, secret, salt, "astronomer-dr-manifest-v1", 32)
}

func sha256File(path string) (string, int64, error) {
	if err := requireRegularFile(path, "manifest payload"); err != nil {
		return "", 0, err
	}
	file, err := os.Open(path) // #nosec G304 -- operator-selected backup payload
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = file.Close() }()
	digest := sha256.New()
	size, err := io.Copy(digest, file)
	if err != nil {
		return "", 0, err
	}
	return "sha256:" + hex.EncodeToString(digest.Sum(nil)), size, nil
}

func payloadRecord(path, key, purpose string) (Payload, error) {
	if err := validateObjectKey(key, "payload key"); err != nil {
		return Payload{}, err
	}
	digest, size, err := sha256File(path)
	if err != nil {
		return Payload{}, err
	}
	return Payload{Envelope: EnvelopeRecord{Algorithm: Algorithm, Purpose: purpose, SchemaVersion: EnvelopeSchemaVersion}, Key: key, SHA256: digest, SizeBytes: size}, nil
}

func manifestTag(document Manifest, secret []byte) ([]byte, error) {
	salt, err := unb64(document.Authentication.Salt, "authentication.salt", 16)
	if err != nil {
		return nil, err
	}
	key, err := manifestAuthKey(secret, salt)
	if err != nil {
		return nil, err
	}
	document.Authentication.Tag = ""
	canonical, err := CanonicalJSON(document)
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(canonical)
	return mac.Sum(nil), nil
}

// CreateManifest writes an authenticated manifest.
func CreateManifest(in ManifestInput) error {
	if !strings.HasSuffix(in.CreatedAt, "Z") || !strings.Contains(in.CreatedAt, "T") {
		return failf("created-at must be a UTC RFC3339 timestamp")
	}
	for field, value := range map[string]string{"chart_version": in.Source.ChartVersion, "identity": in.Source.Identity, "release_name": in.Source.ReleaseName, "release_namespace": in.Source.ReleaseNamespace} {
		if value == "" {
			return failf("source.%s must be non-empty", field)
		}
	}
	archive, err := payloadRecord(in.Archive, in.ArchiveKey, "database-dump")
	if err != nil {
		return err
	}
	keyBundle, err := payloadRecord(in.KeyBundle, in.KeyBundleKey, "key-bundle")
	if err != nil {
		return err
	}
	secret, err := ReadSecret(in.Passphrase)
	if err != nil {
		return err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	document := Manifest{
		Archive: archive, CreatedAt: in.CreatedAt, KeyBundle: keyBundle, SchemaVersion: ManifestSchemaVersion, Source: in.Source,
		Authentication: Authentication{Algorithm: "HMAC-SHA256", KeyDerivation: "HKDF-SHA256", Salt: base64.StdEncoding.EncodeToString(salt)},
	}
	tag, err := manifestTag(document, secret)
	if err != nil {
		return err
	}
	document.Authentication.Tag = base64.StdEncoding.EncodeToString(tag)
	canonical, err := CanonicalJSON(document)
	if err != nil {
		return err
	}
	return writeAtomically(in.Output, func(out io.Writer) error {
		_, err := out.Write(append(canonical, '\n'))
		return err
	})
}

// VerifyInput is manifest-verify's input; empty expectations are skipped.
type VerifyInput struct {
	Input, Passphrase, ArchiveFile, KeyBundleFile                           string
	ExpectReleaseName, ExpectReleaseNamespace, ExpectSourceIdentity, Prefix string
	PrintField                                                              string
}

func loadManifest(path string) (Manifest, error) {
	if err := requireRegularFile(path, "backup manifest"); err != nil {
		return Manifest{}, err
	}
	raw, err := os.ReadFile(path) // #nosec G304 -- operator-selected manifest
	if err != nil {
		return Manifest{}, err
	}
	var document Manifest
	if err := decodeClosed(raw, &document); err != nil {
		return Manifest{}, failf("backup manifest has an open, incomplete or invalid schema")
	}
	if document.SchemaVersion != ManifestSchemaVersion {
		return Manifest{}, failf("unsupported backup manifest schema")
	}
	if document.CreatedAt == "" {
		return Manifest{}, failf("backup manifest created_at is invalid")
	}
	source := document.Source
	if source.DatabaseSchemaVersion < 1 {
		return Manifest{}, failf("backup manifest database schema version is invalid")
	}
	if source.ChartVersion == "" || source.Identity == "" || source.ReleaseName == "" || source.ReleaseNamespace == "" {
		return Manifest{}, failf("backup manifest source identity is incomplete")
	}
	for _, check := range []struct {
		field   string
		payload Payload
		purpose string
	}{{"archive", document.Archive, "database-dump"}, {"key_bundle", document.KeyBundle, "key-bundle"}} {
		if check.payload.Envelope != (EnvelopeRecord{Algorithm: Algorithm, Purpose: check.purpose, SchemaVersion: EnvelopeSchemaVersion}) {
			return Manifest{}, failf("backup manifest %s envelope is unsupported", check.field)
		}
		if err := validateObjectKey(check.payload.Key, check.field+".key"); err != nil {
			return Manifest{}, err
		}
		if !sha256Pattern.MatchString(check.payload.SHA256) {
			return Manifest{}, failf("backup manifest %s digest is invalid", check.field)
		}
		if check.payload.SizeBytes <= 0 {
			return Manifest{}, failf("backup manifest %s size is invalid", check.field)
		}
	}
	if document.Authentication.Algorithm != "HMAC-SHA256" || document.Authentication.KeyDerivation != "HKDF-SHA256" {
		return Manifest{}, failf("unsupported backup manifest authentication")
	}
	return document, nil
}

// VerifyManifest authenticates the manifest and checks every expectation.
// It returns the requested scalar field (empty when none was requested).
func VerifyManifest(in VerifyInput) (string, error) {
	document, err := loadManifest(in.Input)
	if err != nil {
		return "", err
	}
	tag, err := unb64(document.Authentication.Tag, "authentication.tag", 32)
	if err != nil {
		return "", err
	}
	secret, err := ReadSecret(in.Passphrase)
	if err != nil {
		return "", err
	}
	want, err := manifestTag(document, secret)
	if err != nil {
		return "", err
	}
	if !hmac.Equal(tag, want) {
		return "", failf("backup manifest authentication failed")
	}
	if err := checkExpectations(document, in); err != nil {
		return "", err
	}
	if in.PrintField == "" {
		return "", nil
	}
	return printField(document, in.PrintField)
}

// checkExpectations compares the authenticated document with the restore's
// expected identity, object prefix and downloaded payloads.
func checkExpectations(document Manifest, in VerifyInput) error {
	for _, check := range []struct{ field, got, want string }{
		{"release_name", document.Source.ReleaseName, in.ExpectReleaseName},
		{"release_namespace", document.Source.ReleaseNamespace, in.ExpectReleaseNamespace},
		{"identity", document.Source.Identity, in.ExpectSourceIdentity},
	} {
		if check.want != "" && check.got != check.want {
			return failf("backup manifest source.%s does not match this restore", check.field)
		}
	}
	if in.Prefix != "" {
		prefix := strings.TrimRight(in.Prefix, "/") + "/"
		if !strings.HasPrefix(document.Archive.Key, prefix) || !strings.HasPrefix(document.KeyBundle.Key, prefix) {
			return failf("backup manifest object key is outside the expected prefix")
		}
	}
	for _, check := range []struct {
		field, path string
		payload     Payload
	}{{"archive", in.ArchiveFile, document.Archive}, {"key_bundle", in.KeyBundleFile, document.KeyBundle}} {
		if check.path == "" {
			continue
		}
		digest, size, err := sha256File(check.path)
		if err != nil {
			return err
		}
		if size != check.payload.SizeBytes || digest != check.payload.SHA256 {
			return failf("backup manifest %s payload digest/size mismatch", check.field)
		}
	}
	return nil
}

func printField(document Manifest, field string) (string, error) {
	canonical, err := CanonicalJSON(document)
	if err != nil {
		return "", err
	}
	var value any
	if err := decodeNumbers(canonical, &value); err != nil {
		return "", err
	}
	for part := range strings.SplitSeq(field, ".") {
		object, ok := value.(map[string]any)
		if !ok {
			return "", failf("requested manifest field does not exist")
		}
		if value, ok = object[part]; !ok {
			return "", failf("requested manifest field does not exist")
		}
	}
	switch typed := value.(type) {
	case string:
		return typed, nil
	case fmt.Stringer:
		return typed.String(), nil
	default:
		return "", failf("requested manifest field is not scalar")
	}
}
