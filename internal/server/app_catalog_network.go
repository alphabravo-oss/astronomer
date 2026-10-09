package server

import (
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

func configureCatalogHandlerNetwork(handler *handler.CatalogHandler, cfg *config.Config) error {
	policy, err := httpclient.ParseDestinationPolicy(cfg.DistributionDestinationPolicy)
	if err != nil {
		return err
	}
	trust, err := httpclient.TLSForCA(cfg.CatalogCAFile)
	if err != nil {
		return err
	}
	return handler.SetCatalogNetwork(policy, trust, cfg.CatalogProxyURL)
}
