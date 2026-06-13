package tasks

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hibiken/asynq"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

const PlaintextCredentialMigrationType = "security:migrate_plaintext_credentials"

type PlaintextCredentialMigrationQuerier interface {
	ListBackupStorageConfigs(ctx context.Context, arg sqlc.ListBackupStorageConfigsParams) ([]sqlc.BackupStorageConfig, error)
	UpdateBackupStorageConfig(ctx context.Context, arg sqlc.UpdateBackupStorageConfigParams) (sqlc.BackupStorageConfig, error)
	ListAllClusterRegistryConfigs(ctx context.Context) ([]sqlc.ClusterRegistryConfig, error)
	UpdateClusterRegistryConfig(ctx context.Context, arg sqlc.UpdateClusterRegistryConfigParams) (sqlc.ClusterRegistryConfig, error)
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
	return nil
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
