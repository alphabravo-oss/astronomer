package server

import (
	"fmt"

	"k8s.io/client-go/kubernetes"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/db"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
)

type managementBackupReadiness interface {
	ManagementBackupReady() bool
}

func validateManagementBackupStartup(enabled bool, executor managementBackupReadiness) error {
	if enabled && (executor == nil || !executor.ManagementBackupReady()) {
		return fmt.Errorf("management backup is enabled but its executor is not ready")
	}
	return nil
}

func newManagementBackupHandler(cfg *config.Config, queries handler.AdminDrillQuerier, database *db.DB, encryptor *auth.Encryptor, localK8s kubernetes.Interface, namespace string) *handler.AdminDrillHandler {
	h := handler.NewAdminDrillHandler(queries)
	h.SetManagementBackupEnabled(cfg.ManagementBackupEnabled)
	h.SetRunTx(sqlcMutationTxRunner[handler.ManagementBackupMutationTx](database))
	h.SetEncryptor(encryptor)
	h.SetBackupRuntime(cfg.ManagementBackupImage, cfg.ManagementBackupServiceAccount)
	if localK8s != nil && namespace != "" {
		h.SetKubernetes(localK8s, namespace, cfg.ReleaseName)
	}
	return h
}
