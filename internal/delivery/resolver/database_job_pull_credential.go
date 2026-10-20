package resolver

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
)

// Kubelet pull is an independently explicit mounted role. It is never inferred
// from protected push credentials or the central registry HTTP pull credential.
// This owner can only return the captured Secret bytes; it grants no network
// permission, registry operation or image identity.
type DatabaseJobPullCredential struct {
	mutex      *sync.Mutex
	mounted    *ExecVerifier
	reference  string
	registries []string
	bytes      []byte
	digest     [sha256.Size]byte
	closed     bool
}

func (DatabaseJobPullCredential) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "<private database Job pull credential>")
}
func CaptureDatabaseJobPullCredential(ctx context.Context, directory, reference string, registries []string) (*DatabaseJobPullCredential, error) {
	if ctx == nil || directory == "" || reference == "" || len(validation.IsDNS1123Subdomain(reference)) != 0 || len(registries) == 0 || len(registries) > 2 {
		return nil, errors.New("database Job pull constructor unavailable")
	}
	// Exact immutable image repositories derive these registry names upstream.
	// No URL, wildcard, implicit credential helper or cross-host Docker setting.
	expected := append([]string(nil), registries...)
	sort.Strings(expected)
	for index, registry := range expected {
		if registry == "" || strings.ContainsAny(registry, "/\\\r\n\x00@") || index > 0 && registry == expected[index-1] {
			return nil, errors.New("database Job pull registry scope invalid")
		}
	}
	mounted, err := NewExecVerifier(directory)
	if err != nil {
		return nil, err
	}
	material, err := readDatabaseJobPullCredential(ctx, mounted, reference, expected)
	if err != nil {
		return nil, err
	}
	return &DatabaseJobPullCredential{mutex: &sync.Mutex{}, mounted: mounted, reference: reference, registries: expected, bytes: material, digest: sha256.Sum256(material)}, nil
}
func readDatabaseJobPullCredential(ctx context.Context, mounted *ExecVerifier, reference string, registries []string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path := filepath.Join(mounted.trustDir, reference+".json")
	before, err := os.Lstat(path)
	if err != nil || !privateDatabaseReadCredentialFile(before) {
		return nil, errors.New("database Job pull material unavailable")
	}
	material, _, err := mounted.readTrustKeyWithHook(ctx, reference, ".json", nil)
	if err != nil {
		return nil, databaseAuthenticationError(ctx, err)
	}
	valid := false
	defer func() {
		if !valid {
			clear(material)
		}
	}()
	after, err := os.Lstat(path)
	if err != nil || !sameTrustFile(before, after) || before.Mode() != after.Mode() || !privateDatabaseReadCredentialFile(after) || len(material) > maxDatabaseReadCredentialBytes {
		return nil, errors.New("database Job pull material changed")
	}
	if validateDatabaseJobDockerConfig(material, registries) != nil {
		return nil, errors.New("database Job pull material invalid")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	valid = true
	return material, nil
}
func validateDatabaseJobDockerConfig(payload []byte, registries []string) error {
	var document struct {
		Auths map[string]struct {
			Username string `json:"username"`
			Password string `json:"password"`
		} `json:"auths"`
	}
	if jsoncontract.Decode(payload, &document) != nil || len(document.Auths) != len(registries) {
		return errors.New("database Job pull auths shape invalid")
	}
	for _, registry := range registries {
		credential, found := document.Auths[registry]
		if !found || credential.Username == "" || credential.Password == "" || len(credential.Username) > 4096 || len(credential.Password) > 16384 || strings.ContainsAny(credential.Username, "\r\n\x00:") || strings.ContainsAny(credential.Password, "\r\n\x00") {
			return errors.New("database Job pull auth scope invalid")
		}
	}
	return nil
}
func (credential *DatabaseJobPullCredential) Revalidate(ctx context.Context) error {
	if ctx == nil || credential == nil || credential.mutex == nil {
		return errors.New("database Job pull owner unavailable")
	}
	credential.mutex.Lock()
	defer credential.mutex.Unlock()
	if credential.closed {
		return errors.New("database Job pull owner closed")
	}
	material, err := readDatabaseJobPullCredential(ctx, credential.mounted, credential.reference, credential.registries)
	if err != nil {
		return err
	}
	defer clear(material)
	if sha256.Sum256(material) != credential.digest {
		return errors.New("database Job pull mounted material changed")
	}
	return ctx.Err()
}
func (credential *DatabaseJobPullCredential) Data(ctx context.Context) ([]byte, error) {
	if err := credential.Revalidate(ctx); err != nil {
		return nil, err
	}
	credential.mutex.Lock()
	defer credential.mutex.Unlock()
	if credential.closed {
		return nil, errors.New("database Job pull owner closed")
	}
	return append([]byte(nil), credential.bytes...), nil
}
func (credential *DatabaseJobPullCredential) Close() {
	if credential != nil && credential.mutex != nil {
		credential.mutex.Lock()
		defer credential.mutex.Unlock()
		clear(credential.bytes)
		credential.bytes = nil
		credential.closed = true
	}
}
