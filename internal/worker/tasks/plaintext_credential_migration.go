package tasks

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hibiken/asynq"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/catalog"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	imonitoring "github.com/alphabravocompany/astronomer-go/internal/monitoring"
)

const PlaintextCredentialMigrationType = "security:migrate_plaintext_credentials"

type PlaintextCredentialMigrationQuerier interface {
	ListBackupStorageConfigs(ctx context.Context, arg sqlc.ListBackupStorageConfigsParams) ([]sqlc.BackupStorageConfig, error)
	UpdateBackupStorageConfig(ctx context.Context, arg sqlc.UpdateBackupStorageConfigParams) (sqlc.BackupStorageConfig, error)
	ListAllClusterRegistryConfigs(ctx context.Context) ([]sqlc.ClusterRegistryConfig, error)
	UpdateClusterRegistryConfig(ctx context.Context, arg sqlc.UpdateClusterRegistryConfigParams) (sqlc.ClusterRegistryConfig, error)
	// Migration 145 — chart-repository credentials.
	ListHelmRepositoriesWithLegacyAuthConfig(ctx context.Context, arg sqlc.ListHelmRepositoriesWithLegacyAuthConfigParams) ([]sqlc.HelmRepository, error)
	SealHelmRepositoryAuthConfig(ctx context.Context, arg sqlc.SealHelmRepositoryAuthConfigParams) error
	// Migration 146 — monitoring-backend credentials.
	ListMonitoringBackendsWithLegacyAuthConfig(ctx context.Context, arg sqlc.ListMonitoringBackendsWithLegacyAuthConfigParams) ([]sqlc.MonitoringBackend, error)
	SealMonitoringBackendAuthConfig(ctx context.Context, arg sqlc.SealMonitoringBackendAuthConfigParams) error
}

type PlaintextCredentialMigrationDeps struct {
	Queries   PlaintextCredentialMigrationQuerier
	Encryptor *auth.Encryptor
}

var plaintextCredentialMigrationDeps PlaintextCredentialMigrationDeps

func ConfigurePlaintextCredentialMigration(deps PlaintextCredentialMigrationDeps) {
	plaintextCredentialMigrationDeps = deps
}

func ResetPlaintextCredentialMigration() {
	plaintextCredentialMigrationDeps = PlaintextCredentialMigrationDeps{}
}

func NewPlaintextCredentialMigrationTask() (*asynq.Task, error) {
	return asynq.NewTask(PlaintextCredentialMigrationType, nil), nil
}

func HandlePlaintextCredentialMigration(ctx context.Context, _ *asynq.Task) error {
	deps := plaintextCredentialMigrationDeps
	if deps.Queries == nil || deps.Encryptor == nil {
		runtimeLogger().InfoContext(ctx, "plaintext credential migration not configured, skipping")
		return nil
	}
	if err := migrateBackupStorageCredentials(ctx, deps); err != nil {
		return err
	}
	if err := migrateClusterRegistryCredentials(ctx, deps); err != nil {
		return err
	}
	if err := migrateHelmRepositoryAuthConfigs(ctx, deps); err != nil {
		return err
	}
	if err := migrateMonitoringBackendAuthConfigs(ctx, deps); err != nil {
		return err
	}
	return nil
}

// migrateMonitoringBackendAuthConfigs seals pre-146 monitoring-backend
// credentials.
//
// This is what ends the upgrade window opened by migration 146, on the same
// terms as the chart-repository converter below it: the migration can only add
// the column, so every existing row arrives with an empty
// auth_config_encrypted and its Thanos/Prometheus bearer token or password
// still in the clear in the JSONB.
//
// The paging walk mirrors migrateHelmRepositoryAuthConfigs and for the same
// reason — sealing removes a row from the query's predicate, so an unsealable
// row that the SQL predicate nevertheless returns would otherwise be re-read
// forever. In practice this table holds one row ('default'), which makes the
// runaway harmless but also makes it invisible in testing; the walk is written
// to be correct at any row count rather than correct at one.
func migrateMonitoringBackendAuthConfigs(ctx context.Context, deps PlaintextCredentialMigrationDeps) error {
	const pageSize int32 = 500
	skipped := int32(0)
	for {
		rows, err := deps.Queries.ListMonitoringBackendsWithLegacyAuthConfig(ctx, sqlc.ListMonitoringBackendsWithLegacyAuthConfigParams{
			Limit:  pageSize,
			Offset: skipped,
		})
		if err != nil {
			return fmt.Errorf("list monitoring backends with plaintext auth_config: %w", err)
		}
		for _, row := range rows {
			ciphertext, public, err := imonitoring.SealAuthConfig(row.AuthConfig, deps.Encryptor)
			if err != nil {
				return fmt.Errorf("encrypt monitoring backend auth_config %s: %w", row.ID, err)
			}
			if ciphertext == "" {
				// Nothing outside the non-secret allow-list, so there is
				// nothing to protect and sealing would only move the config
				// bag behind a decrypt. Walk past it.
				runtimeLogger().WarnContext(ctx, "monitoring backend matched the plaintext-credential sweep but carries no sealable secret",
					"backend_id", row.ID)
				skipped++
				continue
			}
			if err := deps.Queries.SealMonitoringBackendAuthConfig(ctx, sqlc.SealMonitoringBackendAuthConfigParams{
				ID:                  row.ID,
				AuthConfigEncrypted: ciphertext,
				AuthConfig:          public,
			}); err != nil {
				return fmt.Errorf("seal monitoring backend auth_config %s: %w", row.ID, err)
			}
		}
		if int32(len(rows)) < pageSize {
			return nil
		}
	}
}

// migrateHelmRepositoryAuthConfigs seals pre-145 chart-repository credentials.
//
// This is what ends the upgrade window opened by migration 145: the migration
// can only add the column (SQL has no access to the Fernet key), so every
// existing row arrives with an empty auth_config_encrypted and its password still
// in the clear in the JSONB. Readers tolerate that shape, which is what keeps
// an upgrade from breaking every authenticated repository — but tolerating it
// forever would mean the security fix only ever applied to repositories
// somebody happened to re-save. This runs @every 6h in both the server and the
// dedicated worker, so the window closes on its own.
func migrateHelmRepositoryAuthConfigs(ctx context.Context, deps PlaintextCredentialMigrationDeps) error {
	const pageSize int32 = 500
	// Rows this sweep has looked at and could NOT seal. Sealing removes a row
	// from the query's predicate, so the unsealed remainder always begins with
	// exactly these — offsetting by their count lands on the first row not yet
	// visited, which is the same walk the two converters below perform.
	//
	// The query only returns rows that carry a non-empty secret, so this
	// should stay 0; it exists so that a disagreement between the SQL
	// predicate and catalog.SealAuthConfig degrades into skipping a row rather
	// than re-reading the same page forever. Encryption that silently stops
	// half-done is the failure this whole task exists to prevent.
	skipped := int32(0)
	for {
		rows, err := deps.Queries.ListHelmRepositoriesWithLegacyAuthConfig(ctx, sqlc.ListHelmRepositoriesWithLegacyAuthConfigParams{
			Limit:  pageSize,
			Offset: skipped,
		})
		if err != nil {
			return fmt.Errorf("list chart repositories with plaintext auth_config: %w", err)
		}
		for _, row := range rows {
			ciphertext, public, err := catalog.SealAuthConfig(row.AuthConfig, deps.Encryptor)
			if err != nil {
				return fmt.Errorf("encrypt chart repository auth_config %s: %w", row.ID, err)
			}
			if ciphertext == "" {
				// Nothing to protect (a `charts` list, a bare username), and
				// writing an envelope anyway would move the chart list out of
				// the catalog API's reach. Walk past it.
				runtimeLogger().WarnContext(ctx, "chart repository matched the plaintext-credential sweep but carries no sealable secret",
					"repository_id", row.ID)
				skipped++
				continue
			}
			if err := deps.Queries.SealHelmRepositoryAuthConfig(ctx, sqlc.SealHelmRepositoryAuthConfigParams{
				ID:                  row.ID,
				AuthConfigEncrypted: ciphertext,
				AuthConfig:          public,
			}); err != nil {
				return fmt.Errorf("seal chart repository auth_config %s: %w", row.ID, err)
			}
		}
		if int32(len(rows)) < pageSize {
			return nil
		}
	}
}

func migrateBackupStorageCredentials(ctx context.Context, deps PlaintextCredentialMigrationDeps) error {
	const pageSize int32 = 500
	for offset := int32(0); ; offset += pageSize {
		rows, err := deps.Queries.ListBackupStorageConfigs(ctx, sqlc.ListBackupStorageConfigsParams{
			Limit:  pageSize,
			Offset: offset,
		})
		if err != nil {
			return fmt.Errorf("list backup storage configs: %w", err)
		}
		for _, row := range rows {
			needsEncrypt := row.EncryptedCredentials == "" && (row.AccessKey != "" || row.SecretKey != "")
			needsBlank := row.EncryptedCredentials != "" && (row.AccessKey != "" || row.SecretKey != "")
			if !needsEncrypt && !needsBlank {
				continue
			}
			encrypted := row.EncryptedCredentials
			if needsEncrypt {
				payload, err := json.Marshal(map[string]string{
					"access_key": row.AccessKey,
					"secret_key": row.SecretKey,
				})
				if err != nil {
					return fmt.Errorf("marshal backup credentials %s: %w", row.ID, err)
				}
				encrypted, err = deps.Encryptor.Encrypt(string(payload))
				if err != nil {
					return fmt.Errorf("encrypt backup credentials %s: %w", row.ID, err)
				}
			}
			if _, err := deps.Queries.UpdateBackupStorageConfig(ctx, sqlc.UpdateBackupStorageConfigParams{
				ID:                   row.ID,
				Name:                 row.Name,
				StorageType:          row.StorageType,
				Bucket:               row.Bucket,
				Prefix:               row.Prefix,
				Region:               row.Region,
				EndpointUrl:          row.EndpointUrl,
				AccessKey:            "",
				SecretKey:            "",
				IsDefault:            row.IsDefault,
				ClusterID:            row.ClusterID,
				VeleroNamespace:      row.VeleroNamespace,
				BslName:              row.BslName,
				EncryptedCredentials: encrypted,
			}); err != nil {
				return fmt.Errorf("update backup storage config %s: %w", row.ID, err)
			}
		}
		if len(rows) < int(pageSize) {
			return nil
		}
	}
}

func migrateClusterRegistryCredentials(ctx context.Context, deps PlaintextCredentialMigrationDeps) error {
	rows, err := deps.Queries.ListAllClusterRegistryConfigs(ctx)
	if err != nil {
		return fmt.Errorf("list cluster registry configs: %w", err)
	}
	for _, row := range rows {
		needsEncrypt := row.RegistryPasswordEncrypted == "" && row.RegistryPassword != ""
		needsBlank := row.RegistryPasswordEncrypted != "" && row.RegistryPassword != ""
		if !needsEncrypt && !needsBlank {
			continue
		}
		encrypted := row.RegistryPasswordEncrypted
		if needsEncrypt {
			var err error
			encrypted, err = deps.Encryptor.Encrypt(row.RegistryPassword)
			if err != nil {
				return fmt.Errorf("encrypt cluster registry password %s: %w", row.ID, err)
			}
		}
		if _, err := deps.Queries.UpdateClusterRegistryConfig(ctx, sqlc.UpdateClusterRegistryConfigParams{
			ID:                        row.ID,
			PrivateRegistryUrl:        row.PrivateRegistryUrl,
			RegistryUsername:          row.RegistryUsername,
			RegistryPassword:          "",
			RegistryPasswordEncrypted: encrypted,
			Insecure:                  row.Insecure,
			CaBundle:                  row.CaBundle,
			Namespaces:                row.Namespaces,
			InjectDefaultSa:           row.InjectDefaultSa,
			SecretName:                row.SecretName,
		}); err != nil {
			return fmt.Errorf("update cluster registry config %s: %w", row.ID, err)
		}
	}
	return nil
}
