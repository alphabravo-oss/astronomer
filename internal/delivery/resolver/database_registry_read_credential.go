package resolver

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
)

const maxDatabaseReadCredentialBytes = 32 << 10

type databaseRegistryReadCredential struct {
	username, password string
	material           [sha256.Size]byte
}

// Private fields can still be revealed by formatting a copied value. Cover every
// verb for both values and pointers without serializing the credential.
func (databaseRegistryReadCredential) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "<database registry read credential>")
}

func readDatabaseRegistryCredential(ctx context.Context, mounted *ExecVerifier, reference string) (*databaseRegistryReadCredential, error) {
	if ctx == nil || mounted == nil {
		return nil, errors.New("database registry read credential unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path := filepath.Join(mounted.trustDir, reference+".json")
	before, err := os.Lstat(path)
	if err != nil || !privateDatabaseReadCredentialFile(before) {
		return nil, errors.New("database registry read credential unavailable")
	}
	// Reuse the canonical bounded nonblocking/no-follow regular-FD reader and
	// stable inode/size/mtime checks. Its general allocation cap is 1 MiB; this
	// credential's tighter 32 KiB preflight and post-read bound remain mandatory.
	material, _, err := mounted.readTrustKeyWithHook(ctx, reference, ".json", nil)
	if err != nil {
		return nil, databaseAuthenticationError(ctx, err)
	}
	defer clearBytes(material)
	after, err := os.Lstat(path)
	if err != nil || !sameTrustFile(before, after) || before.Mode() != after.Mode() || !privateDatabaseReadCredentialFile(after) || len(material) > maxDatabaseReadCredentialBytes {
		return nil, errors.New("database registry read credential changed")
	}
	var document struct {
		Schema   int    `json:"schema"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if jsoncontract.Decode(material, &document) != nil || document.Schema != 1 || document.Username == "" || document.Password == "" || strings.Contains(document.Username, ":") || len(document.Username) > 4096 || len(document.Password) > 16384 || strings.ContainsAny(document.Username+document.Password, "\r\n\x00") {
		return nil, errors.New("database registry read credential invalid")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &databaseRegistryReadCredential{username: document.Username, password: document.Password, material: sha256.Sum256(material)}, nil
}

func privateDatabaseReadCredentialFile(info os.FileInfo) bool {
	if info == nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxDatabaseReadCredentialBytes {
		return false
	}
	switch info.Mode().Perm() {
	case 0400, 0440, 0600, 0640:
		return info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) == 0
	default:
		return false
	}
}
