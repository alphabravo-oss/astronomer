package tasks

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/catalog"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	imonitoring "github.com/alphabravocompany/astronomer-go/internal/monitoring"
)

type fakePlaintextCredentialMigrationQuerier struct {
	backups         []sqlc.BackupStorageConfig
	registries      []sqlc.ClusterRegistryConfig
	repos           []sqlc.HelmRepository
	backupUpdates   []sqlc.UpdateBackupStorageConfigParams
	registryUpdates []sqlc.UpdateClusterRegistryConfigParams
	repoSeals       []sqlc.SealHelmRepositoryAuthConfigParams
	backends        []sqlc.MonitoringBackend
	backendSeals    []sqlc.SealMonitoringBackendAuthConfigParams

	// looseRepoListPredicate reproduces the PRE-FIX SQL, which selected on
	// `auth_config <> '{}'` and so also returned rows with no secret to seal.
	// Those rows can never leave the result set, which is what let the sweep
	// starve. Kept as an option so a test can prove the Go loop walks past
	// unsealable rows on its own, rather than relying on the SQL predicate
	// being the only thing standing between us and that bug.
	looseRepoListPredicate bool
}

func (f *fakePlaintextCredentialMigrationQuerier) ListHelmRepositoriesWithLegacyAuthConfig(_ context.Context, arg sqlc.ListHelmRepositoriesWithLegacyAuthConfigParams) ([]sqlc.HelmRepository, error) {
	// Mirrors the SQL predicate in internal/db/queries/catalog.sql: an empty
	// envelope AND at least one non-empty string under a secret key, so a
	// sealed row stops matching and a row with nothing to seal never matches
	// in the first place. Offset is honoured because the production loop uses
	// it to walk past rows it could not seal.
	out := make([]sqlc.HelmRepository, 0, len(f.repos))
	for _, r := range f.repos {
		if r.AuthConfigEncrypted != "" || len(r.AuthConfig) == 0 {
			continue
		}
		if f.looseRepoListPredicate {
			if string(r.AuthConfig) != "{}" {
				out = append(out, r)
			}
			continue
		}
		var doc map[string]any
		if err := json.Unmarshal(r.AuthConfig, &doc); err != nil {
			continue
		}
		hasSecret := false
		for _, k := range catalog.AuthConfigSecretKeys {
			if s, ok := doc[k].(string); ok && s != "" {
				hasSecret = true
				break
			}
		}
		if hasSecret {
			out = append(out, r)
		}
	}
	if int(arg.Offset) >= len(out) {
		return nil, nil
	}
	out = out[arg.Offset:]
	if int(arg.Limit) < len(out) {
		out = out[:arg.Limit]
	}
	return out, nil
}

func (f *fakePlaintextCredentialMigrationQuerier) SealHelmRepositoryAuthConfig(_ context.Context, arg sqlc.SealHelmRepositoryAuthConfigParams) error {
	f.repoSeals = append(f.repoSeals, arg)
	for i := range f.repos {
		if f.repos[i].ID == arg.ID && f.repos[i].AuthConfigEncrypted == "" {
			f.repos[i].AuthConfigEncrypted = arg.AuthConfigEncrypted
			f.repos[i].AuthConfig = arg.AuthConfig
		}
	}
	return nil
}

// Mirrors the SQL predicate in internal/db/queries/monitoring.sql: an empty
// envelope AND at least one key OUTSIDE the non-secret allow-list, so a sealed
// row stops matching and a backend whose auth_config is nothing but
// operationPolicies never matches at all.
func (f *fakePlaintextCredentialMigrationQuerier) ListMonitoringBackendsWithLegacyAuthConfig(_ context.Context, arg sqlc.ListMonitoringBackendsWithLegacyAuthConfigParams) ([]sqlc.MonitoringBackend, error) {
	out := make([]sqlc.MonitoringBackend, 0, len(f.backends))
	for _, b := range f.backends {
		if b.AuthConfigEncrypted != "" || len(b.AuthConfig) == 0 {
			continue
		}
		var doc map[string]any
		if err := json.Unmarshal(b.AuthConfig, &doc); err != nil {
			continue
		}
		if imonitoring.HasAuthConfigSecret(doc) {
			out = append(out, b)
		}
	}
	if int(arg.Offset) >= len(out) {
		return nil, nil
	}
	out = out[arg.Offset:]
	if int(arg.Limit) < len(out) {
		out = out[:arg.Limit]
	}
	return out, nil
}

func (f *fakePlaintextCredentialMigrationQuerier) SealMonitoringBackendAuthConfig(_ context.Context, arg sqlc.SealMonitoringBackendAuthConfigParams) error {
	f.backendSeals = append(f.backendSeals, arg)
	for i := range f.backends {
		if f.backends[i].ID == arg.ID && f.backends[i].AuthConfigEncrypted == "" {
			f.backends[i].AuthConfigEncrypted = arg.AuthConfigEncrypted
			f.backends[i].AuthConfig = arg.AuthConfig
		}
	}
	return nil
}

func (f *fakePlaintextCredentialMigrationQuerier) ListBackupStorageConfigs(_ context.Context, arg sqlc.ListBackupStorageConfigsParams) ([]sqlc.BackupStorageConfig, error) {
	start := int(arg.Offset)
	if start >= len(f.backups) {
		return nil, nil
	}
	end := start + int(arg.Limit)
	if end > len(f.backups) {
		end = len(f.backups)
	}
	return f.backups[start:end], nil
}

func (f *fakePlaintextCredentialMigrationQuerier) UpdateBackupStorageConfig(_ context.Context, arg sqlc.UpdateBackupStorageConfigParams) (sqlc.BackupStorageConfig, error) {
	f.backupUpdates = append(f.backupUpdates, arg)
	return sqlc.BackupStorageConfig{ID: arg.ID, EncryptedCredentials: arg.EncryptedCredentials}, nil
}

func (f *fakePlaintextCredentialMigrationQuerier) ListAllClusterRegistryConfigs(context.Context) ([]sqlc.ClusterRegistryConfig, error) {
	return f.registries, nil
}

func (f *fakePlaintextCredentialMigrationQuerier) UpdateClusterRegistryConfig(_ context.Context, arg sqlc.UpdateClusterRegistryConfigParams) (sqlc.ClusterRegistryConfig, error) {
	f.registryUpdates = append(f.registryUpdates, arg)
	return sqlc.ClusterRegistryConfig{ID: arg.ID, RegistryPasswordEncrypted: arg.RegistryPasswordEncrypted}, nil
}

func TestPlaintextCredentialMigrationEncryptsAndBlanksLegacyRows(t *testing.T) {
	ResetPlaintextCredentialMigration()
	key, err := auth.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	enc, err := auth.NewEncryptor(key)
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	q := &fakePlaintextCredentialMigrationQuerier{
		backups: []sqlc.BackupStorageConfig{{
			ID:        uuid.New(),
			Name:      "s3",
			AccessKey: "AKIA",
			SecretKey: "SECRET",
		}},
		registries: []sqlc.ClusterRegistryConfig{{
			ID:               uuid.New(),
			RegistryPassword: "s3cr3t",
		}},
	}

	if err := HandlePlaintextCredentialMigration(context.Background(), nil); err != nil {
		t.Fatalf("unexpected unconfigured error: %v", err)
	}
	ConfigurePlaintextCredentialMigration(PlaintextCredentialMigrationDeps{Queries: q, Encryptor: enc})
	t.Cleanup(ResetPlaintextCredentialMigration)

	if err := HandlePlaintextCredentialMigration(context.Background(), nil); err != nil {
		t.Fatalf("HandlePlaintextCredentialMigration: %v", err)
	}
	if len(q.backupUpdates) != 1 {
		t.Fatalf("expected one backup update, got %d", len(q.backupUpdates))
	}
	if q.backupUpdates[0].AccessKey != "" || q.backupUpdates[0].SecretKey != "" {
		t.Fatalf("expected backup plaintext columns blanked, got %q/%q", q.backupUpdates[0].AccessKey, q.backupUpdates[0].SecretKey)
	}
	plainBackup, err := enc.Decrypt(q.backupUpdates[0].EncryptedCredentials)
	if err != nil {
		t.Fatalf("Decrypt backup credentials: %v", err)
	}
	var backupCreds map[string]string
	if err := json.Unmarshal([]byte(plainBackup), &backupCreds); err != nil {
		t.Fatalf("decode backup credentials: %v", err)
	}
	if backupCreds["access_key"] != "AKIA" || backupCreds["secret_key"] != "SECRET" {
		t.Fatalf("unexpected backup credentials: %#v", backupCreds)
	}
	if len(q.registryUpdates) != 1 {
		t.Fatalf("expected one registry update, got %d", len(q.registryUpdates))
	}
	if q.registryUpdates[0].RegistryPassword != "" {
		t.Fatalf("expected registry plaintext column blanked, got %q", q.registryUpdates[0].RegistryPassword)
	}
	plainRegistry, err := enc.Decrypt(q.registryUpdates[0].RegistryPasswordEncrypted)
	if err != nil {
		t.Fatalf("Decrypt registry password: %v", err)
	}
	if plainRegistry != "s3cr3t" {
		t.Fatalf("expected registry password s3cr3t, got %q", plainRegistry)
	}
}

func TestPlaintextCredentialMigrationBlanksRowsWithExistingCiphertext(t *testing.T) {
	key, err := auth.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	enc, err := auth.NewEncryptor(key)
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	backupCiphertext, err := enc.Encrypt(`{"access_key":"AKIA","secret_key":"SECRET"}`)
	if err != nil {
		t.Fatalf("Encrypt backup: %v", err)
	}
	registryCiphertext, err := enc.Encrypt("s3cr3t")
	if err != nil {
		t.Fatalf("Encrypt registry: %v", err)
	}
	q := &fakePlaintextCredentialMigrationQuerier{
		backups: []sqlc.BackupStorageConfig{{
			ID:                   uuid.New(),
			AccessKey:            "AKIA",
			SecretKey:            "SECRET",
			EncryptedCredentials: backupCiphertext,
		}},
		registries: []sqlc.ClusterRegistryConfig{{
			ID:                        uuid.New(),
			RegistryPassword:          "s3cr3t",
			RegistryPasswordEncrypted: registryCiphertext,
		}},
	}
	ConfigurePlaintextCredentialMigration(PlaintextCredentialMigrationDeps{Queries: q, Encryptor: enc})
	t.Cleanup(ResetPlaintextCredentialMigration)

	if err := HandlePlaintextCredentialMigration(context.Background(), nil); err != nil {
		t.Fatalf("HandlePlaintextCredentialMigration: %v", err)
	}
	if q.backupUpdates[0].EncryptedCredentials != backupCiphertext {
		t.Fatal("expected existing backup ciphertext to be preserved")
	}
	if q.registryUpdates[0].RegistryPasswordEncrypted != registryCiphertext {
		t.Fatal("expected existing registry ciphertext to be preserved")
	}
}

// Migration 146 adds a FOURTH case to this sweep: the monitoring backend.
//
// The migration itself can only add the column (SQL has no Fernet key), so an
// upgraded install arrives with the Thanos credential still in the clear in
// auth_config. Without this converter the fix would only ever apply to
// installs where somebody happened to re-save the backend through the API.
func TestPlaintextCredentialMigrationSealsMonitoringBackendAuthConfig(t *testing.T) {
	ResetPlaintextCredentialMigration()
	key, err := auth.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	enc, err := auth.NewEncryptor(key)
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	credentialed := uuid.New()
	q := &fakePlaintextCredentialMigrationQuerier{
		backends: []sqlc.MonitoringBackend{
			{
				ID:         credentialed,
				Name:       "default",
				AuthType:   "bearer",
				AuthConfig: json.RawMessage(`{"token":"s3cr3t-thanos-token","operationPolicies":{"maxRetryAttempts":3}}`),
			},
			{
				// Nothing outside the non-secret allow-list. Sealing this
				// would put the retry policy behind a decrypt for no gain, and
				// a row the sweep cannot seal must be walked PAST rather than
				// re-read forever.
				ID:         uuid.New(),
				Name:       "config-only",
				AuthConfig: json.RawMessage(`{"operationPolicies":{"maxRetryAttempts":1}}`),
			},
		},
	}
	ConfigurePlaintextCredentialMigration(PlaintextCredentialMigrationDeps{Queries: q, Encryptor: enc})
	t.Cleanup(ResetPlaintextCredentialMigration)

	if err := HandlePlaintextCredentialMigration(context.Background(), nil); err != nil {
		t.Fatalf("HandlePlaintextCredentialMigration: %v", err)
	}
	if len(q.backendSeals) != 1 {
		t.Fatalf("expected exactly one monitoring backend sealed, got %d", len(q.backendSeals))
	}
	seal := q.backendSeals[0]
	if seal.ID != credentialed {
		t.Fatalf("sealed the wrong backend: %s", seal.ID)
	}
	if strings.Contains(string(seal.AuthConfig), "s3cr3t-thanos-token") {
		t.Fatalf("the sweep left the credential in the clear: %s", seal.AuthConfig)
	}
	plain, err := enc.Decrypt(seal.AuthConfigEncrypted)
	if err != nil {
		t.Fatalf("Decrypt sealed auth_config: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(plain), &doc); err != nil {
		t.Fatalf("decode sealed auth_config: %v", err)
	}
	if doc["token"] != "s3cr3t-thanos-token" {
		t.Fatalf("the envelope does not hold the credential: %v", doc)
	}
	// The config bag stays queryable in the JSONB.
	if !strings.Contains(string(seal.AuthConfig), "maxRetryAttempts") {
		t.Fatalf("the sweep sealed the non-secret config bag away: %s", seal.AuthConfig)
	}

	// Idempotent: a second pass finds the sealed row no longer matching.
	before := len(q.backendSeals)
	if err := HandlePlaintextCredentialMigration(context.Background(), nil); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if len(q.backendSeals) != before {
		t.Fatalf("second pass re-sealed an already-sealed row: %d -> %d", before, len(q.backendSeals))
	}
}
