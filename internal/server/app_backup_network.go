package server

import (
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

func configureBackupNetwork(handler *handler.BackupHandler, cfg *config.Config) error {
	policy, err := httpclient.ParseDestinationPolicy(cfg.BackupS3DestinationPolicy)
	if err != nil {
		return err
	}
	trust, err := httpclient.TLSForCA(cfg.BackupS3CAFile)
	if err != nil {
		return err
	}
	return handler.SetS3Network(policy, trust)
}
